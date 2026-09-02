//go:build !linux

package platform

import (
	"errors"
	"net/netip"
)

// CreateTunnel returns an error indicating that TUN is not supported on this platform.
func CreateTunnel(spec TunnelSpec) (Tunnel, error) {
	return nil, errors.New("TUN is not supported on this platform")
}
