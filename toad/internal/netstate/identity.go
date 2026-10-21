package netstate

import "net/netip"

func (p *Path) equal(other *Path) bool {
	if p == nil || other == nil {
		return p == other
	}
	return p.Family == other.Family && p.IfIndex == other.IfIndex && p.Interface == other.Interface &&
		p.Gateway == other.Gateway && p.PreferredSrc == other.PreferredSrc && p.MTU == other.MTU &&
		p.Table == other.Table && p.Metric == other.Metric
}

func IdentityEqual(a, b Snapshot) bool {
	// ConnectionID is NetworkManager connectivity metadata, not a transport
	// path identity. BSSID is retained because a Wi-Fi identity change forces
	// validation even when the kernel path happens to look equal.
	return a.IPv4.equal(b.IPv4) && a.IPv6.equal(b.IPv6) && a.Metadata.BSSID == b.Metadata.BSSID
}

func (s Snapshot) Available(family int) bool {
	if family == 4 {
		return s.IPv4 != nil && s.IPv4.PreferredSrc.IsValid()
	}
	if family == 6 {
		return s.IPv6 != nil && s.IPv6.PreferredSrc.IsValid()
	}
	return false
}

func IsMappedIPv6(a netip.Addr) bool { return a.Is6() && a.Unmap().Is4() }
