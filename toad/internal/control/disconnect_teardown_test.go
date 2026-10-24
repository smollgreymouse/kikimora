package control

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// teardownRouteManager records RemoveEndpointPolicy calls so a disconnect test
// can prove the endpoint policy (and, on the kernel side, the table-51890
// sentinels through the removal plan) is actually released.
type teardownRouteManager struct {
	mu      sync.Mutex
	removes []endpoint.Policy
	err     error
}

func (m *teardownRouteManager) ReconcileEndpointPolicy(context.Context, endpoint.Policy) error {
	return nil
}

func (m *teardownRouteManager) RemoveEndpointPolicy(_ context.Context, p endpoint.Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removes = append(m.removes, p)
	return m.err
}

func (m *teardownRouteManager) ApplyParking(context.Context, parking.DesiredState) error {
	return nil
}

func (m *teardownRouteManager) Snapshot(context.Context) (routing.KernelState, error) {
	return routing.KernelState{}, nil
}

func (m *teardownRouteManager) removeCalls() []endpoint.Policy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]endpoint.Policy(nil), m.removes...)
}

// newTeardownManager builds a manager for one role with a recording route
// manager, a fake kernel and a file-backed Leshy bridge.
func newTeardownManager(t *testing.T, name string) (*Manager, *recoveryDriver, *teardownRouteManager, string, *routing.Fake) {
	t.Helper()
	dir := t.TempDir()
	cfg := writeConfig(t, dir, name, "openconnect")
	manager, err := NewManager([]string{cfg}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })

	pubDir := filepath.Join(dir, "pub")
	routes := &teardownRouteManager{}
	kernel := &routing.Fake{}
	driver := NewRecoveryDriver(manager, RecoveryServices{
		Executor: kernel,
		Routes:   routes,
		Leshy:    leshy.FileBridge{Dir: pubDir},
	}).(*recoveryDriver)
	manager.SetRecoveryDriver(driver)
	return manager, driver, routes, pubDir, kernel
}

func TestDisconnectRoleReleasesOwnedResources(t *testing.T) {
	manager, driver, routes, pubDir, kernel := newTeardownManager(t, "primary")

	// Seed the three resource classes the release must own: verified parks in
	// the fake kernel (restored through the checkpoint path), a published
	// Leshy route target and the ownership checkpoint on disk.
	kernel.State = routing.KernelState{Routes: []routing.Operation{{
		Kind: "park", Prefix: "198.51.100.20/32", Table: 254, Metric: 42760, Protocol: 4,
	}}}
	if err := driver.parkingManager().RestoreCheckpoint(context.Background(), parking.Checkpoint{
		Role:   "primary",
		Parked: []parking.OwnedRoute{{Role: "primary", Prefix: netip.MustParsePrefix("198.51.100.20/32")}},
	}); err != nil {
		t.Fatal(err)
	}
	if !driver.parkingManager().Snapshot("primary").Active {
		t.Fatal("parks must be active before the disconnect")
	}
	if err := driver.restoreOwnershipCheckpoint(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	if err := driver.writeOwnershipCheckpoint("primary"); err != nil {
		t.Fatal(err)
	}
	bridge := leshy.FileBridge{Dir: pubDir}
	if err := bridge.Publish(context.Background(), leshy.RolePublication{Role: "primary", Zone: "primary", Interface: "kkprimary"}); err != nil {
		t.Fatal(err)
	}

	if err := manager.DisconnectRole("primary"); err != nil {
		t.Fatalf("DisconnectRole: %v", err)
	}

	removes := routes.removeCalls()
	if len(removes) != 1 || removes[0].Zone != "primary" || removes[0].Priority == 0 {
		t.Fatalf("endpoint policy not removed once with the role identity: %+v", removes)
	}
	if driver.parkingManager().Snapshot("primary").Active {
		t.Fatal("parks survived the explicit disconnect")
	}
	if _, err := os.Stat(filepath.Join(pubDir, "primary.dev")); !os.IsNotExist(err) {
		t.Fatalf("Leshy publication survived the disconnect: %v", err)
	}
	checkpoint, err := parking.ReadCheckpoint(filepath.Join(configStateDir(t, manager, "primary"), "parking.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Parked) != 0 {
		t.Fatalf("ownership checkpoint kept parked routes: %+v", checkpoint)
	}
	manager.mu.Lock()
	lastError := manager.roles["primary"].lastError
	manager.mu.Unlock()
	if lastError != "" {
		t.Fatalf("clean disconnect reported an error: %q", lastError)
	}
}

func TestDisconnectRoleReportsIncompleteCleanup(t *testing.T) {
	manager, _, routes, _, _ := newTeardownManager(t, "primary")
	routes.err = context.DeadlineExceeded

	if err := manager.DisconnectRole("primary"); err == nil {
		t.Fatal("DisconnectRole must fail when the endpoint policy cannot be removed")
	}
	manager.mu.Lock()
	lastError := manager.roles["primary"].lastError
	manager.mu.Unlock()
	if !strings.HasPrefix(lastError, "cleanup incomplete") {
		t.Fatalf("residue not recorded on the role: %q", lastError)
	}
}

func TestDisconnectAllReleasesEveryRole(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "primary", "openconnect"),
		writeConfig(t, dir, "secondary", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer func() { _ = manager.Close() }()
	routes := &teardownRouteManager{}
	kernel := &routing.Fake{}
	pubDir := filepath.Join(dir, "pub")
	driver := NewRecoveryDriver(manager, RecoveryServices{
		Executor: kernel,
		Routes:   routes,
		Leshy:    leshy.FileBridge{Dir: pubDir},
	}).(*recoveryDriver)
	manager.SetRecoveryDriver(driver)

	// Seed every resource class for both roles: verified parks in the fake
	// kernel (loaded through the checkpoint), published route targets and the
	// ownership checkpoints on disk. DisconnectAll must leave none of them.
	kernel.State = routing.KernelState{Routes: []routing.Operation{
		{Kind: "park", Prefix: "198.51.100.20/32", Table: 254, Metric: 42760, Protocol: 4},
		{Kind: "park", Prefix: "198.51.100.21/32", Table: 254, Metric: 42760, Protocol: 4},
	}}
	bridge := leshy.FileBridge{Dir: pubDir}
	for _, name := range []string{"primary", "secondary"} {
		prefix := netip.MustParsePrefix("198.51.100.2" + map[string]string{"primary": "0", "secondary": "1"}[name] + "/32")
		if err := driver.restoreOwnershipCheckpoint(context.Background(), name); err != nil {
			t.Fatal(err)
		}
		if err := driver.parkingManager().RestoreCheckpoint(context.Background(), parking.Checkpoint{
			Role:   name,
			Parked: []parking.OwnedRoute{{Role: name, Prefix: prefix}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := driver.writeOwnershipCheckpoint(name); err != nil {
			t.Fatal(err)
		}
		if err := bridge.Publish(context.Background(), leshy.RolePublication{Role: name, Zone: name, Interface: "kk" + name}); err != nil {
			t.Fatal(err)
		}
	}

	if err := manager.DisconnectAll(); err != nil {
		t.Fatalf("DisconnectAll: %v", err)
	}
	removes := routes.removeCalls()
	if len(removes) != 2 {
		t.Fatalf("RemoveEndpointPolicy called %d times, want one per role: %+v", len(removes), removes)
	}
	state, err := kernel.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Routes) != 0 {
		t.Fatalf("kernel routes survived DisconnectAll: %+v", state.Routes)
	}
	for _, name := range []string{"primary", "secondary"} {
		if _, err := os.Stat(filepath.Join(pubDir, name+".dev")); !os.IsNotExist(err) {
			t.Fatalf("publication of %q survived DisconnectAll: %v", name, err)
		}
		checkpoint, err := parking.ReadCheckpoint(filepath.Join(configStateDir(t, manager, name), "parking.json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(checkpoint.Parked) != 0 {
			t.Fatalf("ownership checkpoint of %q kept parked routes: %+v", name, checkpoint)
		}
		manager.mu.Lock()
		lastError := manager.roles[name].lastError
		enabled := manager.roles[name].enabled
		manager.mu.Unlock()
		if enabled || lastError != "" {
			t.Fatalf("role %q not cleanly released: enabled=%v lastError=%q", name, enabled, lastError)
		}
	}
}

func configStateDir(t *testing.T, m *Manager, role string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.roles[role]
	if !ok || r.cfg == nil {
		t.Fatalf("role %q has no config", role)
	}
	return r.cfg.StateDir
}

var _ platform.RouteManager = (*teardownRouteManager)(nil)

// TestReleaseProductMatchesDisconnectAllState pins the graceful-stop
// contract: the network ends as if Kikimora does not participate, while the
// persisted operator intent stays enabled for the next start.
func TestReleaseProductMatchesDisconnectAllState(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "primary", "openconnect"),
		writeConfig(t, dir, "secondary", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	routes := &teardownRouteManager{}
	kernel := &routing.Fake{}
	kernel.State = routing.KernelState{Routes: []routing.Operation{{
		Kind: "park", Prefix: "198.51.100.20/32", Table: 254, Metric: 42760, Protocol: 4,
	}}}
	driver := NewRecoveryDriver(manager, RecoveryServices{
		Executor: kernel,
		Routes:   routes,
		Leshy:    leshy.FileBridge{Dir: filepath.Join(dir, "pub")},
	}).(*recoveryDriver)
	manager.SetRecoveryDriver(driver)
	pubDir := filepath.Join(dir, "pub")
	bridge := leshy.FileBridge{Dir: pubDir}
	if err := bridge.Publish(context.Background(), leshy.RolePublication{Role: "primary", Zone: "primary", Interface: "kkprimary"}); err != nil {
		t.Fatal(err)
	}
	// Both roles carry persisted operator intent: enabled and desired.
	manager.mu.Lock()
	manager.roles["primary"].enabled = true
	manager.roles["secondary"].enabled = true
	manager.mu.Unlock()

	if err := manager.ReleaseProduct(context.Background()); err != nil {
		t.Fatalf("ReleaseProduct: %v", err)
	}

	removes := routes.removeCalls()
	if len(removes) != 2 {
		t.Fatalf("endpoint policies not released for both roles: %+v", removes)
	}
	if _, err := os.Stat(filepath.Join(pubDir, "primary.dev")); !os.IsNotExist(err) {
		t.Fatalf("publication survived the graceful stop: %v", err)
	}
	if driver.parkingManager().Snapshot("primary").Active {
		t.Fatal("fail-closed parks survived the graceful stop")
	}
	// Operator intent is kept: the roles remain desired, the next start
	// reconnects them.
	for _, name := range []string{"primary", "secondary"} {
		manager.mu.Lock()
		enabled := manager.roles[name].enabled
		lastError := manager.roles[name].lastError
		manager.mu.Unlock()
		if !enabled || lastError != "" {
			t.Fatalf("role %q intent not preserved: enabled=%v lastError=%q", name, enabled, lastError)
		}
	}
	// The deferred Close must not fight the release: it observes shuttingDown.
	if err := manager.Close(); err != nil {
		t.Fatalf("Close after ReleaseProduct: %v", err)
	}
}

func TestRestartMarkerPathEnvOverride(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "restart.pending")
	t.Setenv("KIKIMORA_RESTART_MARKER", marker)
	if got := RestartMarkerPath(); got != marker {
		t.Fatalf("marker path override ignored: %q", got)
	}
	os.Unsetenv("KIKIMORA_RESTART_MARKER")
	if got := RestartMarkerPath(); got != "/run/kikimora/core-restart.pending" {
		t.Fatalf("default marker path changed: %q", got)
	}
}

// TestDisconnectRoleReleasesCrashInheritedParks covers the false-success
// window: parks inherited from a crashed core live in the kernel and in the
// on-disk checkpoint while this process's in-memory parking manager is empty.
// The release must load the checkpoint before clearing, or the parks survive.
func TestDisconnectRoleReleasesCrashInheritedParks(t *testing.T) {
	manager, driver, routes, _, kernel := newTeardownManager(t, "primary")
	parkPrefix := netip.MustParsePrefix("198.51.100.20/32")
	parkRoute := routing.Operation{Kind: "park", Prefix: parkPrefix.String(), Table: 254, Metric: 42760, Protocol: 4}
	kernel.State = routing.KernelState{Routes: []routing.Operation{parkRoute}}
	if err := parking.WriteCheckpoint(filepath.Join(configStateDir(t, manager, "primary"), "parking.json"), parking.Checkpoint{
		Role:   "primary",
		Parked: []parking.OwnedRoute{{Role: "primary", Prefix: parkPrefix}},
	}); err != nil {
		t.Fatal(err)
	}
	// Deliberately no parkingManager restore: the in-memory state is empty,
	// exactly like after a core crash replacement.
	if driver.parkingManager().Snapshot("primary").Active {
		t.Fatal("parks must not be in memory before the disconnect")
	}

	if err := manager.DisconnectRole("primary"); err != nil {
		t.Fatalf("DisconnectRole: %v", err)
	}
	if routes.removeCalls() == nil {
		t.Fatal("endpoint policy not removed")
	}
	state, err := kernel.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range state.Routes {
		if route.Kind == "park" && route.Prefix == parkPrefix.String() {
			t.Fatalf("crash-inherited park survived the disconnect: %+v", state.Routes)
		}
	}
}

// TestDisconnectRoleLeavesSiblingResourcesAlone pins the DisconnectRole
// isolation at the manager level: only the disconnected role's policy is
// removed and the sibling's publication file survives.
func TestDisconnectRoleLeavesSiblingResourcesAlone(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "primary", "openconnect"),
		writeConfig(t, dir, "secondary", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer func() { _ = manager.Close() }()
	pubDir := filepath.Join(dir, "pub")
	routes := &teardownRouteManager{}
	driver := NewRecoveryDriver(manager, RecoveryServices{
		Executor: &routing.Fake{},
		Routes:   routes,
		Leshy:    leshy.FileBridge{Dir: pubDir},
	}).(*recoveryDriver)
	manager.SetRecoveryDriver(driver)
	bridge := leshy.FileBridge{Dir: pubDir}
	for _, zone := range []string{"primary", "secondary"} {
		if err := bridge.Publish(context.Background(), leshy.RolePublication{Role: zone, Zone: zone, Interface: "kk" + zone}); err != nil {
			t.Fatal(err)
		}
	}

	if err := manager.DisconnectRole("primary"); err != nil {
		t.Fatalf("DisconnectRole: %v", err)
	}
	removes := routes.removeCalls()
	if len(removes) != 1 || removes[0].Zone != "primary" {
		t.Fatalf("sibling policy must not be removed, got %+v", removes)
	}
	if _, err := os.Stat(filepath.Join(pubDir, "secondary.dev")); err != nil {
		t.Fatalf("sibling publication removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pubDir, "primary.dev")); !os.IsNotExist(err) {
		t.Fatalf("own publication survived: %v", err)
	}
}
