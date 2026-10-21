package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedRole(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+".toml")
	content := `name = "` + name + `"
protocol = "openconnect"
interface = "kk-` + name + `"

[openconnect]
gateway = "https://vpn.example/"
username = "operator"
password_file = "/etc/kikimora/credentials/` + name + `.secret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetLeshyZone(t *testing.T) {
	dir := t.TempDir()
	path := seedRole(t, dir, "oc-main")

	if err := SetLeshyZone(dir, "oc-main", "secondary"); err != nil {
		t.Fatalf("SetLeshyZone: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `leshy_zone = "secondary"`) {
		t.Fatalf("zone not written:\n%s", text)
	}
	// The rest of the role configuration must survive the rewrite.
	if !strings.Contains(text, "https://vpn.example/") || !strings.Contains(text, "/etc/kikimora/credentials/oc-main.secret") {
		t.Fatalf("tunnel settings lost:\n%s", text)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestSetLeshyZoneRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	seedRole(t, dir, "oc-main")

	if err := SetLeshyZone(dir, "oc-main", "bypass"); err == nil {
		t.Fatal("bypass accepted as a role zone")
	}
	if err := SetLeshyZone(dir, "oc-main", "bad zone"); err == nil {
		t.Fatal("invalid zone accepted")
	}
	if err := SetLeshyZone(dir, "absent", "primary"); err == nil {
		t.Fatal("missing role accepted")
	}
}

func TestSetLeshyZoneCanDetach(t *testing.T) {
	dir := t.TempDir()
	path := seedRole(t, dir, "oc-main")

	if err := SetLeshyZone(dir, "oc-main", "primary"); err != nil {
		t.Fatal(err)
	}
	if err := SetLeshyZone(dir, "oc-main", ""); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "leshy_zone") {
		t.Fatalf("zone still present:\n%s", data)
	}
}
