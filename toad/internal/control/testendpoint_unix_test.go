//go:build unix

package control

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// controlTestSocket returns the control endpoint for a test working directory.
func controlTestSocket(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, "core.sock")
}

// waitForControlEndpoint blocks until the listener is reachable.
func waitForControlEndpoint(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(address); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control endpoint %s was not created", address)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
