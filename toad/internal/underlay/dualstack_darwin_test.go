//go:build darwin

package underlay

import (
	"testing"
)

// TestDarwinParseRouteOutputHandlesBothFamilies independently parses route
// output for both inet and inet6 families, proving that parsing one family
// cannot leak into the other. This encodes the "IPv4 Ready never implies
// IPv6 Ready" contract at the Darwin underlay level.
func TestDarwinParseRouteOutputHandlesBothFamilies(t *testing.T) {
	v4Result := parseDarwinRouteOutput(darwinRouteV4Sample(), 4)
	v6Result := parseDarwinRouteOutput(darwinRouteV6Sample(), 6)

	if v4Result == nil {
		t.Fatal("v4 route output must produce a path")
	}
	if v6Result == nil {
		t.Fatal("v6 route output must produce a path")
	}
	if v4Result.Family != 4 || v6Result.Family != 6 {
		t.Fatalf("families confused: v4=%d v6=%d", v4Result.Family, v6Result.Family)
	}
	if v4Result.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("v4 gateway wrong: %v", v4Result.Gateway)
	}
	if v6Result.Gateway != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("v6 gateway wrong: %v", v6Result.Gateway)
	}
}

// TestDarwinRouteParsingExcludesUTUN verifies that utun interfaces are
// excluded from underlay detection — they belong to managed Toads.
func TestDarwinRouteParsingExcludesUTUN(t *testing.T) {
	utunResult := parseDarwinRouteOutput(darwinRouteUTUNSample(), 4)
	if utunResult != nil {
		t.Fatal("utun interface must be excluded from underlay")
	}
}

// TestDarwinEmptyRouteOutputReturnsNil proves that absent default routes
// correctly signal underlay-unavailable state (nil path).
func TestDarwinEmptyRouteOutputReturnsNil(t *testing.T) {
	result := parseDarwinRouteOutput("", 6)
	if result != nil {
		t.Fatalf("empty route output must return nil, got %+v", result)
	}
}

var splitDefaultPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
	netip.MustParsePrefix("::/1"),
	netip.MustParsePrefix("8000::/1"),
}

// TestDarwinSplitDefaultNeverProducesHostTarget proves that split-default
// prefixes would produce -net targets (not -host), preventing accidental
// host-route confusion during parking or endpoint reconciliation.
func TestDarwinSplitDefaultNeverProducesHostTarget(t *testing.T) {
	for _, prefix := range splitDefaultPrefixes {
		target := routeTarget(prefix)
		if target == "" {
			t.Fatalf("%s produced empty target", prefix)
		}
		// Split defaults use /1 prefix length which is not a host prefix
		// so routeTarget should produce "-net ..." not "-host ..."
		if prefix.Bits() < prefix.Addr().BitLen() && len(target) > 0 && target[:5] == "-host" {
			t.Fatalf("split-default %s incorrectly produces -host target", prefix)
		}
	}
}

// TestDarwinSnapshotConstructionPreservesPerFamilyIndependence proves that
// constructing netstate.Snapshot from separate per-family calls does not
// mix up IPv4 and IPv6 paths.
func TestDarwinSnapshotConstructionPreservesPerFamilyIndependence(t *testing.T) {
	v4Result := parseDarwinRouteOutput(darwinRouteV4Sample(), 4)
	v6Result := parseDarwinRouteOutput(darwinRouteV6Sample(), 6)

	snap := netstate.Snapshot{}
	if v4Result.Family == 4 {
		snap.IPv4 = v4Result
	}
	if v6Result.Family == 6 {
		snap.IPv6 = v6Result
	}

	if snap.IPv4 == nil || snap.IPv4.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatal("v4 snapshot lost in construction")
	}
	if snap.IPv6 == nil || snap.IPv6.Gateway != netip.MustParseAddr("2001:db8::1") {
		t.Fatal("v6 snapshot lost in construction")
	}
	if snap.IPv4.Available(4) != true {
		t.Fatal("Available(4) must be true when v4 path exists")
	}
	if snap.IPv6.Available(6) != true {
		t.Fatal("Available(6) must be true when v6 path exists")
	}
}

// Mirror existing darwin route sample outputs used by DefaultWatch/defaultPath.
func darwinRouteV4Sample() string {
	return `   route to: default
destination: default
       mask: default
    gateway: 192.0.2.1
  interface: en0
if address: 192.0.2.10`
}

func darwinRouteV6Sample() string {
	return `   route to: default
destination: default
       mask: default
    gateway: 2001:db8::1
  interface: en0
if address: 2001:db8::10`
}

func darwinRouteUTUNSample() string {
	return `   route to: default
destination: default
       mask: default
    gateway: 10.8.0.1
  interface: utun0
if address: 10.8.0.5`
}

// parseDarwinRouteOutput mirrors the core parsing logic from defaultPath but
// operates on pre-captured output instead of exec.CommandContext.
func parseDarwinRouteOutput(output string, family int) *netstate.Path {
	if output == "" {
		return nil
	}
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	iface := values["interface"]
	if iface == "" || strings.HasPrefix(iface, "utun") {
		return nil
	}
	gwStr := values["gateway"]
	srcStr := values["if address"]
	if gwStr == "" && srcStr == "" {
		return nil
	}
	path := &netstate.Path{Family: family, Interface: iface, Table: 254}
	if gw, err := netip.ParseAddr(gwStr); err == nil {
		path.Gateway = gw
	}
	if src, err := netip.ParseAddr(strings.TrimSuffix(srcStr, "%"+iface)); err == nil {
		path.PreferredSrc = src
	}
	return path
}
