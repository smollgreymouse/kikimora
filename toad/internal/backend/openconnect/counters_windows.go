//go:build windows

package openconnect

import (
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// readInterfaceCounters returns the RX/TX byte counters for the named
// network interface on Windows via the IP Helper GetIfTable2Ex facility
// already used by the underlay observer. The call is cheap (one kernel
// round-trip) and never allocates beyond the returned table slice.
func readInterfaceCounters(iface string) (uint64, uint64) {
	table, err := winipcfg.GetIfTable2Ex(winipcfg.MibIfEntryNormal)
	if err != nil {
		return 0, 0
	}
	for i := range table {
		if table[i].Alias() == iface {
			return table[i].InOctets, table[i].OutOctets
		}
	}
	return 0, 0
}
