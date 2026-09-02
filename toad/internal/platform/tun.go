package platform

import (
	"net/netip"
	"os"
)

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

// FDProvider is implemented by platforms that can safely expose a duplicate of
// the owner fd to a protocol core. The duplicate does not transfer ownership of
// the Toad-managed tunnel lifecycle.
type FDProvider interface {
	DuplicateFile() (*os.File, error)
}
