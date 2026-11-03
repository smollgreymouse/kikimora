// Package toadruntime owns one long-lived protocol instance and exposes the
// narrow control contract used by kikimora-core.
package toadruntime

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type Interface struct {
	Name      string
	IfIndex   int
	MTU       int
	Addresses []string
}
type InterfaceReader func() (Interface, error)

type Runtime struct {
	mu             sync.Mutex
	cfg            *config.Config
	generation     uint64
	tunnel         platform.Tunnel
	backend        backend.Backend
	readInterface  InterfaceReader
	state          state.Snapshot
	revision       uint64
	changed        chan struct{}
	started        bool
	writer         state.Writer
	endpoints      []toadctl.TransportEndpoint
	repairer       platform.InterfaceRepairer
	repairInterval time.Duration
	nextRepair     time.Time
}

func New(cfg *config.Config, b backend.Backend, tunnel platform.Tunnel, read InterfaceReader) *Runtime {
	var stateDir string
	if cfg != nil {
		stateDir = cfg.StateDir
	}
	return &Runtime{
		cfg:            cfg,
		backend:        b,
		tunnel:         tunnel,
		readInterface:  read,
		revision:       1,
		changed:        make(chan struct{}),
		writer:         state.Writer{Dir: stateDir},
		repairInterval: time.Second,
	}
}

func (r *Runtime) SetInterfaceRepairer(repairer platform.InterfaceRepairer) {
	r.mu.Lock()
	r.repairer = repairer
	r.mu.Unlock()
}

func (r *Runtime) Start(ctx context.Context, req toadctl.StartRequest) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if r.backend == nil || r.cfg == nil || r.readInterface == nil {
		return fmt.Errorf("runtime is incomplete")
	}
	if err := r.backend.Start(ctx); err != nil {
		return err
	}
	r.refreshEndpoints(ctx)
	iface, err := waitInterface(ctx, r.readInterface, interfaceTimeout(r.cfg.Protocol))
	if err != nil {
		_ = r.backend.Close()
		return err
	}
	r.mu.Lock()
	r.generation = req.Generation
	if r.generation == 0 {
		r.generation = uint64(time.Now().UnixNano())
	}
	families := r.currentFamiliesLocked(ctx, iface)
	r.state = fromHealth(r.cfg, iface, r.backend.Health(ctx), r.generation, families)
	r.started = true
	r.bump()
	r.mu.Unlock()
	return r.publish()
}
func (r *Runtime) Quiesce(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	r.state.State = "quiesced"
	r.state.Reason = "transport quiesced"
	r.bump()
	return r.publishLocked()
}
func (r *Runtime) Stop(_ context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	r.started = false
	r.state.State = "stopped"
	r.state.Reason = "stopped by control request"
	r.bump()
	err := r.publishLocked()
	r.mu.Unlock()
	if r.backend != nil {
		if closeErr := r.backend.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}
func (r *Runtime) Validate(ctx context.Context) toadctl.ValidationResult {
	r.mu.Lock()
	started := r.started
	cfg := r.cfg
	read := r.readInterface
	expectedIfIndex := r.state.Interface.IfIndex
	r.mu.Unlock()
	if !started {
		return toadctl.ValidationResult{State: "stopped", Reason: "runtime is stopped"}
	}
	if read == nil {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed interface reader is unavailable"}
	}
	iface, err := read()
	if err != nil {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed interface is unavailable"}
	}
	if !interfaceIdentityReady(cfg, iface) {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed route target is structurally unavailable"}
	}
	if expectedIfIndex > 0 && iface.IfIndex != expectedIfIndex {
		return toadctl.ValidationResult{State: "degraded", Reason: "managed interface identity changed"}
	}
	families := r.currentFamiliesLocked(ctx, iface)
	if validator, ok := r.backend.(backend.Validator); ok {
		v := validator.Validate(ctx)
		if !v.Healthy {
			// Backend health is transport-wide: every configured family
			// stops being a usable route target with the backend's reason.
			healthy := toadctl.RouteFamilies{
				IPv4: toadctl.FamilyReadiness{Configured: families.IPv4.Configured, Reason: v.Reason},
				IPv6: toadctl.FamilyReadiness{Configured: families.IPv6.Configured, Reason: v.Reason},
			}
			return toadctl.ValidationResult{Healthy: false, State: v.State, Reason: v.Reason, Families: healthy}
		}
	}
	if !families.Ready() {
		return toadctl.ValidationResult{
			Healthy:  false,
			State:    "degraded",
			Reason:   "no configured address family is usable on the managed interface",
			Families: families,
		}
	}
	return toadctl.ValidationResult{Healthy: true, State: "ready", Reason: "managed route target is structurally ready", Families: families}
}
func (r *Runtime) Rebind(ctx context.Context, binding toadctl.UnderlayBinding) error {
	rebindable, ok := r.backend.(backend.Rebindable)
	if !ok {
		return &toadctl.APIError{Code: "capability_unsupported", Message: "rebind is not supported by this Toad", Retryable: false}
	}
	return rebindable.Rebind(ctx, binding)
}
func (r *Runtime) RestartTransport(ctx context.Context, binding toadctl.UnderlayBinding) error {
	restartable, ok := r.backend.(backend.TransportRestarter)
	if !ok {
		return &toadctl.APIError{Code: "capability_unsupported", Message: "transport restart is not supported by this Toad", Retryable: false}
	}
	return restartable.RestartTransport(ctx, binding)
}
func (r *Runtime) Snapshot() toadctl.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toadSnapshotLocked()
}
func (r *Runtime) WaitForRevision(ctx context.Context, rev uint64) (toadctl.Snapshot, error) {
	for {
		r.mu.Lock()
		if r.revision > rev {
			s := r.toadSnapshotLocked()
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
func (r *Runtime) Handle(ctx context.Context, req toadctl.Request) toadctl.Response {
	res := toadctl.Response{Version: toadctl.ProtocolVersion, ID: req.ID}
	switch req.Method {
	case "Quiesce", "Stop", "Validate", "Rebind", "RestartTransport":
		if err := r.requireGeneration(req.Generation); err != nil {
			return fail(res, "stale_generation", err)
		}
	}
	switch req.Method {
	case "Handshake":
		caps := r.capabilities()
		res.OK = true
		res.Capabilities = &caps
	case "Start":
		if req.Start == nil {
			req.Start = &toadctl.StartRequest{Generation: req.Generation}
		}
		if err := r.Start(ctx, *req.Start); err != nil {
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
	case "Inspect", "Snapshot", "Subscribe":
		res.OK = true
	case "Validate":
		v := r.Validate(ctx)
		res.OK = true
		res.Validation = &v
	case "Rebind":
		if err := r.Rebind(ctx, req.BindingValue()); err != nil {
			return fail(res, "capability_unsupported", err)
		}
		res.OK = true
	case "RestartTransport":
		if err := r.RestartTransport(ctx, req.BindingValue()); err != nil {
			return fail(res, "capability_unsupported", err)
		}
		res.OK = true
	default:
		return fail(res, "unsupported_method", fmt.Errorf("unsupported method %q", req.Method))
	}
	snap := r.Snapshot()
	res.Snapshot = &snap
	return res
}

const healthLoopInterval = 250 * time.Millisecond

func (r *Runtime) RunHealthLoop(ctx context.Context) error {
	ticker := time.NewTicker(healthLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.mu.Lock()
			if r.started {
				h := r.backend.Health(ctx)
				knownIfIndex := r.state.Interface.IfIndex
				iface, err := r.readInterface()
				if err != nil {
					iface = Interface{Name: r.cfg.Interface}
				}
				families := r.currentFamiliesLocked(ctx, iface)
				structuralReady := interfaceIdentityReady(r.cfg, iface) && families.Ready()
				next := fromHealth(r.cfg, iface, h, r.generation, families)
				if err != nil {
					next.State = "degraded"
					next.Reason = "managed interface is unavailable"
					next.RouteReady = false
				} else if knownIfIndex > 0 && iface.IfIndex > 0 && iface.IfIndex != knownIfIndex {
					next.State = "degraded"
					next.Reason = "managed interface identity changed"
					next.RouteReady = false
				} else if !structuralReady && r.cfg.Protocol == config.ProtocolOpenConnect {
					// OpenConnect addresses are negotiated by the server. Never inject a
					// previously observed address back with netlink: a missing negotiated
					// address is transport/session drift and must be recovered by the
					// control plane.
					next.State = "degraded"
					next.Reason = "OpenConnect negotiated interface drift requires transport recovery"
				} else if !structuralReady && iface.IfIndex > 0 && r.repairer != nil {
					next.State = "degraded"
					next.Reason = "managed interface drift detected"
					next.RouteReady = false
					now := time.Now()
					if r.state.RouteReady {
						// Publish one fail-closed structural snapshot before mutating the
						// interface. Without this hand-off, a successful same-tick repair
						// overwrites the only evidence that RouteReady ever dropped.
						r.nextRepair = now.Add(healthLoopInterval)
					} else if !now.Before(r.nextRepair) {
						if reporter, ok := r.backend.(backend.LocalInterfaceReporter); ok {
							interval := r.repairInterval
							if interval <= 0 {
								interval = time.Second
							}
							// Rate-limit every attempted repair, including expectation and
							// reread failures. Otherwise a nominally successful mutation that
							// does not restore structural readiness can run every health tick.
							r.nextRepair = now.Add(interval)
							expected, expectationErr := reporter.LocalInterfaceExpectation(ctx)
							if expectationErr != nil {
								next.Reason = "managed interface drift expectation unavailable"
							} else {
								expected.IfIndex = knownIfIndex
								if repairErr := r.repairer.RepairInterface(ctx, r.cfg.Interface, expected); repairErr != nil {
									next.Reason = "managed interface drift repair failed"
								} else if repaired, readErr := r.readInterface(); readErr != nil {
									next.Reason = "managed interface drift reread failed"
								} else if knownIfIndex > 0 && repaired.IfIndex != knownIfIndex {
									families = r.currentFamiliesLocked(ctx, repaired)
									next = fromHealth(r.cfg, repaired, h, r.generation, families)
									next.State = "degraded"
									next.Reason = "managed interface identity changed"
									next.RouteReady = false
								} else {
									iface = repaired
									families = r.currentFamiliesLocked(ctx, iface)
									next = fromHealth(r.cfg, iface, h, r.generation, families)
									if families.Ready() {
										r.nextRepair = time.Time{}
									} else {
										next.State = "degraded"
										next.Reason = "managed interface drift persists after repair"
										next.RouteReady = false
									}
								}
							}
						}
					}
				}
				if next.State != r.state.State || next.Reason != r.state.Reason || !sameInterface(next.Interface, r.state.Interface) || next.RouteReady != r.state.RouteReady || next.Families != r.state.Families || !sameSession(next.Session, r.state.Session) {
					r.state = next
					r.bump()
					_ = r.publishLocked()
				}
			}
			r.mu.Unlock()
		}
	}
}
func (r *Runtime) Close() error { return r.Stop(context.Background()) }
func (r *Runtime) capabilities() toadctl.Capabilities {
	_, reports := r.backend.(backend.EndpointReporter)
	_, rebind := r.backend.(backend.Rebindable)
	_, restart := r.backend.(backend.TransportRestarter)
	return toadctl.Capabilities{Validate: true, Rebind: rebind, RestartTransportKeepingTUN: restart, ReportsLiveEndpoints: reports}
}
func (r *Runtime) publish() error { r.mu.Lock(); defer r.mu.Unlock(); return r.publishLocked() }
func (r *Runtime) publishLocked() error {
	if !r.started && r.state.State == "" {
		return nil
	}
	return r.writer.Write(r.state)
}
func (r *Runtime) toadSnapshotLocked() toadctl.Snapshot {
	return toadctl.Snapshot{
		ProtocolVersion:    toadctl.ProtocolVersion,
		Revision:           r.revision,
		Generation:         r.generation,
		State:              r.state.State,
		Reason:             r.state.Reason,
		InterfaceName:      r.state.Interface.Name,
		IfIndex:            r.state.Interface.IfIndex,
		MTU:                r.state.Interface.MTU,
		Addresses:          append([]string(nil), r.state.Interface.Addresses...),
		RouteReady:         r.state.RouteReady,
		Families:           r.state.Families,
		SessionConnected:   r.state.Session.Connected,
		LastHandshakeAgeMS: r.state.Session.LastHandshakeAgeMS,
		RXBytes:            r.state.Session.RXBytes,
		TXBytes:            r.state.Session.TXBytes,
		Endpoint:           r.state.Session.Endpoint,
		Capabilities:       r.capabilities(),
		Endpoints:          append([]toadctl.TransportEndpoint(nil), r.endpoints...),
		UpdatedAt:          r.state.UpdatedAt,
	}
}

func (r *Runtime) refreshEndpoints(ctx context.Context) {
	reporter, ok := r.backend.(backend.EndpointReporter)
	if !ok {
		return
	}
	values, err := reporter.TransportEndpoints(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints = r.endpoints[:0]
	for _, value := range values {
		r.endpoints = append(r.endpoints, toadctl.TransportEndpoint{Network: value.Network, Address: value.Address, Hostname: value.Hostname, Port: value.Port, ObservedAt: value.ObservedAt, Active: value.Active})
	}
}

func sameSession(a, b state.SessionState) bool {
	if a.Connected != b.Connected || a.RXBytes != b.RXBytes || a.TXBytes != b.TXBytes || a.Endpoint != b.Endpoint {
		return false
	}
	if a.LastHandshakeAgeMS == nil || b.LastHandshakeAgeMS == nil {
		return a.LastHandshakeAgeMS == nil && b.LastHandshakeAgeMS == nil
	}
	return *a.LastHandshakeAgeMS == *b.LastHandshakeAgeMS
}
func (r *Runtime) bump() { r.revision++; close(r.changed); r.changed = make(chan struct{}) }
func interfaceTimeout(p config.Protocol) time.Duration {
	if p == config.ProtocolOpenConnect {
		return 30 * time.Second
	}
	return 3 * time.Second
}
func waitInterface(ctx context.Context, read InterfaceReader, timeout time.Duration) (Interface, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if v, err := read(); err == nil && v.IfIndex > 0 {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return Interface{}, ctx.Err()
		case <-deadline.C:
			return Interface{}, fmt.Errorf("managed interface did not become ready within %s", timeout)
		case <-tick.C:
		}
	}
}
func fromHealth(cfg *config.Config, iface Interface, h backend.Health, generation uint64, families toadctl.RouteFamilies) state.Snapshot {
	s := state.New(cfg.Name, string(cfg.Protocol), iface.Name, iface.MTU)
	s.Generation = generation
	s.State = h.State
	s.Reason = h.Reason
	s.Families = families
	s.RouteReady = interfaceIdentityReady(cfg, iface) && families.Ready()
	s.Interface.IfIndex = iface.IfIndex
	s.Interface.Addresses = append([]string(nil), iface.Addresses...)
	s.Session.Connected = h.Connected
	s.Session.RXBytes = h.RXBytes
	s.Session.TXBytes = h.TXBytes
	s.Session.Endpoint = h.Endpoint
	if h.LastHandshakeAge != nil {
		v := h.LastHandshakeAge.Milliseconds()
		s.Session.LastHandshakeAgeMS = &v
	}
	return s
}

// currentFamiliesLocked computes the per-family readiness for the given
// interface. OpenConnect consults its negotiated expectation; everything
// else derives from the authoritative configuration.
func (r *Runtime) currentFamiliesLocked(ctx context.Context, iface Interface) toadctl.RouteFamilies {
	if r.cfg == nil || r.cfg.Protocol != config.ProtocolOpenConnect {
		return interfaceFamilyReadiness(r.cfg, iface, nil)
	}
	reporter, ok := r.backend.(backend.LocalInterfaceReporter)
	if !ok {
		return interfaceFamilyReadiness(r.cfg, iface, nil)
	}
	expectation, err := reporter.LocalInterfaceExpectation(ctx)
	if err != nil {
		return toadctl.RouteFamilies{
			IPv4: toadctl.FamilyReadiness{Reason: "negotiated interface expectation is unavailable"},
			IPv6: toadctl.FamilyReadiness{Reason: "negotiated interface expectation is unavailable"},
		}
	}
	return interfaceFamilyReadiness(r.cfg, iface, &expectation)
}

func (r *Runtime) requireGeneration(generation uint64) error {
	if generation == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.generation != generation {
		return &toadctl.APIError{Code: "stale_generation", Message: "request targets a stale Toad generation", Retryable: true}
	}
	return nil
}

// interfaceIdentityReady reports whether the managed interface exists with a
// usable identity. It deliberately says nothing about address families.
func interfaceIdentityReady(cfg *config.Config, iface Interface) bool {
	return cfg != nil && iface.IfIndex > 0 && iface.Name != "" && iface.MTU > 0
}

// interfaceFamilyReadiness computes the authoritative per-family state.
//
// AWG2/Xray: cfg.Address is the authoritative configuration; a family is
// Configured when any of its prefixes is configured, and Ready only when
// every configured prefix of that family is observed on the current
// interface. Missing one family never makes the other family not-Ready.
//
// OpenConnect: the negotiated expectation (openconnect-network.env) is the
// authority. A negotiated address makes its family Configured; the family
// is Ready when that negotiated address is actually on the interface.
// disable_ipv6 forces IPv6 not-configured/not-ready. A server that did not
// negotiate IPv6 simply leaves that family unconfigured — it is not an
// error and must not create a recovery loop.
//
// The expectation is only consulted for OpenConnect; nil is fine elsewhere.
func interfaceFamilyReadiness(cfg *config.Config, iface Interface, expectation *interfaceinfo.Expectation) toadctl.RouteFamilies {
	var families toadctl.RouteFamilies
	if cfg == nil {
		return families
	}
	observed := make(map[netip.Prefix]struct{}, len(iface.Addresses))
	for _, raw := range iface.Addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil {
			observed[prefix] = struct{}{}
		}
	}

	switch cfg.Protocol {
	case config.ProtocolOpenConnect:
		if expectation == nil {
			return toadctl.RouteFamilies{
				IPv4: toadctl.FamilyReadiness{Reason: "negotiated interface expectation is unavailable"},
				IPv6: toadctl.FamilyReadiness{Reason: "negotiated interface expectation is unavailable"},
			}
		}
		var negotiatedV4, negotiatedV6 bool
		for _, prefix := range expectation.Addresses {
			addr := prefix.Addr()
			if !addr.IsValid() || addr.IsLinkLocalUnicast() {
				continue
			}
			if addr.Is4() {
				negotiatedV4 = true
			} else {
				negotiatedV6 = true
			}
		}
		if openConnectDisableIPv6(cfg) {
			families.IPv6 = toadctl.FamilyReadiness{Reason: "IPv6 is disabled by configuration"}
		} else {
			families.IPv6 = toadctl.FamilyReadiness{
				Configured: negotiatedV6,
				Ready:      negotiatedV6 && observedFamily(observed, 6),
				Reason:     notObservedReason(negotiatedV6, observedFamily(observed, 6), "negotiated IPv6 prefix is missing from the interface"),
			}
		}
		families.IPv4 = toadctl.FamilyReadiness{
			Configured: negotiatedV4,
			Ready:      negotiatedV4 && observedFamily(observed, 4),
			Reason:     notObservedReason(negotiatedV4, observedFamily(observed, 4), "negotiated IPv4 prefix is missing from the interface"),
		}
		return families
	default:
		// cfg.Address is authoritative (AWG2, Xray).
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
}

func observedFamily(observed map[netip.Prefix]struct{}, family int) bool {
	for prefix := range observed {
		addr := prefix.Addr()
		if !addr.IsValid() || addr.IsLinkLocalUnicast() {
			continue
		}
		if (family == 4) == addr.Is4() {
			return true
		}
	}
	return false
}

// openConnectDisableIPv6 reads the profile's disable_ipv6 switch without
// assuming the optional OpenConnect section exists.
func openConnectDisableIPv6(cfg *config.Config) bool {
	return cfg.OpenConnect != nil && cfg.OpenConnect.DisableIPv6
}

// notObservedReason explains a configured-but-missing family; a configured
// and observed family and a not-configured family need no reason.
func notObservedReason(configured, observed bool, missing string) string {
	if !configured || observed {
		return ""
	}
	return missing
}

// interfaceStructurallyReady keeps the historical all-or-nothing entry point:
// the interface exists and at least one configured family is usable.
func interfaceStructurallyReady(cfg *config.Config, iface Interface) bool {
	return interfaceIdentityReady(cfg, iface) && interfaceFamilyReadiness(cfg, iface, nil).Ready()
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
	var apiErr *toadctl.APIError
	if errors.As(err, &apiErr) {
		copy := *apiErr
		response.Error = &copy
		return response
	}
	response.Error = &toadctl.APIError{Code: code, Message: err.Error(), Retryable: true}
	return response
}
