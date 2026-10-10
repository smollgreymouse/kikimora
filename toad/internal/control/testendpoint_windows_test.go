//go:build windows

package control

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// controlTestSocket returns a short AF_UNIX socket path for the test. Windows
// AF_UNIX sockets work under the user temp directory; tests reuse one fixed
// short name and rely on prepareControlAddress removing stale files before
// each listener binds.
func controlTestSocket(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(os.TempDir(), "kikimora-core-test.sock")
}

// waitForControlEndpoint blocks until the listener is reachable. Windows does
// not expose Unix socket files to os.Stat, so readiness is probed by dialing.
func waitForControlEndpoint(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control endpoint %s never became reachable: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
