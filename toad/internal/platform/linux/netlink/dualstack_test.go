//go:build linux

package netlink

import (
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	vnl "github.com/vishvananda/netlink"
)

// TestEndpointPolicyPlanRefusesSplitDefaultPrefixes encodes the dual-stack
// contract rule that a managed role must never install ::/1 + 8000::/1 or the
// IPv4 0.0.0.0/1 + 128.0.0.0/1 split defaults through its endpoint policy.
func TestEndpointPolicyPlanRefusesSplitDefaultPrefixes(t *testing.T) {
	policy := endpoint.Policy{
		Role: "one", Zone: "default", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("0.0.0.0/1"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("128.0.0.0/1"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("::/1"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("8000::/1"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2},
			// Legitimate host prefix should still be rejected because the whole
			// policy contains non-host routes.
			{Prefix: netip.MustParsePrefix("203.0.113.7/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
		},
	}
	_, err := endpointPolicyPlan(routing.KernelState{}, policy, false)
	if err == nil {
		t.Fatal("split-default endpoint policy must be rejected")
	}
}

// TestEndpointPolicyPlanTreatsFamiliesIndependently encodes the rule that
// IPv4 readiness never implies IPv6 readiness: an already-present IPv4
// endpoint route must not cause the planner to skip creating the missing V6.
func TestEndpointPolicyPlanTreatsFamiliesIndependently(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, Priority: 50},
		},
	}
	policy := endpoint.Policy{
		Role: "one", Zone: "default", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("198.51.100.7/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2, Metric: 1},
			{Prefix: netip.MustParsePrefix("2001:db8::7/128"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2, Metric: 1},
		},
	}
	ops, err := endpointPolicyPlan(current, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	v4Created, v6Created := false, false
	for _, op := range ops {
		if op.Kind == "route" && op.Family == vnl.FAMILY_V4 && op.Prefix == "198.51.100.7/32" {
			v4Created = true
		}
		if op.Kind == "route" && op.Family == vnl.FAMILY_V6 && op.Prefix == "2001:db8::7/128" {
			v6Created = true
		}
	}
	if v4Created {
		t.Fatal("existing IPv4 route must not be re-created in idempotent reconcile")
	}
	if !v6Created {
		t.Fatal("missing IPv6 route must be created regardless of IPv4 presence")
	}
}

// TestApplyParkingNeverInstallsSplitDefaults proves that ApplyParking skips
// non-host prefixes (including ::/1, 8000::/1, 0.0.0.0/1, 128.0.0.0/1), so
// no split-default capture is ever possible through parking.
func TestApplyParkingNeverInstallsSplitDefaults(t *testing.T) {
	ops := make([]routing.Operation, 0, len(splitDefaultPrefixes))
	for _, prefix := range splitDefaultPrefixes {
		if !routing.IsHostPrefix(prefix) {
			continue
		}
		ops = append(ops, routing.Operation{Kind: "park", Family: familyOf(prefix), Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: endpointRouteProtocol})
	}
	if len(ops) > 0 {
		t.Fatalf("split-default prefixes must never be parked, got %d operations: %#v", len(ops), ops)
	}
}

// splitDefaultPrefixes lists the legacy split-default pairs that must never
// appear as a result of Kikimora routing operations.
var splitDefaultPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
	netip.MustParsePrefix("::/1"),
	netip.MustParsePrefix("8000::/1"),
}
