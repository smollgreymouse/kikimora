package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The D9 diagnostics matrix (08D1 post-D7 audit §9): the snapshot and the
// leshy-mode status describe the committed truth from the post-recovery
// descriptor and the transitional truth from the live gate — phases visible
// while the transaction runs, the transition fingerprint surviving exactly
// as long as the journal, and never a token, a full activation id or a full
// transition id.

func TestTransitionPhaseIsVisibleWhileTheTransactionRuns(t *testing.T) {
	manager, stateDir := transactionManager(t)
	armPrivilegedAuthForTest(t, manager)

	// A real mid-transaction failure: the config write is blocked after the
	// journal landed, so the live gate sits in the prepared phase.
	blockedConfig := filepath.Join(filepath.Dir(manager.leshyConfigPath), "config.toml")
	if err := os.MkdirAll(blockedConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	manager.leshyConfigPath = blockedConfig
	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err == nil {
		t.Fatal("the blocked config write must fail the enable")
	}
	transition, found, err := ReadTransitionJournal(stateDir)
	if err != nil || !found {
		t.Fatalf("the journal must survive: found=%v err=%v", found, err)
	}
	snapshot := manager.Snapshot()
	if snapshot.ModeTransition != "prepared" {
		t.Fatalf("the phase must be visible while the journal exists: %q", snapshot.ModeTransition)
	}
	if snapshot.ModeTransitionIDFingerprint != transitionIDFingerprint(transition.ID) {
		t.Fatalf("the snapshot fingerprint must hash the journal id: %q", snapshot.ModeTransitionIDFingerprint)
	}
	status := manager.ManagedLeshyMode()
	if status.Transition != "prepared" || status.TransitionIDFingerprint != snapshot.ModeTransitionIDFingerprint {
		t.Fatalf("the leshy-mode status must agree with the snapshot: %#v", status)
	}

	// The rollback converges once the blocked path is gone: the journal is
	// removed and the fingerprint disappears with it.
	if err := os.RemoveAll(blockedConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverManagedModeTransition(); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	snapshot = manager.Snapshot()
	if snapshot.ModeTransition != "none" || snapshot.ModeTransitionIDFingerprint != "" {
		t.Fatalf("the fingerprint must disappear after the cleanup: %#v", snapshot)
	}
	status = manager.ManagedLeshyMode()
	if status.Transition != "" && status.Transition != "none" {
		t.Fatalf("the status transition must be gone: %#v", status)
	}

	// The later phases are the same diagnostics contract: the gate drives
	// what the snapshot reports until the journal is gone.
	transition, err = NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		"/run/kk-test.sock", "", "0123456789abcdef0123456789abcdef", 1,
		[]string{"primary"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	transition.Phase = ManagedTransitionConfigWritten
	manager.activateModeTransitionGate()
	manager.setModeTransitionProgress(transition)
	snapshot = manager.Snapshot()
	if snapshot.ModeTransition != "config_written" || snapshot.ModeTransitionIDFingerprint != transitionIDFingerprint(transition.ID) {
		t.Fatalf("the config_written phase must be visible: %#v", snapshot)
	}
	transition.Phase = ManagedTransitionMarkerWritten
	manager.setModeTransitionProgress(transition)
	if snapshot := manager.Snapshot(); snapshot.ModeTransition != "marker_written" {
		t.Fatalf("the marker_written phase must be visible: %#v", snapshot)
	}
	manager.settleModeTransitionGate()
	if snapshot := manager.Snapshot(); snapshot.ModeTransition != "none" || snapshot.ModeTransitionIDFingerprint != "" {
		t.Fatalf("the fingerprint must disappear once the journal is gone: %#v", snapshot)
	}
}

func TestCommittedAndTransitionalTruthNeverCarriesSecrets(t *testing.T) {
	manager, stateDir := transactionManager(t)
	tokenHex := armPrivilegedAuthForTest(t, manager)
	if _, err := createPrivilegedAuthKey(PrivilegedAuthKeyPath(stateDir)); err != nil {
		t.Fatalf("stage the credential: %v", err)
	}
	if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
		t.Fatal(err)
	}
	marker, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Arm the transitional state mid-flight for the secret sweep.
	manager.activateModeTransitionGate()
	manager.mu.Lock()
	manager.modeTransitionIDFingerprint = transitionIDFingerprint(strings.Repeat("a", 32))
	manager.mu.Unlock()

	status := manager.ManagedLeshyMode()
	snapshot := manager.Snapshot()
	statusJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(statusJSON) + string(snapshotJSON)

	// The bearer token never appears in status/Snapshot diagnostics.
	if strings.Contains(text, tokenHex) {
		t.Fatal("the privileged auth token leaked into diagnostics")
	}
	// The full activation id never appears; only its fingerprint.
	if strings.Contains(text, marker.ActivationID) {
		t.Fatal("the full activation id leaked into diagnostics")
	}
	if status.ActivationIDFingerprint != activationIDFingerprint(marker.ActivationID) {
		t.Fatalf("the status fingerprint must hash the activation: %#v", status)
	}
	// The full transition id never appears; only its fingerprint.
	fullTransitionID := strings.Repeat("a", 32)
	if strings.Contains(text, fullTransitionID) {
		t.Fatal("the full transition id leaked into diagnostics")
	}
	if status.TransitionIDFingerprint != transitionIDFingerprint(fullTransitionID) {
		t.Fatalf("the transition fingerprint must hash the id: %#v", status)
	}

	// The auth key file lives under the state dir, never beside the marker.
	if _, err := os.Stat(filepath.Join(manager.configDir, "control-route-auth.key")); !os.IsNotExist(err) {
		t.Fatalf("the credential must not live in the config directory: %v", err)
	}
	if _, err := os.Stat(PrivilegedAuthKeyPath(stateDir)); err != nil {
		t.Fatalf("the credential lives under the state dir: %v", err)
	}
}

func TestLocalRestartHasAnEmptyActivationFingerprint(t *testing.T) {
	manager, _, stateDir := deferredManager(t)
	stageAuthKey(t, stateDir)
	if err := WriteCommittedModeGeneration(stateDir, 3); err != nil {
		t.Fatal(err)
	}
	state, err := manager.StartupManagedAuthority(stateDir)
	if err != nil {
		t.Fatalf("startup managed authority: %v", err)
	}
	if state.Managed {
		t.Fatalf("descriptor: %#v", state)
	}
	snapshot := manager.Snapshot()
	if snapshot.ActivationIDFingerprint != "" {
		t.Fatalf("a local restart must carry an empty fingerprint: %#v", snapshot)
	}
	if snapshot.ModeGeneration != 3 {
		t.Fatalf("the committed generation must be the descriptor's: %#v", snapshot)
	}
}
