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

// requireWintunAdapter skips the adapter-lifecycle tests unless this host can
// really create a Wintun adapter. Two independent preconditions must hold: the
// wintun.dll redistributable has to be loadable, and the process has to be
// elevated, because WintunCreateAdapter loads a kernel driver.
//
// The probe mirrors the load contract of golang.zx2c4.com/wintun, which calls
// LoadLibraryEx with LOAD_LIBRARY_SEARCH_APPLICATION_DIR|SYSTEM32 only: a DLL
// that merely sits somewhere on PATH does not count, so the product has to
// ship or install wintun.dll next to its own binaries.
func requireWintunAdapter(t *testing.T) {
	t.Helper()

	wintunDLL, err := windows.LoadLibraryEx(
		"wintun.dll",
		0,
		windows.LOAD_LIBRARY_SEARCH_APPLICATION_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32,
	)
	if err != nil {
		t.Skipf("wintun.dll is not loadable from the binary directory or System32 (%v); the Wintun adapter lifecycle tests need a prepared Windows acceptance host", err)
	}
	_ = windows.FreeLibrary(wintunDLL)

	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("creating a Wintun adapter requires an elevated process")
	}
}

func TestReleaseAdapterClosesHandle(t *testing.T) {
	requireWintunAdapter(t)

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
	requireWintunAdapter(t)

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
