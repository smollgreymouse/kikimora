//go:build windows

package routes

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type kernelCalls struct {
	created []winipcfg.MibIPforwardRow2
	deleted []winipcfg.MibIPforwardRow2
}

func cannedRow(prefix netip.Prefix, ifIndex uint32, metric uint32, protocol int, gateway netip.Addr) winipcfg.MibIPforwardRow2 {
	row := winipcfg.MibIPforwardRow2{
		InterfaceIndex: ifIndex,
		Metric:         metric,
		Protocol:       winipcfg.RouteProtocol(protocol),
	}
	_ = row.DestinationPrefix.SetPrefix(prefix)
	if gateway.IsValid() {
		_ = row.NextHop.SetAddrPort(netip.AddrPortFrom(gateway, 0))
	}
	return row
}

func withKernel(t *testing.T, rows []winipcfg.MibIPforwardRow2) *kernelCalls {
	t.Helper()
	calls := &kernelCalls{}
	previousTable, previousCreate, previousDelete, previousUnicast := routeTable, createRoute, deleteRoute, unicastAddr
	routeTable = func(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error) {
		var matching []winipcfg.MibIPforwardRow2
		for _, row := range rows {
			if row.DestinationPrefix.Prefix().Addr().Is6() == (family == winipcfg.AddressFamily(windows.AF_INET6)) {
				matching = append(matching, row)
			}
		}
		return matching, nil
	}
	unicastAddr = func(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
		if family == winipcfg.AddressFamily(windows.AF_INET) {
			return netip.MustParseAddr("203.0.113.1"), nil
		}
		return netip.MustParseAddr("2001:db8::1"), nil
	}
	createRoute = func(row *winipcfg.MibIPforwardRow2) error {
		calls.created = append(calls.created, *row)
		return nil
	}
	deleteRoute = func(row *winipcfg.MibIPforwardRow2) error {
		calls.deleted = append(calls.deleted, *row)
		return nil
	}
	t.Cleanup(func() {
		routeTable, createRoute, deleteRoute, unicastAddr = previousTable, previousCreate, previousDelete, previousUnicast
	})
	return calls
}

func TestApplyRouteAndParkOperations(t *testing.T) {
	calls := withKernel(t, nil)
	e := NewExecutor()

	err := e.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{
		{Kind: "route", Prefix: "198.51.100.7/32", IfIndex: 5, Metric: 1, Protocol: ownedRouteProtocol, Gateway: "192.168.1.1"},
		{Kind: "park", Prefix: "0.0.0.0/1", Table: kernelTable, Metric: parkRouteMetric, Protocol: ownedRouteProtocol},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.created) != 2 {
		t.Fatalf("expected two creates, got %d", len(calls.created))
	}
	if calls.created[0].InterfaceIndex != 5 {
		t.Fatalf("route ifindex mismatch: %d", calls.created[0].InterfaceIndex)
	}
	if calls.created[0].Protocol != ownedRouteProtocol {
		t.Fatalf("ownership tag missing: %d", calls.created[0].Protocol)
	}
	parked := calls.created[1]
	if parked.InterfaceIndex != loopbackIfIndex {
		t.Fatalf("park route must point at loopback: %d", parked.InterfaceIndex)
	}
	if parked.NextHop.Addr() != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("park next hop must be loopback gateway: %v", parked.NextHop.Addr())
	}

	// Policy rules do not exist on Windows and must be rejected, not ignored.
	if err := e.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{
		{Kind: "rule", Prefix: "198.51.100.7/32", Table: 51890, Priority: 10},
	}}); err == nil {
		t.Fatal("rule operations must fail on windows")
	}
}

func TestApplyDeleteToleratesMissingRoute(t *testing.T) {
	calls := withKernel(t, nil)
	e := NewExecutor()
	deleteRoute = func(row *winipcfg.MibIPforwardRow2) error {
		calls.deleted = append(calls.deleted, *row)
		return windows.ERROR_NOT_FOUND
	}
	err := e.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{
		{Kind: "delete-park", Prefix: "0.0.0.0/1", Table: kernelTable, Metric: parkRouteMetric, Protocol: ownedRouteProtocol},
	}})
	if err != nil {
		t.Fatalf("missing route on delete must be tolerated: %v", err)
	}
	if len(calls.deleted) != 1 {
		t.Fatalf("delete was not attempted: %d", len(calls.deleted))
	}
}

func TestReconcileEndpointPolicySkipsExistingRoutes(t *testing.T) {
	existing := cannedRow(netip.MustParsePrefix("198.51.100.7/32"), 5, 1, ownedRouteProtocol, netip.Addr{})
	calls := withKernel(t, []winipcfg.MibIPforwardRow2{existing})
	e := NewExecutor()

	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 10, Routes: []endpoint.Route{
		{Prefix: netip.MustParsePrefix("198.51.100.7/32"), IfIndex: 5, Metric: 1},
		{Prefix: netip.MustParsePrefix("203.0.113.9/32"), IfIndex: 5},
	}}
	if err := e.ReconcileEndpointPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	if len(calls.created) != 1 {
		t.Fatalf("only the missing endpoint route may be created, got %d", len(calls.created))
	}
	if calls.created[0].DestinationPrefix.Prefix() != netip.MustParsePrefix("203.0.113.9/32") {
		t.Fatalf("unexpected created prefix: %v", calls.created[0].DestinationPrefix.Prefix())
	}

	// Invalid policies fail closed without touching the kernel.
	if err := e.ReconcileEndpointPolicy(context.Background(), endpoint.Policy{Role: "one"}); err == nil {
		t.Fatal("policy without routes must be invalid")
	}
	bad := endpoint.Policy{Role: "one", Zone: "default", Priority: 10, Routes: []endpoint.Route{
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), IfIndex: 5},
	}}
	if err := e.ReconcileEndpointPolicy(context.Background(), bad); err == nil {
		t.Fatal("non-host endpoint prefixes must be rejected")
	}
}

func TestRemoveEndpointPolicyNeverTouchesUnrelated(t *testing.T) {
	ours := cannedRow(netip.MustParsePrefix("198.51.100.7/32"), 5, 1, ownedRouteProtocol, netip.Addr{})
	admin := cannedRow(netip.MustParsePrefix("203.0.113.9/32"), 5, 50, 2, netip.MustParseAddr("192.168.1.1"))
	otherRole := cannedRow(netip.MustParsePrefix("198.51.100.8/32"), 5, 1, ownedRouteProtocol, netip.Addr{})
	calls := withKernel(t, []winipcfg.MibIPforwardRow2{ours, admin, otherRole})
	e := NewExecutor()

	policy := endpoint.Policy{Role: "one", Zone: "default", Priority: 10, Routes: []endpoint.Route{
		{Prefix: netip.MustParsePrefix("198.51.100.7/32"), IfIndex: 5},
	}}
	if err := e.RemoveEndpointPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	if len(calls.deleted) != 1 {
		t.Fatalf("exactly one owned route may be removed, got %d", len(calls.deleted))
	}
	if calls.deleted[0].DestinationPrefix.Prefix() != netip.MustParsePrefix("198.51.100.7/32") {
		t.Fatalf("unexpected removed prefix: %v", calls.deleted[0].DestinationPrefix.Prefix())
	}
}

func TestSnapshotExposesRoutesWithoutRules(t *testing.T) {
	withKernel(t, []winipcfg.MibIPforwardRow2{
		cannedRow(netip.MustParsePrefix("0.0.0.0/0"), 5, 25, 3, netip.MustParseAddr("192.168.1.1")),
		cannedRow(netip.MustParsePrefix("::/0"), 5, 256, 3, netip.MustParseAddr("fe80::1")),
	})
	e := NewExecutor()
	state, err := e.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Rules) != 0 {
		t.Fatalf("windows has no policy rules: %#v", state.Rules)
	}
	if len(state.Routes) != 2 {
		t.Fatalf("expected two routes, got %#v", state.Routes)
	}
	var v4, v6 *routing.Operation
	for i := range state.Routes {
		switch state.Routes[i].Family {
		case 4:
			v4 = &state.Routes[i]
		case 6:
			v6 = &state.Routes[i]
		}
	}
	if v4 == nil || v4.Gateway != "192.168.1.1" || v4.Table != kernelTable {
		t.Fatalf("unexpected v4 snapshot row: %#v", v4)
	}
	if v6 == nil || v6.Gateway != "fe80::1" {
		t.Fatalf("unexpected v6 snapshot row: %#v", v6)
	}
}

func TestApplyParkingMirrorsLinuxSemantics(t *testing.T) {
	calls := withKernel(t, nil)
	e := NewExecutor()

	desired := parking.DesiredState{Role: "one", Prefixes: []netip.Prefix{
		netip.MustParsePrefix("198.51.100.7/32"),
		netip.MustParsePrefix("2001:db8::1/128"),
		netip.MustParsePrefix("198.51.100.0/24"), // non-host prefix must be skipped
	}}
	if err := e.ApplyParking(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if len(calls.created) != 2 {
		t.Fatalf("expected two parked prefixes, got %d", len(calls.created))
	}
	for _, row := range calls.created {
		if row.InterfaceIndex != loopbackIfIndex || row.Metric != parkRouteMetric || row.Protocol != ownedRouteProtocol {
			t.Fatalf("unexpected parked row: ifindex=%d metric=%d protocol=%d",
				row.InterfaceIndex, row.Metric, row.Protocol)
		}
	}
}

func TestApplySurfacesCreateFailures(t *testing.T) {
	withKernel(t, nil)
	e := NewExecutor()
	createRoute = func(row *winipcfg.MibIPforwardRow2) error {
		return errors.New("kernel refused")
	}
	err := e.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{
		{Kind: "route", Prefix: "198.51.100.7/32", IfIndex: 5, Metric: 1, Protocol: ownedRouteProtocol},
	}})
	if err == nil {
		t.Fatal("create failures must surface")
	}
}
