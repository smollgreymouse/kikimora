package netstate

import (
	"net/netip"
	"testing"
)

func path(gateway, source string, ifindex int) *Path {
	return &Path{Family: 4, IfIndex: ifindex, Interface: "wlan0", Gateway: netip.MustParseAddr(gateway), PreferredSrc: netip.MustParseAddr(source), MTU: 1500, Table: 254, Metric: 100}
}
func TestCompareIgnoresObservedAtAndAdvancesMaterialEpoch(t *testing.T) {
	a := Snapshot{Epoch: 1, IPv4: path("192.0.2.1", "192.0.2.2", 3)}
	b := a
	b.ObservedAt = a.ObservedAt.Add(5)
	if got, _, changed := Compare(a, b); changed || got.Epoch != a.Epoch {
		t.Fatalf("timestamp-only change: %#v %v", got, changed)
	}
	b.IPv4 = path("192.0.2.254", "192.0.2.2", 3)
	got, reason, changed := Compare(a, b)
	if !changed || got.Epoch != 2 || reason != ChangeGateway {
		t.Fatalf("gateway change: %#v %v %v", got, reason, changed)
	}
}
func TestMappedIPv6IsRejected(t *testing.T) {
	if !IsMappedIPv6(netip.MustParseAddr("::ffff:192.0.2.1")) {
		t.Fatal("mapped IPv6 accepted")
	}
}

func TestCompareIgnoresConnectivityOnlyMetadata(t *testing.T) {
	old := Snapshot{Epoch: 2, IPv4: path("192.0.2.1", "192.0.2.2", 2), Metadata: Metadata{ConnectionID: "a"}}
	next := old
	next.Metadata.ConnectionID = "b"
	merged, reason, changed := Compare(old, next)
	if changed || reason != "" || merged.Epoch != old.Epoch {
		t.Fatalf("connectivity-only event caused change: %#v %q %v", merged, reason, changed)
	}
}

func TestCompareDetectsWiFiIdentityChange(t *testing.T) {
	old := Snapshot{Epoch: 2, IPv4: path("192.0.2.1", "192.0.2.2", 2), Metadata: Metadata{BSSID: "a"}}
	next := old
	next.Metadata.BSSID = "b"
	merged, reason, changed := Compare(old, next)
	if !changed || reason != ChangeWiFiIdentity || merged.Epoch != 3 {
		t.Fatalf("Wi-Fi identity change not detected: %#v %q %v", merged, reason, changed)
	}
}
