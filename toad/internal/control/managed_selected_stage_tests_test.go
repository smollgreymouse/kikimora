package control

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/lockfile"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// Real tests for the Stage-0 skeleton items whose owning stages landed
// (08D1 §36.5). Each test replaces one skip.

// --- writer fence (Stage 1B, §31.2) -----------------------------------------

// The OS writer fence is machine-verifiable: one holder excludes everyone
// else before DNS/routes, an abnormal exit releases it, and the content of
// the lock file is never authority. The control-plane wiring of the fence
// into Enable/Disable is the Stage 6.2 transaction; the primitive contract
// lives here.
func TestWriterFenceExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leshy-writer.lock")
	leshy, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer leshy.Close()

	// A second Leshy on the same generated installation fails before any
	// route or DNS mutation could happen.
	if _, err := lockfile.Acquire(path); err == nil {
		t.Fatal("a second writer must be refused while the lease is held")
	}
	// The core's Enable/Disable fence shares the same contract: acquiring
	// while busy fails, after release it succeeds.
	if err := leshy.Close(); err != nil {
		t.Fatal(err)
	}
	core, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("the fence must be acquirable after release: %v", err)
	}
	core.Close()
}

// --- core StateDir singleton (Stage 1B, §31.15) -----------------------------

func TestCoreStateDirSingletonLock(t *testing.T) {
	dir := t.TempDir()
	first, err := lockfile.Acquire(filepath.Join(dir, "core.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// A second core on the same StateDir must fail before it opens a
	// socket, launches a Toad or mutates a route.
	if _, err := lockfile.Acquire(filepath.Join(dir, "core.lock")); err == nil {
		t.Fatal("a second core must be refused on the same StateDir")
	}
}

// --- privileged auth (Stage 1C, §31.6) ---------------------------------------

// The API surface denies unauthenticated privileged mutations before intent
// parsing, with zero registry/kernel mutation.
func TestPrivilegedAuthDeniedThroughHandle(t *testing.T) {
	dir := t.TempDir()
	key, err := EnsurePrivilegedAuthKey(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager([]string{writeConfig(t, t.TempDir(), "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}
	manager.SetPrivilegedAuth(key)

	request := Request{Version: APIVersion, ID: "auth", Method: "EnsureLeshyRoute",
		LeshyRoute: &LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}}

	// Correct token: the request reaches the transaction.
	okResponse := manager.Handle(context.Background(), func() Request {
		r := request
		r.PrivilegedAuth = encodePrivilegedAuthKey(key)
		return r
	}())
	if !okResponse.OK {
		t.Fatalf("the correct credential must pass: %#v", okResponse.APIError)
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("the authorized ensure must apply: %d", fake.reconcileCount())
	}

	// An empty token rides the isolated legacy v1 branch while the install
	// is pre-schema2 (§31.6 rollout); a wrong token is always denied.
	wrong := request
	wrong.PrivilegedAuth = "wrong-token"
	response := manager.Handle(context.Background(), wrong)
	if response.OK || response.APIError == nil || response.APIError.Code != "privileged_auth_required" {
		t.Fatalf("a wrong token must be denied with privileged_auth_required: %#v", response)
	}
	if response.APIError.Retryable {
		t.Fatal("the auth denial must be non-retryable")
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("denied mutations must not touch the kernel: %d", fake.reconcileCount())
	}
}

// --- K14 production recovery selector (§0.8) ----------------------------------

// Manager.recoverRole on a marker-managed role must reach the managed
// sequence through the production engine — FreezeSelected first, the
// WithdrawSelected step, UnfreezeSelected last — not the legacy sequence.
func TestProductionRecoverRoleDrivesManagedSequence(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.SetManagedSelectedRole("primary", true)
	manager.selectedRoutes.SetRoutes(newSelectedRoutesFake())
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.mu.Lock()
	if r, ok := manager.roles["primary"]; ok {
		r.enabled = true
		manager.roles["primary"] = r
	}
	manager.mu.Unlock()

	// The engine picks the managed sequence for a restarting transport: put
	// the role into the state DecideRole maps to ActionRestartTransport
	// (recovering with a structurally lost route target, same fixture
	// pattern as the route-target regression).
	manager.mu.Lock()
	if r, ok := manager.roles["primary"]; ok {
		r.enabled = true
		r.process = &fakeProcess{done: make(chan error, 1)}
		manager.roles["primary"] = r
	}
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "primary", true); err != nil {
		t.Fatal(err)
	}
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}},
		Reason:   netstate.ChangeInitial,
	})
	drift := toadctl.Snapshot{
		Generation: 10, Revision: 2, State: "degraded", RouteReady: false,
		InterfaceName: "kkprimary", IfIndex: 7, MTU: 1380,
		Addresses: []string{"10.0.0.1/24"},
		Capabilities: toadctl.Capabilities{
			Validate:                   true,
			RestartTransportKeepingTUN: true,
		},
	}
	if live, accepted := manager.observeToadSnapshotForProcess("primary", manager.roles["primary"].process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	// The structural route-target loss puts the role into recovering (the
	// supervision does this in production); DecideRole then selects the
	// transport restart that carries the managed sequence.
	if !manager.product.SetRecovery("primary", core.RecoveryState{}, core.RoleRecovering, "fixture route-target loss") {
		t.Fatal("the role must enter recovering")
	}

	current, ok := manager.product.Role("primary")
	if !ok {
		t.Fatal("the role must exist in the product state")
	}
	action := core.SelectTransportAction(current, manager.product.Snapshot().Underlay, current.Toad.Capabilities)
	t.Logf("fixture action=%s state=%s desired=%v toadGen=%d routeReady=%v caps=%+v",
		action, current.State, current.Desired, current.ToadGeneration, current.Toad.RouteReady, current.Toad.Capabilities)

	driver := &managedSequenceDriver{manager: manager}
	if err := manager.recoverRole(driver, "primary", 1, 1); err != nil {
		t.Fatalf("managed recovery must complete on the fixture: %v", err)
	}
	steps := driver.recordedSteps()
	if len(steps) == 0 || steps[0] != core.RecoveryFreezeSelected {
		t.Fatalf("managed recovery must start with freeze-selected: %v", steps)
	}
	if last := steps[len(steps)-1]; last != core.RecoveryUnfreezeSelected {
		t.Fatalf("managed recovery must end with unfreeze-selected: %v", steps)
	}
	for _, wanted := range []core.RecoveryStep{
		core.RecoveryPark, core.RecoveryWithdrawSelected, core.RecoveryWithdraw, core.RecoveryQuiesce,
		core.RecoveryRestoreSelected, core.RecoveryObserveRestore, core.RecoveryPublish,
	} {
		found := false
		for _, step := range steps {
			if step == wanted {
				found = true
			}
		}
		if !found {
			t.Fatalf("the managed sequence must contain %s: %v", wanted, steps)
		}
	}
	// The ordering the packet mandates: Park < WithdrawSelected < Withdraw.
	indexOf := func(step core.RecoveryStep) int {
		for i, s := range steps {
			if s == step {
				return i
			}
		}
		return -1
	}
	if p, w, wd := indexOf(core.RecoveryPark), indexOf(core.RecoveryWithdrawSelected), indexOf(core.RecoveryWithdraw); !(p < w && w < wd) {
		t.Fatalf("park < withdraw-selected < withdraw violated: %v", steps)
	}
}

// managedSequenceDriver records the step order and no-ops the transport
// steps so recoverRole completes against the fixtures.
type managedSequenceDriver struct {
	manager *Manager
	mu      sync.Mutex
	steps   []core.RecoveryStep
}

func (d *managedSequenceDriver) record(step core.RecoveryStep) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.steps = append(d.steps, step)
	return nil
}

// recordedSteps snapshots the step order for assertions.
func (d *managedSequenceDriver) recordedSteps() []core.RecoveryStep {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]core.RecoveryStep(nil), d.steps...)
}

func (d *managedSequenceDriver) ObserveRoutes(_ context.Context, _ string) error {
	return d.record(core.RecoveryObserveRoutes)
}
func (d *managedSequenceDriver) Park(ctx context.Context, role string) error {
	if err := d.record(core.RecoveryPark); err != nil {
		return err
	}
	return d.manager.selectedRoutes.ParkRegistered(ctx, role)
}
func (d *managedSequenceDriver) WithdrawSelected(_ context.Context, _ string) error {
	return d.record(core.RecoveryWithdrawSelected)
}
func (d *managedSequenceDriver) Withdraw(_ context.Context, _ string) error {
	return d.record(core.RecoveryWithdraw)
}
func (d *managedSequenceDriver) Quiesce(_ context.Context, _ string) error {
	return d.record(core.RecoveryQuiesce)
}
func (d *managedSequenceDriver) ApplyEndpoint(_ context.Context, _ string) error {
	return d.record(core.RecoveryApplyEndpoint)
}
func (d *managedSequenceDriver) Rebind(_ context.Context, _ string) error {
	return d.record(core.RecoveryRebind)
}
func (d *managedSequenceDriver) StartTransport(_ context.Context, _ string) error {
	return d.record(core.RecoveryStartTransport)
}
func (d *managedSequenceDriver) Validate(_ context.Context, _ string) error {
	return d.record(core.RecoveryValidate)
}
func (d *managedSequenceDriver) Publish(_ context.Context, _ string) error {
	return d.record(core.RecoveryPublish)
}
func (d *managedSequenceDriver) ResyncLeshy(_ context.Context, _ string) error {
	return d.record(core.RecoveryResyncLeshy)
}
func (d *managedSequenceDriver) ObserveRestoration(ctx context.Context, role string) error {
	if err := d.record(core.RecoveryObserveRestore); err != nil {
		return err
	}
	err := d.manager.selectedRoutes.ObserveRestoration(ctx, role)
	if errors.Is(err, errNoRestorationInProgress) {
		return nil
	}
	return err
}
func (d *managedSequenceDriver) FreezeSelected(ctx context.Context, role string) error {
	if err := d.record(core.RecoveryFreezeSelected); err != nil {
		return err
	}
	_, _, err := d.manager.selectedRoutes.Freeze(ctx, role)
	return err
}
func (d *managedSequenceDriver) RestoreSelected(ctx context.Context, role string) error {
	if err := d.record(core.RecoveryRestoreSelected); err != nil {
		return err
	}
	product, _ := d.manager.product.Role(role)
	return d.manager.selectedRoutes.Restore(ctx, role, product.Validation.Families.IPv4.Ready, product.Validation.Families.IPv6.Ready)
}
func (d *managedSequenceDriver) UnfreezeSelected(_ context.Context, role string) error {
	if err := d.record(core.RecoveryUnfreezeSelected); err != nil {
		return err
	}
	d.manager.selectedRoutes.Unfreeze(role)
	return nil
}
func (d *managedSequenceDriver) ReleaseResources(context.Context, string) error { return nil }

// errNoRestorationInProgress marks the fixture case where the registry has
// no parked records: the fixture driver treats it as success.
var errNoRestorationInProgress = errors.New("no park transactions to observe")

// --- K16 observation sequence (§31.9) -----------------------------------------

// An event that has reached the process must suppress the zero-I/O fast
// path until the reconciler processes it — even when the registry entry is
// Active with a token.
func TestObservationSequenceSuppressesStaleFastPath(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetRouteAuthorityReady()

	// Land the route and mint the verification token.
	intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if manager.registrySnapshotState("primary", "203.0.113.10/32") != routing.SelectedActive {
		t.Fatal("the record must be active before the sequence probe")
	}
	mutations := fake.reconcileCount()

	// Attach a fake observation source whose sequence is ahead of the
	// processed counter: an event reached the process but was not handled.
	observation := &fakeObservation{sequence: 7}
	manager.observation = observation
	manager.processedSequence.Store(0)

	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	// §31.9: the pending event must suppress the stale zero-I/O ACK. The
	// suppressed attempt re-drives the canonical transaction (BeginRepair →
	// Pending → Applying → platform reconcile) instead of answering from
	// the stale token. The platform here still holds the exact row, so the
	// honest verdict is already-applied — but it came from a fresh exact
	// reconcile, not the token: the reconcile count proves the re-drive.
	if fake.reconcileCount() != mutations+1 {
		t.Fatalf("the suppressed attempt must re-drive one exact reconcile: %d", fake.reconcileCount())
	}
	if hGot := manager.RegistrySnapshot("primary")[0].State; hGot != routing.SelectedActive {
		t.Fatalf("the re-drive must land Active: %q", hGot)
	}

	// Once the event is processed the fast path returns: the answer rides
	// the re-minted token with zero additional platform I/O.
	afterRedrive := fake.reconcileCount()
	manager.processedSequence.Store(7)
	replay, err := manager.Ensure(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if replay.State != "already-applied" {
		t.Fatalf("a current fast path must answer already-applied, got %q", replay.State)
	}
	if fake.reconcileCount() != afterRedrive {
		t.Fatal("a current fast path must not mutate")
	}
}

type fakeObservation struct{ sequence uint64 }

func (f *fakeObservation) CurrentSelectedRouteSequence() uint64 { return f.sequence }

// registrySnapshotState reads one record's state for the test.
func (m *SelectedRouteManager) registrySnapshotState(role, prefixText string) routing.SelectedRouteState {
	prefix := netip.MustParsePrefix(prefixText)
	for _, entry := range m.RegistrySnapshot(role) {
		if entry.Prefix == prefix {
			return entry.State
		}
	}
	return ""
}

// --- startup freeze + Ensure denial (Stage 1D/K13 cross-check) ---------------

func TestStartupFrozenRoleDeniesEnsureUntilGate(t *testing.T) {
	dir := markerDir(t, "/run/kk-sequence-test.sock")
	seededCheckpoint(t, dir)
	manager := managedManager(t, dir)
	defer manager.Close()
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()

	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.11/32", Source: LeshyRouteDNS,
	}); err == nil {
		t.Fatal("a startup-frozen role must deny ensures")
	}
	if fake.reconcileCount() != 0 {
		t.Fatal("a frozen role must not mutate")
	}
	// The freeze releases only through CompleteStartupRestore or the
	// recovery Unfreeze — never by the Ensure path itself.
	if manager.selectedRolesFrozen("primary") != true {
		t.Fatal("the role must stay frozen")
	}
	manager.selectedRoutes.Unfreeze("primary")
	if manager.selectedRolesFrozen("primary") {
		t.Fatal("Unfreeze must clear the freeze")
	}
}

// selectedRolesFrozen reads the freeze flag for the test.
func (m *Manager) selectedRolesFrozen(role string) bool {
	m.selectedRoutes.mu.Lock()
	defer m.selectedRoutes.mu.Unlock()
	return m.selectedRoutes.roleState(role).frozen
}

var _ = time.Second // keeps the time import if the file evolves
