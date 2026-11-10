package control

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The D7R-3 matrix (08D1 post-D7 audit §7, companion §10/§11): the transition
// gate is real, the Enable/Disable preconditions are strict, and the target
// verification compares every journal field.

func TestEnableWhileManagedIsRefusedWithZeroMutation(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
		t.Fatal(err)
	}
	before, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err == nil {
		t.Fatal("an enable while already managed must be refused")
	} else if !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("the refusal must be explicit: %v", err)
	}
	after, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if after.ActivationID != before.ActivationID || after.ModeGeneration != before.ModeGeneration {
		t.Fatalf("the refused enable must not touch the marker: %#v vs %#v", after, before)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 1 {
		t.Fatalf("the refused enable must not move the generation: %d err=%v", generation, err)
	}
	if _, found, _ := ReadTransitionJournal(stateDir); found {
		t.Fatal("the refused enable must not leave a journal")
	}
	configAfter, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(configBefore) != string(configAfter) {
		t.Fatal("the refused enable must not rewrite the config")
	}
}

func TestEnableWithAStaleSelectedCheckpointIsRefused(t *testing.T) {
	manager, stateDir := transactionManager(t)
	// A leftover checkpoint from an earlier managed era.
	writeRoleCheckpoint(t, manager.configDir, fencedCheckpoint("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", 4, nil))
	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err == nil {
		t.Fatal("an enable with a stale selected checkpoint must be refused")
	}
	if _, found, _ := ReadTransitionJournal(stateDir); found {
		t.Fatal("the refused enable must not leave a journal")
	}
	if _, err := os.Stat(filepath.Join(manager.configDir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatal("the refused enable must not write the marker")
	}
}

func TestDisableWithoutACommittedMarkerIsRefused(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if _, err := manager.DisableManagedLeshy(""); err == nil {
		t.Fatal("a disable without a committed marker must be refused")
	} else if !strings.Contains(err.Error(), "not committed") {
		t.Fatalf("the refusal must be explicit: %v", err)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 0 {
		t.Fatalf("the refused disable must not move the generation: %d err=%v", generation, err)
	}
}

func TestDisablePreconditionsCoverTheSelectedRegistry(t *testing.T) {
	cases := []struct {
		name    string
		stage   func(t *testing.T, manager *Manager)
		message string
	}{
		{"active registry entry", func(t *testing.T, manager *Manager) {
			if err := manager.selectedRoutes.registry.Reserve("primary", "primary",
				netip.MustParsePrefix("203.0.113.10/32"), routing.SelectedDNS, 1, 1, "kkprimary",
				routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}); err != nil {
				t.Fatal(err)
			}
		}, "selected registry records"},
		{"park transaction on a record", func(t *testing.T, manager *Manager) {
			prefix := netip.MustParsePrefix("203.0.113.10/32")
			if err := manager.selectedRoutes.registry.Reserve("primary", "primary",
				prefix, routing.SelectedDNS, 1, 1, "kkprimary",
				routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}); err != nil {
				t.Fatal(err)
			}
			if err := manager.selectedRoutes.registry.ReservePark("primary", prefix,
				routing.SelectedParkIdentity{Kind: routing.SelectedParkUnreachable, Table: routing.ManagedSelectedRouteTable, Protocol: 4, Metric: routing.ManagedParkMetric}, false); err != nil {
				t.Fatal(err)
			}
		}, "park transaction"},
		{"in-flight mutation", func(t *testing.T, manager *Manager) {
			manager.selectedRoutes.mu.Lock()
			state := manager.selectedRoutes.roleState("primary")
			manager.selectedRoutes.beginFlight(state)
			manager.selectedRoutes.mu.Unlock()
		}, "in flight"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, stateDir := transactionManager(t)
			if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
				t.Fatal(err)
			}
			markerBefore, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
			if err != nil {
				t.Fatal(err)
			}
			tc.stage(t, manager)

			if _, err := manager.DisableManagedLeshy(""); err == nil {
				t.Fatalf("%s: the disable must be refused", tc.name)
			} else if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("%s: wrong refusal: %v", tc.name, err)
			}
			markerAfter, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
			if err != nil {
				t.Fatal(err)
			}
			if markerAfter.ActivationID != markerBefore.ActivationID || markerAfter.ModeGeneration != markerBefore.ModeGeneration {
				t.Fatalf("%s: the refused disable must not touch the marker", tc.name)
			}
			if _, found, _ := ReadTransitionJournal(stateDir); found {
				t.Fatalf("%s: the refused disable must not leave a journal", tc.name)
			}
		})
	}
}

func TestTransitionGateDeniesRouteMutations(t *testing.T) {
	dir := markerDir(t, "/run/kk-test.sock")
	manager := managedManager(t, dir)
	defer manager.Close()
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}

	// Gate armed: every route mutation denies with zero writer calls.
	manager.mu.Lock()
	manager.modeTransitionInFlight = true
	manager.mu.Unlock()
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err == nil || !strings.Contains(err.Error(), "transition is in flight") {
		t.Fatalf("an active gate must deny the ensure: %v", err)
	}
	if err := manager.selectedRoutes.Forget(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err == nil || !strings.Contains(err.Error(), "transition is in flight") {
		t.Fatalf("an active gate must deny the forget: %v", err)
	}
	if err := manager.selectedRoutes.FlushZone(context.Background(), "primary"); err == nil || !strings.Contains(err.Error(), "transition is in flight") {
		t.Fatalf("an active gate must deny the flush: %v", err)
	}
	if fake.reconcileCount() != 0 || len(fake.removes) != 0 {
		t.Fatalf("a gated mutation must not touch the writer: reconciles=%d removes=%d", fake.reconcileCount(), len(fake.removes))
	}

	// Gate cleared: the same intents go through.
	manager.mu.Lock()
	manager.modeTransitionInFlight = false
	manager.mu.Unlock()
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatalf("a cleared gate must admit the ensure: %v", err)
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("the ungated ensure must mutate the kernel once: %d", fake.reconcileCount())
	}
}

func TestEnableFailureAfterJournalCreationLeavesTheGateArmed(t *testing.T) {
	manager, stateDir := transactionManager(t)
	// The config write is where the first post-journal mutation lands; block
	// it by making the config path a directory.
	blocked := filepath.Join(filepath.Dir(manager.leshyConfigPath), "config.toml")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	manager.leshyConfigPath = blocked

	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err == nil {
		t.Fatal("the blocked config write must fail the enable")
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
		t.Fatalf("the journal must survive the failed enable: found=%v err=%v", found, err)
	}
	if !manager.modeTransitionGateActive() {
		t.Fatal("a failure after journal creation must leave the live core fail-closed")
	}

	// The restart recovers the journal and re-arms the committed state. The
	// blocked path is the test's write failure, not a durable condition — a
	// real restart writes through the normal path.
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverManagedModeTransition(); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if manager.modeTransitionGateActive() {
		t.Fatal("the gate must clear once the recovery removed the journal")
	}
}

// stageCommittedManagedTarget stages the §7.5 "committed == To Managed" state:
// generation 1 committed, the exact target marker on disk, the exact target
// config and the journal awaiting verify-and-clean.
func stageCommittedManagedTarget(t *testing.T, activation string) (*Manager, string, string) {
	t.Helper()
	manager, dir, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	server, err := manager.buildLeshyServer(manager.leshyConfigPath, "/run/kk-test.sock", activation, 1)
	if err != nil {
		t.Fatal(err)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := config.RenderLeshyConfig(server, zones)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.leshyConfigPath, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		"/run/kk-test.sock", "", activation, 0, manager.managedZoneNames(), configDigest(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	if err := WriteCommittedModeGeneration(stateDir, 1); err != nil {
		t.Fatal(err)
	}
	writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
		Schema:         managedModeMarkerSchema,
		Socket:         "/run/kk-test.sock",
		CoreVersion:    "test",
		ManagedZones:   manager.managedZoneNames(),
		ActivationID:   activation,
		ModeGeneration: 1,
	})
	return manager, dir, stateDir
}

func corruptMarkerField(t *testing.T, dir string, mutate func(*managedModeMarker)) {
	t.Helper()
	marker, _, err := readManagedMarker(filepath.Join(dir, "leshy-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	mutate(marker)
	writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), *marker)
}

func TestCommittedManagedTargetVerifiesEveryJournalField(t *testing.T) {
	activation := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	mutations := []struct {
		name   string
		mutate func(*managedModeMarker)
	}{
		{"foreign socket", func(m *managedModeMarker) { m.Socket = "/run/other.sock" }},
		{"foreign zones", func(m *managedModeMarker) { m.ManagedZones = []string{"other"} }},
		{"foreign activation", func(m *managedModeMarker) { m.ActivationID = "cccccccccccccccccccccccccccccccc" }},
		{"foreign generation", func(m *managedModeMarker) { m.ModeGeneration = 2 }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			manager, dir, stateDir := stageCommittedManagedTarget(t, activation)
			corruptMarkerField(t, dir, tc.mutate)
			if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
				t.Fatalf("%s: the corrupted marker must fail the target verification", tc.name)
			}
			if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
				t.Fatalf("%s: the journal must be retained: found=%v err=%v", tc.name, found, err)
			}
		})
	}

	t.Run("exact target verifies and converges", func(t *testing.T) {
		manager, _, stateDir := stageCommittedManagedTarget(t, activation)
		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("the exact target must verify: %v", err)
		}
		if !state.Managed || state.Generation != 1 {
			t.Fatalf("descriptor: %#v", state)
		}
		if _, found, _ := ReadTransitionJournal(stateDir); found {
			t.Fatal("the journal must be removed after verification")
		}
	})
}

func TestCommittedLocalTargetRefusesASurvivingMarker(t *testing.T) {
	manager, dir, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	server, err := manager.buildLeshyServer(manager.leshyConfigPath, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := config.RenderLeshyConfig(server, zones)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.leshyConfigPath, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	transition, err := NewManagedLeshyTransition(ManagedLeshyManaged, ManagedLeshyLocal,
		"/run/kk-test.sock", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "", 5, manager.managedZoneNames(), configDigest(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	if err := WriteCommittedModeGeneration(stateDir, 6); err != nil {
		t.Fatal(err)
	}
	// The rollback crashed before the marker removal: the verify-and-clean
	// path must refuse instead of accepting a managed marker next to a
	// committed local target.
	writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
		Schema:         managedModeMarkerSchema,
		Socket:         "/run/kk-test.sock",
		CoreVersion:    "test",
		ManagedZones:   []string{"primary"},
		ActivationID:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
		ModeGeneration: 5,
	})
	if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
		t.Fatal("a surviving marker must fail the local target verification")
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
		t.Fatalf("the journal must be retained: found=%v err=%v", found, err)
	}
}

// TestSettleGateRequiresJournalAbsence keeps the §7.1 clearing rule visible:
// a journal read error never clears the gate.
func TestSettleGateRequiresJournalAbsence(t *testing.T) {
	manager, _, stateDir := deferredManager(t)
	manager.activateModeTransitionGate()
	if !manager.modeTransitionGateActive() {
		t.Fatal("the gate must be active after arming")
	}
	// A corrupt journal cannot be read: the gate stays armed.
	if err := os.WriteFile(TransitionJournalPath(stateDir), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.settleModeTransitionGate()
	if !manager.modeTransitionGateActive() {
		t.Fatal("a journal read error must keep the gate armed")
	}
	if _, err := manager.RecoverManagedModeTransition(); err == nil {
		t.Fatal("a corrupt journal must fail recovery")
	}
}
