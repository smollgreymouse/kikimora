package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateTunnelWritesLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	path, err := CreateTunnel(dir, TunnelSpec{
		Name:          "lab-awg",
		Protocol:      "amneziawg2",
		Profile:       "lab",
		Endpoint:      "198.51.100.10:51820",
		Address:       []string{"10.8.1.3/32"},
		PrivateKey:    "private-material",
		PeerPublicKey: "peer-material",
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("tunnel written to %s, want inside %s", path, dir)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	if cfg.Name != "lab-awg" {
		t.Fatalf("name = %q", cfg.Name)
	}
	if cfg.Protocol != ProtocolAWG2 {
		t.Fatalf("protocol = %q", cfg.Protocol)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0] != "lab" {
		t.Fatalf("profiles = %v", cfg.Profiles)
	}
	if cfg.AWG2 == nil || cfg.AWG2.Endpoint != "198.51.100.10:51820" {
		t.Fatalf("awg2 section = %#v", cfg.AWG2)
	}
}

func TestCreateTunnelValidatesTheSpec(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		spec TunnelSpec
	}{
		{"unsupported protocol", TunnelSpec{Name: "x", Protocol: "wireguard"}},
		{"bad name", TunnelSpec{Name: "bad name", Protocol: "openconnect", Gateway: "https://vpn", Username: "lab"}},
		{"awg without keys", TunnelSpec{Name: "x", Protocol: "amneziawg2", Endpoint: "h:1", Address: []string{"10.8.1.9/32"}}},
		{"vless without uuid", TunnelSpec{Name: "x", Protocol: "vless-reality", Endpoint: "h:1", Address: []string{"10.9.0.2/32"}}},
		{"openconnect without gateway", TunnelSpec{Name: "x", Protocol: "openconnect"}},
	}
	for _, testCase := range cases {
		if _, err := CreateTunnel(dir, testCase.spec); err == nil {
			t.Fatalf("%s: accepted", testCase.name)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("rejected specs left %d files behind", len(entries))
	}
}

func TestCreateTunnelRefusesDuplicates(t *testing.T) {
	dir := t.TempDir()
	spec := TunnelSpec{Name: "dup", Protocol: "openconnect", Gateway: "https://vpn.example", Username: "lab"}
	if _, err := CreateTunnel(dir, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := CreateTunnel(dir, spec); err == nil {
		t.Fatal("second create accepted a duplicate")
	}
}

func TestCreateProfileTouchesEveryConfigOnce(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if _, err := CreateTunnel(dir, TunnelSpec{
			Name:     name,
			Protocol: "openconnect",
			Gateway:  "https://vpn.example",
			Username: "lab",
			Profile:  "default",
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	changed, err := CreateProfile(dir, "second")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if changed != 2 {
		t.Fatalf("changed %d files, want 2", changed)
	}
	for _, name := range []string{"one", "two"} {
		cfg, err := Load(filepath.Join(dir, name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Profiles) != 2 {
			t.Fatalf("%s profiles = %v, want two", name, cfg.Profiles)
		}
	}

	again, err := CreateProfile(dir, "second")
	if err != nil {
		t.Fatalf("second CreateProfile: %v", err)
	}
	if again != 0 {
		t.Fatalf("idempotent run changed %d files", again)
	}

	if _, err := CreateProfile(dir, "bad name"); err == nil {
		t.Fatal("invalid profile name accepted")
	}
}
