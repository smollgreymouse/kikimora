//go:build linux

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
	groups, err := os.Getgroups()
	if err != nil {
		return err
	}
	for _, gid := range groups {
		if uint32(gid) == credential.Gid {
			return nil
		}
	}
	return fmt.Errorf("control peer uid %d is not authorized", credential.Uid)
}
