//go:build darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	utunControlName = "com.apple.net.utun_control"
	utunOptIfName   = 2
)

type darwinTunnel struct {
	mu     sync.Mutex
	file   *os.File
	name   string
	index  int
	mtu    int
	closed bool
}

// CreateTunnel owns a kernel utun control socket for the lifetime of the Toad.
// The interface disappears when the owner fd closes, matching the Linux TUN
// ownership contract and preventing stale interfaces after a crash.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	if err := validateDarwinTunnelSpec(spec); err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 0)
	if err != nil {
		return nil, fmt.Errorf("open macOS utun control socket: %w", err)
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	var ctl unix.CtlInfo
	copy(ctl.Name[:], utunControlName)
	if err := unix.IoctlCtlInfo(fd, &ctl); err != nil {
		return nil, fmt.Errorf("find macOS utun control: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrCtl{ID: ctl.Id, Unit: 0}); err != nil {
		return nil, fmt.Errorf("create macOS utun interface: %w", err)
	}
	name, err := unix.GetsockoptString(fd, unix.AF_SYS_CONTROL, utunOptIfName)
	if err != nil || name == "" {
		return nil, fmt.Errorf("read macOS utun interface name: %w", err)
	}
	if err := configureDarwinTunnel(spec, name); err != nil {
		return nil, err
	}
	link, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("lookup created macOS utun %q: %w", name, err)
	}
	closeFD = false
	return &darwinTunnel{file: os.NewFile(uintptr(fd), name), name: name, index: link.Index, mtu: spec.MTU}, nil
}

func validateDarwinTunnelSpec(spec TunnelSpec) error {
	if spec.MTU < 576 || spec.MTU > 9000 {
		return fmt.Errorf("MTU must be in range 576..9000, got %d", spec.MTU)
	}
	for _, prefix := range spec.Addresses {
		if !prefix.IsValid() {
			return errors.New("tunnel address prefix is invalid")
		}
	}
	return nil
}

func configureDarwinTunnel(spec TunnelSpec, name string) error {
	if err := runIfconfig(name, "mtu", fmt.Sprint(spec.MTU)); err != nil {
		return fmt.Errorf("set macOS utun MTU: %w", err)
	}
	for _, prefix := range spec.Addresses {
		family := "inet6"
		if prefix.Addr().Is4() {
			family = "inet"
		}
		args := []string{family, prefix.Addr().String()}
		if family == "inet6" {
			args = append(args, "prefixlen", strconv.Itoa(prefix.Bits()))
		} else {
			args = append(args, prefix.Addr().String())
		}
		args = append(args, "alias")
		if err := runIfconfig(name, args...); err != nil {
			return fmt.Errorf("assign macOS utun address %s: %w", prefix, err)
		}
	}
	if err := runIfconfig(name, "up"); err != nil {
		return fmt.Errorf("bring macOS utun up: %w", err)
	}
	return nil
}

func runIfconfig(name string, args ...string) error {
	command := append([]string{name}, args...)
	output, err := exec.CommandContext(context.Background(), "ifconfig", command...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifconfig %s: %w (%s)", strings.Join(command, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (t *darwinTunnel) Name() string { return t.name }
func (t *darwinTunnel) IfIndex() int { return t.index }
func (t *darwinTunnel) MTU() int     { return t.mtu }

func (t *darwinTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if t.file == nil {
		return nil
	}
	err := t.file.Close()
	t.file = nil
	return err
}
