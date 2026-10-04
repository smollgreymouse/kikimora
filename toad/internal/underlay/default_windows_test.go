//go:build windows

package underlay

import (
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

var errAliasUnknown = errors.New("test alias table: interface unknown")

func cannedRoute(luid winipcfg.LUID, index uint32, metric uint32, prefix netip.Prefix, gateway netip.Addr) winipcfg.MibIPforwardRow2 {
	row := winipcfg.MibIPforwardRow2{
		InterfaceLUID:  luid,
		InterfaceIndex: index,
		Metric:         metric,
	}
	if err := row.DestinationPrefix.SetPrefix(prefix); err != nil {
		panic(err)
	}
	if gateway.IsValid() {
		if err := row.NextHop.SetAddrPort(netip.AddrPortFrom(gateway, 0)); err != nil {
			panic(err)
		}
	}
	return row
}

func cannedAddress(luid winipcfg.LUID, addr netip.Addr, skipAsSource bool) winipcfg.MibUnicastIPAddressRow {
	row := winipcfg.MibUnicastIPAddressRow{
		InterfaceLUID: luid,
		SkipAsSource:  skipAsSource,
		DadState:      winipcfg.DadStatePreferred,
	}
	if err := row.Address.SetAddrPort(netip.AddrPortFrom(addr, 0)); err != nil {
		panic(err)
	}
	return row
}

func withTestTables(t *testing.T, routes map[winipcfg.AddressFamily][]winipcfg.MibIPforwardRow2,
	unicast map[winipcfg.AddressFamily][]winipcfg.MibUnicastIPAddressRow, aliases map[winipcfg.LUID]string) {
	t.Helper()
	previousRouteTable, previousUnicastTable, previousAlias := routeTable, unicastTable, interfaceAlias
	routeTable = func(family winipcfg.AddressFamily) ([]winipcfg.MibIPforwardRow2, error) {
		return routes[family], nil
	}
	unicastTable = func(family winipcfg.AddressFamily) ([]winipcfg.MibUnicastIPAddressRow, error) {
		return unicast[family], nil
	}
	interfaceAlias = func(luid winipcfg.LUID) (string, error) {
		name, ok := aliases[luid]
		if !ok {
			return "", errAliasUnknown
		}
		return name, nil
	}
	t.Cleanup(func() {
		routeTable, unicastTable, interfaceAlias = previousRouteTable, previousUnicastTable, previousAlias
	})
}

func TestDefaultPathPicksLowestMetricAndSkipsExcluded(t *testing.T) {
	v4 := []winipcfg.MibIPforwardRow2{
		cannedRoute(0xB, 5, 50, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParseAddr("192.0.2.1")),
		cannedRoute(0xC, 9, 10, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParseAddr("192.0.2.2")),
		cannedRoute(0xB, 5, 1, netip.MustParsePrefix("192.168.0.0/16"), netip.Addr{}),
	}
	withTestTables(t, map[winipcfg.AddressFamily][]winipcfg.MibIPforwardRow2{windows.AF_INET: v4}, nil,
		map[winipcfg.LUID]string{0xB: "Ethernet", 0xC: "kkone"})

	path, err := defaultPath(windows.AF_INET, map[string]bool{"kkone": true})
	if err != nil {
		t.Fatal(err)
	}
	if path == nil {
		t.Fatal("expected a default path")
	}
	if path.Interface != "Ethernet" || path.IfIndex != 5 || path.Metric != 50 {
		t.Fatalf("unexpected path: %#v", path)
	}
	if path.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("unexpected gateway: %v", path.Gateway)
	}
	if path.Family != 4 || path.Table != 254 {
		t.Fatalf("unexpected family/table: %#v", path)
	}
}

func TestDefaultPathWithoutCandidatesReturnsNil(t *testing.T) {
	withTestTables(t, map[winipcfg.AddressFamily][]winipcfg.MibIPforwardRow2{windows.AF_INET6: {
		cannedRoute(0xC, 9, 10, netip.MustParsePrefix("::/0"), netip.MustParseAddr("2001:db8::1")),
	}}, nil, map[winipcfg.LUID]string{0xC: "kkoc0"})

	path, err := defaultPath(windows.AF_INET6, map[string]bool{"kkoc0": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != nil {
		t.Fatalf("excluded-only default must read as no underlay: %#v", path)
	}
}

func TestPreferredSourceSkipsSkipAsSourceAndTentative(t *testing.T) {
	luid := winipcfg.LUID(0xD)
	v4 := []winipcfg.MibUnicastIPAddressRow{
		cannedAddress(luid, netip.MustParseAddr("198.51.100.7"), true),
		cannedAddress(luid, netip.MustParseAddr("169.254.10.10"), false),
	}
	usable := cannedAddress(luid, netip.MustParseAddr("198.51.100.9"), false)
	usable.DadState = winipcfg.DadStateInvalid
	v4 = append(v4, usable)
	withTestTables(t, nil, map[winipcfg.AddressFamily][]winipcfg.MibUnicastIPAddressRow{windows.AF_INET: v4}, nil)

	source, err := preferredSource(windows.AF_INET, luid)
	if err != nil {
		t.Fatal(err)
	}
	if source != netip.MustParseAddr("169.254.10.10") {
		t.Fatalf("expected link-local fallback, got %v", source)
	}

	global := []winipcfg.MibUnicastIPAddressRow{cannedAddress(luid, netip.MustParseAddr("198.51.100.9"), false)}
	withTestTables(t, nil, map[winipcfg.AddressFamily][]winipcfg.MibUnicastIPAddressRow{windows.AF_INET: global}, nil)
	source, err = preferredSource(windows.AF_INET, luid)
	if err != nil {
		t.Fatal(err)
	}
	if source != netip.MustParseAddr("198.51.100.9") {
		t.Fatalf("expected global unicast source, got %v", source)
	}
}

func TestDefaultSnapshotBuildsBothFamilies(t *testing.T) {
	routes := map[winipcfg.AddressFamily][]winipcfg.MibIPforwardRow2{
		windows.AF_INET:  {cannedRoute(0xB, 5, 50, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParseAddr("192.0.2.1"))},
		windows.AF_INET6: {cannedRoute(0xB, 5, 50, netip.MustParsePrefix("::/0"), netip.MustParseAddr("2001:db8::1"))},
	}
	unicast := map[winipcfg.AddressFamily][]winipcfg.MibUnicastIPAddressRow{
		windows.AF_INET:  {cannedAddress(0xB, netip.MustParseAddr("198.51.100.9"), false)},
		windows.AF_INET6: {cannedAddress(0xB, netip.MustParseAddr("2001:db8::9"), false)},
	}
	withTestTables(t, routes, unicast, map[winipcfg.LUID]string{0xB: "Ethernet"})

	snapshot, err := DefaultSnapshot(t.Context(), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.IPv4 == nil || snapshot.IPv6 == nil {
		t.Fatalf("both families must resolve: %#v", snapshot)
	}
	if snapshot.IPv4.PreferredSrc != netip.MustParseAddr("198.51.100.9") {
		t.Fatalf("unexpected v4 source: %v", snapshot.IPv4.PreferredSrc)
	}
	if snapshot.IPv6.Gateway != netip.MustParseAddr("2001:db8::1") || snapshot.IPv6.Family != 6 {
		t.Fatalf("unexpected v6 path: %#v", snapshot.IPv6)
	}
}
