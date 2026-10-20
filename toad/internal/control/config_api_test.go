package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func TestHandleCreateTunnelAndProfile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "one", "openconnect")
	manager, err := NewManager([]string{path}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	response := manager.Handle(context.Background(), Request{
		Version: 2,
		Method:  "CreateTunnel",
		Tunnel: &config.TunnelSpec{
			Name:         "lab-oc",
			Protocol:     "openconnect",
			Profile:      "lab",
			Gateway:      "https://vpn.example",
			Username:     "lab",
			PasswordFile: filepath.Join(dir, "oc.password"),
		},
	})
	if !response.OK {
		t.Fatalf("CreateTunnel failed: %s", response.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab-oc.toml")); err != nil {
		t.Fatalf("tunnel file was not written: %v", err)
	}

	response = manager.Handle(context.Background(), Request{
		Version: 2,
		Method:  "CreateProfile",
		Profile: "second",
	})
	if !response.OK {
		t.Fatalf("CreateProfile failed: %s", response.Error)
	}
	if response.Message == "" {
		t.Fatal("CreateProfile did not report what changed")
	}
	loaded, err := config.Load(filepath.Join(dir, "lab-oc.toml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, profile := range loaded.Profiles {
		if profile == "second" {
			found = true
		}
	}
	if !found {
		t.Fatalf("profile not added, profiles = %v", loaded.Profiles)
	}

	if bad := manager.Handle(context.Background(), Request{Version: 2, Method: "CreateTunnel"}); bad.OK {
		t.Fatal("CreateTunnel without a spec succeeded")
	}
	if bad := manager.Handle(context.Background(), Request{Version: 2, Method: "CreateTunnel", Tunnel: &config.TunnelSpec{Name: "x"}}); bad.OK {
		t.Fatal("CreateTunnel with an empty spec succeeded")
	}

	moved := manager.Handle(context.Background(), Request{
		Version:  2,
		Method:   "SetTunnelProfiles",
		Role:     "lab-oc",
		Profiles: []string{"moved"},
	})
	if !moved.OK {
		t.Fatalf("SetTunnelProfiles failed: %s", moved.Error)
	}
	after, err := config.Load(filepath.Join(dir, "lab-oc.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Profiles) != 1 || after.Profiles[0] != "moved" {
		t.Fatalf("profiles = %v, want [moved]", after.Profiles)
	}
	if bad := manager.Handle(context.Background(), Request{Version: 2, Method: "SetTunnelProfiles", Role: "lab-oc"}); bad.OK {
		t.Fatal("SetTunnelProfiles with an empty membership succeeded")
	}

	removed := manager.Handle(context.Background(), Request{Version: 2, Method: "RemoveTunnel", Role: "lab-oc"})
	if !removed.OK {
		t.Fatalf("RemoveTunnel failed: %s", removed.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab-oc.toml")); !os.IsNotExist(err) {
		t.Fatalf("tunnel file survived removal: %v", err)
	}
}
