package control

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
)

// These tests pin the 2026-11-16 PROD1 contract: a profile or membership
// mutation is a latest-intent operation. A settlement goroutine that belongs to
// an older intent must never start or stop a process that the newest intent
// already owns.

func newProfileTestManager(t *testing.T, launcher Launcher, roleNames ...string) *Manager {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, 0, len(roleNames))
	for _, name := range roleNames {
		paths = append(paths, writeConfig(t, dir, name, "openconnect"))
	}
	manager, err := NewManager(paths, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func createWorkProfile(t *testing.T, manager *Manager, roles ...string) {
	t.Helper()
	result := manager.Handle(context.Background(), Request{
		Version: 2, Method: "CreateProfile", Profile: "work", Roles: roles,
	})
	if !result.OK {
		t.Fatalf("CreateProfile(work): %s", result.Error)
	}
}

func roleFlags(manager *Manager, name string) (enabled, hasProcess bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	r, ok := manager.roles[name]
	if !ok {
		return false, false
	}
	return r.enabled, r.process != nil
}

func waitRoleState(t *testing.T, manager *Manager, name string, wantEnabled, wantProcess bool) {
	t.Helper()
	deadline := time.Now().Add(testWaitBudget)
	for {
		enabled, hasProcess := roleFlags(manager, name)
		if enabled == wantEnabled && hasProcess == wantProcess {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("role %s settled at enabled=%v process=%v, want enabled=%v process=%v",
				name, enabled, hasProcess, wantEnabled, wantProcess)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestRapidProfileSwitchLatestIntentWins reproduces the confirmed P0: a switch
// work -> default issued immediately after default -> work used to leave the
// re-enabled role desired but processless because the stale switch goroutine
// stopped it after the new start had already no-opped on the old process.
func TestRapidProfileSwitchLatestIntentWins(t *testing.T) {
	// The defect is a scheduler race: with several Ps the stale stop may run
	// before the new start and the test would only fail intermittently. One P
	// makes the interleaving deterministic on every machine.
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")

	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	for attempt := 0; attempt < 25; attempt++ {
		if err := manager.SetActiveProfile("work"); err != nil {
			t.Fatalf("SetActiveProfile(work) attempt %d: %v", attempt, err)
		}
		if err := manager.SetActiveProfile("default"); err != nil {
			t.Fatalf("SetActiveProfile(default) attempt %d: %v", attempt, err)
		}
	}

	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)
}

// TestRapidProfileSwitchABA keeps the common role untouched and brings the
// departing/rejoining role back exactly once.
func TestRapidProfileSwitchABA(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")

	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	manager.mu.Lock()
	commonBefore := manager.roles["two"].process
	manager.mu.Unlock()

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatal(err)
	}

	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	manager.mu.Lock()
	commonAfter := manager.roles["two"].process
	manager.mu.Unlock()
	if commonBefore == nil || commonAfter != commonBefore {
		t.Fatalf("common role two was churned by A->B->A: before=%p after=%p", commonBefore, commonAfter)
	}
}

// blockingLauncher blocks the first Start of a role until the test releases the
// gate, so a profile switch can be issued while a start is in flight.
type blockingLauncher struct {
	mu        sync.Mutex
	started   []string
	gates     map[string]chan struct{}
	processes map[string][]*countingProcess
}

type countingProcess struct {
	once    sync.Once
	stopped atomic.Bool
	done    chan error
}

func (p *countingProcess) Wait() error { return <-p.done }

func (p *countingProcess) Stop() error {
	p.once.Do(func() {
		p.stopped.Store(true)
		p.done <- nil
	})
	return nil
}

func newBlockingLauncher() *blockingLauncher {
	return &blockingLauncher{
		gates:     make(map[string]chan struct{}),
		processes: make(map[string][]*countingProcess),
	}
}

func (l *blockingLauncher) startRole(role string) <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	gate := make(chan struct{})
	l.gates[role] = gate
	return gate
}

func (l *blockingLauncher) release(role string) {
	l.mu.Lock()
	gate := l.gates[role]
	delete(l.gates, role)
	l.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

func (l *blockingLauncher) Start(_ context.Context, path string) (Process, error) {
	role := strings.TrimSuffix(filepath.Base(path), ".toml")
	l.mu.Lock()
	l.started = append(l.started, role)
	gate := l.gates[role]
	l.mu.Unlock()
	if gate != nil {
		<-gate
	}
	p := &countingProcess{done: make(chan error, 1)}
	l.mu.Lock()
	l.processes[role] = append(l.processes[role], p)
	l.mu.Unlock()
	return p, nil
}

func (l *blockingLauncher) startCount(role string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, started := range l.started {
		if started == role {
			count++
		}
	}
	return count
}

func (l *blockingLauncher) liveProcesses(role string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	live := 0
	for _, p := range l.processes[role] {
		if !p.stopped.Load() {
			live++
		}
	}
	return live
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(testWaitBudget)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestRapidProfileSwitchDuringStart issues a profile change while the role's
// start is still blocked in the launcher. The newest intent wins, and exactly
// one live process survives.
func TestRapidProfileSwitchDuringStart(t *testing.T) {
	launcher := newBlockingLauncher()
	manager := newProfileTestManager(t, launcher, "one", "two")
	createWorkProfile(t, manager, "two")

	launcher.startRole("one")
	connectErr := make(chan error, 1)
	go func() { connectErr <- manager.ConnectRole(context.Background(), "one") }()
	waitFor(t, "the initial start of one to enter the launcher", func() bool {
		return launcher.startCount("one") == 1
	})

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatal(err)
	}
	// The rejoin keeps the in-flight start owned by the newest intent instead
	// of launching a second process for the same role.
	launcher.release("one")

	if err := <-connectErr; err != nil {
		t.Fatalf("ConnectRole: %v", err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitFor(t, "exactly one live process for one", func() bool {
		return launcher.liveProcesses("one") == 1
	})
}

// recoveryGateDriver models a recovery-era start prerequisite that is not
// available yet: ApplyEndpoint fails transiently until the test opens the gate.
type recoveryGateDriver struct {
	releaseRecordingDriver
	blocked atomic.Bool
}

func (d *recoveryGateDriver) ApplyEndpoint(context.Context, string) error {
	if d.blocked.Load() {
		return core.ErrUnderlayPathUnavailable
	}
	return nil
}

// TestRapidProfileSwitchDuringRecovery issues profile changes while the role is
// waiting for a transient start prerequisite and then settles the retry. The
// role must converge once the prerequisite appears.
func TestRapidProfileSwitchDuringRecovery(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")

	driver := &recoveryGateDriver{}
	driver.blocked.Store(true)
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatalf("ConnectRole with a transient prerequisite: %v", err)
	}
	waitFor(t, "one to wait for its start prerequisite", func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		r := manager.roles["one"]
		return r != nil && r.enabled && r.startPending
	})

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatal(err)
	}
	driver.blocked.Store(false)
	// Kick the pending retry instead of waiting for the first backoff window.
	if err := manager.RetryRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
}

// TestStaleProfileTeardownDoesNotReleaseReenabledRoleResources proves that a
// stale settlement neither stops nor tears down a role the newest intent has
// already re-enabled.
func TestStaleProfileTeardownDoesNotReleaseReenabledRoleResources(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")

	driver := &releaseRecordingDriver{}
	manager.SetRecoveryDriver(driver)

	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if containsRole(driver.releasedRoles(), "one") {
			t.Fatalf("stale profile teardown released the re-enabled role: %v", driver.releasedRoles())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

// TestStaleSettlementFenceRejectsOldEpoch pins the primitive itself: a
// settlement created before the newest profile mutation may neither stop nor
// start a role, even when the role's own flags happen to match.
func TestAuthorizerResolvesSyntheticBypassZone(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	activation := "0123456789abcdef0123456789abcdef"
	manager.mu.Lock()
	manager.managedSocket = "/run/kk-prod2-test.sock"
	manager.managedActivationID = activation
	manager.modeGeneration = 3
	manager.managedSelectedRoles = map[string]bool{"one": true, "two": true}
	manager.bypassMode = "two"
	manager.bypassModeLoaded = true
	manager.mu.Unlock()

	intent := &LeshyRouteIntent{
		Zone: config.BypassList, Prefix: "203.0.113.40/32",
		ActivationID: activation, ModeGeneration: 3,
	}
	if err := manager.authorizeLeshyRouteIntent(intent); err != nil {
		t.Fatalf("the synthetic bypass zone must authorize against the live owner: %v", err)
	}

	manager.mu.Lock()
	manager.bypassMode = config.BypassDirect
	manager.mu.Unlock()
	if err := manager.authorizeLeshyRouteIntent(intent); err == nil {
		t.Fatal("a direct default route has no managed owner and must be denied")
	}
}

func TestStaleSettlementFenceRejectsOldEpoch(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "two")
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	manager.mu.Lock()
	staleEpoch := manager.profileSwitchEpoch
	manager.mu.Unlock()

	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", false, false)
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)

	if err := manager.stopRoleProcessForProfile("one", staleEpoch); !errors.Is(err, errStaleProfileSettlement) {
		t.Fatalf("stale stop returned %v, want errStaleProfileSettlement", err)
	}
	if err := manager.startRoleProcessForProfile(context.Background(), "one", staleEpoch); !errors.Is(err, errStaleProfileSettlement) {
		t.Fatalf("stale start returned %v, want errStaleProfileSettlement", err)
	}
	waitRoleState(t, manager, "one", true, true)
}

// The AUD1 tests below pin the policyMu serialization contract (2026-11-16
// post-e7ec9c2 audit §2.5): concurrent profile and membership mutations commit
// one at a time, and the final state equals a serial order of the calls.

// diskMembershipMatchesMemory verifies the audit's central invariant after a
// concurrent storm: every role's on-disk membership equals the in-memory one,
// which is what a serialized transaction guarantees.
func diskMembershipMatchesMemory(t *testing.T, manager *Manager) bool {
	t.Helper()
	manager.mu.Lock()
	want := make(map[string][]string, len(manager.roles))
	for name, r := range manager.roles {
		want[name] = append([]string(nil), r.cfg.Profiles...)
	}
	manager.mu.Unlock()
	for name, profiles := range want {
		path := filepath.Join(manager.configDir, name+".toml")
		cfg, err := config.Load(path)
		if err != nil {
			t.Logf("load %s: %v", path, err)
			return false
		}
		if !slices.Equal(cfg.Profiles, profiles) {
			t.Logf("role %s: disk %v != memory %v", name, cfg.Profiles, profiles)
			return false
		}
	}
	return true
}

func desiredFlagsMatchActiveProfile(t *testing.T, manager *Manager) bool {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	want := manager.activeProfileRolesLocked()
	for name, r := range manager.roles {
		if r.enabled != want[name] {
			t.Logf("role %s enabled=%v, active profile wants %v", name, r.enabled, want[name])
			return false
		}
	}
	return true
}

func TestConcurrentSetActiveProfileAndSetTunnelProfilesSerialize(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	createWorkProfile(t, manager, "one", "two")
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 25; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if err := manager.SetActiveProfile("work"); err != nil {
					errs <- fmt.Errorf("SetActiveProfile(work): %w", err)
				}
			} else {
				if err := manager.SetActiveProfile("default"); err != nil {
					errs <- fmt.Errorf("SetActiveProfile(default): %w", err)
				}
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if _, err := manager.SetTunnelProfiles("two", []string{"default", "work"}); err != nil {
					errs <- fmt.Errorf("SetTunnelProfiles(two, [default work]): %w", err)
				}
			} else {
				if _, err := manager.SetTunnelProfiles("two", []string{"default"}); err != nil {
					errs <- fmt.Errorf("SetTunnelProfiles(two, [default]): %w", err)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// The newest intent wins: the membership of every role settles to the last
	// call's value, disk and memory agree, and the desired flags match the
	// active profile.
	waitFor(t, "disk membership to match memory", func() bool {
		return diskMembershipMatchesMemory(t, manager)
	})
	waitFor(t, "desired flags to match the active profile", func() bool {
		return desiredFlagsMatchActiveProfile(t, manager)
	})
}

func TestConcurrentSetProfileRolesAndSetBypassModeStayConsistent(t *testing.T) {
	manager := newProfileTestManager(t, &fakeLauncher{}, "one", "two")
	// The storm must never touch a real Leshy service or the production
	// config/bypass paths: everything Leshy-related is redirected into the
	// test's temporary root.
	root := t.TempDir()
	manager.routingDir = filepath.Join(root, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(root, "leshy", "bypass.conf")
	manager.leshyConfigPath = filepath.Join(root, "leshy", "config.toml")
	manager.reloadHook = func(context.Context) error { return nil }
	createWorkProfile(t, manager, "one", "two")
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatal(err)
	}
	waitRoleState(t, manager, "one", true, true)
	waitRoleState(t, manager, "two", true, true)

	// An observer samples the committed pair (bypass owner, active profile)
	// while the storm runs: no committed state may have a bypass owner that
	// the active profile excludes (AUD1 §2.5).
	stop := make(chan struct{})
	violations := make(chan string, 8)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			manager.mu.Lock()
			owner, hasOwner := manager.bypassOwnerLocked()
			active := manager.activeProfile
			members := append([]string(nil), manager.profileToRoles[active]...)
			manager.mu.Unlock()
			if hasOwner && !slices.Contains(members, owner) {
				select {
				case violations <- fmt.Sprintf("bypass owner %q not in active profile %q (%v)", owner, active, members):
				default:
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer close(stop)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				// Selecting two is rejected whenever a concurrently committed
				// membership edit left it outside the active profile: the
				// invariant holds fail-closed, so that rejection is expected.
				if err := manager.SetBypassMode("two"); err != nil && !strings.Contains(err.Error(), "not enabled by the active profile") {
					errs <- fmt.Errorf("SetBypassMode(two): %w", err)
				}
			} else {
				if err := manager.SetBypassMode(config.BypassDirect); err != nil {
					errs <- fmt.Errorf("SetBypassMode(direct): %w", err)
				}
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				// Dropping two from the active profile is rejected while two
				// owns the default route; the error must be exactly that
				// validation, never a half-applied composition.
				if err := manager.SetProfileRoles("work", []string{"one"}); err != nil && !strings.Contains(err.Error(), "default route") {
					errs <- fmt.Errorf("SetProfileRoles(work, [one]): %w", err)
				}
			} else {
				if err := manager.SetProfileRoles("work", []string{"one", "two"}); err != nil {
					errs <- fmt.Errorf("SetProfileRoles(work, [one two]): %w", err)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(violations)
	for violation := range violations {
		t.Fatal(violation)
	}

	// Final committed state is one serialized order: bypass owner is direct or
	// a member of the active profile, and disk equals memory.
	manager.mu.Lock()
	owner, hasOwner := manager.bypassOwnerLocked()
	active := manager.activeProfile
	members := append([]string(nil), manager.profileToRoles[active]...)
	manager.mu.Unlock()
	if hasOwner && !slices.Contains(members, owner) {
		t.Fatalf("final state: bypass owner %q not in active profile %q (%v)", owner, active, members)
	}
	if !diskMembershipMatchesMemory(t, manager) {
		t.Fatal("final disk membership diverged from memory")
	}
}
