package xray

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	xraycore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

var _ backend.Backend = (*Backend)(nil)

type Backend struct {
	mu       sync.Mutex
	cfg      *config.Config
	instance *xraycore.Instance
	health   backend.Health
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
	b.health = backend.Health{State: "stopped"}
	if err != nil {
		return fmt.Errorf("close official Xray instance: %w", err)
	}
	return nil
}
