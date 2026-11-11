// Package readytoad_test exercises the ready-toad binary contract without
// real interfaces (08D1 remediation R4 §6.5): the injected resolver seam
// drives Start/Validate/Rebind/RestartTransport/poll semantics, family
// readiness, address filtering and the Subscribe revision stream over the
// toadctl.Handler contract.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// fixtureResolver is the deterministic injected resolver: the test mutates
// its result to model interface changes.
type fixtureResolver struct {
	mu   sync.Mutex
	info map[string]ifaceInfo
	err  error
}

func (f *fixtureResolver) resolve(name string) (ifaceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return ifaceInfo{}, f.err
	}
	if info, ok := f.info[name]; ok {
		return info, nil
	}
	return ifaceInfo{}, fmt.Errorf("route +1: no such interface %s", name)
}

func (f *fixtureResolver) set(name string, info ifaceInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.info == nil {
		f.info = map[string]ifaceInfo{}
	}
	f.info[name] = info
}

func (f *fixtureResolver) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// writeNormalRoleConfig stages the R4 §6.2 valid normal role config.
func writeNormalRoleConfig(t *testing.T, dir, name, ifaceName string, addresses []string) string {
	t.Helper()
	addressList := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		addressList = append(addressList, fmt.Sprintf("%q", addr))
	}
	content := fmt.Sprintf(`name = "%s"
protocol = "openconnect"
interface = "%s"
mtu = 1380
state_dir = "%s"
address = [%s]

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
`, name, ifaceName, filepath.ToSlash(filepath.Join(dir, "state")), strings.Join(addressList, ", "))
	path := filepath.Join(dir, name+".toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// newFixtureRuntime builds a readyRuntime with the injected resolver.
func newFixtureRuntime(t *testing.T, cfgPath, stateDir string, resolver *fixtureResolver) *readyRuntime {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	return &readyRuntime{
		cfg:       cfg,
		ifaceName: cfg.Interface,
		revision:  1,
		changed:   make(chan struct{}),
		writer:    state.Writer{Dir: stateDir},
		resolve:   resolver.resolve,
	}
}

func startRuntime(t *testing.T, r *readyRuntime) {
	t.Helper()
	if err := r.Start(context.Background(), toadctl.StartRequest{Generation: 42}); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// TestReadyToadCLIRequiresRunAndConfig pins the CLI surface: exactly the run
// verb with a -config path is accepted.
func TestReadyToadCLIRequiresRunAndConfig(t *testing.T) {
	if _, err := parseRunCommand([]string{"serve", "-config", "x.toml"}); err == nil {
		t.Fatal("the run verb is mandatory")
	}
	if _, err := parseRunCommand([]string{"run"}); err == nil {
		t.Fatal("-config is mandatory")
	}
	path, err := parseRunCommand([]string{"run", "-config", "role.toml"})
	if err != nil || path != "role.toml" {
		t.Fatalf("parseRunCommand = %q, %v", path, err)
	}
}

// TestReadyToadLoadsNormalRoleConfig proves the fixture loads a valid normal
// openconnect role config (never the unsupported awg2 fixture).
func TestReadyToadLoadsNormalRoleConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("a normal openconnect role config must load: %v", err)
	}
	if cfg.Interface != "kkalpha0" || cfg.Protocol != "openconnect" {
		t.Fatalf("loaded config is not the fixture: %+v", cfg)
	}
}

// TestReadyToadStartStoresGenerationAndPublishesReady proves Start stores the
// request generation (generating one only when zero) and publishes the first
// ready snapshot with an incremented revision.
func TestReadyToadStartStoresGenerationAndPublishesReady(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)

	if err := r.Start(context.Background(), toadctl.StartRequest{Generation: 42}); err != nil {
		t.Fatalf("start: %v", err)
	}
	snap := r.Snapshot()
	if snap.Generation != 42 {
		t.Fatalf("generation = %d, want 42", snap.Generation)
	}
	if snap.State != "ready" || !snap.RouteReady {
		t.Fatalf("state = %q routeReady=%v, want ready", snap.State, snap.RouteReady)
	}
	if !snap.SessionConnected {
		t.Fatal("SessionConnected must be true while the runtime is started")
	}
	if snap.Revision <= 1 {
		t.Fatalf("the first published snapshot must increment the revision, got %d", snap.Revision)
	}

	// A zero generation is generated, not zero.
	other := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state2"), resolver)
	if err := other.Start(context.Background(), toadctl.StartRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if other.Snapshot().Generation == 0 {
		t.Fatal("a zero start generation must be replaced with a generated one")
	}
}

// TestReadyToadStartFailsWhenInterfaceMissing proves Start returns a startup
// error when the configured interface is initially absent and never publishes
// a successful degraded start.
func TestReadyToadStartFailsWhenInterfaceMissing(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{} // no interface registered: resolution fails
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)

	if err := r.Start(context.Background(), toadctl.StartRequest{Generation: 42}); err == nil {
		t.Fatal("Start with a missing interface must fail")
	}
	snap := r.Snapshot()
	if snap.State == "ready" || snap.RouteReady {
		t.Fatalf("a failed start must not publish a successful snapshot: %+v", snap)
	}
}

// TestReadyToadHandshakeCapabilities pins the capability report.
func TestReadyToadHandshakeCapabilities(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)

	resp := r.Handle(context.Background(), toadctl.Request{
		Version: toadctl.ProtocolVersion, ID: "hs", Method: "Handshake",
	})
	if !resp.OK || resp.Capabilities == nil {
		t.Fatalf("handshake failed: %+v", resp.Error)
	}
	want := toadctl.Capabilities{Validate: true, Rebind: true, RestartTransportKeepingTUN: true}
	if *resp.Capabilities != want {
		t.Fatalf("capabilities = %+v, want %+v", *resp.Capabilities, want)
	}
}

// TestReadyToadValidateHealthy proves Validate is healthy exactly when the
// resolved interface carries a usable configured family.
func TestReadyToadValidateHealthy(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)

	if v := r.Validate(context.Background()); !v.Healthy || v.State != "ready" {
		t.Fatalf("validate with a usable family = %+v, want healthy/ready", v)
	}
	// Identity change degrades the validation.
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 77, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	if v := r.Validate(context.Background()); v.Healthy || v.State != "degraded" || v.Reason != "managed interface identity changed" {
		t.Fatalf("validate after an ifindex change = %+v, want degraded identity change", v)
	}
	// Stopped runtimes validate as stopped.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := r.Validate(context.Background()); v.Healthy || v.State != "stopped" {
		t.Fatalf("validate while stopped = %+v, want stopped", v)
	}
}

// TestReadyToadFamilyReadinessIndependent proves family truth stays
// independent: readiness requires at least one current address of the family,
// never every configured prefix, and one family never implies the other.
func TestReadyToadFamilyReadinessIndependent(t *testing.T) {
	tests := []struct {
		name        string
		cfgAddrs    []string
		ifaceAddrs  []string
		wantV4Ready bool
		wantV6Ready bool
	}{
		{
			name:        "v4 configured and present",
			cfgAddrs:    []string{"10.0.0.1/24"},
			ifaceAddrs:  []string{"10.0.0.1/24", "fe80::1/64"},
			wantV4Ready: true,
		},
		{
			name:        "v4 configured absent",
			cfgAddrs:    []string{"10.0.0.1/24"},
			ifaceAddrs:  []string{"fe80::1/64"},
			wantV4Ready: false,
		},
		{
			name:        "one of two configured v4 prefixes present is still ready",
			cfgAddrs:    []string{"10.0.0.1/24", "10.0.2.1/24"},
			ifaceAddrs:  []string{"10.0.0.1/24"},
			wantV4Ready: true,
		},
		{
			name:        "v6 configured and present, v4 absent",
			cfgAddrs:    []string{"10.0.0.1/24", "2001:db8::1/64"},
			ifaceAddrs:  []string{"2001:db8::1/64"},
			wantV6Ready: true,
		},
		{
			name:       "nothing configured is never ready",
			cfgAddrs:   []string{},
			ifaceAddrs: []string{"10.0.0.1/24", "2001:db8::1/64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface := ifaceInfo{Name: "test0", IfIndex: 42, MTU: 1500, Addresses: tt.ifaceAddrs}
			cfg := &config.Config{Name: "test", Address: tt.cfgAddrs}
			families := interfaceFamilyReadiness(cfg, iface)
			if families.IPv4.Ready != tt.wantV4Ready {
				t.Errorf("IPv4.Ready = %v, want %v", families.IPv4.Ready, tt.wantV4Ready)
			}
			if families.IPv6.Ready != tt.wantV6Ready {
				t.Errorf("IPv6.Ready = %v, want %v", families.IPv6.Ready, tt.wantV6Ready)
			}
			if families.IPv4.Configured != contains(cfg.Address, func(a string) bool { return isFamily(a, true) }) {
				t.Errorf("IPv4.Configured derives only from cfg.Address: %+v", families.IPv4)
			}
		})
	}
}

func contains(list []string, pred func(string) bool) bool {
	for _, item := range list {
		if pred(item) {
			return true
		}
	}
	return false
}

func isFamily(addr string, v4 bool) bool {
	prefix, err := netip.ParsePrefix(addr)
	if err != nil {
		return false
	}
	return prefix.Addr().Is4() == v4
}

// TestReadyToadSnapshotFiltersUnconfiguredFamilyAddresses proves
// Snapshot.Addresses carries only addresses whose family the role configured.
func TestReadyToadSnapshotFiltersUnconfiguredFamilyAddresses(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{
		Name: "kkalpha0", IfIndex: 9, MTU: 1380,
		Addresses: []string{"10.99.0.1/24", "2001:db8:99::1/64", "fe80::1/64"},
	})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)

	snap := r.Snapshot()
	for _, addr := range snap.Addresses {
		if isFamily(addr, false) {
			t.Fatalf("an unconfigured IPv6 address leaked into the snapshot: %v", snap.Addresses)
		}
	}
	if len(snap.Addresses) != 1 || snap.Addresses[0] != "10.99.0.1/24" {
		t.Fatalf("addresses = %v, want only the configured family", snap.Addresses)
	}
	if snap.Families.IPv6.Configured {
		t.Fatal("IPv6 must not be configured when the role configures none")
	}
}

// TestReadyToadRebindRefreshesIfIndexAndRevision proves Rebind resamples the
// interface synchronously, preserves the generation, publishes a new revision
// even when nothing changed, and surfaces resolver failures after publishing
// degraded state.
func TestReadyToadRebindRefreshesIfIndexAndRevision(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)
	before := r.Snapshot()

	// The ifindex changed under the fixture: Rebind must adopt it.
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 12, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	if err := r.Rebind(context.Background(), toadctl.UnderlayBinding{}); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	after := r.Snapshot()
	if after.IfIndex != 12 {
		t.Fatalf("ifindex = %d, want the resampled 12", after.IfIndex)
	}
	if after.Generation != before.Generation {
		t.Fatalf("generation changed across rebind: %d -> %d", before.Generation, after.Generation)
	}
	if after.Revision <= before.Revision {
		t.Fatalf("revision must advance on rebind: %d -> %d", before.Revision, after.Revision)
	}

	// A no-change rebind still publishes a new revision (R4 §6.4).
	still := r.Snapshot()
	if err := r.Rebind(context.Background(), toadctl.UnderlayBinding{}); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if r.Snapshot().Revision <= still.Revision {
		t.Fatalf("an equal-value rebind must still increment the revision: %d -> %d", still.Revision, r.Snapshot().Revision)
	}

	// Resolver failure publishes degraded and returns the error.
	resolver.failWith(errors.New("interface vanished"))
	if err := r.Rebind(context.Background(), toadctl.UnderlayBinding{}); err == nil {
		t.Fatal("a resolver failure must return an error")
	}
	failed := r.Snapshot()
	if failed.State != "degraded" || failed.RouteReady {
		t.Fatalf("a failed rebind must publish degraded state: %+v", failed)
	}
}

// TestReadyToadRestartTransportRefreshesIfIndexAndRevision proves
// RestartTransport implements exactly the Rebind resample behavior.
func TestReadyToadRestartTransportRefreshesIfIndexAndRevision(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)
	before := r.Snapshot()

	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 15, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	if err := r.RestartTransport(context.Background(), toadctl.UnderlayBinding{}); err != nil {
		t.Fatalf("restart transport: %v", err)
	}
	after := r.Snapshot()
	if after.IfIndex != 15 {
		t.Fatalf("ifindex = %d, want the resampled 15", after.IfIndex)
	}
	if after.Generation != before.Generation || after.Revision <= before.Revision {
		t.Fatalf("restart transport must preserve the generation and advance the revision: %+v", after)
	}

	resolver.failWith(errors.New("interface vanished"))
	if err := r.RestartTransport(context.Background(), toadctl.UnderlayBinding{}); err == nil {
		t.Fatal("a resolver failure must return an error")
	}
	if r.Snapshot().State != "degraded" {
		t.Fatalf("a failed restart must publish degraded state: %+v", r.Snapshot())
	}
}

// TestReadyToadPollPublishesFamilyLossAndRestore proves the poll loop
// publishes a new revision when family readiness changes: later loss degrades
// and a later restore returns the runtime to ready, both observable through
// the revision stream.
func TestReadyToadPollPublishesFamilyLossAndRestore(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)
	if snap := r.Snapshot(); snap.State != "ready" {
		t.Fatalf("initial state = %q, want ready", snap.State)
	}
	pollCtx, stopPoll := context.WithCancel(context.Background())
	defer stopPoll()
	go r.pollLoop(pollCtx)

	// Loss: the interface address disappears; the next poll publishes degraded.
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: nil})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r.Snapshot().State == "degraded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the poll loop never published the loss: %+v", r.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Restore: the address returns; the next poll publishes ready again.
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	deadline = time.Now().Add(5 * time.Second)
	for {
		if r.Snapshot().State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the poll loop never published the restore: %+v", r.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestReadyToadSubscribeWakesOnRevisionChange proves Subscribe observers see
// a revision change created after the initial revision was recorded: the test
// changes the injected resolver result and rebinds, and the waiter wakes.
func TestReadyToadSubscribeWakesOnRevisionChange(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)

	initialRev := r.Snapshot().Revision
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create the revision change AFTER recording the initial revision.
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 21, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	go func() {
		_ = r.Rebind(context.Background(), toadctl.UnderlayBinding{})
	}()

	snap, err := r.WaitForRevision(ctx, initialRev)
	if err != nil {
		t.Fatalf("wait for revision: %v", err)
	}
	if snap.Revision <= initialRev {
		t.Fatalf("revision did not advance: initial=%d got=%d", initialRev, snap.Revision)
	}
	if snap.IfIndex != 21 {
		t.Fatalf("the subscriber snapshot must carry the resampled ifindex, got %d", snap.IfIndex)
	}
}

// TestReadyToadStopClearsSessionConnected proves Stop sets SessionConnected
// false and the state stopped.
func TestReadyToadStopClearsSessionConnected(t *testing.T) {
	dir := t.TempDir()
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)
	if !r.Snapshot().SessionConnected {
		t.Fatal("SessionConnected must be true while started")
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	if snap.SessionConnected {
		t.Fatal("SessionConnected must be false after Stop")
	}
	if snap.State != "stopped" {
		t.Fatalf("state after stop = %q, want stopped", snap.State)
	}
}

// shortBaseDir returns a temp base whose path stays inside the 108-byte
// AF_UNIX sun_path limit even with the state/control.sock suffix appended.
func shortBaseDir(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", "kk8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	return base
}

// TestReadyToadContextShutdownRemovesControlSocket proves the runtime's
// control socket is cleaned up when the context ends (the Serve contract the
// core relies on for restarts).
func TestReadyToadContextShutdownRemovesControlSocket(t *testing.T) {
	dir := shortBaseDir(t)
	resolver := &fixtureResolver{}
	resolver.set("kkalpha0", ifaceInfo{Name: "kkalpha0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.99.0.1/24"}})
	cfgPath := writeNormalRoleConfig(t, dir, "alpha", "kkalpha0", []string{"10.99.0.1/24"})
	r := newFixtureRuntime(t, cfgPath, filepath.Join(dir, "state"), resolver)
	startRuntime(t, r)

	socket := filepath.Join(dir, "state", "control.sock")
	server := toadctl.Server{Socket: socket, Handler: r}
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		select {
		case err := <-serveErr:
			t.Fatalf("Serve failed before binding: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the control socket was never created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-serveErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the control socket survived the context shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var _ = state.New
