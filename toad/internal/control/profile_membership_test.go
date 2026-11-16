package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// These tests pin the second half of PROD1: editing the membership of the
// active profile is a composition change and must reconcile desired state and
// processes exactly like a profile switch (2026-11-16 audit §2.1).

func configProfiles(t *testing.T, path string) []string {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return cfg.Profiles
}

func TestActiveProfileMembershipRemovalStopsRole(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	driver := &releaseRecordingDriver{}
	manager.SetRecoveryDriver(driver)

	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	if err := manager.SetProfileRoles("work", []string{"one"}); err != nil {
		t.Fatalf("SetProfileRoles(work, [one]): %v", err)
	}
	waitRoleState(t, manager, "two", false, false)
	waitRoleState(t, manager, "one", true, true)
	waitFor(t, "two resources to be released", func() bool {
		return containsRole(driver.releasedRoles(), "two")
	})
}

func TestActiveProfileMembershipAdditionStartsRole(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", false, false)

	if err := manager.SetProfileRoles("work", []string{"one", "two"}); err != nil {
		t.Fatalf("SetProfileRoles(work, [one two]): %v", err)
	}
	waitRoleState(t, manager, "two", true, true)
}

func TestInactiveProfileMembershipEditLeavesProcesses(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	manager.mu.Lock()
	before := manager.roles["one"].process
	manager.mu.Unlock()

	// work is not the active profile, so dropping one from it changes no
	// desired flag and no process.
	if err := manager.SetProfileRoles("work", nil); err != nil {
		t.Fatalf("SetProfileRoles(work, []): %v", err)
	}
	manager.mu.Lock()
	after := manager.roles["one"].process
	manager.mu.Unlock()
	if before == nil || after != before {
		t.Fatalf("inactive profile edit churned the running role: before=%p after=%p", before, after)
	}
	waitRoleState(t, manager, "one", true, true)
}

func TestSetTunnelProfilesActiveMembershipReconciles(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	// Moving one out of the live profile must stop it without a restart.
	if _, err := manager.SetTunnelProfiles("one", []string{"default"}); err != nil {
		t.Fatalf("SetTunnelProfiles(one, [default]): %v", err)
	}
	waitRoleState(t, manager, "one", false, false)

	// Moving it back in must start it again.
	if _, err := manager.SetTunnelProfiles("one", []string{"default", "work"}); err != nil {
		t.Fatalf("SetTunnelProfiles(one, [default work]): %v", err)
	}
	waitRoleState(t, manager, "one", true, true)
}

func TestSetProfileRolesMidWriteFailureRestoresFilesAndMemory(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	dir := manager.configDir
	twoPath := filepath.Join(dir, "two.toml")

	// Changes are written in role order (one, then two). Making the second
	// file read-only forces the failure after one.toml was already replaced.
	if err := os.Chmod(twoPath, 0o400); err != nil {
		t.Skipf("cannot make %s read-only: %v", twoPath, err)
	}
	defer os.Chmod(twoPath, 0o600)

	err := manager.SetProfileRoles("work", []string{"two"})
	if err == nil {
		t.Fatal("SetProfileRoles succeeded despite an unwritable role config")
	}

	if profiles := configProfiles(t, filepath.Join(dir, "one.toml")); !containsRole(profiles, "work") {
		t.Fatalf("one.toml was not restored after the failed transaction: %v", profiles)
	}
	manager.mu.Lock()
	oneProfiles := append([]string(nil), manager.roles["one"].cfg.Profiles...)
	oneEnabled := manager.roles["one"].enabled
	workMembers := append([]string(nil), manager.profileToRoles["work"]...)
	manager.mu.Unlock()
	if !containsRole(oneProfiles, "work") {
		t.Fatalf("in-memory membership changed despite the failed transaction: %v", oneProfiles)
	}
	if len(workMembers) != 1 || workMembers[0] != "one" {
		t.Fatalf("profile map changed despite the failed transaction: %v", workMembers)
	}
	if oneEnabled {
		t.Fatal("the failed transaction enabled a role it never reached")
	}
}

func TestRapidMembershipEditsObeyNewestEpoch(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", false, false)

	for attempt := 0; attempt < 10; attempt++ {
		if err := manager.SetProfileRoles("work", []string{"two"}); err != nil {
			t.Fatalf("edit to [two] attempt %d: %v", attempt, err)
		}
		if err := manager.SetProfileRoles("work", []string{"one"}); err != nil {
			t.Fatalf("edit to [one] attempt %d: %v", attempt, err)
		}
	}

	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", false, false)
}
