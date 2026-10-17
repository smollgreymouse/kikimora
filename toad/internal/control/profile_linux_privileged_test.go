//go:build linux && privileged

package control

import (
	"os"
	"testing"
)

func TestPrivilegedProfileSwitchingLinux(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root required for privileged profile integration test")
	}
	runProfileSwitchingPrivileged(t)
}
