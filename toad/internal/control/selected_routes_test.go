package control

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// selectedRoutesFake is the fake platform writer: it records reconciles and
// simulates a kernel that always lands the desired row (exact read-back).
type selectedRoutesFake struct {
	mu          sync.Mutex
	reconciles  []routing.SelectedRouteDesired
	removes     []routing.SelectedRoute
	applies     []routing.Transaction
	rows        map[string][]routing.SelectedKernelRow
	applyErr    error
	failReserve bool
}

func newSelectedRoutesFake() *selectedRoutesFake {
	return &selectedRoutesFake{rows: make(map[string][]routing.SelectedKernelRow)}
}

func keyFor(prefix netip.Prefix) string { return prefix.String() }

func (f *selectedRoutesFake) ReconcileSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired, record *routing.SelectedRoute) (routing.SelectedReconcileResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return routing.SelectedReconcileResult{}, f.applyErr
	}
	f.reconciles = append(f.reconciles, desired)
	// Simulate the exact kernel landing unless a pre-seeded row is foreign
	// under the shared planner's exact rules.
	key := keyFor(desired.Prefix)
	if _, err := routing.PlanSelectedRoute(f.rows[key], record); err != nil {
		return routing.SelectedReconcileResult{}, err
	}
	identity, _ := f.NormalizeSelectedRoute(context.Background(), desired)
	mutated := true
	for _, row := range f.rows[key] {
		if row.Kind == routing.SelectedKernelRoute && row.Route == identity {
			mutated = false
		}
	}
	f.rows[key] = []routing.SelectedKernelRow{{
		Kind:  routing.SelectedKernelRoute,
		Route: identity,
	}}
	return routing.SelectedReconcileResult{Mutated: mutated, Verified: identity}, nil
}

// NormalizeSelectedRoute mirrors the Linux normalization: the identity the
// kernel will read back for the desired route.
func (f *selectedRoutesFake) NormalizeSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error) {
	identity := routing.SelectedRouteIdentity{
		Table:    routing.ManagedSelectedRouteTable,
		IfIndex:  desired.IfIndex,
		Protocol: 4,
		Scope:    routing.ScopeLink,
	}
	if !desired.Prefix.Addr().Is4() {
		identity.Metric = 1024
	}
	if desired.Gateway.IsValid() {
		identity.NextHop = desired.Gateway.String()
		identity.Scope = routing.ScopeUniverse
	}
	return identity, nil
}

func (f *selectedRoutesFake) RemoveSelectedRoute(_ context.Context, owned routing.SelectedRoute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes = append(f.removes, owned)
	delete(f.rows, keyFor(owned.Prefix))
	return nil
}

func (f *selectedRoutesFake) reconcileCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reconciles)
}

// Apply records park/delete transactions (the routing.Executor surface the
// provisional managed park path uses).
func (f *selectedRoutesFake) Apply(_ context.Context, transaction routing.Transaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies = append(f.applies, transaction)
	return nil
}

func (f *selectedRoutesFake) applyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applies)
}

// PreflightSelectedRoute applies the shared planner rules to the fake
// rows: any unauthorized same-prefix row is a conflict.
func (f *selectedRoutesFake) PreflightSelectedRoute(_ context.Context, prefix netip.Prefix, _ routing.SelectedRouteIdentity, record *routing.SelectedRoute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := routing.PlanSelectedRoute(f.rows[keyFor(prefix)], record)
	return err
}

// The managed-park surface: the fake simulates a kernel that always lands
// the exact desired park row.
func (f *selectedRoutesFake) NormalizeManagedPark(_ context.Context, prefix netip.Prefix) (routing.SelectedParkIdentity, error) {
	identity := routing.SelectedParkIdentity{
		Kind:     routing.SelectedParkUnreachable,
		Table:    routing.ManagedSelectedRouteTable,
		Protocol: 4,
		Metric:   routing.ManagedParkMetric,
	}
	return identity, identity.Validate(prefix)
}

func (f *selectedRoutesFake) PreflightManagedPark(_ context.Context, prefix netip.Prefix, _ routing.SelectedParkIdentity, record *routing.SelectedRoute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows[keyFor(prefix)] {
		if row.Kind != routing.SelectedKernelPark {
			continue
		}
		if !parkRowFakeAuthorized(row.Park, record != nil && record.Park != nil, record) {
			return fmt.Errorf("park preflight %s: unauthorized park row %v", prefix, row.Park)
		}
	}
	return nil
}

func parkRowFakeAuthorized(row routing.SelectedParkIdentity, hasPark bool, record *routing.SelectedRoute) bool {
	if !hasPark || record == nil || record.Park == nil {
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
		return (park.Owned != nil && row.Equal(*park.Owned)) ||
			(park.RetireDesired && row.Equal(park.Desired))
	default:
		return false
	}
}

func (f *selectedRoutesFake) ReconcileManagedPark(_ context.Context, _ string, prefix netip.Prefix, record *routing.SelectedRoute) (routing.SelectedParkReconcileResult, error) {
	if record == nil || record.Park == nil || record.Park.State != routing.SelectedParkApplying {
		return routing.SelectedParkReconcileResult{}, fmt.Errorf("park reconcile requires an applying transaction")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[keyFor(prefix)] = append(f.rows[keyFor(prefix)], routing.SelectedKernelRow{
		Kind: routing.SelectedKernelPark,
		Park: record.Park.Desired,
	})
	return routing.SelectedParkReconcileResult{Mutated: true, Verified: record.Park.Desired}, nil
}

func (f *selectedRoutesFake) ObserveManagedPark(_ context.Context, prefix netip.Prefix, expected routing.SelectedParkIdentity) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows[keyFor(prefix)] {
		if row.Kind == routing.SelectedKernelPark && row.Park.Equal(expected) {
			return true, nil
		}
	}
	return false, nil
}

func (f *selectedRoutesFake) RemoveManagedPark(_ context.Context, _ string, prefix netip.Prefix, park routing.SelectedPark) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.rows[keyFor(prefix)]
	kept := rows[:0]
	for _, row := range rows {
		if row.Kind != routing.SelectedKernelPark {
			kept = append(kept, row)
		}
	}
	f.rows[keyFor(prefix)] = kept
	return nil
}

func (f *selectedRoutesFake) SnapshotSelectedRoutes(context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[netip.Prefix][]routing.SelectedKernelRow, len(f.rows))
	for key, rows := range f.rows {
		prefix, err := netip.ParsePrefix(key)
		if err != nil {
			continue
		}
		out[prefix] = append(out[prefix], rows...)
	}
	return out, nil
}

func (f *selectedRoutesFake) SubscribeSelectedRouteInvalidations(context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	ch := make(chan routing.SelectedRouteInvalidation)
	close(ch)
	return ch, nil
}

// The remaining platform.RouteManager methods are unused by this surface.
func (f *selectedRoutesFake) ReconcileEndpointPolicy(context.Context, endpoint.Policy) error {
	return nil
}
func (f *selectedRoutesFake) RemoveEndpointPolicy(context.Context, endpoint.Policy) error { return nil }
func (f *selectedRoutesFake) ApplyParking(context.Context, parking.DesiredState) error    { return nil }
func (f *selectedRoutesFake) Snapshot(context.Context) (routing.KernelState, error) {
	return routing.KernelState{}, nil
}

// readyRuntime is the authoritative product role the manager snapshots.
func readyRuntime(zone string, v4Ready, v6Ready bool) core.RoleRuntime {
	runtime := core.RoleRuntime{Desired: true, ToadGeneration: 1}
	runtime.Validation = toadctl.ValidationResult{
		Healthy: true,
		State:   "ready",
		Families: toadctl.RouteFamilies{
			IPv4: toadctl.FamilyReadiness{Configured: true, Ready: v4Ready},
			IPv6: toadctl.FamilyReadiness{Configured: v6Ready, Ready: v6Ready},
		},
	}
	runtime.Toad = toadctl.Snapshot{InterfaceName: "kk-awg0", IfIndex: 7}
	_ = zone
	return runtime
}

func newTestSelectedManager(t *testing.T, runtime core.RoleRuntime, fake *selectedRoutesFake) *SelectedRouteManager {
	t.Helper()
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		fake,
		func(string) (core.RoleRuntime, bool) { return runtime, true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return t.TempDir(), true },
	)
	return manager
}

func TestEnsureAppliesAReadyV4HostIntent(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)

	result, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "applied" || result.Role != "primary" || result.Family != 4 || result.Generation != 1 {
		t.Fatalf("unexpected verdict: %#v", result)
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("one reconcile expected, got %d", fake.reconcileCount())
	}
	if fake.reconciles[0].IfIndex != 7 {
		t.Fatalf("desired must derive from the toad snapshot: %#v", fake.reconciles[0])
	}
	snapshot := manager.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != routing.SelectedActive {
		t.Fatalf("the destination must be active after the ack: %#v", snapshot)
	}
}

func TestDuplicateEnsureCausesOneMutation(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)
	intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
	for i := 0; i < 10; i++ {
		if _, err := manager.Ensure(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("duplicate ensures must coalesce to one mutation, got %d", fake.reconcileCount())
	}
}

func TestConcurrentDuplicateEnsureCoalesces(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)
	intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := manager.Ensure(context.Background(), intent); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fake.reconcileCount() != 1 {
		t.Fatalf("concurrent duplicate ensures must coalesce, got %d", fake.reconcileCount())
	}
}

func TestNonReadyFamilyIsDenied(t *testing.T) {
	fake := newSelectedRoutesFake()
	// IPv4 ready, IPv6 not: the AAAA intent is denied, the A intent passes.
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)

	result, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "2001:db8::10/128", Source: LeshyRouteDNS,
	})
	var denied *SelectedRouteDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("a non-ready family must be denied: %v", err)
	}
	if result.State != "denied" {
		t.Fatalf("the verdict must be denied: %#v", result)
	}
	if fake.reconcileCount() != 0 {
		t.Fatal("a denied intent must not touch the kernel")
	}

	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatalf("the ready family must pass: %v", err)
	}
}

func TestEnsureCannotSelectAnArbitraryInterface(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	// The wire type has no interface fields; the derived target comes from
	// the controller's own snapshot, so a crafted request cannot name one.
	result, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "10.0.0.99/32", Source: LeshyRouteDNS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.reconciles[0].IfIndex != 7 || fake.reconciles[0].Gateway.IsValid() {
		t.Fatalf("the target must come from the controller snapshot: %#v", fake.reconciles[0])
	}
	_ = result
}

func TestUnhealthyValidationDeniesEveryFamily(t *testing.T) {
	fake := newSelectedRoutesFake()
	runtime := readyRuntime("primary", true, true)
	runtime.Validation.Healthy = false
	runtime.Validation.Reason = "backend validation failed"
	manager := newTestSelectedManager(t, runtime, fake)

	for _, prefix := range []string{"203.0.113.10/32", "2001:db8::10/128"} {
		_, err := manager.Ensure(context.Background(), LeshyRouteIntent{
			Zone: "primary", Prefix: prefix, Source: LeshyRouteDNS,
		})
		if !errors.As(err, new(*SelectedRouteDeniedError)) {
			t.Fatalf("%s must be denied on an unhealthy validation: %v", prefix, err)
		}
	}
	if fake.reconcileCount() != 0 {
		t.Fatal("denied intents must not mutate the kernel")
	}
}

func TestForeignRowConflicts(t *testing.T) {
	fake := newSelectedRoutesFake()
	fake.mu.Lock()
	fake.rows["203.0.113.10/32"] = []routing.SelectedKernelRow{{
		Kind:        routing.SelectedKernelRoute,
		Route:       routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 2, Protocol: 2},
		Description: "foreign row with a different identity",
	}}
	fake.mu.Unlock()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)

	result, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	var conflict *SelectedRouteConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("a foreign row must conflict: %v", err)
	}
	if result.State != "conflict" {
		t.Fatalf("the verdict must be conflict: %#v", result)
	}
}

func TestReserveHappensBeforeTheKernelApply(t *testing.T) {
	// Failure injection: the kernel apply fails after the Reserve. The
	// registry must keep the Pending ownership so recovery can park the
	// destination, and the checkpoint must exist.
	fake := newSelectedRoutesFake()
	fake.applyErr = errors.New("netlink unavailable")
	dir := t.TempDir()
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		fake,
		func(string) (core.RoleRuntime, bool) { return readyRuntime("primary", true, false), true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return dir, true },
	)

	_, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if err == nil {
		t.Fatal("the failed apply must surface an error")
	}
	snapshot := manager.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != routing.SelectedApplying {
		t.Fatalf("an authorized interrupted apply keeps the Applying evidence: %#v", snapshot)
	}
	if _, err := routing.ReadSelectedRoutesCheckpoint(dir + "/selected-routes.json"); err != nil {
		t.Fatalf("the reserved ownership must be checkpointed: %v", err)
	}
}

func TestCapacityDeniesTheLastUniqueIntent(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.capacity = 2
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.1/32", Source: LeshyRouteDNS}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.2/32", Source: LeshyRouteDNS}); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.3/32", Source: LeshyRouteDNS})
	if !errors.As(err, new(*SelectedRouteDeniedError)) || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("over-capacity must be a safety denial: %v", err)
	}
	// Existing intents keep reconciling: a refresh is never denied.
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.1/32", Source: LeshyRouteDNS}); err != nil {
		t.Fatalf("refreshing an existing intent must be allowed: %v", err)
	}
	// Static intents do not consume the dynamic quota.
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "10.0.0.0/24", Source: LeshyRouteStatic}); err != nil {
		t.Fatalf("static intents bypass the dynamic quota: %v", err)
	}
}

func TestFreezeBlocksNewEnsuresAndUnfreezeBumpsGeneration(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)

	owned, generation, err := manager.Freeze(context.Background(), "primary")
	if err != nil || generation != 1 || len(owned) != 0 {
		t.Fatalf("freeze on a fresh role seeds generation 1: %v %d %#v", err, generation, owned)
	}
	_, err = manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if !errors.As(err, new(*SelectedRouteDeniedError)) || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("a frozen role must deny new intents: %v", err)
	}
	newGeneration := manager.Unfreeze("primary")
	if newGeneration != 2 {
		t.Fatalf("unfreeze must bump the seeded route generation: %d", newGeneration)
	}
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.RegistrySnapshot("primary")
	if snapshot[0].RouteGeneration != 2 {
		t.Fatalf("the intent must carry the new generation: %#v", snapshot[0])
	}
}

func TestRestoreOnlyReappliesReadyFamilies(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, false), fake)
	for _, prefix := range []string{"203.0.113.10/32", "2001:db8::10/128"} {
		desired := routing.SelectedRouteDesired{Role: "primary", Prefix: netip.MustParsePrefix(prefix), IfIndex: 7}
		identity, err := fake.NormalizeSelectedRoute(context.Background(), desired)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.registry.Reserve("primary", "primary", netip.MustParsePrefix(prefix), routing.SelectedDNS, 1, 1, "kk-awg0", identity); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Restore(context.Background(), "primary", true, false); err != nil {
		t.Fatal(err)
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("only the ready family is restored: %d", fake.reconcileCount())
	}
	if fake.reconciles[0].Prefix.String() != "203.0.113.10/32" {
		t.Fatalf("the wrong family was restored: %#v", fake.reconciles[0])
	}
}

func TestForgetRemovesOnlyTheNamedIntent(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	for _, prefix := range []string{"203.0.113.10/32", "203.0.113.11/32"} {
		if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: prefix, Source: LeshyRouteDNS}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Forget(context.Background(), LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}); err != nil {
		t.Fatal(err)
	}
	if len(fake.removes) != 1 || fake.removes[0].Prefix.String() != "203.0.113.10/32" {
		t.Fatalf("forget must remove exactly the named row: %#v", fake.removes)
	}
	if got := len(manager.RegistrySnapshot("primary")); got != 1 {
		t.Fatalf("one entry must remain: %d", got)
	}
}

func TestUnknownZoneIsAHardError(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	_, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "unknown", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("an unknown zone must be a hard denial: %v", err)
	}
	if fake.reconcileCount() != 0 {
		t.Fatal("an unknown zone must not mutate anything")
	}
}

func TestAPIEnsureEndToEndVerdictMapping(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if manager.selectedRoutes == nil {
		t.Fatal("the manager must wire the selected-route surface")
	}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	runtime := readyRuntime("primary", true, true)
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return runtime, true
	}

	response := manager.Handle(context.Background(), Request{
		Version: 3, ID: "t", Method: "EnsureLeshyRoute", Role: "primary",
		LeshyRoute: &LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS},
	})
	if !response.OK || response.LeshyRoute == nil || response.LeshyRoute.State != "applied" {
		t.Fatalf("e2e ensure must be applied: %#v (%v)", response, response.Error)
	}
	if fake.reconcileCount() != 1 {
		t.Fatalf("the e2e path must mutate once: %d", fake.reconcileCount())
	}
}

func TestAPIDeniedVerdictIsAnOKResponse(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return core.RoleRuntime{Desired: false}, true
	}

	response := manager.Handle(context.Background(), Request{
		Version: 3, ID: "t", Method: "EnsureLeshyRoute", Role: "primary",
		LeshyRoute: &LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS},
	})
	if !response.OK || response.LeshyRoute == nil {
		t.Fatalf("a denial must be an OK response with a verdict: %#v", response)
	}
	if response.LeshyRoute.State == "" {
		t.Fatalf("the verdict state must be set: %#v", response.LeshyRoute)
	}
}

func TestConfiguredZoneMustMapToExactlyOneRole(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// The single-role install maps the primary zone to exactly one role; a
	// second role on the same zone would make Ensure fail hard.
	if got := manager.selectedRoutes.zoneRoles["primary"]; len(got) != 1 {
		t.Fatalf("the primary zone must map to exactly one role: %#v", got)
	}
}

func TestManagedModeGateRequiresSocketAndVerifiedLeshy(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableManagedSelectedRoutes("", true); err == nil {
		t.Fatal("managed mode without a socket must be refused")
	}
	if err := manager.EnableManagedSelectedRoutes("/run/kikimora/core.sock", false); err == nil {
		t.Fatal("managed mode without a verified Leshy must be refused")
	}
	// Nothing flipped: the install stays in legacy-file mode.
	if manager.managedSocketForGeneration() != "" {
		t.Fatal("a refused activation must not leave managed state behind")
	}
	if manager.managedSelected("primary") {
		t.Fatal("no role may run the managed sequence after a refused activation")
	}
}

func TestManagedModeActivationGeneratesTheManagedConfig(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "leshy", "config.toml")
	manager.leshyConfigPath = configPath
	if err := manager.EnableManagedSelectedRoutes("/run/kikimora/core.sock", true); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(generated)
	if !strings.Contains(content, "routing_backend = \"kikimora\"") ||
		!strings.Contains(content, "kikimora_socket = \"/run/kikimora/core.sock\"") {
		t.Fatalf("the generated config must select the managed backend:\n%s", content)
	}
	if !manager.managedSelected("primary") {
		t.Fatal("the role must run the managed recovery sequence")
	}
	// The marker persists: a restarted core re-arms the mode.
	restarted, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	restarted.configDir = dir
	restarted.leshyConfigPath = configPath
	if !restarted.managedSelected("primary") || restarted.managedSocketForGeneration() != "/run/kikimora/core.sock" {
		t.Fatalf("the marker must re-arm the managed mode at boot: %#v", restarted.managedSelectedRoles)
	}
	// Regeneration after the restart keeps the managed backend.
	if err := restarted.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	regenerated, _ := os.ReadFile(configPath)
	if !strings.Contains(string(regenerated), "routing_backend = \"kikimora\"") {
		t.Fatalf("the regenerated config lost the managed backend:\n%s", regenerated)
	}
}

func TestManagedModeDisableIsOneExplicitTransaction(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "leshy", "config.toml")
	manager.leshyConfigPath = configPath
	if err := manager.EnableManagedSelectedRoutes("/run/kikimora/core.sock", true); err != nil {
		t.Fatal(err)
	}
	if err := manager.DisableManagedSelectedRoutes(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "routing_backend") {
		t.Fatalf("the rollback must remove the managed backend keys:\n%s", content)
	}
	if manager.managedSelected("primary") || manager.managedSocketForGeneration() != "" {
		t.Fatal("the rollback must clear the managed state")
	}
	if _, err := os.Stat(manager.managedMarkerPath()); !os.IsNotExist(err) {
		t.Fatal("the marker must be gone after the rollback")
	}
}

func TestGeneratedManagedConfigRequiresTheSocket(t *testing.T) {
	dir := t.TempDir()
	server := config.LeshyServer{RoutingBackend: "kikimora", KikimoraSocket: ""}
	if err := config.WriteLeshyConfig(filepath.Join(dir, "config.toml"), server, nil); err == nil {
		t.Fatal("a managed config without a socket must not be written")
	}
	valid := config.LeshyServer{RoutingBackend: "kikimora", KikimoraSocket: "/run/kikimora/core.sock"}
	path := filepath.Join(dir, "config.toml")
	if err := config.WriteLeshyConfig(path, valid, nil); err != nil {
		t.Fatal(err)
	}
	readBack := config.ReadLeshyServer(path)
	if readBack.RoutingBackend != "kikimora" || readBack.KikimoraSocket != "/run/kikimora/core.sock" {
		t.Fatalf("managed fields round-trip: %#v", readBack)
	}
}

func TestDiagnosticsCounters(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := manager.Ensure(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
	}
	// A new unique intent over the (tightened) capacity is a denial.
	manager.capacity = 1
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.99/32", Source: LeshyRouteDNS,
	}); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatal("setup: a denial was expected")
	}
	stats := manager.Diagnostics("primary")
	if stats.Ensures != 7 || stats.KernelMutations != 1 || stats.DuplicateHits != 5 || stats.Denials != 1 {
		t.Fatalf("counters mismatch: %#v", stats)
	}
	if stats.Registered != 1 || stats.Active != 1 {
		t.Fatalf("registry counts mismatch: %#v", stats)
	}
}

func TestSoakTenThousandDuplicateEnsuresStayFlat(t *testing.T) {
	// Deterministic fake-kernel soak (08D K10): 10k DNS classifications over
	// 100 unique destinations. After convergence the kernel mutation count
	// must stay flat, duplicates must be accepted through the fast path,
	// and the registry must stay bounded by the capacity.
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)

	const unique = 100
	for i := 0; i < unique; i++ {
		intent := LeshyRouteIntent{
			Zone:   "primary",
			Prefix: fmt.Sprintf("203.0.%d.%d/32", i/256, i%256),
			Source: LeshyRouteDNS,
		}
		if _, err := manager.Ensure(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
	}
	if fake.reconcileCount() != unique {
		t.Fatalf("convergence: %d mutations for %d unique intents", fake.reconcileCount(), unique)
	}

	for i := 0; i < 10_000; i++ {
		intent := LeshyRouteIntent{
			Zone:   "primary",
			Prefix: fmt.Sprintf("203.0.%d.%d/32", (i%unique)/256, (i%unique)%256),
			Source: LeshyRouteDNS,
		}
		if _, err := manager.Ensure(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
	}
	if fake.reconcileCount() != unique {
		t.Fatalf("replayed classifications mutated the kernel: %d", fake.reconcileCount())
	}
	stats := manager.Diagnostics("primary")
	if stats.KernelMutations != unique {
		t.Fatalf("kernel mutations drifted: %#v", stats)
	}
	if stats.DuplicateHits != 10_000 {
		t.Fatalf("duplicate hits: %#v", stats)
	}
	if stats.Registered != unique {
		t.Fatalf("the registry must stay bounded by the workload: %#v", stats)
	}
	// Family snapshots stay split for diagnostics.
	if len(manager.registry.SnapshotFamily("primary", 4)) != unique {
		t.Fatal("family snapshot mismatch")
	}
}

func TestRoleSnapshotCarriesSelectedRouteDiagnostics(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	var role *RoleSnapshot
	for i := range snapshot.Roles {
		if snapshot.Roles[i].ID == "primary" {
			role = &snapshot.Roles[i]
		}
	}
	if role == nil || role.SelectedRoutes == nil {
		t.Fatalf("role diagnostics missing: %#v", snapshot.Roles)
	}
	if role.SelectedRoutes.Active != 1 || role.SelectedRoutes.KernelMutations != 1 {
		t.Fatalf("snapshot diagnostics mismatch: %#v", role.SelectedRoutes)
	}
}

func TestRoleIPv6DefaultOnAndPerRoleToggle(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// The role needs a routing list, otherwise the generator emits no zone.
	routingDir := manager.routingDirectory()
	if err := manager.SetRoutingList("primary", "example.com\n"); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "leshy", "config.toml")
	manager.leshyConfigPath = configPath

	// Default: dual-stack on.
	if err := manager.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ipv6 = true") {
		t.Fatalf("IPv6 must default to enabled:\n%s", content)
	}

	// The UI toggle flips the policy per role and regenerates.
	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, ID: "t", Method: "SetRoleIPv6", Role: "primary", Enabled: boolPtr(false),
	})
	if !response.OK {
		t.Fatalf("SetRoleIPv6 failed: %#v", response)
	}
	if manager.RoleIPv6("primary") {
		t.Fatal("the toggle must persist off")
	}
	content, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ipv6 = false") {
		t.Fatalf("the regenerated zone must carry the disabled policy:\n%s", content)
	}

	// The snapshot exposes the policy for the UI.
	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		if role.ID == "primary" && role.LeshyIPv6 {
			t.Fatalf("snapshot must reflect the disabled policy: %#v", role)
		}
	}

	// An unknown role is refused.
	response = manager.Handle(context.Background(), Request{
		Version: APIVersion, ID: "t", Method: "SetRoleIPv6", Role: "nope", Enabled: boolPtr(true),
	})
	if response.OK {
		t.Fatal("an unknown role must be refused")
	}

	// Back on: the toggle is per role, nothing else changes.
	if err := manager.SetRoleIPv6("primary", true); err != nil {
		t.Fatal(err)
	}
	if !manager.RoleIPv6("primary") {
		t.Fatal("the toggle must persist on")
	}
	_ = routingDir
}

func boolPtr(v bool) *bool { return &v }

func TestFlushZoneRemovesEveryRegisteredIntentOfTheZone(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	for _, prefix := range []string{"203.0.113.10/32", "2001:db8::10/128"} {
		if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
			Zone: "primary", Prefix: prefix, Source: LeshyRouteDNS,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.FlushZone(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	if got := len(manager.RegistrySnapshot("primary")); got != 0 {
		t.Fatalf("the zone must be flushed: %d entries left", got)
	}
	if len(fake.removes) != 2 {
		t.Fatalf("both kernel rows must be forgotten: %#v", fake.removes)
	}
	// An unknown zone is a hard denial, not a silent success.
	if err := manager.FlushZone(context.Background(), "nope"); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("an unknown zone must be denied: %v", err)
	}
}

func TestSetSelectedRouteWriterDeniesUntilWired(t *testing.T) {
	// A manager constructed without a writer denies everything fail-closed;
	// SetRoutes flips it live.
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		nil,
		func(string) (core.RoleRuntime, bool) { return readyRuntime("primary", true, true), true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return t.TempDir(), true },
	)
	_, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if !errors.As(err, new(*SelectedRouteDeniedError)) || !strings.Contains(err.Error(), "no selected-route writer") {
		t.Fatalf("an unwired writer must deny: %v", err)
	}
	fake := newSelectedRoutesFake()
	manager.SetRoutes(fake)
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatalf("after SetRoutes the intent must apply: %v", err)
	}
}

func TestCanonicalIntentPrefixRejectsBadShapes(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		source LeshyRouteSource
	}{
		{"non-canonical v4", "203.0.113.10/24", LeshyRouteDNS},
		{"mapped ipv6", "::ffff:203.0.113.10/128", LeshyRouteDNS},
		{"dns non-host", "10.0.0.0/8", LeshyRouteDNS},
		{"unknown source", "203.0.113.10/32", "carrier-pigeon"},
	}
	for _, tc := range cases {
		_, err := canonicalIntentPrefix(LeshyRouteIntent{Zone: "primary", Prefix: tc.prefix, Source: tc.source})
		if err == nil {
			t.Fatalf("%s must be rejected", tc.name)
		}
	}
	// Static intents may be non-host, canonical only.
	prefix, err := canonicalIntentPrefix(LeshyRouteIntent{Zone: "primary", Prefix: "10.0.0.0/24", Source: LeshyRouteStatic})
	if err != nil {
		t.Fatal(err)
	}
	if prefix.String() != "10.0.0.0/24" {
		t.Fatalf("static prefix: %v", prefix)
	}
}

func TestConflictErrorImplementsTheContract(t *testing.T) {
	err := &SelectedRouteConflictError{Role: "primary", Zone: "primary", Prefix: "10.0.0.1/32"}
	if !isConflictErr(err) {
		t.Fatal("isConflictErr must recognize the typed conflict")
	}
	if err.Error() != "route 10.0.0.1/32 conflicts with a foreign row for role \"primary\"" {
		t.Fatalf("conflict message: %s", err.Error())
	}
}
