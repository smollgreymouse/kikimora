package control

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// The D7R committed-state matrix (08D1 post-D7 audit §5, companion coverage
// audit §1-§3): every test reconstructs the production startup from a staged
// durable crash state — a fresh deferred Manager, journal recovery, a fresh
// committed load, the restore — and proves the final mode, generation and
// config identity. The stale pre-recovery marker cache is gone; these tests
// are the regression proof.

// deferredManager builds a production-style deferred Manager over one temp
// directory: configDir == dir, an explicit leshy config path inside dir and a
// state subdirectory. The manager keeps its monitor goroutine stopped; tests
// close it through the cleanup.
func deferredManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "primary", "openconnect")
	manager, err := NewManagerWithLegacyAndDeps(dir, []string{configPath}, &fakeLauncher{}, "", "", "", managerDependencies{
		restorePolicy: DeferManagedRestore,
	})
	if err != nil {
		t.Fatalf("new deferred manager: %v", err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.SetStateDir(stateDir)
	// The generated config lives in the temp tree; the writer lock stays at
	// the platform default so the config gate compares identical paths.
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")
	return manager, dir, stateDir
}

// stageAuthKey pre-creates the privileged credential: a crashed enable ran on
// a core whose startup already created it (EnsurePrivilegedAuthKey fails
// closed for a managed install without one). The canonical bytes are written
// directly — the hardened-DACL writer locks the file to SYSTEM/Administrators,
// which a non-elevated test process cannot read back; the VM matrices cover
// the real DACL path.
func stageAuthKey(t *testing.T, stateDir string) {
	t.Helper()
	raw := make([]byte, privilegedAuthKeyBytes)
	for i := range raw {
		raw[i] = byte(i + 3)
	}
	encoded := strings.ToLower(hex.EncodeToString(raw)) + "\n"
	if err := os.WriteFile(PrivilegedAuthKeyPath(stateDir), []byte(encoded), 0o600); err != nil {
		t.Fatalf("stage the privileged auth key: %v", err)
	}
}

// stageLocalToManagedJournal stages the crash state of an Enable that died
// after the journal write: committed generation 0, no marker, journal with
// the digest of the config the roll-forward must re-render.
func stageLocalToManagedJournal(t *testing.T, manager *Manager, stateDir, socket, activationID string, from uint64) {
	t.Helper()
	server, err := manager.buildLeshyServer(manager.leshyConfigPath, socket, activationID, from+1)
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
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		socket, "", activationID, from, manager.managedZoneNames(), configDigest(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
}

// stageManagedToLocalJournal stages the crash state of a Disable that died
// before the marker removal: the old managed marker is still on disk and the
// journal names the local target.
func stageManagedToLocalJournal(t *testing.T, manager *Manager, stateDir, socket, fromActivationID string, from uint64) {
	t.Helper()
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
	transition, err := NewManagedLeshyTransition(ManagedLeshyManaged, ManagedLeshyLocal,
		socket, fromActivationID, "", from, manager.managedZoneNames(), configDigest(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
}

func writeMarkerFile(t *testing.T, path string, marker managedModeMarker) {
	t.Helper()
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDeferredProductionRestoreReadsMarkerAfterRecoveryEnable is the §7.5
// "Enable crash before marker exists" case: the deferred constructor sees no
// marker, the journal roll-forward creates one, and the fresh load/restore
// arms the managed state with the new activation.
func TestDeferredProductionRestoreReadsMarkerAfterRecoveryEnable(t *testing.T) {
	manager, _, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	activationID := "0123456789abcdef0123456789abcdef"
	socket := "/run/kikimora/core.sock"
	stageLocalToManagedJournal(t, manager, stateDir, socket, activationID, 0)

	if manager.ManagedModeActive() {
		t.Fatal("the deferred constructor must not arm managed state before recovery")
	}

	state, err := manager.StartupManagedAuthority(stateDir)
	if err != nil {
		t.Fatalf("startup managed authority: %v", err)
	}
	if !state.Managed || state.Marker == nil {
		t.Fatalf("the roll-forward must produce a managed descriptor: %#v", state)
	}
	if state.Generation != 1 || state.Marker.ActivationID != activationID || state.Marker.Socket != socket {
		t.Fatalf("descriptor after roll-forward: %#v", state)
	}
	if !manager.ManagedModeActive() {
		t.Fatal("the restored state must arm managed mode")
	}
	snapshot := manager.Snapshot()
	if snapshot.ModeGeneration != 1 {
		t.Fatalf("generation after restore: %d", snapshot.ModeGeneration)
	}
	if snapshot.ActivationIDFingerprint != activationIDFingerprint(activationID) {
		t.Fatalf("activation fingerprint after restore: %q", snapshot.ActivationIDFingerprint)
	}
	if !manager.managedSelected("primary") {
		t.Fatal("the marker-managed role must be armed after restore")
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || found {
		t.Fatalf("journal must be gone after recovery: found=%v err=%v", found, err)
	}
	data, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`routing_backend = "kikimora"`,
		`kikimora_socket = "` + socket + `"`,
		`kikimora_activation_id = "` + activationID + `"`,
		"kikimora_mode_generation = 1",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated config misses %q:\n%s", want, data)
		}
	}
}

// TestDeferredProductionRestoreReadsAbsenceAfterRecoveryDisable is the §7.5
// "Disable crash while old marker still exists" case: the deferred
// constructor must not cache the old marker, the recovery removes it, and the
// fresh load stays local — the stale managed state is never reapplied.
func TestDeferredProductionRestoreReadsAbsenceAfterRecoveryDisable(t *testing.T) {
	manager, dir, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	oldActivation := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	socket := "/run/kikimora/core.sock"
	if err := WriteCommittedModeGeneration(stateDir, 5); err != nil {
		t.Fatal(err)
	}
	writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
		Schema:         managedModeMarkerSchema,
		Socket:         socket,
		CoreVersion:    "test",
		ManagedZones:   []string{"primary"},
		ActivationID:   oldActivation,
		ModeGeneration: 5,
	})
	stageManagedToLocalJournal(t, manager, stateDir, socket, oldActivation, 5)

	if manager.ManagedModeActive() {
		t.Fatal("the deferred constructor must not apply the marker it saw before recovery")
	}

	state, err := manager.StartupManagedAuthority(stateDir)
	if err != nil {
		t.Fatalf("startup managed authority: %v", err)
	}
	if state.Managed {
		t.Fatalf("the rollback must produce a local descriptor: %#v", state)
	}
	if state.Generation != 6 {
		t.Fatalf("generation after the rollback: %d", state.Generation)
	}
	if manager.ManagedModeActive() {
		t.Fatal("the stale managed marker must never be reapplied")
	}
	snapshot := manager.Snapshot()
	if snapshot.ManagedMode || snapshot.ModeGeneration != 6 || snapshot.ActivationIDFingerprint != "" {
		t.Fatalf("snapshot after the rollback: %#v", snapshot)
	}
	if _, err := os.Stat(filepath.Join(dir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatalf("the marker must be gone after the rollback: %v", err)
	}
	data, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"routing_backend", "kikimora_socket", "kikimora_activation_id", "kikimora_mode_generation"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("local config still carries %q:\n%s", forbidden, data)
		}
	}
}

// TestCommittedMarkerGenerationMustEqualCommittedCounter is the P0-2 gate: a
// marker whose generation differs from the committed counter fails startup in
// both directions.
func TestCommittedMarkerGenerationMustEqualCommittedCounter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		markerGen uint64
		committed uint64
	}{
		{"marker older than committed", 3, 4},
		{"marker newer than committed", 5, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, dir, stateDir := deferredManager(t)
			stageAuthKey(t, stateDir)
			if err := WriteCommittedModeGeneration(stateDir, tc.committed); err != nil {
				t.Fatal(err)
			}
			writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
				Schema:         managedModeMarkerSchema,
				Socket:         "/run/kikimora/core.sock",
				CoreVersion:    "test",
				ManagedZones:   []string{"primary"},
				ActivationID:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
				ModeGeneration: tc.markerGen,
			})
			if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
				t.Fatal("a marker generation that contradicts the committed counter must fail startup")
			}
		})
	}
}

// TestCommittedLocalDescriptorRecordsTheCommittedGeneration: no marker means
// local mode at the committed generation, and the diagnostics carry it.
func TestCommittedLocalDescriptorRecordsTheCommittedGeneration(t *testing.T) {
	manager, _, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	if err := WriteCommittedModeGeneration(stateDir, 4); err != nil {
		t.Fatal(err)
	}
	state, err := manager.StartupManagedAuthority(stateDir)
	if err != nil {
		t.Fatalf("startup managed authority: %v", err)
	}
	if state.Managed || state.Generation != 4 {
		t.Fatalf("descriptor: %#v", state)
	}
	snapshot := manager.Snapshot()
	if snapshot.ManagedMode || snapshot.ModeGeneration != 4 || snapshot.ActivationIDFingerprint != "" {
		t.Fatalf("snapshot: %#v", snapshot)
	}
}

// TestCorruptGenerationFileFailsStartup: an unreadable committed counter must
// fail startup instead of being reported as a warning (P0-2).
func TestCorruptGenerationFileFailsStartup(t *testing.T) {
	manager, _, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	if err := os.WriteFile(ModeGenerationPath(stateDir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
		t.Fatal("a corrupt committed generation must fail startup")
	}
}

// TestStartupConfigGateRewritesStaleConfigs covers the §5.5 rewrite cases: a
// managed descriptor repairs a stale local config, and a local descriptor
// strips managed-only fields from a stale managed config — both before
// readiness.
func TestStartupConfigGateRewritesStaleConfigs(t *testing.T) {
	t.Run("managed descriptor over stale local config", func(t *testing.T) {
		manager, dir, stateDir := deferredManager(t)
		stageAuthKey(t, stateDir)
		if err := WriteCommittedModeGeneration(stateDir, 2); err != nil {
			t.Fatal(err)
		}
		activation := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
		socket := "/run/kikimora/core.sock"
		writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
			Schema:         managedModeMarkerSchema,
			Socket:         socket,
			CoreVersion:    "test",
			ManagedZones:   []string{"primary"},
			ActivationID:   activation,
			ModeGeneration: 2,
		})
		staleLocal := "# stale local config\n[server]\nlisten_address = \"127.0.0.1:53053\"\n"
		if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manager.leshyConfigPath, []byte(staleLocal), 0o644); err != nil {
			t.Fatal(err)
		}
		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("startup managed authority: %v", err)
		}
		if !state.Managed {
			t.Fatalf("descriptor: %#v", state)
		}
		data, err := os.ReadFile(manager.leshyConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `kikimora_activation_id = "`+activation+`"`) {
			t.Fatalf("the managed gate must rewrite the stale local config:\n%s", data)
		}
	})
	t.Run("local descriptor over stale managed config", func(t *testing.T) {
		manager, _, stateDir := deferredManager(t)
		stageAuthKey(t, stateDir)
		if err := WriteCommittedModeGeneration(stateDir, 3); err != nil {
			t.Fatal(err)
		}
		staleManaged := `[server]
listen_address = "127.0.0.1:53053"
routing_backend = "kikimora"
kikimora_socket = "/run/kikimora/core.sock"
writer_lock = "` + config.LeshyWriterLockPath() + `"
kikimora_auth_file = "C:/gone.key"
kikimora_activation_id = "cccccccccccccccccccccccccccccccc"
kikimora_mode_generation = 2
`
		if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manager.leshyConfigPath, []byte(staleManaged), 0o644); err != nil {
			t.Fatal(err)
		}
		state, err := manager.StartupManagedAuthority(stateDir)
		if err != nil {
			t.Fatalf("startup managed authority: %v", err)
		}
		if state.Managed {
			t.Fatalf("descriptor: %#v", state)
		}
		server, err := config.ReadLeshyServerStrict(manager.leshyConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if server.RoutingBackend != "" || server.KikimoraSocket != "" || server.KikimoraActivationID != "" || server.KikimoraModeGeneration != 0 {
			t.Fatalf("the local gate must strip the managed fields: %#v", server)
		}
	})
}

// TestStartupConfigGateFailsClosedOnWriteFailure: a config write failure must
// return a startup error — never serve a managed install whose generated
// config could not be produced.
func TestStartupConfigGateFailsClosedOnWriteFailure(t *testing.T) {
	manager, dir, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	if err := WriteCommittedModeGeneration(stateDir, 2); err != nil {
		t.Fatal(err)
	}
	writeMarkerFile(t, filepath.Join(dir, "leshy-managed.json"), managedModeMarker{
		Schema:         managedModeMarkerSchema,
		Socket:         "/run/kikimora/core.sock",
		CoreVersion:    "test",
		ManagedZones:   []string{"primary"},
		ActivationID:   "dddddddddddddddddddddddddddddddd",
		ModeGeneration: 2,
	})
	// The atomic writer creates missing parent directories, so the failure is
	// staged where the rename lands: the config path itself is a directory.
	blocked := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	manager.leshyConfigPath = blocked
	if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
		t.Fatal("a managed config write failure must fail startup before readiness")
	}
}

// TestStartupAuthorityRefusesAJournalWithoutMutation: recovery failures keep
// the durable state exactly as staged (fail closed, zero mutation).
func TestStartupAuthorityRefusesAJournalWithoutMutation(t *testing.T) {
	manager, dir, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	// committed 5 + a journal claiming from=0: matches neither generation.
	if err := WriteCommittedModeGeneration(stateDir, 5); err != nil {
		t.Fatal(err)
	}
	stageLocalToManagedJournal(t, manager, stateDir, "/run/kikimora/core.sock",
		"0123456789abcdef0123456789abcdef", 0)
	if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
		t.Fatal("a stale journal must fail startup")
	}
	if _, err := os.Stat(filepath.Join(dir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatal("a refused startup must not write the marker")
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 5 {
		t.Fatalf("a refused startup must not move the generation: %d err=%v", generation, err)
	}
}
