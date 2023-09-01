/*
 Copyright 2022 Google LLC

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

      https://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package integration_test

import (
	"testing"

	"github.com/openconfig/lemming/internal/binding"
	"github.com/openconfig/lemming/policytest"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"

	valpb "github.com/openconfig/lemming/bgp/tests/proto/policyval"
)

func TestMain(m *testing.M) {
	ondatra.RunTests(m, binding.Get(".."))
}

func TestPrefixSet(t *testing.T) {
	installPolicies := func(t *testing.T, pair12, pair52, pair23 *policytest.DevicePair, invert bool) {
		t.Log("Installing test policies")
		dut2 := pair12.Second
		port1 := pair12.FirstPort

		prefix1 := "10.33.0.0/16"
		prefix2 := "10.34.0.0/16"

		// Policy to reject routes with the given prefix set
		policyName := "def1"

		// Create prefix set
		prefixSetName := "reject-" + prefix1
		prefix1Path := policytest.RoutingPolicyPath.DefinedSets().PrefixSet(prefixSetName).Prefix(prefix1, "exact").IpPrefix()
		gnmi.Replace(t, dut2, prefix1Path.Config(), prefix1)
		prefix2Path := policytest.RoutingPolicyPath.DefinedSets().PrefixSet(prefixSetName).Prefix(prefix2, "16..23").IpPrefix()
		gnmi.Replace(t, dut2, prefix2Path.Config(), prefix2)

		policy := &oc.RoutingPolicy_PolicyDefinition_Statement_OrderedMap{}
		stmt, err := policy.AppendNew("stmt1")
		if err != nil {
			t.Fatalf("Cannot append new BGP policy statement: %v", err)
		}
		// Match on prefix set & reject route
		stmt.GetOrCreateConditions().GetOrCreateMatchPrefixSet().SetPrefixSet(prefixSetName)
		if invert {
			stmt.GetOrCreateConditions().GetOrCreateMatchPrefixSet().SetMatchSetOptions(oc.RoutingPolicy_MatchSetOptionsRestrictedType_INVERT)
		} else {
			stmt.GetOrCreateConditions().GetOrCreateMatchPrefixSet().SetMatchSetOptions(oc.RoutingPolicy_MatchSetOptionsRestrictedType_ANY)
		}
		stmt.GetOrCreateActions().SetPolicyResult(oc.RoutingPolicy_PolicyResultType_REJECT_ROUTE)
		// Install policy
		gnmi.Replace(t, dut2, policytest.RoutingPolicyPath.PolicyDefinition(policyName).Config(), &oc.RoutingPolicy_PolicyDefinition{Statement: policy})
		gnmi.Replace(t, dut2, policytest.BGPPath.Neighbor(port1.IPv4).ApplyPolicy().ImportPolicy().Config(), []string{policyName})
	}

	invertResult := func(result valpb.RouteTestResult, invert bool) valpb.RouteTestResult {
		if invert {
			switch result {
			case valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT:
				return valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD
			case valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD:
				return valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT
			default:
			}
		}
		return result
	}

	getspec := func(invert bool) *valpb.PolicyTestCase {
		spec := &valpb.PolicyTestCase{
			Description: "Test that one prefix gets accepted and the other rejected via an ANY prefix-set.",
			RouteTests: []*valpb.RouteTestCase{{
				Description: "Exact match",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.33.0.0/16",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD, invert),
			}, {
				Description: "Not exact match",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.33.0.0/17",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT, invert),
			}, {
				Description: "No match with any prefix",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.3.0.0/16",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT, invert),
			}, {
				Description: "mask length too short",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.34.0.0/15",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT, invert),
			}, {
				Description: "Lower end of mask length",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.34.0.0/16",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD, invert),
			}, {
				Description: "Middle of mask length",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.34.0.0/20",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD, invert),
			}, {
				Description: "Upper end of mask length",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.34.0.0/23",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_DISCARD, invert),
			}, {
				Description: "mask length too long",
				Input: &valpb.TestRoute{
					ReachPrefix: "10.34.0.0/24",
				},
				ExpectedResultBeforePolicy: valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT,
				ExpectedResult:             invertResult(valpb.RouteTestResult_ROUTE_TEST_RESULT_ACCEPT, invert),
			}},
		}
		return spec
	}

	t.Run("ANY", func(t *testing.T) {
		policytest.TestPolicy(t, policytest.TestCase{
			Spec: getspec(false),
			InstallPolicies: func(t *testing.T, pair12, pair52, pair23 *policytest.DevicePair) {
				installPolicies(t, pair12, pair52, pair23, false)
			},
		})
	})
	t.Run("INVERT", func(t *testing.T) {
		policytest.TestPolicy(t, policytest.TestCase{
			Spec: getspec(true),
			InstallPolicies: func(t *testing.T, pair12, pair52, pair23 *policytest.DevicePair) {
				installPolicies(t, pair12, pair52, pair23, true)
			},
		})
	})
}
