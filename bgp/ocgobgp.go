// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bgp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	log "github.com/golang/glog"
	"github.com/openconfig/lemming/gnmi/oc"
	"github.com/openconfig/lemming/internal/lemmingutil"
	gobgpoc "github.com/wenovus/gobgp/v3/pkg/config/oc"
)

// convertPolicyName converts from OC policy name to a neighbour-qualified
// policy name in order to put all the policies into a global list.
func convertPolicyName(neighAddr, ocPolicyName string) string {
	return neighAddr + "|" + ocPolicyName
}

func convertSetCommunities(setCommunity *oc.RoutingPolicy_PolicyDefinition_Statement_Actions_BgpActions_SetCommunity, convertedCommSets []gobgpoc.CommunitySet, commSetIndexMap map[string]int) ([]string, error) {
	switch setCommunity.GetMethod() {
	case oc.SetCommunity_Method_INLINE:
		var setCommunitiesList []string
		for _, comm := range setCommunity.GetInline().GetCommunities() {
			setCommunitiesList = append(setCommunitiesList, convertCommunity(comm))
		}
		return setCommunitiesList, nil
	case oc.SetCommunity_Method_REFERENCE:
		if commRef := setCommunity.GetReference().GetCommunitySetRef(); commRef != "" {
			// YANG validation should ensure that the referred community set is present.
			index, ok := commSetIndexMap[commRef]
			if !ok {
				return nil, fmt.Errorf("Referenced community set not present in index map: %q", commRef)
			}
			return convertedCommSets[index].CommunityList, nil
		}
	}
	return nil, nil
}

// convertPolicyDefinition converts an OC policy definition to GoBGP policy definition.
//
// It adds neighbour set to disambiguate it from another instance of the policy
// for another neighbour. This is necessary since all policies will go into a
// single apply-policy list.
func convertPolicyDefinition(policy *oc.RoutingPolicy_PolicyDefinition, neighAddr string, occommset map[string]*oc.RoutingPolicy_DefinedSets_BgpDefinedSets_CommunitySet, convertedCommSets []gobgpoc.CommunitySet, commSetIndexMap map[string]int) gobgpoc.PolicyDefinition {
	convertedPolicyName := convertPolicyName(neighAddr, policy.GetName())
	var statements []gobgpoc.Statement
	for _, statement := range policy.Statement.Values() {
		setCommunitiesList, err := convertSetCommunities(statement.GetActions().GetBgpActions().GetSetCommunity(), convertedCommSets, commSetIndexMap)
		if err != nil {
			log.Error(err)
		}
		setmed, err := convertMED(statement.GetActions().GetBgpActions().GetSetMed())
		if err != nil {
			log.Errorf("MED value not supported: %v", err)
		}
		statements = append(statements, gobgpoc.Statement{
			// In GoBGP, statements must have globally-unique names.
			// Ensure uniqueness by qualifying each one with the name of the converted policy.
			Name: convertedPolicyName + ":" + statement.GetName(),
			Conditions: gobgpoc.Conditions{
				MatchPrefixSet: gobgpoc.MatchPrefixSet{
					PrefixSet:       statement.GetConditions().GetMatchPrefixSet().GetPrefixSet(),
					MatchSetOptions: convertMatchSetOptionsRestrictedType(statement.GetConditions().GetMatchPrefixSet().GetMatchSetOptions()),
				},
				MatchNeighborSet: gobgpoc.MatchNeighborSet{
					// Name the neighbor set as the policy so that the policy only applies to referring neighbours.
					NeighborSet: neighAddr,
				},
				BgpConditions: gobgpoc.BgpConditions{
					MatchCommunitySet: gobgpoc.MatchCommunitySet{
						CommunitySet:    statement.Conditions.GetBgpConditions().GetCommunitySet(),
						MatchSetOptions: convertMatchSetOptionsType(occommset[statement.GetConditions().GetBgpConditions().GetCommunitySet()].GetMatchSetOptions()),
					},
					MatchAsPathSet: gobgpoc.MatchAsPathSet{
						AsPathSet:       statement.Conditions.GetBgpConditions().GetMatchAsPathSet().GetAsPathSet(),
						MatchSetOptions: convertMatchSetOptionsType(statement.GetConditions().GetBgpConditions().GetMatchAsPathSet().GetMatchSetOptions()),
					},
				},
			},
			Actions: gobgpoc.Actions{
				RouteDisposition: convertRouteDisposition(statement.GetActions().GetPolicyResult()),
				BgpActions: gobgpoc.BgpActions{
					SetCommunity: gobgpoc.SetCommunity{
						SetCommunityMethod: gobgpoc.SetCommunityMethod{
							CommunitiesList: setCommunitiesList,
						},
						Options: strings.ToLower(statement.GetActions().GetBgpActions().GetSetCommunity().GetOptions().String()),
					},
					SetLocalPref: statement.GetActions().GetBgpActions().GetSetLocalPref(),
					SetMed:       gobgpoc.BgpSetMedType(setmed),
					SetAsPathPrepend: gobgpoc.SetAsPathPrepend{
						RepeatN: statement.GetActions().GetBgpActions().GetSetAsPathPrepend().GetRepeatN(),
						As:      strconv.FormatUint(uint64(statement.GetActions().GetBgpActions().GetSetAsPathPrepend().GetAsn()), 10),
					},
				},
			},
		})
	}

	return gobgpoc.PolicyDefinition{
		Name:       convertedPolicyName,
		Statements: statements,
	}
}

func convertNeighborApplyPolicy(neigh *oc.NetworkInstance_Protocol_Bgp_Neighbor) gobgpoc.ApplyPolicy {
	return gobgpoc.ApplyPolicy{
		Config: gobgpoc.ApplyPolicyConfig{
			DefaultImportPolicy: convertDefaultPolicy(neigh.GetApplyPolicy().GetDefaultImportPolicy()),
			DefaultExportPolicy: convertDefaultPolicy(neigh.GetApplyPolicy().GetDefaultExportPolicy()),
			ImportPolicyList:    neigh.GetApplyPolicy().GetImportPolicy(),
			ExportPolicyList:    neigh.GetApplyPolicy().GetExportPolicy(),
		},
	}
}

// TODO(wenbli): Add unit tests for these conversion functions.

func convertDefaultPolicy(ocpolicy oc.E_RoutingPolicy_DefaultPolicyType) gobgpoc.DefaultPolicyType {
	switch ocpolicy {
	case oc.RoutingPolicy_DefaultPolicyType_REJECT_ROUTE:
		return gobgpoc.DEFAULT_POLICY_TYPE_REJECT_ROUTE
	case oc.RoutingPolicy_DefaultPolicyType_ACCEPT_ROUTE:
		return gobgpoc.DEFAULT_POLICY_TYPE_ACCEPT_ROUTE
	default:
		return gobgpoc.DEFAULT_POLICY_TYPE_REJECT_ROUTE
	}
}

func convertMatchSetOptionsType(ocMatchSetOpts oc.E_RoutingPolicy_MatchSetOptionsType) gobgpoc.MatchSetOptionsType {
	switch ocMatchSetOpts {
	case oc.RoutingPolicy_MatchSetOptionsType_INVERT:
		return gobgpoc.MATCH_SET_OPTIONS_TYPE_INVERT
	case oc.RoutingPolicy_MatchSetOptionsType_ANY:
		return gobgpoc.MATCH_SET_OPTIONS_TYPE_ANY
	case oc.RoutingPolicy_MatchSetOptionsType_ALL:
		return gobgpoc.MATCH_SET_OPTIONS_TYPE_ALL
	default:
		return gobgpoc.MATCH_SET_OPTIONS_TYPE_ANY
	}
}

func convertMatchSetOptionsRestrictedType(ocrestrictedMatchSetOpts oc.E_RoutingPolicy_MatchSetOptionsRestrictedType) gobgpoc.MatchSetOptionsRestrictedType {
	switch ocrestrictedMatchSetOpts {
	case oc.RoutingPolicy_MatchSetOptionsRestrictedType_INVERT:
		return gobgpoc.MATCH_SET_OPTIONS_RESTRICTED_TYPE_INVERT
	case oc.RoutingPolicy_MatchSetOptionsRestrictedType_ANY:
		return gobgpoc.MATCH_SET_OPTIONS_RESTRICTED_TYPE_ANY
	default:
		return gobgpoc.MATCH_SET_OPTIONS_RESTRICTED_TYPE_ANY
	}
}

func convertRouteDisposition(ocpolicyresult oc.E_RoutingPolicy_PolicyResultType) gobgpoc.RouteDisposition {
	switch ocpolicyresult {
	case oc.RoutingPolicy_PolicyResultType_REJECT_ROUTE:
		return gobgpoc.ROUTE_DISPOSITION_REJECT_ROUTE
	case oc.RoutingPolicy_PolicyResultType_ACCEPT_ROUTE:
		return gobgpoc.ROUTE_DISPOSITION_ACCEPT_ROUTE
	default:
		return gobgpoc.ROUTE_DISPOSITION_NONE
	}
}

func defaultPolicyToRouteDisp(gobgpdefaultpolicy gobgpoc.DefaultPolicyType) gobgpoc.RouteDisposition {
	switch gobgpdefaultpolicy {
	case gobgpoc.DEFAULT_POLICY_TYPE_REJECT_ROUTE:
		return gobgpoc.ROUTE_DISPOSITION_REJECT_ROUTE
	case gobgpoc.DEFAULT_POLICY_TYPE_ACCEPT_ROUTE:
		return gobgpoc.ROUTE_DISPOSITION_ACCEPT_ROUTE
	default:
		return gobgpoc.ROUTE_DISPOSITION_REJECT_ROUTE
	}
}

// convertCommunity converts any community union type to its string representation to be used in GoBGP.
func convertCommunity(community any) string {
	switch c := community.(type) {
	case oc.UnionString:
		return string(c)
	case oc.UnionUint32:
		uint32ToCommunityString(uint32(c))
	case oc.E_BgpTypes_BGP_WELL_KNOWN_STD_COMMUNITY:
		switch c {
		case oc.BgpTypes_BGP_WELL_KNOWN_STD_COMMUNITY_NO_EXPORT:
			return "65535:65281"
		case oc.BgpTypes_BGP_WELL_KNOWN_STD_COMMUNITY_NO_ADVERTISE:
			return "65535:65282"
		case oc.BgpTypes_BGP_WELL_KNOWN_STD_COMMUNITY_NO_EXPORT_SUBCONFED:
			return "65535:65283"
		case oc.BgpTypes_BGP_WELL_KNOWN_STD_COMMUNITY_NOPEER:
			return "65535:65284"
		}
	}
	return ""
}

func convertCommunitySet(occommset map[string]*oc.RoutingPolicy_DefinedSets_BgpDefinedSets_CommunitySet) ([]gobgpoc.CommunitySet, map[string]int) {
	indexMap := map[string]int{}
	var commsets []gobgpoc.CommunitySet
	commNames := lemmingutil.Mapkeys(occommset)
	slices.Sort(commNames)
	for _, communitySetName := range commNames {
		var communityList []string
		for _, community := range occommset[communitySetName].CommunityMember {
			communityList = append(communityList, convertCommunity(community))
		}

		indexMap[communitySetName] = len(commsets)
		commsets = append(commsets, gobgpoc.CommunitySet{
			CommunitySetName: communitySetName,
			CommunityList:    communityList,
		})
	}
	return commsets, indexMap
}

// convertCommunityOC converts a GoBGP community to its OC representation.
func convertCommunityOC(y uint32) oc.NetworkInstance_Protocol_Bgp_Rib_Community_Community_Union {
	switch y {
	default:
		return oc.UnionString(fmt.Sprintf("%d:%d", y>>16, y&0x0000ffff))
	}
}

// communitiesToOC converts any GoBGP community to its RIB representation in OpenConfig.
func communitiesToOC(communities []uint32) []oc.NetworkInstance_Protocol_Bgp_Rib_Community_Community_Union {
	var occomms []oc.NetworkInstance_Protocol_Bgp_Rib_Community_Community_Union
	for _, comm := range communities {
		occomms = append(occomms, convertCommunityOC(comm))
	}
	return occomms
}

func convertPrefixSets(ocprefixsets map[string]*oc.RoutingPolicy_DefinedSets_PrefixSet) []gobgpoc.PrefixSet {
	var prefixSets []gobgpoc.PrefixSet
	prefixSetNames := lemmingutil.Mapkeys(ocprefixsets)
	slices.Sort(prefixSetNames)
	for _, prefixSetName := range prefixSetNames {
		var prefixList []gobgpoc.Prefix
		for _, prefix := range ocprefixsets[prefixSetName].Prefix {
			r := prefix.GetMasklengthRange()
			if r == "exact" {
				// GoBGP recognizes "" instead of "exact"
				r = ""
			}
			prefixList = append(prefixList, gobgpoc.Prefix{
				IpPrefix:        prefix.GetIpPrefix(),
				MasklengthRange: r,
			})
		}

		prefixSets = append(prefixSets, gobgpoc.PrefixSet{
			PrefixSetName: prefixSetName,
			PrefixList:    prefixList,
		})
	}
	return prefixSets
}

func convertASPathSets(ocpathset map[string]*oc.RoutingPolicy_DefinedSets_BgpDefinedSets_AsPathSet) []gobgpoc.AsPathSet {
	var pathsets []gobgpoc.AsPathSet
	for pathsetName, pathset := range ocpathset {
		pathsets = append(pathsets, gobgpoc.AsPathSet{
			AsPathSetName: pathsetName,
			AsPathList:    pathset.AsPathSetMember,
		})
	}
	return pathsets
}

func convertMED(med oc.RoutingPolicy_PolicyDefinition_Statement_Actions_BgpActions_SetMed_Union) (string, error) {
	if med == nil {
		return "", nil
	}
	switch c := med.(type) {
	case oc.UnionString:
		return string(c), nil
	case oc.UnionUint32:
		return strconv.FormatUint(uint64(c), 10), nil
	case oc.E_BgpActions_SetMed:
		switch c {
		case oc.BgpActions_SetMed_IGP:
			// TODO(wenbli): Find IGP cost to return.
		}
		return "", fmt.Errorf("unsupported value for MED: (%T, %v)", med, med)
	default:
		return "", fmt.Errorf("unrecognized value for MED: (%T, %v)", med, med)
	}
}
