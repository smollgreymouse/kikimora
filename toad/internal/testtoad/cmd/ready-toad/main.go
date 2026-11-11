// Package main implements ready-toad: a deterministic fixture Toad for
// managed-success E2E tests (08D1 Phase F, §12.1). It loads a normal role
// TOML, resolves the configured interface, publishes real ifindex/addresses,
// samples every 100ms, reports independent IPv4/IPv6 readiness, and supports
// the full Start/Validate/Rebind/RestartTransportKeepingTUN lifecycle.
// No VPN transport is established; no route mutation is performed.
//
// Usage:
//
//	ready-toad run -config <role.toml>
//
// The fixture is used as kikimora-core --toad-binary on both Linux and Windows.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

const (
	pollInterval = 100 * time.Millisecond
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: ready-toad run -config <role.toml>")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	path := fs.String("config", "", "path to per-instance TOML config")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := &readyRuntime{
		cfg:          cfg,
		ifaceName:    cfg.Interface,
		revision:     1,
		changed:      make(chan struct{}),
		writer:       state.Writer{Dir: cfg.StateDir},
	}

	if err := r.Start(ctx, toadctl.StartRequest{Generation: uint64(time.Now().UnixNano())}); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	go r.pollLoop(ctx)

	server := toadctl.Server{
		Socket:  filepath.Join(cfg.StateDir, "control.sock"),
		Handler: r,
	}
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// readyRuntime implements the toadctl.Handler contract without a VPN backend.
type readyRuntime struct {
	mu        sync.Mutex
	cfg       *config.Config
	ifaceName string
	generation uint64
	started   bool
	state     state.Snapshot
	revision  uint64
	changed   chan struct{}
	writer    state.Writer
}

func (r *readyRuntime) Start(ctx context.Context, req toadctl.StartRequest) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil
	}
	generation := req.Generation
	if generation == 0 {
		generation = uint64(time.Now().UnixNano())
	}
	r.generation = generation
	r.started = true
	r.mu.Unlock()

	iface, err := resolveInterface(r.ifaceName)
	if err != nil {
		r.mu.Lock()
		r.state = state.New(r.cfg.Name, string(r.cfg.Protocol), r.ifaceName, 0)
		r.state.State = "degraded"
		r.state.Reason = fmt.Sprintf("managed interface not found: %v", err)
		r.bumpLocked()
		r.mu.Unlock()
		_ = r.publish()
		return nil
	}
	r.mu.Lock()
	r.state = buildSnapshot(r.cfg, iface, r.generation)
	r.bumpLocked()
	r.mu.Unlock()
	return r.publish()
}

func (r *readyRuntime) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	r.started = false
	r.state.State = "stopped"
	r.state.Reason = "stopped by control request"
	r.bumpLocked()
	return r.publishLocked()
}

func (r *readyRuntime) Quiesce(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	r.state.State = "quiesced"
	r.state.Reason = "transport quiesced"
	r.bumpLocked()
	return r.publishLocked()
}

func (r *readyRuntime) Validate(ctx context.Context) toadctl.ValidationResult {
	r.mu.Lock()
	started := r.started
	cfg := r.cfg
	ifaceName := r.ifaceName
	expectedIfIndex := r.state.Interface.IfIndex
	r.mu.Unlock()

	if !started {
		return toadctl.ValidationResult{State: "stopped", Reason: "runtime is stopped"}
	}

	iface, err := resolveInterface(ifaceName)
	if err != nil {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed interface is unavailable", Families: toadctl.RouteFamilies{
			IPv4: toadctl.FamilyReadiness{Reason: "interface not found"},
			IPv6: toadctl.FamilyReadiness{Reason: "interface not found"},
		}}
	}
	if expectedIfIndex > 0 && iface.IfIndex != expectedIfIndex {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed interface identity changed"}
	}
	families := readinessFromInterface(cfg, iface)
	if !families.Ready() {
		return toadctl.ValidationResult{
			Healthy: false, State: "degraded",
			Reason:   "no configured address family is usable on the managed interface",
			Families: families,
		}
	}
	return toadctl.ValidationResult{
		Healthy: true, State: "ready",
		Reason: "managed route target is structurally ready", Families: families,
	}
}

func (r *readyRuntime) Rebind(_ context.Context, _ toadctl.UnderlayBinding) error {
	return nil // ready-toad has no transport to rebind
}

func (r *readyRuntime) RestartTransport(_ context.Context, _ toadctl.UnderlayBinding) error {
	return nil // ready-toad has no transport to restart
}

func (r *readyRuntime) Snapshot() toadctl.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toSnapshotLocked()
}

func (r *readyRuntime) WaitForRevision(ctx context.Context, rev uint64) (toadctl.Snapshot, error) {
	for {
		r.mu.Lock()
		if r.revision > rev {
			s := r.toSnapshotLocked()
			r.mu.Unlock()
			return s, nil
		}
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return toadctl.Snapshot{}, ctx.Err()
		}
	}
}

func (r *readyRuntime) Handle(ctx context.Context, req toadctl.Request) toadctl.Response {
	res := toadctl.Response{Version: toadctl.ProtocolVersion, ID: req.ID}
	switch req.Method {
	case "Handshake":
		res.OK = true
		caps := toadctl.Capabilities{Validate: true, Rebind: true, RestartTransportKeepingTUN: true}
		res.Capabilities = &caps
	case "Start":
		startReq := req.Start
		if startReq == nil {
			startReq = &toadctl.StartRequest{Generation: req.Generation}
		}
		if err := r.Start(ctx, *startReq); err != nil {
			return fail(res, "start_failed", err)
		}
		res.OK = true
	case "Quiesce":
		if err := r.Quiesce(ctx); err != nil {
			return fail(res, "quiesce_failed", err)
		}
		res.OK = true
	case "Stop":
		if err := r.Stop(ctx); err != nil {
			return fail(res, "stop_failed", err)
		}
		res.OK = true
	case "Snapshot", "Subscribe", "Inspect":
		res.OK = true
	case "Validate":
		v := r.Validate(ctx)
		res.OK = true
		res.Validation = &v
	case "Rebind":
		if err := r.Rebind(ctx, req.BindingValue()); err != nil {
			return fail(res, "rebind_failed", err)
		}
		res.OK = true
	case "RestartTransport":
		if err := r.RestartTransport(ctx, req.BindingValue()); err != nil {
			return fail(res, "restart_transport_failed", err)
		}
		res.OK = true
	default:
		return fail(res, "unsupported_method", fmt.Errorf("unsupported method %q", req.Method))
	}
	snap := r.Snapshot()
	res.Snapshot = &snap
	return res
}

func (r *readyRuntime) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pollOnce(ctx)
		}
	}
}

func (r *readyRuntime) pollOnce(_ context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return
	}
	knownIfIndex := r.state.Interface.IfIndex
	iface, err := resolveInterface(r.ifaceName)
	if err != nil {
		if knownIfIndex != 0 || r.state.State != "degraded" || r.state.State == "" {
			r.state = state.New(r.cfg.Name, string(r.cfg.Protocol), r.ifaceName, 0)
			r.state.State = "degraded"
			r.state.Reason = fmt.Sprintf("managed interface not found: %v", err)
			r.state.RouteReady = false
			r.bumpLocked()
			_ = r.publishLocked()
		}
		return
	}
	if knownIfIndex > 0 && iface.IfIndex > 0 && iface.IfIndex != knownIfIndex {
		r.state = buildSnapshot(r.cfg, iface, r.generation)
		r.state.State = "degraded"
		r.state.Reason = "managed interface identity changed"
		r.state.RouteReady = false
		r.bumpLocked()
		_ = r.publishLocked()
		return
	}
	next := buildSnapshot(r.cfg, iface, r.generation)
	if next.State != r.state.State || next.Reason != r.state.Reason ||
		next.RouteReady != r.state.RouteReady || next.Families != r.state.Families ||
		!sameInterface(next.Interface, r.state.Interface) {
		r.state = next
		r.bumpLocked()
		_ = r.publishLocked()
	}
}

func (r *readyRuntime) toSnapshotLocked() toadctl.Snapshot {
	return toadctl.Snapshot{
		ProtocolVersion: toadctl.ProtocolVersion,
		Revision:        r.revision,
		Generation:      r.generation,
		State:           r.state.State,
		Reason:          r.state.Reason,
		InterfaceName:   r.state.Interface.Name,
		IfIndex:         r.state.Interface.IfIndex,
		MTU:             r.state.Interface.MTU,
		Addresses:       append([]string(nil), r.state.Interface.Addresses...),
		RouteReady:      r.state.RouteReady,
		Families:        r.state.Families,
		Capabilities:    toadctl.Capabilities{Validate: true, Rebind: true, RestartTransportKeepingTUN: true},
		UpdatedAt:       r.state.UpdatedAt,
	}
}

func (r *readyRuntime) bumpLocked() {
	r.revision++
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *readyRuntime) publish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publishLocked()
}

func (r *readyRuntime) publishLocked() error {
	if !r.started && r.state.State == "" {
		return nil
	}
	return r.writer.Write(r.state)
}

// helpers

type ifaceInfo struct {
	Name      string
	IfIndex   int
	MTU       int
	Addresses []string
}

func resolveInterface(name string) (ifaceInfo, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ifaceInfo{}, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ifaceInfo{}, fmt.Errorf("read addresses for %s: %w", name, err)
	}
	addresses := make([]string, 0, len(addrs))
	for _, raw := range addrs {
		prefix, err := netip.ParsePrefix(raw.String())
		if err != nil {
			continue
		}
		addresses = append(addresses, prefix.String())
	}
	sort.Strings(addresses)
	return ifaceInfo{
		Name: iface.Name, IfIndex: iface.Index, MTU: iface.MTU, Addresses: addresses,
	}, nil
}

func buildSnapshot(cfg *config.Config, iface ifaceInfo, generation uint64) state.Snapshot {
	s := state.New(cfg.Name, string(cfg.Protocol), iface.Name, iface.MTU)
	s.Generation = generation
	s.Interface.IfIndex = iface.IfIndex
	s.Interface.Addresses = append([]string(nil), iface.Addresses...)
	families := readinessFromAddresses(cfg, iface)
	s.Families = families
	s.RouteReady = families.Ready()
	if s.RouteReady {
		s.State = "ready"
		s.Reason = "managed route target is structurally ready"
	} else {
		s.State = "degraded"
		s.Reason = "no configured address family is usable on the managed interface"
	}
	return s
}

func readinessFromAddresses(cfg *config.Config, iface ifaceInfo) toadctl.RouteFamilies {
	if iface.IfIndex <= 0 {
		return toadctl.RouteFamilies{
			IPv4: toadctl.FamilyReadiness{Reason: "interface not found"},
			IPv6: toadctl.FamilyReadiness{Reason: "interface not found"},
		}
	}
	return interfaceFamilyReadiness(cfg, iface)
}

func readinessFromInterface(cfg *config.Config, iface ifaceInfo) toadctl.RouteFamilies {
	if iface.IfIndex <= 0 {
		return toadctl.RouteFamilies{
			IPv4: toadctl.FamilyReadiness{Reason: "interface not found"},
			IPv6: toadctl.FamilyReadiness{Reason: "interface not found"},
		}
	}
	return interfaceFamilyReadiness(cfg, iface)
}

func interfaceFamilyReadiness(cfg *config.Config, iface ifaceInfo) toadctl.RouteFamilies {
	var families toadctl.RouteFamilies
	observed := make(map[netip.Prefix]struct{}, len(iface.Addresses))
	for _, raw := range iface.Addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil {
			observed[prefix] = struct{}{}
		}
	}
	var configuredV4, configuredV6 int
	var missingV4, missingV6 bool
	for _, raw := range cfg.Address {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		if prefix.Addr().Is4() {
			configuredV4++
			if _, ok := observed[prefix]; !ok {
				missingV4 = true
			}
		} else {
			configuredV6++
			if _, ok := observed[prefix]; !ok {
				missingV6 = true
			}
		}
	}
	families.IPv4 = toadctl.FamilyReadiness{
		Configured: configuredV4 > 0,
		Ready:      configuredV4 > 0 && !missingV4,
		Reason:     notObservedReason(configuredV4 > 0, !missingV4, "a configured IPv4 prefix is missing from the interface"),
	}
	families.IPv6 = toadctl.FamilyReadiness{
		Configured: configuredV6 > 0,
		Ready:      configuredV6 > 0 && !missingV6,
		Reason:     notObservedReason(configuredV6 > 0, !missingV6, "a configured IPv6 prefix is missing from the interface"),
	}
	return families
}

func notObservedReason(configured, observed bool, missing string) string {
	if !configured || observed {
		return ""
	}
	return missing
}

func sameInterface(a, b state.InterfaceState) bool {
	if a.Name != b.Name || a.IfIndex != b.IfIndex || a.MTU != b.MTU || len(a.Addresses) != len(b.Addresses) {
		return false
	}
	aa := append([]string(nil), a.Addresses...)
	bb := append([]string(nil), b.Addresses...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func fail(response toadctl.Response, code string, err error) toadctl.Response {
	response.OK = false
	response.Error = &toadctl.APIError{Code: code, Message: err.Error(), Retryable: true}
	return response
}

// state.Snapshot is used for both disk snapshots and diagnostics.
// state.New initialises the snapshot with the role name, protocol and interface.
var _ = state.New