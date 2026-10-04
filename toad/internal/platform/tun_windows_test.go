//go:build windows

package platform

import (
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTunnelGUIDStableAndDistinct(t *testing.T) {
	first := tunnelGUID("one")
	again := tunnelGUID("one")
	other := tunnelGUID("two")

	if first != again {
		t.Fatal("the same role must always derive the same adapter GUID")
	}
	if first == other {
		t.Fatal("distinct roles must never collide on one adapter GUID")
	}
	if first == (windows.GUID{}) {
		t.Fatal("derived GUID must not be the zero GUID")
	}
}

func TestCreateTunnelWindowsValidationFailsBeforeDriver(t *testing.T) {
	longName := strings.Repeat("a", maxWindowsTunnelName+1)
	if _, err := CreateTunnel(TunnelSpec{Name: longName, MTU: 1500}); err == nil {
		t.Fatal("overlong Wintun adapter name must be rejected")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected deterministic name-length error, got: %v", err)
	}

	if _, err := CreateTunnel(TunnelSpec{Name: " padded ", MTU: 1500}); err == nil {
		t.Fatal("adapter names with surrounding whitespace must be rejected")
	}

	if _, err := CreateTunnel(TunnelSpec{Name: "ok-name", MTU: 0}); err == nil {
		t.Fatal("zero MTU must be rejected before driver access")
	}

	if _, err := CreateTunnel(TunnelSpec{
		Name:      "ok-name",
		MTU:       1500,
		Addresses: []netip.Prefix{netip.Prefix{}},
	}); err == nil {
		t.Fatal("invalid address prefix must be rejected before driver access")
	}
}
