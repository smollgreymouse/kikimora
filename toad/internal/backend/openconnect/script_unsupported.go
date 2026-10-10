//go:build !linux && !windows

package openconnect

import "fmt"

func writeRouteFreeVPNScript(string) (string, error) {
	return "", fmt.Errorf("OpenConnect Toad route-target script is not implemented on this platform")
}
