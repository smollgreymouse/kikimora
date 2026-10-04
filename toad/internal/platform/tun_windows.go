//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	wgdevice "golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// wintunTunnelType groups every adapter this code owns so the installer can
// enumerate and retire them; it never overlaps with other products.
const wintunTunnelType = "Kikimora Toad"

// maxWindowsTunnelName bounds adapter names; Wintun rejects anything longer.
const maxWindowsTunnelName = 32

// platform seams for deterministic tests.
var (
	newTunnelDevice = func(name string, guid *windows.GUID, mtu int) (tunnelDevice, error) {
		device, err := wgdevice.CreateTUNWithRequestedGUID(name, guid, mtu)
		if err != nil {
			return nil, err
		}
		luidProvider, ok := device.(interface{ LUID() uint64 })
		if !ok {
			return nil, errors.New("wintun device does not expose its LUID")
		}
		return &wireguardDevice{device: device, luid: luidProvider.LUID()}, nil
	}
	setTunnelAddresses = func(luid winipcfg.LUID, addresses []netip.Prefix) error {
		return luid.SetIPAddresses(addresses)
	}
)

// wireguardDevice adapts wireguard-go's wintun Device to the owner contract.
type wireguardDevice struct {
	device wgdevice.Device
	luid   uint64
}

func (d *wireguardDevice) Name() (string, error) { return d.device.Name() }
func (d *wireguardDevice) Close() error          { return d.device.Close() }
func (d *wireguardDevice) LUID() uint64          { return d.luid }

// tunnelDevice is the slice of wireguard-go's wintun Device the owner needs.
type tunnelDevice interface {
	Name() (string, error)
	Close() error
	LUID() uint64
}

// windowsTunnel owns the Wintun adapter session for one Toad instance. The
// adapter is persistent and identified by a name-derived deterministic GUID,
// so repeated recovery reuses the same adapter and never accumulates
// duplicates; Close releases the data session and the adapter stays
// addressable for the next owner.
type windowsTunnel struct {
	mu     sync.Mutex
	name   string
	device tunnelDevice
	luid   winipcfg.LUID
	index  int
	mtu    int
	closed bool
}

// CreateTunnel creates (or deterministically reopens) the role-owned Wintun
// adapter and applies the MTU and address configuration.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	if err := validateWindowsTunnelSpec(spec); err != nil {
		return nil, err
	}

	guid := tunnelGUID(spec.Name)
	device, err := newTunnelDevice(spec.Name, &guid, spec.MTU)
	if err != nil {
		return nil, fmt.Errorf("create Wintun adapter %q: %w", spec.Name, err)
	}

	tunnel := &windowsTunnel{
		name:   spec.Name,
		device: device,
		luid:   winipcfg.LUID(device.LUID()),
		mtu:    spec.MTU,
	}
	if err := tunnel.configure(spec.Addresses); err != nil {
		_ = device.Close()
		return nil, err
	}
	return tunnel, nil
}

func (t *windowsTunnel) configure(addresses []netip.Prefix) error {
	index, err := interfaceIndexOf(t.name, t.luid)
	if err != nil {
		return err
	}
	t.index = index
	if len(addresses) > 0 {
		if err := setTunnelAddresses(t.luid, addresses); err != nil {
			return fmt.Errorf("configure addresses on %q: %w", t.name, err)
		}
	}
	return nil
}

func interfaceIndexOf(name string, luid winipcfg.LUID) (int, error) {
	link, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("look up created adapter %q: %w", name, err)
	}
	if link.Index <= 0 {
		return 0, fmt.Errorf("created adapter %q has invalid interface index %d", name, link.Index)
	}
	return link.Index, nil
}

func (t *windowsTunnel) Name() string {
	return t.name
}

func (t *windowsTunnel) IfIndex() int {
	return t.index
}

func (t *windowsTunnel) MTU() int {
	return t.mtu
}

// Close releases the adapter data session. The persistent adapter itself is
// identified by the deterministic role GUID and is reused by the next owner;
// retiring adapters of removed roles belongs to the installer.
func (t *windowsTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	if err := t.device.Close(); err != nil {
		return fmt.Errorf("closing Wintun session for %q: %w", t.name, err)
	}
	t.closed = true
	t.device = nil
	return nil
}

// validateWindowsTunnelSpec enforces the Wintun adapter name contract before
// any driver access so invalid specs fail deterministically and unprivileged.
func validateWindowsTunnelSpec(spec TunnelSpec) error {
	if spec.Name == "" {
		return errors.New("tunnel name cannot be empty")
	}
	if strings.IndexByte(spec.Name, 0) >= 0 {
		return errors.New("tunnel name cannot contain NUL")
	}
	if strings.TrimSpace(spec.Name) != spec.Name {
		return fmt.Errorf("tunnel name %q must not start or end with whitespace", spec.Name)
	}
	if len(spec.Name) > maxWindowsTunnelName {
		return fmt.Errorf("tunnel name %q is too long: Wintun adapter names are at most %d characters", spec.Name, maxWindowsTunnelName)
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

// tunnelGUID derives the stable adapter identity from the role-owned name.
// The same role always maps to the same adapter across restarts, upgrades and
// reboots, and two roles can never collide.
func tunnelGUID(name string) windows.GUID {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("kikimora-toad:"+name))
	var guid windows.GUID
	guid.Data1 = binary.BigEndian.Uint32(id[0:4])
	guid.Data2 = binary.BigEndian.Uint16(id[4:6])
	guid.Data3 = binary.BigEndian.Uint16(id[6:8])
	copy(guid.Data4[:], id[8:16])
	return guid
}
