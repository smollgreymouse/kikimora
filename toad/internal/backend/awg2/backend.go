package awg2

import (
	"context"
	"errors"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
)

var ErrUnsupportedPlatform = errors.New("amneziawg2 attachment unsupported on this platform")

// Backend attaches the official AmneziaWG core to an already-owned Toad tunnel.
// The tunnel lifecycle remains outside of this package.
type Backend struct {
	mu sync.Mutex
	health backend.Health
	close func() error
}

func New() *Backend {
	return &Backend{health: backend.Health{State: "stopped"}}
}

func (b *Backend) Start(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ErrUnsupportedPlatform
}

func (b *Backend) Health(context.Context) backend.Health {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.health
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.close != nil {
		return b.close()
	}
	return nil
}
