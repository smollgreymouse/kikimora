//go:build !linux && !darwin

package underlay

import (
	"context"
	"errors"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
)

var ErrUnsupported = errors.New("canonical underlay monitoring is unsupported on this platform")

func DefaultSnapshot(context.Context, map[string]bool) (netstate.Snapshot, error) {
	return netstate.Snapshot{}, ErrUnsupported
}

func DefaultWatch(ctx context.Context, _ chan<- netstate.Invalidation) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrUnsupported
	}
}
