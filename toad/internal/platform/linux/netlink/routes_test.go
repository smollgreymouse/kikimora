//go:build linux

package netlink

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	vnl "github.com/vishvananda/netlink"
)

func TestRouteForOperationPreservesProtocol(t *testing.T) {
	op := routing.Operation{
		Kind:     "route",
		Prefix:   "192.0.2.10/32",
		Table:    endpointTable,
		IfIndex:  7,
		Metric:   3,
		Gateway:  "192.0.2.1",
		Protocol: 99,
	}
	route, err := routeForOperation(op)
	if err != nil {
		t.Fatal(err)
	}
	if int(route.Protocol) != 99 || route.Table != endpointTable || route.LinkIndex != 7 || route.Priority != 3 {
		t.Fatalf("route conversion lost ownership fields: %+v", route)
	}
}

func TestEndpointPolicyPlanReconcilesCompleteSet(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "unreachable", Family: vnl.FAMILY_V6, Prefix: "::/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "192.0.2.10/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "192.0.2.11/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "192.0.2.10/32", Table: endpointTable, Priority: 50},
			{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "192.0.2.11/32", Table: endpointTable, Priority: 50},
		},
	}
	policy := endpoint.Policy{
		Role: "primary", Zone: "primary", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("192.0.2.11/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2, Metric: 1},
			{Prefix: netip.MustParsePrefix("192.0.2.12/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2, Metric: 1},
		},
	}
	ops, err := endpointPolicyPlan(current, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []routing.Operation{
		{Kind: "route", Family: vnl.FAMILY_V4, Prefix: "192.0.2.12/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
		{Kind: "rule", Family: vnl.FAMILY_V4, Prefix: "192.0.2.12/32", Table: endpointTable, Priority: 50},
		{Kind: "delete-rule", Family: vnl.FAMILY_V4, Prefix: "192.0.2.10/32", Table: endpointTable, Priority: 50},
		{Kind: "delete-route", Family: vnl.FAMILY_V4, Prefix: "192.0.2.10/32", Table: endpointTable, IfIndex: 2, Metric: 1, Gateway: "192.0.2.1", Protocol: endpointRouteProtocol},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("unexpected reconciliation plan:\n got=%#v\nwant=%#v", ops, want)
	}
}

func TestEndpointPolicyPlanKeepsSharedRoute(t *testing.T) {
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "route", Family: vnl.FAMILY_V6, Prefix: "2001:db8::10/128", Table: endpointTable, IfIndex: 3, Metric: 1, Gateway: "2001:db8::1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{
			{Kind: "rule", Family: vnl.FAMILY_V6, Prefix: "2001:db8::10/128", Table: endpointTable, Priority: 50},
			{Kind: "rule", Family: vnl.FAMILY_V6, Prefix: "2001:db8::10/128", Table: endpointTable, Priority: 51},
		},
	}
	policy := endpoint.Policy{Role: "primary", Zone: "primary", Priority: 50}
	ops, err := endpointPolicyPlan(current, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Kind != "delete-rule" {
		t.Fatalf("shared endpoint route was garbage-collected: %#v", ops)
	}
}

func TestEndpointPolicyPlanIsIdempotent(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8::10/128")
	policy := endpoint.Policy{
		Role: "secondary", Zone: "secondary", Priority: 51,
		Routes: []endpoint.Route{{Prefix: prefix, Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 3, Metric: 1}},
	}
	current := routing.KernelState{
		Routes: []routing.Operation{
			{Kind: "unreachable", Family: vnl.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "unreachable", Family: vnl.FAMILY_V6, Prefix: "::/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "route", Family: vnl.FAMILY_V6, Prefix: prefix.String(), Table: endpointTable, IfIndex: 3, Metric: 1, Gateway: "2001:db8::1", Protocol: endpointRouteProtocol},
		},
		Rules: []routing.Operation{{Kind: "rule", Family: vnl.FAMILY_V6, Prefix: prefix.String(), Table: endpointTable, Priority: 51}},
	}
	ops, err := endpointPolicyPlan(current, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("idempotent reconciliation produced mutations: %#v", ops)
	}
}

func TestEndpointPolicyPlanIsSafeAtEveryFailureBoundary(t *testing.T) {
	policy := endpoint.Policy{
		Role: "primary", Zone: "primary", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("192.0.2.12/32"), Gateway: netip.MustParseAddr("192.0.2.1"), IfIndex: 2, Metric: 1},
			{Prefix: netip.MustParsePrefix("2001:db8::12/128"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 3, Metric: 1},
		},
	}
	ops, err := endpointPolicyPlan(routing.KernelState{}, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	state := routing.KernelState{}
	for boundary, op := range ops {
		applyModelOperation(&state, op)
		assertEndpointRulesProtected(t, state, boundary+1)
	}
}

func assertEndpointRulesProtected(t *testing.T, state routing.KernelState, boundary int) {
	t.Helper()
	for _, rule := range state.Rules {
		if rule.Kind != "rule" || rule.Table != endpointTable {
			continue
		}
		wantDefault := "0.0.0.0/0"
		if rule.Family == vnl.FAMILY_V6 {
			wantDefault = "::/0"
		}
		hasSentinel, hasRoute := false, false
		for _, route := range state.Routes {
			if route.Kind == "unreachable" && route.Table == endpointTable && route.Prefix == wantDefault {
				hasSentinel = true
			}
			if route.Kind == "route" && route.Table == endpointTable && route.Prefix == rule.Prefix {
				hasRoute = true
			}
		}
		if !hasSentinel || !hasRoute {
			t.Fatalf("failure boundary %d exposed rule %#v without route/sentinel: routes=%#v", boundary, rule, state.Routes)
		}
	}
}

func applyModelOperation(state *routing.KernelState, op routing.Operation) {
	switch op.Kind {
	case "route", "unreachable":
		for i := range state.Routes {
			if state.Routes[i].Kind == op.Kind && state.Routes[i].Table == op.Table && state.Routes[i].Prefix == op.Prefix {
				state.Routes[i] = op
				return
			}
		}
		state.Routes = append(state.Routes, op)
	case "rule":
		for i := range state.Rules {
			if state.Rules[i].Table == op.Table && state.Rules[i].Priority == op.Priority && state.Rules[i].Prefix == op.Prefix {
				state.Rules[i] = op
				return
			}
		}
		state.Rules = append(state.Rules, op)
	case "delete-rule":
		out := state.Rules[:0]
		for _, rule := range state.Rules {
			if rule.Table == op.Table && rule.Priority == op.Priority && rule.Prefix == op.Prefix {
				continue
			}
			out = append(out, rule)
		}
		state.Rules = out
	case "delete-route":
		out := state.Routes[:0]
		for _, route := range state.Routes {
			if route.Table == op.Table && route.Prefix == op.Prefix {
				continue
			}
			out = append(out, route)
		}
		state.Routes = out
	}
}
