package netns

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"net/netip"
	"testing"
)

func TestParkingIsReleasedOnlyForObservedPrefix(t *testing.T) {
	m := parking.NewManager(&routing.Fake{})
	p := netip.MustParsePrefix("203.0.113.8/32")
	if err := m.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{p}); err != nil {
		t.Fatal(err)
	}
	if m.ObserveRestoration(context.Background(), "primary", netip.MustParsePrefix("198.51.100.8/32")) {
		t.Fatal("released unrelated route")
	}
	if !m.Snapshot("primary").Active {
		t.Fatal("parking cleared too early")
	}
	m.ObserveRestoration(context.Background(), "primary", p)
	if m.Snapshot("primary").Active {
		t.Fatal("parking not released")
	}
}

func TestParkingReleasesPrefixesIndependently(t *testing.T) {
	m := parking.NewManager(&routing.Fake{})
	one := netip.MustParsePrefix("203.0.113.8/32")
	two := netip.MustParsePrefix("203.0.113.9/32")
	if err := m.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{one, two}); err != nil {
		t.Fatal(err)
	}
	if !m.ObserveRestoration(context.Background(), "primary", one) {
		t.Fatal("first route was not observed")
	}
	state := m.Snapshot("primary")
	if !state.Active || state.Count != 1 || len(state.Prefixes) != 1 || state.Prefixes[0] != two {
		t.Fatalf("released all parks at once: %#v", state)
	}
}

func TestParkingIgnoresNonHostAndEndpointRoutes(t *testing.T) {
	m := parking.NewManager(&routing.Fake{})
	if err := m.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParsePrefix("198.51.100.8/32"),
	}); err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot("primary")
	if state.Count != 1 || state.Prefixes[0].String() != "198.51.100.8/32" {
		t.Fatalf("invalid parking candidates accepted: %#v", state)
	}
}

func TestParkingCheckpointRestoresOnlyKernelVerifiedParks(t *testing.T) {
	fake := &routing.Fake{}
	m := parking.NewManager(fake)
	present := netip.MustParsePrefix("203.0.113.8/32")
	stale := netip.MustParsePrefix("203.0.113.9/32")
	_ = fake.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{{Kind: "park", Prefix: present.String(), Table: 254, Metric: 42760}}})
	if err := m.RestoreCheckpoint(context.Background(), parking.Checkpoint{Role: "primary", Parked: []parking.OwnedRoute{{Prefix: present}, {Prefix: stale}}}); err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot("primary")
	if !state.Active || state.Count != 1 || state.Prefixes[0] != present {
		t.Fatalf("stale checkpoint was restored: %#v", state)
	}
}

func TestParkingDerivesOnlyOwnedHostRoutesFromKernel(t *testing.T) {
	fake := &routing.Fake{}
	_ = fake.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{
		{Kind: "route", Prefix: "203.0.113.8/32", Table: 254, IfIndex: 7, Protocol: 4},
		{Kind: "route", Prefix: "203.0.113.0/24", Table: 254, IfIndex: 7, Protocol: 4},
		{Kind: "route", Prefix: "198.51.100.8/32", Table: 51890, IfIndex: 7, Protocol: 4},
		{Kind: "route", Prefix: "192.0.2.8/32", Table: 254, IfIndex: 8, Protocol: 4},
	}})
	m := parking.NewManager(fake)
	if err := m.PrepareWithdrawalFromKernel(context.Background(), "primary", 7); err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot("primary")
	if state.Count != 1 || state.Prefixes[0].String() != "203.0.113.8/32" {
		t.Fatalf("wrong kernel candidates: %#v", state)
	}
}

func TestParkingKernelReleaseNeedsWinningRoute(t *testing.T) {
	fake := &routing.Fake{}
	m := parking.NewManager(fake)
	p := netip.MustParsePrefix("203.0.113.8/32")
	if err := m.PrepareWithdrawal(context.Background(), "primary", []netip.Prefix{p}); err != nil {
		t.Fatal(err)
	}
	if released, err := m.ObserveRestorationFromKernel(context.Background(), "primary"); err != nil || released != 0 {
		t.Fatalf("park released without replacement: released=%d err=%v", released, err)
	}
	if err := fake.Apply(context.Background(), routing.Transaction{Operations: []routing.Operation{{Kind: "route", Prefix: p.String(), Table: 254, IfIndex: 7, Metric: 10, Protocol: 4}}}); err != nil {
		t.Fatal(err)
	}
	if released, err := m.ObserveRestorationFromKernel(context.Background(), "primary"); err != nil || released != 1 {
		t.Fatalf("winning route did not release park: released=%d err=%v", released, err)
	}
}
