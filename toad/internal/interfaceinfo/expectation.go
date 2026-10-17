package interfaceinfo

import "net/netip"

// Expectation is protocol-owned local interface configuration. It contains no
// route/DNS policy and is safe for the Toad runtime to reconcile in place.
type Expectation struct {
	// IfIndex is the last authoritative interface identity. Zero means that
	// identity has not been established yet and must not be guessed by name.
	IfIndex   int
	MTU       int
	Addresses []netip.Prefix
}
