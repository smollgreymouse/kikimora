//go:build windows

package awg2

import (
	"errors"
	"testing"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"golang.org/x/sys/windows"
)

var errAttachStub = errors.New("attach stub")

type fakeGuidTunnel struct {
	name string
	mtu  int
	guid windows.GUID
}

func (f fakeGuidTunnel) Name() string { return f.name }
func (f fakeGuidTunnel) IfIndex() int { return 7 }
func (f fakeGuidTunnel) MTU() int     { return f.mtu }
func (f fakeGuidTunnel) Close() error { return nil }
func (f fakeGuidTunnel) TunnelGUID() windows.GUID {
	return f.guid
}

// bareTunnel satisfies platform.Tunnel without the windows GUID contract.
type bareTunnel struct {
	name string
	mtu  int
}

func (b bareTunnel) Name() string { return b.name }
func (b bareTunnel) IfIndex() int { return 7 }
func (b bareTunnel) MTU() int     { return b.mtu }
func (b bareTunnel) Close() error { return nil }

func TestAttachForwardsDeterministicIdentity(t *testing.T) {
	guid := windows.GUID{Data1: 0x1234, Data4: [8]byte{1, 2, 3}}
	tunnel := fakeGuidTunnel{name: "kkone", mtu: 1380, guid: guid}

	var gotName string
	var gotGUID *windows.GUID
	var gotMTU int
	previousOpen := openAWGTunnel
	openAWGTunnel = func(name string, guid *windows.GUID, mtu int) (awgtun.Device, error) {
		gotName = name
		if guid != nil {
			copied := *guid
			gotGUID = &copied
		}
		gotMTU = mtu
		return nil, errAttachStub
	}
	t.Cleanup(func() { openAWGTunnel = previousOpen })

	if _, err := attachTunnel(tunnel); !errors.Is(err, errAttachStub) {
		t.Fatalf("expected stub error, got %v", err)
	}
	if gotName != "kkone" || gotMTU != 1380 {
		t.Fatalf("identity mismatch: name=%q mtu=%d", gotName, gotMTU)
	}
	if gotGUID == nil || *gotGUID != guid {
		t.Fatalf("GUID mismatch: %+v", gotGUID)
	}
}

func TestAttachRejectsTunnelWithoutGUIDContract(t *testing.T) {
	previousOpen := openAWGTunnel
	openAWGTunnel = func(name string, guid *windows.GUID, mtu int) (awgtun.Device, error) {
		t.Fatal("must not touch the driver for a tunnel without the GUID contract")
		return nil, nil
	}
	t.Cleanup(func() { openAWGTunnel = previousOpen })

	if _, err := attachTunnel(bareTunnel{name: "kkone", mtu: 1380}); err == nil {
		t.Fatal("tunnel without the GUID contract must be rejected")
	}
}
