//go:build windows

package control

import (
	"os"

	"golang.org/x/sys/windows"
)

// processIsElevated reports whether the test process can read back a file
// whose DACL is SYSTEM+Administrators only: an unelevated token carries the
// Administrators group deny-only, so the reuse roundtrip needs elevation
// (the packaged core runs as SYSTEM).
func processIsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func readableAfterDACL(path string) bool {
	_, err := os.ReadFile(path)
	return err == nil
}
