package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// R8B §2.8b (2026-11-17 residual audit): one runServe ordering test that
// proves the persisted desired/default policy validation happened BEFORE the
// initial route-authority proof, and that an all-off startup drains the
// dormant owner's synthetic zone instead of activating it.

// countingRouteManager wraps the real platform writer and records the
// selected-route writer handout plus every snapshot the reconciler's proof
// takes. It satisfies platform.SelectedRouteObservation through the
// embedded interface, so the proof's subscription attempt reaches the real
// writer unchanged.
type countingRouteManager struct {
	platform.RouteManager
	mu        sync.Mutex
	handouts  int
	snapshots int
}

func (c *countingRouteManager) Snapshot(ctx context.Context) (routing.KernelState, error) {
	c.mu.Lock()
	c.snapshots++
	c.mu.Unlock()
	return c.RouteManager.Snapshot(ctx)
}

func (c *countingRouteManager) counts() (handouts, snapshots int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handouts, c.snapshots
}

// startupOrderServeOptions extends the hermetic serve options with the R8B
// §2.8 startup inputs: a state dir (desired.json), the go+go ownership gate,
// a dormant-owner bypass.conf and a no-op Leshy reload (the harness never
// restarts a system Leshy from a unit test).
func startupOrderServeOptions(t *testing.T, roles map[string]bool, bypassOwner string) (serveOptions, string) {
	t.Helper()
	opts, root := hermeticServeOptions(t)
	hermeticServeConfig(t, root)
	// A second role so a contradiction is expressible: with one role,
	// desired=false is simply the all-off.
	password := filepath.Join(root, "oc.password")
	two := fmt.Sprintf(`name = "two"
protocol = "openconnect"
interface = "kktwo"
mtu = 1380
state_dir = %q
address = ["10.0.0.2/24"]

[openconnect]
gateway = "vpn.example.test"
username = "tester"
vpn_protocol = "anyconnect"
auth_group = ""
password_file = %q
token_mode = "none"
token_secret_file = ""
user_agent = ""
server_cert = ""
disable_udp = false
disable_ipv6 = false
`, root, password)
	if err := os.WriteFile(filepath.Join(root, "configs", "two.toml"), []byte(two), 0o600); err != nil {
		t.Fatal(err)
	}

	opts.configs = []string{
		filepath.Join(root, "configs", "one.toml"),
		filepath.Join(root, "configs", "two.toml"),
	}

	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	opts.stateDir = stateDir
	desired := fmt.Sprintf(`{"schema":1,"active_profile":"default","roles":{%s}}`, desiredRolesJSON(roles))
	if err := os.WriteFile(filepath.Join(stateDir, "desired.json"), []byte(desired), 0o600); err != nil {
		t.Fatal(err)
	}
	if bypassOwner != "" {
		if err := config.WriteBypassMode(opts.bypassConfig, bypassOwner); err != nil {
			t.Fatalf("seed bypass.conf: %v", err)
		}
	}
	ownership := filepath.Join(root, "ownership.toml")
	if err := os.WriteFile(ownership, []byte("routing_owner = \"go\"\ntunnel_owner = \"go\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.ownershipConfig = ownership
	noReload := false
	opts.leshyReload = &noReload
	return opts, root
}

func desiredRolesJSON(roles map[string]bool) string {
	parts := make([]string, 0, len(roles))
	for name, on := range roles {
		parts = append(parts, fmt.Sprintf("%q:%v", name, on))
	}
	return strings.Join(parts, ",")
}

// installCountingWriter swaps the runServe writer factory for the counting
// wrapper and restores it at cleanup.
func installCountingWriter(t *testing.T) *countingRouteManager {
	t.Helper()
	counter := &countingRouteManager{}
	previous := defaultRouteManager
	defaultRouteManager = func() (platform.RouteManager, routing.Executor) {
		counter.mu.Lock()
		counter.handouts++
		counter.mu.Unlock()
		t.Logf("factory: handout #%d", counter.handouts)
		routes, executor := platform.DefaultRouteManager()
		counter.RouteManager = routes
		return counter, executor
	}
	t.Cleanup(func() { defaultRouteManager = previous })
	return counter
}

func TestRunServeValidatesDesiredPolicyBeforeAuthorityProof(t *testing.T) {
	// The contradiction: bypass owner "one" desired=false while the sibling
	// "two" stays desired. Startup must fail with the named validation error
	// and the route-authority proof must never run (R8B §2.8b).
	opts, _ := startupOrderServeOptions(t, map[string]bool{"one": false, "two": true}, "one")
	counter := installCountingWriter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, opts) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the contradictory policy must fail startup")
		}
		if !strings.Contains(err.Error(), "validate the persisted desired/default policy") {
			t.Fatalf("startup failed with the wrong error: %v", err)
		}
	case <-time.After(30 * time.Second):
		cancel()
		<-done
		t.Fatal("runServe did not fail within the deadline; the validation ran after the proof")
	}
	// The ordering proof: zero writer handouts means the reconciler was
	// never installed, so no authority proof ran before the validation.
	if handouts, _ := counter.counts(); handouts != 0 {
		t.Fatalf("the route writer was handed out %d time(s) before the validation failed", handouts)
	}
}

func TestRunServeAllOffStartsWithDormantOwner(t *testing.T) {
	// R8B §2.8 permanent rows: all roles desired=false + bypass=role →
	// startup succeeds all-off with an effective-direct policy; the dormant
	// owner's synthetic zone leaves the generated config; no VPN starts.
	opts, _ := startupOrderServeOptions(t, map[string]bool{"one": false, "two": false}, "one")
	counter := installCountingWriter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, opts) }()

	// The generated config first materializes with the dormant owner's zone
	// (inside StartupManagedAuthority) and is re-rendered by the drain: wait
	// for the effective-direct render, then assert the state.
	if data, err := os.ReadFile(opts.bypassConfig); err != nil {
		t.Fatalf("seeded bypass missing: %v", err)
	} else {
		t.Logf("seeded bypass.conf = %q", data)
	}
	deadline := time.Now().Add(30 * time.Second)
	drained := false
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			cancel()
			if data, readErr := os.ReadFile(opts.leshyConfig); readErr == nil {
				t.Fatalf("all-off startup failed: %v\nCONFIG:\n%s", err, data)
			}
			t.Fatalf("all-off startup failed: %v", err)
		default:
		}
		// ReadLeshyZones reports a missing file as (nil, nil): require the
		// file to exist before trusting a zero-zone parse. The drained
		// render legitimately has zero zones here (no routing lists exist in
		// the hermetic dir and the bypass zone left the config).
		if _, statErr := os.Stat(opts.leshyConfig); statErr == nil {
			zones, err := config.ReadLeshyZones(opts.leshyConfig)
			if err == nil {
				bypass := false
				for _, zone := range zones {
					if zone.Name == config.BypassList {
						bypass = true
					}
				}
				t.Logf("poll zones=%v bypassZone=%v", len(zones), bypass)
				if !bypass {
					drained = true
					break
				}
			} else {
				t.Logf("poll parse: %v", err)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !drained {
		cancel()
		<-done
		t.Fatalf("the dormant owner's synthetic zone was never drained from %s", opts.leshyConfig)
	}
	// The writer was handed out after the drain (the reconciler was
	// installed): on platforms without a selected-route writer the proof is
	// vacuous, so the handout — not a snapshot — is the ordering evidence.
	if handouts, _ := counter.counts(); handouts == 0 {
		t.Fatal("the route writer was never handed out; the reconciler never started")
	}
	// The operator configuration survived the all-off startup untouched.
	mode, err := os.ReadFile(opts.bypassConfig)
	if err != nil {
		t.Fatalf("read back bypass.conf: %v", err)
	}
	if strings.TrimSpace(string(mode)) != "one" {
		t.Fatalf("bypass.conf was rewritten by the all-off startup: %q", mode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("all-off serve exited with error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("all-off serve did not stop after context cancel")
	}
}
