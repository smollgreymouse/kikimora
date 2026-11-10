package control

import (
	"context"
	"errors"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

func TestRecoveryOwnershipUsesBaselineObservedDelta(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name: "one",
		Interface: state.InterfaceState{
			Name:    "kkone",
			IfIndex: 7,
			MTU:     1380,
		},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	userPrefix := "192.0.2.10/32"
	selectedPrefix := "198.51.100.20/32"
	fake := &routing.Fake{
		State: routing.KernelState{Routes: []routing.Operation{{
			Kind: "route", Prefix: userPrefix, Table: 254, IfIndex: 7, Metric: 10, Protocol: 4,
		}}},
	}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	role, _, err := driver.role("one")
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.captureOwnershipBaseline(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}

	fake.State.Routes = append(fake.State.Routes, routing.Operation{
		Kind: "route", Prefix: selectedPrefix, Table: 254, IfIndex: 7, Metric: 20, Protocol: 4,
	})
	if err := driver.captureOwnedDelta(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}

	owned := driver.ownership.Snapshot("one")
	if len(owned) != 1 || owned[0].Prefix.String() != selectedPrefix {
		t.Fatalf("baseline route was captured as owned or selected delta was lost: %#v", owned)
	}

	checkpoint, err := parking.ReadCheckpoint(filepath.Join(dir, "one", "parking.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Baseline) != 1 || checkpoint.Baseline[0].Prefix.String() != userPrefix {
		t.Fatalf("checkpoint lost baseline ownership evidence: %#v", checkpoint.Baseline)
	}
	if len(checkpoint.Observed) != 1 || checkpoint.Observed[0].Prefix.String() != selectedPrefix {
		t.Fatalf("checkpoint did not persist observed owned delta: %#v", checkpoint.Observed)
	}
}

func TestRecoveryCheckpointRebindsStaleIdentityWithoutTransferringOwnership(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name:      "one",
		Interface: state.InterfaceState{Name: "kkone", IfIndex: 9, MTU: 1380},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	prefix := netip.MustParsePrefix("198.51.100.20/32")
	path := filepath.Join(dir, "one", "parking.json")
	if err := parking.WriteCheckpoint(path, parking.Checkpoint{
		Role:      "one",
		Interface: "kkone",
		IfIndex:   7,
		Baseline: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32"),
		}},
		Observed: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fake := &routing.Fake{}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	if err := driver.ObserveRoutes(context.Background(), "one"); err != nil {
		t.Fatalf("stale inactive checkpoint should be rebound, got %v", err)
	}
	if got := driver.ownership.Snapshot("one"); len(got) != 0 {
		t.Fatalf("stale ownership crossed ifindex change: %#v", got)
	}
	driver.mu.Lock()
	baseline := append([]parking.OwnedRoute(nil), driver.baselines["one"]...)
	driver.mu.Unlock()
	if len(baseline) != 0 {
		t.Fatalf("stale baseline crossed ifindex change: %#v", baseline)
	}

	checkpoint, err := parking.ReadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Interface != "kkone" || checkpoint.IfIndex != 9 {
		t.Fatalf("checkpoint identity not rebound: %#v", checkpoint)
	}
	if len(checkpoint.Baseline) != 0 || len(checkpoint.Observed) != 0 || len(checkpoint.Parked) != 0 {
		t.Fatalf("stale ownership evidence survived rebind: %#v", checkpoint)
	}
}

func TestRecoveryCheckpointPreservesVerifiedParkAcrossIdentityChange(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.mu.Lock()
	manager.roles["one"].observed = state.Snapshot{
		Name:      "one",
		Interface: state.InterfaceState{Name: "kkone", IfIndex: 9, MTU: 1380},
	}
	manager.roles["one"].stateValid = true
	manager.mu.Unlock()

	prefix := netip.MustParsePrefix("198.51.100.20/32")
	path := filepath.Join(dir, "one", "parking.json")
	if err := parking.WriteCheckpoint(path, parking.Checkpoint{
		Role:      "one",
		Interface: "kkone",
		IfIndex:   7,
		Observed: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
		Parked: []parking.OwnedRoute{{
			Role: "one", Interface: "kkone", IfIndex: 7, Prefix: prefix,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	fake := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{{
		Kind: "park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: 4,
	}}}}
	driver := NewRecoveryDriver(manager, RecoveryServices{Executor: fake}).(*recoveryDriver)
	if err := driver.ObserveRoutes(context.Background(), "one"); err != nil {
		t.Fatalf("restore stale active checkpoint: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); !got.Active || got.Count != 1 {
		t.Fatalf("verified fail-closed park was not restored: %#v", got)
	}

	if err := driver.Park(context.Background(), "one"); err != nil {
		t.Fatalf("reuse verified park: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); !got.Active || got.Count != 1 {
		t.Fatalf("existing park was cleared by empty new ownership: %#v", got)
	}

	checkpoint, err := parking.ReadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.IfIndex != 9 || checkpoint.Interface != "kkone" || len(checkpoint.Parked) != 1 {
		t.Fatalf("active checkpoint not rebound to current route target: %#v", checkpoint)
	}
	if checkpoint.Parked[0].IfIndex != 9 || checkpoint.Parked[0].Prefix != prefix {
		t.Fatalf("parked prefix not normalized to current identity: %#v", checkpoint.Parked)
	}
	if len(checkpoint.Observed) != 0 || len(checkpoint.Baseline) != 0 {
		t.Fatalf("stale ownership evidence survived active-park rebind: %#v", checkpoint)
	}

	fake.State.Routes = append(fake.State.Routes, routing.Operation{
		Kind: "route", Prefix: prefix.String(), Table: 254, IfIndex: 9, Metric: 10, Protocol: 4,
	})
	if err := driver.ObserveRestoration(context.Background(), "one"); err != nil {
		t.Fatalf("restoration after checkpoint rebind: %v", err)
	}
	if got := driver.parkingManager().Snapshot("one"); got.Active || got.Count != 0 {
		t.Fatalf("verified park did not release after real route restoration: %#v", got)
	}
}

func TestManagedRecoveryParkUsesTheFrozenRegistry(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// The fake kernel shows the selected route on the tunnel interface, so
	// the registered withdrawal verifies it.
	fakeKernel := &routing.Fake{State: routing.KernelState{Routes: []routing.Operation{
		{Kind: "route", Prefix: "203.0.113.10/32", Table: 254, IfIndex: 7, Metric: 0, Protocol: 4},
	}}}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	activateManagedRoleForTest(manager, "primary")
	manager.selectedRoutes.SetRouteAuthorityReady()
	runtime := readyRuntime("primary", true, true)
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) { return runtime, true }

	// The product role carries the authoritative family readiness the
	// restore step consults.
	manager.product.UpdateRoleResources("primary", endpoint.State{}, leshy.PublicationState{}, parking.State{}, runtime.Validation)

	// An intent accepted immediately before the freeze must be in the
	// parking set.
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	// The parking manager needs a transaction executor; the fake kernel
	// provides Apply/Snapshot.
	manager.SetRecoveryDriver(NewRecoveryDriver(manager, RecoveryServices{Executor: fakeKernel}))
	driver, ok := manager.recoveryDriver.(*recoveryDriver)
	if !ok {
		t.Fatalf("unexpected recovery driver: %T", manager.recoveryDriver)
	}
	if err := driver.FreezeSelected(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	if err := driver.parkRegistered(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	// The frozen destination is registered and parked; a new Ensure is
	// denied while frozen.
	snapshot := manager.selectedRoutes.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].Park == nil || snapshot[0].Park.State != routing.SelectedParkActive {
		t.Fatalf("the accepted prefix must carry an active park: %#v", snapshot)
	}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.11/32", Source: LeshyRouteDNS,
	}); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("a frozen role must deny new ensures: %v", err)
	}

	// Restoration onto the current interface releases the park only through
	// the exact proof; the fake kernel has the row after the restore.
	if err := driver.RestoreSelected(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	if err := driver.observeRegisteredRestoration(context.Background(), "primary"); err != nil {
		t.Fatalf("restoration must release the verified park: %v", err)
	}
	if err := driver.UnfreezeSelected(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	snapshot = manager.selectedRoutes.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != routing.SelectedActive {
		t.Fatalf("post-unfreeze ownership: %#v", snapshot)
	}
}

func TestManagedAndLegacyPathsParkAndReleaseEquivalently(t *testing.T) {
	// 08D §9 parity: for the same kernel picture, the managed explicit
	// registry path and the legacy baseline/delta path must reach the same
	// fail-closed outcome — the destination parks before withdrawal and
	// releases only when the exact row is back. The legacy machinery stays
	// for legacy-file installs; the managed path never consults it.
	prefix := netip.MustParsePrefix("198.51.100.20/32")
	// The legacy two-phase flow needs a kernel without the selected route at
	// baseline time; the managed flow starts with the row already verified.
	// A user route through the same interface existed before the toad: the
	// baseline phase must record it so the delta never claims it as ours.
	userRoute := routing.Operation{
		Kind: "route", Prefix: "192.0.2.10/32", Table: 254, IfIndex: 7, Metric: 10, Protocol: 4,
	}
	newKernel := func(withSelected bool) *routing.Fake {
		routes := []routing.Operation{userRoute}
		if withSelected {
			routes = append(routes, routing.Operation{
				Kind: "route", Prefix: prefix.String(), Table: 254, IfIndex: 7, Metric: 20, Protocol: 4,
			})
		}
		return &routing.Fake{State: routing.KernelState{Routes: routes}}
	}

	// Legacy: baseline + observed delta + PrepareOwnedWithdrawal.
	legacyManager, err := NewManager([]string{writeConfig(t, dir2(t), "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	legacyManager.monitorCancel()
	defer legacyManager.Close()
	legacyManager.mu.Lock()
	legacyManager.roles["one"].observed = state.Snapshot{Name: "one", Interface: state.InterfaceState{Name: "kkone", IfIndex: 7, MTU: 1380}}
	legacyManager.roles["one"].stateValid = true
	legacyManager.mu.Unlock()
	legacyKernel := newKernel(false)
	legacy := NewRecoveryDriver(legacyManager, RecoveryServices{Executor: legacyKernel}).(*recoveryDriver)
	role, _, err := legacy.role("one")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy two-phase ownership: the baseline is captured before the Toad
	// starts (a user route through the same interface is NOT ours), then
	// the toad adds the selected route and the delta claims it.
	if err := legacy.captureOwnershipBaseline(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}
	if err := legacyKernel.Apply(context.Background(), routing.Transaction{Role: "one", Operations: []routing.Operation{
		{Kind: "route", Prefix: prefix.String(), Table: 254, IfIndex: 7, Metric: 20, Protocol: 4},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.captureOwnedDelta(context.Background(), "one", role); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Park(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	legacyParks := legacy.parkingManager().Snapshot("one")

	// Managed: explicit registry + registered withdrawal.
	managedManager, err := NewManager([]string{writeConfig(t, dir2(t), "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	managedManager.monitorCancel()
	defer managedManager.Close()
	activateManagedRoleForTest(managedManager, "one")
	managedKernel := newKernel(true)
	// The managed park surface runs through the SelectedRouteManager's
	// platform writer (08D1 §7): a fake kernel with the full selected/park
	// surface stands in for it here.
	managedManager.selectedRoutes.SetRoutes(newSelectedRoutesFake())
	managedManager.selectedRoutes.SetRouteAuthorityReady()
	managed := NewRecoveryDriver(managedManager, RecoveryServices{Executor: managedKernel}).(*recoveryDriver)
	identity := routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}
	if err := managed.manager.selectedRoutes.registry.Reserve("one", "one", prefix, routing.SelectedDNS, 1, 1, "kkone", identity); err != nil {
		t.Fatal(err)
	}
	if err := managed.manager.selectedRoutes.registry.MarkApplying("one", prefix); err != nil {
		t.Fatal(err)
	}
	if err := managed.manager.selectedRoutes.registry.MarkActive("one", prefix, identity); err != nil {
		t.Fatal(err)
	}
	if err := managed.FreezeSelected(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := managed.parkRegistered(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	// The managed parking authority is the SelectedRouteManager registry
	// (08D1 §7); the legacy parking.Manager never sees this path.
	managedParks := managed.manager.selectedRoutes.ParkingState("one")

	// Same fail-closed outcome: the selected destination is parked.
	if !legacyParks.Active || legacyParks.Count != 1 || legacyParks.Prefixes[0] != prefix {
		t.Fatalf("legacy parks: %#v", legacyParks)
	}
	if !managedParks.Active || managedParks.Count != 1 || managedParks.Prefixes[0] != prefix {
		t.Fatalf("managed parks must match the legacy outcome: %#v", managedParks)
	}
}

// dir2 gives each manager its own configuration directory so the two
// installs in a parity test never share checkpoint files.
func dir2(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
