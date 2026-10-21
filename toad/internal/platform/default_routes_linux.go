//go:build linux

package platform

import (
	"github.com/smollgreymouse/kikimora/toad/internal/platform/linux/netlink"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// DefaultRouteManager returns the Linux netlink owner. Executor operations are
// serialized inside the adapter so endpoint and parking cannot race.
func DefaultRouteManager() (RouteManager, routing.Executor) {
	manager := netlink.Executor{}
	return manager, manager
}
