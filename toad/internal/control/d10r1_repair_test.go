package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The D10R-1 repair matrix (08D1 post-Phase-D audit §4): the marker
// retirement is durable, and the managed zone authority refuses an ambiguous
// role mapping before the first durable mutation.

// TestDisableKeepsTheJournalWhenTheMarkerRetirementFails proves the P0-D1
// failure contract: a durable marker removal that cannot complete fails the
// transaction, leaves the transition journal in place and never commits the
// target generation.
func TestDisableKeepsTheJournalWhenTheMarkerRetirementFails(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
		t.Fatal(err)
	}
	blockFileRemoval(t, filepath.Join(manager.configDir, "leshy-managed.json"))

	if _, err := manager.DisableManagedLeshy(""); err == nil {
		t.Fatal("a failing marker retirement must fail the disable")
	}
	transition, found, err := ReadTransitionJournal(stateDir)
	if err != nil || !found {
		t.Fatalf("the journal must remain for recovery: found=%v err=%v", found, err)
	}
	if transition.To != ManagedLeshyLocal {
		t.Fatalf("the surviving journal must name the local target: %#v", transition)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 1 {
		t.Fatalf("the generation must not be committed after a removal failure: %d err=%v", generation, err)
	}
}

// TestLocalTargetRecoveryKeepsTheJournalWhenTheMarkerRetirementFails proves
// the same durable contract on the recovery path: a roll-forward to Local
// that cannot durably remove the marker fails closed and keeps the journal.
func TestLocalTargetRecoveryKeepsTheJournalWhenTheMarkerRetirementFails(t *testing.T) {
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
		ActivationID:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
		ModeGeneration: 5,
	})
	stageManagedToLocalJournal(t, manager, stateDir, "/run/kk-test.sock", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", 5)
	// The recovery path never reads the marker before retiring it, so a
	// non-empty directory in its place fails exactly the durable removal.
	blocked := filepath.Join(dir, "leshy-managed.json")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.StartupManagedAuthority(stateDir); err == nil {
		t.Fatal("a recovery that cannot durably remove the marker must fail closed")
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || !found {
		t.Fatalf("the journal must remain: found=%v err=%v", found, err)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 5 {
		t.Fatalf("the generation must not move: %d err=%v", generation, err)
	}
}

// writeSharedZoneConfig writes one openconnect role TOML carrying an explicit
// leshy_zone, for the duplicate-zone authority regression.
func writeSharedZoneConfig(t *testing.T, dir, name, zone string) string {
	t.Helper()
	content := "name = " + tomlString(name) + `
protocol = "openconnect"
interface = "kk` + name + `"
mtu = 1380
state_dir = ` + tomlString(filepath.Join(dir, name)) + `
leshy_zone = ` + tomlString(zone) + `

[openconnect]
gateway = "203.0.113.1"
username = "smoke"
`
	path := filepath.Join(dir, name+".toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEnableRejectsDuplicateManagedZones is the P0-D2 regression: two roles
// claiming one zone must fail the Enable with a stable precondition error
// and ZERO durable mutation — never a deduplicated journal that could
// recover into an unusable managed activation.
func TestEnableRejectsDuplicateManagedZones(t *testing.T) {
	dir := t.TempDir()
	configA := writeSharedZoneConfig(t, dir, "alpha", "corp")
	configB := writeSharedZoneConfig(t, dir, "beta", "corp")
	manager, err := NewManager([]string{configA, configB}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.SetStateDir(stateDir)
	manager.writerLockPath = filepath.Join(dir, "leshy-writer.lock")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")
	stageAuthKey(t, stateDir)

	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err == nil {
		t.Fatal("a duplicate managed zone must be refused")
	} else if !strings.Contains(err.Error(), "must map to exactly one role") {
		t.Fatalf("the refusal must be the stable zone-authority error: %v", err)
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || found {
		t.Fatalf("zero journal after the refusal: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatalf("zero marker after the refusal: %v", err)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 0 {
		t.Fatalf("zero generation movement after the refusal: %d err=%v", generation, err)
	}

	// The restart must remain Local: a fresh production manager loads the
	// committed state through StartupManagedAuthority and must not inherit
	// any managed authority from the refused attempt.
	restart, err := NewManagerWithConfigDir(dir, []string{configA, configB}, &fakeLauncher{}, "", "", "")
	if err != nil {
		t.Fatalf("restart manager: %v", err)
	}
	restart.monitorCancel()
	defer restart.Close()
	restart.leshyConfigPath = manager.leshyConfigPath
	restart.routingDir = manager.routingDir
	restart.bypassConfigPath = manager.bypassConfigPath
	state, err := restart.StartupManagedAuthority(stateDir)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if state.Managed || state.Generation != 0 {
		t.Fatalf("the restart must remain Local: %#v", state)
	}
}

// TestEnableAcceptsDistinctManagedZones keeps the strict authority from
// over-refusing: distinct zones still produce the canonical sorted set.
func TestEnableAcceptsDistinctManagedZones(t *testing.T) {
	dir := t.TempDir()
	configA := writeSharedZoneConfig(t, dir, "alpha", "corp")
	configB := writeSharedZoneConfig(t, dir, "beta", "lab")
	manager, err := NewManager([]string{configA, configB}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.SetStateDir(stateDir)
	manager.writerLockPath = filepath.Join(dir, "leshy-writer.lock")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")
	stageAuthKey(t, stateDir)

	zones, err := manager.managedZoneNamesStrict()
	if err != nil {
		t.Fatalf("distinct zones must pass the strict authority: %v", err)
	}
	if len(zones) != 2 || zones[0] != "corp" || zones[1] != "lab" {
		t.Fatalf("the canonical zone set is wrong: %#v", zones)
	}
}
