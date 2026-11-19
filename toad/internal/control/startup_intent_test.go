package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// R8B §2.8 permanent rows (2026-11-17 residual audit): the startup
// desired/default policy validation, the all-off dormancy decision and the
// pre-reconciler synthetic-bypass drain.

// allOffStartupManager seeds the same strict policy caches startup loads
// before the intent preparation runs, so the focused tests exercise the
// exact production sequence without a state dir.
func allOffStartupManager(t *testing.T) (*Manager, *selectedRoutesFake) {
	t.Helper()
	manager, fake := managedBypassManager(t)
	if err := config.WriteBypassMode(manager.bypassConfigPath, "one"); err != nil {
		t.Fatalf("seed bypass.conf: %v", err)
	}
	// StartupManagedAuthority loads the strict caches before the intent
	// preparation runs; mirror that load here.
	manager.mu.Lock()
	manager.bypassMode = "one"
	manager.bypassModeLoaded = true
	manager.mu.Unlock()
	return manager, fake
}

func TestValidateStartupDesiredPolicyAllOffKeepsOwnerDormant(t *testing.T) {
	manager, _ := allOffStartupManager(t)
	// Every configured role desired=false: explicit all-off. The configured
	// owner stays dormant — validation must accept it (R8B §2.8 row 1).
	desired := PersistedDesiredState{ActiveProfile: "default", Roles: map[string]bool{"one": false, "two": false}}
	if err := manager.ValidateStartupDesiredPolicy(desired); err != nil {
		t.Fatalf("all-off with a configured owner must validate: %v", err)
	}
	// Missing desired intent (nil Roles, the missing-desired.json synthesis):
	// the same safe all-off (R8B §2.8 row 2).
	if err := manager.ValidateStartupDesiredPolicy(PersistedDesiredState{ActiveProfile: "default"}); err != nil {
		t.Fatalf("missing desired intent must validate as all-off: %v", err)
	}
	// A bypass owner desired=false while a sibling stays desired IS a
	// contradiction (R8B §2.8 row 4).
	partial := PersistedDesiredState{ActiveProfile: "default", Roles: map[string]bool{"one": false, "two": true}}
	if err := manager.ValidateStartupDesiredPolicy(partial); err == nil {
		t.Fatal("the bypass owner desired=false beside a desired sibling must fail startup")
	} else if !strings.Contains(err.Error(), "contradicts the operator intent") {
		t.Fatalf("unexpected contradiction error: %v", err)
	}
}

func TestPrepareStartupRoutingIntentAllOffFlipsEffectiveDirect(t *testing.T) {
	manager, _ := allOffStartupManager(t)
	desired := PersistedDesiredState{ActiveProfile: "default", Roles: map[string]bool{"one": false, "two": false}}
	if err := manager.PrepareStartupRoutingIntent(desired); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	manager.mu.Lock()
	effective := manager.bypassMode
	configured := manager.configuredBypassOwner
	drain := manager.startupBypassDrainOwner
	status := manager.desiredStateStatus
	manager.mu.Unlock()
	if effective != config.BypassDirect {
		t.Fatalf("the effective bypass mode = %q, want direct", effective)
	}
	if configured != "one" {
		t.Fatalf("the configured owner was not retained dormant: %q", configured)
	}
	if drain != "one" {
		t.Fatalf("the drain owner = %q, want one", drain)
	}
	if !strings.Contains(status, "dormant") {
		t.Fatalf("the status must report the dormancy: %q", status)
	}
	// The operator configuration is never rewritten (R8B §2.8: dormant, not
	// deleted).
	mode, err := config.ReadBypassModeStrict(manager.bypassConfigPath)
	if err != nil {
		t.Fatalf("read back bypass.conf: %v", err)
	}
	if mode != "one" {
		t.Fatalf("bypass.conf was rewritten: %q", mode)
	}
	// A non-all-off intent must not flip anything.
	manager2, _ := allOffStartupManager(t)
	desiredOn := PersistedDesiredState{ActiveProfile: "default", Roles: map[string]bool{"one": true}}
	if err := manager2.PrepareStartupRoutingIntent(desiredOn); err != nil {
		t.Fatalf("prepare (owner desired): %v", err)
	}
	manager2.mu.Lock()
	defer manager2.mu.Unlock()
	if manager2.bypassMode != "one" {
		t.Fatalf("a desired owner must keep the configured mode: %q", manager2.bypassMode)
	}
}

func TestCompleteStartupBypassDrainRetiresRestoredEvidence(t *testing.T) {
	manager, fake := allOffStartupManager(t)
	// Restore-shaped state: the owner's synthetic bypass evidence is in the
	// registry (the checkpoint restore put it there in production).
	manager.SetManagedSelectedRole("one", true)
	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.40/32", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), intent); err != nil {
		t.Fatalf("seed the restored bypass evidence: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.40/32"); got != routing.SelectedActive {
		t.Fatalf("pre-drain record = %q, want active", got)
	}
	// The all-off decision and the committed managed descriptor the drain
	// re-renders against (StartupManagedAuthority retains it in production).
	desired := PersistedDesiredState{ActiveProfile: "default", Roles: map[string]bool{"one": false, "two": false}}
	if err := manager.PrepareStartupRoutingIntent(desired); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// A managed render requires the core state directory; the fixture manager
	// starts local.
	manager.SetStateDir(t.TempDir())
	manager.mu.Lock()
	manager.managedSocket = filepath.Join(manager.stateDir, "core.sock")
	manager.managedActivationID = "0123456789abcdef0123456789abcdef"
	manager.modeGeneration = 1
	manager.startupCommitted = committedManagedState{
		Managed: true,
		Marker: &managedModeMarker{
			Socket:         manager.managedSocket,
			ActivationID:   "0123456789abcdef0123456789abcdef",
			ModeGeneration: 1,
		},
	}
	manager.mu.Unlock()

	if err := manager.CompleteStartupBypassDrain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if snapshot := manager.selectedRoutes.RegistrySnapshot("one"); len(snapshot) != 0 {
		t.Fatalf("the synthetic bypass evidence survived the drain: %#v", snapshot)
	}
	if len(fake.removes) == 0 {
		t.Fatal("the drain never removed the kernel row")
	}
	// The drain resolved the decision: no second drain, no dormant fence.
	manager.mu.Lock()
	drain := manager.startupBypassDrainOwner
	transition := manager.bypassTransition
	quarantine := manager.policyAuthorityError
	effective := manager.bypassMode
	manager.mu.Unlock()
	if drain != "" {
		t.Fatalf("the drain owner stayed armed: %q", drain)
	}
	if transition.Active {
		t.Fatalf("the bypass fence survived the drain: %#v", transition)
	}
	if quarantine != "" {
		t.Fatalf("the policy authority stayed quarantined: %q", quarantine)
	}
	if effective != config.BypassDirect {
		t.Fatalf("the effective bypass mode = %q, want direct", effective)
	}
	// bypass.conf still carries the operator's decision.
	mode, err := config.ReadBypassModeStrict(manager.bypassConfigPath)
	if err != nil {
		t.Fatalf("read back bypass.conf: %v", err)
	}
	if mode != "one" {
		t.Fatalf("bypass.conf was rewritten by the drain: %q", mode)
	}
}
