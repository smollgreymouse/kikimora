//go:build linux

package netlink

import (
	"context"
	"net"
	"net/netip"
	"sort"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/vishvananda/netlink"
)

type Snapshotter struct{}

func (Snapshotter) Snapshot(_ context.Context, excluded map[string]bool) (netstate.Snapshot, error) {
	return netstate.Snapshot{IPv4: defaultPath(netlink.FAMILY_V4, excluded), IPv6: defaultPath(netlink.FAMILY_V6, excluded)}, nil
}

func defaultPath(family int, excluded map[string]bool) *netstate.Path {
	routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: 254}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil
	}
	type candidate struct {
		route    netlink.Route
		name     string
		priority int
	}
	var candidates []candidate
	for _, route := range routes {
		if route.Dst != nil && route.Dst.String() != "0.0.0.0/0" && route.Dst.String() != "::/0" {
			continue
		}
		if route.LinkIndex <= 0 {
			continue
		}
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			continue
		}
		name := link.Attrs().Name
		if route.LinkIndex == 1 || name == "lo" || name == "leshy-dns0" || excluded[name] {
			continue
		}
		priority := route.Priority
		candidates = append(candidates, candidate{route, name, priority})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		if a.route.LinkIndex != b.route.LinkIndex {
			return a.route.LinkIndex < b.route.LinkIndex
		}
		return a.name < b.name
	})
	winner := candidates[0]
	link, _ := netlink.LinkByName(winner.name)
	mtu := 0
	if link != nil && link.Attrs() != nil {
		mtu = link.Attrs().MTU
	}
	path := &netstate.Path{Family: family, IfIndex: winner.route.LinkIndex, Interface: winner.name, MTU: mtu, Table: winner.route.Table, Metric: uint32(winner.priority)}
	if winner.route.Gw != nil {
		if a, ok := addr(winner.route.Gw, family); ok {
			path.Gateway = a
		}
	}
	if winner.route.Src != nil {
		if a, ok := addr(winner.route.Src, family); ok {
			path.PreferredSrc = a
		}
	}
	return path
}
func addr(raw net.IP, family int) (netip.Addr, bool) {
	if family == netlink.FAMILY_V4 {
		v := raw.To4()
		if v == nil {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte{v[0], v[1], v[2], v[3]}), true
	}
	v := raw.To16()
	if v == nil {
		return netip.Addr{}, false
	}
	var b [16]byte
	copy(b[:], v)
	return netip.AddrFrom16(b), true
}
