//go:build linux

package netlink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The reconcile-result and invalidation types live in routing to keep the
// import graph acyclic; the platform contract surfaces them under the
// platform names.

// The managed-park surface (08D1 §8): deterministic RTN_UNREACHABLE rows in
// the main table with RTPROT_STATIC and ManagedParkMetric, transactional
// exactly like the selected routes. Everything runs under kernelMu so park
// and selected mutations never interleave.

// parkRowFor builds the netlink request for one exact park identity.
func parkRowFor(prefix netip.Prefix, identity routing.SelectedParkIdentity) netlink.Route {
	mask := net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())
	route := netlink.Route{
		Dst:      &net.IPNet{IP: prefix.Addr().AsSlice(), Mask: mask},
		Table:    identity.Table,
		Type:     rtnUnreachable,
		Protocol: netlink.RouteProtocol(identity.Protocol),
		Priority: int(identity.Metric),
		Scope:    netlink.Scope(identity.Scope),
	}
	return route
}

// NormalizeManagedPark builds the deterministic RTN_UNREACHABLE identity for
// the prefix without mutation (08D1 §8): table 254, RTPROT_STATIC,
// ManagedParkMetric, no interface, no next hop, and the same scope the
// actual netlink route builder writes.
func (Executor) NormalizeManagedPark(_ context.Context, prefix netip.Prefix) (routing.SelectedParkIdentity, error) {
	if !prefix.IsValid() || !routing.IsCanonicalPrefix(prefix) {
		return routing.SelectedParkIdentity{}, fmt.Errorf("park prefix %q is not canonical", prefix)
	}
	identity := routing.SelectedParkIdentity{
		Kind:     routing.SelectedParkUnreachable,
		Table:    selectedRouteTable,
		Protocol: selectedRouteProtocolLinux,
		Metric:   routing.ManagedParkMetric,
		Scope:    int(netlink.Scope(unix.RT_SCOPE_UNIVERSE)),
	}
	if err := identity.Validate(prefix); err != nil {
		return routing.SelectedParkIdentity{}, err
	}
	return identity, nil
}

// PreflightManagedPark applies the independent park-state rules (08D1 §4.2)
// against the exact prefix rows.
func (Executor) PreflightManagedPark(_ context.Context, prefix netip.Prefix, desired routing.SelectedParkIdentity, park *routing.SelectedPark) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	rows, err := selectedRouteRows(familyOf(prefix), prefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		projected := selectedKernelRow(row, prefix)
		switch projected.Kind {
		case routing.SelectedKernelForeign:
			return fmt.Errorf("park preflight %s: foreign row: %s", prefix, projected.Description)
		case routing.SelectedKernelRoute:
			return fmt.Errorf("park preflight %s: a selected/forwarding row occupies the prefix: %s", prefix, projected.Description)
		case routing.SelectedKernelPark:
			if parkRowAuthorizedForPreflight(projected.Park, park) {
				continue
			}
			return fmt.Errorf("park preflight %s: unauthorized park row %v", prefix, projected.Park)
		}
	}
	_ = desired
	return nil
}

// parkRowAuthorizedForPreflight mirrors the planner's park recognition for
// the preflight boundary (§3, §4.2): exact Park.Desired is allowed only once
// Applying authorized the mutation; previous Owned stays recognized through
// Pending.
func parkRowAuthorizedForPreflight(row routing.SelectedParkIdentity, park *routing.SelectedPark) bool {
	if park == nil {
		return false
	}
	switch park.State {
	case routing.SelectedParkPending:
		// Exact previous Owned allowed; a distinct Park.Desired is not
		// allowed before Applying (the mutation is not authorized).
		return park.Owned != nil && row.Equal(*park.Owned)
	case routing.SelectedParkApplying:
		return row.Equal(park.Desired) || (park.Owned != nil && row.Equal(*park.Owned))
	case routing.SelectedParkActive:
		return park.Owned != nil && row.Equal(*park.Owned)
	case routing.SelectedParkRetiring:
		if park.Owned != nil && row.Equal(*park.Owned) {
			return true
		}
		return park.RetireDesired && row.Equal(park.Desired)
	default:
		return false
	}
}

// ReconcileManagedPark is called only with a ParkApplying record (08D1 §8):
// it re-reads the prefix, permits exact Park.Desired and exact previous
// Park.Owned only, creates/replaces the unreachable row, requires exact
// read-back, and removes stale previous Park.Owned only after Desired is
// proven. EEXIST is re-read and classified, never unconditional success.
func (Executor) ReconcileManagedPark(_ context.Context, _ string, prefix netip.Prefix, park *routing.SelectedPark) (routing.SelectedParkReconcileResult, error) {
	if park == nil {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("reconcile managed park %s: no park transaction", prefix)
	}
	if park.State != routing.SelectedParkApplying {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("reconcile managed park %s requires park state applying, got %q", prefix, park.State)
	}
	kernelMu.Lock()
	defer kernelMu.Unlock()

	if !prefix.IsValid() || !routing.IsCanonicalPrefix(prefix) {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("park prefix %q is not canonical", prefix)
	}
	if err := park.Desired.Validate(prefix); err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	family := familyOf(prefix)
	rows, err := selectedRouteRows(family, prefix)
	if err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	mutated := false
	desiredPresent := false
	staleOwned := false
	for _, row := range rows {
		projected := selectedKernelRow(row, prefix)
		switch {
		case projected.Kind == routing.SelectedKernelForeign:
			return routing.SelectedParkReconcileResult{}, fmt.Errorf("park reconcile %s: foreign row: %s", prefix, projected.Description)
		case projected.Kind == routing.SelectedKernelRoute:
			return routing.SelectedParkReconcileResult{}, fmt.Errorf("park reconcile %s: a forwarding row occupies the prefix: %s", prefix, projected.Description)
		case projected.Kind == routing.SelectedKernelPark:
			switch {
			case projected.Park.Equal(park.Desired):
				desiredPresent = true
			case park.Owned != nil && projected.Park.Equal(*park.Owned):
				staleOwned = true
			default:
				return routing.SelectedParkReconcileResult{}, fmt.Errorf("park reconcile %s: unauthorized park row %v", prefix, projected.Park)
			}
		}
	}
	if !desiredPresent {
		route := parkRowFor(prefix, park.Desired)
		if err := netlink.RouteAdd(&route); err != nil && !errors.Is(err, syscall.EEXIST) && !errors.Is(err, syscall.EBUSY) {
			return routing.SelectedParkReconcileResult{}, err
		}
		mutated = true
	}
	// Stale previous Owned is removed only after Desired is proven.
	if staleOwned && park.Owned != nil && !park.Owned.Equal(park.Desired) {
		rows, err = selectedRouteRows(family, prefix)
		if err != nil {
			return routing.SelectedParkReconcileResult{}, err
		}
		for _, row := range rows {
			projected := selectedKernelRow(row, prefix)
			if projected.Kind == routing.SelectedKernelPark && projected.Park.Equal(*park.Owned) {
				if err := netlink.RouteDel(&row); err != nil && !os.IsNotExist(err) {
					return routing.SelectedParkReconcileResult{}, err
				}
				mutated = true
				break
			}
		}
	}
	// Exact read-back: exactly one Desired park row and nothing else.
	rows, err = selectedRouteRows(family, prefix)
	if err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	for _, row := range rows {
		projected := selectedKernelRow(row, prefix)
		if projected.Kind == routing.SelectedKernelPark && projected.Park.Equal(park.Desired) {
			return routing.SelectedParkReconcileResult{Mutated: mutated, Verified: projected.Park}, nil
		}
	}
	return routing.SelectedParkReconcileResult{}, fmt.Errorf("park %s is not exactly present after the apply", prefix)
}

// ObserveManagedPark compares an expected persisted identity exactly
// (08D1 §8): it never recomputes a current identity.
func (Executor) ObserveManagedPark(_ context.Context, prefix netip.Prefix, expected routing.SelectedParkIdentity) (bool, error) {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	rows, err := selectedRouteRows(familyOf(prefix), prefix)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		projected := selectedKernelRow(row, prefix)
		if projected.Kind == routing.SelectedKernelPark && projected.Park.Equal(expected) {
			return true, nil
		}
	}
	return false, nil
}

// RemoveManagedPark deletes only identities authorized by the persisted
// park transaction (08D1 §8): Pending performs no delete; Applying may
// delete exact Desired and previous Owned; Active deletes exact Owned;
// Retiring deletes Owned plus Desired only when RetireDesired=true. Every
// candidate is re-read and identity-checked; a different same-prefix row is
// untouched; absent authorized identities are success.
func (Executor) RemoveManagedPark(_ context.Context, _ string, prefix netip.Prefix, park routing.SelectedPark) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()

	candidates := make([]routing.SelectedParkIdentity, 0, 2)
	switch park.State {
	case routing.SelectedParkPending:
		// The park mutation was never authorized: nothing to delete.
		return nil
	case routing.SelectedParkApplying:
		if park.Owned != nil {
			candidates = append(candidates, *park.Owned)
		}
		candidates = append(candidates, park.Desired)
	case routing.SelectedParkActive:
		if park.Owned == nil {
			return fmt.Errorf("active park %s has no owned identity", prefix)
		}
		candidates = append(candidates, *park.Owned)
	case routing.SelectedParkRetiring:
		if park.Owned != nil {
			candidates = append(candidates, *park.Owned)
		}
		if park.RetireDesired {
			candidates = append(candidates, park.Desired)
		}
	default:
		return fmt.Errorf("park %s has an unknown state %q", prefix, park.State)
	}

	rows, err := selectedRouteRows(familyOf(prefix), prefix)
	if err != nil {
		return err
	}
	deleted := make(map[routing.SelectedParkIdentity]bool, len(candidates))
	for _, candidate := range candidates {
		if deleted[candidate] {
			continue
		}
		deleted[candidate] = true
		for _, row := range rows {
			projected := selectedKernelRow(row, prefix)
			if projected.Kind != routing.SelectedKernelPark || !projected.Park.Equal(candidate) {
				continue
			}
			if err := netlink.RouteDel(&row); err != nil && !os.IsNotExist(err) {
				return err
			}
			break
		}
	}
	// Verify all authorized identities are absent.
	rows, err = selectedRouteRows(familyOf(prefix), prefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		projected := selectedKernelRow(row, prefix)
		if projected.Kind != routing.SelectedKernelPark {
			continue
		}
		for _, candidate := range candidates {
			if projected.Park.Equal(candidate) {
				return fmt.Errorf("park %s identity %v is still present after the delete", prefix, candidate)
			}
		}
	}
	return nil
}

// SnapshotSelectedRoutes performs one live full kernel snapshot over both
// families (08D1 §4.3): every main-table row projects to its exact class —
// selected rows, parks and foreign rows — grouped by canonical prefix.
// The audit calls it exactly once per cycle.
func (Executor) SnapshotSelectedRoutes(ctx context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	out := make(map[netip.Prefix][]routing.SelectedKernelRow)
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		// The main-table family dump is the full snapshot the audit owns;
		// request paths use targeted prefix reads instead.
		rows, err := netlink.RouteListFiltered(family, &netlink.Route{Table: selectedRouteTable}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Dst == nil {
				continue
			}
			prefix, ok := prefixFromIPNet(row.Dst.IP, row.Dst.Mask)
			if !ok {
				continue
			}
			prefix = prefix.Masked()
			projected := selectedKernelRow(row, prefix)
			out[prefix] = append(out[prefix], projected)
		}
	}
	return out, nil
}

// selectedObserver is the process-wide selected-route subscription so
// repeated Subscribe calls do not multiply netlink sockets.
var selectedObserver = struct {
	mu      sync.Mutex
	publish func(routing.SelectedRouteInvalidation)
}{}

// SubscribeSelectedRouteInvalidations subscribes the netlink route and link
// groups (both families) and emits classified invalidations (08D1 §4). The
// debouncer and observation sequence live in the reconciler (§10, §31.9).
func (Executor) SubscribeSelectedRouteInvalidations(ctx context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	out := make(chan routing.SelectedRouteInvalidation, 64)
	routes := make(chan netlink.RouteUpdate, 64)
	links := make(chan netlink.LinkUpdate, 64)
	done := make(chan struct{})
	if err := netlink.RouteSubscribe(routes, done); err != nil {
		close(out)
		return nil, err
	}
	if err := netlink.LinkSubscribe(links, done); err != nil {
		close(done)
		close(out)
		return nil, err
	}
	go func() {
		defer close(out)
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-routes:
				if !ok {
					return
				}
				invalidation, ok := classifyRouteUpdate(update)
				if !ok {
					continue
				}
				select {
				case out <- invalidation:
				case <-ctx.Done():
					return
				}
			case update, ok := <-links:
				if !ok {
					return
				}
				select {
				case out <- routing.SelectedRouteInvalidation{
					Kind:    routing.SelectedInvalidateInterface,
					IfIndex: update.Link.Attrs().Index,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// prefixFromIPNet converts an IPNet mask pair onto a canonical netip.Prefix.
func prefixFromIPNet(ip net.IP, mask net.IPMask) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, bits := mask.Size()
	if bits == 0 || ones < 0 || ones > bits {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr.Unmap(), ones), true
}

// classifyRouteUpdate maps one netlink route event onto the invalidation
// classes: main-table rows with a destination prefix are Prefix events,
// anything else is a Full event (§10.2).
func classifyRouteUpdate(update netlink.RouteUpdate) (routing.SelectedRouteInvalidation, bool) {
	if update.Table != selectedRouteTable || update.Dst == nil {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull}, true
	}
	prefix, ok := prefixFromIPNet(update.Dst.IP, update.Dst.Mask)
	if !ok {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull}, true
	}
	return routing.SelectedRouteInvalidation{
		Kind:    routing.SelectedInvalidatePrefix,
		Prefix:  prefix.Masked(),
		IfIndex: update.LinkIndex,
	}, true
}
