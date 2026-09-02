//go:build linux

package platform

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"unsafe"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// linuxTunnel represents a Linux TUN device.
type linuxTunnel struct {
	name   string
	iface  netlink.Link
	file   *os.File
	mtu    int
	index  int
	closed bool
}

// CreateTunnel creates a TUN device on Linux.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	if spec.Name == "" {
		return nil, errors.New("tunnel name cannot be empty")
	}
	if spec.MTU <= 0 {
		return nil, errors.New("MTU must be positive")
	}

	// Open /dev/net/tun
	tunFile, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening /dev/net/tun: %w", err)
	}

	// Set up TUN interface using unix.NewIfreq for validation and creation
	ifreq, err := unix.NewIfreq(spec.Name)
	if err != nil {
		tunFile.Close()
		return nil, fmt.Errorf("creating ifreq: %w", err)
	}
	ifreq.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, tunFile.Fd(), unix.TUNSETIFF, uintptr(unsafe.Pointer(&ifreq))); errno != 0 {
		tunFile.Close()
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Get link by name
	iface, err := netlink.LinkByName(spec.Name)
	if err != nil {
		tunFile.Close()
		return nil, fmt.Errorf("getting link by name: %w", err)
	}

	// Set MTU
	if err := netlink.LinkSetMTU(iface, spec.MTU); err != nil {
		tunFile.Close()
		return nil, fmt.Errorf("setting MTU: %w", err)
	}

	// Add addresses
	for _, addr := range spec.Addresses {
		var mask net.IPMask
		if addr.Addr().Is4() {
			mask = net.CIDRMask(addr.Bits(), 32)
		} else {
			mask = net.CIDRMask(addr.Bits(), 128)
		}
		netAddr := &netlink.Addr{IPNet: &net.IPNet{
			IP:   addr.Addr().AsSlice(),
			Mask: mask,
		}}
		if err := netlink.AddrAdd(iface, netAddr); err != nil {
			tunFile.Close()
			return nil, fmt.Errorf("adding address %s: %w", addr.String(), err)
		}
	}

	// Set interface up
	if err := netlink.LinkSetUp(iface); err != nil {
		tunFile.Close()
		return nil, fmt.Errorf("setting interface up: %w", err)
	}

	// Get interface index
	index := iface.Attrs().Index

	return &linuxTunnel{
		name:  spec.Name,
		iface: iface,
		file:  tunFile,
		mtu:   spec.MTU,
		index: index,
	}, nil
}

func (t *linuxTunnel) Name() string {
	return t.name
}

func (t *linuxTunnel) IfIndex() int {
	return t.index
}

func (t *linuxTunnel) MTU() int {
	return t.mtu
}

func (t *linuxTunnel) DuplicateFile() (*os.File, error) {
	if t.closed {
		return nil, errors.New("tunnel is closed")
	}
	// Duplicate the file descriptor with close-on-exec
	fd, err := unix.Dup(int(t.file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicating file descriptor: %w", err)
	}
	// Set close-on-exec flag
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("setting close-on-exec: %w", err)
	}
	// Wrap the raw fd in an *os.File
	file := os.NewFile(uintptr(fd), "")
	return file, nil
}

func (t *linuxTunnel) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	if err := t.file.Close(); err != nil {
		return fmt.Errorf("closing tunnel file: %w", err)
	}
	return nil
}

// Force the use of netip.Prefix to satisfy vet.
var _ netip.Prefix
