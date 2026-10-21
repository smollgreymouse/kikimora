package netns

import (
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"net/netip"
	"testing"
)

func TestUnderlayIdentityIgnoresConnectivityMetadata(t *testing.T) {
	a := netstate.Snapshot{Epoch: 3, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0", Gateway: netip.MustParseAddr("192.0.2.1"), PreferredSrc: netip.MustParseAddr("192.0.2.2")}}
	b := a
	b.Metadata.ConnectionID = "nm-id"
	b.ObservedAt = a.ObservedAt.Add(1)
	if !netstate.IdentityEqual(a, b) {
		t.Fatal("connectivity metadata changed the path identity")
	}
	b.Metadata.BSSID = "wifi-b"
	if netstate.IdentityEqual(a, b) {
		t.Fatal("Wi-Fi identity change was ignored")
	}
}
