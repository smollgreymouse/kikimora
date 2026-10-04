//go:build windows

package underlay

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// watchTick re-arms the observer for changes notifications may have missed
// across driver resets and suspend/resume; Monitor dedups via netstate.Compare,
// so redundant sweeps never bump the canonical epoch.
const watchTick = 30 * time.Second

// invalidationBuffer decouples OS callback threads from the invalidation
// stream; overflow is harmless because the monitor re-snapshots on the next
// delivered event or periodic audit.
const invalidationBuffer = 64

// platform seams for deterministic tests.
var (
	routeTable       = winipcfg.GetIPForwardTable2
	unicastTable     = winipcfg.GetUnicastIPAddressTable
	interfaceByIndex = net.InterfaceByIndex
	interfaceAlias   = func(luid winipcfg.LUID) (string, error) {
		row, err := luid.Interface()
		if err != nil {
			return "", err
		}
		return row.Alias(), nil
	}
)

// DefaultSnapshot assembles the canonical underlay snapshot from the native
// IP forwarding tables: the lowest-metric default route per family whose
// interface is not a managed role interface. No default route is the valid
// underlay-unavailable state (nil path), not an error.
func DefaultSnapshot(ctx context.Context, excluded map[string]bool) (netstate.Snapshot, error) {
	ipv4, err := defaultPath(windows.AF_INET, excluded)
	if err != nil {
		return netstate.Snapshot{}, err
	}
	ipv6, err := defaultPath(windows.AF_INET6, excluded)
	if err != nil {
		return netstate.Snapshot{}, err
	}
	return netstate.Snapshot{
		Epoch:      0,
		IPv4:       ipv4,
		IPv6:       ipv6,
		ObservedAt: time.Now(),
	}, nil
}

// defaultPath selects the active default route (0.0.0.0/0 or ::/0) with the
// lowest metric on a non-excluded interface.
func defaultPath(family winipcfg.AddressFamily, excluded map[string]bool) (*netstate.Path, error) {
	routes, err := routeTable(family)
	if err != nil {
		return nil, fmt.Errorf("read IP forwarding table: %w", err)
	}
	var best *winipcfg.MibIPforwardRow2
	var bestAlias string
	for i := range routes {
		row := &routes[i]
		if row.DestinationPrefix.PrefixLength != 0 || !row.DestinationPrefix.Prefix().Addr().IsUnspecified() {
			continue
		}
		alias, err := interfaceAlias(row.InterfaceLUID)
		if err != nil {
			continue
		}
		if excluded[alias] {
			continue
		}
		if best == nil || row.Metric < best.Metric {
			best = row
			bestAlias = alias
		}
	}
	if best == nil {
		return nil, nil
	}

	source, err := preferredSource(family, best.InterfaceLUID)
	if err != nil {
		return nil, err
	}
	mtu := 0
	if link, err := interfaceByIndex(int(best.InterfaceIndex)); err == nil {
		mtu = link.MTU
	}
	return &netstate.Path{
		Family:       familyNumber(family),
		IfIndex:      int(best.InterfaceIndex),
		Interface:    bestAlias,
		Gateway:      best.NextHop.Addr(),
		PreferredSrc: source,
		MTU:          mtu,
		Table:        254,
		Metric:       best.Metric,
	}, nil
}

// preferredSource picks the interface's preferred unicast address: the first
// global address that is not marked SkipAsSource and finished DAD, falling
// back to the first usable address on the interface.
func preferredSource(family winipcfg.AddressFamily, luid winipcfg.LUID) (netip.Addr, error) {
	addresses, err := unicastTable(family)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("read unicast address table: %w", err)
	}
	var fallback netip.Addr
	for i := range addresses {
		row := &addresses[i]
		if row.InterfaceLUID != luid || row.SkipAsSource || row.DadState != winipcfg.DadStatePreferred {
			continue
		}
		addr := row.Address.Addr()
		if !addr.IsValid() {
			continue
		}
		if addr.IsGlobalUnicast() {
			return addr, nil
		}
		if !fallback.IsValid() {
			fallback = addr
		}
	}
	return fallback, nil
}

// DefaultWatch subscribes to the native IP Helper change notifications —
// route, interface and unicast-address (DHCP renewals) — and adds a periodic
// audit for events the callbacks cannot deliver across suspend/resume.
func DefaultWatch(ctx context.Context, out chan<- netstate.Invalidation) error {
	invalidations := make(chan netstate.Invalidation, invalidationBuffer)
	emit := func(source string) {
		select {
		case invalidations <- netstate.Invalidation{Source: source}:
		default:
		}
	}

	routeCallback, err := winipcfg.RegisterRouteChangeCallback(func(_ winipcfg.MibNotificationType, _ *winipcfg.MibIPforwardRow2) {
		emit("windows-route-change")
	})
	if err != nil {
		return fmt.Errorf("subscribe route changes: %w", err)
	}
	defer routeCallback.Unregister()
	interfaceCallback, err := winipcfg.RegisterInterfaceChangeCallback(func(_ winipcfg.MibNotificationType, _ *winipcfg.MibIPInterfaceRow) {
		emit("windows-interface-change")
	})
	if err != nil {
		return fmt.Errorf("subscribe interface changes: %w", err)
	}
	defer interfaceCallback.Unregister()
	addressCallback, err := winipcfg.RegisterUnicastAddressChangeCallback(func(_ winipcfg.MibNotificationType, _ *winipcfg.MibUnicastIPAddressRow) {
		emit("windows-address-change")
	})
	if err != nil {
		return fmt.Errorf("subscribe address changes: %w", err)
	}
	defer addressCallback.Unregister()

	ticker := time.NewTicker(watchTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			emit("windows-periodic-audit")
		case invalidation := <-invalidations:
			select {
			case out <- invalidation:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func familyNumber(family winipcfg.AddressFamily) int {
	if family == winipcfg.AddressFamily(windows.AF_INET6) {
		return 6
	}
	return 4
}
