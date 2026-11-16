package control

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func mustConfigDigestV2(t *testing.T, data []byte) string {
	t.Helper()
	digest, err := configDigestV2(data)
	if err != nil {
		t.Fatalf("digest config with the v2 contract: %v", err)
	}
	return digest
}

// renderStateRootConfig renders the generated Leshy config the way the mode
// transaction will for a given state root.
func renderStateRootConfig(t *testing.T, manager *Manager, stateDir, socket, activationID string, generation uint64) []byte {
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
	return rendered
}

// TestConfigDigestV2NormalizesOnlyTheStateRoot pins the PROD3 digest contract:
// the v2 digest ignores exactly the core-owned state root of
// server.kikimora_auth_file and still pins every other generated setting.
func TestConfigDigestV2NormalizesOnlyTheStateRoot(t *testing.T) {
	base := "[server]\nlisten_address = \"127.0.0.1:53053\"\nkikimora_auth_file = \"C:/state/core/control-route-auth.key\"\n"
	legacyRoot := "[server]\nlisten_address = \"127.0.0.1:53053\"\nkikimora_auth_file = \"C:/state/control-route-auth.key\"\n"
	otherFile := "[server]\nlisten_address = \"127.0.0.1:53053\"\nkikimora_auth_file = \"C:/state/core/other.key\"\n"
	otherSetting := "[server]\nlisten_address = \"127.0.0.1:53054\"\nkikimora_auth_file = \"C:/state/core/control-route-auth.key\"\n"

	baseV2 := mustConfigDigestV2(t, []byte(base))
	if baseV2 != mustConfigDigestV2(t, []byte(legacyRoot)) {
		t.Fatal("the v2 digest must ignore the core-owned state root")
	}
	if configDigest([]byte(base)) == configDigest([]byte(legacyRoot)) {
		t.Fatal("the legacy digest must still see the state root, or this test proves nothing")
	}
	if baseV2 == mustConfigDigestV2(t, []byte(otherFile)) {
		t.Fatal("the v2 digest must still pin the credential file name")
	}
	if baseV2 == mustConfigDigestV2(t, []byte(otherSetting)) {
		t.Fatal("the v2 digest must still pin the rest of the server block")
	}
}

// TestRecoverManagedModeTransitionAcrossStateRootMove pins the R1 upgrade
// contract: a schema-1 journal written by an installed pre-R1 core, interrupted
// at config_written, must be upgraded to the semantic digest and rolled forward
// after the state root moved from state to state\\core (PROD3, C8).
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
	// still owned the pre-migration root, and wrote exactly these bytes.
	manager.SetStateDir(legacyStateDir)
	from, err := ReadCommittedModeGeneration(legacyStateDir)
	if err != nil {
		t.Fatal(err)
	}
	to := from + 1
	activationID := "0123456789abcdef0123456789abcdef"
	socket := "/run/kikimora/core.sock"
	rendered := renderStateRootConfig(t, manager, legacyStateDir, socket, activationID, to)
	if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.leshyConfigPath, rendered, 0o644); err != nil {
		t.Fatal(err)
	}

	// Schema 1 is what the installed pre-R1 core wrote: the raw byte digest,
	// no digest-version field.
	legacyJournal := ManagedLeshyTransition{
		Schema:         1,
		ID:             "fedcba9876543210fedcba9876543210",
		From:           ManagedLeshyLocal,
		To:             ManagedLeshyManaged,
		Phase:          ManagedTransitionConfigWritten,
		Socket:         socket,
		ToActivationID: activationID,
		FromGeneration: from,
		ToGeneration:   to,
		Zones:          mustManagedZoneNames(t, manager),
		ConfigSHA256:   configDigest(rendered),
	}
	if err := legacyJournal.Validate(); err != nil {
		t.Fatalf("legacy journal: %v", err)
	}

	// R1 copies the authority files, journal included, into the new root.
	if err := WriteTransitionJournal(migratedStateDir, legacyJournal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migratedStateDir, "control-route-auth.key"),
		[]byte("cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Recovery runs against the migrated root, exactly as the installed service
	// does at startup after the MSI moved the layout. It must converge.
	manager.SetStateDir(migratedStateDir)
	ran, recoverErr := manager.RecoverManagedModeTransition()
	if recoverErr != nil {
		t.Fatalf("a legacy config_written journal must converge across the state-root move: %v", recoverErr)
	}
	if !ran {
		t.Fatal("recovery reported that it did not run")
	}
	if _, found, err := ReadTransitionJournal(migratedStateDir); err != nil || found {
		t.Fatalf("a converged recovery must consume the journal: found=%v err=%v", found, err)
	}
	committed, err := ReadCommittedModeGeneration(migratedStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if committed != to {
		t.Fatalf("committed generation = %d, want the journal target %d", committed, to)
	}
	marker, found, err := manager.readManagedMarkerFile()
	if err != nil || !found {
		t.Fatalf("managed marker missing after convergence: found=%v err=%v", found, err)
	}
	if marker.ActivationID != activationID || marker.ModeGeneration != to {
		t.Fatalf("marker does not match the journal target: %#v", marker)
	}
	server, err := config.ReadLeshyServerStrict(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if server.KikimoraAuthFile != PrivilegedAuthKeyPath(migratedStateDir) {
		t.Fatalf("generated config still points at %q, want the migrated root", server.KikimoraAuthFile)
	}
}

// TestLegacyTransitionJournalRejectsUnexplainedMismatch proves the upgrader is
// not "accept any mismatch": a durable config that does not match the legacy
// digest is refused, and the journal survives for a deliberate retry.
func TestLegacyTransitionJournalRejectsUnexplainedMismatch(t *testing.T) {
	dir := t.TempDir()
	legacyStateDir := filepath.Join(dir, "state")
	migratedStateDir := filepath.Join(legacyStateDir, "core")
	for _, d := range []string{legacyStateDir, migratedStateDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	manager.writerLockPath = filepath.Join(dir, "leshy-writer.lock")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")
	manager.SetStateDir(legacyStateDir)

	socket := "/run/kikimora/core.sock"
	activationID := "0123456789abcdef0123456789abcdef"
	rendered := renderStateRootConfig(t, manager, legacyStateDir, socket, activationID, 1)
	if err := os.MkdirAll(filepath.Dir(manager.leshyConfigPath), 0o750); err != nil {
		t.Fatal(err)
	}
	// The durable file drifted from what the journal recorded.
	drifted := []byte("[server]\nlisten_address = \"127.0.0.1:53053\"\n")
	if err := os.WriteFile(manager.leshyConfigPath, drifted, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyJournal := ManagedLeshyTransition{
		Schema: 1, ID: "fedcba9876543210fedcba9876543210",
		From: ManagedLeshyLocal, To: ManagedLeshyManaged, Phase: ManagedTransitionConfigWritten,
		Socket: socket, ToActivationID: activationID, FromGeneration: 0, ToGeneration: 1,
		Zones: mustManagedZoneNames(t, manager), ConfigSHA256: configDigest(rendered),
	}
	if err := WriteTransitionJournal(migratedStateDir, legacyJournal); err != nil {
		t.Fatal(err)
	}

	manager.SetStateDir(migratedStateDir)
	if _, err := manager.RecoverManagedModeTransition(); err == nil {
		t.Fatal("an unexplained legacy digest mismatch must be refused")
	}
	if _, found, err := ReadTransitionJournal(migratedStateDir); err != nil || !found {
		t.Fatalf("a refused upgrade must keep the journal: found=%v err=%v", found, err)
	}
	committed, err := ReadCommittedModeGeneration(migratedStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if committed != 0 {
		t.Fatalf("a refused upgrade committed generation %d", committed)
	}
}
