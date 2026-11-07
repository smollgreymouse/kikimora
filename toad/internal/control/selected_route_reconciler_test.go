package control

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// The 08D1 §37.12 owning regression suite for the selected-route reconciler.

// fakeObservationSource is a controllable platform.SelectedRouteObservation.
type fakeObservationSource struct {
	mu        syncMutex
	sequence  uint64
	snapErr   error
	snapGate  chan struct{}
	rows      map[netip.Prefix][]routing.SelectedKernelRow
	snapshots int
}

type syncMutex = struct {
	mu sync.Mutex
}

// reconcilerHarness wires a manager + reconciler with injectable clocks and
// a scripted platform.
type reconcilerHarness struct {
	manager  *Manager
	sr       *SelectedRouteManager
	rec      *selectedRouteReconciler
	fake     *selectedRoutesFake
	obs      *fakeObservationSource
	tick     chan tick
	stopTick func()
}

func newReconcilerHarness(t *testing.T, role string) *reconcilerHarness {
	t.Helper()
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, role, "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })

	fake := newSelectedRoutesFake()
	obs := &fakeObservationSource{rows: make(map[netip.Prefix][]routing.SelectedKernelRow)}
	manager.selectedRoutes.SetRoutes(&observationFake{selectedRoutesFake: fake, source: obs})
	manager.SetManagedSelectedRole(role, true)

	h := &reconcilerHarness{
		manager: manager,
		sr:      manager.selectedRoutes,
		fake:    fake,
		obs:     obs,
	}
	h.rec = &selectedRouteReconciler{
		manager:       manager,
		routes:        manager.selectedRoutes.routes,
		auditInterval: time.Hour, // deterministic: only explicit audits run
		debounce:      10 * time.Millisecond,
	}
	h.rec.newTicker = func(interval time.Duration) (chan tick, func()) {
		h.tick = make(chan tick, 8)
		return h.tick, func() {}
	}
	h.sr.reconciler = h.rec
	h.sr.observation = obs
	return h
}

// observationFake forwards the RouteManager surface to the fake and the
// observation surface to the scripted source.
type observationFake struct {
	*selectedRoutesFake
	source *fakeObservationSource
}

func (o *observationFake) CurrentSelectedRouteSequence() uint64 {
	o.source.mu.mu.Lock()
	defer o.source.mu.mu.Unlock()
	return o.source.sequence
}

func (o *observationFake) SnapshotSelectedRoutes(ctx context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	o.source.mu.mu.Lock()
	o.source.snapshots++
	snapErr := o.source.snapErr
	rows := o.source.rows
	if o.source.snapGate != nil {
		gate := o.source.snapGate
		o.source.mu.mu.Unlock()
		<-gate
		o.source.mu.mu.Lock()
	}
	o.source.mu.mu.Unlock()
	if snapErr != nil {
		return nil, snapErr
	}
	out := make(map[netip.Prefix][]routing.SelectedKernelRow, len(rows))
	for prefix, list := range rows {
		out[prefix] = append(out[prefix], list...)
	}
	return out, nil
}

func (o *observationFake) SubscribeSelectedRouteInvalidations(ctx context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	ch := make(chan routing.SelectedRouteInvalidation)
	close(ch)
	return ch, nil
}

func (o *observationFake) NormalizeSelectedRoute(ctx context.Context, d routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error) {
	return o.selectedRoutesFake.NormalizeSelectedRoute(ctx, d)
}

// CurrentSelectedRouteSequence implements the observation surface.
func (f *fakeObservationSource) CurrentSelectedRouteSequence() uint64 {
	return f.currentSequence()
}

func (f *fakeObservationSource) advance() uint64 {
	f.mu.mu.Lock()
	defer f.mu.mu.Unlock()
	f.sequence++
	return f.sequence
}

func (f *fakeObservationSource) setRows(prefix netip.Prefix, rows []routing.SelectedKernelRow) {
	f.mu.mu.Lock()
	defer f.mu.mu.Unlock()
	f.rows[prefix] = rows
}

// seedActive registers an Active record and mints its token.
func seedActive(t *testing.T, h *reconcilerHarness, prefixText string, ifindex int) routing.SelectedRoute {
	t.Helper()
	prefix := netip.MustParsePrefix(prefixText)
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: prefix, IfIndex: ifindex}
	identity, err := h.fake.NormalizeSelectedRoute(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sr.registry.Reserve("primary", "primary", prefix, routing.SelectedDNS, 1, 1, "kk0", identity); err != nil {
		t.Fatal(err)
	}
	if err := h.sr.registry.MarkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := h.sr.registry.MarkActive("primary", prefix, identity); err != nil {
		t.Fatal(err)
	}
	entry, _ := h.sr.registryEntry("primary", prefix)
	h.sr.mintVerificationToken("primary", *entry)
	h.obs.setRows(prefix, []routing.SelectedKernelRow{{
		Kind:  routing.SelectedKernelRoute,
		Route: identity,
	}})
	return *entry
}

// TestInitialObserverFailureKeepsAuthorityClosed: §37.1/§44.
func TestInitialObserverFailureKeepsAuthorityClosed(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	// No writer wired: the proof fails and the gate stays closed.
	h.rec.routes = nil
	if _, err := h.rec.initialAuthorityProof(context.Background()); err == nil {
		t.Fatal("a missing writer must fail the initial proof")
	}
	if h.sr.authorityReady.Load() {
		t.Fatal("the gate must stay closed after a failed proof")
	}
}

// TestInitialAuditFailureKeepsAuthorityClosed: §37.1/§44.
func TestInitialAuditFailureKeepsAuthorityClosed(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)
	h.obs.snapErr = errors.New("snapshot unavailable")
	if _, err := h.rec.initialAuthorityProof(context.Background()); err == nil {
		t.Fatal("a failing snapshot must fail the initial proof")
	}
	if h.sr.authorityReady.Load() {
		t.Fatal("the gate must stay closed after a failed snapshot")
	}
	if h.obs.snapshotCount() != 1 {
		t.Fatalf("one snapshot attempt expected: %d", h.obs.snapshotCount())
	}
}

// scriptedSnapshotFake moves the observation sequence during the first
// snapshot and is quiet afterwards.
type scriptedSnapshotFake struct {
	observationFake
	movements int
}

func (s *scriptedSnapshotFake) SnapshotSelectedRoutes(ctx context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	if s.movements == 0 {
		s.movements++
		// The event lands "during" the snapshot: the sequence moves under
		// the read.
		s.source.advance()
	}
	return s.observationFake.SnapshotSelectedRoutes(ctx)
}

// stableSnapshotErr exposes the error for the discard assertion.
func (r *selectedRouteReconciler) stableSnapshotErr(ctx context.Context) error {
	_, err := r.stableSnapshot(ctx)
	return err
}

// TestStableSnapshotDiscardsEventDuringRead: §37.2/§44 — an event injected
// between the snapshot start and completion discards the stale observation;
// the loop retries and the quiet snapshot succeeds with the current
// sequence.
func TestStableSnapshotDiscardsEventDuringRead(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)

	scripted := &scriptedSnapshotFake{observationFake: observationFake{
		selectedRoutesFake: h.fake,
		source:             h.obs,
	}}
	h.rec.routes = scripted

	// The first attempt discards the stale snapshot; the retry succeeds.
	snap, err := h.rec.stableSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Sequence != h.obs.currentSequence() {
		t.Fatalf("the stable snapshot must carry the current sequence: %d vs %d",
			snap.Sequence, h.obs.currentSequence())
	}
}

// TestAuditAdvancesProcessedSequenceOnlyOnSuccess: §37.2/§40.4.
func TestAuditAdvancesProcessedSequenceOnlyOnSuccess(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)
	h.obs.advance() // an event the audit must observe

	h.rec.audit(context.Background())
	if got := h.rec.processedSequence.Load(); got != h.obs.currentSequence() {
		t.Fatalf("a successful audit must advance the processed sequence to %d, got %d",
			h.obs.currentSequence(), got)
	}

	// A failing audit must NOT advance the processed sequence.
	h.obs.snapErr = errors.New("down")
	before := h.rec.processedSequence.Load()
	h.obs.advance()
	h.rec.audit(context.Background())
	if got := h.rec.processedSequence.Load(); got != before {
		t.Fatalf("a failed audit must not advance the processed sequence: %d -> %d", before, got)
	}
}

// TestActiveDriftRepairsThroughPendingCheckpoint: §37.4/§44 — the external
// delete of an Active row repairs exactly once through BeginRepair and the
// Pending checkpoint.
func TestActiveDriftRepairsThroughPendingCheckpoint(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	entry := seedActive(t, h, "203.0.113.10/32", 5)

	// The external delete: the snapshot no longer holds the row.
	h.obs.setRows(entry.Prefix, nil)

	checkpoints := make([]string, 0, 4)
	h.sr.SetFailpoint(func(point, role string, prefix netip.Prefix) error {
		if point == "route.after_pending_checkpoint" {
			checkpoints = append(checkpoints, "pending")
		}
		return nil
	})

	if err := h.rec.reconcileSnapshotChecked(context.Background(), stableSelectedSnapshot{
		Rows:     map[netip.Prefix][]routing.SelectedKernelRow{},
		Sequence: h.obs.currentSequence(),
	}); err != nil {
		t.Fatal(err)
	}

	// The record came back Active with the same Desired (BeginRepair kept
	// the identity) and the repair counted.
	if got := h.sr.RegistrySnapshot("primary")[0].State; got != routing.SelectedActive {
		t.Fatalf("the repair must land Active, got %q", got)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("the repair must pass through the Pending checkpoint once: %v", checkpoints)
	}
	if h.sr.Diagnostics("primary").DriftRepairs != 1 {
		t.Fatalf("DriftRepairs must count the repair: %#v", h.sr.Diagnostics("primary"))
	}
}

// TestForeignRowQuarantinesFromAudit: §37.11/§31.21/§44 — an unauthorized
// same-prefix row quarantines instead of overwriting, and a RetryRole with
// the conflict still present keeps the quarantine.
func TestForeignRowQuarantinesFromAudit(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	entry := seedActive(t, h, "203.0.113.10/32", 5)

	// A foreign row occupies the prefix.
	h.obs.setRows(entry.Prefix, []routing.SelectedKernelRow{{
		Kind:        routing.SelectedKernelForeign,
		Description: "foreign fixture",
	}})
	err := h.rec.reconcileSnapshotChecked(context.Background(), stableSelectedSnapshot{
		Rows:     h.obs.snapshotRows(),
		Sequence: h.obs.currentSequence(),
	})
	if err == nil {
		t.Fatal("a foreign row must fail the audit")
	}
	state := h.sr.roleState("primary")
	if !state.integrityQuarantined {
		t.Fatal("the role must be quarantined")
	}
	if !state.frozen {
		t.Fatal("the quarantine must freeze the role")
	}

	// The conflict remains: the retry keeps the quarantine with zero
	// destructive mutation.
	if err := h.sr.retryQuarantinedRole(context.Background(), "primary"); err == nil {
		t.Fatal("a retry while the conflict remains must be refused")
	}
	if !h.sr.roleState("primary").integrityQuarantined {
		t.Fatal("the quarantine must persist while the conflict remains")
	}

	// The conflict is gone: the retry clears the quarantine (frozen) and
	// requests the managed recovery.
	h.obs.setRows(entry.Prefix, []routing.SelectedKernelRow{{
		Kind:  routing.SelectedKernelRoute,
		Route: entry.Desired,
	}})
	if err := h.sr.retryQuarantinedRole(context.Background(), "primary"); err != nil {
		t.Fatalf("the retry must clear a resolved quarantine: %v", err)
	}
	if h.sr.roleState("primary").integrityQuarantined {
		t.Fatal("the quarantine must clear after the conflict is gone")
	}
	if !h.sr.roleState("primary").frozen {
		t.Fatal("the role must stay frozen until the recovery owns the Unfreeze")
	}
}

// TestRetireRecordPreservesParkUntilExactCleanup: §37.6/§44.
func TestRetireRecordPreservesParkUntilExactCleanup(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	entry := seedActive(t, h, "203.0.113.10/32", 5)

	park := routing.SelectedParkIdentity{
		Kind: routing.SelectedParkUnreachable, Table: routing.ManagedSelectedRouteTable,
		Protocol: 4, Metric: routing.ManagedParkMetric,
	}
	if err := h.sr.registry.ReservePark("primary", entry.Prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := h.sr.registry.MarkParkApplying("primary", entry.Prefix); err != nil {
		t.Fatal(err)
	}
	if err := h.sr.registry.MarkParkActive("primary", entry.Prefix, park); err != nil {
		t.Fatal(err)
	}

	// The fake's park surface reports the exact park present.
	h.fake.parkRows[entry.Prefix.String()] = park

	state := h.sr.roleState("primary")
	state.applyMu.Lock()
	err := h.sr.retireRecordLocked(context.Background(), "primary", entry.Prefix)
	state.applyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if remaining := h.sr.RegistrySnapshot("primary"); len(remaining) != 0 {
		t.Fatalf("the registry entry must be gone after the retirement: %#v", remaining)
	}
	// The park transaction completed before the registry removal.
	if calls := h.fake.parkRemoves; calls != 1 {
		t.Fatalf("the exact park removal must run once: %d", calls)
	}
}

// TestInterfaceInvalidationRequestsRecovery: §37.9/§44 — an interface
// invalidation requests the full managed recovery for every affected role.
func TestInterfaceInvalidationRequestsRecovery(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)

	// The recovery request path requires a driver; inject a recording one.
	driver := &managedSequenceDriver{manager: h.manager}
	h.manager.SetRecoveryDriver(driver)
	h.manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}
	h.manager.mu.Lock()
	if r, ok := h.manager.roles["primary"]; ok {
		r.enabled = true
		r.process = &fakeProcess{done: make(chan error, 1)}
		h.manager.roles["primary"] = r
	}
	h.manager.mu.Unlock()
	if err := h.manager.product.SetRoleDesired(context.Background(), "primary", true); err != nil {
		t.Fatal(err)
	}
	h.manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}},
		Reason:   netstate.ChangeInitial,
	})
	drift := toadctl.Snapshot{
		Generation: 10, Revision: 2, State: "degraded", RouteReady: false,
		InterfaceName: "kkprimary", IfIndex: 7, MTU: 1380,
		Addresses:    []string{"10.0.0.1/24"},
		Capabilities: toadctl.Capabilities{Validate: true, RestartTransportKeepingTUN: true},
	}
	if live, accepted := h.manager.observeToadSnapshotForProcess("primary", h.manager.roles["primary"].process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	if !h.manager.product.SetRecovery("primary", core.RecoveryState{}, core.RoleRecovering, "fixture route-target loss") {
		t.Fatal("the role must enter recovering")
	}

	h.sr.handleInterfaceInvalidation(context.Background(), 7)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(driver.recordedSteps()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	steps := driver.recordedSteps()
	if len(steps) == 0 {
		t.Fatal("an interface invalidation must request the managed recovery")
	}
	if steps[0] != core.RecoveryFreezeSelected {
		t.Fatalf("the managed sequence must start with freeze-selected: %v", steps)
	}
}

// snapshotRows exposes the observation rows for one-shot proofs.
func (f *fakeObservationSource) snapshotRows() map[netip.Prefix][]routing.SelectedKernelRow {
	out := make(map[netip.Prefix][]routing.SelectedKernelRow, len(f.rows))
	for prefix, list := range f.rows {
		out[prefix] = append(out[prefix], list...)
	}
	return out
}

func (f *fakeObservationSource) currentSequence() uint64 {
	f.mu.mu.Lock()
	defer f.mu.mu.Unlock()
	return f.sequence
}

func (f *fakeObservationSource) snapshotCount() int {
	f.mu.mu.Lock()
	defer f.mu.mu.Unlock()
	return f.snapshots
}

// sessionCountingFake counts native subscriptions and closes the channel on
// cancellation — the Linux NotifyRouteChange2/RouteSubscribe contract.
type sessionCountingFake struct {
	observationFake
	registers   int
	unregisters int
	closed      chan struct{}
	mu          sync.Mutex
}

// subscribeCountingFake routes Subscribe through the counting register so
// the session test observes the exact native subscription count.
type subscribeCountingFake struct {
	sessionCountingFake
}

func (s *subscribeCountingFake) SubscribeSelectedRouteInvalidations(context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	out := make(chan routing.SelectedRouteInvalidation, 8)
	s.mu.Lock()
	s.registers++
	s.mu.Unlock()
	return out, nil
}

// ctxClosingFake models the platform subscription contract: the returned
// stream closes when the subscription context is canceled.
type ctxClosingFake struct {
	observationFake
	out chan routing.SelectedRouteInvalidation
}

func (f *ctxClosingFake) SubscribeSelectedRouteInvalidations(ctx context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	f.out = make(chan routing.SelectedRouteInvalidation)
	go func() {
		<-ctx.Done()
		close(f.out)
	}()
	return f.out, nil
}

// TestFailedProofCancelsRetainedSubscription: §47.1 — a proof that fails
// after subscribing must tear the subscription down; the event channel is
// never discarded and left advancing the sequence.
func TestFailedProofCancelsRetainedSubscription(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)
	h.obs.snapErr = errors.New("snapshot unavailable")
	sub := &ctxClosingFake{observationFake: observationFake{
		selectedRoutesFake: h.fake,
		source:             h.obs,
	}}
	h.rec.routes = sub

	if _, err := h.rec.initialAuthorityProof(context.Background()); err == nil {
		t.Fatal("a failing snapshot must fail the initial proof")
	}
	select {
	case _, ok := <-sub.out:
		if ok {
			t.Fatal("the failed proof must close its subscription, not discard it")
		}
	case <-time.After(time.Second):
		t.Fatal("the failed proof left its subscription alive")
	}
}

// TestOneObserverSessionPerStartup: §47.1 — the proof subscribes once and
// the retained session is the one the loop consumes; no discarded channels.
func TestOneObserverSessionPerStartup(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)
	api := &subscribeCountingFake{sessionCountingFake: sessionCountingFake{
		observationFake: observationFake{
			selectedRoutesFake: h.fake,
			source:             h.obs,
		},
	}}
	h.rec.routes = api

	session, err := h.rec.initialAuthorityProof(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if api.registers != 1 {
		t.Fatalf("the proof must subscribe exactly once: %d", api.registers)
	}
	if session == nil || session.Events == nil {
		t.Fatal("the retained session must carry the event channel")
	}
	if h.sr.authorityReady.Load() != true {
		t.Fatal("a successful proof must open the gate")
	}
}

// TestObserverLossClosesAuthority: §47.2 — loss closes the gate immediately;
// a failed replacement keeps it closed; a successful re-proof reopens.
func TestObserverLossClosesAuthority(t *testing.T) {
	h := newReconcilerHarness(t, "primary")
	seedActive(t, h, "203.0.113.10/32", 5)

	// Successful startup proof opens the gate.
	session, err := h.rec.initialAuthorityProof(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !h.sr.authorityReady.Load() {
		t.Fatal("the gate must be open after a successful proof")
	}

	// Observer loss: the gate closes BEFORE the replacement proof.
	h.rec.closeAuthorityGate("route observer lost / reinitializing")
	if h.sr.authorityReady.Load() {
		t.Fatal("observer loss must close the gate immediately")
	}
	_ = session

	// A failed re-proof keeps the gate closed.
	h.rec.routes = nil
	if _, err := h.rec.initialAuthorityProof(context.Background()); err == nil {
		t.Fatal("the replacement proof must fail without a writer")
	}
	if h.sr.authorityReady.Load() {
		t.Fatal("a failed replacement must keep the gate closed")
	}

	// A successful re-proof reopens.
	h.rec.routes = newSelectedRoutesFake()
	if _, err := h.rec.initialAuthorityProof(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !h.sr.authorityReady.Load() {
		t.Fatal("a successful re-proof must reopen the gate")
	}
}
