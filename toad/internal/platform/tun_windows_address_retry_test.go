//go:build windows

package platform

import (
	"errors"
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// applyTunnelAddresses gives a freshly created adapter a bounded number of
// chances to accept its address configuration; Windows can answer
// ERROR_NOT_SUPPORTED for a short window while the interface is published.

func stubAddressSeam(t *testing.T) *int {
	t.Helper()
	origSet := setTunnelAddresses
	origDelay := addressRetryDelay
	t.Cleanup(func() {
		setTunnelAddresses = origSet
		addressRetryDelay = origDelay
	})
	addressRetryDelay = 0
	calls := new(int)
	return calls
}

func TestApplyTunnelAddressesAbsorbsTransientFailures(t *testing.T) {
	calls := stubAddressSeam(t)
	setTunnelAddresses = func(_ winipcfg.LUID, _ []netip.Prefix) error {
		*calls++
		if *calls < 3 {
			return errors.New("The request is not supported.")
		}
		return nil
	}

	prefix, err := netip.ParsePrefix("10.8.1.3/32")
	if err != nil {
		t.Fatalf("parse prefix: %v", err)
	}
	if err := applyTunnelAddresses(winipcfg.LUID(1), []netip.Prefix{prefix}); err != nil {
		t.Fatalf("expected the retry loop to absorb transient failures, got %v", err)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", *calls)
	}
}

func TestApplyTunnelAddressesGivesUpAfterBoundedAttempts(t *testing.T) {
	calls := stubAddressSeam(t)
	sentinel := errors.New("The request is not supported.")
	setTunnelAddresses = func(_ winipcfg.LUID, _ []netip.Prefix) error {
		*calls++
		return sentinel
	}

	prefix, err := netip.ParsePrefix("10.8.1.3/32")
	if err != nil {
		t.Fatalf("parse prefix: %v", err)
	}
	err = applyTunnelAddresses(winipcfg.LUID(1), []netip.Prefix{prefix})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the last error to survive, got %v", err)
	}
	if *calls != addressApplyAttempts {
		t.Fatalf("expected %d attempts, got %d", addressApplyAttempts, *calls)
	}
}
