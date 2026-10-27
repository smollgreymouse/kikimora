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
func TestCompareDetectsPreferredSourceOnlyChange(t *testing.T) {
	old := Snapshot{Epoch: 4, IPv4: path("192.0.2.1", "192.0.2.10", 3)}
	next := old
	next.IPv4 = path("192.0.2.1", "192.0.2.11", 3)
	merged, reason, changed := Compare(old, next)
	if !changed || reason != ChangePreferredSource || merged.Epoch != 5 {
		t.Fatalf("source-only change: merged=%#v reason=%q changed=%v", merged, reason, changed)
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

// TestCompareDetectsWiFiToEthernetSwitch covers the live-network-switch user path: the
// operator moves between Wi-Fi and a wired dock while roles are up. The interface
// identity (ifindex and name, both families) changes, so the epoch must advance and the
// control plane must revalidate everything running.
func TestCompareDetectsWiFiToEthernetSwitch(t *testing.T) {
	old := Snapshot{
		Epoch:    9,
		IPv4:     path("192.168.1.1", "192.168.1.52", 3),
		IPv6:     &Path{Family: 6, IfIndex: 3, Interface: "wlan0", Gateway: netip.MustParseAddr("fe80::1"), PreferredSrc: netip.MustParseAddr("2a00::52"), MTU: 1500, Table: 254, Metric: 600},
		Metadata: Metadata{BSSID: "aa:bb:cc:dd:ee:ff"},
	}
	next := Snapshot{
		Epoch: old.Epoch,
		IPv4:  path("192.168.1.1", "192.168.1.52", 7),
		IPv6:  &Path{Family: 6, IfIndex: 7, Interface: "eth0", Gateway: netip.MustParseAddr("fe80::1"), PreferredSrc: netip.MustParseAddr("2a00::52"), MTU: 1500, Table: 254, Metric: 100},
	}
	next.IPv4.Interface = "eth0"
	merged, reason, changed := Compare(old, next)
	if !changed || reason != ChangeInterface || merged.Epoch != old.Epoch+1 {
		t.Fatalf("wifi→ethernet switch: merged=%#v reason=%q changed=%v", merged, reason, changed)
	}
}
