//go:build darwin

package control

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func authorizePeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("control peer is not a Unix socket")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Xucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, credentialErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if credential == nil {
		return fmt.Errorf("control peer credentials unavailable")
	}
	if credential.Uid == 0 || int(credential.Uid) == os.Getuid() {
		return nil
	}
	groups, err := os.Getgroups()
	if err != nil {
		return err
	}
	count := int(credential.Ngroups)
	if count < 0 || count > len(credential.Groups) {
		return fmt.Errorf("invalid control peer group count %d", count)
	}
	for _, gid := range groups {
		for _, peerGID := range credential.Groups[:count] {
			if uint32(gid) == peerGID {
				return nil
			}
		}
	}
	return fmt.Errorf("control peer uid %d is not authorized", credential.Uid)
}
