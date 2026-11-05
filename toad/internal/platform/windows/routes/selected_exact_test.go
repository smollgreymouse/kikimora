//go:build windows

package routes

import (
	"context"
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The 08D1 §9/§31.19/§31.25/§31.26 Windows selected/park writer matrix.

// fakeRow builds one kernel row as the snapshot projection would see it.
func fakeRow(prefix string, ifindex int, luid uint64, protocol int, metric uint32, gateway string) routing.Operation {
	return routing.Operation{
		Kind: "route", Prefix: prefix, Table: kernelTable,
		IfIndex: ifindex, InterfaceLuid: luid, Protocol: protocol,
		Metric: metric, Gateway: gateway,
	}
}

func selectedFake(rows []routing.Operation) *Executor {
	e := &Executor{}
	previousTable := routeTable
	routeTable = func(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error) {
		return nil, nil
	}
	_ = previousTable
	e.mu.Lock()
	// The executor snapshot reads through routeTable; the tests that need
	// row-level state drive it through rowsForTest instead.
	e.mu.Unlock()
	_ = rows
	return e
}

func TestSelectedWinsOverParkEffectiveMetric(t *testing.T) {
	// §31.25: the winning proof compares route offset + interface metric in
	// uint64. selected offset 0 + interface metric 50000 vs park 42760 + 1
	// keeps the park; interface metric 25 lets the selected row win.
	e := NewExecutor()
	prefix := netip.MustParsePrefix("203.0.113.10/32")
	selected := routing.SelectedRouteIdentity{Table: kernelTable, IfIndex: 5, Protocol: ownedRouteProtocol, Metric: 0, InterfaceLuid: 100}
	park := routing.SelectedParkIdentity{Kind: routing.SelectedParkLoopback, Table: kernelTable, IfIndex: 1, NextHop: "127.0.0.1", Protocol: ownedRouteProtocol, Metric: parkRouteMetric, InterfaceLuid: 1}

	previous := ipInterfaceMetric
	t.Cleanup(func() { ipInterfaceMetric = previous })
	ipInterfaceMetric = func(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
		if luid == 100 {
			return 50000, nil
		}
		return 1, nil
	}
	wins, err := e.SelectedWinsOverPark(context.Background(), prefix, selected, park)
	if err != nil || wins {
		t.Fatalf("a heavy selected interface metric must not win: %v %v", wins, err)
	}
	ipInterfaceMetric = func(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
		if luid == 100 {
			return 25, nil
		}
		return 1, nil
	}
	wins, err = e.SelectedWinsOverPark(context.Background(), prefix, selected, park)
	if err != nil || !wins {
		t.Fatalf("a light selected interface metric must win: %v %v", wins, err)
	}
	// uint64 arithmetic never wraps at the uint32 max (§31.25).
	ipInterfaceMetric = func(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
		if luid == 100 {
			return 0xFFFFFFFF, nil
		}
		return 1, nil
	}
	wins, err = e.SelectedWinsOverPark(context.Background(), prefix, selected, park)
	if err != nil || wins {
		t.Fatalf("uint32 max interface metric must not wrap into a win: %v %v", wins, err)
	}
}

func TestSelectedIdentityIncludesLuid(t *testing.T) {
	// §31.19: same index + different LUID is a different identity.
	a := routing.SelectedRouteIdentity{Table: kernelTable, IfIndex: 5, Protocol: ownedRouteProtocol, InterfaceLuid: 11}
	b := a
	b.InterfaceLuid = 12
	if a.Equal(b) {
		t.Fatal("the LUID is part of the exact identity")
	}
	c := a
	c.IfIndex = 6
	if a.Equal(c) {
		t.Fatal("the index is part of the exact identity")
	}
	// The projection carries the LUID from the snapshot rows.
	state := routing.KernelState{Routes: []routing.Operation{
		fakeRow("203.0.113.10/32", 5, 77, ownedRouteProtocol, 0, ""),
	}}
	rows := selectedRowsFor(&state, netip.MustParsePrefix("203.0.113.10/32"))
	if len(rows) != 1 || rows[0].Route.InterfaceLuid != 77 {
		t.Fatalf("the projection must keep the LUID: %#v", rows)
	}
}

func TestProtocolIcmpLookalikeIsForeign(t *testing.T) {
	// §31.26: a protocol-4 row on Windows is the ICMP class — foreign even
	// when every other field matches the desired identity.
	state := routing.KernelState{Routes: []routing.Operation{
		fakeRow("203.0.113.10/32", 5, 77, 4 /* RTPROT_STATIC, ICMP on Windows */, 0, ""),
	}}
	rows := selectedRowsFor(&state, netip.MustParsePrefix("203.0.113.10/32"))
	if len(rows) != 1 || rows[0].Kind != routing.SelectedKernelForeign {
		t.Fatalf("a protocol-4 row must project foreign: %#v", rows)
	}
}

func TestRowForOperationInitializesBeforeOverrides(t *testing.T) {
	// §31.25: every created row goes through InitializeIpForwardEntry; the
	// builder must not start from a bare struct literal. The observable
	// contract: lifetimes are infinite and the protocol is NetMgmt.
	previousUnicast := unicastAddr
	unicastAddr = func(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
		return netip.MustParseAddr("203.0.113.5"), nil
	}
	t.Cleanup(func() { unicastAddr = previousUnicast })
	row, err := rowForOperation(routing.Operation{
		Kind: "route", Prefix: "203.0.113.10/32", IfIndex: 5, Protocol: ownedRouteProtocol,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ValidLifetime != 0xFFFFFFFF || row.PreferredLifetime != 0xFFFFFFFF {
		t.Fatalf("created rows must carry infinite lifetimes: %#v", row)
	}
	if int(row.Protocol) != int(winipcfg.RouteProtocolNetMgmt) {
		t.Fatalf("created rows use NetMgmt(3): %d", row.Protocol)
	}
}

func TestDeleteUsesPersistedLuidNotCurrentAdapter(t *testing.T) {
	// §31.19: exact old-identity deletion constructs the row with the
	// persisted LUID; it never resolves the current adapter.
	calls := withKernel(t, nil)
	e := NewExecutor()
	previousUnicast := unicastAddr
	unicastAddr = func(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
		return netip.MustParseAddr("203.0.113.5"), nil
	}
	t.Cleanup(func() { unicastAddr = previousUnicast })
	persisted := routing.SelectedRouteIdentity{
		Table: kernelTable, IfIndex: 5, Protocol: ownedRouteProtocol,
		InterfaceLuid: 4242,
	}
	if err := e.applyOperation(deleteRouteOperation(netip.MustParsePrefix("203.0.113.10/32"), persisted)); err != nil {
		t.Fatal(err)
	}
	if len(calls.deleted) != 1 {
		t.Fatalf("one delete expected: %d", len(calls.deleted))
	}
	if uint64(calls.deleted[0].InterfaceLUID) != 4242 {
		t.Fatalf("the persisted LUID must reach the delete: %d", calls.deleted[0].InterfaceLUID)
	}
	if calls.deleted[0].InterfaceIndex != 5 {
		t.Fatalf("the persisted index must reach the delete: %d", calls.deleted[0].InterfaceIndex)
	}
	_ = windows.ERROR_NOT_FOUND
}
