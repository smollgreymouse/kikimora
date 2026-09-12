//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// linuxTunnel owns the file descriptor that keeps a Linux TUN interface alive.
type linuxTunnel struct {
	mu     sync.Mutex
	name   string
	file   *os.File
	mtu    int
	index  int
	closed bool
}

// CreateTunnel creates and configures a Linux TUN interface in the caller's
// current network namespace. The returned Tunnel owns the fd that keeps the
// non-persistent interface alive.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	if err := validateLinuxTunnelSpec(spec); err != nil {
		return nil, err
	}

	tunFile, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening /dev/net/tun: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = tunFile.Close()
		}
	}()

	ifreq, err := unix.NewIfreq(spec.Name)
	if err != nil {
		return nil, fmt.Errorf("creating TUN ifreq for %q: %w", spec.Name, err)
	}
	ifreq.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(int(tunFile.Fd()), unix.TUNSETIFF, ifreq); err != nil {
		return nil, fmt.Errorf("ioctl TUNSETIFF for %q: %w", spec.Name, err)
	}

	actualName := ifreq.Name()
	link, err := netlink.LinkByName(actualName)
	if err != nil {
		return nil, fmt.Errorf("getting created TUN %q: %w", actualName, err)
	}
	if err := netlink.LinkSetMTU(link, spec.MTU); err != nil {
		return nil, fmt.Errorf("setting MTU %d on %q: %w", spec.MTU, actualName, err)
	}
	for _, prefix := range spec.Addresses {
		addr, err := netlink.ParseAddr(prefix.String())
		if err != nil {
			return nil, fmt.Errorf("converting address %s for %q: %w", prefix, actualName, err)
		}
		if err := netlink.AddrAdd(link, addr); err != nil {
			return nil, fmt.Errorf("adding address %s to %q: %w", prefix, actualName, err)
		}
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("setting %q up: %w", actualName, err)
	}

	attrs := link.Attrs()
	if attrs == nil || attrs.Index <= 0 {
		return nil, fmt.Errorf("created TUN %q has invalid link attributes", actualName)
	}

	closeOnError = false
	return &linuxTunnel{
		name:  actualName,
		file:  tunFile,
		mtu:   spec.MTU,
		index: attrs.Index,
	}, nil
}

func validateLinuxTunnelSpec(spec TunnelSpec) error {
	if spec.Name == "" {
		return errors.New("tunnel name cannot be empty")
	}
	if strings.IndexByte(spec.Name, 0) >= 0 {
		return errors.New("tunnel name cannot contain NUL")
	}
	if len(spec.Name) >= unix.IFNAMSIZ {
		return fmt.Errorf("tunnel name %q is too long: Linux interface names are at most %d bytes", spec.Name, unix.IFNAMSIZ-1)
	}
	if spec.MTU <= 0 {
		return errors.New("MTU must be positive")
	}
	for _, prefix := range spec.Addresses {
		if !prefix.IsValid() {
			return errors.New("tunnel address prefix is invalid")
		}
	}
	return nil
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

// DuplicateFile returns an independently-owned duplicate of the Toad owner fd.
// The returned fd is created atomically with FD_CLOEXEC and may be handed to a
// protocol core without transferring ownership of the original TUN fd.
func (t *linuxTunnel) DuplicateFile() (*os.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.file == nil {
		return nil, errors.New("tunnel is closed")
	}

	fd, err := unix.FcntlInt(t.file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("duplicating TUN fd with close-on-exec: %w", err)
	}
	// Go decides whether an os.File can use the runtime poller when NewFile is
	// called. Match amneziawg-go's own supplied-fd path and set O_NONBLOCK on
	// the raw descriptor before wrapping it, otherwise reads surface EAGAIN.
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("setting duplicated TUN fd nonblocking: %w", err)
	}
	file := os.NewFile(uintptr(fd), t.name+"-dup")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("wrapping duplicated TUN fd")
	}
	return file, nil
}

func (t *linuxTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	if t.file == nil {
		t.closed = true
		return nil
	}
	if err := t.file.Close(); err != nil {
		return fmt.Errorf("closing TUN owner fd: %w", err)
	}
	t.closed = true
	t.file = nil
	return nil
}
