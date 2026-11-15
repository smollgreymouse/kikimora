package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// mustStateRootDigest renders the generated Leshy config the way recovery will
// for a given state root and returns its digest. Kept separate so a failing
// assertion can report both digests side by side.
func mustStateRootDigest(t *testing.T, manager *Manager, stateDir, socket, activationID string, generation uint64) string {
	t.Helper()
	manager.SetStateDir(stateDir)
	server, err := manager.buildLeshyServer(manager.leshyConfigPath, socket, activationID, generation)
	if err != nil {
		t.Fatalf("render server for %s: %v", stateDir, err)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatalf("render zones for %s: %v", stateDir, err)
	}
	rendered, err := config.RenderLeshyConfig(server, zones)
	if err != nil {
		t.Fatalf("render config for %s: %v", stateDir, err)
	}
	return configDigest(rendered)
}

// TestRecoverManagedModeTransitionAcrossStateRootMove pins the interaction
// between the schema2 mode-transition journal and an R1-style core-state move
// (state -> state\core on Windows, 08D1 R1 §3.1).
//
// Why this test exists: the journal pins ConfigSHA256 over the exact generated
// Leshy config bytes, and that config embeds the privileged credential path
// derived from the state directory (config_api.go PrivilegedAuthKeyPath). A
// migration that relocates the authority root therefore changes the rendered
// bytes. Until 2026-11-15 nothing in the suite exercised that combination:
// TestWindowsCoreStateMigrationCopiesInFlightTransition asserts only that the
// journal FILE is copied, and its transition literal carries no config_sha256.
//
// The test does not prescribe the product decision. It records which behaviour
// the current code has, so either resolution becomes a deliberate change:
//
//   - if recovery converges, the fence is effectively root-independent and the
//     in-flight upgrade row can be satisfied;
//   - if recovery refuses with a digest mismatch, an interrupted Enable cannot
//     survive a supported state-layout upgrade without operator action, and the
//     limitation must be documented instead of discovered in the field.
//
// The second outcome is what the native win10 gate observed as a service start
// loop ending in an MSI 1603 rollback.
func TestRecoverManagedModeTransitionAcrossStateRootMove(t *testing.T) {
	dir := t.TempDir()

	legacyStateDir := filepath.Join(dir, "state")             // pre-migration root
	migratedStateDir := filepath.Join(legacyStateDir, "core") // post-migration root
	for _, d := range []string{legacyStateDir, migratedStateDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	manager.writerLockPath = filepath.Join(dir, "leshy-writer.lock")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")

	// The crashing Enable rendered and journaled the transition while the core
	// still owned the pre-migration root.
	manager.SetStateDir(legacyStateDir)
	from, err := ReadCommittedModeGeneration(legacyStateDir)
	if err != nil {
		t.Fatal(err)
	}
	to := from + 1
	activationID := "0123456789abcdef0123456789abcdef"
	socket := "/run/kikimora/core.sock"
	journaledDigest := mustStateRootDigest(t, manager, legacyStateDir, socket, activationID, to)
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		socket, "", activationID, from, mustManagedZoneNames(t, manager), journaledDigest)
	if err != nil {
		t.Fatal(err)
	}

	// R1 copies the authority files, journal included, into the new root.
	if err := WriteTransitionJournal(migratedStateDir, *transition); err != nil {
		t.Fatal(err)
	}
	// The credential travels with them; recovery needs it after the marker write.
	if err := os.WriteFile(filepath.Join(migratedStateDir, "control-route-auth.key"),
		[]byte(strings.Repeat("cd", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Recovery now runs against the migrated root, exactly as the installed
	// service does at startup after the MSI moved the layout.
	manager.SetStateDir(migratedStateDir)
	recoveredDigest := mustStateRootDigest(t, manager, migratedStateDir, socket, activationID, to)

	_, recoverErr := manager.RecoverManagedModeTransition()
	journalSurvived := true
	if _, found, err := ReadTransitionJournal(migratedStateDir); err == nil && !found {
		journalSurvived = false
	}

	switch {
	case journaledDigest == recoveredDigest:
		// Root-independent fence: recovery must converge and consume the journal.
		if recoverErr != nil {
			t.Fatalf("digests are equal but recovery failed: %v", recoverErr)
		}
		if journalSurvived {
			t.Fatal("a converged recovery must consume the transition journal")
		}
	case recoverErr == nil:
		// Digests differ yet recovery succeeded: the fence tolerated the move.
		// Recorded explicitly because the native gate observed the opposite.
		t.Logf("recovery tolerated a state-root move: journaled=%s recovered=%s ran=%s",
			journaledDigest, recoveredDigest, transition.ID)
		if journalSurvived {
			t.Fatal("a successful recovery must consume the transition journal")
		}
	default:
		// Current behaviour: fail closed on the digest mismatch. Assert the
		// message names the mismatch so the operator-facing log stays diagnosable,
		// and that the journal is left in place for a deliberate retry.
		if !strings.Contains(recoverErr.Error(), "rendered digest") ||
			!strings.Contains(recoverErr.Error(), "does not match the journaled") {
			t.Fatalf("expected a digest-mismatch refusal, got: %v", recoverErr)
		}
		if !journalSurvived {
			t.Fatal("a refused recovery must not consume the journal")
		}
		t.Logf("KNOWN LIMITATION: an interrupted Enable cannot roll forward across a state-root move; "+
			"journaled=%s recovered=%s; operator must re-enable managed mode", journaledDigest, recoveredDigest)
	}
}
