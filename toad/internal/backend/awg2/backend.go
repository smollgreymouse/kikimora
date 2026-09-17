package awg2

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

var _ backend.Backend = (*Backend)(nil)

// Backend attaches the official AmneziaWG core to an already-owned Toad tunnel.
// The tunnel lifecycle remains outside of this package.
type Backend struct {
	mu     sync.Mutex
	cfg    *config.Config
	tunnel platform.Tunnel
	dev    *device.Device
	health backend.Health
}

func New(cfg *config.Config, tunnel platform.Tunnel) *Backend {
	return &Backend{
		cfg:    cfg,
		tunnel: tunnel,
		health: backend.Health{State: "stopped"},
	}
}

func (b *Backend) Start(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.dev != nil {
		return nil
	}
	if b.cfg == nil || b.cfg.AWG2 == nil {
		return fmt.Errorf("AWG2 backend requires normalized AWG2 config")
	}
	if b.tunnel == nil {
		return fmt.Errorf("AWG2 backend requires a Toad-owned tunnel")
	}

	payload, err := buildConfigUAPI(b.cfg.AWG2)
	if err != nil {
		return fmt.Errorf("build AWG2 UAPI config: %w", err)
	}

	awgTun, err := attachTunnel(b.tunnel)
	if err != nil {
		return err
	}

	logger := device.NewLogger(device.LogLevelError, "toad/"+b.cfg.Name+": ")
	dev := device.NewDevice(awgTun, conn.NewDefaultBind(), logger)
	closeOnError := true
	defer func() {
		if closeOnError {
			dev.Close()
		}
	}()

	if err := dev.IpcSetOperation(strings.NewReader(payload)); err != nil {
		return fmt.Errorf("configure official AWG2 device: %w", err)
	}
	if err := dev.Up(); err != nil {
		return fmt.Errorf("bring official AWG2 device up: %w", err)
	}

	closeOnError = false
	b.dev = dev
	b.health = backend.Health{State: "connecting", Reason: "awaiting AWG2 handshake"}
	return nil
}

func (b *Backend) Health(context.Context) backend.Health {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.dev == nil {
		return b.health
	}

	var raw bytes.Buffer
	if err := b.dev.IpcGetOperation(&raw); err != nil {
		b.health = backend.Health{State: "degraded", Reason: "cannot read AWG2 health"}
		return b.health
	}
	health, err := parseHealthUAPI(raw.String(), time.Now())
	if err != nil {
		b.health = backend.Health{State: "degraded", Reason: "cannot parse AWG2 health"}
		return b.health
	}
	b.health = health
	return b.health
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.dev == nil {
		return nil
	}
	b.dev.Close()
	b.dev = nil
	b.health = backend.Health{State: "stopped"}
	return nil
}

func (b *Backend) Validate(ctx context.Context) backend.Validation {
	h := b.Health(ctx)
	return backend.Validation{Healthy: h.State == "online", State: h.State, Reason: h.Reason}
}
func (b *Backend) TransportEndpoints(context.Context) ([]backend.TransportEndpoint, error) {
	if b.cfg == nil || b.cfg.AWG2 == nil {
		return nil, fmt.Errorf("AWG2 config is unavailable")
	}
	raw := b.cfg.AWG2.Endpoint
	addr, err := netip.ParseAddrPort(raw)
	if err != nil {
		return []backend.TransportEndpoint{{Network: "udp", Hostname: raw, Active: true}}, nil
	}
	return []backend.TransportEndpoint{{Network: "udp", Address: addr, Active: true}}, nil
}
func (b *Backend) RestartTransport(ctx context.Context, _ toadctl.UnderlayBinding) error {
	if err := b.Close(); err != nil {
		return err
	}
	return b.Start(ctx)
}
