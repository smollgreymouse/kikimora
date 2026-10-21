//go:build !linux && !windows

package config

import "fmt"

// leshyRestartCommands is unsupported on this platform; the lists stay on disk for the operator.
func leshyRestartCommands() ([][]string, error) {
	return nil, fmt.Errorf("reloading Leshy is not supported on this platform")
}
