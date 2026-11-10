package control

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/lockfile"
)

func transactionManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
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
	return manager, stateDir
}

func testCapabilities() []string {
	return []string{"af_unix_control", "managed_selected_routes_latest", "managed_selected_routes_v2", "privileged_control_auth_v1"}
}

func TestEnableManagedLeshyWritesTheWholeTransaction(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if status := manager.ManagedLeshyMode(); status.Mode != string(ManagedLeshyLocal) {
		t.Fatalf("fresh install must be local: %#v", status)
	}
	status, err := manager.EnableManagedLeshy("/run/kikimora/core.sock", "", testCapabilities())
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if status.Mode != string(ManagedLeshyManaged) || status.Generation != 1 {
		t.Fatalf("status after enable: %#v", status)
	}
	if status.ActivationIDFingerprint == "" {
		t.Fatal("the status must expose the activation fingerprint")
	}

	marker, found, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil || !found {
		t.Fatalf("marker: found=%v err=%v", found, err)
	}
	if marker.Schema != 2 || marker.ModeGeneration != 1 || len(marker.ActivationID) != 32 {
		t.Fatalf("marker content: %#v", marker)
	}
	if fingerprint := activationIDFingerprint(marker.ActivationID); fingerprint != status.ActivationIDFingerprint {
		t.Fatalf("fingerprint mismatch: %q vs %q", fingerprint, status.ActivationIDFingerprint)
	}

	data, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`routing_backend = "kikimora"`,
		`kikimora_socket = "/run/kikimora/core.sock"`,
		"kikimora_auth_file = " + strconv.Quote(PrivilegedAuthKeyPath(stateDir)),
		`kikimora_activation_id = "` + marker.ActivationID + `"`,
		"kikimora_mode_generation = 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated managed config misses %q:\n%s", want, text)
		}
	}
	if _, found, err := ReadTransitionJournal(stateDir); err != nil || found {
		t.Fatalf("journal must be removed after a complete enable: found=%v err=%v", found, err)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != 1 {
		t.Fatalf("committed generation: %d err=%v", generation, err)
	}

	// The second transition proves the lock was released and the generation
	// advanced.
	status, err = manager.DisableManagedLeshy("")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if status.Mode != string(ManagedLeshyLocal) || status.Generation != 2 {
		t.Fatalf("status after disable: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(manager.configDir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatal("the marker must be gone after disable")
	}
	data, err = os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"routing_backend", "kikimora_socket", "kikimora_auth_file", "kikimora_activation_id", "kikimora_mode_generation"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("local config still carries %q:\n%s", forbidden, data)
		}
	}
}

func TestEnableManagedLeshyRequiresEveryCapability(t *testing.T) {
	manager, _ := transactionManager(t)
	_, err := manager.EnableManagedLeshy("/run/kikimora/core.sock", "", []string{"af_unix_control", "managed_selected_routes_v1"})
	if err == nil {
		t.Fatal("a v1-only Leshy must be rejected")
	}
	if !strings.Contains(err.Error(), "managed_selected_routes_v2") {
		t.Fatalf("the error must name the missing capability: %v", err)
	}
	if status := manager.ManagedLeshyMode(); status.Mode != string(ManagedLeshyLocal) {
		t.Fatalf("a rejected activation must not change the mode: %#v", status)
	}
}

func TestModeTransactionRefusesABusyWriterLock(t *testing.T) {
	manager, stateDir := transactionManager(t)
	lock, err := lockfile.Acquire(manager.writerLockPath)
	if err != nil {
		t.Fatalf("hold the writer lock: %v", err)
	}
	defer lock.Close()

	_, err = manager.EnableManagedLeshy("/run/kikimora/core.sock", "", testCapabilities())
	if err == nil {
		t.Fatal("a busy writer lock must refuse the transaction")
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("the error must say the lock is busy: %v", err)
	}
	if _, found, _ := ReadTransitionJournal(stateDir); found {
		t.Fatal("a refused transaction must not leave a journal")
	}
	if _, err := os.Stat(filepath.Join(manager.configDir, "leshy-managed.json")); !os.IsNotExist(err) {
		t.Fatal("a refused transaction must not write the marker")
	}
}

func TestModeTransactionRefusesAStaleJournal(t *testing.T) {
	manager, stateDir := transactionManager(t)
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		"/run/kikimora/core.sock", "", "0123456789abcdef0123456789abcdef", 0,
		[]string{"primary"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnableManagedLeshy("/run/kikimora/core.sock", "", testCapabilities()); err == nil {
		t.Fatal("an existing journal must refuse a new transition")
	}
}

func TestModeTransactionRefusesAForeignAuthFile(t *testing.T) {
	manager, _ := transactionManager(t)
	_, err := manager.EnableManagedLeshy("/run/kikimora/core.sock", "/tmp/other.key", testCapabilities())
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a foreign auth file must be rejected: %v", err)
	}
}

func TestManagedModeAPIRejectsAWrongCredential(t *testing.T) {
	manager, _ := transactionManager(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	manager.SetPrivilegedAuth(key)
	wrong := hex.EncodeToString(make([]byte, 32))

	response := manager.Handle(context.Background(), Request{
		Version:        APIVersion,
		Method:         "EnableManagedLeshy",
		Socket:         "/run/kikimora/core.sock",
		Capabilities:   testCapabilities(),
		PrivilegedAuth: wrong,
	})
	if response.OK {
		t.Fatal("a wrong credential must be denied")
	}
	if status := manager.ManagedLeshyMode(); status.Mode != string(ManagedLeshyLocal) {
		t.Fatalf("a denied request must not change the mode: %#v", status)
	}
}

func TestManagedModeAPIEnablesThroughTheTransaction(t *testing.T) {
	manager, _ := transactionManager(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	manager.SetPrivilegedAuth(key)
	token := hex.EncodeToString(key)

	response := manager.Handle(context.Background(), Request{
		Version:        APIVersion,
		Method:         "EnableManagedLeshy",
		Socket:         "/run/kikimora/core.sock",
		Capabilities:   testCapabilities(),
		PrivilegedAuth: token,
	})
	if !response.OK {
		t.Fatalf("enable through the API: %s", response.Error)
	}
	if response.ManagedMode == nil || response.ManagedMode.Mode != string(ManagedLeshyManaged) {
		t.Fatalf("response status: %#v", response.ManagedMode)
	}

	statusResponse := manager.Handle(context.Background(), Request{
		Version:        APIVersion,
		Method:         "GetManagedLeshyMode",
		PrivilegedAuth: token,
	})
	if !statusResponse.OK || statusResponse.ManagedMode == nil {
		t.Fatalf("status through the API: %s", statusResponse.Error)
	}
	if statusResponse.ManagedMode.Generation != 1 {
		t.Fatalf("committed generation: %#v", statusResponse.ManagedMode)
	}

	disableResponse := manager.Handle(context.Background(), Request{
		Version:        APIVersion,
		Method:         "DisableManagedLeshy",
		PrivilegedAuth: token,
	})
	if !disableResponse.OK {
		t.Fatalf("disable through the API: %s", disableResponse.Error)
	}
	if disableResponse.ManagedMode == nil || disableResponse.ManagedMode.Mode != string(ManagedLeshyLocal) {
		t.Fatalf("disable status: %#v", disableResponse.ManagedMode)
	}
}

func TestRecoverManagedModeTransitionRollsForward(t *testing.T) {
	manager, stateDir := transactionManager(t)
	// A crash after `prepared`: the config/marker/generation were never
	// written, but the journal names the target.
	from, err := ReadCommittedModeGeneration(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	to := from + 1
	activationID := "0123456789abcdef0123456789abcdef"
	server, err := manager.buildLeshyServer(manager.leshyConfigPath, "/run/kikimora/core.sock", activationID, to)
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
		"/run/kikimora/core.sock", "", activationID, from, manager.managedZoneNames(), configDigest(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}

	recovered, err := manager.RecoverManagedModeTransition()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !recovered {
		t.Fatal("a pending journal must report a recovery")
	}
	marker, found, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil || !found {
		t.Fatalf("marker after recovery: found=%v err=%v", found, err)
	}
	if marker.ActivationID != activationID || marker.ModeGeneration != to {
		t.Fatalf("marker after recovery: %#v", marker)
	}
	if generation, err := ReadCommittedModeGeneration(stateDir); err != nil || generation != to {
		t.Fatalf("generation after recovery: %d err=%v", generation, err)
	}
	if _, found, _ := ReadTransitionJournal(stateDir); found {
		t.Fatal("the journal must be removed after a successful roll-forward")
	}
	data, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(rendered) {
		t.Fatal("the recovered config must equal the journaled bytes")
	}
}

func TestRecoverManagedModeTransitionVerifiesACommittedTarget(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if _, err := manager.EnableManagedLeshy("/run/kikimora/core.sock", "", testCapabilities()); err != nil {
		t.Fatal(err)
	}
	// A crash after the generation commit but before the journal removal: the
	// durable state already matches the target.
	generation, err := ReadCommittedModeGeneration(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manager.leshyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	marker, _, err := readManagedMarker(filepath.Join(manager.configDir, "leshy-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		marker.Socket, "", marker.ActivationID, generation-1, marker.ManagedZones, configDigest(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	recovered, err := manager.RecoverManagedModeTransition()
	if err != nil {
		t.Fatalf("verify-and-clean: %v", err)
	}
	if !recovered {
		t.Fatal("a pending journal must report a recovery")
	}
	if _, found, _ := ReadTransitionJournal(stateDir); found {
		t.Fatal("the journal must be removed after verification")
	}
}

func TestRecoverManagedModeTransitionFailsClosedOnAStaleJournal(t *testing.T) {
	manager, stateDir := transactionManager(t)
	if err := WriteCommittedModeGeneration(stateDir, 5); err != nil {
		t.Fatal(err)
	}
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged,
		"/run/kikimora/core.sock", "", "0123456789abcdef0123456789abcdef", 0,
		[]string{"primary"}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverManagedModeTransition(); err == nil {
		t.Fatal("a journal that matches neither generation must fail closed")
	}
}

func TestVerifyConfigDigestDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyConfigDigest(path, configDigest([]byte("a\n"))); err != nil {
		t.Fatalf("matching digest: %v", err)
	}
	if err := verifyConfigDigest(path, configDigest([]byte("b\n"))); err == nil {
		t.Fatal("a drifting file must fail the digest proof")
	}
	if err := verifyConfigDigest(filepath.Join(dir, "missing.toml"), "x"); err == nil {
		t.Fatal("a missing file must fail the digest proof")
	}
	_ = errors.New
}
