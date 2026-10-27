package control

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
)

// releaseRecordingDriver records which roles the control plane tore down.
type releaseRecordingDriver struct {
	mu       sync.Mutex
	released []string
}

func (d *releaseRecordingDriver) ObserveRoutes(context.Context, string) error { return nil }
func (d *releaseRecordingDriver) Park(context.Context, string) error          { return nil }
func (d *releaseRecordingDriver) Withdraw(context.Context, string) error      { return nil }
func (d *releaseRecordingDriver) Quiesce(context.Context, string) error       { return nil }
func (d *releaseRecordingDriver) ApplyEndpoint(context.Context, string) error { return nil }
func (d *releaseRecordingDriver) Rebind(context.Context, string) error        { return nil }
func (d *releaseRecordingDriver) StartTransport(context.Context, string) error {
	return nil
}
func (d *releaseRecordingDriver) Validate(context.Context, string) error    { return nil }
func (d *releaseRecordingDriver) Publish(context.Context, string) error     { return nil }
func (d *releaseRecordingDriver) ResyncLeshy(context.Context, string) error { return nil }
func (d *releaseRecordingDriver) ObserveRestoration(context.Context, string) error {
	return nil
}
func (d *releaseRecordingDriver) ReleaseResources(_ context.Context, role string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.released = append(d.released, role)
	return nil
}

func (d *releaseRecordingDriver) releasedRoles() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.released)
}

// TestSetActiveProfileReleasesDepartingRoles pins the profile-switch teardown contract
// from the 2026-10-26 UI audit: a role that leaves the active profile is stopped and its
// owned resources (Leshy publication, endpoint policy, parks, ownership checkpoint) are
// released exactly like an explicit disconnect. The pre-fix switch only stopped the
// process, so the departed role's route target kept pointing at a dead tunnel.
func TestSetActiveProfileReleasesDepartingRoles(t *testing.T) {
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

	driver := &releaseRecordingDriver{}
	manager.SetRecoveryDriver(driver)

	created := manager.Handle(context.Background(), Request{
		Version: 2, Method: "CreateProfile", Profile: "work", Roles: []string{"two"},
	})
	if !created.OK {
		t.Fatalf("CreateProfile(work): %s", created.Error)
	}

	// one is running when the operator switches to a profile it does not belong to.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActiveProfile("work"); err != nil {
		t.Fatalf("SetActiveProfile(work): %v", err)
	}

	deadline := time.Now().Add(testWaitBudget)
	for {
		if slices.Contains(driver.releasedRoles(), "one") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("departing role one was never released; released=%v", driver.releasedRoles())
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, role := range driver.releasedRoles() {
		if role == "two" {
			t.Fatal("the joining role two must not be released by the switch")
		}
	}

	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		want := role.ID == "two"
		if role.DesiredEnabled != want {
			t.Fatalf("role %s desired = %v after the switch, want %v", role.ID, role.DesiredEnabled, want)
		}
		if want && role.State != string(core.RoleStarting) {
			t.Fatalf("joining role two state = %q, want Starting", role.State)
		}
	}
}
