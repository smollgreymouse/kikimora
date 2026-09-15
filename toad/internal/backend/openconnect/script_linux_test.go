//go:build linux

package openconnect

import (
	"strings"
	"testing"
)

func TestRouteFreeScriptOnlyConfiguresManagedInterface(t *testing.T) {
	for _, forbidden := range []string{"ip route", "resolvectl", "nmcli", "/etc/resolv.conf", "iptables", "nft "} {
		if strings.Contains(routeFreeVPNScript, forbidden) {
			t.Fatalf("route-free script unexpectedly contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"connect|reconnect",
		"ip link set dev \"$TUNDEV\" up",
		"INTERNAL_IP4_ADDRESS",
		"INTERNAL_IP4_NETMASKLEN",
		"openconnect-network.env",
		"INTERNAL_IP4_DNS",
		"CISCO_SPLIT_DNS",
		"chmod 0600",
	} {
		if !strings.Contains(routeFreeVPNScript, required) {
			t.Fatalf("route-free script is missing %q", required)
		}
	}
}
