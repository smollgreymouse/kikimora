// Command fake-toad is a network-free Toad replacement for end-to-end tests.
// It exercises the same state and control-socket contracts used by kikimora-toad.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type fakeRuntime struct {
	mu      sync.Mutex
	snap    toadctl.Snapshot
	changed chan struct{}
}

func newFakeRuntime(cfg *config.Config, generation uint64) *fakeRuntime {
	addresses := append([]string(nil), cfg.Address...)
	return &fakeRuntime{
		snap: toadctl.Snapshot{
			ProtocolVersion:  toadctl.ProtocolVersion,
			Revision:         1,
			Generation:       generation,
			State:            "online",
			Reason:           "fake-toad online",
			InterfaceName:    cfg.Interface,
			IfIndex:          7,
			MTU:              cfg.MTU,
			Addresses:        addresses,
			RouteReady:       true,
			SessionConnected: true,
			Endpoint:         endpoint(cfg.Protocol),
			Capabilities:     toadctl.Capabilities{Validate: true},
			UpdatedAt:        time.Now().UTC(),
		},
		changed: make(chan struct{}),
	}
}

func (r *fakeRuntime) Snapshot() toadctl.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

func (r *fakeRuntime) Handle(_ context.Context, req toadctl.Request) toadctl.Response {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := toadctl.Response{Version: toadctl.ProtocolVersion, ID: req.ID}
	switch req.Method {
	case "Handshake":
		caps := r.snap.Capabilities
		res.OK = true
		res.Capabilities = &caps
	case "Inspect", "Snapshot", "Subscribe":
		res.OK = true
	case "Validate":
		if req.Generation != 0 && req.Generation != r.snap.Generation {
			res.Error = &toadctl.APIError{Code: "stale_generation", Message: "stale fake Toad generation", Retryable: true}
			return res
		}
		res.OK = true
		res.Validation = &toadctl.ValidationResult{Healthy: r.snap.RouteReady, State: "ready", Reason: "fake route target validated"}
	case "Stop", "Quiesce":
		if req.Generation != 0 && req.Generation != r.snap.Generation {
			res.Error = &toadctl.APIError{Code: "stale_generation", Message: "stale fake Toad generation", Retryable: true}
			return res
		}
		res.OK = true
	default:
		res.Error = &toadctl.APIError{Code: "unsupported_method", Message: "unsupported fake Toad method", Retryable: false}
		return res
	}
	snap := r.snap
	res.Snapshot = &snap
	return res
}

func (r *fakeRuntime) WaitForRevision(ctx context.Context, revision uint64) (toadctl.Snapshot, error) {
	for {
		r.mu.Lock()
		if r.snap.Revision > revision {
			snap := r.snap
			r.mu.Unlock()
			return snap, nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return toadctl.Snapshot{}, ctx.Err()
		}
	}
}

func (r *fakeRuntime) stop() toadctl.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snap.Revision++
	r.snap.State = "stopped"
	r.snap.Reason = "fake-toad stopped"
	r.snap.RouteReady = false
	r.snap.SessionConnected = false
	r.snap.IfIndex = 0
	r.snap.Addresses = nil
	r.snap.UpdatedAt = time.Now().UTC()
	close(r.changed)
	r.changed = make(chan struct{})
	return r.snap
}

func writeState(writer state.Writer, cfg *config.Config, snap toadctl.Snapshot) error {
	return writer.Write(state.Snapshot{
		Name:       cfg.Name,
		Protocol:   string(cfg.Protocol),
		Generation: snap.Generation,
		State:      snap.State,
		Reason:     snap.Reason,
		RouteReady: snap.RouteReady,
		Interface: state.InterfaceState{
			Name:      snap.InterfaceName,
			IfIndex:   snap.IfIndex,
			MTU:       snap.MTU,
			Addresses: append([]string(nil), snap.Addresses...),
		},
		Session: state.SessionState{
			Connected: snap.SessionConnected,
			Endpoint:  snap.Endpoint,
		},
	})
}

func main() {
	if len(os.Args) != 4 || os.Args[1] != "run" || os.Args[2] != "-config" {
		fmt.Fprintln(os.Stderr, "usage: fake-toad run -config FILE")
		os.Exit(2)
	}
	cfg, err := config.Load(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	generation := uint64(time.Now().UnixNano())
	runtime := newFakeRuntime(cfg, generation)
	writer := state.Writer{Dir: cfg.StateDir}
	if err := writeState(writer, cfg, runtime.Snapshot()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	socket := filepath.Join(cfg.StateDir, "control.sock")
	if err := (toadctl.Server{Socket: socket, Handler: runtime}).Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = writeState(writer, cfg, runtime.stop())
}

func endpoint(protocol config.Protocol) string {
	if protocol == config.ProtocolOpenConnect {
		return "fake-openconnect.invalid:443"
	}
	return filepath.Base(string(protocol)) + ".invalid:443"
}
