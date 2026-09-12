//go:build !linux

package platform

import "errors"

// CreateTunnel reports that the platform adapter has not been implemented yet.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	return nil, errors.New("TUN is not supported on this platform")
}
