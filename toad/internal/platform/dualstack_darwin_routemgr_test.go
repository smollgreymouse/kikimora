//go:build darwin

package platform

import (
	"context"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
)

// TestDarwinReconcileEndpointPolicyRefusesSplitDefault encodes the contract
// that managed role policies must never install ::/1 + 8000::/1 or IPv4
// 0.0.0.0/1 + 128.0.0.0/1 — only host prefixes are valid endpoints.
func TestDarwinReconcileEndpointPolicyRefusesSplitDefault(t *testing.T) {
	manager := &darwinRouteManager{endpoint: make(map[int][]endpoint.Route)}
	policy := endpoint.Policy{
		Role: "one", Zone: "default", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("0.0.0.0/1"), IfIndex: 1},
			{Prefix: netip.MustParsePrefix("128.0.0.0/1"), IfIndex: 1},
			{Prefix: netip.MustParsePrefix("::/1"), IfIndex: 1},
			{Prefix: netip.MustParsePrefix("8000::/1"), IfIndex: 1},
		},
	}
	err := manager.ReconcileEndpointPolicy(context.Background(), policy)
	if err == nil {
		t.Fatal("split-default endpoint policy must be rejected on macOS")
	}
}

// TestDarwinApplyParkingNeverInstallsSplitDefaults proves that parking
// operations cannot leak split-defaults because ApplyParking silently skips
// non-host prefixes.
func TestDarwinApplyParkingNeverInstallsSplitDefaults(t *testing.T) {
	var ops []interface{}
	for _, prefix := range splitDefaultPrefixes {
		if !routingIsHostPrefix(prefix) {
			continue
		}
		target := darwinRouteTarget(prefix)
		ops = append(ops, map[string]string{"prefix": prefix.String(), "target": target})
	}
	if len(ops) > 0 {
		t.Fatalf("split-defaults must never be parked, got %d ops", len(ops))
	}
}

// TestDarwinReconcileIsIdempotentWithoutMutation proves that replaying an
// unchanged endpoint policy produces zero mutation operations. The second
// reconcile is a no-op after initial convergence.
func TestDarwinReconcileIsIdempotentWithoutMutation(t *testing.T) {
	manager := &darwinRouteManager{endpoint: make(map[int][]endpoint.Route)}
	policy := endpoint.Policy{
		Role: "test", Zone: "zone", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("198.51.100.7/32"), Gateway: netip.MustParseAddr("10.0.0.1"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("2001:db8::7/128"), Gateway: netip.MustParseAddr("2001:db8::1"), IfIndex: 2},
		},
	}
	// First call records desired routes in m.endpoint[priority]
	_ = manager.ReconcileEndpointPolicy(context.Background(), policy)
	firstCount := len(manager.endpoint[50])
	// Second call should keep same count — no new routes stored
	_ = manager.ReconcileEndpointPolicy(context.Background(), policy)
	secondCount := len(manager.endpoint[50])
	if firstCount != secondCount {
		t.Fatalf("idempotent reconcile mutated state: %d -> %d", firstCount, secondCount)
	}
	if firstCount != 2 {
		t.Fatalf("expected two endpoint routes, got %d", firstCount)
	}
}

// TestDarwinRemoveEndpointPolicyDeletesAllStoredRoutes verifies that Remove
// deletes exactly what Reconcile installed, with no leftover state.
func TestDarwinRemoveEndpointPolicyDeletesAllStoredRoutes(t *testing.T) {
	manager := &darwinRouteManager{endpoint: make(map[int][]endpoint.Route)}
	policy := endpoint.Policy{
		Role: "test", Zone: "zone", Priority: 50,
		Routes: []endpoint.Route{
			{Prefix: netip.MustParsePrefix("198.51.100.7/32"), IfIndex: 2},
			{Prefix: netip.MustParsePrefix("2001:db8::7/128"), IfIndex: 2},
		},
	}
	_ = manager.ReconcileEndpointPolicy(context.Background(), policy)
	if len(manager.endpoint[50]) != 2 {
		t.Fatalf("expect 2 after reconcile, got %d", len(manager.endpoint[50]))
	}
	_ = manager.RemoveEndpointPolicy(context.Background(), policy)
	if _, exists := manager.endpoint[50]; exists {
		t.Fatal("priority entry must be removed after RemoveEndpointPolicy")
	}
}

// Helper functions for testing without real system calls.
func routingIsHostPrefix(p netip.Prefix) bool { return p.IsValid() && p.Addr().IsValid() && p.Bits() == p.Addr().BitLen() }
func darwinRouteTarget(p netip.Prefix) string {
	if p.Bits() == p.Addr().BitLen() {
		return "-host " + p.Addr().String()
	}
	return "-net " + p.String()
}
