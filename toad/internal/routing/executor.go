package routing

import (
	"context"
	"sync"
)

type Serialized struct {
	Inner Executor
	mu    sync.Mutex
}

func (s *Serialized) Apply(ctx context.Context, tx Transaction) error {
	if s.Inner == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Inner.Apply(ctx, tx)
}
func (s *Serialized) Snapshot(ctx context.Context) (KernelState, error) {
	if s.Inner == nil {
		return KernelState{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Inner.Snapshot(ctx)
}
