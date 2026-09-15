//go:build !linux

package openconnect

import "fmt"

func writeRouteFreeVPNScript(string) (string, error) {
	return "", fmt.Errorf("OpenConnect Toad route-target script is currently implemented only on Linux")
}
