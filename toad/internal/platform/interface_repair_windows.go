//go:build windows

package platform

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type windowsInterfaceRepairer struct{}

func DefaultInterfaceRepairer() InterfaceRepairer { return windowsInterfaceRepairer{} }

func (windowsInterfaceRepairer) RepairInterface(_ context.Context, name string, expected interfaceinfo.Expectation) error {
	if name == "" {
		return fmt.Errorf("managed interface name is empty")
	}

	row, luid, err := findInterfaceByAlias(name)
	if err != nil {
		return fmt.Errorf("lookup managed interface %q: %w", name, err)
	}

	if expected.IfIndex > 0 && int(row.InterfaceIndex) != expected.IfIndex {
		return &ManagedInterfaceIdentityError{
			Name:            name,
			ExpectedIfIndex: expected.IfIndex,
			ActualIfIndex:   int(row.InterfaceIndex),
		}
	}

	if expected.MTU > 0 && int(row.MTU) != expected.MTU {
		if err := setAdapterMTU(luid, expected.MTU); err != nil {
			return fmt.Errorf("restore MTU %d on %q: %w", expected.MTU, name, err)
		}
	}

	if len(expected.Addresses) > 0 {
		existing, err := luidUnicastAddresses(luid)
		if err != nil {
			return fmt.Errorf("read addresses on %q: %w", name, err)
		}
		have := make(map[netip.Prefix]bool, len(existing))
		for _, addr := range existing {
			have[addr] = true
		}
		var missing []netip.Prefix
		for _, prefix := range expected.Addresses {
			if prefix.IsValid() && !have[prefix] {
				missing = append(missing, prefix)
			}
		}
		if len(missing) > 0 {
			if err := luid.AddIPAddresses(missing); err != nil {
				return fmt.Errorf("restore addresses on %q: %w", name, err)
			}
		}
	}

	// Verify all expected addresses are now present.
	if len(expected.Addresses) > 0 {
		verify, err := luidUnicastAddresses(luid)
		if err != nil {
			return fmt.Errorf("verify addresses on %q: %w", name, err)
		}
		present := make(map[netip.Prefix]bool, len(verify))
		for _, addr := range verify {
			present[addr] = true
		}
		for _, prefix := range expected.Addresses {
			if prefix.IsValid() && !present[prefix] {
				return fmt.Errorf("address %s was not restored on %q", prefix, name)
			}
		}
	}
	return nil
}

// findInterfaceByAlias walks the system interface table and returns the
// first row whose adapter alias matches the given name.
func findInterfaceByAlias(name string) (winipcfg.MibIfRow2, winipcfg.LUID, error) {
	table, err := winipcfg.GetIfTable2Ex(winipcfg.MibIfEntryNormal)
	if err != nil {
		return winipcfg.MibIfRow2{}, 0, err
	}
	for i := range table {
		if table[i].Alias() == name {
			return table[i], table[i].InterfaceLUID, nil
		}
	}
	return winipcfg.MibIfRow2{}, 0, fmt.Errorf("interface %q not found", name)
}

// luidUnicastAddresses returns all unicast prefixes currently assigned to
// the adapter identified by the given LUID, across both address families.
func luidUnicastAddresses(luid winipcfg.LUID) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		rows, err := winipcfg.GetUnicastIPAddressTable(family)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			if rows[i].InterfaceLUID != luid {
				continue
			}
			addr := rows[i].Address.Addr()
			if !addr.IsValid() {
				continue
			}
			out = append(out, netip.PrefixFrom(addr, int(rows[i].OnLinkPrefixLength)))
		}
	}
	return out, nil
}

// setAdapterMTU applies the desired MTU to both IPv4 and IPv6 IP-interface
// entries. Either call may fail if the address family is not configured on
// the adapter; that is tolerable because the address-family-specific repair
// will still succeed on the other family.
func setAdapterMTU(luid winipcfg.LUID, mtu int) error {
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		entry, err := luid.IPInterface(family)
		if err != nil {
			continue
		}
		entry.NLMTU = uint32(mtu)
		if err := entry.Set(); err != nil {
			return fmt.Errorf("set %s MTU: %w", familyLabel(family), err)
		}
	}
	return nil
}

func familyLabel(f winipcfg.AddressFamily) string {
	switch f {
	case windows.AF_INET:
		return "IPv4"
	case windows.AF_INET6:
		return "IPv6"
	default:
		return fmt.Sprintf("family(%d)", f)
	}
}
