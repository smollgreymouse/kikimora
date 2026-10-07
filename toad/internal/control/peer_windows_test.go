//go:build windows

package control

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

type nonUnixConn struct {
	net.Conn
}

func TestAuthorizePeerRejectsNonUnixConn(t *testing.T) {
	conn := &nonUnixConn{}
	if err := authorizePeer(conn); err == nil {
		t.Fatal("authorizePeer must reject non-Unix connections")
	}
}

func TestAuthorizePeerAcceptsUnixConn(t *testing.T) {
	sockPath := peerTestSocketPath(t, "accept.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("AF_UNIX not available: %v", err)
	}
	defer listener.Close()

	connCh := make(chan net.Conn, 1)
	go func() {
		c, _ := listener.Accept()
		if c != nil {
			connCh <- c
		}
	}()

	dialConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial test socket: %v", err)
	}
	defer dialConn.Close()

	serverConn := <-connCh
	defer serverConn.Close()

	if err := authorizePeer(serverConn); err != nil {
		t.Fatalf("authorizePeer must accept Unix conn: %v", err)
	}
}

// peerTestSocketPath returns a socket path well within the Windows AF_UNIX
// 108-character limit. t.TempDir() includes the full test function name and
// can exceed the limit; this helper uses a short prefix under os.TempDir().
func peerTestSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "kkpeer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create short socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := filepath.Join(dir, name)
	_ = os.Remove(p)
	return p
}
