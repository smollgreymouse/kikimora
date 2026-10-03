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
