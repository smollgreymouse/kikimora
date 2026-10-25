package control

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// endpointPendingDriver models an endpoint reconciliation that cannot resolve
// its transport hostname yet: the first ApplyEndpoint during automatic recovery
// reports the resolution as pending, every later call succeeds.
type endpointPendingDriver struct {
	applyCalls    int
	firstApply    chan struct{}
	succeededOnce chan struct{}
}

func (d *endpointPendingDriver) ObserveRoutes(context.Context, string) error { return nil }
func (d *endpointPendingDriver) Park(context.Context, string) error          { return nil }
func (d *endpointPendingDriver) Withdraw(context.Context, string) error      { return nil }
func (d *endpointPendingDriver) Quiesce(context.Context, string) error       { return nil }
func (d *endpointPendingDriver) Rebind(context.Context, string) error        { return nil }
func (d *endpointPendingDriver) StartTransport(context.Context, string) error {
	return nil
}
func (d *endpointPendingDriver) Validate(context.Context, string) error         { return nil }
func (d *endpointPendingDriver) Publish(context.Context, string) error          { return nil }
func (d *endpointPendingDriver) ResyncLeshy(context.Context, string) error      { return nil }
func (d *endpointPendingDriver) ReleaseResources(context.Context, string) error { return nil }
func (d *endpointPendingDriver) ObserveRestoration(context.Context, string) error {
	return nil
}
func (d *endpointPendingDriver) ApplyEndpoint(context.Context, string) error {
	d.applyCalls++
	if d.applyCalls == 1 {
		select {
		case <-d.firstApply:
		default:
			close(d.firstApply)
		}
		return core.ErrEndpointResolutionPending
	}
	select {
	case <-d.succeededOnce:
	default:
		close(d.succeededOnce)
	}
	return nil
}

// validateServingToad answers the manager's Validate request over a real
// toadctl socket, like a live OpenConnect Toad whose client owns the TUN.
type validateServingToad struct {
	generation uint64
}

func (h validateServingToad) Handle(_ context.Context, req toadctl.Request) toadctl.Response {
	snapshot := &toadctl.Snapshot{
		Generation:    h.generation,
		Revision:      2,
		State:         "online",
		Reason:        "official OpenConnect data phase owns stable managed TUN",
		RouteReady:    true,
		InterfaceName: "kkone",
		IfIndex:       7,
		MTU:           1380,
		Addresses:     []string{"10.80.0.253/24"},
	}
	res := toadctl.Response{Version: toadctl.ProtocolVersion, ID: req.ID, OK: true, Snapshot: snapshot}
	if req.Method == "Validate" {
		res.Validation = &toadctl.ValidationResult{
			Healthy: true,
			State:   "ready",
			Reason:  "official OpenConnect client owns the managed TUN",
		}
	}
	return res
}
func (h validateServingToad) WaitForRevision(context.Context, uint64) (toadctl.Snapshot, error) {
	return toadctl.Snapshot{Generation: h.generation, Revision: 3, State: "online", RouteReady: true}, nil
}

// TestEndpointPendingRecoveryHandsBackOnLaterReadySnapshot reproduces the
// Windows VM freeze (2026-10-25): automatic recovery ended with
// ErrEndpointResolutionPending, which keeps the role Recovering "until the next
// material underlay epoch" — but on a stable underlay that epoch never comes,
// no retry is armed and the validation gate rejects every later RouteReady
// snapshot, so the role (and the aggregate) stays Recovering for ever. A live
// RouteReady snapshot from the current generation must hand the role back into
// validation and restore Ready.
func TestEndpointPendingRecoveryHandsBackOnLaterReadySnapshot(t *testing.T) {
	// The live control socket must stay under the AF_UNIX path limit; the
	// per-test TempDir path is longer than 108 bytes on Windows.
	dir, err := os.MkdirTemp("", "ephb")
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

	socket := filepath.Join(dir, "one", "control.sock")
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()
	go func() {
		_ = (toadctl.Server{Socket: socket, Handler: validateServingToad{generation: 10}}).Serve(serverCtx)
	}()
	deadline := time.Now().Add(testWaitBudget)
	for {
		if conn, err := net.Dial("unix", socket); err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("toad control socket not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.roles["one"].controlSocket = socket
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

	driver := &endpointPendingDriver{firstApply: make(chan struct{}), succeededOnce: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.Reason = "OpenConnect negotiated interface drift requires transport recovery"
	drift.RouteReady = false
	drift.Addresses = []string{"fe80::1/64"}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	select {
	case <-driver.firstApply:
	case <-time.After(testWaitBudget):
		t.Fatal("recovery never reached endpoint reconciliation")
	}
	role, ok := manager.product.Role("one")
	if !ok {
		t.Fatal("product role missing")
	}
	if role.State != core.RoleRecovering {
		t.Fatalf("pending endpoint resolution must keep the role recoverable, got %q", role.State)
	}
	if at := role.Recovery.NextRetryAt; at != nil && !at.IsZero() {
		t.Fatalf("test premise: an unexpected retry was armed at %v", *at)
	}

	restored := ready
	restored.Revision = drift.Revision + 1
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, restored); !live || !accepted {
		t.Fatalf("restored snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleValidation("one", process)

	deadline = time.Now().Add(testWaitBudget)
	for time.Now().Before(deadline) {
		current, ok := manager.product.Role("one")
		if ok && current.State == core.RoleReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	current, _ := manager.product.Role("one")
	manager.mu.Lock()
	retryPending := manager.recoveryRetryPending["one"]
	handoff := manager.restartHandoffPending["one"]
	manager.mu.Unlock()
	t.Fatalf("role stayed frozen in Recovering after a live RouteReady snapshot: state=%q reason=%q retryPending=%v handoff=%v",
		current.State, current.Reason, retryPending, handoff)
}

// TestDisconnectCancelsInFlightRecovery pins the UI contract: pressing
// Disconnect while a role is mid-recovery cancels the transaction. The worker
// (blockingRecoveryDriver from control_test.go, parked in StartTransport)
// must not commit its result (Ready/Recovering/Failed) over the terminal
// Stopped the disconnect published, and must leave no armed retry behind.
func TestDisconnectCancelsInFlightRecovery(t *testing.T) {
	dir, err := os.MkdirTemp("", "dcir")
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
		Generation: 10, Revision: 1, State: "online", RouteReady: true,
		InterfaceName: "kkone", IfIndex: 7, MTU: 1380,
		Addresses: []string{"10.80.0.253/24"},
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

	driver := &blockingRecoveryDriver{startTransportCalled: make(chan struct{}), release: make(chan struct{})}
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

	select {
	case <-driver.startTransportCalled:
	case <-time.After(testWaitBudget):
		t.Fatal("recovery worker never reached the transport step")
	}

	// The operator disconnects while the worker is still blocked inside
	// StartTransport.
	if err := manager.DisconnectRole("one"); err != nil {
		t.Fatalf("disconnect during in-flight recovery failed: %v", err)
	}
	if role, ok := manager.product.Role("one"); !ok || role.State != core.RoleStopped {
		t.Fatalf("disconnect did not publish Stopped: %#v", role)
	}

	// Release the worker: its transaction completes successfully on the wire,
	// but its mandate is gone.
	close(driver.release)

	deadline := time.Now().Add(testWaitBudget)
	for time.Now().Before(deadline) {
		current, ok := manager.product.Role("one")
		if !ok {
			t.Fatal("product role vanished")
		}
		if current.State != core.RoleStopped {
			t.Fatalf("cancelled recovery resurrected role state: %q (%q)", current.State, current.Reason)
		}
		manager.mu.Lock()
		inFlight := manager.roles["one"].recoveryInFlight
		retryPending := manager.recoveryRetryPending["one"]
		handoff := manager.restartHandoffPending["one"]
		manager.mu.Unlock()
		if !inFlight && !retryPending && !handoff {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	retryPending := manager.recoveryRetryPending["one"]
	handoff := manager.restartHandoffPending["one"]
	manager.mu.Unlock()
	t.Fatalf("cancelled recovery left bookkeeping behind: recoveryInFlight=%v retryPending=%v handoff=%v",
		inFlight, retryPending, handoff)
}
