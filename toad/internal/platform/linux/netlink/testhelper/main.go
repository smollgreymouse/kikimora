//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	kknetlink "github.com/smollgreymouse/kikimora/toad/internal/platform/linux/netlink"
	vnl "github.com/vishvananda/netlink"
)

func main() {
	var (
		role    = flag.String("role", "test", "parking role")
		prefix  = flag.String("prefix", "", "selected host prefix")
		ifindex = flag.Int("ifindex", 0, "selected route interface index")
		metric  = flag.Int("metric", 10, "selected route metric")
	)
	flag.Parse()
	if *prefix == "" || *ifindex <= 0 {
		fatalf("-prefix and positive -ifindex are required")
	}
	selected, err := netip.ParsePrefix(*prefix)
	if err != nil || selected.Bits() != selected.Addr().BitLen() {
		fatalf("invalid host prefix %q", *prefix)
	}

	ctx := context.Background()
	executor := kknetlink.Executor{}
	manager := parking.NewManager(executor)
	if err := manager.PrepareWithdrawal(ctx, *role, []netip.Prefix{selected}); err != nil {
		fatalf("park selected prefix: %v", err)
	}
	state := manager.Snapshot(*role)
	if !state.Active || state.Count != 1 {
		fatalf("parking state after prepare: %#v", state)
	}
	if !hasUnreachable(ctx, executor, selected) {
		fatalf("kernel has no unreachable park for %s", selected)
	}

	route := selectedRoute(selected, *ifindex, *metric)
	if err := vnl.RouteDel(&route); err != nil {
		fatalf("remove selected route: %v", err)
	}
	if leaked, detail := routeLeaks(selected.Addr(), *ifindex); leaked {
		fatalf("selected destination leaked after withdrawal: %s", detail)
	}

	if err := vnl.RouteReplace(&route); err != nil {
		fatalf("restore selected route: %v", err)
	}
	released, err := manager.ObserveRestorationFromKernel(ctx, *role)
	if err != nil {
		fatalf("observe restoration: %v", err)
	}
	if released != 1 || manager.Snapshot(*role).Active {
		fatalf("park was not released after winning selected route: released=%d state=%#v", released, manager.Snapshot(*role))
	}
	if hasUnreachable(ctx, executor, selected) {
		fatalf("unreachable park remained after restoration for %s", selected)
	}
	if leaked, detail := routeLeaks(selected.Addr(), *ifindex); leaked {
		fatalf("restored destination does not use selected interface: %s", detail)
	}

	fmt.Printf("Go route parking cycle passed: prefix=%s ifindex=%d\n", selected, *ifindex)
}

func selectedRoute(prefix netip.Prefix, ifindex, metric int) vnl.Route {
	_, dst, err := net.ParseCIDR(prefix.String())
	if err != nil {
		panic(err)
	}
	return vnl.Route{
		Dst:       dst,
		LinkIndex: ifindex,
		Table:     254,
		Priority:  metric,
		Protocol:  vnl.RouteProtocol(4),
	}
}

func hasUnreachable(ctx context.Context, executor kknetlink.Executor, prefix netip.Prefix) bool {
	state, err := executor.Snapshot(ctx)
	if err != nil {
		fatalf("snapshot kernel routes: %v", err)
	}
	for _, route := range state.Routes {
		if route.Kind == "unreachable" && route.Table == 254 && route.Prefix == prefix.String() && route.Metric >= 42760 {
			return true
		}
	}
	return false
}

func routeLeaks(destination netip.Addr, selectedIfIndex int) (bool, string) {
	routes, err := vnl.RouteGet(net.IP(destination.AsSlice()))
	if err != nil {
		// RTN_UNREACHABLE is expected to make RouteGet fail. That is the
		// strongest fail-closed result.
		return false, "unreachable"
	}
	for _, route := range routes {
		if route.Type == 7 {
			return false, "unreachable"
		}
		if route.LinkIndex == selectedIfIndex {
			return false, fmt.Sprintf("selected ifindex=%d", selectedIfIndex)
		}
		if route.LinkIndex > 0 {
			return true, fmt.Sprintf("kernel chose ifindex=%d route=%+v", route.LinkIndex, route)
		}
	}
	return false, "no forwarding route"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
