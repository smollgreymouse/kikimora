//go:build linux

package underlay

import (
	"net"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	vnl "github.com/vishvananda/netlink"
)

type fakeLinkType struct {
	index int
	name  string
	mtu   int
}

func (f *fakeLinkType) Attrs() *vnl.LinkAttrs { return nil }
func (f *fakeLinkType) Type() string          { return "" }

type stubLink struct {
	vnl.Link
	index       int
	name        string
	mtu         int
	addrList4   []netip.Addr
	addrList6   []netip.Addr
}

func (l *stubLink) Attrs() *vnl.LinkAttrs {
	return &vnl.LinkAttrs{
		Index: l.index,
		Name:  l.name,
		MTU:   l.mtu,
	}
}

type stubNetlinkSource struct {
	routes map[int][]vnl.Route
	links  map[int]vnl.Link
	addrs  map[int]map[int][]netip.Addr
	get    map[string][]vnl.Route
}

func (s *stubNetlinkSource) RouteListFiltered(family int, filter *vnl.Route, mask uint64) ([]vnl.Route, error) {
	return s.routes[family], nil
}
func (s *stubNetlinkSource) RouteGetWithOptions(dst net.IP, opts *vnl.RouteGetOptions) ([]vnl.Route, error) {
	return s.get[dst.String()], nil
}
func (s *stubNetlinkSource) LinkByIndex(index int) (vnl.Link, error) {
	return s.links[index], nil
}
func (s *stubNetlinkSource) LinkByName(name string) (vnl.Link, error) {
	for _, l := range s.links {
		if l.Attrs().Name == name {
			return l, nil
		}
	}
	return nil, vnl.LinkNotFoundError{}
}
func (s *stubNetlinkSource) AddrList(link vnl.Link, family int) ([]vnl.Addr, error) {
	var addrs []vnl.Addr
	var sources []netip.Addr
	if family == vnl.FAMILY_V4 {
		sources = s.addrs[link.Attrs().Index][0]
	} else {
		sources = s.addrs[link.Attrs().Index][1]
	}
	for _, a := range sources {
		var ipstr string
		if family == vnl.FAMILY_V4 {
			ip4 := a.As4()
			ipstr = net.IP(ip4[:]).String()
		} else {
			ipstr = a.String()
		}
		addrs = append(addrs, vnl.Addr{IPNet: &net.IPNet{IP: net.ParseIP(ipstr), Mask: net.CIDRMask(64, 128)}})
	}
	return addrs, nil
}

// TestLinuxUnderlayReturnsIndependentFamilies proves that the Linux underlay
// Snapshot returns IPv4 and IPv6 paths independently: one family having no
// default route does not affect the other.
func TestLinuxUnderlayReturnsIndependentFamilies(t *testing.T) {
	src := &stubNetlinkSource{
		links: map[int]vnl.Link{
			3: &stubLink{index: 3, name: "en0", mtu: 1500},
		},
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {{
				Table:      254,
				Dst:        mustCIDR("0.0.0.0/0"),
				Gw:         net.ParseIP("192.0.2.1"),
				LinkIndex:  3,
				Priority:   100,
				Protocol:   vnl.RTPROT_STATIC,
			}},
			vnl.FAMILY_V6: {{
				Table:      254,
				Dst:        mustCIDR("::/0"),
				Gw:         net.ParseIP("2001:db8::1"),
				LinkIndex:  3,
				Priority:   200,
				Protocol:   vnl.RTPROT_STATIC,
			}},
		},
		addrs: map[int]map[int][]netip.Addr{
			3: {
				0: {netip.MustParseAddr("192.0.2.10")}, // v4
				1: {netip.MustParseAddr("2001:db8::10")}, // v6
			},
		},
	}

	ipv4path := defaultPath(src, vnl.FAMILY_V4, nil)
	ipv6path := defaultPath(src, vnl.FAMILY_V6, nil)
	if ipv4path == nil || ipv6path == nil {
		t.Fatalf("both families must resolve: IPv4=%v IPv6=%v", ipv4path, ipv6path)
	}
	if ipv4path.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("IPv4 gateway wrong: %v", ipv4path.Gateway)
	}
	if ipv6path.Gateway != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("IPv6 gateway wrong: %v", ipv6path.Gateway)
	}
	if ipv4path.Interface != "en0" || ipv6path.Interface != "en0" {
		t.Fatalf("interface mismatch: v4=%s v6=%s", ipv4path.Interface, ipv6path.Interface)
	}
}

// TestLinuxUnderlayHandlesMissingFamily proves that when one family has no
// default route, the remaining family is unaffected.
func TestLinuxUnderlayHandlesMissingFamily(t *testing.T) {
	en0 := &stubLink{index: 3, name: "eth0", mtu: 1500}
	src := &stubNetlinkSource{
		links: map[int]vnl.Link{3: en0},
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {{
				Table: 254, Dst: mustCIDR("0.0.0.0/0"),
				Gw: net.ParseIP("10.0.0.1"), LinkIndex: 3, Priority: 100,
			}},
			vnl.FAMILY_V6: nil, // No IPv6 default
		},
		addrs: map[int]map[int][]netip.Addr{
			3: {
				0: {netip.MustParseAddr("10.0.0.5")},
			},
		},
	}

	snap := netstate.Snapshot{
		IPv4: defaultPath(src, vnl.FAMILY_V4, nil),
		IPv6: defaultPath(src, vnl.FAMILY_V6, nil),
	}

	if snap.IPv4 == nil {
		t.Fatal("IPv4 must be available with valid default route")
	}
	if snap.IPv6 != nil {
		t.Fatalf("IPv6 must be nil without default: %+v", snap.IPv6)
	}
	if !snap.Available(4) {
		t.Fatal("Available(4) must be true")
	}
	if snap.Available(6) {
		t.Fatal("Available(6) must be false when no v6 path")
	}
}

// TestLinuxUnderlayPreferGlobalOverLinkLocal verifies that preferred source
// selection prefers global unicast addresses over link-local, which prevents
// the legacy ULA-mistaken-as-public bug from reproducing on IPv6.
func TestLinuxUnderlayPreferGlobalOverLinkLocal(t *testing.T) {
	en0 := &stubLink{index: 3, name: "wlan0", mtu: 1500}
	src := &stubNetlinkSource{
		links: map[int]vnl.Link{3: en0},
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V6: {{
				Table: 254, Dst: mustCIDR("::/0"),
				Gw: net.ParseIP("2001:db8::1"), LinkIndex: 3, Priority: 200,
			}},
		},
		addrs: map[int]map[int][]netip.Addr{
			3: {
				1: {
					netip.MustParseAddr("fd00::1"),  // ULA (link-local unicast)
					netip.MustParseAddr("2001:db8::10"), // Global
				},
			},
		},
	}

	path := defaultPath(src, vnl.FAMILY_V6, nil)
	if path == nil {
		t.Fatal("path must exist")
	}
	if !path.PreferredSrc.IsGlobalUnicast() {
		t.Fatalf("preferred source should be global, got %v (ULA=%t)",
			path.PreferredSrc, path.PreferredSrc.IsLinkLocalUnicast())
	}
	if path.PreferredSrc.String() != "2001:db8::10" {
		t.Fatalf("expected global address, got %s", path.PreferredSrc)
	}
}

// TestLinuxUnderlayRejectsMappedIPv6AsSource verifies that IPv4-mapped
// IPv6 addresses are never accepted as valid sources, preventing the DNS/
// AAAA blackhole from reappearing via this code path.
func TestLinuxUnderlayRejectsMappedIPv6AsSource(t *testing.T) {
	en0 := &stubLink{index: 3, name: "wlan0", mtu: 1500}
	src := &stubNetlinkSource{
		links: map[int]vnl.Link{3: en0},
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V6: {{
				Table: 254, Dst: mustCIDR("::/0"),
				Gw: net.ParseIP("fe80::1"), LinkIndex: 3, Priority: 200,
			}},
		},
		addrs: map[int]map[int][]netip.Addr{
			3: {
				1: {
					netip.MustParseAddr("::ffff:192.0.2.10"), // Mapped (invalid v6 source)
				},
			},
		},
	}

	path := defaultPath(src, vnl.FAMILY_V6, nil)
	if path == nil {
		t.Fatal("path must still exist even with only mapped address")
	}
	if path.PreferredSrc.IsValid() && path.PreferredSrc.Is4In6() {
		t.Fatal("mapped IPv6 must never be selected as preferred source")
	}
}

func mustCIDR(s string) *net.IPNet {
	_, dst, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return dst
}
