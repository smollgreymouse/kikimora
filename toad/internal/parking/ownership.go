package parking

import (
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

type OwnedRoute struct {
	Role      string       `json:"role"`
	Interface string       `json:"interface"`
	IfIndex   int          `json:"ifindex"`
	Prefix    netip.Prefix `json:"prefix"`
}

func IsCandidate(route OwnedRoute, baseline map[netip.Prefix]bool) bool {
	return routing.IsHostPrefix(route.Prefix) && !baseline[route.Prefix]
}
