//go:build darwin && privileged

package control

import (
	"os"
	"testing"
)

func TestPrivilegedProfileSwitchingDarwin(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root required")
	}
	runProfileSwitchingPrivileged(t)
}
