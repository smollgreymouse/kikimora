package platform

import (
	"net/netip"
	"os"
)

// TunnelSpec describes the parameters for creating a TUN device.
type TunnelSpec struct {
	Name      string
	MTU       int
	Addresses []netip.Prefix
}

// Tunnel represents a TUN device.
type Tunnel interface {
	Name() string
	IfIndex() int
	MTU() int
	Close() error
}

// LinuxTunnelExtensions represents Linux-specific methods on a Tunnel.
type LinuxTunnelExtensions interface {
	DuplicateFile() (*os.File, error)
}
