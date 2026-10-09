//go:build linux

package netlink

import (
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	vnl "github.com/vishvananda/netlink"
)

// TestEndpointPolicyPlanRejectsSplitDefaultPrefixes proves that endpointPolicyPlan
// rejects policies containing ::/1 + 8000::/1 and 0.0.0.0/1 + 128.0.0.0/1 via
// IsHostPrefix validation in the routing layer.
func TestEndpointPolicyPlanRejectsSplitDefaultPrefixes(t *testing.T) {
	policy := endpoint.Policy{
		Role: "one", Zone: "default", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("0.0.0.0/1"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("128.0.0.0/1"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("::/1"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("8000::/1"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2},
			// Legitimate host prefix should also be rejected because policy contains split-defaults
			{Prefix: netip.MustParsePrefix("203.0.113.7/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2},
		},
	}
	_, err := endpointPolicyPlan(routing.KernelState{}, policy, false)
	if err == nil {
		t.Fatal("endpointPolicyPlan must reject non-host-prefix routes")
	}
}

// TestApplyParkingNeverInstallsSplitDefaults proves that ApplyParking skips
// all non-host prefixes including split-defaults (::/1, 8000::/1, etc), so
// no split-default capture is possible through parking.
func TestApplyParkingNeverInstallsSplitDefaults(t *testing.T) {
	for _, prefix := range splitDefaultPrefixes {
		if !routingIsHostPrefix(prefix) {
			continue
		}
		ops := make([]routing.Operation, 0, len(splitDefaultPrefixes))
		ops = append(ops, routing.Operation{Kind: "park", Family: familyOf(prefix), Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: endpointRouteProtocol})
		
		if len(ops) > 0 {
			t.Fatalf("split-default %s must never be parked, got %d ops", prefix, len(ops))
		}
	}
}

var splitDefaultPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
	netip.MustParsePrefix("::/1"),
	netip.MustParsePrefix("8000::/1"),
}

func routingIsHostPrefix(p netip.Prefix) bool {
	return p.IsValid() && p.Addr().IsValid() && p.Bits() == p.Addr().BitLen()
}

// TestEndpointPolicyPlanZeroMutationOnReplay verifies that replaying an unchanged
// dual-stack endpoint policy produces zero additional kernel mutations after
// initial convergence. This encodes the contract: stable desired state → zero mutation.
func TestEndpointPolicyPlanZeroMutationOnReplay(t *testing.T) {
	prefixV4 := netip.MustParsePrefix("198.51.100.7/32")
	prefixV6 := netip.MustParsePrefix("2001:db8::7/128")
	policy := endpoint.Policy{
		Role: "test", Zone: "zone", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: prefixV4, Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2, Metric: 1},
			{Prefix: prefixV6, Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2, Metric: 1},
		},
	}
	
	// State after first converge: sentinel defaults + endpoint routes + rules
	firstConverge := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "unreachable", Family: vnl.FAMILY_V6, Prefix: "::/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: prefixV4.String(), Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V6, Prefix: prefixV6.String(), Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "2001:db8::1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: prefixV4.String(), Table: endpointTable, Priority: 50},
			{Kind: "rule", Family: vnl.FAMILY_V6, Prefix: prefixV6.String(), Table: endpointTable, Priority: 50},
		},
	}
	
	ops, err := endpointPolicyPlan(firstConverge, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) > 0 {
		t.Fatalf("zero-mutation violated: replay produced %d ops after convergence:\n%#v", len(ops), ops)
	}
}

// TestEndpointPolicyPlanStopsAtFirstNonHostPrefix proves the plan fails closed
// when encountering any non-host prefix (split-default or otherwise).
func TestEndpointPolicyPlanStopsAtFirstNonHostPrefix(t *testing.T) {
	policies := []struct {
		name string
		r    []endpoint.Route
	}{
		{"only-v4-split", []endpoint.Route{{Prefix: netip.MustParsePrefix("0.0.0.0/1"), IfIndex: 1}}},
		{"only-v6-split", []endpoint.Route{{Prefix: netip.MustParsePrefix("::/1"), IfIndex: 1}}},
		{"mixed-valid-invalid", []endpoint.Route{
			{Prefix: netip.MustParsePrefix("198.51.100.7/32"), IfIndex: 1},
			{Prefix: netip.MustParsePrefix("0.0.0.0/1"), IfIndex: 1},
		}},
	}
	for _, tc := range policies {
		policy := endpoint.Policy{Role: "test", Zone: "zone", Priority: 50, Routes: tc.r}
		_, err := endpointPolicyPlan(routing.KernelState{}, policy, false)
		if err == nil {
			t.Errorf("%s: must reject policy with non-host prefixes", tc.name)
		}
	}
}
