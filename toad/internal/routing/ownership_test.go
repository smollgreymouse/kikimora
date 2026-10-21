package routing

import (
	"net/netip"
	"testing"
)

func TestMemoryOwnershipCopiesAndScopesRoutes(t *testing.T) {
	registry := NewMemoryOwnership()
	route := SelectedRouteOwner{Role: "primary", Interface: "kk0", IfIndex: 7, Prefix: netip.MustParsePrefix("192.0.2.10/32")}
	input := []SelectedRouteOwner{route}
	registry.Replace("primary", input)
	input[0].IfIndex = 99

	got := registry.Snapshot("primary")
	if len(got) != 1 || got[0].IfIndex != 7 {
		t.Fatalf("registry did not own an independent copy: %#v", got)
	}
	got[0].IfIndex = 88
	if again := registry.Snapshot("primary"); len(again) != 1 || again[0].IfIndex != 7 {
		t.Fatalf("snapshot mutation leaked into registry: %#v", again)
	}
	if other := registry.Snapshot("secondary"); len(other) != 0 {
		t.Fatalf("role ownership leaked: %#v", other)
	}
	registry.Remove("primary")
	if got := registry.Snapshot("primary"); len(got) != 0 {
		t.Fatalf("remove kept ownership: %#v", got)
	}
}
