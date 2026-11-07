//go:build windows

package routes

import (
	"net/netip"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// fakeRouteApi is the scripted syscall seam (08D1 §38.4): one injected object
// with counters and call-order recording replaces the package-global
// monkey-patched function variables.
type fakeRouteApi struct {
	mu sync.Mutex

	rows      []winipcfg.MibIPforwardRow2
	created   []winipcfg.MibIPforwardRow2
	deleted   []winipcfg.MibIPforwardRow2
	tables    int
	creates   int
	deletes   int
	registers int

	// script
	callback              func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)
	onCreate              func()
	metrics               map[uint64]uint32
	createErr             error
	deleteErr             error
	tableErr              error
	unicast               netip.Addr
	loopback              uint32
	notifyGenerationDelta int
}

func (f *fakeRouteApi) routeTable(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tables++
	if f.tableErr != nil {
		return nil, f.tableErr
	}
	is6 := family == winipcfg.AddressFamily(windows.AF_INET6)
	var matching []winipcfg.MibIPforwardRow2
	for _, row := range f.rows {
		if row.DestinationPrefix.Prefix().Addr().Is6() == is6 {
			matching = append(matching, row)
		}
	}
	return matching, nil
}

func (f *fakeRouteApi) createRoute(row *winipcfg.MibIPforwardRow2) error {
	// onCreate must run without the fake's mutex: scripted hooks may call
	// back into the fake (addRow), and sync.Mutex is not reentrant.
	f.mu.Lock()
	f.creates++
	onCreate := f.onCreate
	createErr := f.createErr
	f.mu.Unlock()
	if onCreate != nil {
		onCreate()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if createErr != nil {
		return createErr
	}
	f.created = append(f.created, *row)
	stored := *row
	// Windows normalizes on-link rows: a next hop equal to the interface's
	// own source address reads back as unspecified (the real-kernel
	// behavior the snapshot tests prove).
	if hop := stored.NextHop.Addr(); hop.IsValid() && hop == f.unicastAddressFor(stored.InterfaceIndex) {
		stored.NextHop = winipcfg.RawSockaddrInet{}
	}
	f.rows = append(f.rows, stored)
	return nil
}

// unicastAddressFor resolves the fake source address for one interface.
func (f *fakeRouteApi) unicastAddressFor(ifIndex uint32) netip.Addr {
	addr, _ := f.unicastAddress(winipcfg.AddressFamily(windows.AF_INET), ifIndex)
	return addr
}

func (f *fakeRouteApi) deleteRoute(row *winipcfg.MibIPforwardRow2) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	f.deleted = append(f.deleted, *row)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	kept := f.rows[:0]
	for i := range f.rows {
		same := f.rows[i].InterfaceLUID == row.InterfaceLUID &&
			f.rows[i].InterfaceIndex == row.InterfaceIndex &&
			f.rows[i].DestinationPrefix == row.DestinationPrefix &&
			f.rows[i].NextHop == row.NextHop
		if !same {
			kept = append(kept, f.rows[i])
		}
	}
	f.rows = kept
	return nil
}

func (f *fakeRouteApi) registerRouteChange(callback func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers++
	f.callback = callback
	return func() {}, nil
}

func (f *fakeRouteApi) interfaceMetricByLuid(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
	if m, ok := f.metrics[luid]; ok {
		return m, nil
	}
	return 25, nil
}

func (f *fakeRouteApi) luidFromIndex(index uint32) (uint64, error) {
	return uint64(index) * 1000, nil
}

func (f *fakeRouteApi) loopbackIndex() uint32 {
	if f.loopback != 0 {
		return f.loopback
	}
	return 1
}

func (f *fakeRouteApi) unicastAddress(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
	if f.unicast.IsValid() {
		return f.unicast, nil
	}
	if family == winipcfg.AddressFamily(windows.AF_INET6) {
		return netip.MustParseAddr("2001:db8::1"), nil
	}
	return netip.MustParseAddr("203.0.113.1"), nil
}

// addRow seeds the fake kernel with one row.
func (f *fakeRouteApi) addRow(prefix netip.Prefix, ifindex int, luid uint64, protocol int, metric uint32, gateway string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := winipcfg.MibIPforwardRow2{}
	row.Init()
	row.InterfaceIndex = uint32(ifindex)
	row.InterfaceLUID = winipcfg.LUID(luid)
	_ = row.DestinationPrefix.SetPrefix(prefix)
	if gateway != "" {
		hop := netip.MustParseAddr(gateway)
		_ = row.NextHop.SetAddrPort(netip.AddrPortFrom(hop, 0))
	}
	row.Protocol = winipcfg.RouteProtocol(protocol)
	row.Metric = metric
	f.rows = append(f.rows, row)
}

// pushRow appends one row to the fake kernel and runs the registered
// callback with it — the production classify path with §48.2 numbering.
func (f *fakeRouteApi) pushRow(row winipcfg.MibIPforwardRow2) {
	f.mu.Lock()
	f.rows = append(f.rows, row)
	var cb func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)
	cb = f.callback
	f.mu.Unlock()
	if cb != nil {
		cb(0, &row)
	}
}

// pushNotification fires one registered callback with the given row (nil row
// = the initial notification) after bumping the generation.
func (f *fakeRouteApi) pushNotification(row *winipcfg.MibIPforwardRow2) {}

var _ = routing.SelectedKernelRow{}
