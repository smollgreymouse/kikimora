package parking

import "net/netip"

type OwnedRoute struct {
	Role      string       `json:"role"`
	Interface string       `json:"interface"`
	IfIndex   int          `json:"ifindex"`
	Prefix    netip.Prefix `json:"prefix"`
}

func IsCandidate(route OwnedRoute, baseline map[netip.Prefix]bool) bool {
	return route.Prefix.IsValid() && route.Prefix.Bits() == 32 && !baseline[route.Prefix]
}
