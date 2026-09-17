package underlay

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
	if r.Resolver == nil {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	return r.Resolver.LookupNetIP(ctx, "ip", host)
}
