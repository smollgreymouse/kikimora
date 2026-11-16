package control

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
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
