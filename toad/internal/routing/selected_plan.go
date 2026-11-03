package routing

import (
	"fmt"
	"net/netip"
)

// Selected-route reconciliation rules shared by every platform (08D §6).
// The rules are pure so both platforms enforce one contract and the
// ownership decisions are unit-testable without a kernel.

// SelectedRouteDesired is the authoritative route the controller wants in
// the kernel for one registered destination. A zero Gateway selects the
// device route through IfIndex.
type SelectedRouteDesired struct {
	Role     string
	Prefix   netip.Prefix
	IfIndex  int
	Gateway  netip.Addr
	Protocol int
	Metric   uint32
}

// SelectedKernelRow is the neutral view of one same-prefix kernel row.
type SelectedKernelRow struct {
	IfIndex  int
	Gateway  netip.Addr // zero when the row points at an interface
	Protocol int
	Metric   uint32
}

// SelectedReconcileAction is the mutation PlanSelectedRoute decided.
type SelectedReconcileAction int

const (
	// SelectedReconcileNone: the exact desired row is already in place.
	SelectedReconcileNone SelectedReconcileAction = iota
	// SelectedReconcileAdd: no row occupies the prefix; add the desired one.
	SelectedReconcileAdd
	// SelectedReconcileReplace: every occupying row is provably owned by the
	// managed stack, so rewriting to the new target is allowed.
	SelectedReconcileReplace
)

// selectedRowProtocols we consider provably written by the managed stack:
// the executors' static protocol and the boot protocol Leshy's local writer
// used before the managed cutover. Unspec covers migration-era rows. A
// routing daemon's rows on the same device are never treated as owned.
func selectedRowProtocolOwned(protocol int) bool {
	return protocol == 0 || protocol == 3 || protocol == 4 // unspec, boot, static
}

// SelectedRowMatchesDesired reports whether a kernel row is exactly the
// desired route: main table, output interface, gateway when one is
// expected, protocol and metric.
func SelectedRowMatchesDesired(row SelectedKernelRow, desired SelectedRouteDesired) bool {
	if row.IfIndex != desired.IfIndex {
		return false
	}
	if desired.Gateway.IsValid() {
		if !row.Gateway.IsValid() {
			return false
		}
		if row.Gateway.Unmap() != desired.Gateway.Unmap() {
			return false
		}
	} else if row.Gateway.IsValid() {
		return false
	}
	if desired.Protocol != 0 && row.Protocol != desired.Protocol {
		return false
	}
	return row.Metric == desired.Metric
}

// SelectedRowMatchesOwnedIdentity reports whether a same-prefix row is
// provably the previous owned row: same output interface and a protocol the
// managed stack writes. Anything else is foreign and must not be touched.
func SelectedRowMatchesOwnedIdentity(row SelectedKernelRow, owned *SelectedRoute) bool {
	if owned == nil || row.IfIndex != owned.IfIndex {
		return false
	}
	return selectedRowProtocolOwned(row.Protocol)
}

// PlanSelectedRoute decides the mutation for one prefix from the current
// same-prefix rows.
//
//  1. the exact desired row already present -> no mutation;
//  2. otherwise, a row that is not provably the previous owned row is a
//     conflict: refuse, never replace or delete it;
//  3. otherwise, when rows exist they are all owned -> replace is allowed;
//  4. with no rows, add the desired route.
//
// Success is only proven after an exact read-back, which the platform
// implementations perform after executing the returned action.
func PlanSelectedRoute(existing []SelectedKernelRow, desired SelectedRouteDesired, owned *SelectedRoute) (SelectedReconcileAction, error) {
	for _, row := range existing {
		if SelectedRowMatchesDesired(row, desired) {
			return SelectedReconcileNone, nil
		}
	}
	for _, row := range existing {
		if !SelectedRowMatchesOwnedIdentity(row, owned) {
			return 0, fmt.Errorf(
				"selected route %v conflicts with a foreign row (ifindex %d, protocol %d, metric %d); refusing to replace it",
				desired.Prefix, row.IfIndex, row.Protocol, row.Metric)
		}
	}
	if len(existing) > 0 {
		return SelectedReconcileReplace, nil
	}
	return SelectedReconcileAdd, nil
}
