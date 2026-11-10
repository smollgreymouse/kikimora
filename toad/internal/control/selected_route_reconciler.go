package control

import (
	"context"
	"fmt"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The selected-route reconciler (08D1 §10): the 10-second safety audit, the
// debounced native event handling, the observation-sequence fast-path
// suppression (§31.9), the single-flight audit discipline (§31.10), the
// startup-ready gate (§31.11) and the integrity quarantine (§31.21).

const (
	// selectedAuditInterval is the fixed periodic safety audit.
	selectedAuditInterval = 10 * time.Second
	// selectedEventDebounce coalesces native route events.
	selectedEventDebounce = 100 * time.Millisecond
	// auditDeadline bounds one full audit (§31.10).
	auditDeadline = 5 * time.Second
	// observer backoff schedule: 1s,2s,4s,8s,... capped at 30s (§10.4).
	observerBackoffMax = 30 * time.Second
)

type selectedRouteReconciler struct {
	manager *Manager
	routes  platform.RouteManager

	// Injectables for tests.
	auditInterval time.Duration
	debounce      time.Duration
	newTicker     func(interval time.Duration) (chan tick, func())

	// process singleflight (§31.10): one audit at a time; busy ticks are
	// counted, never queued.
	auditRunning   atomic.Bool
	auditSkipped   atomic.Uint64
	auditFailures  atomic.Uint64
	observerErrors atomic.Uint64
	driftRepairs   atomic.Uint64
	audits         atomic.Uint64

	// processedObservationSequence is the §31.9 fast-path gate.
	processedSequence atomic.Uint64

	// authorityError holds the §31.11 gate error text.
	authorityError atomic.Value // string
}

// openAuthorityGate opens the §31.11 route-authority gate: committed mode
// recovered, checkpoints restored, platform writer wired, observer running
// and the initial stable audit complete. A role-level restorePending stays
// untouched — that gate is per-role (CompleteStartupRestore).
func (r *selectedRouteReconciler) openAuthorityGate() {
	r.authorityError.Store("")
	if r.manager.selectedRoutes != nil {
		r.manager.selectedRoutes.authorityReady.Store(true)
	}
}

// authorityErrorText reads the last gate error for diagnostics.
func (r *selectedRouteReconciler) authorityErrorText() string {
	if v, ok := r.authorityError.Load().(string); ok {
		return v
	}
	return ""
}

type tick struct{}

// StartSelectedRouteReconciler wires the platform observer and starts the
// audit/event loops (§10). The recovery driver must already exist: route
// events and startup-resume request recovery through it.
//
// The returned channel yields exactly one value: the initial §47.1 route
// authority proof. Production startup waits on it before reporting readiness,
// so a packaged Leshy never starts against an unproven writer/observer.
func (m *Manager) StartSelectedRouteReconciler(ctx context.Context) <-chan error {
	if m.selectedRoutes == nil {
		return nil
	}
	r := &selectedRouteReconciler{
		manager:       m,
		routes:        m.selectedRoutes.routes,
		auditInterval: selectedAuditInterval,
		debounce:      selectedEventDebounce,
	}
	if m.selectedRoutes.routes != nil {
		if observation, ok := m.selectedRoutes.routes.(platform.SelectedRouteObservation); ok {
			m.selectedRoutes.observation = observation
		}
	}
	m.mu.Lock()
	m.selectedRoutes.reconciler = r
	m.mu.Unlock()
	ready := make(chan error, 1)
	go r.run(ctx, ready)
	return ready
}

// selectedObserverSession carries the one live observer subscription
// (08D1 §47.1): the reconciler subscribes exactly once for the proof and
// RETAINS that exact event channel for consumption — a discarded proof
// channel would leave a second native observer advancing the sequence
// without anyone reading it. cancel tears the subscription down (unregister
// + close) when the proof fails or the session is replaced.
type selectedObserverSession struct {
	Events <-chan routing.SelectedRouteInvalidation
	cancel context.CancelFunc
}

// Close releases the observer registration and closes the stream.
func (s *selectedObserverSession) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// run drives the reconciler: the §47.1 initial authority proof with one
// retained observer session, the audit ticker, and the debounced event
// stream with observer-loss restart. Every subscription rides the
// reconciler lifecycle context (§37.3) — cancellation unregisters the
// native observer and closes the stream.
func (r *selectedRouteReconciler) run(ctx context.Context, ready chan<- error) {
	session, err := r.initialAuthorityProof(ctx)
	if err != nil {
		r.authorityError.Store(err.Error())
	}
	if ready != nil {
		ready <- err
		close(ready)
	}

	tickerC := make(chan tick, 1)
	stopTicker := startTicker(tickerC, r.auditInterval)
	defer stopTicker()
	defer func() {
		if session != nil {
			session.Close()
		}
	}()

	backoff := time.Second
	var events <-chan routing.SelectedRouteInvalidation
	if session != nil {
		events = session.Events
	}

	for {
		if ctx.Err() != nil {
			return
		}
		if events == nil {
			// §47.2: no observer (initial proof failed or the stream was
			// lost). Authority stays closed; retry the single-subscription
			// proof with bounded backoff until it succeeds or ctx ends.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > observerBackoffMax {
				backoff = observerBackoffMax
			}
			replacement, proofErr := r.initialAuthorityProof(ctx)
			if proofErr != nil {
				r.authorityError.Store(proofErr.Error())
				continue
			}
			session = replacement
			events = replacement.Events
			backoff = time.Second
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-tickerC:
			if err := r.audit(ctx); err != nil {
				// §47.5: a failed audit does not advance the processed
				// sequence; verification was already cleared inside.
				continue
			}
		case batch, ok := <-events:
			if !ok {
				// §47.2: observer loss closes the authority gate FIRST —
				// managed Ensure stays retryable-denied while the
				// replacement subscription and stable proof run.
				r.observerErrors.Add(1)
				r.manager.selectedRoutes.recordObserverError()
				r.closeAuthorityGate("route observer lost / reinitializing")
				r.manager.selectedRoutes.ClearAllVerification()
				if session != nil {
					session.Close()
					session = nil
				}
				events = nil
				backoff = time.Second
				continue
			}
			backoff = time.Second
			if err := r.handleEventBatch(ctx, batch, events); err != nil {
				// §47.5: the failed event was not processed — the
				// observation sequence stays ahead of processedSequence, so
				// the fast path remains suppressed until a later audit
				// proves coherence.
				r.auditFailures.Add(1)
			}
		}
	}
}

// initialAuthorityProof implements §47.1/§47.2: subscribe once, take one
// stable snapshot against the same observer generation, reconcile
// successfully, publish the processed sequence, and only then open the
// authority gate. The returned session's channel is the one the event loop
// consumes — never a discarded duplicate.
func (r *selectedRouteReconciler) initialAuthorityProof(ctx context.Context) (*selectedObserverSession, error) {
	sr := r.manager.selectedRoutes
	if sr == nil || r.routes == nil {
		return nil, fmt.Errorf("no selected-route writer")
	}
	// The proof owns its subscription context: if the proof fails after the
	// observer was registered, cancel tears it down instead of leaving a
	// discarded channel advancing the sequence (§47.1).
	sessionCtx, cancel := context.WithCancel(ctx)
	fail := func(err error) (*selectedObserverSession, error) {
		cancel()
		return nil, err
	}
	events, err := r.routes.SubscribeSelectedRouteInvalidations(sessionCtx)
	if err != nil {
		return fail(fmt.Errorf("observer subscribe failed: %w", err))
	}
	snapshot, err := r.stableSnapshot(ctx)
	if err != nil {
		return fail(fmt.Errorf("stable snapshot failed: %w", err))
	}
	if err := r.reconcileSnapshotChecked(ctx, snapshot); err != nil {
		return fail(fmt.Errorf("initial reconcile failed: %w", err))
	}
	r.processedSequence.Store(snapshot.Sequence)
	sr.noteProcessedSequence(snapshot.Sequence)
	session := &selectedObserverSession{Events: events, cancel: cancel}
	r.openAuthorityGate()
	return session, nil
}

// closeAuthorityGate implements §47.2: the gate closes immediately on
// observer loss and stays closed until a fresh successful proof.
func (r *selectedRouteReconciler) closeAuthorityGate(reason string) {
	r.authorityError.Store(reason)
	if sr := r.manager.selectedRoutes; sr != nil {
		sr.authorityReady.Store(false)
	}
}

// stableSelectedSnapshot is one full-kernel observation tagged with the
// observation sequence it was consistent with (§37.2).
type stableSelectedSnapshot struct {
	Rows     map[netip.Prefix][]routing.SelectedKernelRow
	Sequence uint64
}

// stableSnapshot implements §37.2: loop until the audit deadline sampling
// the observation sequence around SnapshotSelectedRoutes; a sequence move
// under the read discards the stale snapshot. The processed sequence never
// advances on failure — the caller decides.
func (r *selectedRouteReconciler) stableSnapshot(ctx context.Context) (stableSelectedSnapshot, error) {
	deadline := time.Now().Add(auditDeadline)
	for {
		if ctx.Err() != nil {
			return stableSelectedSnapshot{}, ctx.Err()
		}
		if time.Now().After(deadline) {
			return stableSelectedSnapshot{}, fmt.Errorf("stable snapshot deadline exceeded")
		}
		before := r.currentSequence()
		rows, err := r.routes.SnapshotSelectedRoutes(ctx)
		if err != nil {
			return stableSelectedSnapshot{}, err
		}
		after := r.currentSequence()
		if before == after {
			return stableSelectedSnapshot{Rows: rows, Sequence: after}, nil
		}
	}
}

// handleEventBatch drains the channel into one debounced batch (§10.2):
// the highest Sequence wins and the affected scopes union; a gap between
// sequences escalates to Full.
func (r *selectedRouteReconciler) handleEventBatch(ctx context.Context, first routing.SelectedRouteInvalidation, events <-chan routing.SelectedRouteInvalidation) error {
	batch := first
	deadline := time.After(r.debounce)
	for {
		select {
		case <-deadline:
			return r.processBatch(ctx, batch)
		case next, ok := <-events:
			if !ok {
				return r.processBatch(ctx, batch)
			}
			if next.Sequence != batch.Sequence+1 && next.Sequence > batch.Sequence {
				// A detected event gap escalates to Full (§31.9).
				batch = routing.SelectedRouteInvalidation{
					Kind:     routing.SelectedInvalidateFull,
					Sequence: next.Sequence,
				}
				continue
			}
			if next.Sequence > batch.Sequence {
				batch.Sequence = next.Sequence
			}
			batch = mergeInvalidations(batch, next)
		}
	}
}

// mergeInvalidations unions the affected scopes; Full dominates (§10.2).
func mergeInvalidations(a, b routing.SelectedRouteInvalidation) routing.SelectedRouteInvalidation {
	if a.Kind == routing.SelectedInvalidateFull || b.Kind == routing.SelectedInvalidateFull {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull, Sequence: maxU64(a.Sequence, b.Sequence)}
	}
	if a.Kind == routing.SelectedInvalidatePrefix && b.Kind == routing.SelectedInvalidatePrefix && a.Prefix == b.Prefix {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidatePrefix, Prefix: a.Prefix, Sequence: maxU64(a.Sequence, b.Sequence)}
	}
	if a.Kind == routing.SelectedInvalidateInterface && b.Kind == routing.SelectedInvalidateInterface && a.IfIndex == b.IfIndex {
		return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateInterface, IfIndex: a.IfIndex, Sequence: maxU64(a.Sequence, b.Sequence)}
	}
	return routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull, Sequence: maxU64(a.Sequence, b.Sequence)}
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// processBatch applies one debounced event (§10.2/§47.5) through the
// canonical transaction under the role's applyMu. The processed sequence
// advances only when the batch was successfully made safe: verification for
// the affected scope is already cleared inside the handlers, and a failure
// leaves the event unclaimed for a later retry.
func (r *selectedRouteReconciler) processBatch(ctx context.Context, batch routing.SelectedRouteInvalidation) error {
	sr := r.manager.selectedRoutes
	if sr == nil {
		return nil
	}
	var err error
	switch batch.Kind {
	case routing.SelectedInvalidatePrefix:
		err = sr.handlePrefixInvalidation(ctx, batch.Prefix)
	case routing.SelectedInvalidateInterface:
		err = sr.handleInterfaceInvalidation(ctx, batch.IfIndex)
	case routing.SelectedInvalidateFull:
		sr.ClearAllVerification()
		err = r.audit(ctx)
	}
	if err != nil {
		return err
	}
	r.processedSequence.Store(batch.Sequence)
	sr.noteProcessedSequence(batch.Sequence)
	return nil
}

// currentSequence reads the platform observation sequence when present.
func (r *selectedRouteReconciler) currentSequence() uint64 {
	if r.manager.selectedRoutes != nil && r.manager.selectedRoutes.observation != nil {
		return r.manager.selectedRoutes.observation.CurrentSelectedRouteSequence()
	}
	return r.processedSequence.Load()
}

// audit runs one full safety audit (§10.3/§31.10): single-flight, one
// stable snapshot, and the processed sequence advances only when the whole
// reconcile succeeded.
func (r *selectedRouteReconciler) audit(ctx context.Context) error {
	if r.routes == nil {
		return nil
	}
	if !r.auditRunning.CompareAndSwap(false, true) {
		r.auditSkipped.Add(1)
		if sr := r.manager.selectedRoutes; sr != nil {
			sr.recordAuditSkipped()
		}
		// §47.5: a skipped tick is not a processed tick — the sequence stays
		// untouched and the caller retries on the next tick.
		return fmt.Errorf("audit skipped: previous audit still running")
	}
	defer r.auditRunning.Store(false)

	snapshot, err := r.stableSnapshot(ctx)
	r.audits.Add(1)
	if sr := r.manager.selectedRoutes; sr != nil {
		sr.recordAudit(err != nil)
	}
	if err != nil {
		r.auditFailures.Add(1)
		r.manager.selectedRoutes.ClearAllVerification()
		return err
	}
	if err := r.reconcileSnapshotChecked(ctx, snapshot); err != nil {
		r.auditFailures.Add(1)
		r.manager.selectedRoutes.ClearAllVerification()
		return err
	}
	// §37.2/§40.4: the processed sequence advances only after the full
	// reconcile succeeded.
	r.processedSequence.Store(snapshot.Sequence)
	if sr := r.manager.selectedRoutes; sr != nil {
		sr.noteProcessedSequence(snapshot.Sequence)
	}
	return nil
}

// reconcileSnapshotChecked walks the snapshot under each role's applyMu and
// re-loads the registry record before acting (§40.3): a record whose
// state/generation/identity changed since the snapshot skips the stale
// result. A per-record error (quarantine) fails the whole proof.
func (r *selectedRouteReconciler) reconcileSnapshotChecked(ctx context.Context, snapshot stableSelectedSnapshot) error {
	sr := r.manager.selectedRoutes
	m := r.manager
	for _, role := range m.managedRoleNames() {
		state := sr.roleState(role)
		entries := sr.RegistrySnapshot(role)
		complete := len(entries) > 0
		for _, entry := range entries {
			state.applyMu.Lock()
			current, ok := sr.registryEntry(role, entry.Prefix)
			if !ok || current.State != entry.State || current.Desired != entry.Desired {
				// §40.3: the snapshot is stale for this record — skip the
				// result, the next audit re-reads.
				state.applyMu.Unlock()
				complete = false
				continue
			}
			err := r.auditRecord(ctx, role, *current, snapshot.Rows[entry.Prefix], sr, m)
			state.applyMu.Unlock()
			if err != nil {
				return err
			}
			if current.State != routing.SelectedActive {
				complete = false
			}
		}
		if state.restorePending && complete && sr.CompleteStartupRestore(role) {
			continue
		}
	}
	return nil
}

// auditRecord applies the §10.3 rules for one record. The caller holds the
// role's applyMu (§40.3), and so does every canonical helper below.
func (r *selectedRouteReconciler) auditRecord(ctx context.Context, role string, entry routing.SelectedRoute, rows []routing.SelectedKernelRow, sr *SelectedRouteManager, m *Manager) error {
	if entry.Park != nil {
		// §10.3: the observer never unparks — parks ride the recovery.
		if sr.roleState(role).restorePending {
			m.requestManagedSelectedRecovery(role, "resume checkpointed managed park transaction")
		}
		return nil
	}
	if sr.roleState(role).restorePending {
		m.requestManagedSelectedRecovery(role, "resume restored managed ownership")
		return nil
	}

	// §37.11: the audit classifier is the planner's — any unauthorized
	// park, other selected identity, duplicate Desired or explicit foreign
	// row is an integrity conflict. The record itself is the authorization
	// context.
	if _, err := routing.PlanSelectedRoute(rows, &entry); err != nil {
		conflict := fmt.Errorf("foreign same-prefix row on %s: %v", entry.Prefix, err)
		m.failSelectedRouteIntegrity(role, conflict)
		return conflict
	}

	switch entry.State {
	case routing.SelectedRetiring:
		// Continue the exact retirement; allowed even when frozen (§10.3).
		return sr.retireRecordLocked(ctx, role, entry.Prefix)
	case routing.SelectedActive:
		if len(rows) == 1 && rows[0].Kind == routing.SelectedKernelRoute &&
			routing.SelectedRowMatchesIdentity(rows[0], entry.Desired) {
			// The exact row is present and the planner found no foreign
			// row: refresh the verification token (zero mutation).
			sr.mintVerificationToken(role, entry)
			return nil
		}
		// Active drift: the planner already proved the row is missing or
		// foreign. Demote through BeginRepair (§37.4), checkpoint the
		// Pending boundary, and drive the canonical transaction from
		// Pending.
		if err := sr.registry.BeginRepair(role, entry.Prefix); err != nil {
			return err
		}
		if err := sr.writeCheckpoint(role, sr.routeGenerationLocked(role)); err != nil {
			return err
		}
		if err := sr.runFailpoint("route.after_pending_checkpoint", role, entry.Prefix); err != nil {
			return err
		}
		if _, err := sr.reconcileRecordLocked(ctx, role, entry.Prefix, "audit drift repair"); err != nil {
			return err
		}
		sr.recordDriftRepair()
		return nil
	case routing.SelectedApplying, routing.SelectedPending:
		_, err := sr.reconcileRecordLocked(ctx, role, entry.Prefix, "audit resume")
		return err
	}
	return nil
}

// startTicker is the injectable ticker seam: tests replace it with a fake
// clock channel (§10.3).
func startTicker(out chan<- tick, interval time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				select {
				case out <- tick{}:
				default:
				}
			}
		}
	}()
	return func() { close(stop) }
}
