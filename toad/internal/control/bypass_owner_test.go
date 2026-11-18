package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// PROD2A: the synthetic catch-all zone Leshy generates for the global default
// route must resolve to that role instead of being denied as an unknown zone.

func TestManagedBypassZoneResolvesToLiveOwner(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetBypassOwner(func() BypassOwnerDecision { return BypassOwnerDecision{Role: "secondary", Available: true} })

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
	manager.SetBypassOwner(func() BypassOwnerDecision { return BypassOwnerDecision{Role: "primary", Available: true} })

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
	manager.SetBypassOwner(func() BypassOwnerDecision { return BypassOwnerDecision{} })

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
	manager.SetBypassOwner(func() BypassOwnerDecision { return BypassOwnerDecision{Role: owner, Available: true} })

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
	// R8B §2.7: the runtime default route requires a desired role.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
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
	connectBypassRoles(t, manager, "two")
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
	connectBypassRoles(t, manager, "one", "two")
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

// The AUD2 tests below pin the fail-closed bypass transition contract
// (2026-11-16 post-e7ec9c2 audit §3.3/§3.4): while a default-route owner
// switch is in flight no external bypass intent is routable, the old owner
// cannot be re-added, ordinary zones are unaffected, and a failed commit
// restores the exact previous policy.

// blockingReloadHook gates the first reload until the test releases it, so
// the concurrent probes run exactly while the switch transaction is armed.
type blockingReloadHook struct {
	entered chan struct{}
	gate    chan struct{}
	calls   atomic.Int64
}

func newBlockingReloadHook() *blockingReloadHook {
	return &blockingReloadHook{entered: make(chan struct{}), gate: make(chan struct{})}
}

func (h *blockingReloadHook) hook(context.Context) error {
	if h.calls.Add(1) == 1 {
		close(h.entered)
		<-h.gate
		return nil
	}
	return nil
}

func TestBypassSwitchFencesConcurrentEnsure(t *testing.T) {
	manager, fake := managedBypassManager(t)
	// The role configs in this fixture carry no generated zone, so register
	// one ordinary role zone explicitly for the unaffected-zone probe.
	manager.selectedRoutes.mu.Lock()
	manager.selectedRoutes.zoneRoles["one"] = []string{"one"}
	manager.selectedRoutes.mu.Unlock()
	connectBypassRoles(t, manager, "one", "two")
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	bypassIntent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.88/32", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), bypassIntent); err != nil {
		t.Fatalf("Ensure under the old owner: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.88/32"); got != routing.SelectedActive {
		t.Fatalf("old owner bypass record = %q, want active", got)
	}

	hook := newBlockingReloadHook()
	manager.reloadHook = hook.hook

	switchDone := make(chan error, 1)
	go func() { switchDone <- manager.SetBypassMode("two") }()
	select {
	case <-hook.entered:
	case <-time.After(testWaitBudget):
		t.Fatal("the switch never reached the reload boundary")
	}

	// While the switch is in flight the old owner is not routable: a
	// concurrent bypass Ensure must be denied with the transition fence and
	// must not re-add the old owner.
	_, err := manager.selectedRoutes.Ensure(context.Background(), bypassIntent)
	var denied *SelectedRouteDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("concurrent bypass Ensure during the switch = %v, want a typed denial", err)
	}
	if !strings.Contains(denied.Reason, "switching") {
		t.Fatalf("denial reason = %q, want the transition fence", denied.Reason)
	}

	// Ordinary role zones are unaffected by the bypass fence.
	ordinary := LeshyRouteIntent{Zone: "one", Prefix: "203.0.113.89/32", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), ordinary); err != nil {
		t.Fatalf("ordinary zone Ensure during the bypass switch: %v", err)
	}

	close(hook.gate)
	if err := <-switchDone; err != nil {
		t.Fatalf("SetBypassMode(two) after the fence probes: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.88/32"); got != "" {
		t.Fatalf("the old owner record survived the drain: %q", got)
	}
	if len(fake.removes) == 0 {
		t.Fatal("the switch never drained the old owner rows")
	}
	// After the commit the new owner is routable again.
	result, err := manager.selectedRoutes.Ensure(context.Background(), bypassIntent)
	if err != nil {
		t.Fatalf("Ensure after the switch: %v", err)
	}
	if result.Role != "two" {
		t.Fatalf("post-switch intent resolved to %q, want two", result.Role)
	}
}

func TestBypassSwitchRollbackRestoresOldPolicyAndClearsFence(t *testing.T) {
	manager, _ := managedBypassManager(t)
	connectBypassRoles(t, manager, "one", "two")
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	// First reload = the switch commit (fails); second reload = the rollback
	// of the old policy (succeeds).
	var calls atomic.Int64
	manager.reloadHook = func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("leshy restart failed")
		}
		return nil
	}

	if err := manager.SetBypassMode("two"); err == nil {
		t.Fatal("the switch must fail when its reload fails")
	}
	if got := manager.BypassMode(); got != "one" {
		t.Fatalf("cached owner = %q after the rollback, want one", got)
	}
	if got := config.ReadBypassMode(manager.bypassConfigPath); got != "one" {
		t.Fatalf("bypass file = %q after the rollback, want one", got)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range zones {
		if zone.Name == config.BypassList {
			want := config.RoleZoneTarget(manager.leshyPublicationDirectory(), "one")
			if zone.RouteTarget != want {
				t.Fatalf("restored catch-all target = %s, want the old owner %s", zone.RouteTarget, want)
			}
		}
	}
	// The fence is cleared only after the old policy is restored: the old
	// owner is routable again.
	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.91/32", Source: LeshyRouteDNS}
	result, err := manager.selectedRoutes.Ensure(context.Background(), intent)
	if err != nil {
		t.Fatalf("bypass Ensure after the rollback: %v", err)
	}
	if result.Role != "one" {
		t.Fatalf("post-rollback intent resolved to %q, want the old owner one", result.Role)
	}
	if manager.bypassTransitionActive() {
		t.Fatal("the transition fence survived the completed rollback")
	}
}

func TestBypassSwitchRollbackReloadFailureStillRestoresOldState(t *testing.T) {
	manager, _ := managedBypassManager(t)
	connectBypassRoles(t, manager, "one", "two")
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	// The reload hook is installed after the initial commit, so every call it
	// sees fails — the switch reload and the rollback reload alike: the disk
	// and memory state still return to the old owner and the error joins both
	// failures.
	manager.reloadHook = func(context.Context) error {
		return errors.New("leshy restart failed")
	}

	err := manager.SetBypassMode("two")
	if err == nil {
		t.Fatal("the switch must fail when its reload fails")
	}
	if !strings.Contains(err.Error(), "restore the Leshy reload") {
		t.Fatalf("the joined rollback failure is not visible: %v", err)
	}
	if got := manager.BypassMode(); got != "one" {
		t.Fatalf("cached owner = %q after the failed rollback, want one", got)
	}
	if got := config.ReadBypassMode(manager.bypassConfigPath); got != "one" {
		t.Fatalf("bypass file = %q after the failed rollback, want one", got)
	}
}

// The drain during the switch is the internal explicit-owner flush: it must
// retire exactly the old owner bypass records even while external flushes of
// the catch-all zone are fenced.
func TestFlushBypassOwnerRetiresOnlyTheExplicitOwner(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := newTestSelectedManager(t, readyRuntime("primary", true, true), fake)
	manager.SetBypassOwner(func() BypassOwnerDecision { return BypassOwnerDecision{Role: "primary", Available: true} })

	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.92/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	ordinary := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.93/32", Source: LeshyRouteDNS}
	if _, err := manager.Ensure(context.Background(), ordinary); err != nil {
		t.Fatal(err)
	}

	if err := manager.FlushBypassOwner(context.Background(), "primary"); err != nil {
		t.Fatalf("FlushBypassOwner: %v", err)
	}
	if got := manager.registrySnapshotState("primary", "203.0.113.92/32"); got != "" {
		t.Fatalf("bypass record survived the explicit-owner drain: %q", got)
	}
	if got := manager.registrySnapshotState("primary", "203.0.113.93/32"); got == "" {
		t.Fatal("the explicit-owner drain retired an ordinary zone record")
	}
	if err := manager.FlushBypassOwner(context.Background(), ""); err == nil {
		t.Fatal("a drain without an explicit owner must be refused")
	}
}

// PC8A §1.1 (2026-11-17 post-X1 audit): the active profile bypass invariant
// is unconditional — an empty active profile is legal only when the global
// default route is direct.

func TestSetProfileRolesRejectsEmptyActiveProfileWithBypassOwner(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.routingDir = filepath.Join(t.TempDir(), "domains")
	manager.reloadHook = func(context.Context) error { return nil }

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("SetBypassMode(one): %v", err)
	}
	before := manager.BypassMode()

	// Emptying the live profile would leave the bypass owner undesired while
	// bypass.conf still names it: rejected, nothing changes.
	if err := manager.SetProfileRoles("work", []string{}); err == nil {
		t.Fatal("emptying the active profile with a non-direct bypass owner must be rejected")
	}
	if got := manager.BypassMode(); got != before {
		t.Fatalf("bypass mode moved despite the rejection: %q -> %q", before, got)
	}
	manager.mu.Lock()
	_, workExists := manager.profileToRoles["work"]
	members := append([]string(nil), manager.profileToRoles["work"]...)
	manager.mu.Unlock()
	if !workExists || len(members) != 1 || members[0] != "one" {
		t.Fatalf("active profile changed despite the rejection: %v", members)
	}
}

func TestSetProfileRolesAllowsEmptyActiveProfileWhenBypassDirect(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.routingDir = filepath.Join(t.TempDir(), "domains")
	manager.reloadHook = func(context.Context) error { return nil }

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	// With a direct default route an empty active profile is a legal
	// everything-off state.
	if err := manager.SetProfileRoles("work", []string{}); err != nil {
		t.Fatalf("empty active profile with bypass=direct must be allowed: %v", err)
	}
	manager.mu.Lock()
	members := append([]string(nil), manager.profileToRoles["work"]...)
	manager.mu.Unlock()
	if len(members) != 0 {
		t.Fatalf("active profile did not become empty: %v", members)
	}
}

func TestSetProfileRolesEmptyInactiveProfileDoesNotAffectRunningComposition(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.routingDir = filepath.Join(t.TempDir(), "domains")
	manager.reloadHook = func(context.Context) error { return nil }
	connectBypassRoles(t, manager, "one")

	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("SetBypassMode(one): %v", err)
	}
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	// "work" is not the live profile: emptying it must not touch the running
	// composition, and the unconditional invariant must not over-fire.
	if err := manager.SetProfileRoles("work", []string{}); err != nil {
		t.Fatalf("emptying an inactive profile must be allowed: %v", err)
	}
	waitRoleState(t, manager, "one", true, true)
}

// PC8A §1.9 (2026-11-17 post-X1 audit): a live-policy rollback that cannot be
// proven quarantines the policy authority — route_authority_ready stays false
// with a stable error until an explicit reproof or a proving restart.

func TestBypassRollbackFailureQuarantinesAuthority(t *testing.T) {
	manager, _ := managedBypassManager(t)
	connectBypassRoles(t, manager, "one", "two")
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	// The switch reload fails; the rollback must restore bypass.conf but its
	// reload step also fails, which is the unproved-rollback condition.
	var calls int
	manager.reloadHook = func(context.Context) error {
		calls++
		return errors.New("leshy restart failed")
	}
	err := manager.SetBypassMode("two")
	if err == nil {
		t.Fatal("the switch must fail when its reload fails")
	}
	if !strings.Contains(err.Error(), "restore the Leshy reload") {
		t.Fatalf("the joined rollback failure is not visible: %v", err)
	}

	manager.mu.Lock()
	quarantined := manager.policyAuthorityError != ""
	reason := manager.policyAuthorityError
	ready := manager.routeAuthorityReadyLocked()
	manager.mu.Unlock()
	if !quarantined {
		t.Fatal("an unproved rollback must quarantine the policy authority")
	}
	if !strings.Contains(reason, "unproved") {
		t.Fatalf("quarantine reason: %q", reason)
	}
	if ready {
		t.Fatal("route_authority_ready must be false while quarantined")
	}

	// The quarantine is stable: a later ordinary successful mutation does not
	// clear it, and external bypass intents stay fenced.
	if err := manager.SetBypassMode("one"); err == nil {
		t.Fatal("mutating policy while quarantined must still be refused by the fence")
	}
	manager.mu.Lock()
	stillQuarantined := manager.policyAuthorityError != ""
	manager.mu.Unlock()
	if !stillQuarantined {
		t.Fatal("the quarantine must survive unrelated command activity")
	}

	// Explicit reproof with working reloads clears the quarantine.
	manager.reloadHook = func(context.Context) error { return nil }
	if err := manager.ReprovePolicyAuthority(context.Background()); err != nil {
		t.Fatalf("ReprovePolicyAuthority: %v", err)
	}
	manager.mu.Lock()
	ready = manager.routeAuthorityReadyLocked()
	manager.mu.Unlock()
	if !ready {
		t.Fatal("the explicit reproof must clear the quarantine")
	}
}

// PC8A §1.2: a pre-existing unreadable policy file fails BEFORE any mutation.
func TestSetRoutingListUnreadablePriorFileFailsBeforeMutation(t *testing.T) {
	manager := hermeticPolicyManager(t)
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(manager.routingDirectory(), "one.txt")
	if _, err := os.ReadFile(listPath); err != nil {
		t.Fatal(err)
	}

	// Make the prior file unreadable: a directory at the path is a portable
	// read error on every platform.
	if err := os.Remove(listPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(listPath, 0o750); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Remove(listPath); err == nil {
			return
		}
	}()

	manager.reloadHook = func(context.Context) error { return nil }
	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, Method: "SetRoutingList", List: "one", Content: "example.net\n",
	})
	if response.OK {
		t.Fatal("an unreadable prior policy file must fail the command before any mutation")
	}
	if info, statErr := os.Stat(listPath); statErr != nil || !info.IsDir() {
		t.Fatalf("the broken prior state must be preserved untouched: %v", statErr)
	}
}

// PC8A §1.5: a zone-less custom role cannot become the default route in
// managed mode — the catch-all zone would be denied by requireManagedRole.
func TestSetBypassModeRejectsZonelessRoleInManagedMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "zoneless.toml")
	content := `name = "zoneless"
protocol = "openconnect"
interface = "kkzoneless"
mtu = 1380
state_dir = "` + filepath.ToSlash(filepath.Join(dir, "state", "zoneless")) + `"
address = ["10.205.0.1/24"]
leshy_zone = ""

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
password_file = "` + filepath.ToSlash(filepath.Join(dir, "pw")) + `"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pw"), []byte("pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The explicitly-zoned role: leshy_zone = "normal" set in the TOML.
	normalPath := filepath.Join(dir, "normal.toml")
	normalContent := `name = "normal"
protocol = "openconnect"
interface = "kknormal"
mtu = 1380
state_dir = "` + filepath.ToSlash(filepath.Join(dir, "state", "normal")) + `"
address = ["10.206.0.1/24"]
leshy_zone = "normal"

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
password_file = "` + filepath.ToSlash(filepath.Join(dir, "pw")) + `"
`
	if err := os.WriteFile(normalPath, []byte(normalContent), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager([]string{cfgPath, normalPath}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.conf")
	manager.leshyConfigPath = filepath.Join(dir, "leshy", "config.toml")
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.reloadHook = func(context.Context) error { return nil }
	// Managed mode with the zone-less role authorized as a schema2 role. The
	// committed-state dir is required by the regenerate preflight (PC8A §1.3).
	manager.SetStateDir(filepath.Join(dir, "corestate"))
	activateManagedRoleForTest(manager, "zoneless")
	activateManagedRoleForTest(manager, "normal")
	connectBypassRoles(t, manager, "zoneless", "normal")

	if err := manager.SetBypassMode("zoneless"); err == nil {
		t.Fatal("a zone-less role must not become the default route in managed mode")
	}
	// The explicit zone role is accepted.
	if err := manager.SetBypassMode("normal"); err != nil {
		t.Fatalf("a role with an explicit unique zone must be accepted: %v", err)
	}
}

// connectBypassRoles marks the named roles desired (R8B §2.7): the runtime
// default-route mutation requires a connected target role. The fake launcher
// keeps this hermetic.
func connectBypassRoles(t *testing.T, manager *Manager, roles ...string) {
	t.Helper()
	for _, role := range roles {
		if err := manager.ConnectRole(context.Background(), role); err != nil {
			t.Fatalf("ConnectRole(%s): %v", role, err)
		}
	}
}

// R8B §2.6 (2026-11-17 residual audit): disconnecting the live global
// default-route owner is rejected fail-closed.
func TestDisconnectRoleRejectsCurrentBypassOwner(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.routingDir = filepath.Join(t.TempDir(), "domains")
	manager.reloadHook = func(context.Context) error { return nil }
	connectBypassRoles(t, manager, "one", "two")

	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("SetBypassMode(one): %v", err)
	}
	if err := manager.DisconnectRole("one"); err == nil {
		t.Fatal("disconnecting the current bypass owner must be rejected")
	}
	// The rejected disconnect changed nothing: the role stays desired/running
	// and the policy still names it.
	waitRoleState(t, manager, "one", true, true)
	if got := manager.BypassMode(); got != "one" {
		t.Fatalf("bypass mode moved despite the rejection: %q", got)
	}

	// Moving the default route to direct clears the fence: disconnect works.
	if err := manager.SetBypassMode(config.BypassDirect); err != nil {
		t.Fatal(err)
	}
	if err := manager.DisconnectRole("one"); err != nil {
		t.Fatalf("disconnect after the default moved to direct: %v", err)
	}
	waitRoleState(t, manager, "one", false, false)
}

// R8B §2.7: a disconnected role cannot become the default route.
func TestSetBypassModeRejectsDisconnectedRole(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	manager.bypassConfigPath = filepath.Join(t.TempDir(), "bypass.conf")
	manager.leshyConfigPath = filepath.Join(t.TempDir(), "config.toml")
	manager.routingDir = filepath.Join(t.TempDir(), "domains")
	manager.reloadHook = func(context.Context) error { return nil }
	connectBypassRoles(t, manager, "one")
	// two stays disconnected.

	if err := manager.SetBypassMode("two"); err == nil {
		t.Fatal("a disconnected role must not become the default route")
	}
	if got := config.ReadBypassMode(manager.bypassConfigPath); got != config.BypassDirect {
		t.Fatalf("the rejected mode reached the bypass file: %q", got)
	}
	// Connect two: now accepted.
	connectBypassRoles(t, manager, "two")
	if err := manager.SetBypassMode("two"); err != nil {
		t.Fatalf("SetBypassMode(two) after connect: %v", err)
	}
}

// R8B §2.9: manual per-role connect is an override only inside the active profile.
func TestConnectRoleRejectsRoleOutsideActiveProfile(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")
	// The default profile contains every role; switch to work (only two).
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "two", true, true)

	// one is not in work: ConnectRole(one) must be rejected without
	// creating desired state.
	if err := manager.DisconnectRole("two"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "one"); err == nil {
		t.Fatal("ConnectRole outside the active profile must be rejected")
	}
	manager.mu.Lock()
	enabled := manager.roles["one"].enabled
	manager.mu.Unlock()
	if enabled {
		t.Fatal("the rejected connect left the role desired")
	}
}
