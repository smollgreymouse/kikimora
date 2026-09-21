package xray

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"

	xraycore "github.com/xtls/xray-core/core"
	featurestats "github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

var _ backend.Backend = (*Backend)(nil)

type Backend struct {
	mu              sync.Mutex
	cfg             *config.Config
	instance        *xraycore.Instance
	stats           featurestats.Manager
	lastRX          uint64
	lastTX          uint64
	everTransferred bool
	health          backend.Health
}

func (b *Backend) Validate(context.Context) backend.Validation {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.instance == nil || !b.instance.IsRunning() {
		return backend.Validation{Healthy: false, State: "degraded", Reason: "official Xray instance is not running"}
	}
	return backend.Validation{Healthy: true, State: "ready", Reason: "official Xray instance is running on the managed TUN"}
}
func (b *Backend) trafficLocked() (rx, tx uint64) {
	if b.stats == nil {
		return 0, 0
	}
	up := b.stats.GetCounter("inbound>>>toad-tun>>>traffic>>>uplink")
	down := b.stats.GetCounter("inbound>>>toad-tun>>>traffic>>>downlink")
	if up != nil && up.Value() > 0 {
		tx = uint64(up.Value())
	}
	if down != nil && down.Value() > 0 {
		rx = uint64(down.Value())
	}
	return
}

func (b *Backend) TransportEndpoints(context.Context) ([]backend.TransportEndpoint, error) {
	if b.cfg == nil {
		return nil, fmt.Errorf("VLESS config is unavailable")
	}
	specs, err := b.cfg.TransportEndpointSpecs()
	if err != nil {
		return nil, err
	}
	out := make([]backend.TransportEndpoint, 0, len(specs))
	for _, spec := range specs {
		value := backend.TransportEndpoint{Network: spec.Network, Address: spec.Address, Hostname: spec.Hostname, Port: spec.Port, Active: true}
		if spec.Address.IsValid() {
			value.Port = uint16(spec.Address.Port())
		}
		out = append(out, value)
	}
	return out, nil
}

func New(cfg *config.Config) *Backend {
	return &Backend{
		cfg:    cfg,
		health: backend.Health{State: "stopped"},
	}
}

func (b *Backend) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.instance != nil {
		return nil
	}
	if b.cfg == nil || b.cfg.VLESS == nil {
		return fmt.Errorf("Xray backend requires normalized VLESS Reality config")
	}

	raw, err := buildCoreJSON(b.cfg)
	if err != nil {
		return err
	}
	pbConfig, err := serial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build official Xray config: %w", err)
	}
	instance, err := xraycore.NewWithContext(ctx, pbConfig)
	if err != nil {
		return fmt.Errorf("create official Xray instance: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = instance.Close()
		}
	}()
	if err := instance.Start(); err != nil {
		return fmt.Errorf("start official Xray instance: %w", err)
	}

	manager, _ := instance.GetFeature(featurestats.ManagerType()).(featurestats.Manager)
	b.stats = manager

	closeOnError = false
	b.instance = instance
	b.health = backend.Health{State: "connecting", Reason: "Xray running; VLESS Reality session not yet proven"}
	return nil
}

func (b *Backend) Health(context.Context) backend.Health {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.instance == nil {
		return b.health
	}
	if !b.instance.IsRunning() {
		b.health = backend.Health{State: "degraded", Reason: "official Xray instance is not running"}
		return b.health
	}

	endpoint := ""
	if b.cfg != nil && b.cfg.VLESS != nil {
		endpoint = b.cfg.VLESS.Endpoint
	}
	iface, err := net.InterfaceByName(b.cfg.Interface)
	if err != nil {
		b.health = backend.Health{
			State:    "connecting",
			Reason:   "official Xray instance running; managed TUN not ready",
			Endpoint: endpoint,
		}
		return b.health
	}

	tunUp := iface.Flags&net.FlagUp != 0
	rx, tx := b.trafficLocked()
	if rx > 0 || tx > 0 {
		b.everTransferred = true
	}

	state := "connecting"
	reason := "Xray managed TUN is up; tunneled session not yet proven"
	connected := false
	if !tunUp {
		reason = "Xray managed TUN exists but link is not up"
	} else if b.everTransferred {
		state = "online"
		reason = "Xray tunneled traffic observed"
		connected = true
	}

	b.health = backend.Health{
		State:     state,
		Reason:    reason,
		Connected: connected,
		RXBytes:   rx,
		TXBytes:   tx,
		Endpoint:  endpoint,
	}
	return b.health
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.instance == nil {
		return nil
	}
	err := b.instance.Close()
	b.instance = nil
	b.stats = nil
	b.lastRX = 0
	b.lastTX = 0
	b.everTransferred = false
	b.health = backend.Health{State: "stopped"}
	if err != nil {
		return fmt.Errorf("close official Xray instance: %w", err)
	}
	return nil
}
