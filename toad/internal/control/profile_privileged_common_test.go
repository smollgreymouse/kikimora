//go:build privileged

package control

import (
	"testing"
)

func runProfileSwitchingPrivileged(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	paths := []string{
		writeProfileConfig(t, dir, "work", []string{"work"}),
		writeProfileConfig(t, dir, "home", []string{"home"}),
		writeProfileConfig(t, dir, "both", []string{"work", "home"}),
	}
	manager, err := NewManager(paths, launcher, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	profiles := manager.Profiles()
	wantProfiles := []string{"default", "home", "work"}
	if !testingEquals(profiles, wantProfiles) {
		t.Fatalf("Profiles: want %v, got %v", wantProfiles, profiles)
	}

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatalf("SetActiveProfile work: %v", err)
	}
	workSnap := manager.Snapshot()
	if workSnap.ActiveProfile != "work" {
		t.Fatalf("active profile: want work, got %q", workSnap.ActiveProfile)
	}
	assertRoleEnabled(t, workSnap, "work", true)
	assertRoleEnabled(t, workSnap, "both", true)
	assertRoleEnabled(t, workSnap, "home", false)

	if err := manager.SetActiveProfile("home"); err != nil {
		t.Fatalf("SetActiveProfile home: %v", err)
	}
	homeSnap := manager.Snapshot()
	assertRoleEnabled(t, homeSnap, "work", false)
	assertRoleEnabled(t, homeSnap, "both", true)
	assertRoleEnabled(t, homeSnap, "home", true)

	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatalf("SetActiveProfile default: %v", err)
	}
	defaultSnap := manager.Snapshot()
	assertRoleEnabled(t, defaultSnap, "work", true)
	assertRoleEnabled(t, defaultSnap, "both", true)
	assertRoleEnabled(t, defaultSnap, "home", true)
}

func testingEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
