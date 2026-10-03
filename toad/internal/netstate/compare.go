package netstate

import "net/netip"

func Compare(old, next Snapshot) (Snapshot, ChangeReason, bool) {
	if old.Epoch == 0 && !IdentityEqual(old, next) {
		next.Epoch = 1
		return next, ChangeInitial, true
	}
	if IdentityEqual(old, next) {
		next.Epoch = old.Epoch
		return next, "", false
	}
	if pathIdentityChanged(old.IPv4, next.IPv4) || pathIdentityChanged(old.IPv6, next.IPv6) {
		next.Epoch = old.Epoch + 1
		return next, ChangeInterface, true
	}
	if addrChanged(old.IPv4, next.IPv4, func(p *Path) netip.Addr { return p.Gateway }) || addrChanged(old.IPv6, next.IPv6, func(p *Path) netip.Addr { return p.Gateway }) {
		next.Epoch = old.Epoch + 1
		return next, ChangeGateway, true
	}
	if addrChanged(old.IPv4, next.IPv4, func(p *Path) netip.Addr { return p.PreferredSrc }) || addrChanged(old.IPv6, next.IPv6, func(p *Path) netip.Addr { return p.PreferredSrc }) {
		next.Epoch = old.Epoch + 1
		return next, ChangePreferredSource, true
	}
	if (old.IPv4 == nil) != (next.IPv4 == nil) || (old.IPv6 == nil) != (next.IPv6 == nil) {
		next.Epoch = old.Epoch + 1
		return next, ChangeAvailability, true
	}
	if old.Metadata.BSSID != next.Metadata.BSSID {
		next.Epoch = old.Epoch + 1
		return next, ChangeWiFiIdentity, true
	}
	next.Epoch = old.Epoch + 1
	return next, ChangeInterface, true
}

func addrChanged(a, b *Path, value func(*Path) netip.Addr) bool {
	if a == nil || b == nil {
		return false
	}
	return value(a) != value(b)
}

func pathIdentityChanged(a, b *Path) bool {
	if a == nil || b == nil {
		return false
	}
	return a.IfIndex != b.IfIndex || a.Interface != b.Interface
}
