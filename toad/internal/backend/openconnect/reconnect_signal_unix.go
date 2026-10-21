//go:build linux || darwin

package openconnect

import (
	"os"
	"syscall"
)

func signalReconnect(process *os.Process) error {
	return process.Signal(syscall.SIGUSR2)
}
