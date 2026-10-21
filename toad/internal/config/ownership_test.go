package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnershipAllowsOnlyMonotonicCutoverCombinations(t *testing.T) {
	for _, ownership := range []Ownership{
		{RoutingOwner: "legacy", TunnelOwner: "external", EndpointOwner: "legacy"},
		{RoutingOwner: "go", TunnelOwner: "external", EndpointOwner: "go"},
		{RoutingOwner: "go", TunnelOwner: "go", EndpointOwner: "go"},
	} {
		if err := ownership.Valid(); err != nil {
			t.Fatalf("valid ownership rejected: %#v: %v", ownership, err)
		}
	}
	if err := (Ownership{RoutingOwner: "legacy", TunnelOwner: "go", EndpointOwner: "legacy"}).Valid(); err == nil {
		t.Fatal("mixed legacy routing/Go tunnel ownership accepted")
	}
}

func TestLoadOwnershipNormalizesLegacyConfigDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership.conf")
	if err := os.WriteFile(path, []byte("routing_owner = \"go\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ownership, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.RoutingOwner != "go" || ownership.TunnelOwner != "external" || ownership.EndpointOwner != "go" {
		t.Fatalf("ownership defaults were not normalized: %#v", ownership)
	}
}
