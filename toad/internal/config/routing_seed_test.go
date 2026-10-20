package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedRoutingDefaultsWritesMissingListsOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	if err := SeedRoutingDefaults(dir); err != nil {
		t.Fatalf("SeedRoutingDefaults: %v", err)
	}
	for _, list := range []RoutingList{RoutingPrimary, RoutingSecondary, RoutingBypass} {
		content, err := ReadRoutingList(dir, list)
		if err != nil || content == "" {
			t.Fatalf("%s not seeded: %v", list, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "primary.txt"), []byte("operator.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedRoutingDefaults(dir); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	content, err := ReadRoutingList(dir, RoutingPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if content != "operator.example\n" {
		t.Fatalf("seeding overwrote the operator list: %q", content)
	}
}
