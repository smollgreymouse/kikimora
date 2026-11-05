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
func (m *Manager) StartSelectedRouteReconciler(ctx context.Context) {
	if m.selectedRoutes == nil {
		return
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
	m.selectedRoutes.reconciler = r
	go r.run(ctx)
}

// run drives the reconciler: the audit ticker, the debounced event stream
// with observer-loss restart, and the initial stable audit that opens the
// route-authority gate (§31.11).
func (r *selectedRouteReconciler) run(ctx context.Context) {
	events, err := r.subscribe()
	if err != nil {
		r.observerErrors.Add(1)
		r.manager.selectedRoutes.recordObserverError()
	}

	// The initial one-shot audit: once it completes, route authority is
	// coherent and the gate opens (§31.11).
	r.audit(ctx)
	r.openAuthorityGate()

	var tickerC chan tick
	var stopTicker func()
	if r.newTicker != nil {
		tickerC, stopTicker = r.newTicker(r.auditInterval)
	} else {
		tickerC = make(chan tick, 1)
		stopTicker = startTicker(tickerC, r.auditInterval)
	}
	defer stopTicker()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tickerC:
			// §31.10: a tick while an audit is running is counted, never
			// queued.
			r.audit(ctx)
		case batch, ok := <-events:
			if !ok {
				// Observer loss (§10.4): clear all verification, one-shot
				// full audit, resubscribe with bounded backoff.
				r.observerErrors.Add(1)
				r.manager.selectedRoutes.recordObserverError()
				r.manager.selectedRoutes.ClearAllVerification()
				r.audit(ctx)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > observerBackoffMax {
					backoff = observerBackoffMax
				}
				events, err = r.subscribe()
				if err != nil {
					r.observerErrors.Add(1)
					r.manager.selectedRoutes.recordObserverError()
					events = nil
				} else {
					backoff = time.Second
				}
				continue
			}
			backoff = time.Second
			r.handleEventBatch(ctx, batch, events)
		}
	}
}

// subscribe opens the platform observer stream.
func (r *selectedRouteReconciler) subscribe() (<-chan routing.SelectedRouteInvalidation, error) {
	if r.routes == nil {
		return nil, platform.ErrSelectedRouteObserverUnsupported
	}
	return r.routes.SubscribeSelectedRouteInvalidations(context.Background())
}

// handleEventBatch drains the channel into one debounced batch (§10.2):
// the highest Sequence wins and the affected scopes union; a gap between
// sequences escalates to Full.
func (r *selectedRouteReconciler) handleEventBatch(ctx context.Context, first routing.SelectedRouteInvalidation, events <-chan routing.SelectedRouteInvalidation) {
	batch := first
	deadline := time.After(r.debounce)
	for {
		select {
		case <-deadline:
			r.processBatch(ctx, batch)
			return
		case next, ok := <-events:
			if !ok {
				r.processBatch(ctx, batch)
				return
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

// processBatch applies one debounced event (§10.2).
func (r *selectedRouteReconciler) processBatch(ctx context.Context, batch routing.SelectedRouteInvalidation) {
	sr := r.manager.selectedRoutes
	if sr == nil {
		return
	}
	switch batch.Kind {
	case routing.SelectedInvalidatePrefix:
		sr.handlePrefixInvalidation(ctx, batch.Prefix)
	case routing.SelectedInvalidateInterface:
		sr.handleInterfaceInvalidation(ctx, batch.IfIndex)
	case routing.SelectedInvalidateFull:
		sr.ClearAllVerification()
		r.audit(ctx)
	}
	r.processedSequence.Store(batch.Sequence)
	sr.noteProcessedSequence(batch.Sequence)
}

// audit runs one full safety audit (§10.3): exactly one
// SnapshotSelectedRoutes call, single-flight, deadline-bounded.
func (r *selectedRouteReconciler) audit(ctx context.Context) {
	if r.routes == nil {
		return
	}
	if !r.auditRunning.CompareAndSwap(false, true) {
		r.auditSkipped.Add(1)
		if sr := r.manager.selectedRoutes; sr != nil {
			sr.recordAuditSkipped()
		}
		return
	}
	defer r.auditRunning.Store(false)

	auditCtx, cancel := context.WithTimeout(ctx, auditDeadline)
	defer cancel()
	snapshot, err := r.routes.SnapshotSelectedRoutes(auditCtx)
	r.audits.Add(1)
	if sr := r.manager.selectedRoutes; sr != nil {
		sr.recordAudit(err != nil)
	}
	if err != nil {
		r.auditFailures.Add(1)
		// §31.10: an audit failure invalidates zero-I/O proof until the
		// next successful audit and never triggers broad delete/replace.
		r.manager.selectedRoutes.ClearAllVerification()
		return
	}
	r.reconcileFromSnapshot(ctx, snapshot)
	r.processedSequence.Store(r.currentSequence())
}

// currentSequence reads the platform observation sequence when present.
func (r *selectedRouteReconciler) currentSequence() uint64 {
	if r.manager.selectedRoutes != nil && r.manager.selectedRoutes.observation != nil {
		return r.manager.selectedRoutes.observation.CurrentSelectedRouteSequence()
	}
	return r.processedSequence.Load()
}

// reconcileFromSnapshot walks every marker-managed role's records against
// the snapshot (§10.3) and applies the state rules.
func (r *selectedRouteReconciler) reconcileFromSnapshot(ctx context.Context, snapshot map[netip.Prefix][]routing.SelectedKernelRow) {
	sr := r.manager.selectedRoutes
	m := r.manager
	m.mu.Lock()
	managed := make(map[string]bool, len(m.managedSelectedRoles))
	for name, on := range m.managedSelectedRoles {
		managed[name] = on
	}
	m.mu.Unlock()

	for role, on := range managed {
		if !on {
			continue
		}
		state := sr.roleState(role)
		sr.mu.Lock()
		restorePending := state.restorePending
		frozen := state.frozen
		quarantined := state.integrityQuarantined
		sr.mu.Unlock()
		if quarantined {
			// §31.21: quarantined roles only observe; RetryRole owns the
			// exit.
			continue
		}
		entries := sr.RegistrySnapshot(role)
		complete := len(entries) > 0
		for _, entry := range entries {
			rows := snapshot[entry.Prefix]
			action := r.auditRecord(ctx, role, entry, rows, restorePending, frozen)
			if !action {
				complete = false
			}
		}
		// §10.3: after a non-Park startup checkpoint has all non-Retiring
		// records Active with current verification, no Retiring remains and
		// Park==nil, the startup gate clears.
		if restorePending && complete && sr.CompleteStartupRestore(role) {
			continue
		}
	}
}

// auditRecord applies the §10.3 rules for one record; the result reports
// whether the record is fully proven (Active with a fresh token).
func (r *selectedRouteReconciler) auditRecord(ctx context.Context, role string, entry routing.SelectedRoute, rows []routing.SelectedKernelRow, restorePending, frozen bool) bool {
	sr := r.manager.selectedRoutes

	// Startup cases first: parks and recovery-owned prefixes never get
	// observer-side repairs (§10.3).
	if entry.Park != nil {
		if restorePending {
			// §10.3: restorePending + Park != nil -> request full managed
			// recovery; the observer never unparks.
			r.manager.requestManagedSelectedRecovery(role, "resume checkpointed managed park transaction")
			return false
		}
		// A park outside startup belongs to the recovery engine; observe
		// only.
		return false
	}
	if frozen && restorePending {
		r.manager.requestManagedSelectedRecovery(role, "resume restored managed ownership")
		return false
	}

	classify := func(row routing.SelectedKernelRow, record *routing.SelectedRoute) string {
		if row.Kind == routing.SelectedKernelForeign {
			return "foreign"
		}
		if row.Kind == routing.SelectedKernelPark {
			return "park"
		}
		if routing.SelectedRowMatchesIdentity(row, record.Desired) {
			return "desired"
		}
		if record.Owned != nil && routing.SelectedRowMatchesIdentity(row, *record.Owned) {
			return "owned"
		}
		return "other"
	}
	desiredCount, ownedCount, foreign := 0, 0, false
	for _, row := range rows {
		switch classify(row, &entry) {
		case "foreign":
			foreign = true
		case "desired":
			desiredCount++
		case "owned":
			ownedCount++
		}
	}
	if foreign {
		// §10.3/§31.21: an unrecognized same-prefix row is an integrity
		// failure with a dedicated quarantine, never an overwrite.
		r.manager.failSelectedRouteIntegrity(role, fmt.Errorf(
			"foreign same-prefix row on %s conflicts with the managed ownership", entry.Prefix))
		return false
	}

	switch entry.State {
	case routing.SelectedRetiring:
		// Continue the exact selected+park retirement; allowed even when
		// frozen because it reduces explicitly retired ownership (§10.3).
		sr.continueRetirement(ctx, role, entry)
		return false
	case routing.SelectedActive:
		if desiredCount == 1 && !frozen {
			// The exact row is present and no foreign row exists: refresh
			// the verification token (zero mutation).
			sr.mintVerificationToken(role, entry)
			return true
		}
		if desiredCount == 0 && !frozen {
			// SelectedActive missing/stale row with a healthy role: exact
			// targeted repair (§10.3).
			if r.repairRecord(ctx, role, entry) {
				r.driftRepairs.Add(1)
				sr.recordDriftRepair()
				return true
			}
		}
		return desiredCount == 1
	case routing.SelectedApplying:
		if !frozen {
			// Complete the authorized transaction (§10.3).
			sr.resumeApplyingRecord(ctx, role, entry)
		}
		return false
	case routing.SelectedPending:
		if !frozen {
			// Preflight/continue through a targeted reconcile (§10.3).
			sr.resumeApplyingRecord(ctx, role, entry)
		}
		return false
	}
	return false
}

// repairRecord drives one exact targeted reconcile for a drifted Active
// record.
func (r *selectedRouteReconciler) repairRecord(ctx context.Context, role string, entry routing.SelectedRoute) bool {
	sr := r.manager.selectedRoutes
	desired := routing.SelectedRouteDesired{Role: role, Prefix: entry.Prefix, IfIndex: entry.Desired.IfIndex}
	record, ok := sr.registryEntry(role, entry.Prefix)
	if !ok {
		return false
	}
	if err := sr.registry.MarkApplying(role, entry.Prefix); err != nil {
		return false
	}
	result, err := r.manager.selectedRoutes.routes.ReconcileSelectedRoute(ctx, desired, record)
	if err != nil {
		return false
	}
	if err := sr.registry.MarkActive(role, entry.Prefix, result.Verified); err != nil {
		return false
	}
	sr.writeCheckpoint(role, sr.routeGenerationLocked(role))
	sr.mintVerificationToken(role, entry)
	return true
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
