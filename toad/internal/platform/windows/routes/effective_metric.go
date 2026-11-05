//go:build windows

package routes

import (
	"context"
	"fmt"
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

// ipInterfaceMetric is the test seam for the IP interface metric lookup;
// production points at the real GetIpInterfaceEntry path.
var ipInterfaceMetric = func(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
	row, err := winipcfg.LUID(luid).IPInterface(family)
	if err != nil {
		return 0, fmt.Errorf("read interface metric for LUID %d: %w", luid, err)
	}
	return row.Metric, nil
}

// interfaceMetricByLuid reads the exact interface metric for one LUID and
// family.
func interfaceMetricByLuid(luid uint64, family winipcfg.AddressFamily) (uint32, error) {
	return ipInterfaceMetric(luid, family)
}

// SelectedWinsOverPark proves, for one same-prefix pair of exact
// identities, that the selected row wins under the Windows route selection
// rules represented by the projection (08D1 §31.25). The proof invalidates
// when the interface metrics change: the observer/audit re-proves it, and a
// changed interface metric never reuses an old winning token.
func (e *Executor) SelectedWinsOverPark(_ context.Context, prefix netip.Prefix, selected routing.SelectedRouteIdentity, park routing.SelectedParkIdentity) (bool, error) {
	family := winipcfg.AddressFamily(windows.AF_INET)
	if !prefix.Addr().Is4() {
		family = winipcfg.AddressFamily(windows.AF_INET6)
	}
	selectedIfMetric, err := interfaceMetricByLuid(selected.InterfaceLuid, family)
	if err != nil {
		return false, err
	}
	parkIfMetric, err := interfaceMetricByLuid(park.InterfaceLuid, family)
	if err != nil {
		return false, err
	}
	selectedEffective := uint64(selected.Metric) + uint64(selectedIfMetric)
	parkEffective := uint64(park.Metric) + uint64(parkIfMetric)
	return selectedEffective < parkEffective, nil
}
