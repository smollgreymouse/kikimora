//go:build windows

package routes

import (
	"context"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The Windows effective-metric restoration proof (08D1 §31.25): a route's
// preference is its metric offset plus its interface metric, so proving
// that a selected row wins over the loopback park compares effective
// metrics in uint64 arithmetic (never uint32, which wraps at the max).
//
//	selected_effective = uint64(route.Metric) + uint64(interface.Metric)
//	park_effective     = uint64(park.Metric) + uint64(loopback.Metric)

// SelectedWinsOverPark proves, for one same-prefix pair of exact
// identities, that the selected row wins under the Windows route selection
// rules represented by the projection (08D1 §31.25). The proof invalidates
// when the interface metrics change: the observer/audit re-proves it, and a
// changed interface metric never reuses an old winning token. The lookup
// goes through the injected API seam (§38.4).
func (e *Executor) SelectedWinsOverPark(_ context.Context, prefix netip.Prefix, selected routing.SelectedRouteIdentity, park routing.SelectedParkIdentity) (bool, error) {
	family := winipcfg.AddressFamily(windows.AF_INET)
	if !prefix.Addr().Is4() {
		family = winipcfg.AddressFamily(windows.AF_INET6)
	}
	selectedIfMetric, err := e.api.interfaceMetricByLuid(selected.InterfaceLuid, family)
	if err != nil {
		return false, err
	}
	parkIfMetric, err := e.api.interfaceMetricByLuid(park.InterfaceLuid, family)
	if err != nil {
		return false, err
	}
	selectedEffective := uint64(selected.Metric) + uint64(selectedIfMetric)
	parkEffective := uint64(park.Metric) + uint64(parkIfMetric)
	return selectedEffective < parkEffective, nil
}
