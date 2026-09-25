package control

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

func TestRecoveryOwnershipUsesBaselineObservedDelta(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name: "one",
		Interface: state.InterfaceState{
			Name:    "kkone",
			IfIndex: 7,
			MTU:     1380,
		},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	userPrefix := "192.0.2.10/32"
	selectedPrefix := "198.51.100.20/32"
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{{
			Kind: "route", Prefix: userPrefix, Table: 254, IfIndex: 7, Metric: 10, Protocol: 4,
		}}},
	}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	role, _, err := driver.role("one")
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.captureOwnershipBaseline(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}

	fake.State.Routes = append(fake.State.Routes, routing.Operation{
		Kind: "route", Prefix: selectedPrefix, Table: 254, IfIndex: 7, Metric: 20, Protocol: 4,
	})
	if err := driver.captureOwnedDelta(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}

	owned := driver.ownership.Snapshot("one")
	if len(owned) != 1 || owned[0].Prefix.String() != selectedPrefix {
		t.Fatalf("baseline route was captured as owned or selected delta was lost: %#v", owned)
	}

	checkpoint, err := parking.ReadCheckpoint(filepath.Join(dir, "one", "parking.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Baseline) != 1 || checkpoint.Baseline[0].Prefix.String() != userPrefix {
		t.Fatalf("checkpoint lost baseline ownership evidence: %#v", checkpoint.Baseline)
	}
	if len(checkpoint.Observed) != 1 || checkpoint.Observed[0].Prefix.String() != selectedPrefix {
		t.Fatalf("checkpoint did not persist observed owned delta: %#v", checkpoint.Observed)
	}
}

func TestRecoveryCheckpointRebindsStaleIdentityWithoutTransferringOwnership(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name:      "one",
		Interface: state.InterfaceState{Name: "kkone", IfIndex: 9, MTU: 1380},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	prefix := netip.MustParsePrefix("198.51.100.20/32")
	path := filepath.Join(dir, "one", "parking.json")
	if err := parking.WriteCheckpoint(path, parking.Checkpoint{
		Role:      "one",
		Interface: "kkone",
		IfIndex:   7,
		Baseline: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32"),
		}},
		Observed: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fake := &routing.Fake{}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	if err := driver.ObserveRoutes(context.Background(), "one"); err != nil {
		t.Fatalf("stale inactive checkpoint should be rebound, got %v", err)
	}
	if got := driver.ownership.Snapshot("one"); len(got) != 0 {
		t.Fatalf("stale ownership crossed ifindex change: %#v", got)
	}
	driver.mu.Lock()
	baseline := append([]parking.OwnedRoute(nil), driver.baselines["one"]...)
	driver.mu.Unlock()
	if len(baseline) != 0 {
		t.Fatalf("stale baseline crossed ifindex change: %#v", baseline)
	}

	checkpoint, err := parking.ReadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Interface != "kkone" || checkpoint.IfIndex != 9 {
		t.Fatalf("checkpoint identity not rebound: %#v", checkpoint)
	}
	if len(checkpoint.Baseline) != 0 || len(checkpoint.Observed) != 0 || len(checkpoint.Parked) != 0 {
		t.Fatalf("stale ownership evidence survived rebind: %#v", checkpoint)
	}
}

func TestRecoveryCheckpointPreservesVerifiedParkAcrossIdentityChange(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name:      "one",
		Interface: state.InterfaceState{Name: "kkone", IfIndex: 9, MTU: 1380},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	prefix := netip.MustParsePrefix("198.51.100.20/32")
	path := filepath.Join(dir, "one", "parking.json")
	if err := parking.WriteCheckpoint(path, parking.Checkpoint{
		Role:      "one",
		Interface: "kkone",
		IfIndex:   7,
		Observed: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
		Parked: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{{
		Kind: "park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: 4,
	}}}}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	if err := driver.ObserveRoutes(context.Background(), "one"); err != nil {
		t.Fatalf("restore stale active checkpoint: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); !got.Active || got.Count != 1 {
		t.Fatalf("verified fail-closed park was not restored: %#v", got)
	}

	if err := driver.Park(context.Background(), "one"); err != nil {
		t.Fatalf("reuse verified park: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); !got.Active || got.Count != 1 {
		t.Fatalf("existing park was cleared by empty new ownership: %#v", got)
	}

	checkpoint, err := parking.ReadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.IfIndex != 9 || checkpoint.Interface != "kkone" || len(checkpoint.Parked) != 1 {
		t.Fatalf("active checkpoint not rebound to current route target: %#v", checkpoint)
	}
	if checkpoint.Parked[0].IfIndex != 9 || checkpoint.Parked[0].Prefix != prefix {
		t.Fatalf("parked prefix not normalized to current identity: %#v", checkpoint.Parked)
	}
	if len(checkpoint.Observed) != 0 || len(checkpoint.Baseline) != 0 {
		t.Fatalf("stale ownership evidence survived active-park rebind: %#v", checkpoint)
	}

	fake.State.Routes = append(fake.State.Routes, routing.Operation{
		Kind: "route", Prefix: prefix.String(), Table: 254, IfIndex: 9, Metric: 10, Protocol: 4,
	})
	if err := driver.ObserveRestoration(context.Background(), "one"); err != nil {
		t.Fatalf("restoration after checkpoint rebind: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); got.Active || got.Count != 0 {
		t.Fatalf("verified park did not release after real route restoration: %#v", got)
	}
}
