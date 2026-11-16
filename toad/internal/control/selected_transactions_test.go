package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

// The AUD3 tests (2026-11-16 post-e7ec9c2 audit §4.3) pin the publication
// lifecycle primitives: an explicit departure retires the exact role/zone
// records, advances the invalidation generation exactly once, clears the
// fast-path tokens, leaves the admission frozen, and a successful republish
// opens the admission without another generation bump.

func twoRoleSelectedManager(t *testing.T, fake *selectedRoutesFake) (*SelectedRouteManager, map[string]string) {
	t.Helper()
	dirs := map[string]string{"primary": t.TempDir(), "secondary": t.TempDir()}
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		fake,
		func(name string) (core.RoleRuntime, bool) { return readyRuntime(name, true, true), true },
		map[string][]string{"primary": {"primary"}, "secondary": {"secondary"}},
		func(name string) (string, bool) { dir, ok := dirs[name]; return dir, ok },
	)
	manager.SetActivationFence(testActivationFence)
	manager.SetManagedRoleCheck(func(string) bool { return true })
	return manager, dirs
}

func selectedCheckpointFile(t *testing.T, dir, role string) routing.SelectedRoutesCheckpoint {
	t.Helper()
	checkpoint, err := routing.ReadSelectedRoutesCheckpoint(filepath.Join(dir, "selected-routes.json"))
	if err != nil {
		t.Fatalf("read the selected-routes checkpoint for %q: %v", role, err)
	}
	return checkpoint
}

func TestRetirePublicationRetiresExactZoneAndStaysFrozen(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager, dirs := twoRoleSelectedManager(t, fake)
	primaryIntent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
	secondaryIntent := LeshyRouteIntent{Zone: "secondary", Prefix: "203.0.113.11/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), primaryIntent); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), secondaryIntent); err != nil {
		t.Fatal(err)
	}
	// The zero-I/O token is live: a duplicate Ensure takes the fast path.
	if _, err := manager.Ensure(context.Background(), primaryIntent); err != nil {
		t.Fatal(err)
	}
	if fake.reconcileCount() != 2 {
		t.Fatalf("reconciles before retirement = %d, want 2 (fast path on)", fake.reconcileCount())
	}

	before := manager.currentGeneration("primary")
	generation, err := manager.RetirePublication(context.Background(), "primary", "primary")
	if err != nil {
		t.Fatalf("RetirePublication: %v", err)
	}
	if generation != before+1 {
		t.Fatalf("retirement generation = %d, want exactly %d", generation, before+1)
	}
	if manager.currentGeneration("primary") != generation {
		t.Fatal("the retirement did not leave the advanced generation in place")
	}
	if snapshot := manager.RegistrySnapshot("primary"); len(snapshot) != 0 {
		t.Fatalf("retired ownership survived: %#v", snapshot)
	}
	if snapshot := manager.RegistrySnapshot("secondary"); len(snapshot) != 1 || snapshot[0].Zone != "secondary" {
		t.Fatalf("the unrelated role's ownership did not survive: %#v", snapshot)
	}
	// The admission stays frozen: a new Ensure is denied and cannot capture
	// the old generation.
	if _, err := manager.Ensure(context.Background(), primaryIntent); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("a retired publication must deny new ensures: %v", err)
	}
	// The checkpoint records the new generation and no retired ownership.
	checkpoint := selectedCheckpointFile(t, dirs["primary"], "primary")
	if checkpoint.RouteGeneration != generation {
		t.Fatalf("checkpoint generation = %d, want %d", checkpoint.RouteGeneration, generation)
	}
	if len(checkpoint.Entries) != 0 {
		t.Fatalf("checkpoint kept retired ownership: %#v", checkpoint.Entries)
	}

	// A successful republish (OpenPublication) reopens admission without
	// another generation bump, and the cleared verification token forces the
	// real reconcile again.
	if opened := manager.OpenPublication("primary"); opened != generation {
		t.Fatalf("OpenPublication generation = %d, want the retirement generation %d", opened, generation)
	}
	if manager.currentGeneration("primary") != generation {
		t.Fatal("OpenPublication must not bump the invalidation generation")
	}
	if _, err := manager.Ensure(context.Background(), primaryIntent); err != nil {
		t.Fatalf("Ensure after the republish: %v", err)
	}
	if fake.reconcileCount() != 3 {
		t.Fatalf("reconciles after the republish = %d, want 3 (token cleared by the retirement)", fake.reconcileCount())
	}
	// The secondary role was never touched.
	if manager.currentGeneration("secondary") != 1 {
		t.Fatalf("unrelated role generation = %d, want the untouched 1", manager.currentGeneration("secondary"))
	}
}

func TestRetirePublicationRejectsEmptyZone(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager, _ := twoRoleSelectedManager(t, fake)
	if _, err := manager.RetirePublication(context.Background(), "primary", ""); err == nil {
		t.Fatal("a retirement without a zone must be refused")
	}
}

// scriptedLeshyBridge is a Leshy bridge with a scriptable Publish failure so
// the publication path can be driven through success and failure.
type scriptedLeshyBridge struct {
	publishErr error
	published  []string
	withdrawn  []string
}

func (b *scriptedLeshyBridge) Publish(_ context.Context, p leshy.RolePublication) error {
	if b.publishErr != nil {
		return b.publishErr
	}
	b.published = append(b.published, p.Zone)
	return nil
}

func (b *scriptedLeshyBridge) Withdraw(_ context.Context, zone string) error {
	b.withdrawn = append(b.withdrawn, zone)
	return nil
}

func (b *scriptedLeshyBridge) Resync(context.Context, string) error { return nil }

// The driver-level tests below pin the AUD3 release/publish wiring: an
// explicit departure freezes the admission and publishes the advanced
// generation, a failed Leshy Publish never opens the admission, and the
// recovery Withdraw preserves zone + generation for the later Unfreeze bump.

func managedPublicationDriver(t *testing.T, bridge *scriptedLeshyBridge) (*Manager, *recoveryDriver, *selectedRoutesFake) {
	t.Helper()
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "amneziawg2")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	activateManagedRoleForTest(manager, "primary")
	manager.selectedRoutes.SetRouteAuthorityReady()
	runtime := readyRuntime("primary", true, true)
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) { return runtime, true }
	manager.product.UpdateRoleResources("primary", endpoint.State{}, leshy.PublicationState{}, parking.State{}, runtime.Validation)
	manager.SetRecoveryDriver(NewRecoveryDriver(manager, RecoveryServices{Executor: &routing.Fake{}, Leshy: bridge}))
	driver := manager.recoveryDriver.(*recoveryDriver)
	return manager, driver, fake
}

func TestReleasePublicationFreezesAdmissionAndPublishesGeneration(t *testing.T) {
	bridge := &scriptedLeshyBridge{}
	manager, driver, _ := managedPublicationDriver(t, bridge)
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	before := manager.selectedRoutes.currentGeneration("primary")

	if errs := driver.releaseManagedSelected(context.Background(), "primary"); len(errs) > 0 {
		t.Fatalf("releaseManagedSelected: %v", errors.Join(errs...))
	}
	generation := manager.selectedRoutes.currentGeneration("primary")
	if generation != before+1 {
		t.Fatalf("departure generation = %d, want exactly %d", generation, before+1)
	}
	// The admission is frozen after the departure...
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.14/32", Source: LeshyRouteDNS,
	}); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("a departed role must deny new ensures: %v", err)
	}
	// ...and the unpublished snapshot carries the zone and the advanced
	// generation, which is the invalidation identity Leshy consumes.
	product, _ := manager.product.Role("primary")
	if product.Publication.Published {
		t.Fatal("the publication stayed published after the departure")
	}
	if product.Publication.Zone != "primary" {
		t.Fatalf("publication zone = %q, want primary", product.Publication.Zone)
	}
	if product.Publication.Generation != generation {
		t.Fatalf("publication generation = %d, want %d", product.Publication.Generation, generation)
	}

	// A successful republish opens the admission and keeps the same
	// invalidation generation.
	manager.mu.Lock()
	manager.roles["primary"].observed = state.Snapshot{Name: "primary", Interface: state.InterfaceState{Name: "kkprimary", IfIndex: 7, MTU: 1380}}
	manager.roles["primary"].stateValid = true
	manager.mu.Unlock()
	if err := driver.Publish(context.Background(), "primary"); err != nil {
		t.Fatalf("Publish after the departure: %v", err)
	}
	product, _ = manager.product.Role("primary")
	if !product.Publication.Published || product.Publication.Generation != generation {
		t.Fatalf("republished snapshot: published=%v generation=%d, want true/%d",
			product.Publication.Published, product.Publication.Generation, generation)
	}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.14/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatalf("Ensure after the republish: %v", err)
	}
}

func TestFailedPublishLeavesAdmissionFrozen(t *testing.T) {
	bridge := &scriptedLeshyBridge{publishErr: errors.New("leshy publish failed")}
	manager, driver, _ := managedPublicationDriver(t, bridge)
	generation, err := manager.selectedRoutes.RetirePublication(context.Background(), "primary", "primary")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.roles["primary"].observed = state.Snapshot{Name: "primary", Interface: state.InterfaceState{Name: "kkprimary", IfIndex: 7, MTU: 1380}}
	manager.roles["primary"].stateValid = true
	manager.mu.Unlock()

	if err := driver.Publish(context.Background(), "primary"); err == nil {
		t.Fatal("a failed Leshy Publish must fail the publication step")
	}
	// The admission stays frozen: nothing may accept routes for a publication
	// that failed to appear, and the generation is unchanged.
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.15/32", Source: LeshyRouteDNS,
	}); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("a failed publication must keep the admission frozen: %v", err)
	}
	if manager.selectedRoutes.currentGeneration("primary") != generation {
		t.Fatal("the failed publication must not change the invalidation generation")
	}
}

func TestManagedWithdrawPreservesZoneAndGeneration(t *testing.T) {
	bridge := &scriptedLeshyBridge{}
	manager, driver, _ := managedPublicationDriver(t, bridge)
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.16/32", Source: LeshyRouteDNS,
	}); err != nil {
		t.Fatal(err)
	}
	// Recovery order: FreezeSelected -> ... -> Withdraw -> UnfreezeSelected.
	if err := driver.FreezeSelected(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	before := manager.selectedRoutes.currentGeneration("primary")
	if err := driver.Withdraw(context.Background(), "primary"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	product, _ := manager.product.Role("primary")
	if product.Publication.Published {
		t.Fatal("the withdrawal must unpublish the role")
	}
	if product.Publication.Zone != "primary" {
		t.Fatalf("withdrawn zone = %q, want primary preserved", product.Publication.Zone)
	}
	if product.Publication.Generation != before {
		t.Fatalf("withdrawn generation = %d, want the unchanged %d (Withdraw never bumps)", product.Publication.Generation, before)
	}
	if len(bridge.withdrawn) != 1 || bridge.withdrawn[0] != "primary" {
		t.Fatalf("the Leshy withdrawal calls: %v", bridge.withdrawn)
	}

	// The subsequent UnfreezeSelected performs the existing single increment
	// and the snapshot carries unpublished + same zone + N+1, which is the
	// changed (zone, generation) pair pinned Leshy invalidates on.
	if err := driver.UnfreezeSelected(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	product, _ = manager.product.Role("primary")
	if product.Publication.Published {
		t.Fatal("the unfrozen snapshot must stay unpublished")
	}
	if product.Publication.Zone != "primary" {
		t.Fatalf("unfrozen zone = %q, want primary", product.Publication.Zone)
	}
	if product.Publication.Generation != before+1 {
		t.Fatalf("unfrozen generation = %d, want exactly %d", product.Publication.Generation, before+1)
	}
}
