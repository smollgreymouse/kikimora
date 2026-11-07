//go:build windows

package routes

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The 08D1 §38.6 native matrix for the coherent cache, the exact Replace and
// the EEXIST classification. The fakeRouteApi is the only syscall seam
// (§38.4); call order comes from the recorded created/deleted slices.

func reconcileTestRecord(t *testing.T, e *Executor, prefixText string, ifindex int, state routing.SelectedRouteState) *routing.SelectedRoute {
	t.Helper()
	prefix := netip.MustParsePrefix(prefixText)
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: prefix, IfIndex: ifindex}
	identity, err := e.NormalizeSelectedRoute(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	return &routing.SelectedRoute{
		Role: "primary", Zone: "primary", Prefix: prefix,
		Source: routing.SelectedDNS, ToadGeneration: 1, RouteGeneration: 1,
		Desired: identity, State: state,
	}
}

func TestSelectedCacheStableDuplicatesCauseZeroScans(t *testing.T) {
	// §38.1/§44: 1000 converged duplicate requests perform zero new full FIB
	// scans.
	calls := withKernel(t, nil)
	e := newFakeExecutor(calls)
	record := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: record.Prefix, IfIndex: 5}

	if _, err := e.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
		t.Fatal(err)
	}
	if err := e.registryFakeActivate(record); err != nil {
		t.Fatal(err)
	}
	afterFirst := calls.tables

	for i := 0; i < 1000; i++ {
		if _, err := e.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	if calls.tables != afterFirst {
		t.Fatalf("converged duplicates must not scan: before=%d after=%d", afterFirst, calls.tables)
	}
}

// registryFakeActivate promotes the record to Active with Owned==Desired the
// way the manager would after MarkActive.
func (e *Executor) registryFakeActivate(record *routing.SelectedRoute) error {
	owned := record.Desired
	record.Owned = &owned
	record.State = routing.SelectedActive
	// The cache must already hold the row the read-back proved.
	e.mu.Lock()
	defer e.mu.Unlock()
	rows, ok := e.selectedCache[record.Prefix]
	if !ok {
		return errors.New("cache must be primed by the first reconcile")
	}
	for _, row := range rows {
		if routing.SelectedRowMatchesIdentity(row, record.Desired) {
			return nil
		}
	}
	return errors.New("cache holds no desired row after the apply")
}

func TestSelectedCacheGenerationMismatchRefreshesOnce(t *testing.T) {
	calls := withKernel(t, nil)
	e := newFakeExecutor(calls)
	record := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: record.Prefix, IfIndex: 5}

	if _, err := e.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
		t.Fatal(err)
	}
	if err := e.registryFakeActivate(record); err != nil {
		t.Fatal(err)
	}
	baseline := calls.tables

	// A notification moves the generation: exactly one stable refresh serves
	// the next request, and converged requests stay flat afterwards.
	e.bumpNotifyGenerationForTest()
	if _, err := e.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
		t.Fatal(err)
	}
	afterRefresh := calls.tables
	if afterRefresh != baseline+2 { // one refresh = v4+v6 table reads
		t.Fatalf("a generation mismatch must refresh exactly once: %d -> %d", baseline, afterRefresh)
	}
	for i := 0; i < 50; i++ {
		if _, err := e.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
			t.Fatal(err)
		}
	}
	if calls.tables != afterRefresh {
		t.Fatalf("post-refresh duplicates must not scan: %d -> %d", afterRefresh, calls.tables)
	}
}

func TestSelectedReplaceIsDesiredFirstThenExactOldDelete(t *testing.T) {
	// §38.2/§44: Replace creates and proves the exact Desired BEFORE deleting
	// the exact previous Owned; nothing else on the prefix is touched.
	api := withKernel(t, nil)
	api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 3, 3000, ownedRouteProtocol, 0, "")
	e := newFakeExecutor(api)
	record := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)
	old := routing.SelectedRouteIdentity{Table: kernelTable, IfIndex: 3, Protocol: ownedRouteProtocol, InterfaceLuid: 3000}
	record.Owned = &old

	result, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: record.Prefix, IfIndex: 5}, record)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Mutated {
		t.Fatal("a replacement must mutate")
	}
	if len(api.created) != 1 || len(api.deleted) != 1 {
		t.Fatalf("desired-first replace: created=%d deleted=%d", len(api.created), len(api.deleted))
	}
	if int(api.created[0].InterfaceIndex) != 5 {
		t.Fatalf("the desired row must be created first: %+v", api.created[0])
	}
	if uint64(api.deleted[0].InterfaceLUID) != 3000 || api.deleted[0].InterfaceIndex != 3 {
		t.Fatalf("only the exact old Owned may be deleted: %+v", api.deleted[0])
	}
}

func TestSelectedEEXISTExactDesiredIsSuccess(t *testing.T) {
	// §38.3: EEXIST + exact Desired only after refresh -> success. The race
	// shape: the row lands in the kernel after the plan's cache read and the
	// create hits EEXIST; the classification refresh proves the exact
	// Desired instead of replacing.
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	fresh := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)
	// The row lands in the kernel at create time, and the create reports
	// EEXIST: the classification refresh must find the exact Desired.
	luid, _ := api.luidFromIndex(5)
	api.onCreate = func() {
		api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 5, luid, ownedRouteProtocol, 0, "")
	}
	api.createErr = windows.ERROR_OBJECT_ALREADY_EXISTS
	result, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: fresh.Prefix, IfIndex: 5}, fresh)
	if err != nil {
		t.Fatalf("EEXIST with the exact desired row must converge: %v", err)
	}
	if !result.Mutated || result.Verified != fresh.Desired {
		t.Fatalf("unexpected verdict: %#v", result)
	}
	if len(api.deleted) != 0 {
		t.Fatalf("EEXIST exact must not delete: %d", len(api.deleted))
	}
}

func TestSelectedEEXISTForeignConflicts(t *testing.T) {
	// §38.3/§44: EEXIST + a foreign same-prefix row -> conflict, zero delete.
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	fresh := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)

	// The fake kernel holds a foreign row (different LUID identity) on the
	// prefix, so the EEXIST refresh classifies a conflict.
	api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 9, 9000, ownedRouteProtocol, 0, "")
	api.createErr = windows.ERROR_OBJECT_ALREADY_EXISTS
	_, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: fresh.Prefix, IfIndex: 5}, fresh)
	if err == nil {
		t.Fatal("EEXIST with a foreign row must conflict")
	}
	if len(api.deleted) != 0 {
		t.Fatalf("a foreign row must never be deleted: %d", len(api.deleted))
	}
}

func TestSelectedWrongLuidRowIsForeignAndUntouched(t *testing.T) {
	// §31.19/§44: same index, different LUID -> foreign; zero mutation.
	api := withKernel(t, nil)
	api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 5, 5555, ownedRouteProtocol, 0, "")
	e := newFakeExecutor(api)
	fresh := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)

	_, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: fresh.Prefix, IfIndex: 5}, fresh)
	if err == nil {
		t.Fatal("a same-prefix row with a different LUID is foreign")
	}
	if len(api.created) != 0 || len(api.deleted) != 0 {
		t.Fatalf("a foreign row must not be replaced or deleted: created=%d deleted=%d", len(api.created), len(api.deleted))
	}
}

func TestSelectedDuplicateDesiredConflicts(t *testing.T) {
	// §38.3: two identical Desired rows -> conflict.
	api := withKernel(t, nil)
	api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 5, 5000, ownedRouteProtocol, 0, "")
	api.addRow(netip.MustParsePrefix("203.0.113.10/32"), 5, 5000, ownedRouteProtocol, 0, "")
	e := newFakeExecutor(api)
	fresh := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)

	if _, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: fresh.Prefix, IfIndex: 5}, fresh); err == nil {
		t.Fatal("duplicate Desired rows must conflict")
	}
}

func TestSelectedCallbackRowIsInvalidationOnly(t *testing.T) {
	// §31.25/§38.5: the callback row is incomplete and never projected as
	// ownership; the callback performs zero FIB enumeration and never
	// mutates the cache directly.
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	beforeCreated, beforeDeleted := len(api.created), len(api.deleted)

	// The observer registration includes the initial snapshot; the baseline
	// is taken after it. The callback only bumps the generation and emits
	// invalidation data — observable as a generation move with no table
	// reads.
	if _, err := e.observeSelectedInvalidations(); err != nil {
		t.Fatal(err)
	}
	baselineTables := api.tables
	e.bumpNotifyGenerationForTest()
	if api.tables != baselineTables {
		t.Fatalf("the callback must not enumerate the FIB: %d -> %d", baselineTables, api.tables)
	}
	if len(api.created) != beforeCreated || len(api.deleted) != beforeDeleted {
		t.Fatal("the callback must not mutate the kernel")
	}
	// The next request refreshes against the moved generation (the cache is
	// stale), then mutates and re-proves: two stable refreshes total. The
	// converged duplicates afterwards must stay flat.
	afterBump := api.tables
	record := reconcileTestRecord(t, e, "203.0.113.10/32", 5, routing.SelectedApplying)
	if _, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: record.Prefix, IfIndex: 5}, record); err != nil {
		t.Fatal(err)
	}
	if api.tables == afterBump {
		t.Fatal("a stale cache must refresh before the request is served")
	}
	afterFirst := api.tables
	for i := 0; i < 50; i++ {
		if _, err := e.ReconcileSelectedRoute(context.Background(),
			routing.SelectedRouteDesired{Role: "primary", Prefix: record.Prefix, IfIndex: 5}, record); err != nil {
			t.Fatal(err)
		}
	}
	if api.tables != afterFirst {
		t.Fatalf("converged duplicates must not scan: %d -> %d", afterFirst, api.tables)
	}
}

func TestSelectedObserverRegisteredBeforeInitialSnapshot(t *testing.T) {
	// §38.6: the notification registration precedes the first stable
	// snapshot so no startup event is lost.
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	if _, err := e.observeSelectedInvalidations(); err != nil {
		t.Fatal(err)
	}
	if api.registers != 1 || api.tables != 2 {
		t.Fatalf("register-then-snapshot order: registers=%d tables=%d", api.registers, api.tables)
	}
	// The registration is idempotent: no extra observers.
	if _, err := e.observeSelectedInvalidations(); err != nil {
		t.Fatal(err)
	}
	if api.registers != 1 {
		t.Fatalf("the observer must register exactly once: %d", api.registers)
	}
}

func TestSelectedEffectiveMetricNonWinningKeepsPark(t *testing.T) {
	// §31.25/§44: a selected row that does not beat the park's effective
	// metric must not win the unpark proof.
	calls := withKernel(t, nil)
	e := newFakeExecutor(calls)
	prefix := netip.MustParsePrefix("203.0.113.10/32")
	selected := routing.SelectedRouteIdentity{Table: kernelTable, IfIndex: 5, Protocol: ownedRouteProtocol, Metric: 0, InterfaceLuid: 100}
	park := routing.SelectedParkIdentity{Kind: routing.SelectedParkLoopback, Table: kernelTable, IfIndex: 1, NextHop: "127.0.0.1", Protocol: ownedRouteProtocol, Metric: parkRouteMetric, InterfaceLuid: 1}

	// The fake's interface metric is 25 for every LUID: 0+25 < 42760+25 wins;
	// force the losing side with a huge selected offset instead.
	selected.Metric = 60000
	wins, err := e.SelectedWinsOverPark(context.Background(), prefix, selected, park)
	if err != nil || wins {
		t.Fatalf("a heavy selected row must not win: %v %v", wins, err)
	}
	selected.Metric = 0
	wins, err = e.SelectedWinsOverPark(context.Background(), prefix, selected, park)
	if err != nil || !wins {
		t.Fatalf("the light selected row must win: %v %v", wins, err)
	}
	_ = winipcfg.RouteProtocolNetMgmt
}

// §48.2: the first event carries Sequence 1; after successful processing the
// processed sequence equals the current observation sequence and the fast
// path re-opens without waiting for the periodic audit.
func TestWindowsEventSequenceOffByOne(t *testing.T) {
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	out, err := e.SubscribeSelectedRouteInvalidations(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The fake's row push goes through the production classify path with
	// the §48.2 increment-before-use numbering.
	api.rows = nil
	row := cannedRow(netip.MustParsePrefix("203.0.113.99/32"), 5, 5000, ownedRouteProtocol, netip.Addr{})
	api.pushRow(row)

	select {
	case inv := <-out:
		if inv.Sequence != 1 {
			t.Fatalf("the first event must carry Sequence 1, got %d", inv.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("the event never reached the stream")
	}
	_ = out
}

// §48.4 barrier: an event injected between the snapshot start and completion
// forces a retry; the stale first view is never published, the second
// snapshot sees the new view, and the cache generations line up.
func TestWindowsEventDuringSnapshotForcesRetry(t *testing.T) {
	api := withKernel(t, nil)
	e := newFakeExecutor(api)
	prefix := netip.MustParsePrefix("203.0.113.10/32")
	record := reconcileTestRecord(t, e, prefix.String(), 5, routing.SelectedApplying)
	if _, err := e.ReconcileSelectedRoute(context.Background(),
		routing.SelectedRouteDesired{Role: "primary", Prefix: prefix, IfIndex: 5}, record); err != nil {
		t.Fatal(err)
	}

	// A live subscription owns the callback the barrier injects through.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := e.SubscribeSelectedRouteInvalidations(ctx); err != nil {
		t.Fatal(err)
	}

	// The event lands after the first routeTable read started and before it
	// returned: the callback bumps the atomic generation and appends the new
	// row, but the in-flight read still holds the old view.
	barrier := netip.MustParsePrefix("203.0.113.77/32")
	api.mu.Lock()
	api.onTable = func() {
		api.pushRow(cannedRow(barrier, 5, 5000, ownedRouteProtocol, netip.Addr{}))
	}
	api.mu.Unlock()

	e.mu.Lock()
	rows, refreshErr := e.stableSelectedRefreshLocked()
	cacheGeneration := e.cacheGeneration.Load()
	notifyGeneration := e.notifyGeneration.Load()
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	if cacheGeneration != notifyGeneration {
		t.Fatalf("generations must line up after the retry: cache=%d notify=%d",
			cacheGeneration, notifyGeneration)
	}
	if len(rows[barrier]) == 0 {
		t.Fatal("the retried snapshot must include the row that landed during the first read")
	}
}

// §48.3 lifecycle: ctx cancellation unregisters exactly once and closes the
// stream; double cancellation is idempotent; a later subscription creates
// exactly one fresh registration; a callback after cancellation is ignored
// safely.
func TestWindowsObserverCancellationLifecycle(t *testing.T) {
	api := withKernel(t, nil)
	e := newFakeExecutor(api)

	ctx, cancel := context.WithCancel(context.Background())
	out, err := e.SubscribeSelectedRouteInvalidations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if api.registers != 1 {
		t.Fatalf("one registration expected, got %d", api.registers)
	}

	// The stream is live before cancellation.
	api.pushRow(cannedRow(netip.MustParsePrefix("203.0.113.10/32"), 5, 5000, ownedRouteProtocol, netip.Addr{}))
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("the event never reached the live stream")
	}

	cancel()
	// The stream closes after the native unregister.
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				goto closed
			}
		case <-time.After(10 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("the stream did not close after cancellation")
			}
		}
	}
closed:
	if api.unregisters != 1 {
		t.Fatalf("cancellation must unregister exactly once, got %d", api.unregisters)
	}

	// Double cancellation (a second caller of the same subscription) must
	// not unregister again.
	e.mu.Lock()
	sub := e.selectedObserver
	e.mu.Unlock()
	if sub != nil {
		sub.close()
	}
	if api.unregisters != 1 {
		t.Fatalf("double cancellation must still unregister once, got %d", api.unregisters)
	}

	// A callback arriving after cancellation must be ignored safely (the
	// fake keeps the stale callback registered to model an in-flight call).
	api.pushRow(cannedRow(netip.MustParsePrefix("203.0.113.11/32"), 5, 5000, ownedRouteProtocol, netip.Addr{}))

	// A fresh subscription creates one new registration.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	out2, err := e.SubscribeSelectedRouteInvalidations(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if api.registers != 2 {
		t.Fatalf("resubscription must register once, total registers=%d", api.registers)
	}
	api.pushRow(cannedRow(netip.MustParsePrefix("203.0.113.12/32"), 5, 5000, ownedRouteProtocol, netip.Addr{}))
	select {
	case <-out2:
	case <-time.After(time.Second):
		t.Fatal("the replacement subscription must deliver events")
	}
}
