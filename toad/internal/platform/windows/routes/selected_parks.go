//go:build windows

package routes

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// routeChangeHandle owns one NotifyRouteChange2 registration; Unregister
// happens outside the callback (08D1 §17.2 makes the Drop-ownership
// explicit in stage 3A).
type routeChangeHandle struct {
	handle *winipcfg.RouteChangeCallback
}

func (h *routeChangeHandle) unregister() {
	if h == nil || h.handle == nil {
		return
	}
	_ = h.handle.Unregister()
}

// registerRouteChange wraps winipcfg.RegisterRouteChangeCallback.
func registerRouteChange(callback func(winipcfg.MibNotificationType, *winipcfg.MibIPforwardRow2)) (*routeChangeHandle, error) {
	handle, err := winipcfg.RegisterRouteChangeCallback(callback)
	if err != nil {
		return nil, err
	}
	return &routeChangeHandle{handle: handle}, nil
}

// classifyNotification copies only the invalidation material from the
// callback row (08D1 §31.25: the callback row is incomplete and is never
// projected as a complete selected/park row).
func classifyNotification(_ winipcfg.MibNotificationType, row *winipcfg.MibIPforwardRow2) routing.SelectedRouteInvalidation {
	if row == nil {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull}
	}
	prefix := row.DestinationPrefix.Prefix()
	if !prefix.IsValid() {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull}
	}
	return routing.SelectedRouteInvalidation{
		Kind:    routing.SelectedInvalidatePrefix,
		Prefix:  prefix.Masked(),
		IfIndex: int(row.InterfaceIndex),
	}
}

// The managed-park surface (08D1 §9): the writer's deterministic loopback
// park shape, transactional exactly like the selected routes. All state
// transitions run under the executor mutex so park and selected mutations
// never interleave. The exact persisted identities decide every mutation;
// the current loopbackIfIndex is never an ownership proof for old rows.

// normalizeManagedParkIdentity resolves the current loopback once and
// returns the exact desired loopback park identity without mutation.
func normalizeManagedParkIdentity(e *Executor, prefix netip.Prefix) (routing.SelectedParkIdentity, error) {
	if !prefix.IsValid() || !routing.IsCanonicalPrefix(prefix) {
		return routing.SelectedParkIdentity{}, fmt.Errorf("park prefix %q is not canonical", prefix)
	}
	index := e.api.loopbackIndex()
	nextHop := "127.0.0.1"
	if !prefix.Addr().Is4() {
		nextHop = "::1"
	}
	identity := routing.SelectedParkIdentity{
		Kind:     routing.SelectedParkLoopback,
		Table:    kernelTable,
		IfIndex:  int(index),
		NextHop:  nextHop,
		Protocol: ownedRouteProtocol,
		Metric:   routing.ManagedParkMetric,
	}
	if err := identity.Validate(prefix); err != nil {
		return routing.SelectedParkIdentity{}, err
	}
	return identity, nil
}

// NormalizeManagedPark builds the exact desired loopback park identity
// without mutation (08D1 §9).
func (e *Executor) NormalizeManagedPark(_ context.Context, prefix netip.Prefix) (routing.SelectedParkIdentity, error) {
	return normalizeManagedParkIdentity(e, prefix)
}

// PreflightManagedPark classifies cached/current same-prefix rows against
// the persisted park state (08D1 §4.2): the rules are the shared planner's.
// The exact authorized selected identities coexist with the park (§7.4).
func (e *Executor) PreflightManagedPark(_ context.Context, prefix netip.Prefix, _ routing.SelectedParkIdentity, record *routing.SelectedRoute) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	return classifyParkRowsForRecord(selectedRowsFor(&current, prefix), prefix, record, "park preflight")
}

// classifyParkRowsForRecord applies the shared classification to one
// prefix: exact authorized park rows and the record's exact selected
// identities are tolerated, everything else conflicts.
func classifyParkRowsForRecord(rows []routing.SelectedKernelRow, prefix netip.Prefix, record *routing.SelectedRoute, context string) error {
	for _, row := range rows {
		switch row.Kind {
		case routing.SelectedKernelForeign:
			return fmt.Errorf("%s %s: foreign row: %s", context, prefix, row.Description)
		case routing.SelectedKernelRoute:
			if record != nil && (row.Route == record.Desired ||
				(record.Owned != nil && row.Route == *record.Owned)) {
				continue
			}
			return fmt.Errorf("%s %s: unauthorized forwarding row: %s", context, prefix, row.Description)
		case routing.SelectedKernelPark:
			if !parkRowPreflightAuthorized(row.Park, record) {
				return fmt.Errorf("%s %s: unauthorized park row %v", context, prefix, row.Park)
			}
		}
	}
	return nil
}

func parkRowPreflightAuthorized(row routing.SelectedParkIdentity, record *routing.SelectedRoute) bool {
	if record == nil || record.Park == nil {
		return false
	}
	park := record.Park
	switch park.State {
	case routing.SelectedParkPending:
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

// ReconcileManagedPark, only for ParkApplying, creates/verifies the new
// Desired before deleting stale exact previous Owned (08D1 §9). This is
// mandatory when the loopback ifindex changed; ERROR_OBJECT_ALREADY_EXISTS
// triggers a refresh and exact classification.
func (e *Executor) ReconcileManagedPark(_ context.Context, _ string, prefix netip.Prefix, record *routing.SelectedRoute) (routing.SelectedParkReconcileResult, error) {
	if record == nil || record.Park == nil {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("reconcile managed park %s: no park transaction", prefix)
	}
	park := record.Park
	if park.State != routing.SelectedParkApplying {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("reconcile managed park %s requires park state applying, got %q", prefix, park.State)
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if !prefix.IsValid() || !routing.IsCanonicalPrefix(prefix) {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("park prefix %q is not canonical", prefix)
	}
	if err := park.Desired.Validate(prefix); err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	rows, err := e.classifyParkRows(prefix)
	if err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	if err := classifyParkRowsForRecord(rows, prefix, record, "park reconcile"); err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	mutated := false
	desiredPresent, staleOwned := false, false
	for _, row := range rows {
		if row.Kind != routing.SelectedKernelPark {
			continue
		}
		switch {
		case row.Park.Equal(park.Desired):
			desiredPresent = true
		case park.Owned != nil && row.Park.Equal(*park.Owned):
			staleOwned = true
		}
	}
	if !desiredPresent {
		if err := e.createParkRow(prefix, park.Desired); err != nil {
			return routing.SelectedParkReconcileResult{}, err
		}
		mutated = true
	}
	// Stale previous Owned is removed only after Desired is proven.
	if staleOwned && park.Owned != nil && !park.Owned.Equal(park.Desired) {
		if err := e.deleteExactParkRow(prefix, *park.Owned); err != nil {
			return routing.SelectedParkReconcileResult{}, err
		}
		mutated = true
	}
	// Exact read-back.
	rows, err = e.classifyParkRows(prefix)
	if err != nil {
		return routing.SelectedParkReconcileResult{}, err
	}
	for _, row := range rows {
		if row.Kind == routing.SelectedKernelPark && row.Park.Equal(park.Desired) {
			return routing.SelectedParkReconcileResult{Mutated: mutated, Verified: row.Park}, nil
		}
	}
	return routing.SelectedParkReconcileResult{}, fmt.Errorf("park %s is not exactly present after the apply", prefix)
}

// classifyParkRows returns the projected same-prefix rows from the coherent
// cache (§38.1); a notification-diverged cache refreshes exactly once.
func (e *Executor) classifyParkRows(prefix netip.Prefix) ([]routing.SelectedKernelRow, error) {
	return e.cachedSelectedRowsLocked(prefix)
}

// createParkRow creates the deterministic loopback park row for the exact
// identity.
func (e *Executor) createParkRow(prefix netip.Prefix, identity routing.SelectedParkIdentity) error {
	row, err := e.rowForOperation(routing.Operation{
		Kind:     "park",
		Prefix:   prefix.String(),
		Table:    identity.Table,
		IfIndex:  identity.IfIndex,
		Gateway:  identity.NextHop,
		Protocol: identity.Protocol,
		Metric:   identity.Metric,
	})
	if err != nil {
		return err
	}
	if err := e.api.createRoute(&row); err != nil {
		if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			// Re-read and classify; a row exactly equal to the desired
			// identity is success, anything else is a conflict.
			rows, cerr := e.classifyParkRows(prefix)
			if cerr != nil {
				return cerr
			}
			for _, projected := range rows {
				if projected.Kind == routing.SelectedKernelPark && projected.Park.Equal(identity) {
					return nil
				}
			}
			return fmt.Errorf("park %s create hit an existing row that is not the desired identity", prefix)
		}
		return fmt.Errorf("park %s on windows: %w", prefix, err)
	}
	return nil
}

// deleteExactParkRow deletes one exact persisted park identity; the row is
// rebuilt from the persisted identity, never from a freshly resolved
// loopback (08D1 §9).
func (e *Executor) deleteExactParkRow(prefix netip.Prefix, identity routing.SelectedParkIdentity) error {
	row, err := e.rowForOperation(routing.Operation{
		Kind:     "delete-park",
		Prefix:   prefix.String(),
		Table:    identity.Table,
		IfIndex:  identity.IfIndex,
		Gateway:  identity.NextHop,
		Protocol: identity.Protocol,
		Metric:   identity.Metric,
	})
	if err != nil {
		return err
	}
	if err := e.api.deleteRoute(&row); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("delete park %s on windows: %w", prefix, err)
	}
	return nil
}

// ObserveManagedPark compares against the persisted expected identity,
// never against a newly recomputed loopback row (08D1 §9).
func (e *Executor) ObserveManagedPark(_ context.Context, prefix netip.Prefix, expected routing.SelectedParkIdentity) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rows, err := e.classifyParkRows(prefix)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Kind == routing.SelectedKernelPark && row.Park.Equal(expected) {
			return true, nil
		}
	}
	return false, nil
}

// RemoveManagedPark deletes only identities authorized by the persisted
// park State/Owned/RetireDesired and verifies absence; it never calls
// loopbackIfIndex to decide what to delete (08D1 §9).
func (e *Executor) RemoveManagedPark(_ context.Context, _ string, prefix netip.Prefix, park routing.SelectedPark) error {
	e.mu.Lock()
	defer e.mu.Unlock()

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

	for _, candidate := range candidates {
		if err := e.deleteExactParkRow(prefix, candidate); err != nil {
			return err
		}
	}
	// Verify all authorized identities are absent.
	rows, err := e.classifyParkRows(prefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != routing.SelectedKernelPark {
			continue
		}
		for _, candidate := range candidates {
			if row.Park.Equal(candidate) {
				return fmt.Errorf("park %s identity %v is still present after the delete", prefix, candidate)
			}
		}
	}
	return nil
}

// SnapshotSelectedRoutes forces one live full refresh, projects selected
// rows, exact parks and foreign rows, and refreshes the executor cache the
// targeted reads use (08D1 §9; the selectedCache lands with K15 — today the
// executor rebuilds its snapshot per read).
func (e *Executor) SnapshotSelectedRoutes(_ context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	// §38.1/§4.3: the audit's one live observation is one stable refresh.
	return e.stableSelectedRefreshLocked()
}

// SubscribeSelectedRouteInvalidations registers the NotifyRouteChange2
// observer (once) and returns the invalidation stream; the coherent cache
// initializes against the same registration (§38.1/§38.5).
func (e *Executor) SubscribeSelectedRouteInvalidations(context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	return e.observeSelectedInvalidations()
}
