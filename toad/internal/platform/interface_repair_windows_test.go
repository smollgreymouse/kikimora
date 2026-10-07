//go:build windows

package platform

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func TestRepairInterfaceRejectsEmptyName(t *testing.T) {
	r := windowsInterfaceRepairer{}
	err := r.RepairInterface(t.Context(), "", interfaceinfo.Expectation{})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestRepairInterfaceRejectsMissingAdapter(t *testing.T) {
	previous := platformIfTable2Ex
	platformIfTable2Ex = func(_ winipcfg.MibIfEntryLevel) ([]winipcfg.MibIfRow2, error) {
		return nil, nil
	}
	t.Cleanup(func() { platformIfTable2Ex = previous })

	r := windowsInterfaceRepairer{}
	err := r.RepairInterface(t.Context(), "nonexistent", interfaceinfo.Expectation{})
	if err == nil {
		t.Fatal("expected error for missing adapter")
	}
}

func TestRepairInterfaceReturnsErrorOnTableFailure(t *testing.T) {
	previous := platformIfTable2Ex
	testErr := errors.New("test error")
	platformIfTable2Ex = func(_ winipcfg.MibIfEntryLevel) ([]winipcfg.MibIfRow2, error) {
		return nil, testErr
	}
	t.Cleanup(func() { platformIfTable2Ex = previous })

	r := windowsInterfaceRepairer{}
	err := r.RepairInterface(t.Context(), "some-if", interfaceinfo.Expectation{IfIndex: 1})
	if !errors.Is(err, testErr) {
		t.Fatalf("expected %v, got %v", testErr, err)
	}
}

func TestLuidUnicastAddressesFiltersByLUID(t *testing.T) {
	previous := platformUnicastTable
	luid := winipcfg.LUID(42)
	other := winipcfg.LUID(99)
	platformUnicastTable = func(family winipcfg.AddressFamily) ([]winipcfg.MibUnicastIPAddressRow, error) {
		if family == windows.AF_INET6 {
			return nil, nil
		}
		row1 := repairCannedUnicast(luid, netip.PrefixFrom(netip.MustParseAddr("10.0.0.1"), 32))
		row2 := repairCannedUnicast(other, netip.PrefixFrom(netip.MustParseAddr("192.168.0.1"), 24))
		return []winipcfg.MibUnicastIPAddressRow{row1, row2}, nil
	}
	t.Cleanup(func() { platformUnicastTable = previous })

	prefixes, err := luidUnicastAddresses(luid)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 {
		t.Fatalf("unexpected prefixes: %v", prefixes)
	}
	if prefixes[0].Addr().String() != "10.0.0.1" {
		t.Fatalf("unexpected address: %v", prefixes[0])
	}
}

func TestLuidUnicastAddressesReturnsErrorOnFailure(t *testing.T) {
	previous := platformUnicastTable
	testErr := errors.New("test error")
	platformUnicastTable = func(_ winipcfg.AddressFamily) ([]winipcfg.MibUnicastIPAddressRow, error) {
		return nil, testErr
	}
	t.Cleanup(func() { platformUnicastTable = previous })

	_, err := luidUnicastAddresses(winipcfg.LUID(1))
	if !errors.Is(err, testErr) {
		t.Fatalf("expected %v, got %v", testErr, err)
	}
}

func TestManagedInterfaceIdentityErrorMessage(t *testing.T) {
	e := &ManagedInterfaceIdentityError{Name: "kk-oc0", ExpectedIfIndex: 4, ActualIfIndex: 7}
	if e.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func repairCannedUnicast(luid winipcfg.LUID, prefix netip.Prefix) winipcfg.MibUnicastIPAddressRow {
	var row winipcfg.MibUnicastIPAddressRow
	row.InterfaceLUID = luid
	row.OnLinkPrefixLength = uint8(prefix.Bits())
	row.Address.SetAddrPort(netip.AddrPortFrom(prefix.Addr(), 0))
	row.DadState = winipcfg.DadStatePreferred
	return row
}