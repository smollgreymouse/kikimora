package leshy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileBridgeResyncRewritesExistingPublication(t *testing.T) {
	dir := t.TempDir()
	b := FileBridge{Dir: dir}
	if err := b.Publish(context.Background(), RolePublication{Role: "primary", Zone: "primary", Interface: "kk0"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "primary.dev"), []byte("kk0\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Resync(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "primary.dev"))
	if err != nil || string(data) != "kk0\n" {
		t.Fatalf("resync did not normalize publication: %q %v", data, err)
	}
}

func TestFileBridgeUsesZoneInsteadOfRoleID(t *testing.T) {
	dir := t.TempDir()
	b := FileBridge{Dir: dir}
	if err := b.Publish(context.Background(), RolePublication{Role: "corp-vpn", Zone: "secondary", Interface: "kk-corp0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "corp-vpn.dev")); !os.IsNotExist(err) {
		t.Fatalf("role-name publication unexpectedly exists: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "secondary.dev"))
	if err != nil || string(data) != "kk-corp0\n" {
		t.Fatalf("zone publication mismatch: %q %v", data, err)
	}
}

func TestFileBridgeRejectsEmptyZone(t *testing.T) {
	b := FileBridge{Dir: t.TempDir()}
	if err := b.Publish(context.Background(), RolePublication{Role: "corp-vpn", Interface: "kk0"}); err == nil {
		t.Fatal("empty Leshy zone was accepted")
	}
}
