package control

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// markerDir builds a config directory with a schema-1 managed marker.
func markerDir(t *testing.T, socket string, zones ...string) string {
	t.Helper()
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()
	if err := manager.EnableManagedSelectedRoutes(socket, true); err != nil {
		t.Fatal(err)
	}
	_ = zones
	return dir
}

func TestStrictMarkerFailsStartupClosed(t *testing.T) {
	dir := markerDir(t, "/run/kk-test.sock")
	path := filepath.Join(dir, "leshy-managed.json")
	// Malformed JSON.
	if err := os.WriteFile(path, []byte("{\"schema\":1,"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, ""); err == nil {
		t.Fatal("a malformed managed marker must fail core startup")
	}
	// Unsupported schema.
	if err := os.WriteFile(path, []byte("{\"schema\":99,\"socket\":\"/x\",\"managed_zones\":[]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, ""); err == nil {
		t.Fatal("an unsupported marker schema must fail core startup")
	}
	// Duplicate managed zone.
	if err := os.WriteFile(path, []byte("{\"schema\":1,\"socket\":\"/x\",\"managed_zones\":[\"one\",\"one\"]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, ""); err == nil {
		t.Fatal("a duplicate managed zone must fail core startup")
	}
	// A valid marker still loads.
	if err := os.WriteFile(path, []byte("{\"schema\":1,\"socket\":\"/x\",\"core_version\":\"t\",\"managed_zones\":[\"one\"]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("valid marker must load: %v", err)
	}
	defer manager.Close()
	if !manager.ManagedModeActive() {
		t.Fatal("the marker must arm managed mode")
	}
}

// seededCheckpoint writes a schema-2 checkpoint with one Active owned entry.
func seededCheckpoint(t *testing.T, dir string) {
	t.Helper()
	prefix := netip.MustParsePrefix("203.0.113.10/32")
	identity := routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}
	checkpoint := routing.SelectedRoutesCheckpoint{
		Schema:          routing.SelectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 4,
		Entries: []routing.SelectedRoute{{
			Role: "primary", Zone: "primary", Prefix: prefix,
			Source: routing.SelectedDNS, ToadGeneration: 2, RouteGeneration: 4,
			Interface: "kkone", Desired: identity,
			Owned: &identity,
			State: routing.SelectedActive,
		}},
		Platform: routing.RuntimeCheckpointPlatform(),
	}
	// The test config template points state_dir at <configdir>/<role>.
	stateDir := filepath.Join(dir, "primary")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteSelectedRoutesCheckpoint(filepath.Join(stateDir, "selected-routes.json"), checkpoint); err != nil {
		t.Fatal(err)
	}
}

func managedManager(t *testing.T, dir string) *Manager {
	t.Helper()
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	manager.SetManagedSelectedRole("primary", true)
	return manager
}

func TestStartupRestoreFreezesRestoredOwnership(t *testing.T) {
	dir := markerDir(t, "/run/kk-test.sock")
	seededCheckpoint(t, dir)
	manager := managedManager(t, dir)
	defer manager.Close()

	snapshot := manager.selectedRoutes.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != routing.SelectedActive {
		t.Fatalf("the checkpoint ownership must be restored: %#v", snapshot)
	}
	if snapshot[0].Owned == nil || snapshot[0].Owned.IfIndex != 7 {
		t.Fatalf("the exact owned identity must survive the restart: %#v", snapshot[0])
	}
	state := manager.selectedRoutes.roles["primary"]
	if !state.restorePending || !state.frozen {
		t.Fatalf("a non-empty checkpoint must freeze the role with restorePending: %#v", state)
	}
	if state.routeGeneration != 5 {
		t.Fatalf("the seeded generation must be checkpoint+1: %d", state.routeGeneration)
	}
	if len(state.verification) != 0 {
		t.Fatal("no verification token may be restored (§0.2)")
	}

	// Ensure is denied while startup-frozen.
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	if _, err := manager.selectedRoutes.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.11/32", Source: LeshyRouteDNS,
	}); !errors.As(err, new(*SelectedRouteDeniedError)) {
		t.Fatalf("a startup-frozen role must deny ensures: %v", err)
	}
	if fake.reconcileCount() != 0 {
		t.Fatal("a frozen role must not touch the kernel")
	}

	// Restored Active has no token: CompleteStartupRestore must refuse.
	if manager.selectedRoutes.CompleteStartupRestore("primary") {
		t.Fatal("complete restore is illegal without current verification tokens")
	}
	// Mint tokens for the restored record (the audit/recovery owns this in
	// production) and complete the restore.
	manager.selectedRoutes.mu.Lock()
	entry := manager.selectedRoutes.RegistrySnapshot("primary")[0]
	manager.selectedRoutes.roles["primary"].verification = map[netip.Prefix]selectedVerification{
		entry.Prefix: {
			ToadGeneration:  entry.ToadGeneration,
			RouteGeneration: manager.selectedRoutes.roles["primary"].routeGeneration,
			Identity:        entry.Desired,
		},
	}
	manager.selectedRoutes.mu.Unlock()
	if !manager.selectedRoutes.CompleteStartupRestore("primary") {
		t.Fatal("complete restore must clear the exact-conditions gate")
	}
	state = manager.selectedRoutes.roles["primary"]
	if state.restorePending || state.frozen {
		t.Fatalf("complete restore must clear the startup state: %#v", state)
	}
}

func TestCompleteStartupRestoreRefusesParksAndRetiring(t *testing.T) {
	dir := markerDir(t, "/run/kk-test.sock")
	seededCheckpoint(t, dir)
	manager := managedManager(t, dir)
	defer manager.Close()

	entry := manager.selectedRoutes.RegistrySnapshot("primary")[0]
	state := manager.selectedRoutes.roles["primary"]
	state.verification = map[netip.Prefix]selectedVerification{
		entry.Prefix: {
			ToadGeneration:  entry.ToadGeneration,
			RouteGeneration: state.routeGeneration,
			Identity:        entry.Desired,
		},
	}

	// A Park on the record blocks CompleteStartupRestore: the reconciler
	// never clears the freeze with an unreconciled park transaction.
	park := routing.SelectedParkIdentity{
		Kind: routing.SelectedParkUnreachable, Table: routing.ManagedSelectedRouteTable,
		Protocol: 4, Metric: routing.ManagedParkMetric,
	}
	if err := manager.selectedRoutes.registry.ReservePark("primary", entry.Prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if manager.selectedRoutes.CompleteStartupRestore("primary") {
		t.Fatal("a park transaction must block CompleteStartupRestore")
	}
	if err := manager.selectedRoutes.registry.ClearPark("primary", entry.Prefix); err != nil {
		t.Fatal(err)
	}
	// A Retiring record blocks it as well.
	if err := manager.selectedRoutes.registry.Reserve("primary", "primary", netip.MustParsePrefix("203.0.113.11/32"), routing.SelectedDNS, 1, state.routeGeneration, "kkone",
		routing.SelectedRouteIdentity{Table: routing.ManagedSelectedRouteTable, IfIndex: 7, Protocol: 4, Scope: routing.ScopeLink}); err != nil {
		t.Fatal(err)
	}
	if err := manager.selectedRoutes.registry.MarkRetiring("primary", netip.MustParsePrefix("203.0.113.11/32")); err != nil {
		t.Fatal(err)
	}
	if manager.selectedRoutes.CompleteStartupRestore("primary") {
		t.Fatal("a retiring record must block CompleteStartupRestore")
	}
}

func TestFailpointSeamKeepsTransactionEvidence(t *testing.T) {
	// §22.1/§22.3: a hook error simulates a process interruption at a named
	// durable boundary; whatever checkpoint state exists at that point is
	// preserved without compensating rollback.
	cases := []struct {
		point     string
		wantState routing.SelectedRouteState
	}{
		{"route.after_pending_checkpoint", routing.SelectedPending},
		{"route.after_preflight", routing.SelectedPending},
		{"route.after_applying_checkpoint", routing.SelectedApplying},
	}
	for _, tc := range cases {
		fake := newSelectedRoutesFake()
		manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
		var hit bool
		manager.SetFailpoint(func(point, role string, prefix netip.Prefix) error {
			if point == tc.point {
				hit = true
				return errors.New("simulated interruption")
			}
			return nil
		})
		if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
			Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
		}); err == nil {
			t.Fatalf("%s: the interruption must surface", tc.point)
		}
		if !hit {
			t.Fatalf("%s: the failpoint never fired", tc.point)
		}
		snapshot := manager.RegistrySnapshot("primary")
		if len(snapshot) != 1 || snapshot[0].State != tc.wantState {
			t.Fatalf("%s: durable state must be %q: %#v", tc.point, tc.wantState, snapshot)
		}
		if fake.reconcileCount() != 0 {
			t.Fatalf("%s: the interrupted apply must not report kernel success", tc.point)
		}
	}
}

func TestFailpointAfterKernelReconcileKeepsApplying(t *testing.T) {
	// §22.3 route.after_kernel_reconcile: the syscall may have landed but
	// the durable record stays Applying. A restart restores that record and
	// adopts the exact Desired because Applying was durable.
	dir := t.TempDir()
	fake := newSelectedRoutesFake()
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		fake,
		func(string) (core.RoleRuntime, bool) { return readyRuntime("primary", true, true), true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return dir, true },
	)
	manager.SetFailpoint(func(point, role string, prefix netip.Prefix) error {
		if point == "route.after_kernel_reconcile" {
			return errors.New("simulated interruption after the syscall")
		}
		return nil
	})
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err == nil {
		t.Fatal("the interruption must surface")
	}
	snapshot := manager.RegistrySnapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != routing.SelectedApplying {
		t.Fatalf("the durable evidence stays Applying: %#v", snapshot)
	}

	// Restart: a fresh manager restores the checkpoint exactly like the
	// startup path (§2.4) — the record comes back as Applying with no token.
	checkpoint, err := routing.ReadSelectedRoutesCheckpoint(filepath.Join(dir, "selected-routes.json"))
	if err != nil {
		t.Fatalf("the Applying evidence must be checkpointed: %v", err)
	}
	restoredRegistry := routing.NewMemorySelectedRegistry()
	if err := restoredRegistry.RestoreRole(checkpoint); err != nil {
		t.Fatal(err)
	}
	restarted := NewSelectedRouteManager(
		restoredRegistry,
		fake,
		func(string) (core.RoleRuntime, bool) { return readyRuntime("primary", true, true), true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return dir, true },
	)
	if state := restarted.RegistrySnapshot("primary")[0]; state.State != routing.SelectedApplying {
		t.Fatalf("the restart restores the Applying record: %#v", state)
	}
	result, err := restarted.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	})
	if err != nil {
		t.Fatalf("the restarted manager adopts the authorized Desired: %v", err)
	}
	if result.State != "already-applied" {
		t.Fatalf("the adopted row must answer already-applied: %#v", result)
	}
	if got := restarted.RegistrySnapshot("primary")[0].State; got != routing.SelectedActive {
		t.Fatalf("adoption proves Active: %q", got)
	}
}

func TestFreezeTimesOutWhileBlockedAndStaysFrozen(t *testing.T) {
	// §31.20: Freeze waits on the zero-in-flight signal; a timeout leaves
	// the role frozen and the blocked call can still finish.
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.mu.Lock()
	state := manager.roleState("primary")
	manager.beginFlight(state)
	manager.mu.Unlock()
	// Hold applyMu so the in-flight ensure cannot proceed.
	state.applyMu.Lock()
	defer state.applyMu.Unlock()
	release := make(chan struct{})
	go func() {
		<-release
		manager.mu.Lock()
		manager.endFlight(state)
		manager.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := manager.Freeze(ctx, "primary"); err == nil {
		t.Fatal("freeze must time out while an operation is blocked")
	}
	manager.mu.Lock()
	frozen := manager.roleState("primary").frozen
	manager.mu.Unlock()
	if !frozen {
		t.Fatal("a timed-out freeze stays frozen")
	}
	close(release)
}
