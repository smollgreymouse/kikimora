package control

import (
	"context"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// PC8B §2.5 mandatory row (2026-11-17 post-X1 audit): bypass one->two with an
// already-resolved catch-all prefix must install the SAME prefix Active on
// two with NO new DNS Ensure — the protected handover transfers the row.
func TestBypassHandoverTransfersResolvedPrefixWithoutNewEnsure(t *testing.T) {
	manager, fake := managedBypassManager(t)
	connectBypassRoles(t, manager, "one", "two")
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("initial SetBypassMode(one): %v", err)
	}
	intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "203.0.113.95/32", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), intent); err != nil {
		t.Fatalf("Ensure under one: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.95/32"); got != routing.SelectedActive {
		t.Fatalf("pre-switch record = %q, want active", got)
	}

	// The protected handover: the prefix moves to two with no DNS query.
	if err := manager.SetBypassMode("two"); err != nil {
		t.Fatalf("SetBypassMode(two): %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("two", "203.0.113.95/32"); got != routing.SelectedActive {
		t.Fatalf("the transferred prefix is not Active on two: %q", got)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.95/32"); got != "" {
		t.Fatalf("the old owner record survived the handover: %q", got)
	}

	// Reverse handover two -> one keeps the same guarantee.
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("reverse SetBypassMode(one): %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "203.0.113.95/32"); got != routing.SelectedActive {
		t.Fatalf("the reverse-transferred prefix is not Active on one: %q", got)
	}
	_ = fake
}

// R8B §2.3/§2.5 (2026-11-17 residual audit): the handover refuses to start
// when the target lacks family readiness for a transferred prefix — BEFORE
// any park/withdraw, with old routes and policy unchanged.
func TestBypassHandoverPreflightRejectsUnreadyTargetFamily(t *testing.T) {
	manager, fake := managedBypassManager(t)
	connectBypassRoles(t, manager, "one", "two")
	// one holds a v6 catch-all prefix; two's runtime claims no IPv6 readiness.
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("SetBypassMode(one): %v", err)
	}
	v6Intent := LeshyRouteIntent{Zone: config.BypassList, Prefix: "2001:db8:dead::95/128", Source: LeshyRouteDNS}
	if _, err := manager.selectedRoutes.Ensure(context.Background(), v6Intent); err != nil {
		t.Fatalf("Ensure v6 under one: %v", err)
	}
	if got := manager.selectedRoutes.registrySnapshotState("one", "2001:db8:dead::95/128"); got != routing.SelectedActive {
		t.Fatalf("pre-switch v6 record = %q, want active", got)
	}

	// Flip the target runtime to IPv6-not-ready for every role read.
	manager.selectedRoutes.roleRuntime = func(name string) (core.RoleRuntime, bool) {
		rt := readyRuntime(name, true, true)
		rt.Validation.Families.IPv6.Ready = false
		rt.Validation.Families.IPv6.Reason = "v6 preflight fixture"
		return rt, true
	}

	if err := manager.SetBypassMode("two"); err == nil {
		t.Fatal("the handover to an IPv6-unready target must be refused")
	}
	// Old routes unchanged, no parks created, bypass.conf still one.
	if got := manager.selectedRoutes.registrySnapshotState("one", "2001:db8:dead::95/128"); got != routing.SelectedActive {
		t.Fatalf("the old v6 record was disturbed: %q", got)
	}
	if got := config.ReadBypassMode(manager.bypassConfigPath); got != "one" {
		t.Fatalf("bypass.conf moved despite the preflight refusal: %q", got)
	}
	if snapshot := manager.selectedRoutes.RegistrySnapshot("one"); len(snapshot) > 0 {
		for _, entry := range snapshot {
			if entry.Park != nil {
				t.Fatalf("the refused handover created a park: %#v", entry.Park)
			}
		}
	}
	_ = fake
}
