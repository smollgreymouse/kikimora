//go:build linux

package openconnect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readInterfaceCounters(iface string) (uint64, uint64) {
	read := func(name string) uint64 {
		raw, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", name))
		if err != nil {
			return 0
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0
		}
		return value
	}
	return read("rx_bytes"), read("tx_bytes")
}
