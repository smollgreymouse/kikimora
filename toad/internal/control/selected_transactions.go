package control

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The canonical selected-route transaction helpers (08D1 §40.1/§40.2):
// exactly two helpers compose registry transitions, checkpoints, platform
// preflight/mutation/read-back and verification tokens — reconcileRecordLocked
// and retireRecordLocked. Ensure, Forget, Flush, the observer, the audit,
// startup resume and explicit release all call through them. No other helper
// may compose registry state transitions on its own.
//
// Locking (§31.8/§40.2): every caller holds the role's applyMu for the whole
// transaction (registry transition → checkpoint → platform I/O → registry
// transition → checkpoint → token update). Manager metadata locks are never
// held across disk or platform I/O.

// errRepairInvalidState marks a BeginRepair attempt from a non-Active record.
var errRepairInvalidState = errors.New("repair requires an active record")

// reconcileRecordLocked drives one record through the exact §37.5
// state-aware transaction. Caller holds the role's applyMu. The request
// context reaches every platform call (§37.8) — no detached background
// contexts on the mutation path.
func (m *SelectedRouteManager) reconcileRecordLocked(ctx context.Context, role string, prefix netip.Prefix, reason string) (bool, error) {
	if !m.routesResolved {
		return false, fmt.Errorf("this platform has no selected-route writer")
	}
	record, ok := m.registryEntry(role, prefix)
	if !ok {
		return false, fmt.Errorf("reconcile %s: the record is not registered", prefix)
	}

	switch record.State {
	case routing.SelectedActive:
		// §37.4: the caller proved the drift through the planner (the audit
		// holds the kernel rows) — demote through BeginRepair, checkpoint
		// the Pending boundary, and fall through to the Pending path.
		if err := m.registry.BeginRepair(role, prefix); err != nil {
			return false, err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return false, err
		}
		record, _ = m.registryEntry(role, prefix)
		fallthrough

	case routing.SelectedPending:
		// §37.5 Pending path: Preflight → after_preflight → MarkApplying →
		// checkpoint → after_applying_checkpoint → Reconcile → MarkActive →
		// checkpoint → mint token.
		if err := m.routes.PreflightSelectedRoute(ctx, prefix, record.Desired, record); err != nil {
			if isForeignRowError(err) {
				return false, &SelectedRouteConflictError{Role: role, Zone: record.Zone, Prefix: prefix.String()}
			}
			return false, err
		}
		if err := m.runFailpoint("route.after_preflight", role, prefix); err != nil {
			return false, err
		}
		if err := m.registry.MarkApplying(role, prefix); err != nil {
			return false, err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return false, err
		}
		if err := m.runFailpoint("route.after_applying_checkpoint", role, prefix); err != nil {
			return false, err
		}
		record, _ = m.registryEntry(role, prefix)
		return m.driveTransaction(ctx, role, record, reason)

	case routing.SelectedApplying:
		// §37.5 Applying path: the mutation is already authorized and
		// checkpointed — Reconcile directly (no second preflight).
		return m.driveTransaction(ctx, role, record, reason)

	case routing.SelectedRetiring:
		return false, fmt.Errorf("reconcile %s: a retiring record is owned by retirement", prefix)

	default:
		return false, fmt.Errorf("reconcile %s: unknown state %q", prefix, record.State)
	}
}

// driveTransaction runs the authorized tail of the transaction: Reconcile →
// MarkActive(exact Verified) → checkpoint → mint token. Caller holds
// applyMu; record.State must be Applying.
func (m *SelectedRouteManager) driveTransaction(ctx context.Context, role string, record *routing.SelectedRoute, reason string) (bool, error) {
	if record.State != routing.SelectedApplying {
		return false, fmt.Errorf("drive %s requires state applying, got %q", record.Prefix, record.State)
	}
	desired := routing.SelectedRouteDesired{Role: role, Prefix: record.Prefix, IfIndex: record.Desired.IfIndex}
	result, err := m.routes.ReconcileSelectedRoute(ctx, desired, record)
	if err != nil {
		return false, err
	}
	if err := m.runFailpoint("route.after_kernel_reconcile", role, record.Prefix); err != nil {
		return false, err
	}
	if result.Verified != record.Desired {
		return false, fmt.Errorf("reconcile verified %v but the desired identity is %v", result.Verified, record.Desired)
	}
	if err := m.registry.MarkActive(role, record.Prefix, result.Verified); err != nil {
		return false, err
	}
	if err := m.runFailpoint("route.after_mark_active", role, record.Prefix); err != nil {
		return false, err
	}
	generation := m.routeGenerationLocked(role)
	if err := m.writeCheckpoint(role, generation); err != nil {
		return false, err
	}
	if err := m.runFailpoint("route.after_active_checkpoint", role, record.Prefix); err != nil {
		return false, err
	}
	m.mintVerificationToken(role, *record)
	return result.Mutated, nil
}

// retireRecordLocked is the single §37.6/§7.6 retirement: the selected row
// is checkpointed Retiring and removed by exact identity, then the
// independent park transaction retires in full before the registry entry
// disappears. Caller holds the role's applyMu.
func (m *SelectedRouteManager) retireRecordLocked(ctx context.Context, role string, prefix netip.Prefix) error {
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	record, ok := m.registryEntry(role, prefix)
	if !ok {
		return nil
	}

	// Durable retirement evidence first: RetireDesired records whether the
	// syscall was authorized from Applying (§1.4).
	if record.State != routing.SelectedRetiring {
		if err := m.registry.MarkRetiring(role, prefix); err != nil {
			return err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return err
		}
		record, _ = m.registryEntry(role, prefix)
	}

	// Remove only the exact authorized selected identities.
	if err := m.routes.RemoveSelectedRoute(ctx, *record); err != nil {
		return err
	}

	// The independent park transaction retires in full before the registry
	// entry may disappear (§37.6): never delete the selected registry record
	// while Park != nil.
	if record.Park != nil {
		switch record.Park.State {
		case routing.SelectedParkPending:
			// The park mutation was never authorized: clear without a
			// delete (§7.6).
			if err := m.registry.ClearPark(role, prefix); err != nil {
				return err
			}
			if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
				return err
			}
		case routing.SelectedParkApplying, routing.SelectedParkActive:
			if err := m.registry.MarkParkRetiring(role, prefix); err != nil {
				return err
			}
			if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
				return err
			}
			if err := m.resumeParkRetirementLocked(ctx, role, prefix); err != nil {
				return err
			}
		case routing.SelectedParkRetiring:
			if err := m.resumeParkRetirementLocked(ctx, role, prefix); err != nil {
				return err
			}
		}
	}

	m.registry.Remove(role, prefix)
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	m.clearVerificationForPrefix(prefix)
	return nil
}

// resumeParkRetirementLocked completes an interrupted park retirement and is
// the §7.5 exact-absence gate: RemoveManagedPark, prove absence, ClearPark,
// checkpoint. Caller holds applyMu.
func (m *SelectedRouteManager) resumeParkRetirementLocked(ctx context.Context, role string, prefix netip.Prefix) error {
	record, ok := m.registryEntry(role, prefix)
	if !ok || record.Park == nil {
		return nil
	}
	if err := m.routes.RemoveManagedPark(ctx, role, prefix, *record.Park); err != nil {
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

// selectedDrainViolation reports the D7R §7.3 disable-precondition state of
// one managed role: any registry record (parked or not) and any non-zero
// in-flight counter is a violation. The registry is consulted even when the
// role never ran in this process: disk-restored ownership must block the
// rollback too.
func (m *SelectedRouteManager) selectedDrainViolation(role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.registry.Snapshot(role)
	for _, entry := range entries {
		if entry.Park != nil {
			return fmt.Errorf("role %q still has a park transaction on %s; retire the park before disabling managed mode", role, entry.Prefix)
		}
	}
	if len(entries) != 0 {
		return fmt.Errorf("role %q still has %d selected registry records; disconnect before disabling managed mode", role, len(entries))
	}
	if state, ok := m.roles[role]; ok && state.inFlight != 0 {
		return fmt.Errorf("role %q has %d selected-route mutations in flight", role, state.inFlight)
	}
	return nil
}

// Forget retires one explicit intent through the canonical helper while
// holding the role's applyMu for the whole transaction (§37.7).
func (m *SelectedRouteManager) Forget(ctx context.Context, intent LeshyRouteIntent) error {
	prefix, err := canonicalIntentPrefix(intent)
	if err != nil {
		return &SelectedRouteDeniedError{Zone: intent.Zone, Prefix: intent.Prefix, Reason: err.Error()}
	}
	candidates := m.zoneRoles[intent.Zone]
	if len(candidates) != 1 {
		return &SelectedRouteDeniedError{Zone: intent.Zone, Prefix: prefix.String(), Reason: fmt.Sprintf("zone maps to %d configured roles", len(candidates))}
	}
	role := candidates[0]

	// D8 §8.5: local mode is never a Kikimora selected writer.
	if err := m.requireManagedRole(role, intent.Zone, prefix.String()); err != nil {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return err
	}

	// D7R §7.1: route mutations deny while a leshy-mode transition gate is
	// active.
	if m.owner != nil && m.owner.modeTransitionGateActive() {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return &SelectedRouteDeniedError{Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "a leshy-mode transition is in flight"}
	}

	// Bounded route mutation (08D1 §31.20).
	ctx, cancel := context.WithTimeout(ctx, selectedRouteMutationDeadline)
	defer cancel()

	m.countForgetState(role)
	m.mu.Lock()
	state := m.roleState(role)
	m.beginFlight(state)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.endFlight(state)
		m.mu.Unlock()
	}()

	// One retirement path (§40.1): applyMu spans registry → checkpoint →
	// platform → registry → checkpoint.
	state.applyMu.Lock()
	defer state.applyMu.Unlock()
	return m.retireRecordLocked(ctx, role, prefix)
}

// FlushZone retires every registered intent of one zone through the
// canonical helper, holding one applyMu for the deterministic prefix-sorted
// batch (§37.7/§31.8).
func (m *SelectedRouteManager) FlushZone(ctx context.Context, zone string) error {
	candidates := m.zoneRoles[zone]
	if len(candidates) != 1 {
		return &SelectedRouteDeniedError{Zone: zone, Reason: fmt.Sprintf("zone maps to %d configured roles", len(candidates))}
	}
	role := candidates[0]

	// D8 §8.5: local mode is never a Kikimora selected writer.
	if err := m.requireManagedRole(role, zone, ""); err != nil {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return err
	}

	// D7R §7.1: route mutations deny while a leshy-mode transition gate is
	// active.
	if m.owner != nil && m.owner.modeTransitionGateActive() {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return &SelectedRouteDeniedError{Role: role, Zone: zone,
			Reason: "a leshy-mode transition is in flight"}
	}

	ctx, cancel := context.WithTimeout(ctx, selectedRouteMutationDeadline)
	defer cancel()

	m.mu.Lock()
	state := m.roleState(role)
	m.beginFlight(state)
	generation := state.routeGeneration
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.endFlight(state)
		m.mu.Unlock()
	}()

	state.applyMu.Lock()
	defer state.applyMu.Unlock()

	var firstErr error
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Zone != zone {
			continue
		}
		if err := m.retireRecordLocked(ctx, role, entry.Prefix); err != nil && firstErr == nil {
			// A failed delete keeps the Retiring record and its checkpoint
			// evidence (§5.5).
			firstErr = err
		}
	}
	if err := m.writeCheckpoint(role, generation); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Ensure routes through the canonical reconcile: applySelectedRoute's body
// delegates to reconcileRecordLocked once the record is registered, so the
// Ensure deadline context reaches every platform call (§37.8).
func (m *SelectedRouteManager) applySelectedRouteCanonical(ctx context.Context, role, zone string, prefix netip.Prefix, source routing.SelectedRouteSource, ifIndex int, iface string, generation uint64, toadGeneration uint64) (bool, error) {
	if !m.routesResolved {
		return false, &SelectedRouteDeniedError{Role: role, Zone: zone, Prefix: prefix.String(), Reason: "this platform has no selected-route writer"}
	}
	// Zero-I/O fast path (§5.2/§31.9/§47.4): only while an in-memory
	// verification token proves the EXACT normalized Desired identity for
	// the current Toad/route generations and no unprocessed observation
	// event is pending. The current Desired is normalized BEFORE the
	// decision: an ifindex alone does not prove identity (Windows LUID/
	// metric/next-hop can move under the same index — §47.4 regression).
	desired := routing.SelectedRouteDesired{Role: role, Prefix: prefix, IfIndex: ifIndex}
	identity, err := m.routes.NormalizeSelectedRoute(ctx, desired)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	state := m.roleState(role)
	token, hasToken := state.verification[prefix]
	fastPath := hasToken &&
		m.fastPathAllowed() &&
		token.Identity == identity &&
		token.ToadGeneration == toadGeneration &&
		token.RouteGeneration == generation
	fastPath = fastPath && func() bool {
		for _, entry := range m.registry.Snapshot(role) {
			if entry.Prefix == prefix && entry.State == routing.SelectedActive &&
				entry.Zone == zone && entry.Source == source &&
				entry.Desired == identity && entry.Owned != nil && *entry.Owned == entry.Desired {
				return true
			}
		}
		return false
	}()
	m.mu.Unlock()
	if fastPath {
		return true, nil
	}
	if err := m.registry.Reserve(role, zone, prefix, source, toadGeneration, generation, iface, identity); err != nil {
		return false, err
	}
	if err := m.writeCheckpoint(role, generation); err != nil {
		return false, fmt.Errorf("checkpoint reserved ownership: %w", err)
	}
	if err := m.runFailpoint("route.after_pending_checkpoint", role, prefix); err != nil {
		return false, err
	}
	// The canonical transaction owns everything after the Pending
	// checkpoint: preflight, Applying, reconcile, Active, token. The wire
	// state derives from the platform Mutated flag (§5.1): mutated →
	// applied; converged → already-applied.
	mutated, err := m.reconcileRecordLocked(ctx, role, prefix, "ensure")
	if err != nil {
		if isForeignRowError(err) {
			return false, &SelectedRouteConflictError{Role: role, Zone: zone, Prefix: prefix.String()}
		}
		return false, err
	}
	return !mutated, nil
}

// Ensure disallows the legacy applySelectedRoute path: the canonical helper
// is the only composition. (The old method is removed; the caller in ensure()
// is rewired to applySelectedRouteCanonical.)
