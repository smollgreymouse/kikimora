//go:build !windows

package control

import "os"

func processIsElevated() bool { return true }

func readableAfterDACL(path string) bool {
	_, err := os.ReadFile(path)
	return err == nil
}
