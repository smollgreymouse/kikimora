//go:build linux

package netlink

import (
	"net"
	"net/netip"
	"testing"

	vnl "github.com/vishvananda/netlink"
)

type fakeSource struct {
	routes   map[int][]vnl.Route
	routeGet []vnl.Route
	links    map[int]vnl.Link
	addrs    map[int]map[int][]vnl.Addr
}

func (f *fakeSource) RouteListFiltered(family int, _ *vnl.Route, _ uint64) ([]vnl.Route, error) {
	return append([]vnl.Route(nil), f.routes[family]...), nil
}
func (f *fakeSource) RouteGetWithOptions(_ net.IP, _ *vnl.RouteGetOptions) ([]vnl.Route, error) {
	return append([]vnl.Route(nil), f.routeGet...), nil
}
func (f *fakeSource) LinkByIndex(index int) (vnl.Link, error) {
	return f.links[index], nil
}
func (f *fakeSource) LinkByName(name string) (vnl.Link, error) {
	for _, link := range f.links {
		if link != nil && link.Attrs() != nil && link.Attrs().Name == name {
			return link, nil
		}
	}
	return nil, vnl.LinkNotFoundError{}
}
func (f *fakeSource) AddrList(link vnl.Link, family int) ([]vnl.Addr, error) {
	if link == nil || link.Attrs() == nil {
		return nil, nil
	}
	return append([]vnl.Addr(nil), f.addrs[link.Attrs().Index][family]...), nil
}

func dummyLink(index int, name string) vnl.Link {
	return &vnl.Dummy{LinkAttrs: vnl.LinkAttrs{Index: index, Name: name, MTU: 1500}}
}

func v4(raw string) net.IP { return net.ParseIP(raw).To4() }
func v6(raw string) net.IP { return net.ParseIP(raw).To16() }

func addr4(raw string, bits int) vnl.Addr {
	return vnl.Addr{IPNet: &net.IPNet{IP: v4(raw), Mask: net.CIDRMask(bits, 32)}}
}
func addr6(raw string, bits int) vnl.Addr {
	return vnl.Addr{IPNet: &net.IPNet{IP: v6(raw), Mask: net.CIDRMask(bits, 128)}}
}

func TestDefaultPathDerivesDHCPSourceWithRouteGet(t *testing.T) {
	link := dummyLink(2, "wlan0")
	src := &fakeSource{
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {{LinkIndex: 2, Table: 254, Priority: 600, Gw: v4("192.0.2.1")}},
		},
		routeGet: []vnl.Route{{LinkIndex: 2, Src: v4("192.0.2.44")}},
		links:    map[int]vnl.Link{2: link},
		addrs:    map[int]map[int][]vnl.Addr{},
	}
	got := defaultPath(src, vnl.FAMILY_V4, nil)
	if got == nil {
		t.Fatal("default path missing")
	}
	if got.Interface != "wlan0" || got.IfIndex != 2 || got.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("wrong default path: %#v", got)
	}
	if got.PreferredSrc != netip.MustParseAddr("192.0.2.44") {
		t.Fatalf("preferred source=%v", got.PreferredSrc)
	}
}

func TestPreferredSourceChangeIsObservableWithoutGatewayChange(t *testing.T) {
	link := dummyLink(2, "wlan0")
	src := &fakeSource{
		routeGet: []vnl.Route{{LinkIndex: 2, Src: v4("192.0.2.44")}},
		links:    map[int]vnl.Link{2: link},
		addrs:    map[int]map[int][]vnl.Addr{},
	}
	route := vnl.Route{LinkIndex: 2, Gw: v4("192.0.2.1")}
	first, ok := preferredSource(src, route, link, vnl.FAMILY_V4)
	if !ok || first != netip.MustParseAddr("192.0.2.44") {
		t.Fatalf("first source=(%v,%v)", first, ok)
	}
	src.routeGet = []vnl.Route{{LinkIndex: 2, Src: v4("192.0.2.45")}}
	second, ok := preferredSource(src, route, link, vnl.FAMILY_V4)
	if !ok || second != netip.MustParseAddr("192.0.2.45") || second == first {
		t.Fatalf("second source=(%v,%v), first=%v", second, ok, first)
	}
}

func TestPreferredIPv6SourcePrefersGlobalOverLinkLocal(t *testing.T) {
	link := dummyLink(3, "eth0")
	src := &fakeSource{
		links: map[int]vnl.Link{3: link},
		addrs: map[int]map[int][]vnl.Addr{
			3: {
				vnl.FAMILY_V6: {
					addr6("fe80::20", 64),
					addr6("2001:db8::20", 64),
					addr6("2001:db8::10", 64),
				},
			},
		},
	}
	route := vnl.Route{LinkIndex: 3, Gw: v6("fe80::1")}
	got, ok := preferredSource(src, route, link, vnl.FAMILY_V6)
	if !ok || got != netip.MustParseAddr("2001:db8::10") {
		t.Fatalf("preferred IPv6 source=(%v,%v)", got, ok)
	}
}

func TestPreferredIPv6SourceUsesLinkLocalOnlyForLinkLocalGateway(t *testing.T) {
	link := dummyLink(3, "eth0")
	src := &fakeSource{
		links: map[int]vnl.Link{3: link},
		addrs: map[int]map[int][]vnl.Addr{
			3: {vnl.FAMILY_V6: {addr6("fe80::20", 64)}},
		},
	}
	linkLocalRoute := vnl.Route{LinkIndex: 3, Gw: v6("fe80::1")}
	if got, ok := preferredSource(src, linkLocalRoute, link, vnl.FAMILY_V6); !ok || got != netip.MustParseAddr("fe80::20") {
		t.Fatalf("link-local gateway source=(%v,%v)", got, ok)
	}
	globalRoute := vnl.Route{LinkIndex: 3, Gw: v6("2001:db8::1")}
	if got, ok := preferredSource(src, globalRoute, link, vnl.FAMILY_V6); ok || got.IsValid() {
		t.Fatalf("global gateway unexpectedly used link-local source=(%v,%v)", got, ok)
	}
}

func TestPreferredSourceRejectsMappedIPv6AndNoUsableSource(t *testing.T) {
	link := dummyLink(4, "eth1")
	src := &fakeSource{
		routeGet: []vnl.Route{{LinkIndex: 4, Src: net.ParseIP("::ffff:192.0.2.2")}},
		links:    map[int]vnl.Link{4: link},
		addrs: map[int]map[int][]vnl.Addr{
			4: {vnl.FAMILY_V6: {}},
		},
	}
	if got, ok := preferredSource(src, vnl.Route{LinkIndex: 4}, link, vnl.FAMILY_V6); ok || got.IsValid() {
		t.Fatalf("mapped/no source unexpectedly accepted: (%v,%v)", got, ok)
	}
}
