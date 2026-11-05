package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteAtomicFileCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := WriteAtomicFile(path, []byte(`{"v":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != `{"v":1}`+"\n" {
		t.Fatalf("first write: %q", first)
	}

	// Replacement over an existing destination must be atomic and complete:
	// readers observe only the whole old or the whole new file.
	if err := WriteAtomicFile(path, []byte(`{"v":2}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != `{"v":2}`+"\n" {
		t.Fatalf("replacement: %q", second)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("temporary files must not survive: %v", names)
	}
}

func TestWriteAtomicFileCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "state.json")
	if err := WriteAtomicFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAtomicJSONMarshalFailureLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteAtomicJSON(path, map[string]any{"ok": true}, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A channel cannot be marshalled: the failure must leave the previous
	// durable file intact and no temporary file behind.
	if err := WriteAtomicJSON(path, map[string]any{"bad": make(chan int)}, 0o600); err == nil {
		t.Fatal("marshal failure must be returned")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("marshal failure corrupted the previous state: %q -> %q", before, after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary file leaked: %d entries", len(entries))
	}
}

func TestWriteAtomicFileFailurePaths(t *testing.T) {
	dir := t.TempDir()
	// Parent is a file: mkdir and temp creation must fail cleanly.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomicFile(filepath.Join(blocker, "child.json"), []byte("x"), 0o600); err == nil {
		t.Fatal("write under a file parent must fail")
	}
	// Destination is a directory: the final replacement must fail and no
	// partial destination may appear.
	dst := filepath.Join(dir, "dst-dir")
	if err := os.Mkdir(dst, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomicFile(dst, []byte("x"), 0o600); err == nil {
		t.Fatal("replace onto a directory must fail")
	}
}

func TestWriteAtomicFilePermissionBits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	if err := WriteAtomicFile(path, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows exposes only the read-only bit through Go's FileMode; the 0600
	// contract is asserted on POSIX and the ACL contract is covered by the
	// Stage 1C key-creation tests on Windows.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("group/other permission bits must be clear, got %v", info.Mode().Perm())
	}
}

func TestRemoveDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.json")
	if err := WriteAtomicFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDurable(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file must be gone, got %v", err)
	}
	// Missing is success.
	if err := RemoveDurable(path); err != nil {
		t.Fatalf("remove of a missing file must succeed, got %v", err)
	}
	// Directory sync of the parent happened; the parent itself remains.
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
