//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	kknetlink "github.com/smollgreymouse/kikimora/toad/internal/platform/linux/netlink"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/vishvananda/netlink"
)

func main() {
	var (
		uplinkIfIndex = flag.Int("uplink-ifindex", 0, "physical underlay interface index")
		vpnIfIndex    = flag.Int("vpn-ifindex", 0, "managed VPN interface index")
		extIfIndex    = flag.Int("ext-ifindex", 0, "optional external VPN interface index")
	)
	flag.Parse()
	if *uplinkIfIndex <= 0 || *vpnIfIndex <= 0 {
		fatalf("-uplink-ifindex and -vpn-ifindex are required and must be positive")
	}

	ctx := context.Background()
	executor := kknetlink.Executor{}

	// --- Test 1: Reconcile dual-stack endpoint policy ---
	v4Endpoint := netip.MustParsePrefix("203.0.113.10/32")
	v6Endpoint := netip.MustParsePrefix("2001:db8:100::10/128")
	policy := endpoint.Policy{
		Role:     "dualstack-test",
		Zone:     "test",
		Priority: 100,
		Source:   endpoint.SourceNative,
		Routes: []endpoint.Route{
			{Prefix: v4Endpoint, IfIndex: *uplinkIfIndex, Metric: 1},
			{Prefix: v6Endpoint, IfIndex: *uplinkIfIndex, Metric: 1},
		},
	}
	if err := executor.ReconcileEndpointPolicy(ctx, policy); err != nil {
		fatalf("reconcile dual-stack endpoint policy: %v", err)
	}
	fmt.Println("PASS: reconcile dual-stack endpoint policy")

	// --- Test 2: No split-default routes were created ---
	state, err := executor.Snapshot(ctx)
	if err != nil {
		fatalf("snapshot after reconcile: %v", err)
	}
	splitDefaults := []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
	for _, sd := range splitDefaults {
		for _, route := range state.Routes {
			if route.Table == 254 && route.Prefix == sd {
				fatalf("split-default route found in main table: %s (metric=%d)", sd, route.Metric)
			}
		}
	}
	fmt.Println("PASS: no split-default routes created")

	// --- Test 3: Endpoint routes exist in table 51890 ---
	// Executor.Snapshot uses RouteList(nil, family) which only returns main
	// table routes, so we query table 51890 directly via netlink.
	epRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: 51890}, netlink.RT_FILTER_TABLE)
	if err != nil {
		fatalf("list table 51890 v4 routes: %v", err)
	}
	v4Found := false
	for _, r := range epRoutes {
		if r.Dst != nil && r.Dst.String() == v4Endpoint.String() {
			v4Found = true
		}
	}
	if !v4Found {
		fatalf("IPv4 endpoint route not found in table 51890")
	}
	epRoutes6, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: 51890}, netlink.RT_FILTER_TABLE)
	if err != nil {
		fatalf("list table 51890 v6 routes: %v", err)
	}
	v6Found := false
	for _, r := range epRoutes6 {
		if r.Dst != nil && r.Dst.String() == v6Endpoint.String() {
			v6Found = true
		}
	}
	if !v6Found {
		fatalf("IPv6 endpoint route not found in table 51890")
	}
	fmt.Println("PASS: endpoint routes in correct table")

	// --- Test 4: Endpoint rules exist for both families ---
	v4Rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		fatalf("list v4 rules: %v", err)
	}
	v4Rule := false
	for _, rule := range v4Rules {
		if rule.Table == 51890 && rule.Priority == 100 && rule.Dst != nil && rule.Dst.String() == v4Endpoint.String() {
			v4Rule = true
		}
	}
	if !v4Rule {
		fatalf("IPv4 endpoint rule not found (table=51890, priority=100)")
	}
	v6Rules, err := netlink.RuleList(netlink.FAMILY_V6)
	if err != nil {
		fatalf("list v6 rules: %v", err)
	}
	v6Rule := false
	for _, rule := range v6Rules {
		if rule.Table == 51890 && rule.Priority == 100 && rule.Dst != nil && rule.Dst.String() == v6Endpoint.String() {
			v6Rule = true
		}
	}
	if !v6Rule {
		fatalf("IPv6 endpoint rule not found (table=51890, priority=100)")
	}
	fmt.Println("PASS: endpoint rules exist for both families")

	// --- Test 5: Idempotent reconcile — zero new operations ---
	before, err := executor.Snapshot(ctx)
	if err != nil {
		fatalf("snapshot before idempotent reconcile: %v", err)
	}
	if err := executor.ReconcileEndpointPolicy(ctx, policy); err != nil {
		fatalf("idempotent reconcile: %v", err)
	}
	after, err := executor.Snapshot(ctx)
	if err != nil {
		fatalf("snapshot after idempotent reconcile: %v", err)
	}
	if len(after.Routes) != len(before.Routes) {
		fatalf("route count changed on idempotent reconcile: before=%d after=%d", len(before.Routes), len(after.Routes))
	}
	if len(after.Rules) != len(before.Rules) {
		fatalf("rule count changed on idempotent reconcile: before=%d after=%d", len(before.Rules), len(after.Rules))
	}
	fmt.Println("PASS: idempotent reconcile produces zero mutations")

	// --- Test 6: Parking both families ---
	mgr := parking.NewManager(executor)
	if err := mgr.PrepareWithdrawal(ctx, "dualstack-test", []netip.Prefix{v4Endpoint, v6Endpoint}); err != nil {
		fatalf("park both families: %v", err)
	}
	snap := mgr.Snapshot("dualstack-test")
	if !snap.Active || snap.Count != 2 {
		fatalf("parking state: active=%v count=%d", snap.Active, snap.Count)
	}
	fmt.Println("PASS: parking both families works")

	// --- Test 7: Parking refuses split-default prefixes ---
	splitPrefix := netip.MustParsePrefix("::/1")
	err = mgr.PrepareWithdrawal(ctx, "split-default-test", []netip.Prefix{splitPrefix})
	if err == nil {
		fatalf("parking accepted split-default prefix ::/1 — must refuse")
	}
	fmt.Println("PASS: parking refuses split-default prefix")

	// --- Test 8: Parking idempotent on replay ---
	if err := mgr.PrepareWithdrawal(ctx, "dualstack-test", []netip.Prefix{v4Endpoint, v6Endpoint}); err != nil {
		fatalf("park replay: %v", err)
	}
	snap2 := mgr.Snapshot("dualstack-test")
	if snap2.Count != snap.Count {
		fatalf("park count changed on replay: before=%d after=%d", snap.Count, snap2.Count)
	}
	if err := mgr.PrepareWithdrawal(ctx, "dualstack-test", []netip.Prefix{v4Endpoint, v6Endpoint}); err != nil {
		fatalf("park replay 2: %v", err)
	}
	afterPark, _ := executor.Snapshot(ctx)
	parkRouteCount := 0
	for _, r := range afterPark.Routes {
		if r.Kind == "unreachable" && r.Metric == 42760 && r.Table == 254 {
			parkRouteCount++
		}
	}
	if parkRouteCount != 2 {
		fatalf("park route count after replay: expected 2, got %d", parkRouteCount)
	}
	fmt.Println("PASS: parking idempotent on replay")

	// --- Test 9: Remove endpoint policy cleans up ---
	if err := executor.RemoveEndpointPolicy(ctx, policy); err != nil {
		fatalf("remove endpoint policy: %v", err)
	}
	cleanRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: 51890}, netlink.RT_FILTER_TABLE)
	if err != nil {
		fatalf("list table 51890 v4 routes after removal: %v", err)
	}
	for _, r := range cleanRoutes {
		if r.Dst != nil && r.Dst.String() == v4Endpoint.String() {
			fatalf("IPv4 endpoint route remained after removal")
		}
	}
	cleanRoutes6, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: 51890}, netlink.RT_FILTER_TABLE)
	if err != nil {
		fatalf("list table 51890 v6 routes after removal: %v", err)
	}
	for _, r := range cleanRoutes6 {
		if r.Dst != nil && r.Dst.String() == v6Endpoint.String() {
			fatalf("IPv6 endpoint route remained after removal")
		}
	}
	fmt.Println("PASS: remove endpoint policy cleans up")

	// --- Test 10: External VPN does not capture endpoint traffic ---
	if *extIfIndex > 0 {
		v4OnlyPolicy := endpoint.Policy{
			Role:     "v4-only-test",
			Zone:     "test",
			Priority: 200,
			Source:   endpoint.SourceNative,
			Routes: []endpoint.Route{
				{Prefix: v4Endpoint, IfIndex: *uplinkIfIndex, Metric: 1},
			},
		}
		if err := executor.ReconcileEndpointPolicy(ctx, v4OnlyPolicy); err != nil {
			fatalf("reconcile v4-only policy with external VPN: %v", err)
		}
		v4EpRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: 51890}, netlink.RT_FILTER_TABLE)
		if err != nil {
			fatalf("list table 51890 v4 routes: %v", err)
		}
		for _, r := range v4EpRoutes {
			if r.Dst != nil && r.Dst.String() == v4Endpoint.String() && r.LinkIndex == *extIfIndex {
				fatalf("endpoint route captured by external VPN ifindex=%d", *extIfIndex)
			}
		}
		if err := executor.RemoveEndpointPolicy(ctx, v4OnlyPolicy); err != nil {
			fatalf("remove v4-only policy: %v", err)
		}
		fmt.Println("PASS: external VPN does not capture endpoint traffic")
	}

	// --- Test 11: Fail-closed IPv6 unreachable timing ---
	// Verify that an unreachable route for IPv6 causes prompt failure
	// (not a multi-second TCP timeout). We measure wall time of a
	// connection attempt to a destination behind the unreachable route.
	start := time.Now()
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dialer := net.Dialer{}
	_, dialErr := dialer.DialContext(dialCtx, "tcp", "[2001:db8:100::10]:443")
	elapsed := time.Since(start)
	if dialErr == nil {
		fatalf("IPv6 connection succeeded behind unreachable route — fail-closed broken")
	}
	// Prompt failure must be well under the 3s timeout;
	// a blackhole would burn most of the timeout.
	if elapsed > 2*time.Second {
		fatalf("IPv6 failure took %v — not prompt, suggests blackhole not unreachable", elapsed)
	}
	fmt.Printf("PASS: fail-closed IPv6 unreachable fails in %v (prompt)\n", elapsed.Round(time.Millisecond))

	fmt.Println("ALL DUALSTACK NETNS TESTS PASSED")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
