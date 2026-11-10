package control

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 08D1 §31.22: the application-readiness barrier. ServeWithReady fires the
// ready callback exactly once after the socket is bound; the snapshot only
// then reports startup_ready. Ordinary successful boot has exactly one ready
// callback.
func TestServeWithReadyApplicationReadinessBarrier(t *testing.T) {
	// macOS AF_UNIX paths are capped at 104 bytes; the packaged socket lives
	// in a short directory for the same reason.
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if manager.Snapshot().StartupReady {
		t.Fatal("startup_ready must be false before the control socket is bound")
	}
	if manager.Snapshot().ModeTransition != "none" {
		t.Fatalf("mode transition must default to none, got %q", manager.Snapshot().ModeTransition)
	}

	socket := filepath.Join(dir, "test.sock")
	var readyCount int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- ServeWithReady(ctx, socket, manager, func() {
			atomic.AddInt32(&readyCount, 1)
			manager.MarkStartupReady()
		})
	}()

	// The socket must accept and report readiness once bound.
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := Call(socket, Request{Version: APIVersion, Method: "Snapshot"})
		if err == nil && snap.OK && snap.Snapshot != nil {
			if !snap.Snapshot.StartupReady {
				t.Fatal("snapshot after the ready callback must report startup_ready")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if n := atomic.LoadInt32(&readyCount); n != 1 {
		t.Fatalf("ready callback must fire exactly once, got %d", n)
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

// A development manager that never serves keeps startup_ready false: the
// flag is default-false in partial startup state and in tests.
func TestStartupReadyStaysFalseWithoutServe(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.Snapshot().StartupReady {
		t.Fatal("startup_ready must stay false without ServeWithReady")
	}
}

// Production Plan B4: the snapshot exposes the managed-mode diagnostics
// without ever carrying the auth token or the full ActivationID.
func TestSnapshotExposesManagedReadinessDiagnostics(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	snapshot := manager.Snapshot()
	if snapshot.ManagedMode {
		t.Fatal("a fresh local manager must not report managed mode")
	}
	if snapshot.ModeGeneration != 0 {
		t.Fatalf("fresh generation must be 0, got %d", snapshot.ModeGeneration)
	}
	if snapshot.RouteAuthorityReady {
		t.Fatal("route authority cannot be ready before the reconciler proves it")
	}
	if snapshot.ModeTransition != "none" {
		t.Fatalf("mode transition must default to none, got %q", snapshot.ModeTransition)
	}

	manager.SetManagedModeDiagnostics(7, "abcd1234")
	snapshot = manager.Snapshot()
	if snapshot.ModeGeneration != 7 || snapshot.ActivationIDFingerprint != "abcd1234" {
		t.Fatalf("diagnostics not recorded: generation=%d fingerprint=%q", snapshot.ModeGeneration, snapshot.ActivationIDFingerprint)
	}
}

// Production Plan B1: production constructs the manager with deferred managed
// restore, so the marker is validated strictly but applied only through
// RestoreCommittedManagedState (after transition recovery and the committed
// generation check).
func TestDeferredManagedRestoreAppliesMarkerOnDemand(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "one", "openconnect")
	marker := `{"schema":2,"socket":"/tmp/deferred-managed.sock","core_version":"test","managed_zones":[],"activation_id":"0123456789abcdef0123456789abcdef","mode_generation":4}`
	if err := os.WriteFile(filepath.Join(dir, "leshy-managed.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManagerWithLegacyAndDeps(dir, []string{configPath}, &fakeLauncher{}, "", "", "", managerDependencies{
		restorePolicy: DeferManagedRestore,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if manager.ManagedModeActive() {
		t.Fatal("deferred restore must not apply the marker during construction")
	}
	if !manager.CommittedManagedMode() {
		t.Fatal("the committed mode must be visible to the auth rule before restore")
	}
	if err := manager.RestoreCommittedManagedState(); err != nil {
		t.Fatalf("restore committed managed state: %v", err)
	}
	if !manager.ManagedModeActive() {
		t.Fatal("RestoreCommittedManagedState must apply the validated marker")
	}
	if err := manager.RestoreCommittedManagedState(); err != nil {
		t.Fatalf("restore must be idempotent: %v", err)
	}
}
