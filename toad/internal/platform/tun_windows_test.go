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

func TestReleaseAdapterClosesHandle(t *testing.T) {
	spec := TunnelSpec{Name: "test-release", MTU: 1420}
	tunnel, err := CreateTunnel(spec)
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	wt := tunnel.(*windowsTunnel)

	if wt.adapter == nil {
		t.Fatal("adapter handle must exist after CreateTunnel")
	}
	if err := tunnel.(*windowsTunnel).ReleaseAdapter(); err != nil {
		t.Fatalf("ReleaseAdapter: %v", err)
	}
	if wt.adapter != nil {
		t.Fatal("adapter handle must be nil after ReleaseAdapter")
	}
	// Close should succeed even after release (adapter already nil).
	if err := tunnel.Close(); err != nil {
		t.Fatalf("Close after release: %v", err)
	}
}

func TestReleaseAdapterIdempotent(t *testing.T) {
	spec := TunnelSpec{Name: "test-release2", MTU: 1420}
	tunnel, err := CreateTunnel(spec)
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	wt := tunnel.(*windowsTunnel)
	if err := wt.ReleaseAdapter(); err != nil {
		t.Fatalf("first ReleaseAdapter: %v", err)
	}
	if err := wt.ReleaseAdapter(); err != nil {
		t.Fatalf("second ReleaseAdapter: %v", err)
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
