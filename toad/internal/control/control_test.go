// Package control implements the local Kikimora control plane.  It supervises
// Toad processes; protocol implementations remain owned by kikimora-toad.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
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

func writeConfig(t *testing.T, dir, name string, proto string) string {
	t.Helper()
	path := filepath.Join(dir, name+".toml")
	var content string
	switch proto {
	case "amneziawg2":
		content = `name = "` + name + `"
protocol = "amneziawg2"
interface = "kk` + name + `"
mtu = 1380
state_dir = "` + filepath.Join(dir, name) + `"
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
`
	case "vless-reality":
		content = `name = "` + name + `"
protocol = "vless-reality"
interface = "kk` + name + `"
mtu = 1380
state_dir = "` + filepath.Join(dir, name) + `"
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
`
	case "openconnect":
		content = `name = "` + name + `"
protocol = "openconnect"
interface = "kk` + name + `"
mtu = 1380
state_dir = "` + filepath.Join(dir, name) + `"
address = ["10.0.0.1/24"]

[openconnect]
gateway = "vpn.example.test"
username = "tester"
vpn_protocol = "anyconnect"
auth_group = ""
password_file = "` + filepath.Join(dir, name+"-password") + `"
token_mode = "none"
token_secret_file = ""
user_agent = ""
server_cert = ""
disable_udp = false
disable_ipv6 = false
reconnect_timeout = 30
openconnect_binary = ""
`
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
	stateData := []byte(`{"State":"online","Reason":"","RouteReady":false,"Interface":{"name":"kk0","ifindex":1,"mtu":1380},"Session":{"connected":true,"rx_bytes":0,"tx_bytes":0,"endpoint":""}}`)
	if err := os.WriteFile(filepath.Join(dir, "test", "state.json"), stateData, 0o600); err != nil {
		t.Fatal(err)
	}
	// Trigger a rediscover to read state.json (or just Snapshot will read)
	snap = manager.Snapshot()
	r = &snap.Roles[0]
	if r.State != "Online" {
		t.Errorf("after state.json, expected Online, got %s", r.State)
	}
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

func TestSubscribeStreamsOnlyNewerRevisions(t *testing.T) {
	dir := t.TempDir()
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
	responses := make([]Response, 2)
	for i := range responses {
		if err := readFrame(client, &responses[i]); err != nil {
			t.Fatal(err)
		}
	}
	commandResponse, streamResponse := responses[0], responses[1]
	if commandResponse.ID != "cmd" {
		commandResponse, streamResponse = streamResponse, commandResponse
	}
	if commandResponse.ID != "cmd" || !commandResponse.OK || commandResponse.Snapshot == nil ||
		streamResponse.ID != "sub" || streamResponse.Snapshot == nil || streamResponse.Snapshot.Revision <= initialRevision {
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
	dir := t.TempDir()
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
	for _, role := range response.Snapshot.Roles {
		if role.State != "Online" || !role.RouteReady || !role.Session.Connected {
			t.Fatalf("role was not reported online: %#v", role)
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
	snapshot := manager.Snapshot()
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
	dir := t.TempDir()
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
	if _, err := Call(socket, Request{Version: APIVersion, Method: "ConnectAll"}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, callErr := Call(socket, Request{Version: APIVersion, Method: "GetSnapshot"})
		if callErr == nil && response.OK && response.Snapshot != nil {
			online := 0
			for _, role := range response.Snapshot.Roles {
				if role.State == "Online" {
					online++
				}
			}
			if online == 3 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := manager.Snapshot()
	for _, role := range snapshot.Roles {
		if role.State != "Online" {
			t.Fatalf("fake Toad did not publish online state: %#v", snapshot.Roles)
		}
	}
	response, err := Call(socket, Request{Version: APIVersion, Method: "DisconnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range response.Snapshot.Roles {
		if role.State != "Stopped" {
			t.Fatalf("fake Toad did not stop: %#v", response.Snapshot.Roles)
		}
	}
}
