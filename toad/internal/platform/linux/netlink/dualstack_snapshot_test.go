//go:build linux

package netlink

import (
	"net"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	vnl "github.com/vishvananda/netlink"
)

type fakeNetlinkSource struct {
	routes   map[int][]vnl.Route
	links    map[int]vnl.Link
	addrs    map[int]map[int][]vnl.Addr
	getRoute map[string][]vnl.Route // keyed by gateway string
}

func (f fakeNetlinkSource) RouteListFiltered(family int, filter *vnl.Route, mask uint64) ([]vnl.Route, error) {
	return f.routes[family], nil
}
func (f fakeNetlinkSource) RouteGetWithOptions(dst net.IP, opts *vnl.RouteGetOptions) ([]vnl.Route, error) {
	if opts != nil && opts.OifIndex != 0 {
		return f.getRoute[dst.String()], nil
	}
	return nil, nil
}
func (f fakeNetlinkSource) LinkByIndex(index int) (vnl.Link, error) {
	return f.links[index], nil
}
func (f fakeNetlinkSource) LinkByName(name string) (vnl.Link, error) {
	for _, l := range f.links {
		if l.Attrs() != nil && l.Attrs().Name == name {
			return l, nil
		}
	}
	return nil, nil
}
func (f fakeNetlinkSource) AddrList(link vnl.Link, family int) ([]vnl.Addr, error) {
	if link == nil || link.Attrs() == nil {
		return nil, nil
	}
	return f.addrs[link.Attrs().Index][family], nil
}

// fakeLink returns a minimal vnl.Link for testing.
func fakeLink(index int, name string, mtu int) *vnl.GenericLink {
	return &vnl.GenericLink{
		LinkAttrs: vnl.LinkAttrs{
			Index: index,
			Name:  name,
			MTU:   mtu,
		},
	}
}

// TestSnapshotterReturnsIndependentFamilies proves that the Linux underlay
// Snapshotter reads IPv4 and IPv6 paths independently: one family being nil
// never affects the other family.
func TestSnapshotterReturnsIndependentFamilies(t *testing.T) {
	en0 := fakeLink(3, "en0", 1500)
	src := fakeNetlinkSource{
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {
				{LinkIndex: 3, Priority: 100, Gw: net.ParseIP("192.0.2.1"), Table: 254},
			},
			vnl.FAMILY_V6: {
				{LinkIndex: 3, Priority: 200, Gw: net.ParseIP("2001:db8::1"), Table: 254},
			},
		},
		links: map[int]vnl.Link{3: en0},
		addrs: map[int]map[int][]vnl.Addr{
			3: {
				vnl.FAMILY_V4: {{IPNet: &net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)}}},
				vnl.FAMILY_V6: {{IPNet: &net.IPNet{IP: net.ParseIP("2001:db8::10"), Mask: net.CIDRMask(64, 128)}}},
			},
		},
	}

	snap := Snapshotter{source: src}
	result, err := snap.Snapshot(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IPv4 == nil || result.IPv6 == nil {
		t.Fatalf("both families must be present: IPv4=%v IPv6=%v", result.IPv4, result.IPv6)
	}
	if result.IPv4.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("v4 gateway wrong: %v", result.IPv4.Gateway)
	}
	if result.IPv6.Gateway != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("v6 gateway wrong: %v", result.IPv6.Gateway)
	}
	if result.IPv4.Family != 4 || result.IPv6.Family != 6 {
		t.Fatalf("families confused: v4=%d v6=%d", result.IPv4.Family, result.IPv6.Family)
	}
}

// TestSnapshotterHandlesMissingFamily proves that when one family has no
// default route, the other family still resolves correctly.
func TestSnapshotterHandlesMissingFamily(t *testing.T) {
	en0 := fakeLink(5, "wlan0", 1500)
	src := fakeNetlinkSource{
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {
				{LinkIndex: 5, Priority: 100, Gw: net.ParseIP("10.0.0.1"), Table: 254},
			},
			vnl.FAMILY_V6: nil, // No IPv6 default
		},
		links: map[int]vnl.Link{5: en0},
		addrs: map[int]map[int][]vnl.Addr{
			5: {
				vnl.FAMILY_V4: {{IPNet: &net.IPNet{IP: net.ParseIP("10.0.0.5"), Mask: net.CIDRMask(24, 32)}}},
			},
		},
	}

	snap := Snapshotter{source: src}
	result, err := snap.Snapshot(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IPv4 == nil {
		t.Fatal("IPv4 must be available")
	}
	if result.IPv6 != nil {
		t.Fatalf("IPv6 must be nil when no default route: %+v", result.IPv6)
	}
	// Verify snapshot.Available correctly reflects per-family status
	if !result.Available(4) {
		t.Fatal("Available(4) must be true")
	}
	if result.Available(6) {
		t.Fatal("Available(6) must be false when no v6 path")
	}
}

// TestSnapshotterRejectsMappedIPv6 verifies that IPv4-mapped IPv6 addresses
// are rejected as valid sources, preventing the "mapped v6 is a public source"
// confusion that would recreate the legacy DNS/AAAA blackhole.
func TestSnapshotterRejectsMappedIPv6(t *testing.T) {
	mapped := netip.MustParseAddr("::ffff:192.0.2.10")
	if !validSource(mapped, vnl.FAMILY_V6) {
		// This should fail: mapped IPv6 is not a valid v6 source
	} else {
		t.Fatal("mapped IPv6 must be rejected as invalid v6 source")
	}
	// Also verify v4 rejection of mapped address
	if validSource(mapped, vnl.FAMILY_V4) {
		t.Fatal("mapped IPv6 must be rejected as invalid v4 source too")
	}
}

// TestSnapshotterExcludesManagedInterfaces verifies that managed Toad
// interfaces are excluded from underlay detection.
func TestSnapshotterExcludesManagedInterfaces(t *testing.T) {
	utun := fakeLink(10, "utun0", 1400)
	en0 := fakeLink(3, "en0", 1500)
	src := fakeNetlinkSource{
		routes: map[int][]vnl.Route{
			vnl.FAMILY_V4: {
				// utun has lower metric but must be excluded
				{LinkIndex: 10, Priority: 10, Gw: net.ParseIP("10.8.0.1"), Table: 254},
				{LinkIndex: 3, Priority: 100, Gw: net.ParseIP("192.0.2.1"), Table: 254},
			},
		},
		links: map[int]vnl.Link{10: utun, 3: en0},
		addrs: map[int]map[int][]vnl.Addr{
			3: {
				vnl.FAMILY_V4: {{IPNet: &net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)}}},
			},
		},
	}

	snap := Snapshotter{source: src}
	result, err := snap.Snapshot(nil, map[string]bool{"utun0": true})
	if err != nil {
		t.Fatal(err)
	}
	if result.IPv4 == nil {
		t.Fatal("IPv4 path must resolve to physical interface")
	}
	if result.IPv4.Interface == "utun0" {
		t.Fatal("managed interface must be excluded from underlay")
	}
	if result.IPv4.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("must select physical gateway, got %v", result.IPv4.Gateway)
	}
}
