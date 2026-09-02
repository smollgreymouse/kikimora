//go:build linux

package platform

import (
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreateTunnel_LongName(t *testing.T) {
	longName := make([]byte, unix.IFNAMSIZ)
	for i := range longName {
		longName[i] = 'a'
	}
	_, err := CreateTunnel(TunnelSpec{
		Name:      string(longName),
		MTU:       1500,
		Addresses: []netip.Prefix{},
	})
	if err == nil {
		t.Error("expected error for too long name")
	} else if !errors.Is(err, nil) { // Any error is acceptable for this test
		// We just want to make sure it doesn't panic
	}
}

func TestDuplicateFile(t *testing.T) {
	// Skip if we can't open /dev/net/tun (not running as root or in container without permissions)
	if err := unix.Access("/dev/net/tun", unix.W_OK); err != nil {
		t.Skipf("skipping privileged test: %v", err)
		return
	}

	spec := TunnelSpec{
		Name:      "kk-toad-test",
		MTU:       1500,
		Addresses: []netip.Prefix{},
	}

	tun, err := CreateTunnel(spec)
	if err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skipf("skipping privileged test: %v", err)
			return
		}
		t.Fatalf("CreateTunnel failed: %v", err)
	}
	defer tun.Close()

	dupFile, err := tun.(*linuxTunnel).DuplicateFile()
	if err != nil {
		t.Fatalf("DuplicateFile failed: %v", err)
	}
	defer dupFile.Close()

	// Verify that the original tunnel is still usable after closing duplicate
	// (we can't easily test the interface still exists without privileges,
	// but we can test that Close doesn't error on the original)
	if err := tun.Close(); err != nil {
		t.Fatalf("Close failed after duplicating: %v", err)
	}
}
