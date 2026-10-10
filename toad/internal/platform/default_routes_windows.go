//go:build windows

package platform

import (
	"github.com/smollgreymouse/kikimora/toad/internal/platform/windows/routes"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// DefaultRouteManager returns the Windows IP Helper route owner. Executor
// operations are serialized inside the adapter so endpoint and parking cannot
// race, mirroring the Linux netlink wiring.
func DefaultRouteManager() (RouteManager, routing.Executor) {
	manager := routes.NewExecutor()
	return manager, manager
}
