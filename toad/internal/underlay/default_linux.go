//go:build linux

package underlay

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	netlinksource "github.com/smollgreymouse/kikimora/toad/internal/platform/linux/netlink"
)

func DefaultSnapshot(ctx context.Context, excluded map[string]bool) (netstate.Snapshot, error) {
	return (netlinksource.Snapshotter{}).Snapshot(ctx, excluded)
}

func DefaultWatch(ctx context.Context, out chan<- netstate.Invalidation) error {
	return (netlinksource.Watcher{}).Watch(ctx, out)
}
