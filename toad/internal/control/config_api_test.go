package control

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	// A new profile is an empty set: it must not be cloned into every role.
	if slices.Contains(loaded.Profiles, "second") {
		t.Fatalf("new profile leaked into a role config: %v", loaded.Profiles)
	}
	// Regression: the UI creates a profile and expects it in the list right away.
	if !slices.Contains(manager.Profiles(), "second") {
		t.Fatalf("running core does not list the new profile: %v", manager.Profiles())
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

// TestCreateProfileAppearsInTheRunningCore is the regression for the operator's path:
// create a profile in the UI, see it in the list, fill it with roles. A profile is only a
// membership label, so the running core must apply it without a restart; only a new tunnel
// needs one.
func TestCreateProfileAppearsInTheRunningCore(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "one", "openconnect"),
		writeConfig(t, dir, "two", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	if slices.Contains(manager.Snapshot().Profiles, "new") {
		t.Fatalf("profile already present before creation: %v", manager.Snapshot().Profiles)
	}

	response := manager.Handle(context.Background(), Request{Version: 2, Method: "CreateProfile", Profile: "new"})
	if !response.OK {
		t.Fatalf("CreateProfile failed: %s", response.Error)
	}

	snapshot := manager.Snapshot()
	if !slices.Contains(snapshot.Profiles, "new") {
		t.Fatalf("snapshot does not list the new profile: %v", snapshot.Profiles)
	}
	for _, role := range snapshot.Roles {
		if slices.Contains(role.Profiles, "new") {
			t.Fatalf("an empty profile already contains %s: %v", role.ID, role.Profiles)
		}
	}

	// Filling the profile keeps every other membership of the chosen role.
	filled := manager.Handle(context.Background(), Request{
		Version: 2, Method: "SetProfileRoles", Profile: "new", Roles: []string{"one"},
	})
	if !filled.OK {
		t.Fatalf("SetProfileRoles failed: %s", filled.Error)
	}
	snapshot = manager.Snapshot()
	for _, role := range snapshot.Roles {
		switch role.ID {
		case "one":
			if !slices.Contains(role.Profiles, "new") {
				t.Fatalf("role one does not belong to the new profile: %v", role.Profiles)
			}
			if !slices.Contains(role.Profiles, "default") {
				t.Fatalf("role one lost the default membership: %v", role.Profiles)
			}
		case "two":
			if slices.Contains(role.Profiles, "new") {
				t.Fatalf("role two was not selected but belongs to the new profile: %v", role.Profiles)
			}
		}
	}
	if err := manager.SetActiveProfile("new"); err != nil {
		t.Fatalf("SetActiveProfile(new): %v", err)
	}
	if got := manager.Snapshot().ActiveProfile; got != "new" {
		t.Fatalf("active profile = %q, want new", got)
	}
}

// TestRoleBelongsToSeveralProfiles covers the explicit-set model: one role can serve more
// than one profile, and editing one profile's set must not steal the role from another.
func TestRoleBelongsToSeveralProfiles(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "one", "openconnect"),
		writeConfig(t, dir, "two", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	for _, profile := range []string{"office", "travel"} {
		response := manager.Handle(context.Background(), Request{
			Version: 2, Method: "CreateProfile", Profile: profile, Roles: []string{"one"},
		})
		if !response.OK {
			t.Fatalf("CreateProfile(%s): %s", profile, response.Error)
		}
	}
	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		if role.ID != "one" {
			continue
		}
		for _, profile := range []string{"office", "travel"} {
			if !slices.Contains(role.Profiles, profile) {
				t.Fatalf("role one lost %s: %v", profile, role.Profiles)
			}
		}
	}

	// Narrowing one profile leaves the other one intact.
	narrowed := manager.Handle(context.Background(), Request{
		Version: 2, Method: "SetProfileRoles", Profile: "travel", Roles: []string{"two"},
	})
	if !narrowed.OK {
		t.Fatalf("SetProfileRoles(travel): %s", narrowed.Error)
	}
	snapshot = manager.Snapshot()
	for _, role := range snapshot.Roles {
		switch role.ID {
		case "one":
			if !slices.Contains(role.Profiles, "office") {
				t.Fatalf("role one lost office: %v", role.Profiles)
			}
			if slices.Contains(role.Profiles, "travel") {
				t.Fatalf("role one is still in travel: %v", role.Profiles)
			}
		case "two":
			if !slices.Contains(role.Profiles, "travel") {
				t.Fatalf("role two is not in travel: %v", role.Profiles)
			}
		}
	}
}

// TestSetTunnelProfilesAppliesWithoutRestart pins the role-drawer membership path. The
// 2026-10-26 UI audit found the core only rewrote the file: the running snapshot kept the
// stale membership, so the client's optimistic write was visibly reverted on the next
// snapshot and the user's save looked cancelled. The move must apply at once, and a
// profile switch must honour the new membership immediately.
func TestSetTunnelProfilesAppliesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeConfig(t, dir, "one", "openconnect"),
		writeConfig(t, dir, "two", "openconnect"),
	}
	manager, err := NewManager(paths, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	created := manager.Handle(context.Background(), Request{Version: 2, Method: "CreateProfile", Profile: "work"})
	if !created.OK {
		t.Fatalf("CreateProfile: %s", created.Error)
	}
	moved := manager.Handle(context.Background(), Request{
		Version: 2, Method: "SetTunnelProfiles", Role: "one", Profiles: []string{"work"},
	})
	if !moved.OK {
		t.Fatalf("SetTunnelProfiles: %s", moved.Error)
	}
	// The honest message: the move is live, no restart is involved.
	if strings.Contains(moved.Message, "restart") {
		t.Fatalf("SetTunnelProfiles still demands a restart: %q", moved.Message)
	}

	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		switch role.ID {
		case "one":
			// The snapshot view always lists default first; the move must be visible
			// in it right away.
			if !slices.Equal(role.Profiles, []string{"default", "work"}) {
				t.Fatalf("role one membership = %v, want [default work]", role.Profiles)
			}
		case "two":
			if !slices.Equal(role.Profiles, []string{"default"}) {
				t.Fatalf("role two membership = %v, want [default]", role.Profiles)
			}
		}
	}
	loaded, err := config.Load(filepath.Join(dir, "one.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(loaded.Profiles, "work") {
		t.Fatalf("disk membership = %v, want work", loaded.Profiles)
	}

	// The profile switch consumes the new membership without a restart: one is wanted in
	// work, two is not.
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatalf("SetActiveProfile(work): %v", err)
	}
	snapshot = manager.Snapshot()
	for _, role := range snapshot.Roles {
		want := role.ID == "one"
		if role.DesiredEnabled != want {
			t.Fatalf("role %s desired = %v in profile work, want %v", role.ID, role.DesiredEnabled, want)
		}
	}
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatalf("SetActiveProfile(default): %v", err)
	}

	// An empty destination set would orphan the role: the move is rejected and the live
	// membership stays as it was.
	emptied := manager.Handle(context.Background(), Request{
		Version: 2, Method: "SetTunnelProfiles", Role: "one", Profiles: []string{},
	})
	if emptied.OK {
		t.Fatal("an empty membership move was accepted")
	}
	for _, role := range manager.Snapshot().Roles {
		if role.ID == "one" && !slices.Equal(role.Profiles, []string{"default", "work"}) {
			t.Fatalf("rejected move changed role one to %v, want [default work]", role.Profiles)
		}
	}
}

// TestFreshInstallCanCreateProfiles pins the independent-audit P0: a fresh
// Windows install has zero toad TOMLs, and the pre-fix manager derived an
// empty configuration directory from them, rejecting every CreateProfile /
// CreateTunnel request the tray UI sends. The service passes its toads
// directory explicitly; the core must honor it with zero loaded roles.
func TestFreshInstallCanCreateProfiles(t *testing.T) {
	dir := t.TempDir()
	// No paths at all: the fresh-install composition.
	manager, err := NewManagerWithConfigDir(dir, nil, &fakeLauncher{}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	if roles := manager.Snapshot().Roles; len(roles) != 0 {
		t.Fatalf("fresh install already has roles: %v", roles)
	}
	created := manager.Handle(context.Background(), Request{Version: 2, Method: "CreateProfile", Profile: "first"})
	if !created.OK {
		t.Fatalf("CreateProfile on a fresh install failed: %s", created.Error)
	}
	if !slices.Contains(manager.Snapshot().Profiles, "first") {
		t.Fatal("created profile is not visible in the snapshot")
	}
	// The tunnel file lands on disk too (it loads on the next start).
	staged := manager.Handle(context.Background(), Request{
		Version: 2, Method: "CreateTunnel",
		Tunnel: &config.TunnelSpec{
			Name: "lab", Protocol: "openconnect", Gateway: "https://vpn.example",
			Username: "lab", PasswordFile: filepath.Join(dir, "lab.password"),
		},
	})
	if !staged.OK {
		t.Fatalf("CreateTunnel on a fresh install failed: %s", staged.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab.toml")); err != nil {
		t.Fatalf("tunnel file was not written: %v", err)
	}
}
