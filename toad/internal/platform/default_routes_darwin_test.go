//go:build darwin

package platform

import (
	"net/netip"
	"testing"
)

func TestDarwinNetstatPrefixParsing(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		family    int
	}{
		{"default", "0.0.0.0/0", 4},
		{"default", "::/0", 6},
		{"198.51.100.7", "198.51.100.7/32", 4},
		{"2001:db8::7", "2001:db8::7/128", 6},
		{"198.51.100", "198.51.100.0/24", 4},
	} {
		got, ok := netstatPrefix(test.raw, test.family)
		if !ok || got != test.want {
			t.Fatalf("netstatPrefix(%q, %d) = %q, %v; want %q, true", test.raw, test.family, got, ok, test.want)
		}
	}
}

func TestDarwinRouteTargetIsExact(t *testing.T) {
	if got := routeTarget(mustPrefix("198.51.100.7/32")); got != "-host 198.51.100.7" {
		t.Fatalf("unexpected host route target: %q", got)
	}
	if got := routeTarget(mustPrefix("198.51.100.0/24")); got != "-net 198.51.100.0/24" {
		t.Fatalf("unexpected network route target: %q", got)
	}
}

func mustPrefix(raw string) (prefix netip.Prefix) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return prefix
}
