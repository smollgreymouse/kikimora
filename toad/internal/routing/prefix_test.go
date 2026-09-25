package routing

import (
	"net/netip"
	"testing"
)

func TestIsHostPrefix(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"192.0.2.10/32", true},
		{"2001:db8::10/128", true},
		{"192.0.2.0/24", false},
		{"2001:db8::/64", false},
		{"0.0.0.0/0", false},
		{"::/0", false},
	}
	for _, tc := range tests {
		prefix := netip.MustParsePrefix(tc.raw)
		if got := IsHostPrefix(prefix); got != tc.want {
			t.Fatalf("IsHostPrefix(%s)=%v want %v", tc.raw, got, tc.want)
		}
	}
}
