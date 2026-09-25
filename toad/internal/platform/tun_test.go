package platform

import (
	"net/netip"
	"testing"
)

func TestCreateTunnel_InvalidName(t *testing.T) {
	_, err := CreateTunnel(TunnelSpec{
		Name:      "",
		MTU:       1500,
		Addresses: []netip.Prefix{},
	})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestCreateTunnel_InvalidMTU(t *testing.T) {
	_, err := CreateTunnel(TunnelSpec{
		Name:      "test",
		MTU:       0,
		Addresses: []netip.Prefix{},
	})
	if err == nil {
		t.Error("expected error for zero MTU")
	}
	_, err = CreateTunnel(TunnelSpec{
		Name:      "test",
		MTU:       -1,
		Addresses: []netip.Prefix{},
	})
	if err == nil {
		t.Error("expected error for negative MTU")
	}
}
