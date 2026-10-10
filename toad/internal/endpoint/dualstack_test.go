package endpoint

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// TestResolveSeparatesFamiliesForDualStack verifies that endpoint.Resolve
// returns separate IPv4 and IPv6 addresses in a way that callers can filter
// by family. This is the foundation of per-family endpoint routing:
// a caller can select IPv4 routes only, IPv6 routes only, or both.

func TestResolveSeparatesFamiliesForDualStack(t *testing.T) {
	resolved, err := Resolve(context.Background(), testResolver{addresses: []netip.Addr{
		netip.MustParseAddr("192.0.2.10"),
		netip.MustParseAddr("2001:db8::10"),
	}}, "vpn.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	var v4, v6 []netip.AddrPort
	for _, r := range resolved {
		if r.Addr().Is4() {
			v4 = append(v4, r)
		} else if r.Addr().Is6() {
			v6 = append(v6, r)
		}
	}
	if len(v4) != 1 {
		t.Fatalf("expected one v4 endpoint, got %d", len(v4))
	}
	if len(v6) != 1 {
		t.Fatalf("expected one v6 endpoint, got %d", len(v6))
	}
}

// TestResolveRejectsMappedIPv6PreservesDualStack proves that mapped IPv6
// addresses (::ffff:x.x.x.x) are rejected, so a caller filtering by Is4In6
// never sees a false IPv6. This prevents the legacy DNS/AAAA blackhole
// from reappearing at the endpoint resolution layer.
func TestResolveRejectsMappedIPv6PreservesDualStack(t *testing.T) {
	resolved, err := Resolve(context.Background(), testResolver{addresses: []netip.Addr{
		netip.MustParseAddr("::ffff:192.0.2.10"), // must be dropped
		netip.MustParseAddr("192.0.2.11"),
		netip.MustParseAddr("2001:db8::11"),
	}}, "vpn.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resolved {
		if r.Addr().Is4In6() {
			t.Fatalf("mapped IPv6 must be rejected at resolve: %v", r)
		}
	}
	if len(resolved) != 2 {
		t.Fatalf("expected v4+v6 after mapped rejection, got %d: %#v", len(resolved), resolved)
	}
}

// TestManagerRefreshIsIndependentPerFamily verifies that per-family endpoint
// resolution does not depend on the other family being present. A role can
// have IPv4-only or IPv6-only endpoints; the manager must handle both without
// treating the other as an error condition.
func TestManagerRefreshIsIndependentPerFamily(t *testing.T) {
	cases := []struct {
		name    string
		addrs   []netip.Addr
		wantV4  int
		wantV6  int
	}{
		{"v4-only", []netip.Addr{netip.MustParseAddr("192.0.2.10")}, 1, 0},
		{"v6-only", []netip.Addr{netip.MustParseAddr("2001:db8::10")}, 0, 1},
		{"dual-stack", []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Manager{}
			spec := []EndpointSpec{{Hostname: "vpn.example", Port: 443}}
			state, err := m.RefreshSpecsWithError(context.Background(), 1, spec, testResolver{addresses: tc.addrs})
			if err != nil && !errors.Is(err, ErrResolutionUnavailable) {
				t.Fatal(err)
			}
			var v4, v6 int
			for _, r := range state.Resolved {
				if r.Addr().Is4() {
					v4++
				} else if r.Addr().Is6() {
					v6++
				}
			}
			if v4 != tc.wantV4 || v6 != tc.wantV6 {
				t.Fatalf("family split wrong: v4=%d(want %d) v6=%d(want %d)", v4, tc.wantV4, v6, tc.wantV6)
			}
		})
	}
}
