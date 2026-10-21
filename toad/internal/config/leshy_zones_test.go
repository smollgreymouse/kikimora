package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLeshyZones(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[server]
listen_address = "127.0.0.1:53053"

[[zones]]
name = "primary"
mode = "inclusive"
route_type = "dev"
route_target = "/run/kikimora/leshy/vpn/awg.dev"
domains = ["chatgpt.com"]

[[zones]]
name = "secondary"
mode = "inclusive"
route_target = "/run/kikimora/leshy/vpn/xray.dev"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	zones, err := ReadLeshyZones(path)
	if err != nil {
		t.Fatalf("ReadLeshyZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("zones = %d, want 2", len(zones))
	}
	if zones[0].Name != "primary" || zones[0].RouteTarget != "/run/kikimora/leshy/vpn/awg.dev" {
		t.Fatalf("primary zone = %#v", zones[0])
	}
	if zones[1].Name != "secondary" || zones[1].RouteTarget == "" {
		t.Fatalf("secondary zone = %#v", zones[1])
	}
}

func TestReadLeshyZonesMissingIsEmpty(t *testing.T) {
	zones, err := ReadLeshyZones(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing config returned an error: %v", err)
	}
	if len(zones) != 0 {
		t.Fatalf("missing config returned %#v", zones)
	}
}
