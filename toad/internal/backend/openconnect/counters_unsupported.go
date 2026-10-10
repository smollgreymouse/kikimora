//go:build !linux && !windows

package openconnect

func readInterfaceCounters(string) (uint64, uint64) { return 0, 0 }
