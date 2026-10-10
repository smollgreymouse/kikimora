//go:build linux

package netlink

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	vnl "github.com/vishvananda/netlink"
)

type source interface {
	RouteListFiltered(family int, filter *vnl.Route, mask uint64) ([]vnl.Route, error)
	RouteGetWithOptions(destination net.IP, options *vnl.RouteGetOptions) ([]vnl.Route, error)
	LinkByIndex(index int) (vnl.Link, error)
	LinkByName(name string) (vnl.Link, error)
	AddrList(link vnl.Link, family int) ([]vnl.Addr, error)
}

type nativeSource struct{}

func (nativeSource) RouteListFiltered(family int, filter *vnl.Route, mask uint64) ([]vnl.Route, error) {
	return vnl.RouteListFiltered(family, filter, mask)
}
func (nativeSource) RouteGetWithOptions(destination net.IP, options *vnl.RouteGetOptions) ([]vnl.Route, error) {
	return vnl.RouteGetWithOptions(destination, options)
}
func (nativeSource) LinkByIndex(index int) (vnl.Link, error)  { return vnl.LinkByIndex(index) }
func (nativeSource) LinkByName(name string) (vnl.Link, error) { return vnl.LinkByName(name) }
func (nativeSource) AddrList(link vnl.Link, family int) ([]vnl.Addr, error) {
	return vnl.AddrList(link, family)
}

type Snapshotter struct{ source source }

func (s Snapshotter) netlinkSource() source {
	if s.source != nil {
		return s.source
	}
	return nativeSource{}
}

func (s Snapshotter) Snapshot(_ context.Context, excluded map[string]bool) (netstate.Snapshot, error) {
	src := s.netlinkSource()
	return netstate.Snapshot{
		IPv4:       defaultPath(src, vnl.FAMILY_V4, excluded),
		IPv6:       defaultPath(src, vnl.FAMILY_V6, excluded),
		ObservedAt: time.Now().UTC(),
	}, nil
}

func defaultPath(src source, family int, excluded map[string]bool) *netstate.Path {
	routes, err := src.RouteListFiltered(family, &vnl.Route{Table: 254}, vnl.RT_FILTER_TABLE)
	if err != nil {
		return nil
	}
	type candidate struct {
		route    vnl.Route
		link     vnl.Link
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
		link, err := src.LinkByIndex(route.LinkIndex)
		if err != nil || link == nil || link.Attrs() == nil {
			continue
		}
		name := link.Attrs().Name
		if route.LinkIndex == 1 || name == "lo" || name == "leshy-dns0" || excluded[name] {
			continue
		}
		candidates = append(candidates, candidate{route: route, link: link, name: name, priority: route.Priority})
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
	mtu := 0
	if winner.link.Attrs() != nil {
		mtu = winner.link.Attrs().MTU
	}
	path := &netstate.Path{
		Family:    family,
		IfIndex:   winner.route.LinkIndex,
		Interface: winner.name,
		MTU:       mtu,
		Table:     winner.route.Table,
		Metric:    uint32(winner.priority),
	}
	if winner.route.Gw != nil {
		if a, ok := addr(winner.route.Gw, family); ok {
			path.Gateway = a
		}
	}
	if source, ok := preferredSource(src, winner.route, winner.link, family); ok {
		path.PreferredSrc = source
	}
	return path
}

func preferredSource(src source, route vnl.Route, link vnl.Link, family int) (netip.Addr, bool) {
	if route.Src != nil {
		if source, ok := addr(route.Src, family); ok && validSource(source, family) {
			return source, true
		}
	}
	if route.Gw != nil && route.LinkIndex > 0 {
		routes, err := src.RouteGetWithOptions(route.Gw, &vnl.RouteGetOptions{OifIndex: route.LinkIndex})
		if err == nil {
			for _, resolved := range routes {
				if resolved.LinkIndex != 0 && resolved.LinkIndex != route.LinkIndex {
					continue
				}
				if resolved.Src != nil {
					if source, ok := addr(resolved.Src, family); ok && validSource(source, family) {
						return source, true
					}
				}
			}
		}
	}
	if link == nil || link.Attrs() == nil {
		return netip.Addr{}, false
	}
	addrs, err := src.AddrList(link, family)
	if err != nil {
		return netip.Addr{}, false
	}
	global := make([]netip.Addr, 0, len(addrs))
	linkLocal := make([]netip.Addr, 0, len(addrs))
	for _, item := range addrs {
		source, ok := addr(item.IP, family)
		if !ok || !validSource(source, family) || source.IsUnspecified() || source.IsMulticast() {
			continue
		}
		if source.IsGlobalUnicast() && !source.IsLinkLocalUnicast() {
			global = append(global, source)
			continue
		}
		if family == vnl.FAMILY_V6 && source.IsLinkLocalUnicast() {
			linkLocal = append(linkLocal, source)
		}
	}
	sortAddrs(global)
	if len(global) > 0 {
		return global[0], true
	}
	if family == vnl.FAMILY_V6 && gatewayIsLinkLocal(route.Gw) {
		sortAddrs(linkLocal)
		if len(linkLocal) > 0 {
			return linkLocal[0], true
		}
	}
	return netip.Addr{}, false
}

func validSource(source netip.Addr, family int) bool {
	if !source.IsValid() || source.Is4In6() {
		return false
	}
	if family == vnl.FAMILY_V4 {
		return source.Is4()
	}
	return source.Is6()
}

func gatewayIsLinkLocal(raw net.IP) bool {
	if raw == nil {
		return false
	}
	value, ok := addr(raw, vnl.FAMILY_V6)
	return ok && !value.Is4In6() && value.IsLinkLocalUnicast()
}

func sortAddrs(addrs []netip.Addr) {
	sort.Slice(addrs, func(i, j int) bool {
		return addrs[i].Compare(addrs[j]) < 0
	})
}

func addr(raw net.IP, family int) (netip.Addr, bool) {
	if family == vnl.FAMILY_V4 {
		v := raw.To4()
		if v == nil {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte{v[0], v[1], v[2], v[3]}), true
	}
	if raw.To4() != nil {
		return netip.Addr{}, false
	}
	v := raw.To16()
	if v == nil {
		return netip.Addr{}, false
	}
	var b [16]byte
	copy(b[:], v)
	return netip.AddrFrom16(b), true
}
