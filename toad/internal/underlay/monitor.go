package underlay

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
)

type Source interface {
	Watch(context.Context, chan<- netstate.Invalidation) error
	Snapshot(context.Context, map[string]bool) (netstate.Snapshot, error)
}
type Monitor struct {
	Source Source
	Settle func(context.Context, netstate.Snapshot) error
}

func (m Monitor) Run(ctx context.Context, invalidations <-chan netstate.Invalidation, emit func(netstate.Snapshot, netstate.ChangeReason)) error {
	if m.Source == nil {
		return nil
	}
	old, err := m.Source.Snapshot(ctx, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-invalidations:
			next, err := m.Source.Snapshot(ctx, nil)
			if err != nil {
				return err
			}
			merged, reason, changed := netstate.Compare(old, next)
			if changed {
				old = merged
				emit(merged, reason)
			}
		}
	}
}
