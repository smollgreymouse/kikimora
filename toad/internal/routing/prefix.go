package routing

import "net/netip"

// IsHostPrefix reports whether prefix identifies exactly one IP address.
func IsHostPrefix(prefix netip.Prefix) bool {
	return prefix.IsValid() &&
		prefix.Addr().IsValid() &&
		prefix.Bits() == prefix.Addr().BitLen()
}
