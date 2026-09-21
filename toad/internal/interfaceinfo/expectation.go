package interfaceinfo

import "net/netip"

// Expectation is protocol-owned local interface configuration. It contains no
// route/DNS policy and is safe for the Toad runtime to reconcile in place.
type Expectation struct {
	MTU       int
	Addresses []netip.Prefix
}
