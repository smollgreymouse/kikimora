package control

import (
	"context"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
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
