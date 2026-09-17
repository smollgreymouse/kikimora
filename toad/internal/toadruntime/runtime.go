// Package toadruntime owns one long-lived protocol instance and exposes the
// narrow control contract used by kikimora-core.
package toadruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type Interface struct {
	Name    string
	IfIndex int
	MTU     int
}
type InterfaceReader func() (Interface, error)

type Runtime struct {
	mu            sync.Mutex
	cfg           *config.Config
	generation    uint64
	tunnel        platform.Tunnel
	backend       backend.Backend
	readInterface InterfaceReader
	state         state.Snapshot
	revision      uint64
	changed       chan struct{}
	started       bool
	writer        state.Writer
	endpoints     []toadctl.TransportEndpoint
}

func New(cfg *config.Config, b backend.Backend, tunnel platform.Tunnel, read InterfaceReader) *Runtime {
	var stateDir string
	if cfg != nil {
		stateDir = cfg.StateDir
	}
	return &Runtime{cfg: cfg, backend: b, tunnel: tunnel, readInterface: read, revision: 1, changed: make(chan struct{}), writer: state.Writer{Dir: stateDir}}
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
	r.state = fromHealth(r.cfg, iface, r.backend.Health(ctx), r.generation)
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
	r.mu.Unlock()
	if !started {
		return toadctl.ValidationResult{State: "stopped", Reason: "runtime is stopped"}
	}
	h := r.backend.Health(ctx)
	return toadctl.ValidationResult{Healthy: h.Connected || h.State == "online", State: h.State, Reason: h.Reason}
}
func (r *Runtime) Rebind(context.Context, toadctl.UnderlayBinding) error {
	return &toadctl.APIError{Code: "capability_unsupported", Message: "rebind is not supported by this Toad", Retryable: false}
}
func (r *Runtime) RestartTransport(context.Context, toadctl.UnderlayBinding) error {
	return &toadctl.APIError{Code: "capability_unsupported", Message: "transport restart is not supported by this Toad", Retryable: false}
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
func (r *Runtime) RunHealthLoop(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.mu.Lock()
			if r.started {
				if iface, err := r.readInterface(); err == nil {
					next := fromHealth(r.cfg, iface, r.backend.Health(ctx), r.generation)
					if next.State != r.state.State || next.Reason != r.state.Reason || next.Interface != r.state.Interface || !sameSession(next.Session, r.state.Session) {
						r.state = next
						r.bump()
						_ = r.publishLocked()
					}
				}
			}
			r.mu.Unlock()
		}
	}
}
func (r *Runtime) Close() error { return r.Stop(context.Background()) }
func (r *Runtime) capabilities() toadctl.Capabilities {
	_, reports := r.backend.(backend.EndpointReporter)
	_, restart := r.backend.(backend.TransportRestarter)
	return toadctl.Capabilities{Validate: true, RestartTransportKeepingTUN: restart, ReportsLiveEndpoints: reports}
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
		RouteReady:         r.state.RouteReady,
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
		r.endpoints = append(r.endpoints, toadctl.TransportEndpoint{Network: value.Network, Address: value.Address, Hostname: value.Hostname, ObservedAt: value.ObservedAt, Active: value.Active})
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
func fromHealth(cfg *config.Config, iface Interface, h backend.Health, generation uint64) state.Snapshot {
	s := state.New(cfg.Name, string(cfg.Protocol), iface.Name, iface.MTU)
	s.Generation = generation
	s.State = h.State
	s.Reason = h.Reason
	s.RouteReady = iface.IfIndex > 0
	s.Interface.IfIndex = iface.IfIndex
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

func fail(response toadctl.Response, code string, err error) toadctl.Response {
	response.OK = false
	response.Error = &toadctl.APIError{Code: code, Message: err.Error(), Retryable: true}
	return response
}
