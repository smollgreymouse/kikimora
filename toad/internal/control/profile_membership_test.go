package control

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
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

// The tests below pin the AUD1 contract (2026-11-16 post-e7ec9c2 audit §2.5):
// a failed profile or membership API call leaves disk, memory and desired
// state observationally unchanged, and no settlement is scheduled.

func readFileState(t *testing.T, path string) ([]byte, os.FileMode, bool) {
	t.Helper()
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, 0, false
	}
	if statErr != nil {
		t.Fatalf("stat %s: %v", path, statErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data, info.Mode().Perm(), true
}

func TestSetTunnelProfilesDesiredStoreFailureChangesNothing(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	dir := manager.configDir
	onePath := filepath.Join(dir, "one.toml")
	beforeBytes, beforeMode, _ := readFileState(t, onePath)

	manager.mu.Lock()
	beforeEpoch := manager.profileSwitchEpoch
	beforeProfiles := append([]string(nil), manager.roles["one"].cfg.Profiles...)
	beforeWork := append([]string(nil), manager.profileToRoles["work"]...)
	beforeOneProcess := manager.roles["one"].process
	manager.mu.Unlock()

	manager.SetDesiredStateStore(&failingDesiredStateStore{})
	if _, err := manager.SetTunnelProfiles("one", []string{"default"}); err == nil {
		t.Fatal("SetTunnelProfiles succeeded despite a failing desired-state store")
	}

	afterBytes, afterMode, _ := readFileState(t, onePath)
	if !bytes.Equal(beforeBytes, afterBytes) || beforeMode != afterMode {
		t.Fatalf("one.toml changed despite the failed call: mode %v -> %v", beforeMode, afterMode)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !slices.Equal(beforeProfiles, manager.roles["one"].cfg.Profiles) {
		t.Fatalf("in-memory membership changed: %v -> %v", beforeProfiles, manager.roles["one"].cfg.Profiles)
	}
	if !slices.Equal(beforeWork, manager.profileToRoles["work"]) {
		t.Fatalf("profile map changed: %v -> %v", beforeWork, manager.profileToRoles["work"])
	}
	if !manager.roles["one"].enabled || !manager.roles["two"].enabled {
		t.Fatalf("desired flags changed: one=%v two=%v", manager.roles["one"].enabled, manager.roles["two"].enabled)
	}
	if manager.profileSwitchEpoch != beforeEpoch {
		t.Fatalf("settlement epoch advanced despite the failed call: %d -> %d", beforeEpoch, manager.profileSwitchEpoch)
	}
	if manager.roles["one"].process != beforeOneProcess {
		t.Fatal("the failed call churned the running role process")
	}
}

func TestSetProfileRolesDesiredStoreFailureChangesNothing(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	dir := manager.configDir
	oneBytes, oneMode, _ := readFileState(t, filepath.Join(dir, "one.toml"))
	twoBytes, twoMode, _ := readFileState(t, filepath.Join(dir, "two.toml"))

	manager.mu.Lock()
	beforeEpoch := manager.profileSwitchEpoch
	beforeOne := append([]string(nil), manager.roles["one"].cfg.Profiles...)
	beforeTwo := append([]string(nil), manager.roles["two"].cfg.Profiles...)
	beforeWork := append([]string(nil), manager.profileToRoles["work"]...)
	manager.mu.Unlock()

	manager.SetDesiredStateStore(&failingDesiredStateStore{})
	if err := manager.SetProfileRoles("work", []string{"two"}); err == nil {
		t.Fatal("SetProfileRoles succeeded despite a failing desired-state store")
	}

	afterOneBytes, afterOneMode, _ := readFileState(t, filepath.Join(dir, "one.toml"))
	afterTwoBytes, afterTwoMode, _ := readFileState(t, filepath.Join(dir, "two.toml"))
	if !bytes.Equal(oneBytes, afterOneBytes) || oneMode != afterOneMode {
		t.Fatal("one.toml changed despite the failed call")
	}
	if !bytes.Equal(twoBytes, afterTwoBytes) || twoMode != afterTwoMode {
		t.Fatal("two.toml changed despite the failed call")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !slices.Equal(beforeOne, manager.roles["one"].cfg.Profiles) ||
		!slices.Equal(beforeTwo, manager.roles["two"].cfg.Profiles) {
		t.Fatalf("in-memory membership changed: one=%v two=%v",
			manager.roles["one"].cfg.Profiles, manager.roles["two"].cfg.Profiles)
	}
	if !slices.Equal(beforeWork, manager.profileToRoles["work"]) {
		t.Fatalf("profile map changed: %v -> %v", beforeWork, manager.profileToRoles["work"])
	}
	if manager.profileSwitchEpoch != beforeEpoch {
		t.Fatal("settlement epoch advanced despite the failed call")
	}
	if !manager.roles["one"].enabled {
		t.Fatal("desired flag of the still-active member changed")
	}
}

func TestCreateProfileInvalidMembershipLeavesNoGhost(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	dir := manager.configDir
	registryPath := filepath.Join(dir, "profiles.list")
	_, _, registryExisted := readFileState(t, registryPath)

	manager.mu.Lock()
	beforeRoles := map[string][]string{}
	for name, members := range manager.profileToRoles {
		beforeRoles[name] = append([]string(nil), members...)
	}
	manager.mu.Unlock()

	result := manager.Handle(context.Background(), Request{
		Version: 2, Method: "CreateProfile", Profile: "broken", Roles: []string{"missing"},
	})
	if result.OK {
		t.Fatal("CreateProfile with an unknown role succeeded")
	}

	_, _, existed := readFileState(t, registryPath)
	if existed != registryExisted {
		t.Fatalf("profiles.list existence changed: %v -> %v", registryExisted, existed)
	}
	if existed {
		names, err := config.ReadDeclaredProfiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(names, "broken") {
			t.Fatalf("profiles.list kept the ghost profile: %v", names)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if slices.Contains(manager.declaredProfiles, "broken") {
		t.Fatalf("declaredProfiles kept the ghost: %v", manager.declaredProfiles)
	}
	if _, ok := manager.profileToRoles["broken"]; ok {
		t.Fatalf("profileToRoles kept the ghost profile: %v", manager.profileToRoles)
	}
	for name, members := range beforeRoles {
		if !slices.Equal(members, manager.profileToRoles[name]) {
			t.Fatalf("profile %q changed: %v -> %v", name, members, manager.profileToRoles[name])
		}
	}
}

func TestCreateProfileMidCommitFailureLeavesNoGhost(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	dir := manager.configDir
	registryPath := filepath.Join(dir, "profiles.list")
	_, _, registryExisted := readFileState(t, registryPath)

	// The membership write for one fails after the registry was prepared, so
	// the whole transaction (including the registry) must roll back.
	onePath := filepath.Join(dir, "one.toml")
	if err := os.Chmod(onePath, 0o400); err != nil {
		t.Skipf("cannot make %s read-only: %v", onePath, err)
	}
	defer os.Chmod(onePath, 0o600)
	beforeBytes, beforeMode, _ := readFileState(t, onePath)

	if _, err := manager.CreateProfile("ghost", []string{"one"}); err == nil {
		t.Fatal("CreateProfile succeeded despite an unwritable role config")
	}

	_, _, existed := readFileState(t, registryPath)
	if existed != registryExisted {
		t.Fatalf("profiles.list existence changed: %v -> %v", registryExisted, existed)
	}
	afterBytes, afterMode, _ := readFileState(t, onePath)
	if !bytes.Equal(beforeBytes, afterBytes) || beforeMode != afterMode {
		t.Fatalf("one.toml changed despite the failed CreateProfile: mode %v -> %v", beforeMode, afterMode)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if slices.Contains(manager.declaredProfiles, "ghost") {
		t.Fatalf("declaredProfiles kept the ghost: %v", manager.declaredProfiles)
	}
	if _, ok := manager.profileToRoles["ghost"]; ok {
		t.Fatalf("profileToRoles kept the ghost: %v", manager.profileToRoles)
	}
}

func TestCreateProfileValidEmptyProfileCommits(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	dir := manager.configDir

	added, err := manager.CreateProfile("empty", nil)
	if err != nil {
		t.Fatalf("CreateProfile(empty): %v", err)
	}
	if added != 1 {
		t.Fatalf("CreateProfile reported added=%d, want 1", added)
	}
	names, err := config.ReadDeclaredProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "empty") {
		t.Fatalf("profiles.list is missing the declared profile: %v", names)
	}
	manager.mu.Lock()
	if !slices.Contains(manager.declaredProfiles, "empty") {
		manager.mu.Unlock()
		t.Fatalf("declaredProfiles is missing the profile: %v", manager.declaredProfiles)
	}
	members, ok := manager.profileToRoles["empty"]
	if !ok {
		manager.mu.Unlock()
		t.Fatalf("profileToRoles does not expose the empty profile: %v", manager.profileToRoles)
	}
	if len(members) != 0 {
		manager.mu.Unlock()
		t.Fatalf("empty profile has members: %v", members)
	}
	manager.mu.Unlock()

	// The declared profile is switchable right away.
	if err := manager.SetActiveProfile("empty"); err != nil {
		t.Fatalf("SetActiveProfile(empty): %v", err)
	}
}

func TestCreateProfileExistingProfileKeepsRegistry(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one")
	dir := manager.configDir
	registryPath := filepath.Join(dir, "profiles.list")
	beforeBytes, beforeMode, existed := readFileState(t, registryPath)

	// Re-creating an existing profile with a valid membership edit must not
	// duplicate or corrupt the registry.
	added, err := manager.CreateProfile("work", []string{"one", "two"})
	if err != nil {
		t.Fatalf("CreateProfile(work) again: %v", err)
	}
	if added != 0 {
		t.Fatalf("re-creating an existing profile reported added=%d, want 0", added)
	}
	afterBytes, afterMode, afterExisted := readFileState(t, registryPath)
	if existed != afterExisted || beforeMode != afterMode || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("registry changed despite no new declaration: existed %v -> %v", existed, afterExisted)
	}
}
