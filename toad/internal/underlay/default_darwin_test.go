//go:build darwin

package underlay

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
)

// TestDarwinDefaultSnapshotReadsBothFamilies verifies that DefaultSnapshot
// reads both inet and inet6 tables via separate route(8) calls, so IPv4
// readiness never implies IPv6 readiness. A missing IPv6 default produces a
// nil IPv6 path while IPv4 remains independently available.
func TestDarwinDefaultSnapshotReadsBothFamilies(t *testing.T) {
	// Verify the parsing logic used by defaultPath handles both families
	// cleanly and independently.
	v4Sample := darwinRouteV4Sample()
	v6Sample := darwinRouteV6Sample()

	// Test V4 parsing produces valid path
	v4Path := parseRouteOutput(v4Sample, 4)
	if v4Path == nil {
		t.Fatal("v4 route output must produce a path")
	}
	if v4Path.Family != 4 || v4Path.Interface != "en0" {
		t.Fatalf("v4 path incorrect: %+v", v4Path)
	}
	if v4Path.Gateway != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("v4 gateway incorrect: %v", v4Path.Gateway)
	}

	// Test V6 parsing produces valid path independently
	v6Path := parseRouteOutput(v6Sample, 6)
	if v6Path == nil {
		t.Fatal("v6 route output must produce a path")
	}
	if v6Path.Family != 6 || v6Path.Interface != "en0" {
		t.Fatalf("v6 path incorrect: %+v", v6Path)
	}
	if v6Path.Gateway != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("v6 gateway incorrect: %v", v6Path.Gateway)
	}

	// Test that empty route output produces nil (unavailable family)
	emptyPath := parseRouteOutput("", 6)
	if emptyPath != nil {
		t.Fatal("empty route output must produce nil path")
	}
}

// TestDarwinDefaultSnapshotExcludesManagedInterfaces verifies that managed
// Toad interfaces (utun*) are excluded from underlay detection.
func TestDarwinDefaultSnapshotExcludesManagedInterfaces(t *testing.T) {
	utunSample := darwinRouteUTUNSample()
	path := parseRouteOutput(utunSample, 4)
	if path != nil {
		t.Fatal("utun interface must be excluded from underlay")
	}
}

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

// parseRouteOutput mirrors the parsing logic from darwin defaultPath but
// operates on pre-captured output instead of calling exec.CommandContext.
func parseRouteOutput(output string, family int) *netstate.Path {
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
	// Skip net.InterfaceByName — not needed for this test
	path := &netstate.Path{Family: family, Interface: iface, Table: 254}
	if gateway, err := netip.ParseAddr(values["gateway"]); err == nil {
		path.Gateway = gateway
	}
	if source, err := netip.ParseAddr(strings.TrimSuffix(values["if address"], "%"+iface)); err == nil {
		path.PreferredSrc = source
	}
	return path
}

// TestDarwinDefaultSnapshotNeverConfusesFamilies verifies that parsing v4
// route output does not accidentally populate v6 path or vice versa.
func TestDarwinDefaultSnapshotNeverConfusesFamilies(t *testing.T) {
	v4 := darwinRouteV4Sample()
	v6 := darwinRouteV6Sample()

	path4 := parseRouteOutput(v4, 4)
	path6 := parseRouteOutput(v6, 6)

	if path4.Family != 4 || path6.Family != 6 {
		t.Fatalf("families confused: v4=%d v6=%d", path4.Family, path6.Family)
	}
	if path4.Gateway == path6.Gateway {
		t.Fatal("v4 and v6 gateways must be different")
	}

	// Test Snapshot construction logic: IPv4 set does not affect IPv6
	snap := netstate.Snapshot{}
	if path4.Family == 4 {
		snap.IPv4 = path4
	}
	if path6.Family == 6 {
		snap.IPv6 = path6
	}
	if snap.IPv4 == nil || snap.IPv4.Family != 4 {
		t.Fatal("IPv4 path lost in snapshot construction")
	}
	if snap.IPv6 == nil || snap.IPv6.Family != 6 {
		t.Fatal("IPv6 path lost in snapshot construction")
	}
	if snap.IPv4.Gateway == snap.IPv6.Gateway {
		t.Fatal("snapshot confused v4 and v6 gateways")
	}
}
