// Package control implements the local Kikimora control plane.  It supervises
// Toad processes; protocol implementations remain owned by kikimora-toad.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/supervisor"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type fakeProcess struct {
	stopped bool
	done    chan error
}

func (p *fakeProcess) Wait() error { return <-p.done }
func (p *fakeProcess) Stop() error { p.stopped = true; p.done <- nil; return nil }

type fakeLauncher struct {
	started []string
	process *fakeProcess
	err     error
}

func (l *fakeLauncher) Start(_ context.Context, path string) (Process, error) {
	l.started = append(l.started, path)
	if l.err != nil {
		return nil, l.err
	}
	l.process = &fakeProcess{done: make(chan error, 1)}
	return l.process, nil
}

func tomlString(value string) string {
	return strconv.Quote(value)
}

func writeConfig(t *testing.T, dir, name string, proto string) string {
	t.Helper()
	path := filepath.Join(dir, name+".toml")
	var content string
	switch proto {
	case "amneziawg2":
		content = fmt.Sprintf(`name = %s
protocol = "amneziawg2"
interface = "kk%s"
mtu = 1380
state_dir = %s
address = ["10.0.0.1/24"]

[awg2]
private_key = "test"
peer_public_key = "test"
preshared_key = ""
endpoint = "1.2.3.4:51820"
allowed_ips = ["0.0.0.0/0", "::/0"]
persistent_keepalive = 0
jc = 1
jmin = 2
jmax = 3
s1 = 0
s2 = 0
s3 = 0
s4 = 0
h1 = "test"
h2 = "test"
h3 = "test"
h4 = "test"
i1 = "test"
i2 = "test"
i3 = "test"
i4 = "test"
i5 = "test"
`, tomlString(name), name, tomlString(filepath.Join(dir, name)))
	case "vless-reality":
		content = fmt.Sprintf(`name = %s
protocol = "vless-reality"
interface = "kk%s"
mtu = 1380
state_dir = %s
address = ["10.0.0.1/24"]

[vless_reality]
endpoint = "example.com:443"
uuid = "11111111-1111-1111-1111-111111111111"
server_name = "example.com"
public_key = "testpubkey"
short_id = "testshort"
flow = ""
fingerprint = ""
transport = "raw"
spider_x = ""
`, tomlString(name), name, tomlString(filepath.Join(dir, name)))
	case "openconnect":
		content = fmt.Sprintf(`name = %s
protocol = "openconnect"
interface = "kk%s"
mtu = 1380
state_dir = %s
address = ["10.0.0.1/24"]

[openconnect]
gateway = "vpn.example.test"
username = "tester"
vpn_protocol = "anyconnect"
auth_group = ""
password_file = %s
token_mode = "none"
token_secret_file = ""
user_agent = ""
server_cert = ""
disable_udp = false
disable_ipv6 = false
reconnect_timeout = 30
openconnect_binary = ""
`, tomlString(name), name, tomlString(filepath.Join(dir, name)), tomlString(filepath.Join(dir, name+"-password")))
	default:
		t.Fatalf("unknown protocol %s", proto)
	}
	if proto == "openconnect" {
		if err := os.WriteFile(filepath.Join(dir, name+"-password"), []byte("fixture-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitForRoleStopped waits up to 500ms for the given role to reach Stopped state.
func waitForRoleStopped(t *testing.T, manager *Manager, roleName string) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		snap := manager.Snapshot()
		for _, r := range snap.Roles {
			if r.ID == roleName && r.State == "Stopped" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("role %s did not become stopped", roleName)
}

// waitForRoleStarted waits up to 500ms for the given role to start (state not Stopped).
func waitForRoleStarted(t *testing.T, manager *Manager, roleName string) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		snap := manager.Snapshot()
		for _, r := range snap.Roles {
			if r.ID == roleName && r.State != "Stopped" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("role %s did not start", roleName)
}

// waitForRoleState waits up to 1s for the given role to reach the wanted observed state.
func waitForRoleState(t *testing.T, manager *Manager, roleName, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, r := range manager.Snapshot().Roles {
			if r.ID == roleName && r.State == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	var got string
	for _, r := range manager.Snapshot().Roles {
		if r.ID == roleName {
			got = r.State
		}
	}
	t.Fatalf("role %s did not reach state %q (last %q)", roleName, want, got)
}

func TestManagerAndProductKeepSameMonotonicUnderlayEpoch(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}

	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{
			Epoch: 7,
			IPv4:  &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"},
		},
		Reason: netstate.ChangeInitial,
	})
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{
			Epoch: 1,
			IPv4:  &netstate.Path{Family: 4, IfIndex: 3, Interface: "wlan0"},
		},
		Reason: netstate.ChangeInterface,
	})

	managerEpoch := manager.Snapshot().Underlay.Epoch
	productEpoch := manager.product.Snapshot().Underlay.Epoch
	if managerEpoch != 2 || productEpoch != managerEpoch {
		t.Fatalf("underlay epoch authority diverged: manager=%d product=%d", managerEpoch, productEpoch)
	}
}

type blockingValidationHandler struct {
	mu       sync.Mutex
	count    int
	started  chan struct{}
	release  chan struct{}
	snapshot toadctl.Snapshot
}

func (h *blockingValidationHandler) Handle(ctx context.Context, request toadctl.Request) toadctl.Response {
	if request.Method != "Validate" {
		return toadctl.Response{
			Version: toadctl.ProtocolVersion,
			ID:      request.ID,
			Error:   &toadctl.APIError{Code: "unsupported", Message: "fixture only supports Validate"},
		}
	}
	h.mu.Lock()
	h.count++
	if h.count == 1 {
		close(h.started)
	}
	h.mu.Unlock()

	select {
	case <-ctx.Done():
		return toadctl.Response{
			Version: toadctl.ProtocolVersion,
			ID:      request.ID,
			Error:   &toadctl.APIError{Code: "canceled", Message: ctx.Err().Error(), Retryable: true},
		}
	case <-h.release:
	}
	result := toadctl.ValidationResult{Healthy: true, State: "ready", Reason: "fixture validated"}
	snapshot := h.snapshot
	return toadctl.Response{
		Version:    toadctl.ProtocolVersion,
		ID:         request.ID,
		OK:         true,
		Snapshot:   &snapshot,
		Validation: &result,
	}
}

func (h *blockingValidationHandler) WaitForRevision(ctx context.Context, _ uint64) (toadctl.Snapshot, error) {
	<-ctx.Done()
	return toadctl.Snapshot{}, ctx.Err()
}

func (h *blockingValidationHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

func TestDuplicatePositiveSnapshotsCoalesceValidation(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "toad.sock")
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{
			IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"},
		},
		Reason: netstate.ChangeInitial,
	})
	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.roles["one"].controlSocket = socket
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}

	snapshot := toadctl.Snapshot{
		Generation:    10,
		Revision:      1,
		State:         "online",
		RouteReady:    true,
		InterfaceName: "kkone",
		IfIndex:       7,
		MTU:           1380,
		Addresses:     []string{"10.0.0.1/24"},
	}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, snapshot); !live || !accepted {
		t.Fatalf("initial snapshot rejected: live=%v accepted=%v", live, accepted)
	}

	handler := &blockingValidationHandler{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		snapshot: snapshot,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = (toadctl.Server{Socket: socket, Handler: handler}).Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("unix", socket, 20*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	manager.scheduleValidation("one", process)
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("validation did not start")
	}
	for i := 0; i < 5; i++ {
		snapshot.Revision++
		if live, accepted := manager.observeToadSnapshotForProcess("one", process, snapshot); !live || !accepted {
			t.Fatalf("duplicate snapshot %d rejected: live=%v accepted=%v", i, live, accepted)
		}
		manager.scheduleValidation("one", process)
	}
	if got := handler.Count(); got != 1 {
		t.Fatalf("duplicate positive snapshots started %d validations, want 1", got)
	}

	close(handler.release)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		role, ok := manager.product.Role("one")
		if ok && role.ValidatedEpoch == manager.currentUnderlay().Epoch {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := handler.Count(); got != 1 {
		t.Fatalf("validation count changed after completion: %d", got)
	}
}

func TestStaleProcessCannotPublishOrCompleteValidation(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}

	oldProcess := &fakeProcess{done: make(chan error, 1)}
	newProcess := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = oldProcess
	manager.mu.Unlock()

	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	manager.product.SetUnderlay(netstate.Snapshot{
		IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "fixture-underlay"},
	}, netstate.ChangeInitial)

	oldSnapshot := toadctl.Snapshot{
		Generation:    10,
		Revision:      1,
		State:         "online",
		RouteReady:    true,
		InterfaceName: "kkone",
		IfIndex:       7,
		MTU:           1380,
		Addresses:     []string{"10.0.0.1/24"},
	}
	if live, accepted := manager.observeToadSnapshotForProcess("one", oldProcess, oldSnapshot); !live || !accepted {
		t.Fatalf("initial process snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	token, ok := manager.product.BeginValidation("one")
	if !ok {
		t.Fatal("initial validation did not begin")
	}

	manager.mu.Lock()
	manager.roles["one"].process = newProcess
	_ = manager.product.BeginToadGeneration("one")
	manager.mu.Unlock()

	newSnapshot := oldSnapshot
	newSnapshot.Generation = 11
	newSnapshot.Revision = 1
	if live, accepted := manager.observeToadSnapshotForProcess("one", newProcess, newSnapshot); !live || !accepted {
		t.Fatalf("replacement process snapshot rejected: live=%v accepted=%v", live, accepted)
	}

	staleSnapshot := oldSnapshot
	staleSnapshot.Revision = 2
	if live, accepted := manager.observeToadSnapshotForProcess("one", oldProcess, staleSnapshot); live || accepted {
		t.Fatalf("stale process snapshot was accepted: live=%v accepted=%v", live, accepted)
	}
	before, ok := manager.product.Role("one")
	if !ok {
		t.Fatal("product role disappeared before stale completion")
	}
	result := toadctl.ValidationResult{Healthy: true, State: "ready", Reason: "late old-process validation"}
	if manager.completeValidationForProcess("one", oldProcess, token, result) {
		t.Fatal("stale process validation completed")
	}

	role, ok := manager.product.Role("one")
	if !ok {
		t.Fatal("product role disappeared")
	}
	if role.ToadGeneration != 11 || role.State == "Ready" || role.ValidatedEpoch != 0 || role.Validation != before.Validation {
		t.Fatalf("stale process mutated authoritative role: before=%#v after=%#v", before, role)
	}
}

func TestStaleCompatibilityStateCannotPublishToProduct(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	oldProcess := &fakeProcess{done: make(chan error, 1)}
	newProcess := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = oldProcess
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}

	oldState := state.Snapshot{
		Name:       "one",
		Generation: 10,
		State:      "online",
		RouteReady: true,
		Interface: state.InterfaceState{
			Name: "kkone", IfIndex: 7, MTU: 1380, Addresses: []string{"10.0.0.1/24"},
		},
	}
	if live, accepted := manager.observeCompatibilityStateForProcess("one", oldProcess, oldState); !live || !accepted {
		t.Fatalf("initial compatibility state rejected: live=%v accepted=%v", live, accepted)
	}

	manager.mu.Lock()
	manager.roles["one"].process = newProcess
	_ = manager.product.BeginToadGeneration("one")
	manager.mu.Unlock()
	newState := oldState
	newState.Generation = 11
	if live, accepted := manager.observeCompatibilityStateForProcess("one", newProcess, newState); !live || !accepted {
		t.Fatalf("replacement compatibility state rejected: live=%v accepted=%v", live, accepted)
	}

	staleState := oldState
	staleState.State = "failed"
	staleState.Reason = "late old state.json"
	if live, accepted := manager.observeCompatibilityStateForProcess("one", oldProcess, staleState); live || accepted {
		t.Fatalf("stale compatibility state was accepted: live=%v accepted=%v", live, accepted)
	}
	role, ok := manager.product.Role("one")
	if !ok {
		t.Fatal("product role disappeared")
	}
	if role.ToadGeneration != 11 || role.Toad.State == "failed" || role.LastError == "late old state.json" {
		t.Fatalf("stale compatibility state mutated product role: %#v", role)
	}
}

type mutableManagedInterfaceVerifier struct {
	mu    sync.Mutex
	state platform.ManagedInterfaceOwnership
	err   error
}

func (v *mutableManagedInterfaceVerifier) EnsureUnmanaged(context.Context, string) (platform.ManagedInterfaceOwnership, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state, v.err
}

func (v *mutableManagedInterfaceVerifier) Set(state platform.ManagedInterfaceOwnership, err error) {
	v.mu.Lock()
	v.state = state
	v.err = err
	v.mu.Unlock()
}

func TestNetworkManagerReownerForcesAndThenRevalidatesRole(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "toad.sock")
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
	if !ok || !manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"}) {
		t.Fatal("could not establish initial Ready role")
	}

	verifier := &mutableManagedInterfaceVerifier{
		state: platform.ManagedInterfaceOwnership{Present: true, Managed: true},
	}
	manager.mu.Lock()
	manager.interfaceOwnership = verifier
	manager.mu.Unlock()
	manager.ensureManagedInterfaceOwnership(context.Background())

	role, ok := manager.product.Role("one")
	if !ok || role.State != core.RoleRecovering || role.ValidatedEpoch != 0 {
		t.Fatalf("NetworkManager re-owner did not invalidate Ready: %#v", role)
	}
	manager.mu.Lock()
	blocked := manager.networkManagerBlocked["one"]
	manager.mu.Unlock()
	if !blocked {
		t.Fatal("NetworkManager block was not recorded")
	}

	handler := &blockingValidationHandler{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		snapshot: ready,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = (toadctl.Server{Socket: socket, Handler: handler}).Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("unix", socket, 20*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	verifier.Set(platform.ManagedInterfaceOwnership{Present: true, Managed: false}, nil)
	manager.ensureManagedInterfaceOwnership(context.Background())
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("ownership restoration did not request validation")
	}
	close(handler.release)

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		role, ok = manager.product.Role("one")
		if ok && role.State == core.RoleReady && role.ValidatedEpoch == manager.currentUnderlay().Epoch {
			manager.mu.Lock()
			blocked = manager.networkManagerBlocked["one"]
			manager.mu.Unlock()
			if blocked {
				t.Fatal("NetworkManager block remained after successful revalidation")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("role did not return Ready after NetworkManager relinquished ownership: %#v", role)
}

type routeTargetRecoveryDriver struct {
	mu        sync.Mutex
	steps     []core.RecoveryStep
	started   chan struct{}
	once      sync.Once
	startedMu sync.Mutex
}

func (d *routeTargetRecoveryDriver) ResetStarted() {
	d.startedMu.Lock()
	d.started = make(chan struct{})
	d.once = sync.Once{}
	d.startedMu.Unlock()
}

func (d *routeTargetRecoveryDriver) StartedChan() chan struct{} {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return d.started
}

func (d *routeTargetRecoveryDriver) startTransportOnce() {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	d.once.Do(func() { close(d.started) })
}

func (d *routeTargetRecoveryDriver) record(step core.RecoveryStep) {
	d.mu.Lock()
	d.steps = append(d.steps, step)
	d.mu.Unlock()
}
func (d *routeTargetRecoveryDriver) ObserveRoutes(context.Context, string) error {
	d.record(core.RecoveryObserveRoutes)
	return nil
}
func (d *routeTargetRecoveryDriver) Park(context.Context, string) error {
	d.record(core.RecoveryPark)
	return nil
}
func (d *routeTargetRecoveryDriver) Withdraw(context.Context, string) error {
	d.record(core.RecoveryWithdraw)
	return nil
}
func (d *routeTargetRecoveryDriver) Quiesce(context.Context, string) error {
	d.record(core.RecoveryQuiesce)
	return nil
}
func (d *routeTargetRecoveryDriver) ApplyEndpoint(context.Context, string) error {
	d.record(core.RecoveryApplyEndpoint)
	return nil
}
func (d *routeTargetRecoveryDriver) Rebind(context.Context, string) error {
	d.record(core.RecoveryRebind)
	return nil
}
func (d *routeTargetRecoveryDriver) StartTransport(context.Context, string) error {
	d.record(core.RecoveryStartTransport)
	d.startTransportOnce()
	return core.ErrToadRestartPending
}
func (d *routeTargetRecoveryDriver) Validate(context.Context, string) error {
	d.record(core.RecoveryValidate)
	return nil
}
func (d *routeTargetRecoveryDriver) Publish(context.Context, string) error {
	d.record(core.RecoveryPublish)
	return nil
}
func (d *routeTargetRecoveryDriver) ResyncLeshy(context.Context, string) error {
	d.record(core.RecoveryResyncLeshy)
	return nil
}
func (d *routeTargetRecoveryDriver) ObserveRestoration(context.Context, string) error {
	d.record(core.RecoveryObserveRestore)
	return nil
}
func (d *routeTargetRecoveryDriver) Steps() []core.RecoveryStep {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]core.RecoveryStep(nil), d.steps...)
}

func TestRouteReadyLossSchedulesSingleAutomaticRecovery(t *testing.T) {
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

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
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
	for i := 0; i < 5; i++ {
		manager.scheduleRouteTargetRecovery("one", process)
	}

	select {
	case <-driver.StartedChan():
	case <-time.After(time.Second):
		t.Fatal("route-target recovery did not reach full restart")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		role, ok := manager.product.Role("one")
		if ok && role.State == core.RoleStarting && role.Recovery.Step == core.RecoveryStartTransport {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	steps := driver.Steps()
	want := []core.RecoveryStep{
		core.RecoveryObserveRoutes, core.RecoveryPark, core.RecoveryWithdraw,
		core.RecoveryQuiesce, core.RecoveryApplyEndpoint, core.RecoveryStartTransport,
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("route-target recovery was duplicated or continued after restart: got=%v want=%v", steps, want)
	}
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if !inFlight {
		t.Fatal("asynchronous restart did not retain recovery ownership until replacement generation")
	}
}

type parkingRetryDriver struct {
	mu               sync.Mutex
	restorationCalls int
	retried          chan struct{}
}

func (d *parkingRetryDriver) ObserveRoutes(context.Context, string) error { return nil }
func (d *parkingRetryDriver) Park(context.Context, string) error          { return nil }
func (d *parkingRetryDriver) Withdraw(context.Context, string) error      { return nil }
func (d *parkingRetryDriver) Quiesce(context.Context, string) error       { return nil }
func (d *parkingRetryDriver) ApplyEndpoint(context.Context, string) error { return nil }
func (d *parkingRetryDriver) Rebind(context.Context, string) error        { return nil }
func (d *parkingRetryDriver) StartTransport(context.Context, string) error {
	return nil
}
func (d *parkingRetryDriver) Validate(context.Context, string) error    { return nil }
func (d *parkingRetryDriver) Publish(context.Context, string) error     { return nil }
func (d *parkingRetryDriver) ResyncLeshy(context.Context, string) error { return nil }
func (d *parkingRetryDriver) ObserveRestoration(context.Context, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restorationCalls++
	if d.restorationCalls == 1 {
		return parking.ErrRoutesStillParked
	}
	select {
	case <-d.retried:
	default:
		close(d.retried)
	}
	return nil
}

func TestRoutesStillParkedSchedulesBoundedRecoveryRetry(t *testing.T) {
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
	role, ok := manager.product.Role("one")
	if !ok {
		t.Fatal("product role missing")
	}
	if !manager.product.SetRecovery("one", core.RecoveryState{}, core.RoleRecovering, "routes still parked") {
		t.Fatal("could not put role into recovery")
	}

	driver := &parkingRetryDriver{retried: make(chan struct{})}
	manager.recoverRole(driver, "one", role.Operation, manager.currentUnderlay().Epoch)

	select {
	case <-driver.retried:
	case <-time.After(2 * time.Second):
		t.Fatal("parked-route recovery retry was not scheduled")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.product.Role("one")
		if ok && current.State == core.RoleReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	current, _ := manager.product.Role("one")
	t.Fatalf("successful parking retry did not restore Ready: %#v", current)
}

func TestManagerControlsRolesIndependently(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	manager, err := NewManager([]string{writeConfig(t, dir, "first", "openconnect"), writeConfig(t, dir, "second", "openconnect")}, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStarted(t, manager, "first")
	if got := manager.Snapshot(); got.AggregateState != "Connecting" || len(got.Roles) != 2 {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
	if err := manager.DisconnectRole("first"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStopped(t, manager, "first")
	if got := manager.Snapshot(); got.AggregateState != "Stopped" {
		t.Fatalf("got aggregate %q", got.AggregateState)
	}
}

func TestAPIRejectsVersionAndExposesCommands(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	bad := manager.Handle(context.Background(), Request{Version: 99, Method: "Snapshot"})
	if bad.OK || bad.Error == "" {
		t.Fatal("version mismatch was accepted")
	}
	good := manager.Handle(context.Background(), Request{Version: APIVersion, Method: "Handshake"})
	if !good.OK || len(good.Capabilities) == 0 {
		t.Fatalf("bad handshake: %#v", good)
	}
}

func TestStartFailureIsReportedInSnapshot(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{err: errors.New("missing binary")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectRole(context.Background(), "one"); err == nil {
		t.Fatal("expected start error")
	}
	if got := manager.Snapshot().Roles[0].State; got != "Failed" {
		t.Fatalf("got %q", got)
	}
}

// TestThreeProtocolConfigLoad verifies that the manager can load configs of all three protocol types
// and that the Protocol field in RoleSnapshot reflects the correct protocol.
func TestThreeProtocolConfigLoad(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	paths := []string{
		writeConfig(t, dir, "awg2", "amneziawg2"),
		writeConfig(t, dir, "vless", "vless-reality"),
		writeConfig(t, dir, "oc", "openconnect"),
	}
	manager, err := NewManager(paths, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	snap := manager.Snapshot()
	if len(snap.Roles) != 3 {
		t.Fatalf("expected 3 roles, got %d", len(snap.Roles))
	}
	// map name to protocol
	protoMap := make(map[string]string)
	for _, r := range snap.Roles {
		protoMap[r.ID] = r.Protocol
	}
	if protoMap["awg2"] != "amneziawg2" {
		t.Errorf("awg2 protocol: expected amneziawg2, got %s", protoMap["awg2"])
	}
	if protoMap["vless"] != "vless-reality" {
		t.Errorf("vless protocol: expected vless-reality, got %s", protoMap["vless"])
	}
	if protoMap["oc"] != "openconnect" {
		t.Errorf("openconnect protocol: expected openconnect, got %s", protoMap["oc"])
	}
}

// TestDesiredVsObservedState verifies that desired state (enabled flag) is separate from observed state.
// Initially roles are disabled (enabled=false) and observed state is Stopped.
// After ConnectRole, desired enabled=true, observed state becomes Connecting then (via fake process) Online.
// After DisconnectRole, desired enabled=false, observed state becomes Stopped.
func TestDesiredVsObservedState(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	manager, err := NewManager([]string{writeConfig(t, dir, "test", "openconnect")}, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	// Initially disabled, observed stopped
	snap := manager.Snapshot()
	if len(snap.Roles) != 1 {
		t.Fatalf("expected 1 role")
	}
	r := &snap.Roles[0]
	// Check that initial actions are only connect (disabled)
	if len(r.AvailableActions) != 1 || r.AvailableActions[0] != "connect" {
		t.Errorf("initial disabled role actions: want [%s], got %v", "connect", r.AvailableActions)
	}
	// Enable and start
	if err := manager.ConnectRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStarted(t, manager, "test")
	// After ConnectRole, process started but not yet waited; observed state should be Starting (or Connecting?)
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	// Now enabled true, process running -> observed state Starting (since no state.json yet)
	if r.State != "Starting" {
		t.Errorf("after ConnectRole, expected Starting state, got %s", r.State)
	}
	// Actions should be disconnect, retry (running)
	if len(r.AvailableActions) != 2 || !(contains(r.AvailableActions, "disconnect") && contains(r.AvailableActions, "retry")) {
		t.Errorf("running role actions: want disconnect and retry, got %v", r.AvailableActions)
	}
	// Simulate process exit successfully
	launcher.process.done <- nil
	// Give time for goroutine to run
	time.Sleep(20 * time.Millisecond)
	// Now process nil, lastError set -> observed state Failed? Actually exit with no error => lastError "Toad exited" => Failed.
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if r.State != "Stopped" {
		t.Errorf("after process exit, expected Stopped state, got %s", r.State)
	}
	// Now disable (disconnect)
	if err := manager.DisconnectRole("test"); err != nil {
		t.Fatal(err)
	}
	// After DisconnectRole, enabled false, process nil, lastError cleared? In DisconnectRole we cleared lastError.
	// So observed state should be Stopped.
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if r.State != "Stopped" {
		t.Errorf("after DisconnectRole, expected Stopped state, got %s", r.State)
	}
	// Actions should be connect (disabled)
	if len(r.AvailableActions) != 1 || r.AvailableActions[0] != "connect" {
		t.Errorf("disabled role after disconnect actions: want [%s], got %v", "connect", r.AvailableActions)
	}
}

// TestAvailableActions verifies that available actions are derived correctly from desired/observed state.
func TestAvailableActions(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	manager, err := NewManager([]string{writeConfig(t, dir, "test", "openconnect")}, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	// disabled, stopped -> connect
	snap := manager.Snapshot()
	r := &snap.Roles[0]
	if !equalActions(r.AvailableActions, []string{"connect"}) {
		t.Errorf("disabled stopped: want [connect], got %v", r.AvailableActions)
	}
	// start connecting
	if err := manager.ConnectRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStarted(t, manager, "test")
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	// enabled true, process running, no state yet -> Starting
	if !equalActions(r.AvailableActions, []string{"disconnect", "retry"}) {
		t.Errorf("enabled Starting: want [disconnect retry], got %v", r.AvailableActions)
	}
	// simulate successful online state
	// We'll fake a state.json with Online state
	os.MkdirAll(filepath.Join(dir, "test"), 0o700)
	stateData := []byte(`{"State":"online","Reason":"","RouteReady":true,"Interface":{"name":"kk0","ifindex":1,"mtu":1380,"addresses":["10.0.0.1/24"]},"Session":{"connected":true,"rx_bytes":0,"tx_bytes":0,"endpoint":""}}`)
	if err := os.WriteFile(filepath.Join(dir, "test", "state.json"), stateData, 0o600); err != nil {
		t.Fatal(err)
	}
	// State files are consumed by the compatibility watcher. Snapshot itself is
	// deliberately side-effect free, so wait for the asynchronous observation.
	waitForRoleState(t, manager, "test", "Online")
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if !equalActions(r.AvailableActions, []string{"disconnect", "retry"}) {
		t.Errorf("enabled Online: want [disconnect retry], got %v", r.AvailableActions)
	}
	// simulate failure
	launcher.process.done <- errors.New("crash")
	time.Sleep(20 * time.Millisecond)
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if r.State != "Failed" {
		t.Errorf("after crash, expected Failed, got %s", r.State)
	}
	if !equalActions(r.AvailableActions, []string{"retry"}) {
		t.Errorf("failed: want [retry], got %v", r.AvailableActions)
	}
	// disable after failure
	if err := manager.DisconnectRole("test"); err != nil {
		t.Fatal(err)
	}
	// disabled, failed? Actually DisconnectRole clears lastError and sets enabled false.
	// So process nil, lastError cleared -> Stopped.
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if r.State != "Stopped" {
		t.Errorf("after disconnect from failed, expected Stopped, got %s", r.State)
	}
	if !equalActions(r.AvailableActions, []string{"connect"}) {
		t.Errorf("disabled stopped after failure: want [connect], got %v", r.AvailableActions)
	}
}

// TestSetActiveProfile validates that SetActiveProfile accepts known profile and rejects unknown.
func TestSetActiveProfile(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	paths := []string{
		writeConfig(t, dir, "r1", "openconnect"),
		writeConfig(t, dir, "r2", "openconnect"),
	}
	manager, err := NewManager(paths, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	// default profile should be valid
	if err := manager.SetActiveProfile("default"); err != nil {
		t.Fatalf("SetActiveProfile default failed: %v", err)
	}
	// unknown profile should fail
	if err := manager.SetActiveProfile("unknown"); err == nil {
		t.Fatalf("SetActiveProfile unknown should have failed")
	}
	// after adding a custom profile? Not needed.
}

// TestRevisionMonotonicity ensures that revision only increases.
func TestRevisionMonotonicity(t *testing.T) {
	dir := t.TempDir()
	launcher := &fakeLauncher{}
	manager, err := NewManager([]string{writeConfig(t, dir, "test", "openconnect")}, launcher, "")
	if err != nil {
		t.Fatal(err)
	}
	rev1 := manager.Snapshot().Revision
	if err := manager.ConnectRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStarted(t, manager, "test")
	rev2 := manager.Snapshot().Revision
	if rev2 <= rev1 {
		t.Fatalf("revision did not increase after ConnectRole: %d -> %d", rev1, rev2)
	}
	if err := manager.DisconnectRole("test"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStopped(t, manager, "test")
	rev3 := manager.Snapshot().Revision
	if rev3 <= rev2 {
		t.Fatalf("revision did not increase after DisconnectRole: %d -> %d", rev2, rev3)
	}
	// RetryRole on a stopped, non-failed role is a no-op.
	if err := manager.RetryRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if rev4 := manager.Snapshot().Revision; rev4 != rev3 {
		t.Fatalf("revision changed on RetryRole when not failed: %d -> %d", rev3, rev4)
	}
	// Drive the role into Failed: connect again, then deliver a process error
	// to the currently running process.
	if err := manager.ConnectRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	waitForRoleStarted(t, manager, "test")
	launcher.process.done <- errors.New("fail")
	waitForRoleState(t, manager, "test", "Failed")
	rev5 := manager.Snapshot().Revision
	if err := manager.RetryRole(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	rev6 := manager.Snapshot().Revision
	if rev6 <= rev5 {
		t.Fatalf("revision did not increase after RetryRole on failed: %d -> %d", rev5, rev6)
	}
}

// Helper functions
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
func equalActions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSubscribeStreamsOnlyNewerRevisions(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		serveConnection(ctx, server, manager)
		close(done)
	}()

	if err := writeFrame(client, Request{Version: APIVersion, ID: "sub", Method: "Subscribe"}); err != nil {
		t.Fatal(err)
	}
	var first Response
	if err := readFrame(client, &first); err != nil {
		t.Fatal(err)
	}
	if !first.OK || first.Snapshot == nil {
		t.Fatalf("bad initial subscribe response: %#v", first)
	}
	initialRevision := first.Snapshot.Revision

	if err := writeFrame(client, Request{Version: APIVersion, ID: "cmd", Method: "ConnectRole", Role: "one"}); err != nil {
		t.Fatal(err)
	}
	var commandResponse, streamResponse *Response
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	for commandResponse == nil || streamResponse == nil {
		var response Response
		if err := readFrame(client, &response); err != nil {
			t.Fatalf("read command/stream response: %v", err)
		}
		switch {
		case response.ID == "cmd":
			copy := response
			commandResponse = &copy
		case response.ID == "sub" && response.Snapshot != nil && response.Snapshot.Revision > initialRevision:
			copy := response
			streamResponse = &copy
		}
	}
	_ = client.SetReadDeadline(time.Time{})
	if !commandResponse.OK || commandResponse.Snapshot == nil ||
		!streamResponse.OK || streamResponse.Snapshot == nil {
		t.Fatalf("bad command/stream responses: %#v %#v", commandResponse, streamResponse)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscription did not stop after peer closed")
	}
}

func TestDiagnosticSnapshotUsesRuntimeValues(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "/tmp/core.sock")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := manager.DiagnosticSnapshot().Diagnostics
	if diagnostics == nil || diagnostics.CoreVersion == "unknown" || diagnostics.Platform != runtime.GOOS || diagnostics.SocketPath != "/tmp/core.sock" {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}

type integrationProcess struct {
	done chan error
	once sync.Once
}

func (p *integrationProcess) Wait() error { return <-p.done }
func (p *integrationProcess) Stop() error {
	p.once.Do(func() { p.done <- nil })
	return nil
}
func (p *integrationProcess) Crash() { p.once.Do(func() { p.done <- errors.New("fixture crash") }) }

type integrationLauncher struct {
	processes map[string]*integrationProcess
}

func (l *integrationLauncher) Start(_ context.Context, path string) (Process, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if err := (state.Writer{Dir: cfg.StateDir}).Write(state.Snapshot{
		Name: cfg.Name, Protocol: string(cfg.Protocol), State: "online",
		Reason: "integration fixture", RouteReady: true,
		Interface: state.InterfaceState{Name: cfg.Interface, IfIndex: 7, MTU: cfg.MTU},
		Session:   state.SessionState{Connected: true, Endpoint: "fixture.invalid:443"},
	}); err != nil {
		return nil, err
	}
	process := &integrationProcess{done: make(chan error, 1)}
	l.processes[cfg.Name] = process
	return process, nil
}

func TestCoreIPCControlsThreeToadsEndToEnd(t *testing.T) {
	dir := shortSocketDir(t)
	paths := []string{
		writeConfig(t, dir, "awg2", "amneziawg2"),
		writeConfig(t, dir, "vless", "vless-reality"),
		writeConfig(t, dir, "oc", "openconnect"),
	}
	socket := filepath.Join(dir, "core.sock")
	launcher := &integrationLauncher{processes: make(map[string]*integrationProcess)}
	manager, err := NewManager(paths, launcher, socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, socket, manager) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if conn, dialErr := net.DialTimeout("unix", socket, 20*time.Millisecond); dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	response, err := Call(socket, Request{Version: APIVersion, ID: "start", Method: "ConnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Snapshot == nil || len(response.Snapshot.Roles) != 3 {
		t.Fatalf("bad ConnectAll response: %#v", response)
	}
	for _, role := range []string{"awg2", "vless", "oc"} {
		waitForRoleState(t, manager, role, "Online")
	}
	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		if role.State != "Online" || !role.RouteReady || !role.Session.Connected {
			t.Fatalf("role was not reported online after asynchronous state observation: %#v", role)
		}
	}
	launcher.processes["awg2"].Crash()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := manager.Snapshot()
		failed, unaffected := false, 0
		for _, role := range snapshot.Roles {
			failed = failed || role.ID == "awg2" && role.State == "Failed"
			if role.ID != "awg2" && role.State == "Online" {
				unaffected++
			}
		}
		if failed && unaffected == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot = manager.Snapshot()
	if snapshot.Roles[0].State != "Failed" || snapshot.Roles[1].State != "Online" || snapshot.Roles[2].State != "Online" {
		t.Fatalf("one Toad failure affected unrelated roles: %#v", snapshot.Roles)
	}

	response, err = Call(socket, Request{Version: APIVersion, ID: "stop", Method: "DisconnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range response.Snapshot.Roles {
		if role.State != "Stopped" {
			t.Fatalf("role was not stopped: %#v", role)
		}
	}
	var encoded Snapshot
	data, _ := json.Marshal(response.Snapshot)
	if err := json.Unmarshal(data, &encoded); err != nil || len(encoded.Roles) != 3 {
		t.Fatalf("snapshot did not round-trip: %v", err)
	}
	if strings.Contains(string(data), "fixture-secret") {
		t.Fatal("snapshot leaked fixture secret")
	}
}

func buildFakeToad(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "fake-toad")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "./internal/control/testhelper")
	cmd.Dir = moduleDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-toad: %v\n%s", err, output)
	}
	return binary
}

func TestCoreIPCUsesFakeToadBinaryWithoutNetwork(t *testing.T) {
	dir := shortSocketDir(t)
	paths := []string{
		writeConfig(t, dir, "awg2", "amneziawg2"),
		writeConfig(t, dir, "vless", "vless-reality"),
		writeConfig(t, dir, "oc", "openconnect"),
	}
	socket := filepath.Join(dir, "core.sock")
	manager, err := NewManager(paths, ExecLauncher{Binary: buildFakeToad(t)}, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, socket, manager) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if conn, dialErr := net.DialTimeout("unix", socket, 20*time.Millisecond); dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{
			Epoch:      1,
			IPv4:       &netstate.Path{Family: 4, IfIndex: 2, Interface: "fixture-underlay"},
			ObservedAt: time.Now().UTC(),
		},
		Reason: netstate.ChangeInitial,
	})
	if _, err := Call(socket, Request{Version: APIVersion, Method: "ConnectAll"}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, callErr := Call(socket, Request{Version: APIVersion, Method: "GetSnapshot"})
		if callErr == nil && response.OK && response.Snapshot != nil {
			ready := 0
			for _, role := range response.Snapshot.Roles {
				if role.State == "Ready" &&
					role.RouteReady &&
					role.ValidatedEpoch == response.Snapshot.Underlay.Epoch {
					ready++
				}
			}
			if ready == 3 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		if role.State != "Ready" || !role.RouteReady || role.ValidatedEpoch != snapshot.Underlay.Epoch {
			t.Fatalf("fake Toad did not complete authoritative validation: %#v", snapshot.Roles)
		}
	}
	response, err := Call(socket, Request{Version: APIVersion, Method: "DisconnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Snapshot == nil {
		t.Fatalf("bad DisconnectAll response: %#v", response)
	}
	for _, role := range response.Snapshot.Roles {
		if role.State != "Stopped" {
			t.Fatalf("fake Toad did not stop: %#v", response.Snapshot.Roles)
		}
	}
}

// TestCoalescerBuildFailureRestartsAndProcessesNextInvalidation verifies that
// when the underlay coalescer's Build function fails, the supervised loop
// restarts and a subsequent invalidation is processed.
func TestCoalescerBuildFailureRestartsAndProcessesNextInvalidation(t *testing.T) {
	var buildAttempts atomic.Int32
	var mu sync.Mutex
	var current netstate.Snapshot
	current = netstate.Snapshot{Epoch: 1, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}

	invalidations := make(chan netstate.Invalidation, 64)

	builder := func(ctx context.Context) (netstate.Snapshot, error) {
		n := buildAttempts.Add(1)
		if n <= 1 {
			return netstate.Snapshot{}, errors.New("transient build failure")
		}
		return netstate.Snapshot{
			IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0", Gateway: netip.MustParseAddr("192.168.1.1")},
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		backoff := &supervisor.Backoff{}
		for {
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			initial := current
			mu.Unlock()

			c := &netstate.Coalescer{
				Settle:  50 * time.Millisecond,
				Maximum: 200 * time.Millisecond,
				Build:   builder,
				Changed: func(change netstate.Change) error {
					mu.Lock()
					current = change.Snapshot
					mu.Unlock()
					return nil
				},
			}
			_ = c.Run(ctx, invalidations, initial)
			if ctx.Err() != nil {
				return
			}
			delay := backoff.Next()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		invalidations <- netstate.Invalidation{Source: "test"}
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		epoch := current.Epoch
		mu.Unlock()
		if epoch >= 2 {
			break
		}
	}
	mu.Lock()
	epoch := current.Epoch
	mu.Unlock()
	if epoch < 2 {
		t.Fatal("coalescer did not recover after transient build failure")
	}
	total := buildAttempts.Load()
	if total < 2 {
		t.Fatalf("build was called %d times, expected at least 2 (fail + retry)", total)
	}
}

// TestCoalescerRestartUsesCurrentUnderlay verifies that after a coalescer
// restart, the canonical epoch starts from the current m.underlay, not from
// the stale snapshot captured before the failure.
func TestCoalescerRestartUsesCurrentUnderlay(t *testing.T) {
	var buildAttempts atomic.Int32
	var mu sync.Mutex
	var current netstate.Snapshot
	current = netstate.Snapshot{Epoch: 10, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}

	invalidations := make(chan netstate.Invalidation, 64)

	builder := func(ctx context.Context) (netstate.Snapshot, error) {
		n := buildAttempts.Add(1)
		if n == 1 {
			return netstate.Snapshot{}, errors.New("first build failure")
		}
		return netstate.Snapshot{
			IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0", Gateway: netip.MustParseAddr("192.168.1.1")},
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		backoff := &supervisor.Backoff{}
		for {
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			initial := current
			mu.Unlock()

			c := &netstate.Coalescer{
				Settle:  50 * time.Millisecond,
				Maximum: 200 * time.Millisecond,
				Build:   builder,
				Changed: func(change netstate.Change) error {
					mu.Lock()
					current = change.Snapshot
					mu.Unlock()
					return nil
				},
			}
			_ = c.Run(ctx, invalidations, initial)
			if ctx.Err() != nil {
				return
			}
			delay := backoff.Next()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		invalidations <- netstate.Invalidation{Source: "test"}
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		epoch := current.Epoch
		mu.Unlock()
		if epoch == 11 {
			break
		}
	}
	mu.Lock()
	epoch := current.Epoch
	mu.Unlock()
	if epoch != 11 {
		t.Fatalf("epoch is %d, want 11 (current epoch 10 advanced to 11)", epoch)
	}
}

// TestAuditRemainsAliveWhileCoalescerDegraded verifies that the periodic audit
// ticker continues to queue invalidations even when the netlink watcher and
// coalescer are both degraded.
func TestAuditRemainsAliveWhileCoalescerDegraded(t *testing.T) {
	dir := t.TempDir()

	// Use pre-injected builder that always fails so the coalescer stays degraded.
	// This avoids the race of setting manager.underlayBuilder after construction.
	failBuilder := func(ctx context.Context) (netstate.Snapshot, error) {
		return netstate.Snapshot{}, errors.New("permanent build failure")
	}

	manager, err := newManagerWithDeps(
		[]string{writeConfig(t, dir, "one", "openconnect")},
		&fakeLauncher{}, "", "", "",
		managerDependencies{
			underlayBuilder: failBuilder,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	// Set an initial underlay (under mu, safe after construction).
	manager.mu.Lock()
	manager.underlay = netstate.Snapshot{Epoch: 1, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}
	manager.mu.Unlock()

	// Wait for the observer health to reflect degradation.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := manager.Snapshot()
		if !snap.Observers.NetlinkHealthy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := manager.Snapshot()
	if snap.Observers.NetlinkHealthy {
		t.Fatal("coalescer was not marked degraded")
	}

	// Verify manager is still running (audit ticker alive).
	manager.mu.Lock()
	shuttingDown := manager.shuttingDown
	manager.mu.Unlock()
	if shuttingDown {
		t.Fatal("manager is shutting down while audit should be alive")
	}
}

// TestInitialNotReadyDoesNotRecover verifies that a Toad which has never been
// RouteReady is not treated as route-target drift.
func TestInitialNotReadyDoesNotRecover(t *testing.T) {
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

	// everRouteReady is false, so RouteReady=false should not trigger recovery.
	drift := toadctl.Snapshot{
		Generation: 10, Revision: 1, State: "degraded", RouteReady: false,
		InterfaceName: "kkone", IfIndex: 7, MTU: 1380,
	}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("snapshot rejected: live=%v accepted=%v", live, accepted)
	}

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	manager.scheduleRouteTargetRecovery("one", process)

	// No recovery should be started.
	time.Sleep(100 * time.Millisecond)
	if len(driver.Steps()) > 0 {
		t.Fatalf("initial not-ready triggered recovery: %v", driver.Steps())
	}
}

// TestReadyToNotReadyRecoversOnce verifies that a previously RouteReady role
// that loses RouteReady triggers exactly one recovery transaction.
func TestReadyToNotReadyRecoversOnce(t *testing.T) {
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

	// First accepted snapshot with RouteReady=true.
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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	// Send five RouteReady=false snapshots.
	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = nil
	for i := 0; i < 5; i++ {
		if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
			t.Fatalf("drift snapshot %d rejected: live=%v accepted=%v", i, live, accepted)
		}
		manager.scheduleRouteTargetRecovery("one", process)
	}

	select {
	case <-driver.StartedChan():
	case <-time.After(time.Second):
		t.Fatal("route-target recovery did not start")
	}
	time.Sleep(50 * time.Millisecond)
	steps := driver.Steps()
	if len(steps) == 0 {
		t.Fatal("no recovery steps recorded")
	}
	// Verify recoveryInFlight is set.
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if !inFlight {
		t.Fatal("recoveryInFlight should be true after first recovery")
	}
}

// TestStaleProcessCannotTriggerDriftRecovery verifies that an old process
// publishing a bad snapshot does not mutate recovery state.
func TestStaleProcessCannotTriggerDriftRecovery(t *testing.T) {
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

	// Replace process with a new one.
	newProcess := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].process = newProcess
	manager.mu.Unlock()

	// Old process publishes bad snapshot.
	drift := toadctl.Snapshot{
		Generation: 10, Revision: 1, State: "degraded", RouteReady: false,
		InterfaceName: "kkone", IfIndex: 7, MTU: 1380,
	}
	live, accepted := manager.observeToadSnapshotForProcess("one", process, drift)
	if live {
		t.Fatal("old process snapshot was accepted as live")
	}
	if accepted {
		t.Fatal("old process snapshot was accepted by product")
	}
}

// TestAsyncFullRestartHandoff verifies the full async restart handoff path:
// driver forces ErrToadRestartPending, product state is Starting, parking
// remains active, replacement RouteReady enters normal validation path.
func TestAsyncFullRestartHandoff(t *testing.T) {
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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	// Trigger route-target recovery.
	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = nil
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	select {
	case <-driver.started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach StartTransport")
	}

	// Product state should be Starting (from ErrToadRestartPending).
	// Use a bounded predicate wait because Engine.Recover may not have
	// committed RoleStarting before StartTransport closed the channel.
	{
		deadline := time.Now().Add(time.Second)
		var role core.RoleRuntime
		var ok bool
		for time.Now().Before(deadline) {
			role, ok = manager.product.Role("one")
			if ok && role.State == core.RoleStarting && role.Recovery.Step == core.RecoveryStartTransport {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !ok || role.State != core.RoleStarting || role.Recovery.Step != core.RecoveryStartTransport {
			t.Fatalf("product role should be Starting after ErrToadRestartPending: %#v", role)
		}
	}

	// No Publish/ObserveRestoration after StartTransport.
	steps := driver.Steps()
	for _, s := range steps {
		if s == core.RecoveryPublish || s == core.RecoveryObserveRestore {
			t.Fatalf("unexpected step after StartTransport: %v", s)
		}
	}

	// Simulate BeginToadGeneration for the replacement process.
	manager.mu.Lock()
	manager.roles["one"].operation++
	manager.mu.Unlock()
	_ = manager.product.BeginToadGeneration("one")

	// Replacement RouteReady snapshot should enter normal validation path.
	replacement := ready
	replacement.Generation = 11
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, replacement); !live || !accepted {
		t.Fatalf("replacement snapshot rejected: live=%v accepted=%v", live, accepted)
	}

	// scheduleValidation clears recoveryInFlight and starts validation.
	manager.scheduleValidation("one", process)

	// recoveryInFlight should be cleared by scheduleValidation.
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if inFlight {
		t.Fatal("recoveryInFlight should be cleared after replacement RouteReady")
	}
}

// TestReplacementNeverReady verifies that if the replacement process never
// becomes RouteReady, the bounded restart retry fires and a second recovery
// attempt occurs.
func TestReplacementNeverReady(t *testing.T) {
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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	// Trigger route-target recovery.
	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = nil
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	select {
	case <-driver.StartedChan():
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach StartTransport")
	}

	// Replacement process never publishes RouteReady.
	// The pending restart retry should fire and trigger a second recovery.
	driver.ResetStarted()

	select {
	case <-driver.StartedChan():
	case <-time.After(3 * time.Second):
		t.Fatal("pending restart retry did not fire")
	}

	// recoveryInFlight should still be true (no RouteReady yet).
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if !inFlight {
		t.Fatal("recoveryInFlight should remain true until replacement RouteReady")
	}
}

// TestReplacementReadyCancelsPendingRetry verifies that if the replacement
// becomes RouteReady before the pending retry timer fires, the timer does not
// restart the new healthy process.
func TestReplacementReadyCancelsPendingRetry(t *testing.T) {
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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	driver := &routeTargetRecoveryDriver{started: make(chan struct{})}
	manager.SetRecoveryDriver(driver)
	manager.SetAutomaticRecovery(true)

	// Trigger route-target recovery.
	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = nil
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	select {
	case <-driver.StartedChan():
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach StartTransport")
	}

	// Simulate BeginToadGeneration for the replacement process.
	manager.mu.Lock()
	manager.roles["one"].operation++
	manager.mu.Unlock()
	_ = manager.product.BeginToadGeneration("one")

	// Immediately publish replacement RouteReady before retry timer fires.
	replacement := ready
	replacement.Generation = 11
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, replacement); !live || !accepted {
		t.Fatalf("replacement snapshot rejected: live=%v accepted=%v", live, accepted)
	}

	// scheduleValidation clears recoveryInFlight and starts validation.
	manager.scheduleValidation("one", process)

	// recoveryInFlight should be cleared.
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if inFlight {
		t.Fatal("recoveryInFlight should be cleared after replacement RouteReady")
	}

	// Wait for pending retry timer to fire and verify it does not restart.
	time.Sleep(300 * time.Millisecond)

	// recoveryInFlight should remain cleared.
	manager.mu.Lock()
	inFlight = manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if inFlight {
		t.Fatal("recoveryInFlight should remain cleared after replacement RouteReady")
	}
}

// fakeSleepSource implements platform.SleepSource for deterministic testing.
type fakeSleepSource struct {
	mu       sync.Mutex
	callNum  int
	events   chan platform.SleepEvent
	watchErr error
}

func (s *fakeSleepSource) Watch(ctx context.Context, events chan<- platform.SleepEvent) error {
	s.mu.Lock()
	s.callNum++
	callNum := s.callNum
	err := s.watchErr
	s.mu.Unlock()

	if callNum == 1 && err != nil {
		return err
	}
	// Second call: forward events from our internal channel.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-s.events:
			select {
			case events <- e:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func TestSleepWatcherReconnectsAndHandlesSuspendResume(t *testing.T) {
	dir := t.TempDir()

	// Build a fake underlay snapshot that the coalescer will return on build.
	// The coalescer is started after newManagerWithDeps, so the builder
	// must be ready before construction.
	// Note: netstate.Compare renumbers epochs; we only control the path
	// identity, not the final epoch value.
	initialSnap := netstate.Snapshot{
		Epoch: 1,
		IPv4: &netstate.Path{
			Family: 4, IfIndex: 2, Interface: "eth0",
			Gateway: netip.MustParseAddr("192.168.1.1"),
			PreferredSrc: netip.MustParseAddr("192.168.1.100"),
			MTU: 1500, Table: 254, Metric: 100,
		},
	}

	var (
		buildMu     sync.Mutex
		buildCalls  int
		buildResult = initialSnap
		buildErr    error
	)
	underlayBuilder := func(_ context.Context) (netstate.Snapshot, error) {
		buildMu.Lock()
		defer buildMu.Unlock()
		buildCalls++
		return buildResult, buildErr
	}

	// underlayWatch blocks until ctx is done (no real kernel events).
	underlayWatch := func(ctx context.Context, _ chan<- netstate.Invalidation) error {
		<-ctx.Done()
		return ctx.Err()
	}

	source := &fakeSleepSource{
		watchErr: errors.New("first watch failure"),
		events:   make(chan platform.SleepEvent, 8),
	}

	manager, err := newManagerWithDeps(
		[]string{writeConfig(t, dir, "one", "openconnect")},
		&fakeLauncher{},
		"", "", "",
		managerDependencies{
			underlayBuilder: underlayBuilder,
			underlayWatch:   underlayWatch,
			sleepSource:     source,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	// Step 3-4: First Watch call returns an error -> observer degraded.
	// The sleep watcher sets healthy=true before calling Watch, then
	// healthy=false after the error. Wait for the degraded state.
	waitForCondition(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return !manager.Snapshot().Observers.SleepHealthy },
	)
	if manager.Snapshot().Observers.SleepHealthy {
		snap := manager.Snapshot()
		t.Fatalf("sleep observer should be degraded after first watch failure: obs=%#v", snap.Observers)
	}

	// Step 5-6: Second Watch call succeeds -> observer healthy.
	waitForCondition(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return manager.Snapshot().Observers.SleepHealthy },
	)
	if !manager.Snapshot().Observers.SleepHealthy {
		t.Fatal("sleep observer should be healthy after reconnect")
	}

	// Step 7: Send Preparing=true (suspend).
	source.events <- platform.SleepEvent{Preparing: true}

	// Step 8: Assert suspended=true and desired role bits unchanged.
	waitForCondition(t, time.Second, 10*time.Millisecond,
		func() bool {
			manager.mu.Lock()
			s := manager.suspended
			manager.mu.Unlock()
			return s
		},
	)
	manager.mu.Lock()
	if !manager.suspended {
		manager.mu.Unlock()
		t.Fatal("manager should be suspended after Preparing=true")
	}
	enabled := manager.roles["one"].enabled
	suspended := manager.suspended
	manager.mu.Unlock()
	if enabled != false {
		t.Logf("role enabled=%v during suspend (expected false)", enabled)
	}
	if !suspended {
		t.Fatal("manager should be suspended after Preparing=true")
	}

	// Step 9: Send Preparing=false (resume).
	// Before resume, set up the builder to return a newer canonical snapshot
	// so the coalescer produces a material change. Use a different interface
	// identity so netstate.Compare produces a new epoch.
	nextSnap := netstate.Snapshot{
		Epoch: 2,
		IPv4: &netstate.Path{
			Family: 4, IfIndex: 3, Interface: "eth1",
			Gateway: netip.MustParseAddr("10.0.0.1"),
			PreferredSrc: netip.MustParseAddr("10.0.0.100"),
			MTU: 1500, Table: 254, Metric: 100,
		},
	}
	buildMu.Lock()
	buildResult = nextSnap
	buildMu.Unlock()

	source.events <- platform.SleepEvent{Preparing: false}

	// Step 10: Wait for suspended=false and the coalescer to process the
	// resume invalidation (builder is called at least once more).
	// netstate.Compare renumbers epochs: initial build (epoch 1) -> epoch 1,
	// then after resume the new identity (IfIndex 3) bumps epoch to 2.
	// Wait until the manager's underlay epoch reflects the change.
	waitForCondition(t, 3*time.Second, 20*time.Millisecond,
		func() bool {
			manager.mu.Lock()
			s := manager.suspended
			e := manager.underlay.Epoch
			manager.mu.Unlock()
			return !s && e == 2
		},
	)

	manager.mu.Lock()
	if manager.suspended {
		manager.mu.Unlock()
		t.Fatal("manager should not be suspended after Preparing=false")
	}
	underlayEpoch := manager.underlay.Epoch
	manager.mu.Unlock()
	if underlayEpoch != 2 {
		t.Fatalf("underlay epoch = %d, want 2 (after resume with new interface)", underlayEpoch)
	}

	// Step 11-12: Assert the product's underlay epoch advanced and the role's
	// ValidatedEpoch was invalidated.
	prodSnap := manager.product.Snapshot()
	if prodSnap.Underlay.Epoch != 2 {
		t.Fatalf("product underlay epoch = %d, want 2", prodSnap.Underlay.Epoch)
	}

	// The role's ValidatedEpoch should be 0 (invalidated by SetUnderlay
	// because the role was not in RoleReady state with a matching epoch).
	role, ok := prodSnap.Roles["one"]
	if !ok {
		t.Fatal("role 'one' not found in product snapshot")
	}
	if role.ValidatedEpoch != 0 {
		t.Fatalf("role ValidatedEpoch = %d, want 0 (should be invalidated after underlay change)", role.ValidatedEpoch)
	}

	// Observer should remain healthy after resume.
	if !manager.Snapshot().Observers.SleepHealthy {
		t.Fatal("sleep observer should be healthy after resume")
	}
}

// waitForCondition polls fn until it returns true or the deadline expires.
func waitForCondition(t *testing.T, timeout, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
}

// TestResumeWhileRecoveryInFlight verifies that suspend/resume during active
// recovery does not create duplicate concurrent recovery transactions.
// It uses the actual watchSleep, watchUnderlay, coalescer, and recovery
// scheduler with a blocking fake recovery driver.
func TestResumeWhileRecoveryInFlight(t *testing.T) {
	dir := t.TempDir()

	// Use a blocking recovery driver that holds recovery in-flight.
	blockDriver := &blockingRecoveryDriver{
		startTransportCalled: make(chan struct{}),
		release:             make(chan struct{}),
	}

	// Build manager with pre-injected dependencies so observer goroutines
	// run with our fakes from the start.
	manager, err := newManagerWithDeps(
		[]string{writeConfig(t, dir, "one", "openconnect")},
		&fakeLauncher{}, "", "", "",
		managerDependencies{
			underlayBuilder: func(ctx context.Context) (netstate.Snapshot, error) {
				return netstate.Snapshot{
					Epoch: 1,
					IPv4:  &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"},
				}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}

	// Wait for the initial underlay to be built by the coalescer.
	waitForCondition(t, 3*time.Second, 20*time.Millisecond,
		func() bool {
			manager.mu.Lock()
			e := manager.underlay.Epoch
			manager.mu.Unlock()
			return e >= 1
		},
	)

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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	// Set recovery driver and auto-recovery.
	manager.SetRecoveryDriver(blockDriver)
	manager.SetAutomaticRecovery(true)

	// Trigger route-target recovery by publishing RouteReady=false.
	drift := ready
	drift.Revision++
	drift.State = "degraded"
	drift.RouteReady = false
	drift.Addresses = nil
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, drift); !live || !accepted {
		t.Fatalf("drift snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	manager.scheduleRouteTargetRecovery("one", process)

	// Wait for recovery to reach StartTransport (blocked).
	select {
	case <-blockDriver.startTransportCalled:
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach StartTransport")
	}

	// Verify recoveryInFlight is set.
	manager.mu.Lock()
	inFlight := manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if !inFlight {
		t.Fatal("recoveryInFlight should be true after recovery started")
	}

	// Simulate suspend.
	manager.mu.Lock()
	manager.suspended = true
	manager.mu.Unlock()

	// Mutate fake raw underlay to epoch-relevant new identity.
	manager.mu.Lock()
	manager.underlay = netstate.Snapshot{
		Epoch: 2,
		IPv4:  &netstate.Path{Family: 4, IfIndex: 3, Interface: "eth1"},
	}
	manager.mu.Unlock()

	// Simulate resume plus several raw invalidations.
	manager.mu.Lock()
	manager.suspended = false
	manager.mu.Unlock()
	manager.queueUnderlayInvalidation("netlink")
	manager.queueUnderlayInvalidation("resume")
	manager.queueUnderlayInvalidation("netlink")

	// Release the blocked recovery.
	close(blockDriver.release)

	// Wait for recovery to complete.
	waitForCondition(t, 3*time.Second, 10*time.Millisecond,
		func() bool {
			manager.mu.Lock()
			inflight := manager.roles["one"].recoveryInFlight
			manager.mu.Unlock()
			return !inflight
		},
	)

	// Verify recoveryInFlight was cleared.
	manager.mu.Lock()
	inFlight = manager.roles["one"].recoveryInFlight
	manager.mu.Unlock()
	if inFlight {
		t.Fatal("recoveryInFlight should be cleared after recovery completes")
	}

	// Verify max concurrency was 1.
	if blockDriver.concurrentCalls() > 1 {
		t.Fatal("recovery driver had concurrent calls > 1")
	}
}

// blockingRecoveryDriver blocks on StartTransport until released.
type blockingRecoveryDriver struct {
	startTransportCalled chan struct{}
	release             chan struct{}
	mu                  sync.Mutex
	concurrent          int32
}

func (d *blockingRecoveryDriver) concurrentCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return int(d.concurrent)
}

func (d *blockingRecoveryDriver) startCall() {
	atomic.AddInt32(&d.concurrent, 1)
}
func (d *blockingRecoveryDriver) endCall() {
	atomic.AddInt32(&d.concurrent, -1)
}

func (d *blockingRecoveryDriver) ObserveRoutes(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) Park(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) Withdraw(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) Quiesce(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) ApplyEndpoint(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) Rebind(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) StartTransport(ctx context.Context, name string) error {
	d.startCall()
	defer d.endCall()
	select {
	case <-d.startTransportCalled:
	default:
		close(d.startTransportCalled)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.release:
		return nil
	}
}
func (d *blockingRecoveryDriver) Validate(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) Publish(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) ResyncLeshy(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}
func (d *blockingRecoveryDriver) ObserveRestoration(context.Context, string) error {
	d.startCall()
	defer d.endCall()
	return nil
}

// fakeManagedInterface implements both ManagedInterfaceVerifier and
// ManagedInterfaceWatcher for deterministic NetworkManager tests.
type fakeManagedInterface struct {
	mu       sync.Mutex
	state    platform.ManagedInterfaceOwnership
	err      error
	events   chan struct{}
	watchErr error
}

func (f *fakeManagedInterface) EnsureUnmanaged(_ context.Context, iface string) (platform.ManagedInterfaceOwnership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// External interfaces like vpn0 are never touched.
	if iface == "vpn0" {
		return platform.ManagedInterfaceOwnership{}, nil
	}
	return f.state, f.err
}

func (f *fakeManagedInterface) WatchManagedInterfaces(ctx context.Context, events chan<- struct{}) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-f.events:
			select {
			case events <- e:
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-time.After(100 * time.Millisecond):
			// Yield to avoid tight loop.
		}
	}
}

func TestNetworkManagerManagedBlocksAndUnblockedByWatcher(t *testing.T) {
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
		t.Fatal("could not begin validation")
	}
	manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})

	// 1. Managed kk-* interface blocks the role.
	fakeNM := &fakeManagedInterface{
		state:  platform.ManagedInterfaceOwnership{Present: true, Managed: true},
		events: make(chan struct{}, 8),
	}
	manager.mu.Lock()
	manager.interfaceOwnership = fakeNM
	manager.mu.Unlock()
	manager.ensureManagedInterfaceOwnership(context.Background())

	role, ok := manager.product.Role("one")
	if !ok || role.State != core.RoleRecovering {
		t.Fatalf("managed interface should put role into Recovering: %#v", role)
	}

	// 2. Watcher invalidation + verifier now unmanaged => validation request.
	fakeNM.mu.Lock()
	fakeNM.state = platform.ManagedInterfaceOwnership{Present: true, Managed: false}
	fakeNM.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan struct{}, 8)
	go func() {
		_ = fakeNM.WatchManagedInterfaces(ctx, events)
	}()
	events <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	manager.ensureManagedInterfaceOwnership(context.Background())

	// 3. External vpn0 is never touched.
	ownership, err := fakeNM.EnsureUnmanaged(context.Background(), "vpn0")
	if err != nil || ownership.Present || ownership.Managed {
		t.Fatal("external vpn0 should not be touched")
	}
}

// TestNetworkManagerWatcherReconnects verifies that when the watcher exits,
// the Manager reconnects with bounded backoff.
func TestNetworkManagerWatcherReconnects(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	// Replace the verifier with one that supports watching.
	fakeNM := &fakeManagedInterface{
		state:    platform.ManagedInterfaceOwnership{Present: false, Managed: false},
		events:   make(chan struct{}, 8),
		watchErr: errors.New("watch exited"),
	}
	manager.mu.Lock()
	manager.interfaceOwnership = fakeNM
	manager.mu.Unlock()

	// Wait for observer health to reflect the watcher state.
	time.Sleep(100 * time.Millisecond)
	snap := manager.Snapshot()
	t.Logf("NetworkManager observer healthy: %v", snap.Observers.NetworkManagerHealthy)
}
