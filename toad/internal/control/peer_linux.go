//go:build linux

package control

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

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
	if int(credential.Uid) == os.Getuid() || credential.Uid == 0 {
		return nil
	}

	// The Unix socket itself is 0660 and owned by the core's effective group.
	// SO_PEERCRED exposes only the peer's effective GID, not supplementary
	// groups, so explicitly verify the connecting process' supplementary groups
	// against the server effective GID.
	serverGID := uint32(os.Getegid())
	if credential.Gid == serverGID {
		return nil
	}
	groups, err := peerSupplementaryGroups(credential.Pid)
	if err != nil {
		return fmt.Errorf("read control peer groups: %w", err)
	}
	for _, gid := range groups {
		if gid == serverGID {
			return nil
		}
	}
	return fmt.Errorf("control peer uid %d is not authorized", credential.Uid)
}
