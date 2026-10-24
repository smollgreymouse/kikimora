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

// TestRemovePlanDeletesSentinelsWhenLastOwnerLeaves encodes the DisconnectAll
// contract: after the last endpoint-policy owner is removed, the table-51890
// unreachable sentinels must go with it — an ownerless fail-closed table would
// keep blackholing ordinary routing.
func TestRemovePlanDeletesSentinelsWhenLastOwnerLeaves(t *testing.T) {
	sentinel := func(family int, prefix string) routing.Operation {
		return routing.Operation{Kind: "unreachable", Family: family, Prefix: prefix, Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol}
	}
	current := routing.KernelState{
		Routes: []routing.Operation{
			sentinel(vnl.FAMILY_V4, "0.0.0.0/0"),
			sentinel(vnl.FAMILY_V6, "::/0"),
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, Priority: 50},
		},
	}
	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 50}
	ops, err := endpointPolicyPlan(current, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	deletedRules, deletedRoutes, deletedSentinels := 0, 0, 0
	for _, op := range ops {
		switch op.Kind {
		case "delete-rule":
			deletedRules++
		case "delete-route":
			deletedRoutes++
			if op.Prefix == "0.0.0.0/0" || op.Prefix == "::/0" {
				deletedSentinels++
			}
		}
	}
	if deletedRules != 1 {
		t.Fatalf("expected the owner rule deleted, got %d (%+v)", deletedRules, ops)
	}
	if deletedSentinels != 2 {
		t.Fatalf("expected both family sentinels deleted, got %d (%+v)", deletedSentinels, ops)
	}
	if deletedRoutes != 3 {
		t.Fatalf("expected host route plus both sentinels deleted, got %d (%+v)", deletedRoutes, ops)
	}
}

// TestRemovePlanKeepsSentinelsWhileOtherOwnerRemains proves per-role
// disconnect does not release the fail-closed table another role still owns.
func TestRemovePlanKeepsSentinelsWhileOtherOwnerRemains(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, Priority: 50},
			{Kind: "rule", Family: vnl.FAMILY_V6, Prefix: "2001:db8::7/128", Table: endpointTable, Priority: 51},
		},
	}
	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 50}
	ops, err := endpointPolicyPlan(current, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == "delete-route" && op.Prefix == "0.0.0.0/0" {
			t.Fatalf("sentinel deleted while another owner still holds the table (%+v)", ops)
		}
	}
	found := false
	for _, op := range ops {
		if op.Kind == "delete-rule" && op.Prefix == "198.51.100.7/32" {
			found = true
		}
	}
	if !found {
		t.Fatalf("own rule must still be deleted (%+v)", ops)
	}
}

// TestRemovePlanWithoutSentinelsIsQuiet and the idempotency test below pin the
// snapshot-driven shape of the removal: a remove over state that no longer
// holds the resources plans nothing.
func TestRemovePlanWithoutSentinelsIsQuiet(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, Priority: 50},
		},
	}
	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 50}
	ops, err := endpointPolicyPlan(current, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == "delete-route" && op.Table == endpointTable && op.Metric == 32767 {
			t.Fatalf("phantom sentinel deletion without a sentinel in state (%+v)", ops)
		}
	}
}

func TestRemovePlanIsIdempotent(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "unreachable", Family: vnl.FAMILY_V6, Prefix: "::/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "198.51.100.7/32", Table: endpointTable, Priority: 50},
		},
	}
	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 50}
	first, err := endpointPolicyPlan(current, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("first remove planned nothing")
	}
	// Apply the plan to the fake kernel: drop the routes and rules the plan deletes.
	remaining := routing.KernelState{}
	for _, route := range current.Routes {
		dropped := false
		for _, op := range first {
			if op.Kind == "delete-route" && op.Prefix == route.Prefix && op.Table == route.Table &&
				(route.Kind != "unreachable" || op.Metric == route.Metric) {
				dropped = true
				break
			}
		}
		if !dropped {
			remaining.Routes = append(remaining.Routes, route)
		}
	}
	for _, rule := range current.Rules {
		deleted := false
		for _, op := range first {
			if op.Kind == "delete-rule" && op.Priority == rule.Priority && op.Prefix == rule.Prefix && op.Table == rule.Table {
				deleted = true
				break
			}
		}
		if !deleted {
			remaining.Rules = append(remaining.Rules, rule)
		}
	}
	second, err := endpointPolicyPlan(remaining, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second remove must be a no-op, got %+v", second)
	}
}
