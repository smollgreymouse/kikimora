package parking

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

type partialFailureExecutor struct {
	fake      *routing.Fake
	failAfter int
	failed    bool
}

func (e *partialFailureExecutor) Snapshot(ctx context.Context) (routing.KernelState, error) {
	return e.fake.Snapshot(ctx)
}

func (e *partialFailureExecutor) Apply(ctx context.Context, tx routing.Transaction) error {
	if e.failed || e.failAfter <= 0 {
		return e.fake.Apply(ctx, tx)
	}
	for i, op := range tx.Operations {
		if err := e.fake.Apply(ctx, routing.Transaction{Role: tx.Role, Operations: []routing.Operation{op}}); err != nil {
			return err
		}
		if i+1 == e.failAfter {
			e.failed = true
			return errors.New("injected partial route transaction failure")
		}
	}
	return nil
}

func TestPrepareOwnedWithdrawalIsFailClosedAcrossPartialApply(t *testing.T) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.10/32"),
		netip.MustParsePrefix("2001:db8:100::10/128"),
	}
	for _, failAfter := range []int{1, 2} {
		t.Run(fmt.Sprintf("after-%d", failAfter), func(t *testing.T) {
			fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{
				{Kind: "route", Prefix: prefixes[0].String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
				{Kind: "route", Prefix: prefixes[1].String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
			}}}
			executor := &partialFailureExecutor{fake: fake, failAfter: failAfter}
			manager := NewManager(executor)
			owned := []routing.SelectedRouteOwner{
				{Role: "primary", Interface: "kk0", IfIndex: 7, Prefix: prefixes[0]},
				{Role: "primary", Interface: "kk0", IfIndex: 7, Prefix: prefixes[1]},
			}

			if err := manager.PrepareOwnedWithdrawal(context.Background(), "primary", owned); err == nil {
				t.Fatal("partial transaction failure was not reported")
			}
			kernel, err := executor.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, prefix := range prefixes {
				selected, parked := false, false
				for _, route := range kernel.Routes {
					if route.Prefix != prefix.String() || route.Table != 254 {
						continue
					}
					selected = selected || route.Kind == "route"
					parked = parked || route.Kind == "park"
				}
				if !selected && !parked {
					t.Fatalf("%s lost both selected route and park after partial failure: %#v", prefix, kernel.Routes)
				}
			}

			// The same desired transaction must converge after the injected one-shot
			// failure; already-installed parks are idempotent.
			if err := manager.PrepareOwnedWithdrawal(context.Background(), "primary", owned); err != nil {
				t.Fatalf("retry did not converge: %v", err)
			}
			state := manager.Snapshot("primary")
			if !state.Active || state.Count != len(prefixes) {
				t.Fatalf("retry did not record all parks: %#v", state)
			}
		})
	}
}

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

// TestPrepareOwnedWithdrawalIsIdempotentOnReplay encodes the zero-mutation
// steady-state rule: calling PrepareOwnedWithdrawal repeatedly with the same
// desired prefixes must converge to a stable kernel state with no additional
// park routes created after the first successful pass.
func TestPrepareOwnedWithdrawalIsIdempotentOnReplay(t *testing.T) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.10/32"),
		netip.MustParsePrefix("2001:db8:100::10/128"),
	}
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{
			{Kind: "route", Prefix: prefixes[0].String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
			{Kind: "route", Prefix: prefixes[1].String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
		}},
	}
	manager := NewManager(fake)
	owned := []routing.SelectedRouteOwner{
		{Role: "test", Interface: "kk0", IfIndex: 7, Prefix: prefixes[0]},
		{Role: "test", Interface: "kk0", IfIndex: 7, Prefix: prefixes[1]},
	}

	// First pass installs parks.
	if err := manager.PrepareOwnedWithdrawal(context.Background(), "test", owned); err != nil {
		t.Fatal(err)
	}
	kernel, _ := fake.Snapshot(context.Background())
	parkCount := 0
	for _, r := range kernel.Routes {
		if r.Kind == "park" {
			parkCount++
		}
	}
	if parkCount != 2 {
		t.Fatalf("first pass should have created two parks, got %d: %#v", parkCount, kernel.Routes)
	}

	// Second pass must not create additional parks (idempotent apply).
	initialRouteCount := len(kernel.Routes)
	if err := manager.PrepareOwnedWithdrawal(context.Background(), "test", owned); err != nil {
		t.Fatalf("replay must not fail: %v", err)
	}
	kernel2, _ := fake.Snapshot(context.Background())
	if len(kernel2.Routes) != initialRouteCount {
		t.Fatalf("idempotent reconcile produced mutations: routes %d -> %d", initialRouteCount, len(kernel2.Routes))
	}
	state := manager.Snapshot("test")
	if !state.Active || state.Count != 2 {
		t.Fatalf("state corrupted after replay: %#v", state)
	}
}

// TestRestoreCheckpointNeverTrustsStaleParkedState encodes the rule that
// RestoreCheckpoint never treats an old checkpoint as proof of existence:
// only parks verified in the current kernel snapshot are reconstructed.
func TestRestoreCheckpointNeverTrustsStaleParkedState(t *testing.T) {
	checkpoint := Checkpoint{Role: "test", Parked: []OwnedRoute{
		{Role: "test", Interface: "kk0", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32")},
		{Role: "test", Interface: "kk1", IfIndex: 8, Prefix: netip.MustParsePrefix("2001:db8:100::10/128")},
	}}
	// Kernel has only one of the parked prefixes; the other is gone.
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{{
			Kind: "park", Prefix: "192.0.2.10/32", Table: 254, Metric: 42760, Protocol: staticProtocol,
		}}},
	}
	manager := NewManager(fake)
	if err := manager.RestoreCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	state := manager.Snapshot("test")
	if !state.Active || state.Count != 1 {
		t.Fatalf("only verified park must be restored: %#v", state)
	}
	if state.Prefixes[0] != netip.MustParsePrefix("192.0.2.10/32") {
		t.Fatalf("unexpected verified prefix: %v", state.Prefixes)
	}
}

func TestPrepareRegisteredWithdrawalParksExplicitPrefixes(t *testing.T) {
	v4 := netip.MustParsePrefix("203.0.113.10/32")
	v6 := netip.MustParsePrefix("2001:db8::10/128")
	staticWide := netip.MustParsePrefix("10.0.0.0/24")
	fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
		{Kind: "route", Prefix: v6.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
		{Kind: "route", Prefix: staticWide.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
	}}}
	manager := NewManager(fake)
	owned := []routing.SelectedRoute{
		{Role: "primary", Zone: "primary", Prefix: v4, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedActive},
		{Role: "primary", Zone: "primary", Prefix: v6, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedActive},
		{Role: "primary", Zone: "primary", Prefix: staticWide, Source: routing.SelectedStatic, IfIndex: 7, State: routing.SelectedActive},
	}
	parked, err := manager.PrepareRegisteredWithdrawal(context.Background(), "primary", owned)
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 3 {
		t.Fatalf("explicit /32, /128 and /24 must all park: %#v", parked)
	}
	state := manager.Snapshot("primary")
	if state.Count != 3 || !state.Active {
		t.Fatalf("park state: %#v", state)
	}
	kernel, _ := fake.Snapshot(context.Background())
	parks := 0
	for _, route := range kernel.Routes {
		if route.Kind == "park" && route.Metric == 42760 {
			parks++
		}
	}
	if parks != 3 {
		t.Fatalf("three park rows expected: %#v", kernel.Routes)
	}
}

func TestPrepareRegisteredWithdrawalSkipsUnverifiedRows(t *testing.T) {
	v4 := netip.MustParsePrefix("203.0.113.10/32")
	gone := netip.MustParsePrefix("203.0.113.11/32")
	fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
	}}}
	manager := NewManager(fake)
	owned := []routing.SelectedRoute{
		{Role: "primary", Zone: "primary", Prefix: v4, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedActive},
		// A registered destination whose kernel row vanished is not parked:
		// the unreachable protection is pointless for an absent route, and
		// the recovery reconciles the row instead.
		{Role: "primary", Zone: "primary", Prefix: gone, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedPending},
	}
	parked, err := manager.PrepareRegisteredWithdrawal(context.Background(), "primary", owned)
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 1 || parked[0] != v4 {
		t.Fatalf("only the verified row parks: %#v", parked)
	}
}

func TestPrepareRegisteredWithdrawalRejectsBadShapes(t *testing.T) {
	manager := NewManager(&routing.Fake{})
	cases := []struct {
		name  string
		entry routing.SelectedRoute
	}{
		{"dns non-host", routing.SelectedRoute{Role: "primary", Prefix: netip.MustParsePrefix("10.0.0.0/24"), Source: routing.SelectedDNS, IfIndex: 7}},
		{"non-canonical", routing.SelectedRoute{Role: "primary", Prefix: netip.MustParsePrefix("203.0.113.10/24"), Source: routing.SelectedDNS, IfIndex: 7}},
		{"unknown source", routing.SelectedRoute{Role: "primary", Prefix: netip.MustParsePrefix("203.0.113.10/32"), Source: "carrier-pigeon", IfIndex: 7}},
	}
	for _, tc := range cases {
		if _, err := manager.PrepareRegisteredWithdrawal(context.Background(), "primary", []routing.SelectedRoute{tc.entry}); err == nil {
			t.Fatalf("%s must be rejected", tc.name)
		}
	}
}

func TestRegisteredRestorationRequiresTheExactOwnedRow(t *testing.T) {
	v4 := netip.MustParsePrefix("203.0.113.10/32")
	park := func(routes []routing.Operation) *Manager {
		return NewManager(&routing.Fake{State: routing.KernelState{Routes: routes}})
	}
	expected := []routing.SelectedRoute{
		{Role: "primary", Zone: "primary", Prefix: v4, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedActive},
	}

	// A wrong-interface lower-metric route must not release the park.
	manager := park([]routing.Operation{
		{Kind: "unreachable", Prefix: v4.String(), Table: 254, Metric: 42760},
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 2, Metric: 10, Protocol: staticProtocol},
	})
	released, err := manager.RegisteredRestoration(context.Background(), "primary", expected)
	if err != nil || released != 0 {
		t.Fatalf("a physical-interface route must never release a park: %d %v", released, err)
	}

	// A routing-daemon row on the tunnel device is not ours either.
	manager = park([]routing.Operation{
		{Kind: "unreachable", Prefix: v4.String(), Table: 254, Metric: 42760},
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: 2},
	})
	if released, err = manager.RegisteredRestoration(context.Background(), "primary", expected); err != nil || released != 0 {
		t.Fatalf("a foreign-protocol row must not release a park: %d %v", released, err)
	}

	// The exact expected row releases it.
	manager = park([]routing.Operation{
		{Kind: "unreachable", Prefix: v4.String(), Table: 254, Metric: 42760},
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
	})
	// The park exists in the manager state: the withdrawal path installed
	// it before the transport came back.
	if err := manager.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{v4}); err != nil {
		t.Fatal(err)
	}
	released, err = manager.RegisteredRestoration(context.Background(), "primary", expected)
	if err != nil || released != 1 {
		t.Fatalf("the exact expected row releases the park: %d %v", released, err)
	}
	if manager.Snapshot("primary").Active {
		t.Fatal("the park state must be empty after the release")
	}
}

func TestRegisteredRestorationKeepsParksWithoutActiveRegistryState(t *testing.T) {
	v4 := netip.MustParsePrefix("203.0.113.10/32")
	fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{
		{Kind: "unreachable", Prefix: v4.String(), Table: 254, Metric: 42760},
		{Kind: "route", Prefix: v4.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: staticProtocol},
	}}}
	manager := NewManager(fake)
	_ = manager.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{v4})
	// The registry says the route is still Pending, not Active: no release.
	expected := []routing.SelectedRoute{
		{Role: "primary", Zone: "primary", Prefix: v4, Source: routing.SelectedDNS, IfIndex: 7, State: routing.SelectedPending},
	}
	released, err := manager.RegisteredRestoration(context.Background(), "primary", expected)
	if err != nil || released != 0 {
		t.Fatalf("a pending ownership record must keep the park: %d %v", released, err)
	}
}
