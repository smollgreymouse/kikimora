package control

import (
	"context"
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
