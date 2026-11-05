package lockfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecondAcquireFailsImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := Acquire(path); err == nil {
		t.Fatal("second acquire on a held lock must fail")
	}
}

func TestCloseReleasesAndReacquireSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("reacquire after close must succeed: %v", err)
	}
	second.Close()
}

func TestAbandonedFileContentIsNotAuthority(t *testing.T) {
	// A leftover file from a dead process must never block a new holder:
	// exclusivity comes from the OS lock, not the file content.
	path := filepath.Join(t.TempDir(), "leshy-writer.lock")
	if err := os.WriteFile(path, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("stale file content must not block acquire: %v", err)
	}
	lock.Close()
}

func TestNilCloseIsSafe(t *testing.T) {
	var l *Lock
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
