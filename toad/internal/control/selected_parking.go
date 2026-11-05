package control

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// Managed parking, selected withdrawal and exact restoration live in the
// SelectedRouteManager (08D1 §7): the registry/checkpoint is the single
// authority, parking.Manager is never consulted on the managed path, and
// every park kernel mutation is bracketed by a durable Pending/Applying/
// Retiring record.

// ParkingState derives the product parking view from the registry (§7.1):
// it reports proven protection only — ParkActive/Retiring records with an
// exact Owned identity — never a park transaction that has not been read
// back.
func (m *SelectedRouteManager) ParkingState(role string) parking.State {
	return m.parkingState(m.registry.Snapshot(role))
}

func (m *SelectedRouteManager) parkingState(entries []routing.SelectedRoute) parking.State {
	state := parking.State{}
	allProven := len(entries) > 0
	for _, entry := range entries {
		if entry.Park != nil && entry.Park.Owned != nil &&
			(entry.Park.State == routing.SelectedParkActive || entry.Park.State == routing.SelectedParkRetiring) {
			state.Prefixes = append(state.Prefixes, entry.Prefix)
		}
		// Active is true only when every non-Retiring selected record that
		// recovery intends to protect carries an exact ParkActive
		// Owned==Desired.
		if entry.State == routing.SelectedRetiring {
			continue
		}
		if entry.Park == nil || entry.Park.State != routing.SelectedParkActive ||
			entry.Park.Owned == nil || *entry.Park.Owned != entry.Park.Desired {
			allProven = false
		}
	}
	state.Count = len(state.Prefixes)
	state.Active = allProven && state.Count == len(entries)
	return state
}

// ParkRegistered installs and verifies the fail-closed protection for every
// non-Retiring registered record while the old selected rows still exist
// (§7.2). Precondition: the role is frozen. Every transition that changes
// park state or identity is checkpointed before the next kernel mutation.
// A later prefix failure leaves earlier Active parks checkpointed; recovery
// stays frozen and a retry resumes by state.
func (m *SelectedRouteManager) ParkRegistered(ctx context.Context, role string) error {
	m.mu.Lock()
	state := m.roleState(role)
	frozen := state.frozen
	m.mu.Unlock()
	if !frozen {
		return fmt.Errorf("ParkRegistered for role %q requires a frozen role", role)
	}
	if !m.routesResolved {
		return fmt.Errorf("this platform has no managed-park writer")
	}
	state.applyMu.Lock()
	defer state.applyMu.Unlock()

	// Stable registry snapshot in canonical prefix order.
	entries := m.registry.Snapshot(role)
	for _, entry := range entries {
		if entry.State == routing.SelectedRetiring {
			// Retirement owns their cleanup (§7.2).
			continue
		}
		if err := m.parkOne(ctx, role, entry); err != nil {
			return err
		}
	}
	return nil
}

// parkOne resumes one record's park transaction by its persisted state
// (§7.2).
func (m *SelectedRouteManager) parkOne(ctx context.Context, role string, entry routing.SelectedRoute) error {
	prefix := entry.Prefix
	parkDesired, err := m.routes.NormalizeManagedPark(ctx, prefix)
	if err != nil {
		return err
	}

	switch parkStateOf(entry) {
	case parkStateNone:
		return m.runParkTransaction(ctx, role, prefix, parkDesired, entry.Park == nil)

	case parkStatePending:
		current := entry
		if current.Park == nil || current.Park.Desired != parkDesired {
			// The platform park identity changed: re-reserve with
			// forcePending, preserving the previous Owned (§7.2).
			if err := m.registry.ReservePark(role, prefix, parkDesired, true); err != nil {
				return err
			}
			if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
				return err
			}
			updated, _ := m.registryEntry(role, prefix)
			current = *updated
		}
		if err := m.routes.PreflightManagedPark(ctx, prefix, parkDesired, &current); err != nil {
			return err
		}
		return m.applyParkTransaction(ctx, role, prefix, parkDesired, &current)

	case parkStateApplying:
		// The mutation was already authorized: no Pending preflight again
		// (§7.2). Reconcile with the Applying record.
		return m.applyParkTransaction(ctx, role, prefix, parkDesired, &entry)

	case parkStateActive:
		if entry.Park != nil && entry.Park.Desired == parkDesired && entry.Park.Owned != nil {
			present, err := m.routes.ObserveManagedPark(ctx, prefix, *entry.Park.Owned)
			if err != nil {
				return err
			}
			if present {
				// Exact proof: zero mutation (§7.2).
				return nil
			}
		}
		// The exact Active park is missing or the current identity changed:
		// re-reserve with forcePending. The platform creates/verifies the
		// new Desired before deleting the stale exact old Owned, so the
		// ifindex change never opens a protection hole.
		if err := m.registry.ReservePark(role, prefix, parkDesired, true); err != nil {
			return err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return err
		}
		refreshed, _ := m.registryEntry(role, prefix)
		if err := m.routes.PreflightManagedPark(ctx, prefix, parkDesired, refreshed); err != nil {
			return err
		}
		return m.applyParkTransaction(ctx, role, prefix, parkDesired, refreshed)

	case parkStateRetiring:
		// Complete the interrupted retirement first, then start the
		// nil-Park transaction because this recovery still requires
		// protection before WithdrawSelected (§7.2).
		if err := m.resumeParkRetirement(ctx, role, prefix, *entry.Park); err != nil {
			return err
		}
		return m.runParkTransaction(ctx, role, prefix, parkDesired, true)
	}
	return fmt.Errorf("park %s has an unknown state", prefix)
}

type parkTransactionState int

const (
	parkStateNone parkTransactionState = iota
	parkStatePending
	parkStateApplying
	parkStateActive
	parkStateRetiring
)

func parkStateOf(entry routing.SelectedRoute) parkTransactionState {
	if entry.Park == nil {
		return parkStateNone
	}
	switch entry.Park.State {
	case routing.SelectedParkPending:
		return parkStatePending
	case routing.SelectedParkApplying:
		return parkStateApplying
	case routing.SelectedParkActive:
		return parkStateActive
	case routing.SelectedParkRetiring:
		return parkStateRetiring
	}
	return parkStateNone
}

// runParkTransaction runs the full nil-Park transaction (§7.2): Reserve →
// Pending checkpoint → Preflight → Applying checkpoint → Reconcile →
// Active checkpoint.
func (m *SelectedRouteManager) runParkTransaction(ctx context.Context, role string, prefix netip.Prefix, parkDesired routing.SelectedParkIdentity, preflight bool) error {
	if err := m.registry.ReservePark(role, prefix, parkDesired, false); err != nil {
		return err
	}
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_pending_checkpoint", role, prefix); err != nil {
		return err
	}
	record, _ := m.registryEntry(role, prefix)
	if preflight {
		if err := m.routes.PreflightManagedPark(ctx, prefix, parkDesired, record); err != nil {
			return err
		}
		if err := m.runFailpoint("park.after_preflight", role, prefix); err != nil {
			return err
		}
	}
	latest, _ := m.registryEntry(role, prefix)
	return m.applyParkTransaction(ctx, role, prefix, parkDesired, latest)
}

// applyParkTransaction authorizes and executes the park mutation: MarkPark
// Applying → Applying checkpoint → Reconcile → MarkParkActive → Active
// checkpoint.
func (m *SelectedRouteManager) applyParkTransaction(ctx context.Context, role string, prefix netip.Prefix, parkDesired routing.SelectedParkIdentity, record *routing.SelectedRoute) error {
	if err := m.registry.MarkParkApplying(role, prefix); err != nil {
		return err
	}
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_applying_checkpoint", role, prefix); err != nil {
		return err
	}
	result, err := m.routes.ReconcileManagedPark(ctx, role, prefix, record)
	if err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_kernel_reconcile", role, prefix); err != nil {
		return err
	}
	if result.Verified != parkDesired {
		return fmt.Errorf("park reconcile verified %v but the desired identity is %v", result.Verified, parkDesired)
	}
	if err := m.registry.MarkParkActive(role, prefix, result.Verified); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_mark_active", role, prefix); err != nil {
		return err
	}
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	return m.runFailpoint("park.after_active_checkpoint", role, prefix)
}

// resumeParkRetirement completes an interrupted park retirement (§7.5
// ParkRetiring): exact RemoveManagedPark, absent is success, then ClearPark
// with a checkpoint.
func (m *SelectedRouteManager) resumeParkRetirement(ctx context.Context, role string, prefix netip.Prefix, park routing.SelectedPark) error {
	if err := m.routes.RemoveManagedPark(ctx, role, prefix, park); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_kernel_delete", role, prefix); err != nil {
		return err
	}
	if err := m.registry.ClearPark(role, prefix); err != nil {
		return err
	}
	return m.writeCheckpoint(role, m.routeGenerationLocked(role))
}

// WithdrawSelected removes only the exact registered selected identities
// after every record's protection is proven (§5.7/§7.3): the park is then
// the winning same-prefix protection, and the step succeeds only when the
// selected identities are absent and the exact persisted parks are present.
// It does not MarkRetiring, remove registry ownership, clear selected Owned
// or mutate the park transaction.
func (m *SelectedRouteManager) WithdrawSelected(ctx context.Context, role string) error {
	m.mu.Lock()
	state := m.roleState(role)
	frozen := state.frozen
	m.mu.Unlock()
	if !frozen {
		return fmt.Errorf("WithdrawSelected for role %q requires a frozen role", role)
	}
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	state.applyMu.Lock()
	defer state.applyMu.Unlock()

	entries := m.registry.Snapshot(role)
	// Validate all records and exact Park.Owned observations BEFORE deleting
	// any selected row: recovery must never create a window with neither a
	// selected route nor a verified park (§5.7).
	type protected struct {
		record routing.SelectedRoute
		park   routing.SelectedParkIdentity
	}
	plan := make([]protected, 0, len(entries))
	for _, entry := range entries {
		if entry.State == routing.SelectedRetiring {
			continue
		}
		if entry.Park == nil || entry.Park.State != routing.SelectedParkActive ||
			entry.Park.Owned == nil || *entry.Park.Owned != entry.Park.Desired {
			return fmt.Errorf("withdraw %s: record %s has no exact Active park", role, entry.Prefix)
		}
		present, err := m.routes.ObserveManagedPark(ctx, entry.Prefix, *entry.Park.Owned)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("withdraw %s: the recorded park for %s is missing", role, entry.Prefix)
		}
		plan = append(plan, protected{record: entry, park: *entry.Park.Owned})
	}
	// Remove each exact selected identity.
	for _, item := range plan {
		if err := m.routes.RemoveSelectedRoute(ctx, item.record); err != nil {
			// Failure deleting any selected identity stops recovery before
			// the legacy publication withdrawal and before Quiesce (§7.3).
			return err
		}
	}
	// After all removals, verify every Park.Owned still exists.
	for _, item := range plan {
		present, err := m.routes.ObserveManagedPark(ctx, item.record.Prefix, item.park)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("withdraw %s: the park for %s vanished during the withdrawal", role, item.record.Prefix)
		}
	}
	return nil
}

// ObserveRestoration proves the restored selected identity wins and retires
// the park exactly (§7.5). It returns parking.ErrRoutesStillParked while
// any entry still carries a park transaction.
func (m *SelectedRouteManager) ObserveRestoration(ctx context.Context, role string) error {
	if !m.routesResolved {
		return fmt.Errorf("this platform has no managed-park writer")
	}
	state := m.roleState(role)
	state.applyMu.Lock()
	defer state.applyMu.Unlock()

	entries := m.registry.Snapshot(role)
	for _, entry := range entries {
		if entry.Park == nil {
			continue
		}
		switch entry.Park.State {
		case routing.SelectedParkPending, routing.SelectedParkApplying:
			// Protection setup was interrupted: the next recovery retry
			// returns through ParkRegistered first (§7.5). Never clear or
			// skip them.
			return parking.ErrRoutesStillParked
		case routing.SelectedParkActive:
			// A not-yet-proven park stays; the role keeps recovering and a
			// later pass re-runs the proof (§7.5).
			if err := m.retireProvenPark(ctx, role, entry); err != nil && !errors.Is(err, errParkNotProven) {
				return err
			}
		case routing.SelectedParkRetiring:
			if err := m.resumeParkRetirement(ctx, role, entry.Prefix, *entry.Park); err != nil {
				return err
			}
		}
	}
	// ErrRoutesStillParked while any entry still carries a park transaction.
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Park != nil {
			return parking.ErrRoutesStillParked
		}
	}
	return nil
}

// errParkNotProven marks a ParkActive record whose restoration is not yet
// proven; the park stays and the role keeps recovering.
var errParkNotProven = errors.New("parked restoration is not proven yet")

// errRestorationNotWinning is the typed §31.25 failure: the selected row
// does not beat the park under the effective-metric proof, so the park
// stays Active and the role stays frozen.
var errRestorationNotWinning = errors.New("restored selected route does not win over the park")

// selectedWinningProver is the Windows effective-metric proof surface
// (08D1 §31.25); platforms without it use the absolute metric comparison.
type selectedWinningProver interface {
	SelectedWinsOverPark(ctx context.Context, prefix netip.Prefix, selected routing.SelectedRouteIdentity, park routing.SelectedParkIdentity) (bool, error)
}

// retireProvenPark runs the exact unpark (§7.5): all seven conditions must
// hold before MarkParkRetiring, then RemoveManagedPark, then ClearPark.
func (m *SelectedRouteManager) retireProvenPark(ctx context.Context, role string, entry routing.SelectedRoute) error {
	prefix := entry.Prefix
	park := entry.Park
	if entry.State != routing.SelectedActive || entry.Owned == nil || *entry.Owned != entry.Desired {
		// The exact selected identity is not proven; a missing expected park
		// would then be an integrity failure (§7.5).
		return errParkNotProven
	}
	if park.Owned == nil || *park.Owned != park.Desired {
		return errParkNotProven
	}
	present, err := m.routes.ObserveManagedPark(ctx, prefix, *park.Owned)
	if err != nil {
		return err
	}
	if present {
		// No foreign same-prefix row may exist and the selected identity
		// must win: observe the full prefix rows once.
		rows, err := m.routes.SnapshotSelectedRoutes(ctx)
		if err != nil {
			return err
		}
		selectedPresent := false
		for _, row := range rows[prefix] {
			switch {
			case row.Kind == routing.SelectedKernelForeign:
				return fmt.Errorf("unpark %s: foreign row %s", prefix, row.Description)
			case row.Kind == routing.SelectedKernelPark && !row.Park.Equal(*park.Owned):
				return fmt.Errorf("unpark %s: unexpected park row %v", prefix, row.Park)
			case row.Kind == routing.SelectedKernelRoute && routing.SelectedRowMatchesIdentity(row, entry.Desired):
				selectedPresent = true
			}
		}
		if !selectedPresent {
			return errParkNotProven
		}
		// Condition 7 (§7.5) with the Windows effective-metric proof
		// (§31.25): selected route offset + interface metric must beat the
		// park's. A platform without the prover falls back to the absolute
		// Linux metric comparison.
		wins := entry.Desired.Metric < park.Owned.Metric
		if prover, ok := m.routes.(selectedWinningProver); ok {
			wins, err = prover.SelectedWinsOverPark(ctx, prefix, entry.Desired, *park.Owned)
			if err != nil {
				return err
			}
		}
		if !wins {
			// §31.25: keep ParkActive, keep the role frozen and report the
			// typed restoration-not-winning failure; never delete the park
			// to recover connectivity.
			return fmt.Errorf("unpark %s: %w (selected %d vs park %d)",
				prefix, errRestorationNotWinning, entry.Desired.Metric, park.Owned.Metric)
		}
	}
	// MarkParkRetiring + checkpoint before the unpark syscall (§7.5).
	if err := m.registry.MarkParkRetiring(role, prefix); err != nil {
		return err
	}
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_retiring_checkpoint", role, prefix); err != nil {
		return err
	}
	if err := m.routes.RemoveManagedPark(ctx, role, prefix, *park); err != nil {
		return err
	}
	if err := m.runFailpoint("park.after_kernel_delete", role, prefix); err != nil {
		return err
	}
	// The exact selected identity must remain; the persisted park must be
	// absent before ownership is forgotten (§7.5).
	after, err := m.routes.SnapshotSelectedRoutes(ctx)
	if err != nil {
		return err
	}
	selectedPresent := false
	for _, row := range after[prefix] {
		if row.Kind == routing.SelectedKernelRoute && routing.SelectedRowMatchesIdentity(row, entry.Desired) {
			selectedPresent = true
		}
		if row.Kind == routing.SelectedKernelPark && row.Park.Equal(*park.Owned) {
			return fmt.Errorf("unpark %s: the persisted park identity is still present after the delete", prefix)
		}
	}
	if !selectedPresent {
		return fmt.Errorf("unpark %s: the exact selected identity vanished during the unpark", prefix)
	}
	if err := m.registry.ClearPark(role, prefix); err != nil {
		return err
	}
	return m.writeCheckpoint(role, m.routeGenerationLocked(role))
}
