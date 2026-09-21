package endpoint

import (
	"context"
	"net"
	"net/netip"
)

type Resolver interface {
	LookupNetIP(context.Context, string) ([]netip.Addr, error)
}
type NetResolver struct{ Resolver *net.Resolver }

func (r NetResolver) LookupNetIP(ctx context.Context, host string) ([]netip.Addr, error) {
	res := r.Resolver
	if res == nil {
		res = net.DefaultResolver
	}
	return res.LookupNetIP(ctx, "ip", host)
}
func Resolve(ctx context.Context, r Resolver, host string, port uint16) ([]netip.AddrPort, error) {
	addrs, err := r.LookupNetIP(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.AddrPort, 0, len(addrs))
	for _, a := range addrs {
		if a.Is4In6() {
			continue
		}
		if a.Is4() || a.Is6() {
			out = append(out, netip.AddrPortFrom(a, port))
		}
	}
	return out, nil
}
