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
