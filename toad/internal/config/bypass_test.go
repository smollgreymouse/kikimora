package config

import (
	"os"
	"path/filepath"
	"testing"
)

// PC8A §1.3: the strict bypass reader fails closed on corruption.
func TestReadBypassModeStrict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bypass.conf")

	mode, err := ReadBypassModeStrict(path)
	if err != nil || mode != BypassDirect {
		t.Fatalf("missing file: mode=%q err=%v, want direct/nil", mode, err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mode, err = ReadBypassModeStrict(path); err != nil || mode != BypassDirect {
		t.Fatalf("empty file: mode=%q err=%v, want direct/nil", mode, err)
	}
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mode, err = ReadBypassModeStrict(path); err != nil || mode != "one" {
		t.Fatalf("role mode: mode=%q err=%v, want one/nil", mode, err)
	}
	if err := os.WriteFile(path, []byte("bad name!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadBypassModeStrict(path); err == nil {
		t.Fatal("an invalid persisted bypass mode must be an error")
	}
}
