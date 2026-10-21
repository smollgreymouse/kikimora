package config

import (
	"os"
	"path/filepath"
	"testing"
)

func seedTunnel(t *testing.T, dir, name, profile string) {
	t.Helper()
	if _, err := CreateTunnel(dir, TunnelSpec{
		Name:         name,
		Protocol:     "openconnect",
		PasswordFile: filepath.Join(dir, "oc.password"),
		Gateway:      "https://vpn.example",
		Username:     "lab",
		Profile:      profile,
	}); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

func TestSetTunnelProfilesMovesARole(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	path, err := SetTunnelProfiles(dir, "lab", []string{"two", "three", "two"})
	if err != nil {
		t.Fatalf("SetTunnelProfiles: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 2 || cfg.Profiles[0] != "two" || cfg.Profiles[1] != "three" {
		t.Fatalf("profiles = %v, want [two three]", cfg.Profiles)
	}

	if _, err := SetTunnelProfile(dir, "lab", "one"); err != nil {
		t.Fatalf("SetTunnelProfile: %v", err)
	}
	moved, err := Load(filepath.Join(dir, "lab.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Profiles) != 1 || moved.Profiles[0] != "one" {
		t.Fatalf("profiles = %v, want [one]", moved.Profiles)
	}
}

func TestSetTunnelProfilesRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	cases := []struct {
		name     string
		tunnel   string
		profiles []string
	}{
		{"empty membership", "lab", nil},
		{"empty name in list", "lab", []string{""}},
		{"invalid profile", "lab", []string{"bad name"}},
		{"unknown tunnel", "missing", []string{"one"}},
		{"invalid tunnel name", "bad name", []string{"one"}},
	}
	for _, testCase := range cases {
		if _, err := SetTunnelProfiles(dir, testCase.tunnel, testCase.profiles); err == nil {
			t.Fatalf("%s: accepted", testCase.name)
		}
	}

	unchanged, err := Load(filepath.Join(dir, "lab.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Profiles) != 1 || unchanged.Profiles[0] != "one" {
		t.Fatalf("rejected calls changed the file: %v", unchanged.Profiles)
	}
}

func TestCredentialErrorRejectsTunnelsThatCannotAuthenticate(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "oc.password")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{"no section", &Config{Protocol: ProtocolOpenConnect}, true},
		{"no username", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{PasswordFile: secret}}, true},
		{"no secret source", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab"}}, true},
		{"missing file", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab", PasswordFile: filepath.Join(dir, "absent")}}, true},
		{"complete", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab", PasswordFile: secret}}, false},
		{"other protocol", &Config{Protocol: ProtocolAWG2}, false},
	}
	for _, testCase := range cases {
		err := testCase.cfg.CredentialError()
		if testCase.wantErr && err == nil {
			t.Fatalf("%s: accepted", testCase.name)
		}
		if !testCase.wantErr && err != nil {
			t.Fatalf("%s: rejected: %v", testCase.name, err)
		}
	}
}

func TestRemoveTunnelDeletesTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	if err := RemoveTunnel(dir, "lab"); err != nil {
		t.Fatalf("RemoveTunnel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab.toml")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if err := RemoveTunnel(dir, "lab"); err == nil {
		t.Fatal("second removal accepted")
	}
	if err := RemoveTunnel(dir, "bad name"); err == nil {
		t.Fatal("invalid name accepted")
	}
}
