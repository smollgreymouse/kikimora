package control

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// retryKickDriver records that a recovery transaction actually ran after the operator
// pressed retry: the observed path ends at endpoint reconciliation on the second call.
type retryKickDriver struct {
	applyCalls int
	applied    chan struct{}
}

func (d *retryKickDriver) ObserveRoutes(context.Context, string) error { return nil }
func (d *retryKickDriver) Park(context.Context, string) error          { return nil }
func (d *retryKickDriver) Withdraw(context.Context, string) error      { return nil }
func (d *retryKickDriver) Quiesce(context.Context, string) error       { return nil }
func (d *retryKickDriver) Rebind(context.Context, string) error        { return nil }
func (d *retryKickDriver) StartTransport(context.Context, string) error {
	return nil
}
func (d *retryKickDriver) Validate(context.Context, string) error    { return nil }
func (d *retryKickDriver) Publish(context.Context, string) error     { return nil }
func (d *retryKickDriver) ResyncLeshy(context.Context, string) error { return nil }
func (d *retryKickDriver) ObserveRestoration(context.Context, string) error {
	return nil
}
func (d *retryKickDriver) ApplyEndpoint(context.Context, string) error {
	d.applyCalls++
	if d.applyCalls == 1 {
		// The first transaction dies in the endpoint-pending state — the orphan
		// the retry must be able to kick.
		return core.ErrEndpointResolutionPending
	}
	select {
	case <-d.applied:
	default:
		close(d.applied)
	}
	return nil
}
func (d *retryKickDriver) ReleaseResources(context.Context, string) error { return nil }

// TestRetryRoleKicksOrphanedRecoveringTransaction pins the drawer retry contract from the
// 2026-10-26 UI audit (and the win10 incident of 2026-10-25): a Recovering role whose
// transaction died in the endpoint-pending state has a live process, so the pre-fix
// RetryRole returned ok and did nothing — the operator pressed retry and nothing happened.
// The retry must kick the recovery transaction immediately, without waiting for a
// material underlay epoch, and the role must reach Ready through the normal pipeline.
func TestRetryRoleKicksOrphanedRecoveringTransaction(t *testing.T) {
	dir, err := os.MkdirTemp("", "rkk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}},
		Reason:   netstate.ChangeInitial,
	})

	ready := toadctl.Snapshot{
		Generation:    10,
		Revision:      1,
		State:         "online",
		RouteReady:    true,
		InterfaceName: "kkone",
		IfIndex:       7,
		MTU:           1380,
		Addresses:     []string{"10.80.0.253/24"},
	}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, ready); !live || !accepted {
		t.Fatalf("ready snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	token, ok := manager.product.BeginValidation("one")
	if !ok {
		t.Fatal("ready role could not begin validation")
	}
	if !manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"}) {
		t.Fatal("ready role validation did not commit")
	}

	// Reproduce the orphan: recovery dies in the endpoint-pending state, which keeps the
	// role Recovering with no armed retry and no in-flight worker.
	driver := &retryKickDriver{applied: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = []string{"fe80::1/64"}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	deadline := time.Now().Add(testWaitBudget)
	for {
		role, ok := manager.product.Role("one")
		if ok && role.State == core.RoleRecovering && role.Recovery.LastError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery never reported the endpoint-pending orphan: %#v", role)
		}
		time.Sleep(5 * time.Millisecond)
	}
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if inFlight {
		t.Fatal("test premise: recovery worker is still in flight")
	}
	if err := manager.RetryRole(context.Background(), "one"); err != nil {
		t.Fatalf("RetryRole: %v", err)
	}

	select {
	case <-driver.applied:
	case <-time.After(testWaitBudget):
		t.Fatal("retry did not kick a recovery transaction: endpoint reconciliation never re-ran")
	}
	deadline = time.Now().Add(testWaitBudget)
	for time.Now().Before(deadline) {
		if role, ok := manager.product.Role("one"); ok && role.State == core.RoleReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	role, _ := manager.product.Role("one")
	t.Fatalf("kicked recovery did not restore Ready: %#v", role)
}

// TestRetryRoleReadyIsQuietNoOp guards the other side: a Ready role has nothing to
// retry, and the action must not disturb the pipeline.
func TestRetryRoleReadyIsQuietNoOp(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}},
		Reason:   netstate.ChangeInitial,
	})
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, toadctl.Snapshot{
		Generation: 10, Revision: 1, State: "online", RouteReady: true,
		InterfaceName: "kkone", IfIndex: 7, MTU: 1380,
		Addresses: []string{"10.80.0.253/24"},
	}); !live || !accepted {
		t.Fatalf("ready snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	token, ok := manager.product.BeginValidation("one")
	if !ok {
		t.Fatal("ready role could not begin validation")
	}
	if !manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"}) {
		t.Fatal("ready role validation did not commit")
	}

	driver := &releaseRecordingDriver{}
	manager.SetRecoveryDriver(driver)
	if err := manager.RetryRole(context.Background(), "one"); err != nil {
		t.Fatalf("RetryRole on Ready: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if len(driver.releasedRoles()) != 0 {
		t.Fatalf("Ready retry released resources: %v", driver.releasedRoles())
	}
	if role, ok := manager.product.Role("one"); !ok || role.State != core.RoleReady {
		t.Fatalf("Ready retry disturbed the role: %#v", role)
	}
}
