package platform

import "net/netip"

// TunnelSpec describes the platform-neutral parameters for creating a TUN device.
type TunnelSpec struct {
	Name      string
	MTU       int
	Addresses []netip.Prefix
}

// Tunnel represents a TUN device owned by one Toad instance.
type Tunnel interface {
	Name() string
	IfIndex() int
	MTU() int
	Close() error
}
