//go:build linux

package control

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/operatorauth"
	"golang.org/x/sys/unix"
)

func peerSupplementaryGroups(pid int32) ([]uint32, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Groups:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Groups:"))
		groups := make([]uint32, 0, len(fields))
		for _, field := range fields {
			value, err := strconv.ParseUint(field, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse peer group %q: %w", field, err)
			}
			groups = append(groups, uint32(value))
		}
		return groups, nil
	}
	return nil, fmt.Errorf("peer group list unavailable for pid %d", pid)
}

func authorizePeer(conn net.Conn) error {
	return authorizePeerWithRegistry(conn, operatorauth.Registry{Dir: operatorauth.DefaultRegistryDir})
}

// Server identity seams, evaluated once at init. Tests override them to
// close the allow branches and drive the registry/rejection paths with a
// real self-pair socket; production always sees the true server identity.
var (
	serverUID      = os.Getuid()
	serverEGID     = os.Getegid()
	readPeerGroups = peerSupplementaryGroups
)

// authorizePeerWithRegistry authorizes the peer against SO_PEERCRED: server
// uid or root first, then the root-owned operator registry (09G-W1.1), then
// the legacy server-GID compatibility path for CLI/admin traffic. A uid from
// a request body is never consulted. A malformed registry entry fails closed.
func authorizePeerWithRegistry(conn net.Conn, registry operatorauth.Registry) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("control peer is not a Unix socket")
	}
	var credential *unix.Ucred
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if credential == nil {
		return fmt.Errorf("control peer credentials unavailable")
	}
	// 1. The server's own uid and root are always allowed.
	if int(credential.Uid) == serverUID || credential.Uid == 0 {
		return nil
	}
	// 2. The root-owned operator registry: an authorized uid is allowed even
	// without any kikimora supplementary group; a malformed entry fails
	// closed here.
	authorized, err := registry.IsAuthorized(credential.Uid)
	if err != nil {
		return fmt.Errorf("operator registry check for uid %d: %w", credential.Uid, err)
	}
	if authorized {
		return nil
	}

	// 3. The Unix socket itself is 0660 and owned by the core's effective
	// group. SO_PEERCRED exposes only the peer's effective GID, not
	// supplementary groups, so explicitly verify the connecting process'
	// supplementary groups against the server effective GID (CLI/admin
	// compatibility).
	serverGID := uint32(serverEGID)
	if credential.Gid == serverGID {
		return nil
	}
	groups, err := readPeerGroups(credential.Pid)
	if err != nil {
		return fmt.Errorf("read control peer groups: %w", err)
	}
	for _, gid := range groups {
		if gid == serverGID {
			return nil
		}
	}
	// 4. Reject.
	return fmt.Errorf("control peer uid %d is not authorized", credential.Uid)
}
