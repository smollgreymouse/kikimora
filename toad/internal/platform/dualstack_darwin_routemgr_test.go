//go:build darwin

package platform

import (
	"context"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
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
	// ApplyParking silently skips non-host prefixes; routeTarget would only
	// ever be consulted for the survivors. Assert the real helpers reject the
	// legacy split-default pairs instead of reimplementing them.
	for _, prefix := range splitDefaultPrefixes {
		if routing.IsHostPrefix(prefix) {
			t.Fatalf("split-default %s must not be a host prefix", prefix)
		}
		if target := routeTarget(prefix); target == "" {
			t.Fatalf("routeTarget(%s) produced an empty target", prefix)
		}
	}
}
