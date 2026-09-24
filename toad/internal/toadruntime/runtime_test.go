package toadruntime

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type fakeBackend struct {
	started bool
	health  backend.Health
}

func (b *fakeBackend) Start(context.Context) error {
	b.started = true
	return nil
}
func (b *fakeBackend) Health(context.Context) backend.Health {
	if !b.started {
		return backend.Health{State: "stopped"}
	}
	if b.health.State != "" || b.health.Connected {
		return b.health
	}
	return backend.Health{State: "online", Reason: "transport healthy", Connected: true}
}
func (b *fakeBackend) Close() error {
	b.started = false
	return nil
}

type repairBackend struct {
	*fakeBackend
	expectation interfaceinfo.Expectation
	err         error
}

func (b *repairBackend) LocalInterfaceExpectation(context.Context) (interfaceinfo.Expectation, error) {
	return b.expectation, b.err
}

type fakeRepairer struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  interfaceinfo.Expectation
	err   error
	after func()
}

func (r *fakeRepairer) RepairInterface(_ context.Context, _ string, expected interfaceinfo.Expectation) error {
	r.calls.Add(1)
	r.mu.Lock()
	r.last = expected
	r.mu.Unlock()
	if r.after != nil {
		r.after()
	}
	return r.err
}

func (r *fakeRepairer) Last() interfaceinfo.Expectation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

type rebindBackend struct {
	*fakeBackend
	calls   int
	binding toadctl.UnderlayBinding
	err     error
}

func (b *rebindBackend) Rebind(_ context.Context, binding toadctl.UnderlayBinding) error {
	b.calls++
	b.binding = binding
	return b.err
}

type restartBackend struct {
	*fakeBackend
	calls   int
	binding toadctl.UnderlayBinding
	err     error
}

func (b *restartBackend) RestartTransport(_ context.Context, binding toadctl.UnderlayBinding) error {
	b.calls++
	b.binding = binding
	return b.err
}

type bothBackend struct {
	*fakeBackend
	rebinds  int
	restarts int
}

func (b *bothBackend) Rebind(context.Context, toadctl.UnderlayBinding) error {
	b.rebinds++
	return nil
}
func (b *bothBackend) RestartTransport(context.Context, toadctl.UnderlayBinding) error {
	b.restarts++
	return nil
}

type endpointBackend struct {
	*fakeBackend
	values []backend.TransportEndpoint
}

func (b *endpointBackend) TransportEndpoints(context.Context) ([]backend.TransportEndpoint, error) {
	return append([]backend.TransportEndpoint(nil), b.values...), nil
}

func TestRefreshEndpointsPreservesHostnamePortInToadSnapshot(t *testing.T) {
	b := &endpointBackend{
		fakeBackend: &fakeBackend{},
		values: []backend.TransportEndpoint{{
			Network:  "tcp",
			Hostname: "ve.example",
			Port:     4443,
			Active:   true,
		}},
	}
	r := New(&config.Config{}, b, nil, nil)
	r.refreshEndpoints(context.Background())
	snapshot := r.Snapshot()
	if len(snapshot.Endpoints) != 1 {
		t.Fatalf("endpoint count = %d, want 1", len(snapshot.Endpoints))
	}
	got := snapshot.Endpoints[0]
	if got.Hostname != "ve.example" || got.Port != 4443 || got.Network != "tcp" || !got.Active {
		t.Fatalf("endpoint DTO lost normalized fields: %#v", got)
	}
}

func TestCapabilitiesMatchExecutableInterfaces(t *testing.T) {
	cases := []struct {
		name        string
		backend     backend.Backend
		wantRebind  bool
		wantRestart bool
	}{
		{name: "base", backend: &fakeBackend{}},
		{name: "rebind", backend: &rebindBackend{fakeBackend: &fakeBackend{}}, wantRebind: true},
		{name: "restart", backend: &restartBackend{fakeBackend: &fakeBackend{}}, wantRestart: true},
		{name: "both", backend: &bothBackend{fakeBackend: &fakeBackend{}}, wantRebind: true, wantRestart: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(&config.Config{}, tc.backend, nil, nil)
			got := r.capabilities()
			if got.Rebind != tc.wantRebind || got.RestartTransportKeepingTUN != tc.wantRestart {
				t.Fatalf("capabilities=%+v want rebind=%v restart=%v", got, tc.wantRebind, tc.wantRestart)
			}
		})
	}
}

func TestSupportedCapabilitiesDelegateExactlyOnce(t *testing.T) {
	binding := toadctl.UnderlayBinding{IPv4: &toadctl.PathBinding{IfIndex: 7, Interface: "eth0"}}

	rebindErr := errors.New("rebind failed")
	rb := &rebindBackend{fakeBackend: &fakeBackend{}, err: rebindErr}
	r := New(&config.Config{}, rb, nil, nil)
	if err := r.Rebind(context.Background(), binding); !errors.Is(err, rebindErr) {
		t.Fatalf("Rebind error=%v want %v", err, rebindErr)
	}
	if rb.calls != 1 || rb.binding.IPv4 == nil || rb.binding.IPv4.IfIndex != 7 {
		t.Fatalf("rebind delegation: calls=%d binding=%+v", rb.calls, rb.binding)
	}

	restartErr := errors.New("restart failed")
	rs := &restartBackend{fakeBackend: &fakeBackend{}, err: restartErr}
	r = New(&config.Config{}, rs, nil, nil)
	if err := r.RestartTransport(context.Background(), binding); !errors.Is(err, restartErr) {
		t.Fatalf("RestartTransport error=%v want %v", err, restartErr)
	}
	if rs.calls != 1 || rs.binding.IPv4 == nil || rs.binding.IPv4.IfIndex != 7 {
		t.Fatalf("restart delegation: calls=%d binding=%+v", rs.calls, rs.binding)
	}
}

func TestUnsupportedCapabilityReturnsStableAPIError(t *testing.T) {
	r := New(&config.Config{}, &fakeBackend{}, nil, nil)
	for _, call := range []struct {
		name string
		fn   func() error
	}{
		{name: "rebind", fn: func() error { return r.Rebind(context.Background(), toadctl.UnderlayBinding{}) }},
		{name: "restart", fn: func() error { return r.RestartTransport(context.Background(), toadctl.UnderlayBinding{}) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			var apiErr *toadctl.APIError
			if err := call.fn(); !errors.As(err, &apiErr) || apiErr.Code != "capability_unsupported" {
				t.Fatalf("error=%v api=%+v", err, apiErr)
			}
		})
	}
}

func TestHealthLoopRepairsMissingAddressWithSameIfIndex(t *testing.T) {
	cfg := &config.Config{
		Name: "awg", Protocol: config.ProtocolAWG2, Interface: "kk-awg0",
		Address: []string{"10.77.0.2/24"}, MTU: 1380,
	}
	good := Interface{Name: "kk-awg0", IfIndex: 7, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}
	current := Interface{Name: "kk-awg0", IfIndex: 7, MTU: 1380}
	var ifaceMu sync.Mutex
	read := func() (Interface, error) {
		ifaceMu.Lock()
		defer ifaceMu.Unlock()
		copy := current
		copy.Addresses = append([]string(nil), current.Addresses...)
		return copy, nil
	}
	backend := &repairBackend{
		fakeBackend: &fakeBackend{started: true},
		expectation: interfaceinfo.Expectation{
			MTU: 1380, Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.2/24")},
		},
	}
	repairer := &fakeRepairer{after: func() {
		ifaceMu.Lock()
		current = good
		ifaceMu.Unlock()
	}}
	r := New(cfg, backend, nil, read)
	r.SetInterfaceRepairer(repairer)
	r.started = true
	r.generation = 1
	r.state = fromHealth(cfg, good, backend.Health(context.Background()), 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.RunHealthLoop(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().RouteReady && repairer.calls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := repairer.calls.Load(); got != 1 {
		t.Fatalf("repair calls = %d, want 1", got)
	}
	if got := repairer.Last().IfIndex; got != 7 {
		t.Fatalf("repair expected ifindex = %d, want 7", got)
	}
	if snap := r.Snapshot(); !snap.RouteReady || snap.IfIndex != 7 {
		t.Fatalf("repair did not restore route target with same identity: %#v", snap)
	}
}

func TestHealthLoopRateLimitsIncompleteRepair(t *testing.T) {
	cfg := &config.Config{
		Name: "xray", Protocol: config.ProtocolVLESSReality, Interface: "kk-xray0",
		Address: []string{"10.41.0.2/30"}, MTU: 1380,
	}
	good := Interface{Name: "kk-xray0", IfIndex: 5, MTU: 1380, Addresses: []string{"10.41.0.2/30"}}
	bad := Interface{Name: "kk-xray0", IfIndex: 5, MTU: 1380}
	backend := &repairBackend{
		fakeBackend: &fakeBackend{started: true},
		expectation: interfaceinfo.Expectation{
			MTU: 1380, Addresses: []netip.Prefix{netip.MustParsePrefix("10.41.0.2/30")},
		},
	}
	repairer := &fakeRepairer{}
	r := New(cfg, backend, nil, func() (Interface, error) { return bad, nil })
	r.SetInterfaceRepairer(repairer)
	r.started = true
	r.generation = 1
	r.state = fromHealth(cfg, good, backend.Health(context.Background()), 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.RunHealthLoop(ctx) }()

	deadline := time.Now().Add(time.Second)
	for repairer.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if repairer.calls.Load() != 1 {
		t.Fatalf("initial repair calls = %d, want 1", repairer.calls.Load())
	}
	// Two more 250ms health ticks fit here, but the one-second repair interval
	// must suppress another mutation attempt.
	time.Sleep(600 * time.Millisecond)
	if got := repairer.calls.Load(); got != 1 {
		t.Fatalf("repair storm: calls=%d within repair interval", got)
	}
}

func TestHealthLoopDoesNotRepairOpenConnectNegotiatedAddress(t *testing.T) {
	cfg := &config.Config{
		Name: "oc", Protocol: config.ProtocolOpenConnect, Interface: "kk-oc0", MTU: 1380,
	}
	good := Interface{Name: "kk-oc0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.80.0.253/24"}}
	drifted := Interface{Name: "kk-oc0", IfIndex: 9, MTU: 1380, Addresses: []string{"fe80::1/64"}}
	backend := &repairBackend{
		fakeBackend: &fakeBackend{started: true},
		expectation: interfaceinfo.Expectation{
			MTU: 1380, Addresses: []netip.Prefix{netip.MustParsePrefix("10.80.0.253/24")},
		},
	}
	repairer := &fakeRepairer{}
	r := New(cfg, backend, nil, func() (Interface, error) { return drifted, nil })
	r.SetInterfaceRepairer(repairer)
	r.started = true
	r.generation = 1
	r.state = fromHealth(cfg, good, backend.Health(context.Background()), 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.RunHealthLoop(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap := r.Snapshot()
		if !snap.RouteReady && snap.Reason == "OpenConnect negotiated interface drift requires transport recovery" {
			if got := repairer.calls.Load(); got != 0 {
				t.Fatalf("OpenConnect negotiated address was handed to repairer: calls=%d", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("OpenConnect address drift was not exposed: %#v", r.Snapshot())
}

func TestHealthLoopRejectsReplacementIfIndexWithoutRepair(t *testing.T) {
	cfg := &config.Config{
		Name: "awg", Protocol: config.ProtocolAWG2, Interface: "kk-awg0",
		Address: []string{"10.77.0.2/24"}, MTU: 1380,
	}
	original := Interface{Name: "kk-awg0", IfIndex: 7, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}
	replacement := Interface{Name: "kk-awg0", IfIndex: 8, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}
	backend := &repairBackend{
		fakeBackend: &fakeBackend{started: true},
		expectation: interfaceinfo.Expectation{MTU: 1380},
	}
	repairer := &fakeRepairer{}
	r := New(cfg, backend, nil, func() (Interface, error) { return replacement, nil })
	r.SetInterfaceRepairer(repairer)
	r.started = true
	r.generation = 1
	r.state = fromHealth(cfg, original, backend.Health(context.Background()), 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.RunHealthLoop(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap := r.Snapshot()
		if snap.IfIndex == 8 && !snap.RouteReady {
			if repairer.calls.Load() != 0 {
				t.Fatalf("replacement interface was mutated: repair calls=%d", repairer.calls.Load())
			}
			if snap.Reason != "managed interface identity changed" {
				t.Fatalf("replacement reason = %q", snap.Reason)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("replacement interface was not rejected: %#v", r.Snapshot())
}

func TestAWGRouteReadyIsStructuralNotPeerHealth(t *testing.T) {
	cfg := &config.Config{
		Name:      "awg",
		Protocol:  config.ProtocolAWG2,
		Interface: "kk-awg0",
		Address:   []string{"10.77.0.2/24"},
		MTU:       1380,
	}
	iface := Interface{Name: "kk-awg0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}

	for _, h := range []backend.Health{
		{State: "connecting", Reason: "awaiting AWG2 handshake"},
		{State: "reconnecting", Reason: "AWG2 handshake is stale"},
		{State: "degraded", Reason: "cannot read AWG2 health"},
		{State: "online", Connected: true, Reason: "recent AWG2 handshake"},
	} {
		if got := fromHealth(cfg, iface, h, 11); !got.RouteReady {
			t.Fatalf("structurally valid AWG TUN lost route readiness because of peer health: health=%+v snapshot=%+v", h, got)
		}
	}
}

func TestHealthLoopDoesNotRepairStructurallyReadyAWGForStaleHandshake(t *testing.T) {
	cfg := &config.Config{
		Name: "awg", Protocol: config.ProtocolAWG2, Interface: "kk-awg0",
		Address: []string{"10.77.0.2/24"}, MTU: 1380,
	}
	iface := Interface{Name: "kk-awg0", IfIndex: 7, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}
	b := &repairBackend{
		fakeBackend: &fakeBackend{
			started: true,
			health:  backend.Health{State: "reconnecting", Reason: "AWG2 handshake is stale"},
		},
		expectation: interfaceinfo.Expectation{
			MTU: 1380, Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.2/24")},
		},
	}
	repairer := &fakeRepairer{}
	r := New(cfg, b, nil, func() (Interface, error) { return iface, nil })
	r.SetInterfaceRepairer(repairer)
	r.started = true
	r.generation = 1
	r.state = fromHealth(cfg, iface, backend.Health{State: "online", Connected: true}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.RunHealthLoop(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap := r.Snapshot()
		if snap.RouteReady && snap.Reason == "AWG2 handshake is stale" {
			if got := repairer.calls.Load(); got != 0 {
				t.Fatalf("stale AWG handshake triggered interface repair: calls=%d", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stale AWG handshake was not exposed while preserving structural route readiness: %#v", r.Snapshot())
}

func TestValidateRequiresStructuralRouteTarget(t *testing.T) {
	cfg := &config.Config{
		Name:      "awg",
		Protocol:  config.ProtocolAWG2,
		Interface: "kk-awg0",
		Address:   []string{"10.77.0.2/24"},
		MTU:       1380,
	}
	b := &fakeBackend{started: true}
	iface := Interface{Name: "kk-awg0", IfIndex: 9, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}
	r := New(cfg, b, nil, func() (Interface, error) { return iface, nil })
	r.started = true
	r.generation = 11
	r.state.Interface.IfIndex = 9

	if got := r.Validate(context.Background()); !got.Healthy || got.State != "ready" {
		t.Fatalf("valid structural target rejected: %+v", got)
	}

	iface.Addresses = nil
	if got := r.Validate(context.Background()); got.Healthy {
		t.Fatalf("address-less target validated: %+v", got)
	}
	iface.Addresses = []string{"10.77.0.2/24"}
	iface.IfIndex = 10
	if got := r.Validate(context.Background()); got.Healthy || got.Reason != "managed interface identity changed" {
		t.Fatalf("replacement ifindex validated: %+v", got)
	}
}

func TestStructuralReadinessRequiresAllConfiguredAddresses(t *testing.T) {
	cfg := &config.Config{
		Name:      "xray",
		Protocol:  config.ProtocolVLESSReality,
		Interface: "kk-xray0",
		Address:   []string{"10.41.0.2/30", "fd00:41::2/126"},
		MTU:       1380,
	}
	iface := Interface{
		Name:      "kk-xray0",
		IfIndex:   3,
		MTU:       1380,
		Addresses: []string{"10.41.0.2/30"},
	}
	if interfaceStructurallyReady(cfg, iface) {
		t.Fatal("partial configured address set was accepted")
	}
	iface.Addresses = append(iface.Addresses, "fd00:41::2/126")
	if !interfaceStructurallyReady(cfg, iface) {
		t.Fatal("complete configured address set was rejected")
	}
}

func TestOpenConnectStructuralReadinessNeedsNonLinkLocalAddress(t *testing.T) {
	cfg := &config.Config{Name: "oc", Protocol: config.ProtocolOpenConnect, Interface: "kk-oc0", MTU: 1380}
	iface := Interface{Name: "kk-oc0", IfIndex: 4, MTU: 1434, Addresses: []string{"fe80::1/64"}}
	if interfaceStructurallyReady(cfg, iface) {
		t.Fatal("link-local-only OpenConnect interface is route-ready")
	}
	iface.Addresses = append(iface.Addresses, "10.80.0.253/24")
	if !interfaceStructurallyReady(cfg, iface) {
		t.Fatal("negotiated OpenConnect address was not accepted")
	}
}

func TestHandleRejectsStaleGenerationBeforeValidation(t *testing.T) {
	cfg := &config.Config{
		Name:      "awg",
		Protocol:  config.ProtocolAWG2,
		Interface: "kk-awg0",
		Address:   []string{"10.77.0.2/24"},
		MTU:       1380,
	}
	b := &fakeBackend{started: true}
	r := New(cfg, b, nil, func() (Interface, error) {
		return Interface{Name: "kk-awg0", IfIndex: 2, MTU: 1380, Addresses: []string{"10.77.0.2/24"}}, nil
	})
	r.started = true
	r.generation = 22

	res := r.Handle(context.Background(), toadctl.Request{Version: toadctl.ProtocolVersion, Method: "Validate", Generation: 21})
	if res.OK || res.Error == nil || res.Error.Code != "stale_generation" || !res.Error.Retryable {
		t.Fatalf("stale request response=%+v", res)
	}
}
