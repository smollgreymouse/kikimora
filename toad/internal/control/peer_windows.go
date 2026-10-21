//go:build windows

package control

import (
	"fmt"
	"net"
)

// authorizePeer verifies that a control-plane connection arrived over the
// expected AF_UNIX transport. Windows AF_UNIX does not expose SO_PEERCRED
// or an equivalent kernel-level peer-credential mechanism, so the directory
// ACL on C:\ProgramData\Kikimora is the primary authorization gate:
// connect() opens the socket file, and the installer restricts the parent
// directory to SYSTEM/Administrators.
//
// The transport-type check is defense-in-depth: it rejects TCP, named-pipe
// or any other connection that bypasses the directory ACL gate. A file-mode
// check is not attempted because Windows ACLs — not Unix permission bits —
// govern socket file access, and os.Stat always reports 0666 regardless of
// the real ACL. When named pipes become viable (go-winio bug resolved), the
// full peer-PID authorization from Phase 1's original design should replace
// this check.
func authorizePeer(conn net.Conn) error {
	if _, ok := conn.(*net.UnixConn); !ok {
		return fmt.Errorf("control peer is not a Unix socket")
	}
	return nil
}
