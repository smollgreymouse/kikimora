package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// PROD2A: the synthetic catch-all zone Leshy generates for the global default
// route must resolve to that role instead of being denied as an unknown zone.

func TestManagedBypassZoneResolvesToLiveOwner(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetBypassOwner(func() (string, bool) { return "secondary", true })

	result, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: config.BypassList, Prefix: "203.0.113.7/32", Source: LeshyRouteDNS,
	})
	if err != nil {
		t.Fatalf("Ensure(bypass): %v", err)
	}
	if result.Role != "secondary" {
		t.Fatalf("bypass intent resolved to %q, want secondary", result.Role)
	}
	if got := manager.registrySnapshotState("secondary", "203.0.113.7/32"); got != routing.SelectedActive {
		t.Fatalf("record state = %q, want active", got)
	}
}

func TestBypassEnsureForgetFlushUseTheSameOwner(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetBypassOwner(func() (string, bool) { return "primary", true })

	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.8/32", Source: LeshyRouteDNS}
	result, err := manager.Ensure(context.Background(), intent)
	if err != nil || result.Role != "primary" {
		t.Fatalf("Ensure(bypass): role=%q err=%v", result.Role, err)
	}
	if err := manager.Forget(context.Background(), intent); err != nil {
		t.Fatalf("Forget(bypass): %v", err)
	}
	if got := manager.registrySnapshotState("primary", "203.0.113.8/32"); got != "" {
		t.Fatalf("record state after Forget = %q, want absent", got)
	}

	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatalf("Ensure(bypass) again: %v", err)
	}
	if err := manager.FlushZone(context.Background(), config.BypassList); err != nil {
		t.Fatalf("FlushZone(bypass): %v", err)
	}
	if snapshot := manager.RegistrySnapshot("primary"); len(snapshot) != 0 {
		t.Fatalf("FlushZone left records behind: %#v", snapshot)
	}
}

func TestBypassZoneDeniedWhenDefaultRouteIsDirect(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetBypassOwner(func() (string, bool) { return "", false })

	_, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: config.BypassList, Prefix: "203.0.113.9/32", Source: LeshyRouteDNS,
	})
	var denied *SelectedRouteDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Ensure(bypass) error = %v, want SelectedRouteDeniedError", err)
	}
	if snapshot := manager.RegistrySnapshot("primary"); len(snapshot) != 0 {
		t.Fatalf("a denied intent mutated the registry: %#v", snapshot)
	}
}

func TestBypassOwnerSwitchDrainsOldOwnerFirst(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	owner := "primary"
	manager.SetBypassOwner(func() (string, bool) { return owner, true })

	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.20/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	removesBefore := len(fake.removes)

	// The Manager flushes the old owner before committing the new one; the
	// resolver must still answer with the old owner for that flush.
	if err := manager.FlushZone(context.Background(), config.BypassList); err != nil {
		t.Fatalf("FlushZone under the old owner: %v", err)
	}
	owner = "secondary"
	if len(fake.removes) <= removesBefore {
		t.Fatal("the old owner's records were not removed from the platform")
	}
	if snapshot := manager.RegistrySnapshot("primary"); len(snapshot) != 0 {
		t.Fatalf("old owner records survived the drain: %#v", snapshot)
	}
	result, err := manager.Ensure(context.Background(), intent)
	if err != nil {
		t.Fatalf("Ensure after the owner switch: %v", err)
	}
	if result.Role != "secondary" {
		t.Fatalf("new intent resolved to %q, want secondary", result.Role)
	}
}

// managedBypassManager wires the managed selected-route surface so the global
// default-route owner can be exercised at the Manager level.
func managedBypassManager(t *testing.T) (*Manager, *selectedRoutesFake) {
	t.Helper()
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "one", "openconnect"),
		writeConfig(t, dir, "two", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.conf")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.reloadHook = func(context.Context) error { return nil }
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.selectedRoutes.SetManagedRoleCheck(func(string) bool { return true })
	manager.selectedRoutes.SetActivationFence(func(string) (selectedActivationFence, bool) {
		return selectedActivationFence{ActivationID: "bypass-test", ModeGeneration: 1}, true
	})
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("one", true, true), true
	}
	return manager, fake
}

func TestSetBypassModeDrainsOldOwnerBeforeSwitch(t *testing.T) {
	manager, fake := managedBypassManager(t)
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("SetBypassMode(one): %v", err)
	}
	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.30/32", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), intent); err != nil {
		t.Fatalf("Ensure under one: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.30/32"); got != routing.SelectedActive {
		t.Fatalf("record state = %q, want active", got)
	}
	removesBefore := len(fake.removes)

	if err := manager.SetBypassMode("two"); err != nil {
		t.Fatalf("SetBypassMode(two): %v", err)
	}
	if len(fake.removes) <= removesBefore {
		t.Fatal("switching the owner did not drain the old role's rows")
	}
	if snapshot := manager.selectedRoutes.RegistrySnapshot("one"); len(snapshot) != 0 {
		t.Fatalf("old owner records survived the switch: %#v", snapshot)
	}
	result, err := manager.selectedRoutes.Ensure(context.Background(), intent)
	if err != nil {
		t.Fatalf("Ensure under two: %v", err)
	}
	if result.Role != "two" {
		t.Fatalf("post-switch intent resolved to %q, want two", result.Role)
	}
}

func TestSetBypassModeRejectsUnknownRole(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.reloadHook = func(context.Context) error { return nil }

	if err := manager.SetBypassMode("missing"); err == nil {
		t.Fatal("an unknown default-route role must be rejected")
	}
	if _, err := os.Stat(manager.bypassConfigPath); !os.IsNotExist(err) {
		t.Fatalf("the rejected mode reached the bypass file: %v", err)
	}
}

func TestSetBypassModeRejectsRoleOutsideActiveProfile(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.reloadHook = func(context.Context) error { return nil }

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBypassMode("two"); err == nil {
		t.Fatal("a role outside the active profile must be rejected as the default route")
	}
}

func TestSetActiveProfileRejectsProfileWithoutBypassRole(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.reloadHook = func(context.Context) error { return nil }

	if err := manager.SetBypassMode("two"); err != nil {
		t.Fatalf("SetBypassMode(two): %v", err)
	}
	before := manager.activeProfile
	manager.mu.Lock()
	twoBefore := manager.roles["two"].enabled
	manager.mu.Unlock()
	if err := manager.SetActiveProfile("work"); err == nil {
		t.Fatal("a profile that disables the default-route role must be rejected")
	}
	manager.mu.Lock()
	active := manager.activeProfile
	twoEnabled := manager.roles["two"].enabled
	manager.mu.Unlock()
	if active != before {
		t.Fatalf("active profile moved to %q despite the rejection", active)
	}
	if twoEnabled != twoBefore {
		t.Fatal("the rejected switch changed the default-route role's desired state")
	}
}

func TestSetBypassModeReloadFailureRollsBack(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	reloads := 0
	manager.reloadHook = func(context.Context) error {
		reloads++
		if reloads > 1 {
			return errors.New("leshy restart failed")
		}
		return nil
	}

	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	err := manager.SetBypassMode("two")
	if err == nil {
		t.Fatal("a failed Leshy reload must fail the default-route command")
	}
	if got := manager.BypassMode(); got != "one" {
		t.Fatalf("cached bypass mode = %q after the failed reload, want one", got)
	}
	if got := config.ReadBypassMode(manager.bypassConfigPath); got != "one" {
		t.Fatalf("bypass file = %q after rollback, want one", got)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range zones {
		if zone.Name == config.BypassList {
			want := config.RoleZoneTarget(manager.leshyPublicationDirectory(), "one")
			if zone.RouteTarget != want {
				t.Fatalf("catch-all route target = %s, want the previous owner %s", zone.RouteTarget, want)
			}
		}
	}
}

func TestSetBypassModeAPIReportsReloadFailure(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.reloadHook = func(context.Context) error { return errors.New("leshy restart failed") }

	response := manager.Handle(context.Background(), Request{
		Version: 2, Method: "SetBypassMode", Mode: "one",
	})
	if response.OK {
		t.Fatal("the API reported success even though Leshy did not reload")
	}
}
