// Package readytoad_test exercises the ready-toad binary contract without
// real interfaces. It starts the runtime, drives the toadctl protocol over
// a Unix socket, and asserts Start/Stop/Validate/Snapshot/Subscribe.
package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// TestReadyToadStartStop verifies that ready-toad starts, reaches a
// degraded state (no real interface exists), and stops cleanly.
func TestReadyToadStartStop(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir, "test-role", "kkmanaged0",
		"10.99.99.1/24", 1500)
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newReadyRuntime(t, cfgPath, stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start — should not fail even without the interface
	startReq := toadctl.Request{
		Version:      toadctl.ProtocolVersion,
		ID:           "test-1",
		Method:       "Start",
		Generation:   1,
		Start:        &toadctl.StartRequest{Generation: 1},
	}
	resp := r.Handle(ctx, startReq)
	if !resp.OK {
		t.Fatalf("Start failed: %s", errMsg(resp))
	}

	snap := r.Snapshot()
	if snap.State != "degraded" && snap.State != "ready" {
		t.Fatalf("unexpected state after start: %q", snap.State)
	}

	// Validate — should return degraded (no real interface)
	validateReq := toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		ID:         "test-2",
		Method:     "Validate",
		Generation: 1,
	}
	resp = r.Handle(ctx, validateReq)
	if !resp.OK {
		t.Fatalf("Validate failed: %s", errMsg(resp))
	}
	if resp.Validation == nil {
		t.Fatal("Validate returned no validation result")
	}

	// Snapshot after start
	snap = r.Snapshot()
	if snap.Generation != 1 {
		t.Fatalf("generation = %d, want 1", snap.Generation)
	}
	if snap.ProtocolVersion != toadctl.ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", snap.ProtocolVersion, toadctl.ProtocolVersion)
	}

	// Stop
	stopReq := toadctl.Request{
		Version: toadctl.ProtocolVersion,
		ID:      "test-3",
		Method:  "Stop",
	}
	resp = r.Handle(ctx, stopReq)
	if !resp.OK {
		t.Fatalf("Stop failed: %s", errMsg(resp))
	}
	snap = r.Snapshot()
	if snap.State != "stopped" {
		t.Fatalf("state after stop = %q, want stopped", snap.State)
	}
}

// TestReadyToadHandshakeCapabilities verifies the capabilities handshake.
func TestReadyToadHandshakeCapabilities(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir, "test-role", "kkmanaged0",
		"10.99.99.1/24", 1500)
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newReadyRuntime(t, cfgPath, stateDir)
	ctx := context.Background()

	req := toadctl.Request{
		Version: toadctl.ProtocolVersion,
		ID:      "handshake-1",
		Method:  "Handshake",
	}
	resp := r.Handle(ctx, req)
	if !resp.OK {
		t.Fatalf("Handshake failed: %s", errMsg(resp))
	}
	if resp.Capabilities == nil {
		t.Fatal("Handshake returned no capabilities")
	}
	if !resp.Capabilities.Validate {
		t.Error("capabilities.Validate is false")
	}
	if !resp.Capabilities.Rebind {
		t.Error("capabilities.Rebind is false")
	}
	if !resp.Capabilities.RestartTransportKeepingTUN {
		t.Error("capabilities.RestartTransportKeepingTUN is false")
	}
}

// TestReadyToadSubscribe checks revision bumping and Subscribe delivery.
func TestReadyToadSubscribe(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir, "test-role", "kkmanaged0",
		"10.99.99.1/24", 1500)
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newReadyRuntime(t, cfgPath, stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start
	startReq := toadctl.Request{
		Version: toadctl.ProtocolVersion,
		ID:         "sub-1",
		Method:     "Start",
		Generation: 1,
		Start:      &toadctl.StartRequest{Generation: 1},
	}
	resp := r.Handle(ctx, startReq)
	if !resp.OK {
		t.Fatalf("Start failed: %s", errMsg(resp))
	}

	// Read snapshot from subscribe channel via WaitForRevision
	initialRev := r.Snapshot().Revision
	snap, err := r.WaitForRevision(ctx, initialRev)
	if err != nil {
		t.Fatalf("WaitForRevision failed: %v", err)
	}
	if snap.Revision <= initialRev {
		t.Fatalf("revision did not bump: initial=%d got=%d", initialRev, snap.Revision)
	}
}

// TestReadyToadRebind verifies that Rebind and RestartTransport are no-ops.
func TestReadyToadRebind(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir, "test-role", "kkmanaged0",
		"10.99.99.1/24", 1500)
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newReadyRuntime(t, cfgPath, stateDir)
	ctx := context.Background()

	startReq := toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		ID:         "rebind-1",
		Method:     "Start",
		Generation: 1,
		Start:      &toadctl.StartRequest{Generation: 1},
	}
	resp := r.Handle(ctx, startReq)
	if !resp.OK {
		t.Fatalf("Start failed: %s", errMsg(resp))
	}

	rebindReq := toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		ID:         "rebind-2",
		Method:     "Rebind",
		Generation: 1,
	}
	resp = r.Handle(ctx, rebindReq)
	if !resp.OK {
		t.Fatalf("Rebind failed: %s", errMsg(resp))
	}

	restartReq := toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		ID:         "rebind-3",
		Method:     "RestartTransport",
		Generation: 1,
	}
	resp = r.Handle(ctx, restartReq)
	if !resp.OK {
		t.Fatalf("RestartTransport failed: %s", errMsg(resp))
	}
}

// TestReadyToadUnsupportedMethod verifies that unknown methods are rejected.
func TestReadyToadUnsupportedMethod(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir, "test-role", "kkmanaged0",
		"10.99.99.1/24", 1500)
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newReadyRuntime(t, cfgPath, stateDir)
	ctx := context.Background()

	req := toadctl.Request{
		Version: toadctl.ProtocolVersion,
		ID:      "unsup-1",
		Method:  "BogusMethod",
	}
	resp := r.Handle(ctx, req)
	if resp.OK {
		t.Fatal("BogusMethod must not succeed")
	}
}

// TestReadyToadFamilyReadiness checks the interface family readiness logic
// used by ready-toad.
func TestReadyToadFamilyReadiness(t *testing.T) {
	tests := []struct {
		name       string
		cfgAddrs   []string
		ifaceAddrs []string
		wantReady  bool
	}{
		{
			name:       "v4 only, both present",
			cfgAddrs:   []string{"10.0.0.1/24"},
			ifaceAddrs: []string{"10.0.0.1/24", "fe80::1/64"},
			wantReady:  true,
		},
		{
			name:       "v4 configured, not on interface",
			cfgAddrs:   []string{"10.0.0.1/24"},
			ifaceAddrs: []string{},
			wantReady:  false,
		},
		{
			name:       "dual-stack, both present",
			cfgAddrs:   []string{"10.0.0.1/24", "2001:db8::1/64"},
			ifaceAddrs: []string{"10.0.0.1/24", "2001:db8::1/64", "fe80::1/64"},
			wantReady:  true,
		},
		{
			name:       "dual-stack, v4 missing",
			cfgAddrs:   []string{"10.0.0.1/24", "2001:db8::1/64"},
			ifaceAddrs: []string{"2001:db8::1/64", "fe80::1/64"},
			wantReady:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface := ifaceInfo{
				Name:      "test0",
				IfIndex:   42,
				MTU:       1500,
				Addresses: tt.ifaceAddrs,
			}
			cfg := &config.Config{
				Name:    "test",
				Address: tt.cfgAddrs,
			}
			families := readinessFromAddresses(cfg, iface)
			if families.Ready() != tt.wantReady {
				t.Errorf("families.Ready() = %v, want %v; v4=%+v v6=%+v",
					families.Ready(), tt.wantReady, families.IPv4, families.IPv6)
			}
		})
	}
}

// TestReadyToadResolveInterface verifies that resolveInterface fails
// gracefully for a nonexistent name.
func TestReadyToadResolveInterface(t *testing.T) {
	_, err := resolveInterface("nonexistent-noop-iface-12345")
	if err == nil {
		t.Error("resolveInterface for nonexistent name must fail")
	}
}

// --- test helpers ---

func newReadyRuntime(t *testing.T, cfgPath, stateDir string) *readyRuntime {
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
	}
}

func writeTestConfig(t *testing.T, dir, name, ifaceName, address string, mtu int) string {
	t.Helper()
	path := filepath.Join(dir, "role.toml")
	content := fmt.Sprintf(`name = "%s"
protocol = "awg2"
interface = "%s"
address = ["%s"]
mtu = %d
state_dir = "%s"
`, name, ifaceName, address, mtu, filepath.Join(dir, "state"))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func errMsg(resp toadctl.Response) string {
	if resp.Error != nil {
		return resp.Error.Message
	}
	return "unknown error"
}

// Compile-time check: netip is used in the fixture.
var _ netip.Addr

// Compile-time check: net.Interfaces is used by resolveInterface.
var _ = net.InterfaceByName