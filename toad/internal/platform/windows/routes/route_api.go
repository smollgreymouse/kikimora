//go:build windows

package routes

import (
	"fmt"
	"net/netip"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// windowsRouteApi is the single injected syscall seam for the selected-route
// and managed-park writers (08D1 §38.4): every Get/Create/Delete/Register
// call goes through it, tests inject a fake and assert call order, and the
// package-global monkey-patched function variables retire.
type windowsRouteApi interface {
	// routeTable reads one family's full forwarding table.
	routeTable(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error)
	// createRoute creates one exact row (the caller pre-initializes it).
	createRoute(row *winipcfg.MibIPforwardRow2) error
	// deleteRoute deletes one exact row.
	deleteRoute(row *winipcfg.MibIPforwardRow2) error
	// registerRouteChange registers the NotifyRouteChange2 callback.
	registerRouteChange(callback func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)) (unregister func(), err error)
	// interfaceMetricByLuid reads the effective interface metric (§31.25).
	interfaceMetricByLuid(luid uint64, family winipcfg.AddressFamily) (uint32, error)
	// luidFromIndex converts an interface index to its LUID.
	luidFromIndex(index uint32) (uint64, error)
	// loopbackIndex resolves the current loopback interface index.
	loopbackIndex() uint32
	// unicastAddress resolves one interface's usable source address for
	// on-link next hops.
	unicastAddress(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error)
}

// realWindowsRouteApi wraps winipcfg and the IP Helper API.
type realWindowsRouteApi struct{}

func (realWindowsRouteApi) routeTable(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error) {
	return winipcfg.GetIPForwardTable2(family)
}

func (realWindowsRouteApi) createRoute(row *winipcfg.MibIPforwardRow2) error {
	return row.Create()
}

func (realWindowsRouteApi) deleteRoute(row *winipcfg.MibIPforwardRow2) error {
	return row.Delete()
}

func (realWindowsRouteApi) registerRouteChange(callback func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)) (unregister func(), err error) {
	handle, err := winipcfg.RegisterRouteChangeCallback(callback)
	if err != nil {
		return nil, err
	}
	return func() { _ = handle.Unregister() }, nil
}

func (realWindowsRouteApi) interfaceMetricByLuid(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
	row, err := winipcfg.LUID(luid).IPInterface(family)
	if err != nil {
		return 0, fmt.Errorf("read interface metric for LUID %d: %w", luid, err)
	}
	return row.Metric, nil
}

func (realWindowsRouteApi) luidFromIndex(index uint32) (uint64, error) {
	luid, err := winipcfg.LUIDFromIndex(index)
	if err != nil {
		return 0, err
	}
	return uint64(luid), nil
}

func (realWindowsRouteApi) loopbackIndex() uint32 {
	return loopbackIfIndex()
}

func (realWindowsRouteApi) unicastAddress(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
	addresses, err := winipcfg.GetUnicastIPAddressTable(family)
	if err != nil {
		return netip.Addr{}, err
	}
	for i := range addresses {
		row := &addresses[i]
		if row.InterfaceIndex != ifIndex || row.SkipAsSource || row.DadState != winipcfg.DadStatePreferred {
			continue
		}
		if addr := row.Address.Addr(); addr.IsValid() {
			return addr, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("interface %d has no usable source address", ifIndex)
}

var _ windowsRouteApi = realWindowsRouteApi{}
