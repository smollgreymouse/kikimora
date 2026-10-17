//go:build linux

package netlink

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/vishvananda/netlink"
)

type Watcher struct{ Buffer int }

func (w Watcher) Watch(ctx context.Context, out chan<- netstate.Invalidation) error {
	links := make(chan netlink.LinkUpdate, w.BufferOr(64))
	addrs := make(chan netlink.AddrUpdate, w.BufferOr(64))
	routes := make(chan netlink.RouteUpdate, w.BufferOr(64))
	done := make(chan struct{})
	defer close(done)
	if err := netlink.LinkSubscribe(links, done); err != nil {
		return err
	}
	if err := netlink.AddrSubscribe(addrs, done); err != nil {
		return err
	}
	if err := netlink.RouteSubscribe(routes, done); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-links:
			if !ok {
				return nil
			}
			select {
			case out <- netstate.Invalidation{Source: "netlink-link"}:
			default:
			}
		case _, ok := <-addrs:
			if !ok {
				return nil
			}
			select {
			case out <- netstate.Invalidation{Source: "netlink-addr"}:
			default:
			}
		case _, ok := <-routes:
			if !ok {
				return nil
			}
			select {
			case out <- netstate.Invalidation{Source: "netlink-route"}:
			default:
			}
		}
	}
}
func (w Watcher) BufferOr(v int) int {
	if w.Buffer > 0 {
		return w.Buffer
	}
	return v
}
