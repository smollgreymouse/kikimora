package control

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// SelectedRouteManager side of the reconciler contract (08D1 §10): the
// observation-sequence fast-path gate, the invalidation handlers, the
// integrity quarantine (§31.21) and the audit diagnostics counters.

// setObservation carries the §31.9 sequence source; SetRoutes installs it
// when the platform implements SelectedRouteObservation.
func (m *SelectedRouteManager) attachObservation() {
	if observation, ok := m.routes.(platform.SelectedRouteObservation); ok {
		m.observation = observation
	}
}

// fastPathAllowed reports whether the zero-I/O fast path may run (§31.9):
// an event that has reached the process disables it until that event is
// processed.
func (m *SelectedRouteManager) fastPathAllowed() bool {
	if m.observation == nil {
		return true
	}
	return m.processedSequence.Load() == m.observation.CurrentSelectedRouteSequence()
}

// noteProcessedSequence advances the processed gate after handling events
// or audits.
func (m *SelectedRouteManager) noteProcessedSequence(sequence uint64) {
	if sequence > m.processedSequence.Load() {
		m.processedSequence.Store(sequence)
	}
}

// SetRouteAuthorityReady opens the §31.11 gate; production calls it from
// the reconciler after the initial stable audit, tests call it directly.
func (m *SelectedRouteManager) SetRouteAuthorityReady() {
	m.authorityReady.Store(true)
}

// ClearAllVerification drops every role's tokens: Full invalidation,
// observer loss and audit failure all use it (§5.2, §10.4, §31.10).
func (m *SelectedRouteManager) ClearAllVerification() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.roles {
		state.verification = nil
	}
}

// clearVerificationForPrefix drops the tokens whose identity matches the
// prefix family; a prefix invalidation only affects that prefix (§31.9).
func (m *SelectedRouteManager) clearVerificationForPrefix(prefix netip.Prefix) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.roles {
		delete(state.verification, prefix)
	}
}

// clearVerificationForIfindex drops the tokens whose selected Desired/Owned
// uses the ifindex (§10.2).
func (m *SelectedRouteManager) clearVerificationForIfindex(ifindex int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.roles {
		for prefix, token := range state.verification {
			if token.Identity.IfIndex == ifindex {
				delete(state.verification, prefix)
			}
		}
	}
}

// mintVerificationToken stores the zero-I/O proof for an exactly verified
// record (§5.2).
func (m *SelectedRouteManager) mintVerificationToken(role string, entry routing.SelectedRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.roles[role]
	if !ok {
		return
	}
	if state.verification == nil {
		state.verification = make(map[netip.Prefix]selectedVerification)
	}
	state.verification[entry.Prefix] = selectedVerification{
		ToadGeneration:  entry.ToadGeneration,
		RouteGeneration: state.routeGeneration,
		Identity:        entry.Desired,
	}
}

// recordObserverError / recordAudit* / recordDriftRepair keep the §10.6
// diagnostics counters.
func (m *SelectedRouteManager) recordObserverError() { m.observerErrors.Add(1) }
func (m *SelectedRouteManager) recordAuditSkipped()  { m.auditSkippedBusy.Add(1) }
func (m *SelectedRouteManager) recordDriftRepair()   { m.driftRepairs.Add(1) }

func (m *SelectedRouteManager) recordAudit(failed bool) {
	m.audits.Add(1)
	if failed {
		m.auditFailures.Add(1)
	}
}

// handlePrefixInvalidation applies the §10.2/§47.3 prefix rules: applyMu is
// taken per affected role, the record is re-loaded under the lock, and every
// mutation goes through the canonical transactions. Returns an error when a
// targeted transaction fails — the caller keeps the event unclaimed.
func (m *SelectedRouteManager) handlePrefixInvalidation(ctx context.Context, prefix netip.Prefix) error {
	m.clearVerificationForPrefix(prefix)

	var firstErr error
	for _, role := range m.owner.managedRoleNames() {
		m.mu.Lock()
		state := m.roleState(role)
		restorePending := state.restorePending
		frozen := state.frozen
		quarantined := state.integrityQuarantined
		m.mu.Unlock()

		state.applyMu.Lock()
		record, ok := m.registryEntry(role, prefix)
		if !ok {
			state.applyMu.Unlock()
			continue
		}
		if restorePending && record.Park != nil {
			// The recovery engine owns the prefix; observe only (§10.2).
			state.applyMu.Unlock()
			if m.owner != nil {
				m.owner.requestManagedSelectedRecovery(role, "prefix invalidation on a checkpointed managed park")
			}
			return nil
		}
		if quarantined {
			// §31.21: quarantined roles observe only.
			state.applyMu.Unlock()
			continue
		}
		if frozen && !restorePending {
			state.applyMu.Unlock()
			continue
		}
		var err error
		switch record.State {
		case routing.SelectedRetiring:
			err = m.retireRecordLocked(ctx, role, prefix)
		case routing.SelectedPending, routing.SelectedApplying:
			_, err = m.reconcileRecordLocked(ctx, role, prefix, "prefix invalidation")
		case routing.SelectedActive:
			// §47.3: an Active prefix event is classified/live-observed
			// through the same canonical proof (BeginRepair if the row is
			// actually gone, plain re-verify if present).
			_, err = m.reconcileRecordLocked(ctx, role, prefix, "prefix invalidation re-verify")
		}
		state.applyMu.Unlock()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// handleInterfaceInvalidation applies the §10.2/§37.9 interface rules:
// tokens on that ifindex clear and the full managed recovery is requested
// for every affected managed role.
func (m *SelectedRouteManager) handleInterfaceInvalidation(ctx context.Context, ifindex int) error {
	m.clearVerificationForIfindex(ifindex)
	_ = ctx
	for _, role := range m.owner.managedRoleNames() {
		m.mu.Lock()
		state, ok := m.roles[role]
		quarantined := ok && state.integrityQuarantined
		m.mu.Unlock()
		if quarantined {
			// §37.9: a quarantined role observes only — no recovery loop.
			continue
		}
		// §37.9: every affected managed role requests the full managed
		// recovery sequence; the observer never rebinds records to an
		// arbitrary current interface before Rebind.
		if m.owner != nil {
			m.owner.requestManagedSelectedRecovery(role, "managed interface identity changed")
		}
	}
	return nil
}

// failSelectedRouteIntegrity enters the §31.21 quarantine: freeze, record,
// clear tokens, stop automatic recovery retries, publish the incident —
// and never delete or replace the foreign row.
func (m *Manager) failSelectedRouteIntegrity(role string, err error) {
	if m.selectedRoutes == nil {
		return
	}
	m.selectedRoutes.mu.Lock()
	state := m.selectedRoutes.roleState(role)
	state.frozen = true
	state.integrityQuarantined = true
	state.integrityReason = err.Error()
	state.verification = nil
	m.selectedRoutes.mu.Unlock()
	// The product surface learns the incident through the role's last
	// error; the Toad process is not killed (§31.21) and no resource is
	// released.
	m.product.SetRoleDiagnostic(role, fmt.Sprintf("selected-route integrity: %v", err))
}

// selectedRolesQuarantined reads the quarantine flag (m.mu held).
func (m *SelectedRouteManager) selectedRolesQuarantined(role string) bool {
	return m.roleState(role).integrityQuarantined
}

// RetryQuarantinedRole implements the §37.10 RetryRole entry: a full exact
// snapshot decides — the conflict remains keeps the quarantine with zero
// destructive mutation; the conflict gone clears the quarantine (the role
// stays frozen) and requests the full managed recovery.
func (m *SelectedRouteManager) retryQuarantinedRole(ctx context.Context, role string) error {
	m.mu.Lock()
	state := m.roleState(role)
	quarantined := state.integrityQuarantined
	m.mu.Unlock()
	if !quarantined {
		return fmt.Errorf("role %q is not quarantined", role)
	}
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	snapshot, err := m.routes.SnapshotSelectedRoutes(ctx)
	if err != nil {
		return err
	}
	for _, entry := range m.registry.Snapshot(role) {
		for _, row := range snapshot[entry.Prefix] {
			if row.Kind != routing.SelectedKernelForeign {
				continue
			}
			// The conflict remains: keep the quarantine (§31.21).
			return fmt.Errorf("integrity conflict remains on %s: %s", entry.Prefix, row.Description)
		}
	}
	m.mu.Lock()
	state = m.roleState(role)
	state.integrityQuarantined = false
	state.integrityReason = ""
	m.mu.Unlock()
	return nil
}
