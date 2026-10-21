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
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// wintunTunnelType groups every adapter this code owns so the installer can
// enumerate and retire them; it never overlaps with other products.
const wintunTunnelType = "Kikimora Toad"

// maxWindowsTunnelName bounds adapter names; Wintun rejects anything longer.
const maxWindowsTunnelName = 32

// platform seams for deterministic tests.
var (
	newTunnelAdapter = func(name string, guid *windows.GUID) (*wintun.Adapter, error) {
		return wintun.CreateAdapter(name, wintunTunnelType, guid)
	}
	setTunnelAddresses = func(luid winipcfg.LUID, addresses []netip.Prefix) error {
		return luid.SetIPAddresses(addresses)
	}
	// addressRetryDelay is a seam so tests exercise the retry loop without
	// paying the real backoff.
	addressRetryDelay = addressApplyTimeout / addressApplyAttempts
)

// addressApplyAttempts bounds how long a freshly created adapter is given to
// accept its address configuration. Windows can answer ERROR_NOT_SUPPORTED
// for a short window while the interface is still being published, and a
// failure here is fatal for the whole tunnel, so a bounded retry is cheaper
// than a role restart.
const (
	addressApplyAttempts = 5
	addressApplyTimeout  = 2 * time.Second
)

// applyTunnelAddresses retries the address configuration a bounded number of
// times: a freshly created Wintun interface can answer ERROR_NOT_SUPPORTED
// while Windows is still publishing it, and a failure here would abort the
// whole tunnel and cost the role a restart.
func applyTunnelAddresses(luid winipcfg.LUID, addresses []netip.Prefix) error {
	var err error
	for attempt := 1; attempt <= addressApplyAttempts; attempt++ {
		if err = setTunnelAddresses(luid, addresses); err == nil {
			return nil
		}
		if attempt < addressApplyAttempts {
			time.Sleep(addressRetryDelay)
		}
	}
	return err
}

// windowsTunnel owns one persistent Wintun adapter for a Toad instance. It
// deliberately holds only the adapter handle — no data ring session — because
// the protocol core (amneziawg-go) opens its own session on the same adapter
// through the deterministic role GUID; a second ring session on one adapter
// is impossible, so this split is what makes protocol attachment safe.
type windowsTunnel struct {
	mu      sync.Mutex
	name    string
	adapter *wintun.Adapter
	luid    winipcfg.LUID
	guid    windows.GUID
	index   int
	mtu     int
	closed  bool
}

// CreateTunnel creates (or deterministically reopens) the role-owned Wintun
// adapter and applies the address configuration.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	if err := validateWindowsTunnelSpec(spec); err != nil {
		return nil, err
	}

	guid := tunnelGUID(spec.Name)
	adapter, err := newTunnelAdapter(spec.Name, &guid)
	if err != nil {
		return nil, fmt.Errorf("create Wintun adapter %q: %w", spec.Name, err)
	}

	tunnel := &windowsTunnel{
		name:    spec.Name,
		adapter: adapter,
		guid:    guid,
		luid:    winipcfg.LUID(adapter.LUID()),
		mtu:     spec.MTU,
	}
	if err := tunnel.configure(spec.Addresses); err != nil {
		_ = adapter.Close()
		return nil, err
	}
	return tunnel, nil
}

func (t *windowsTunnel) configure(addresses []netip.Prefix) error {
	link, err := net.InterfaceByName(t.name)
	if err != nil {
		return fmt.Errorf("look up created adapter %q: %w", t.name, err)
	}
	if link.Index <= 0 {
		return fmt.Errorf("created adapter %q has invalid interface index %d", t.name, link.Index)
	}
	t.index = link.Index
	if len(addresses) > 0 {
		if err := applyTunnelAddresses(t.luid, addresses); err != nil {
			return fmt.Errorf("configure addresses on %q after %d attempts: %w", t.name, addressApplyAttempts, err)
		}
	}
	return nil
}

func (t *windowsTunnel) Name() string {
	return t.name
}

func (t *windowsTunnel) IfIndex() int {
	return t.index
}

// MTU reports the spec MTU; the Wintun adapter itself is reported at its
// driver default and the protocol core applies the effective packet MTU.
func (t *windowsTunnel) MTU() int {
	return t.mtu
}

// TunnelGUID exposes the deterministic adapter identity for the protocol
// core attachment path (windows-only contract).
func (t *windowsTunnel) TunnelGUID() windows.GUID {
	return t.guid
}

// ReleaseAdapter closes the adapter handle so an external protocol core
// (amneziawg-go) can recreate the adapter by GUID and own both the handle
// and the ring session. The tunnel retains its cached LUID and interface
// index so routing continues to work on the recreated adapter.
func (t *windowsTunnel) ReleaseAdapter() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.adapter == nil {
		return nil
	}
	if err := t.adapter.Close(); err != nil {
		return fmt.Errorf("releasing Wintun adapter %q: %w", t.name, err)
	}
	t.adapter = nil
	return nil
}

// Close releases the adapter handle. The persistent adapter is identified by
// the deterministic role GUID and is reused by the next owner or protocol
// session; retiring adapters of removed roles belongs to the installer.
func (t *windowsTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	if t.adapter != nil {
		if err := t.adapter.Close(); err != nil {
			return fmt.Errorf("closing Wintun adapter %q: %w", t.name, err)
		}
		t.adapter = nil
	}
	t.closed = true
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
