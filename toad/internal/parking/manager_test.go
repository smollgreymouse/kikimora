package parking

import (
	"context"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

func TestPrepareOwnedWithdrawalDoesNotCaptureUserRoute(t *testing.T) {
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{
			{Kind: "route", Prefix: "192.0.2.10/32", Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
			{Kind: "route", Prefix: "192.0.2.11/32", Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
		}},
	}
	manager := NewManager(fake)
	owned := []routing.SelectedRouteOwner{{
		Role: "primary", Interface: "kk0", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32"),
	}}
	if err := manager.PrepareOwnedWithdrawal(context.Background(), "primary", owned); err != nil {
		t.Fatal(err)
	}
	state := manager.Snapshot("primary")
	if state.Count != 1 || len(state.Prefixes) != 1 || state.Prefixes[0].String() != "192.0.2.10/32" {
		t.Fatalf("unexpected parked ownership: %#v", state)
	}
	kernel, _ := fake.Snapshot(context.Background())
	for _, route := range kernel.Routes {
		if route.Kind == "park" && route.Prefix == "192.0.2.11/32" {
			t.Fatalf("unowned user route was parked: %#v", kernel.Routes)
		}
	}
}

func TestPrepareOwnedWithdrawalSupportsIPv6HostRoute(t *testing.T) {
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{{
			Kind: "route", Prefix: "2001:db8:100::10/128", Table: 254, IfIndex: 8, Metric: 10, Protocol: staticProtocol,
		}}},
	}
	manager := NewManager(fake)
	owned := []routing.SelectedRouteOwner{{
		Role: "secondary", Interface: "kk1", IfIndex: 8, Prefix: netip.MustParsePrefix("2001:db8:100::10/128"),
	}}
	if err := manager.PrepareOwnedWithdrawal(context.Background(), "secondary", owned); err != nil {
		t.Fatal(err)
	}
	state := manager.Snapshot("secondary")
	if !state.Active || state.Count != 1 || state.Prefixes[0].Bits() != 128 {
		t.Fatalf("IPv6 host route was not parked: %#v", state)
	}
}

func TestPrepareOwnedWithdrawalRejectsUnverifiedOwnership(t *testing.T) {
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{{
			Kind: "route", Prefix: "192.0.2.10/32", Table: 254, IfIndex: 9, Metric: 10, Protocol: staticProtocol,
		}}},
	}
	manager := NewManager(fake)
	owned := []routing.SelectedRouteOwner{{
		Role: "primary", Interface: "kk0", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32"),
	}}
	if err := manager.PrepareOwnedWithdrawal(context.Background(), "primary", owned); err != nil {
		t.Fatal(err)
	}
	if state := manager.Snapshot("primary"); state.Active || state.Count != 0 {
		t.Fatalf("mismatched ifindex ownership was trusted: %#v", state)
	}
}
