//go:build !linux

package openconnect

func readInterfaceCounters(string) (uint64, uint64) { return 0, 0 }
