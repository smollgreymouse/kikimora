//go:build linux

package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	tunnelName    = "kk-toad0"
	tunnelMTU     = 1380
	tunnelAddress = "10.77.0.2/30"
)

type duplicateFiler interface {
	DuplicateFile() (*os.File, error)
}

func main() {
	if len(os.Args) != 3 {
		fatalf("usage: %s READY_FILE RELEASE_FILE", os.Args[0])
	}
	readyFile := os.Args[1]
	releaseFile := os.Args[2]

	tun, err := platform.CreateTunnel(platform.TunnelSpec{
		Name:      tunnelName,
		MTU:       tunnelMTU,
		Addresses: []netip.Prefix{netip.MustParsePrefix(tunnelAddress)},
	})
	if err != nil {
		fatalf("CreateTunnel: %v", err)
	}

	ifindex := tun.IfIndex()
	if ifindex <= 0 {
		fatalf("invalid ifindex %d", ifindex)
	}
	verifyKernelTunnel(ifindex)

	duplicator, ok := tun.(duplicateFiler)
	if !ok {
		fatalf("Linux tunnel does not expose DuplicateFile")
	}
	dupFile, err := duplicator.DuplicateFile()
	if err != nil {
		fatalf("DuplicateFile: %v", err)
	}
	flags, err := unix.FcntlInt(dupFile.Fd(), unix.F_GETFD, 0)
	if err != nil {
		_ = dupFile.Close()
		fatalf("F_GETFD on duplicate: %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		_ = dupFile.Close()
		fatalf("duplicated fd does not have FD_CLOEXEC")
	}
	if err := dupFile.Close(); err != nil {
		fatalf("closing duplicate fd: %v", err)
	}

	// Closing only the duplicate must not affect the owner-held interface.
	verifyKernelTunnel(ifindex)

	if err := os.WriteFile(readyFile, []byte(strconv.Itoa(ifindex)+"\n"), 0o644); err != nil {
		fatalf("writing ready file: %v", err)
	}
	waitForRelease(releaseFile, 30*time.Second)

	if err := tun.Close(); err != nil {
		fatalf("closing owner fd: %v", err)
	}
	if err := tun.Close(); err != nil {
		fatalf("second owner Close must be idempotent: %v", err)
	}
	waitForTunnelGone(2 * time.Second)
}

func verifyKernelTunnel(expectedIfindex int) {
	link, err := netlink.LinkByName(tunnelName)
	if err != nil {
		fatalf("LinkByName(%s): %v", tunnelName, err)
	}
	attrs := link.Attrs()
	if attrs == nil {
		fatalf("%s has nil link attributes", tunnelName)
	}
	if attrs.Index != expectedIfindex {
		fatalf("%s ifindex changed: got %d want %d", tunnelName, attrs.Index, expectedIfindex)
	}
	if attrs.MTU != tunnelMTU {
		fatalf("%s MTU mismatch: got %d want %d", tunnelName, attrs.MTU, tunnelMTU)
	}
	if attrs.Flags&net.FlagUp == 0 {
		fatalf("%s is not UP", tunnelName)
	}

	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		fatalf("AddrList(%s): %v", tunnelName, err)
	}
	for _, addr := range addrs {
		if addr.IPNet != nil && addr.IPNet.String() == tunnelAddress {
			return
		}
	}
	fatalf("%s is missing address %s", tunnelName, tunnelAddress)
}

func waitForRelease(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			fatalf("checking release file: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fatalf("timed out waiting for release file")
}

func waitForTunnelGone(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := netlink.LinkByName(tunnelName)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); ok {
				return
			}
			fatalf("checking removed tunnel: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fatalf("%s still exists after closing the final owner fd", tunnelName)
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
