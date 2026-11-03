//go:build linux

package netlink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"
	"syscall"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Executor is the single Linux writer for endpoint and parking transactions.
type Executor struct{}

const (
	endpointTable         = 51890
	endpointRouteProtocol = 4 // RTPROT_STATIC
)

var kernelMu sync.Mutex

func (Executor) Apply(_ context.Context, tx routing.Transaction) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	for _, op := range tx.Operations {
		if err := applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func (Executor) Snapshot(_ context.Context) (routing.KernelState, error) {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	return snapshotKernelUnlocked()
}

func snapshotKernelUnlocked() (routing.KernelState, error) {
	state := routing.KernelState{}
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		// RouteList(nil, family) only returns main-table routes; endpoint
		// routes live in table 51890.  Use RouteListFiltered with
		// RT_FILTER_TABLE + Table=RT_TABLE_UNSPEC to capture all tables.
		routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return routing.KernelState{}, err
		}
		for _, route := range routes {
			prefix := "0.0.0.0/0"
			if family == netlink.FAMILY_V6 {
				prefix = "::/0"
			}
			if route.Dst != nil {
				prefix = route.Dst.String()
			}
			kind := "route"
			if route.Type == rtnUnreachable {
				kind = "unreachable"
			}
			state.Routes = append(state.Routes, routing.Operation{
				Kind:     kind,
				Family:   family,
				Prefix:   prefix,
				Table:    route.Table,
				IfIndex:  route.LinkIndex,
				Metric:   uint32(route.Priority),
				Gateway:  ipString(route.Gw),
				Source:   ipString(route.Src),
				Protocol: int(route.Protocol),
			})
		}
		rules, err := netlink.RuleList(family)
		if err != nil {
			return routing.KernelState{}, err
		}
		for _, rule := range rules {
			prefix := ""
			if rule.Dst != nil {
				prefix = rule.Dst.String()
			}
			state.Rules = append(state.Rules, routing.Operation{
				Kind:     "rule",
				Family:   family,
				Prefix:   prefix,
				Table:    rule.Table,
				Priority: rule.Priority,
			})
		}
	}
	return state, nil
}

func (e Executor) ReconcileEndpointPolicy(_ context.Context, p endpoint.Policy) error {
	if !p.Valid() {
		return fmt.Errorf("invalid endpoint policy")
	}
	kernelMu.Lock()
	defer kernelMu.Unlock()

	current, err := snapshotKernelUnlocked()
	if err != nil {
		return err
	}
	ops, err := endpointPolicyPlan(current, p, false)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if err := applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func (e Executor) RemoveEndpointPolicy(_ context.Context, p endpoint.Policy) error {
	if !p.Valid() {
		return fmt.Errorf("invalid endpoint policy")
	}
	kernelMu.Lock()
	defer kernelMu.Unlock()

	current, err := snapshotKernelUnlocked()
	if err != nil {
		return err
	}
	ops, err := endpointPolicyPlan(current, p, true)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if err := applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func endpointPolicyPlan(current routing.KernelState, p endpoint.Policy, remove bool) ([]routing.Operation, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("invalid endpoint policy")
	}

	desired := make(map[string]routing.Operation, len(p.Routes))
	if !remove {
		for _, route := range p.Routes {
			if !routing.IsHostPrefix(route.Prefix) || route.IfIndex <= 0 {
				return nil, fmt.Errorf("invalid endpoint route")
			}
			metric := route.Metric
			if metric == 0 {
				metric = 1
			}
			op := routing.Operation{
				Kind:     "route",
				Family:   familyOf(route.Prefix),
				Prefix:   route.Prefix.String(),
				Table:    endpointTable,
				IfIndex:  route.IfIndex,
				Metric:   metric,
				Protocol: endpointRouteProtocol,
			}
			if route.Gateway.IsValid() {
				op.Gateway = route.Gateway.String()
			}
			desired[op.Prefix] = op
		}
	}

	ops := make([]routing.Operation, 0)
	if !remove {
		for _, sentinel := range []routing.Operation{
			{Kind: "unreachable", Family: netlink.FAMILY_V4, Prefix: "0.0.0.0/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
			{Kind: "unreachable", Family: netlink.FAMILY_V6, Prefix: "::/0", Table: endpointTable, Metric: 32767, Protocol: endpointRouteProtocol},
		} {
			if !containsRoute(current.Routes, sentinel) {
				ops = append(ops, sentinel)
			}
		}
	}

	prefixes := make([]string, 0, len(desired))
	for prefix := range desired {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)

	for _, prefix := range prefixes {
		route := desired[prefix]
		if !containsRoute(current.Routes, route) {
			ops = append(ops, route)
		}
		rule := routing.Operation{
			Kind:     "rule",
			Family:   route.Family,
			Prefix:   prefix,
			Table:    endpointTable,
			Priority: p.Priority,
		}
		if !containsRule(current.Rules, rule) {
			ops = append(ops, rule)
		}
	}

	for _, rule := range current.Rules {
		if rule.Table != endpointTable || rule.Priority != p.Priority {
			continue
		}
		prefix, err := netip.ParsePrefix(rule.Prefix)
		if err != nil || !routing.IsHostPrefix(prefix) {
			continue
		}
		if _, keep := desired[prefix.String()]; keep {
			continue
		}
		removeRule := rule
		removeRule.Kind = "delete-rule"
		ops = append(ops, removeRule)
	}

	finalRefs := make(map[string]bool)
	for _, rule := range current.Rules {
		if rule.Table != endpointTable {
			continue
		}
		prefix, err := netip.ParsePrefix(rule.Prefix)
		if err != nil || !routing.IsHostPrefix(prefix) {
			continue
		}
		if rule.Priority == p.Priority {
			continue
		}
		finalRefs[prefix.String()] = true
	}
	for prefix := range desired {
		finalRefs[prefix] = true
	}

	for _, route := range current.Routes {
		if route.Kind != "route" || route.Table != endpointTable {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil || !routing.IsHostPrefix(prefix) || finalRefs[prefix.String()] {
			continue
		}
		removeRoute := route
		removeRoute.Kind = "delete-route"
		ops = append(ops, removeRoute)
	}

	// The table-51890 unreachable sentinels are fail-closed placeholders for
	// the endpoint default: they must outlive the last policy only while some
	// owner still holds rules in the table. When the removal leaves no other
	// owner behind, keeping them would strand an orphan blackhole table, so
	// the removal plan deletes them too.
	if remove {
		otherOwner := false
		for _, rule := range current.Rules {
			if rule.Table == endpointTable && rule.Priority != p.Priority {
				otherOwner = true
				break
			}
		}
		if !otherOwner {
			for _, route := range current.Routes {
				if route.Kind != "unreachable" || route.Table != endpointTable || route.Protocol != endpointRouteProtocol {
					continue
				}
				removeSentinel := route
				removeSentinel.Kind = "delete-route"
				ops = append(ops, removeSentinel)
			}
		}
	}
	return ops, nil
}

func containsRoute(routes []routing.Operation, want routing.Operation) bool {
	for _, got := range routes {
		if got.Kind == want.Kind &&
			got.Family == want.Family &&
			got.Prefix == want.Prefix &&
			got.Table == want.Table &&
			got.IfIndex == want.IfIndex &&
			got.Metric == want.Metric &&
			got.Gateway == want.Gateway &&
			got.Protocol == want.Protocol {
			return true
		}
	}
	return false
}

func containsRule(rules []routing.Operation, want routing.Operation) bool {
	for _, got := range rules {
		if got.Kind == "rule" &&
			got.Family == want.Family &&
			got.Prefix == want.Prefix &&
			got.Table == want.Table &&
			got.Priority == want.Priority {
			return true
		}
	}
	return false
}

func (e Executor) ApplyParking(ctx context.Context, p parking.DesiredState) error {
	ops := make([]routing.Operation, 0, len(p.Prefixes))
	for _, prefix := range p.Prefixes {
		if !routing.IsHostPrefix(prefix) {
			continue
		}
		ops = append(ops, routing.Operation{
			Kind:     "park",
			Family:   familyOf(prefix),
			Prefix:   prefix.String(),
			Table:    254,
			Metric:   42760,
			Protocol: endpointRouteProtocol,
		})
	}
	return e.Apply(ctx, routing.Transaction{Role: p.Role, Operations: ops})
}

func applyOperation(op routing.Operation) error {
	if op.Kind == "rule" || op.Kind == "delete-rule" {
		rule, err := ruleForOperation(op)
		if err != nil {
			return err
		}
		if op.Kind == "delete-rule" {
			return netlink.RuleDel(rule)
		}
		if err := netlink.RuleDel(rule); err != nil && !os.IsNotExist(err) {
			return err
		}
		return netlink.RuleAdd(rule)
	}

	route, err := routeForOperation(op)
	if err != nil {
		return err
	}
	if op.Kind == "delete-route" || op.Kind == "delete-park" {
		return netlink.RouteDel(&route)
	}
	if op.Kind == "park" {
		if err := netlink.RouteAdd(&route); err != nil && !errors.Is(err, syscall.EEXIST) {
			return err
		}
		return nil
	}
	return netlink.RouteReplace(&route)
}

func routeForOperation(op routing.Operation) (netlink.Route, error) {
	route := netlink.Route{Table: op.Table, LinkIndex: op.IfIndex, Priority: int(op.Metric)}
	if op.Protocol != 0 {
		route.Protocol = netlink.RouteProtocol(op.Protocol)
	}
	if op.Prefix != "" {
		_, dst, err := net.ParseCIDR(op.Prefix)
		if err != nil {
			return netlink.Route{}, err
		}
		route.Dst = dst
	}
	if op.Gateway != "" {
		gateway := net.ParseIP(op.Gateway)
		if gateway == nil {
			return netlink.Route{}, fmt.Errorf("invalid route gateway %q", op.Gateway)
		}
		route.Gw = gateway
	}
	if op.Source != "" {
		source := net.ParseIP(op.Source)
		if source == nil {
			return netlink.Route{}, fmt.Errorf("invalid route source %q", op.Source)
		}
		route.Src = source
	}
	if op.Kind == "unreachable" || op.Kind == "park" {
		route.Type = rtnUnreachable
		route.LinkIndex = 0
	}
	return route, nil
}

func ruleForOperation(op routing.Operation) (*netlink.Rule, error) {
	family := op.Family
	if family == 0 {
		family = netlink.FAMILY_V4
		if op.Prefix != "" {
			prefix, err := netip.ParsePrefix(op.Prefix)
			if err != nil {
				return nil, err
			}
			if prefix.Addr().Is6() {
				family = netlink.FAMILY_V6
			}
		}
	}
	rule := netlink.NewRule()
	rule.Priority, rule.Table, rule.Family = op.Priority, op.Table, family
	if op.Prefix != "" {
		_, dst, err := net.ParseCIDR(op.Prefix)
		if err != nil {
			return nil, err
		}
		rule.Dst = dst
	}
	return rule, nil
}

const rtnUnreachable = 7 // linux RTN_UNREACHABLE

// ---------------------------------------------------------------------------
// Selected routes (08D Â§6): the single-writer path for Leshy-selected
// destinations. Ownership comes from the explicit registry, never from
// kernel discovery, and a foreign row on the same prefix is never replaced
// or deleted. The generic applyOperation above is deliberately not reused:
// its normal write is RouteReplace, which can clobber a foreign row.
// ---------------------------------------------------------------------------

func familyOf(prefix netip.Prefix) int {
	if prefix.Addr().Is6() {
		return netlink.FAMILY_V6
	}
	return netlink.FAMILY_V4
}

// selectedRouteTable is the main table: where Leshy-selected routes live.
const selectedRouteTable = unix.RT_TABLE_MAIN

// selectedRouteRows returns the current same-family main-table rows for one
// exact canonical prefix.
func selectedRouteRows(family int, prefix netip.Prefix) ([]netlink.Route, error) {
	_, dst, err := net.ParseCIDR(prefix.String())
	if err != nil {
		return nil, err
	}
	return netlink.RouteListFiltered(family, &netlink.Route{Dst: dst, Table: selectedRouteTable},
		netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE)
}

// selectedKernelRow projects a kernel row onto the shared neutral row type
// the platform planner works with.
func selectedKernelRow(row netlink.Route) routing.SelectedKernelRow {
	return routing.SelectedKernelRow{
		IfIndex:  row.LinkIndex,
		Gateway:  netipAddr(row.Gw),
		Protocol: int(row.Protocol),
		Metric:   uint32(row.Priority),
	}
}

// selectedRouteMatches reports whether a kernel row is exactly the desired
// route. The rules are the shared platform planner's.
func selectedRouteMatches(row netlink.Route, desired routing.SelectedRouteDesired) bool {
	return routing.SelectedRowMatchesDesired(selectedKernelRow(row), desired)
}

// selectedRoutePlan decides the mutation for one prefix. The ownership and
// identity rules are the shared platform planner's, so both platforms
// enforce one contract.
func selectedRoutePlan(existing []netlink.Route, desired routing.SelectedRouteDesired, owned *routing.SelectedRoute) (routing.SelectedReconcileAction, error) {
	rows := make([]routing.SelectedKernelRow, 0, len(existing))
	for _, row := range existing {
		rows = append(rows, selectedKernelRow(row))
	}
	return routing.PlanSelectedRoute(rows, desired, owned)
}

// selectedRouteMetric normalizes the desired metric: the IPv6 kernel
// assigns its own default (1024) when a route is added without one, so a
// zero metric on an IPv6 route is made explicit and the read-back compares
// deterministically.
func selectedRouteMetric(desired routing.SelectedRouteDesired) int {
	if desired.Metric == 0 && !desired.Prefix.Addr().Is4() {
		return 1024
	}
	return int(desired.Metric)
}

// selectedRouteFor builds the netlink add/replace request for a desired
// selected route. Scope follows the target: link for device routes,
// universe for gateway routes.
func selectedRouteFor(desired routing.SelectedRouteDesired) netlink.Route {
	mask := net.CIDRMask(desired.Prefix.Bits(), desired.Prefix.Addr().BitLen())
	route := netlink.Route{
		Dst:       &net.IPNet{IP: desired.Prefix.Addr().AsSlice(), Mask: mask},
		Table:     selectedRouteTable,
		LinkIndex: desired.IfIndex,
		Priority:  selectedRouteMetric(desired),
		Protocol:  netlink.RouteProtocol(desired.Protocol),
	}
	if desired.Gateway.IsValid() {
		route.Gw = desired.Gateway.AsSlice()
		route.Scope = netlink.Scope(unix.RT_SCOPE_UNIVERSE)
	} else {
		route.Scope = netlink.Scope(unix.RT_SCOPE_LINK)
	}
	return route
}

func netipAddr(ip net.IP) netip.Addr {
	if ip == nil {
		return netip.Addr{}
	}
	if addr, ok := netip.AddrFromSlice(ip); ok {
		return addr.Unmap()
	}
	return netip.Addr{}
}

func netipAddrsEqual(a, b netip.Addr) bool {
	return a.Unmap() == b.Unmap()
}

// ReconcileSelectedRoute makes the kernel row for one registered destination
// match the desired route exactly. owned is the registry record for the
// destination; nil means the destination was never registered, which makes
// every same-prefix row foreign.
func (Executor) ReconcileSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired, owned *routing.SelectedRoute) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()

	if !desired.Prefix.IsValid() || desired.Prefix != desired.Prefix.Masked() {
		return fmt.Errorf("selected route prefix %v is not canonical", desired.Prefix)
	}
	if desired.IfIndex <= 0 {
		return fmt.Errorf("selected route %v has no interface", desired.Prefix)
	}
	family := netlink.FAMILY_V4
	if !desired.Prefix.Addr().Is4() {
		family = netlink.FAMILY_V6
	}

	existing, err := selectedRouteRows(family, desired.Prefix)
	if err != nil {
		return err
	}
	action, err := selectedRoutePlan(existing, desired, owned)
	if err != nil {
		return err
	}
	switch action {
	case routing.SelectedReconcileNone:
		return nil
	case routing.SelectedReconcileAdd:
		route := selectedRouteFor(desired)
		if err := netlink.RouteAdd(&route); err != nil && !errors.Is(err, syscall.EEXIST) {
			return err
		}
	case routing.SelectedReconcileReplace:
		route := selectedRouteFor(desired)
		if err := netlink.RouteReplace(&route); err != nil {
			return err
		}
	}

	// Read back: only an exact row proves success.
	rows, err := selectedRouteRows(family, desired.Prefix)
	if err != nil {
		return err
	}
	normalized := desired
	normalized.Metric = uint32(selectedRouteMetric(desired))
	for _, row := range rows {
		if selectedRouteMatches(row, normalized) {
			return nil
		}
	}
	return fmt.Errorf("selected route %v is not exactly present after the apply (%d rows)", desired.Prefix, len(rows))
}

// RemoveSelectedRoute deletes one registered destination's row, and only
// when the kernel row still carries the recorded owned identity. A foreign
// row on the same prefix is left alone; an absent row is success.
func (Executor) RemoveSelectedRoute(_ context.Context, owned routing.SelectedRoute) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()

	if !owned.Prefix.IsValid() || owned.Prefix != owned.Prefix.Masked() {
		return fmt.Errorf("selected route prefix %v is not canonical", owned.Prefix)
	}
	family := netlink.FAMILY_V4
	if !owned.Prefix.Addr().Is4() {
		family = netlink.FAMILY_V6
	}
	rows, err := selectedRouteRows(family, owned.Prefix)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !routing.SelectedRowMatchesOwnedIdentity(selectedKernelRow(row), &owned) {
			continue
		}
		if err := netlink.RouteDel(&row); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return nil
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
