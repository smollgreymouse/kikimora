package awg2

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

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
