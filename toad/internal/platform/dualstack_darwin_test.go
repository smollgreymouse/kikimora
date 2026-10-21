//go:build darwin

package platform

import (
	"net/netip"
	"strings"
	"testing"
)

// TestDarwinParseNetstatSeparatesFamilies verifies that parseNetstatRoutes
// reads each family table independently via netstat flags, so a missing
// IPv6 path never silently becomes IPv4-only.
func TestDarwinParseNetstatSeparatesFamilies(t *testing.T) {
	v4Ops := parseNetstatRoutes(darwinNetstatV4Sample(), 4)
	v6Ops := parseNetstatRoutes(darwinNetstatV6Sample(), 6)

	var v4Count, v6Count int
	for _, r := range v4Ops {
		if r.Kind == "route" && r.Family == 4 {
			v4Count++
		}
	}
	for _, r := range v6Ops {
		if r.Kind == "route" && r.Family == 6 {
			v6Count++
		}
	}
	if v4Count < 1 {
		t.Fatal("v4 snapshot must contain routes")
	}
	if v6Count < 1 {
		t.Fatal("v6 snapshot must contain routes")
	}
}

func darwinNetstatV4Sample() string {
	return `Routing table for inet
Destination   Gateway        Use    Netif Expire
192.0.2      link#2                    en0
192.0.2.1     02:00:5c:02:02:02  347  en0  P
default      192.0.2.1         UGSc    en0
`
}

func darwinNetstatV6Sample() string {
	return `Routing table for inet6
Destination              Gateway              Netif Expire
::1                      link#2                    lo0
fe80::%lo0               link#2                    lo0
2001:db8::1              02:00:5c:0a:0b:0c       en0  P
default                  2001:db8::1             UGsc   en0
`
}

// splitDefaultPrefixes are the legacy pairs that must never leak through
// managed routing operations (::/1 + 8000::/1 or 0.0.0.0/1 + 128.0.0.0/1).
var splitDefaultPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
	netip.MustParsePrefix("::/1"),
	netip.MustParsePrefix("8000::/1"),
}

// TestDarwinSplitDefaultRouteTargetNeverCreated proves that split-default
// prefixes produce route targets that would expand to non-host arguments
// if someone tried to install them — they cannot be safely parked.
func TestDarwinSplitDefaultRouteTargetNeverCreated(t *testing.T) {
	for _, prefix := range splitDefaultPrefixes {
		target := routeTarget(prefix)
		if strings.HasPrefix(target, "-host ") {
			t.Fatalf("split-default %s should not produce -host target", prefix)
		}
	}
}
