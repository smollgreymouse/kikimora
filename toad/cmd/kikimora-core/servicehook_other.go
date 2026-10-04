//go:build !windows

package main

import "fmt"

// maybeRunService is a no-op on non-Windows platforms.
func maybeRunService() bool { return false }

// serviceCommand reports that SCM hosting is windows-only.
func serviceCommand(args []string) error {
	return fmt.Errorf("service management is only supported on windows")
}
