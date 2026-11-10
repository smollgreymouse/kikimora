package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The D7R-2 checkpoint fencing matrix (08D1 post-D7 audit §6, companion §4):
// every checkpoint write carries the active schema2 activation fence, the
// startup restore compares the fence against the committed marker before any
// kernel mutation, and the local target drains exactly the empty
// current-activation checkpoint files.

// writeRoleCheckpoint stages a selected-routes.json for the test role.
func writeRoleCheckpoint(t *testing.T, dir string, checkpoint routing.SelectedRoutesCheckpoint) string {
	t.Helper()
	stateDir := filepath.Join(dir, "primary")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "selected-routes.json")
	if err := routing.WriteSelectedRoutesCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	return path
}

// fencedCheckpoint builds a valid checkpoint with an explicit fence. A nil
// entries list becomes an empty slice: the validator requires the field to be
// present, and an empty checkpoint is exactly what a drained role leaves.
func fencedCheckpoint(activationID string, generation uint64, entries []routing.SelectedRoute) routing.SelectedRoutesCheckpoint {
	if entries == nil {
		entries = []routing.SelectedRoute{}
	}
	return routing.SelectedRoutesCheckpoint{
		Schema:          routing.SelectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 4,
		ActivationID:    activationID,
		ModeGeneration:  generation,
		Entries:         entries,
		Platform:        routing.RuntimeCheckpointPlatform(),
	}
}

func ownedActiveEntry() routing.SelectedRoute {
	identity := routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}
	return routing.SelectedRoute{
		Role: "primary", Zone: "primary", Prefix: netip.MustParsePrefix("203.0.113.10/32"),
		Source: routing.SelectedDNS, ToadGeneration: 2, RouteGeneration: 4,
		Interface: "kkprimary", Desired: identity,
		Owned: &identity,
		State: routing.SelectedActive,
	}
}

// readMarkerActivation resolves the current marker fence of a staged
// managed directory.
func readMarkerActivation(t *testing.T, dir string) (string, uint64) {
	t.Helper()
	marker, found, err := readManagedMarker(filepath.Join(dir, "leshy-managed.json"))
	if err != nil || !found {
		t.Fatalf("marker: found=%v err=%v", found, err)
	}
	return marker.ActivationID, marker.ModeGeneration
}

// TestManagedEnsureWritesTheCurrentActivationFence is the production-path
// proof (companion §4): a real managed selected transaction writes the
// current marker fence into selected-routes.json.
func TestManagedEnsureWritesTheCurrentActivationFence(t *testing.T) {
	dir := markerDir(t, "/run/kk-test.sock")
	manager := managedManager(t, dir)
	defer manager.Close()
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	activation, generation := readMarkerActivation(t, dir)
	raw, err := os.ReadFile(filepath.Join(dir, "primary", "selected-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint routing.SelectedRoutesCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.ActivationID != activation || checkpoint.ModeGeneration != generation {
		t.Fatalf("the checkpoint must carry the current marker fence: got activation %q generation %d, want %q generation %d",
			checkpoint.ActivationID, checkpoint.ModeGeneration, activation, generation)
	}
}

// TestCheckpointRestoreRequiresTheMarkerFence is the §6.3 mandatory matrix:
// stale activation/generation, a previous activation of the same socket/zone
// and a cross-platform checkpoint fail startup before any mutation; the
// current fence restores.
func TestCheckpointRestoreRequiresTheMarkerFence(t *testing.T) {
	activation := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	foreign := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
	crossPlatform := routing.CheckpointPlatformWindows
	if routing.RuntimeCheckpointPlatform() == routing.CheckpointPlatformWindows {
		crossPlatform = routing.CheckpointPlatformLinux
	}
	cases := []struct {
		name       string
		checkpoint routing.SelectedRoutesCheckpoint
	}{
		{"stale activation", fencedCheckpoint(foreign, 4, []routing.SelectedRoute{ownedActiveEntry()})},
		{"stale mode generation", fencedCheckpoint(activation, 3, []routing.SelectedRoute{ownedActiveEntry()})},
		{"previous activation same socket and zone", fencedCheckpoint("cccccccccccccccccccccccccccccccc", 4, []routing.SelectedRoute{ownedActiveEntry()})},
		{"cross-platform checkpoint", func() routing.SelectedRoutesCheckpoint {
			cp := fencedCheckpoint(activation, 4, []routing.SelectedRoute{ownedActiveEntry()})
			cp.Platform = crossPlatform
			return cp
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, dir, stateDir := deferredManager(t)
			stageAuthKey(t, stateDir)
			if err := WriteCommittedModeGeneration(stateDir, 4); err != nil {
				t.Fatal(err)
			}
			writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
				Schema:         managedModeMarkerSchema,
				Socket:         "/run/kk-test.sock",
				CoreVersion:    "test",
				ManagedZones:   []string{"primary"},
				ActivationID:   activation,
				ModeGeneration: 4,
			})
			writeRoleCheckpoint(t, dir, tc.checkpoint)

			if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
				t.Fatalf("%s: the stale checkpoint must fail startup", tc.name)
			}
			// Zero mutation: the foreign checkpoint must still be on disk
			// exactly as staged, and no kernel row appeared.
			raw, err := os.ReadFile(filepath.Join(dir, "primary", "selected-routes.json"))
			if err != nil {
				t.Fatal(err)
			}
			var stored routing.SelectedRoutesCheckpoint
			if err := json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			if stored.ActivationID != tc.checkpoint.ActivationID || stored.Platform != tc.checkpoint.Platform {
				t.Fatalf("%s: the refused checkpoint must be untouched: %#v", tc.name, stored)
			}
			// Zero registry mutation: the refused restore must not seed any
			// ownership into the fresh manager.
			if rows := manager.selectedRoutes.RegistrySnapshot("primary"); len(rows) != 0 {
				t.Fatalf("%s: a refused restore must not mutate the registry: %#v", tc.name, rows)
			}
		})
	}

	t.Run("current activation restores", func(t *testing.T) {
		manager, dir, stateDir := deferredManager(t)
		stageAuthKey(t, stateDir)
		if err := WriteCommittedModeGeneration(stateDir, 4); err != nil {
			t.Fatal(err)
		}
		writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
			Schema:         managedModeMarkerSchema,
			Socket:         "/run/kk-test.sock",
			CoreVersion:    "test",
			ManagedZones:   []string{"primary"},
			ActivationID:   activation,
			ModeGeneration: 4,
		})
		writeRoleCheckpoint(t, dir, fencedCheckpoint(activation, 4, []routing.SelectedRoute{ownedActiveEntry()}))

		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("the current fence must restore: %v", err)
		}
		if !state.Managed || !manager.ManagedModeActive() {
			t.Fatalf("descriptor: %#v", state)
		}
		snapshot := manager.selectedRoutes.RegistrySnapshot("primary")
		if len(snapshot) != 1 || snapshot[0].State != routing.SelectedActive {
			t.Fatalf("the owned record must be restored: %#v", snapshot)
		}
	})
}

// TestDisableDrainsTheEmptyCheckpoint covers §6.4: a Disable removes the
// empty current-activation checkpoint file; a non-empty or foreign-fenced
// checkpoint fails the transaction closed with the journal still in place.
func TestDisableDrainsTheEmptyCheckpoint(t *testing.T) {
	t.Run("empty matching checkpoint is removed", func(t *testing.T) {
		manager, stateDir := transactionManager(t)
		if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
			t.Fatal(err)
		}
		activation, generation := readMarkerActivation(t, manager.configDir)
		path := writeRoleCheckpoint(t, manager.configDir, fencedCheckpoint(activation, generation, nil))
		if _, err := manager.DisableManagedLeshy(""); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the empty checkpoint must be drained: %v", err)
		}
		if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 2 {
			t.Fatalf("committed generation after disable: %d err=%v", generation, err)
		}
	})
	t.Run("non-empty checkpoint fails closed", func(t *testing.T) {
		manager, stateDir := transactionManager(t)
		if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
			t.Fatal(err)
		}
		activation, generation := readMarkerActivation(t, manager.configDir)
		path := writeRoleCheckpoint(t, manager.configDir, fencedCheckpoint(activation, generation, []routing.SelectedRoute{ownedActiveEntry()}))
		if _, err := manager.DisableManagedLeshy(""); err == nil {
			t.Fatal("a non-empty ownership checkpoint must fail the rollback")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the ownership evidence must survive the refused rollback: %v", err)
		}
		if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
			t.Fatalf("the journal must remain for recovery: found=%v err=%v", found, err)
		}
	})
	t.Run("foreign-fenced empty checkpoint fails closed", func(t *testing.T) {
		manager, _ := transactionManager(t)
		if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
			t.Fatal(err)
		}
		writeRoleCheckpoint(t, manager.configDir, fencedCheckpoint("dddddddddddddddddddddddddddddddd", 9, nil))
		if _, err := manager.DisableManagedLeshy(""); err == nil {
			t.Fatal("an empty checkpoint fenced to another activation must fail the rollback")
		}
	})
}

// TestLocalTargetRecoveryDrainsCheckpoints covers §6.5: the local-target
// recovery performs the same drain — missing is fine, matching empty is
// removed, anything else fails closed.
func TestLocalTargetRecoveryDrainsCheckpoints(t *testing.T) {
	activation := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	stage := func(t *testing.T, checkpoint routing.SelectedRoutesCheckpoint, malformed bool) (*Manager, string, string) {
		manager, dir, stateDir := deferredManager(t)
		stageAuthKey(t, stateDir)
		if err := WriteCommittedModeGeneration(stateDir, 5); err != nil {
			t.Fatal(err)
		}
		writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
			Schema:         managedModeMarkerSchema,
			Socket:         "/run/kk-test.sock",
			CoreVersion:    "test",
			ManagedZones:   []string{"primary"},
			ActivationID:   activation,
			ModeGeneration: 5,
		})
		stageManagedToLocalJournal(t, manager, stateDir, "/run/kk-test.sock", activation, 5)
		if malformed {
			stateDirPath := filepath.Join(dir, "primary")
			if err := os.MkdirAll(stateDirPath, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDirPath, "selected-routes.json"), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if checkpoint.Entries != nil || checkpoint.ActivationID != "" {
			writeRoleCheckpoint(t, dir, checkpoint)
		}
		return manager, dir, stateDir
	}

	t.Run("missing checkpoint recovers local", func(t *testing.T) {
		manager, dir, stateDir := stage(t, routing.SelectedRoutesCheckpoint{}, false)
		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("startup managed authority: %v", err)
		}
		if state.Managed || state.Generation != 6 {
			t.Fatalf("descriptor: %#v", state)
		}
		if _, err := os.Stat(filepath.Join(dir, "leshy-managed.json")); !os.IsNotExist(err) {
			t.Fatalf("the marker must be gone: %v", err)
		}
	})
	t.Run("matching empty checkpoint is drained", func(t *testing.T) {
		manager, dir, stateDir := stage(t, fencedCheckpoint(activation, 5, nil), false)
		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("startup managed authority: %v", err)
		}
		if state.Managed || state.Generation != 6 {
			t.Fatalf("descriptor: %#v", state)
		}
		if _, err := os.Stat(filepath.Join(dir, "primary", "selected-routes.json")); !os.IsNotExist(err) {
			t.Fatalf("the empty checkpoint must be drained: %v", err)
		}
	})
	t.Run("non-empty checkpoint fails closed", func(t *testing.T) {
		manager, dir, stateDir := stage(t, fencedCheckpoint(activation, 5, []routing.SelectedRoute{ownedActiveEntry()}), false)
		if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
			t.Fatal("non-empty ownership evidence must fail the rollback")
		}
		if _, err := os.Stat(filepath.Join(dir, "primary", "selected-routes.json")); err != nil {
			t.Fatalf("the ownership evidence must survive: %v", err)
		}
		if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
			t.Fatalf("the journal must remain: found=%v err=%v", found, err)
		}
	})
	t.Run("wrong fence fails closed", func(t *testing.T) {
		manager, _, stateDir := stage(t, fencedCheckpoint("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", 5, nil), false)
		if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
			t.Fatal("a checkpoint fenced to another activation must fail the rollback")
		}
	})
	t.Run("wrong generation fails closed", func(t *testing.T) {
		manager, _, stateDir := stage(t, fencedCheckpoint(activation, 4, nil), false)
		if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
			t.Fatal("a checkpoint fenced to another generation must fail the rollback")
		}
	})
	t.Run("malformed checkpoint fails closed", func(t *testing.T) {
		manager, _, stateDir := stage(t, routing.SelectedRoutesCheckpoint{}, true)
		if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
			t.Fatal("a malformed checkpoint must fail the rollback")
		}
	})
}

// TestWriteCheckpointRefusesAnUnfencedWrite proves the write-side gate: a
// SelectedRouteManager without a fence provider refuses to persist a
// checkpoint at all.
func TestWriteCheckpointRefusesAnUnfencedWrite(t *testing.T) {
	dir := t.TempDir()
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		nil,
		func(string) (core.RoleRuntime, bool) { return core.RoleRuntime{}, false },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return dir, true },
	)
	if err := manager.writeCheckpoint("primary", 1); err == nil {
		t.Fatal("a write without an activation fence must be refused")
	}
	manager.SetActivationFence(testActivationFence)
	manager.SetManagedRoleCheck(func(string) bool { return true })
	if err := manager.writeCheckpoint("primary", 1); err != nil {
		t.Fatalf("the fenced write must land: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "selected-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint routing.SelectedRoutesCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.ActivationID != "0123456789abcdef0123456789abcdef" || checkpoint.ModeGeneration != 1 {
		t.Fatalf("the injected fence must be written: %#v", checkpoint)
	}
}

// TestDrainRequiresTheSelectedRouteManager keeps the nil-manager guard
// visible: draining without a selected-route manager is a no-op, never a
// panic.
func TestDrainRequiresTheSelectedRouteManager(t *testing.T) {
	manager, _, _ := deferredManager(t)
	transition, err := NewManagedLeshyTransition(ManagedLeshyManaged, ManagedLeshyLocal,
		"/run/kk-test.sock", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "", 5, []string{"primary"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.drainEmptySelectedCheckpoints(transition); err != nil {
		t.Fatalf("a nil selected-route manager must skip the drain: %v", err)
	}
}
