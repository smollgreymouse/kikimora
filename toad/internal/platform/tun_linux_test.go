//go:build linux

package platform

import (
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreateTunnelLongNameFailsBeforeDeviceAccess(t *testing.T) {
	longName := strings.Repeat("a", unix.IFNAMSIZ)
	_, err := CreateTunnel(TunnelSpec{
		Name:      longName,
		MTU:       1500,
		Addresses: []netip.Prefix{},
	})
	if err == nil {
		t.Fatal("expected overlong Linux interface name to be rejected")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected deterministic name-length error, got: %v", err)
	}
}
