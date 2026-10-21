//go:build windows && privileged

package control

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivilegedProfileSwitchingWindows(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	if !token.IsElevated() {
		t.Skip("elevated process required")
	}
	runProfileSwitchingPrivileged(t)
}
