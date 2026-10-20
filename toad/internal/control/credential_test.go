package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
