//go:build !linux && !darwin

package openconnect

import (
	"fmt"
	"os"
)

func signalReconnect(*os.Process) error {
	return fmt.Errorf("OpenConnect reconnect signal is unsupported on this platform")
}
