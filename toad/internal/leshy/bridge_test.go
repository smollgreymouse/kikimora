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
	if err := b.Publish(context.Background(), RolePublication{Role: "primary", Interface: "kk0"}); err != nil {
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
