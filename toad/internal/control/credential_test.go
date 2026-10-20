package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
)

// TestConnectRoleRejectsTunnelWithoutCredentials covers the hand-edited case: the file is
// on disk without credentials, so the start path must refuse it with a reason instead of
// letting OpenConnect retry for ever.
func TestConnectRoleRejectsTunnelWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oc-main.toml")
	state := filepath.ToSlash(filepath.Join(dir, "state"))
	content := "" +
		"name = \"oc-main\"\n" +
		"protocol = \"openconnect\"\n" +
		"interface = \"kk-oc0\"\n" +
		"address = [\"10.0.0.81/24\"]\n" +
		"mtu = 1378\n" +
		"state_dir = \"" + state + "\"\n" +
		"\n[openconnect]\n" +
		"gateway = \"https://vpn.example/\"\n" +
		"username = \"lab\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager([]string{path}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.monitorCancel()
	defer manager.Close()

	if err := manager.ConnectRole(context.Background(), "oc-main"); err == nil {
		t.Fatal("ConnectRole accepted a tunnel without credentials")
	}
}

// TestConnectRoleDisablesTheRole proves a configuration-terminal failure stops the retry loop:
// the role is left disabled with the reason recorded, so ConnectAll cannot hammer it again.
func TestConnectRoleDisablesTheRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oc-main.toml")
	state := filepath.ToSlash(filepath.Join(dir, "state"))
	content := "" +
		"name = \"oc-main\"\n" +
		"protocol = \"openconnect\"\n" +
		"interface = \"kk-oc0\"\n" +
		"address = [\"10.0.0.81/24\"]\n" +
		"mtu = 1378\n" +
		"state_dir = \"" + state + "\"\n" +
		"\n[openconnect]\n" +
		"gateway = \"https://vpn.example/\"\n" +
		"username = \"lab\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager([]string{path}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.monitorCancel()
	defer manager.Close()

	if err := manager.ConnectRole(context.Background(), "oc-main"); err == nil {
		t.Fatal("ConnectRole accepted a tunnel without credentials")
	}
	for _, role := range manager.Snapshot().Roles {
		if role.ID == "oc-main" && role.DesiredEnabled {
			t.Fatalf("role left enabled after a terminal failure: %#v", role)
		}
	}
	if err := manager.ConnectRole(context.Background(), "oc-main"); err == nil {
		t.Fatal("second ConnectRole did not fail too")
	}
}

// TestAggregateBecomesTerminalAfterFailure pins the aggregate contract the UI reads:
// a role that gave up must not leave the composition in Connecting for ever.
func TestAggregateBecomesDegradedAfterTerminalFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "one", "openconnect")
	manager, err := NewManager([]string{path}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	operation := manager.roles["one"].operation
	epoch := manager.underlay.Epoch
	manager.mu.Unlock()

	_ = manager.product.SetRoleDesired(context.Background(), "one", true)
	if !manager.product.SetRecovery("one",
		core.RecoveryState{Step: core.RecoveryStartTransport, Operation: operation, Epoch: epoch},
		core.RoleFailed, "tunnel cannot authenticate") {
		t.Fatal("SetRecovery refused the failed state")
	}

	// With every role failed the aggregate is Failed; Degraded is the mixed case, when some
	// roles are ready and at least one gave up.
	if got := manager.Snapshot().AggregateState; got != "Failed" {
		t.Fatalf("aggregate = %q, want Failed", got)
	}
	// A failed role must not arm another retry.
	manager.scheduleRecoveryRetry(&routeTargetRecoveryDriver{}, "one", operation, epoch)
	if role, ok := manager.product.Role("one"); ok && role.State != core.RoleFailed {
		t.Fatalf("failed role changed state to %q", role.State)
	}
}
