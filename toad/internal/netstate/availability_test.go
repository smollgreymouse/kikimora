package netstate

import (
	"net/netip"
	"testing"
)

// availabilityV4/availabilityV6 build minimal per-family paths so the
// availability tests never depend on the cross-test path() helper.
func availabilityV4() *Path {
	return &Path{Family: 4, IfIndex: 3, Interface: "wlan0", Gateway: netip.MustParseAddr("192.0.2.1"), PreferredSrc: netip.MustParseAddr("192.0.2.10"), Table: 254, Metric: 100}
}

func availabilityV6() *Path {
	return &Path{Family: 6, IfIndex: 3, Interface: "wlan0", Gateway: netip.MustParseAddr("2001:db8::1"), PreferredSrc: netip.MustParseAddr("2001:db8::10"), Table: 254, Metric: 100}
}

// TestAvailableIsIndependentPerFamily encodes the first rule of the
// dual-stack contract: IPv4 Ready never implies IPv6 Ready, and vice versa.
func TestAvailableIsIndependentPerFamily(t *testing.T) {
	cases := []struct {
		name        string
		snap        Snapshot
		wantV4      bool
		wantV6      bool
	}{
		{"both", Snapshot{IPv4: availabilityV4(), IPv6: availabilityV6()}, true, true},
		{"ipv4-only", Snapshot{IPv4: availabilityV4()}, true, false},
		{"ipv6-only", Snapshot{IPv6: availabilityV6()}, false, true},
		{"neither", Snapshot{}, false, false},
		{"v6-without-source", Snapshot{IPv6: &Path{Family: 6, IfIndex: 4, Interface: "wlan0", Gateway: netip.MustParseAddr("2001:db8::1")}}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.snap.Available(4); got != tc.wantV4 {
				t.Fatalf("Available(4) = %v, want %v", got, tc.wantV4)
			}
			if got := tc.snap.Available(6); got != tc.wantV6 {
				t.Fatalf("Available(6) = %v, want %v", got, tc.wantV6)
			}
		})
	}
}

// TestCompareDetectsFamilyAvailabilityChange proves that a family appearing
// or disappearing while the other family is unchanged advances the epoch with
// an availability reason, so the control plane never treats IPv4 health as
// IPv6 health.
func TestCompareDetectsFamilyAvailabilityChange(t *testing.T) {
	v4 := availabilityV4()
	v6 := availabilityV6()

	// IPv4 steady, IPv6 appears -> availability change, not a gateway/source change.
	merged, reason, changed := Compare(Snapshot{Epoch: 3, IPv4: v4}, Snapshot{IPv4: v4, IPv6: v6})
	if !changed || reason != ChangeAvailability || merged.Epoch != 4 {
		t.Fatalf("IPv6 appearance missed: merged=%#v reason=%q changed=%v", merged, reason, changed)
	}

	// IPv4 steady, IPv6 disappears (dead family) -> availability change.
	merged, reason, changed = Compare(Snapshot{Epoch: 4, IPv4: v4, IPv6: v6}, Snapshot{IPv4: v4})
	if !changed || reason != ChangeAvailability || merged.Epoch != 5 {
		t.Fatalf("IPv6 disappearance missed: merged=%#v reason=%q changed=%v", merged, reason, changed)
	}
}