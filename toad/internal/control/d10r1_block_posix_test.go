//go:build !windows

package control

import (
	"os"
	"path/filepath"
	"testing"
)

// blockFileRemoval makes the durable removal of path fail while the file
// stays readable: POSIX removal is governed by the parent directory's write
// bit, so a read-only directory blocks the unlink (P0-D1 failure seam).
func blockFileRemoval(t *testing.T, path string) {
	t.Helper()
	parent := filepath.Dir(path)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("block the marker removal: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
}
