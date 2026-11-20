//go:build !linux && !windows

package config

import "fmt"

// leshyRestartSteps is unsupported on this platform; the lists stay on disk for the operator.
func leshyRestartSteps() ([]reloadStep, error) {
	return nil, fmt.Errorf("reloading Leshy is not supported on this platform")
}
