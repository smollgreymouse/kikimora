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

// selectedRouteProtocolLinux is the platform-native managed selected/park
// protocol: RTPROT_STATIC (08D1 §31.26). It is a Linux contract, never a
// cross-platform constant.
const selectedRouteProtocolLinux = int(unix.RTPROT_STATIC)

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

// selectedKernelRow projects one kernel row onto the shared exact row model
// (08D1 §3, §8): an RTN_UNREACHABLE row with the deterministic managed-park
// identity projects to SelectedKernelPark; a normal forwarding row that is
// fully representable projects to SelectedKernelRoute; anything else becomes
// SelectedKernelForeign and is never dropped. Ownership is decided later by
// exact comparison against the persisted transaction state, never here.
func selectedKernelRow(row netlink.Route, prefix netip.Prefix) routing.SelectedKernelRow {
	description := fmt.Sprintf("ifindex %d gw %v proto %d metric %d scope %d type %d",
		row.LinkIndex, row.Gw, row.Protocol, row.Priority, row.Scope, row.Type)
	if row.Type == unix.RTN_UNREACHABLE {
		if int(row.Protocol) == selectedRouteProtocolLinux &&
			uint32(row.Priority) == routing.ManagedParkMetric &&
			row.LinkIndex == 0 && row.Gw == nil {
			return routing.SelectedKernelRow{
				Kind: routing.SelectedKernelPark,
				Park: routing.SelectedParkIdentity{
					Kind:     routing.SelectedParkUnreachable,
					Table:    selectedRouteTable,
					Protocol: int(row.Protocol),
					Metric:   uint32(row.Priority),
					Scope:    int(row.Scope),
				},
				Description: description,
			}
		}
		// An unreachable/prohibit/blackhole row with the wrong managed-park
		// identity is foreign.
		return routing.SelectedKernelRow{Kind: routing.SelectedKernelForeign, Description: description}
	}
	identity := routing.SelectedRouteIdentity{
		Table:    selectedRouteTable,
		IfIndex:  row.LinkIndex,
		NextHop:  netipAddr(row.Gw).String(),
		Protocol: int(row.Protocol),
		Metric:   uint32(row.Priority),
		Scope:    int(row.Scope),
	}
	if netipAddr(row.Gw).IsValid() {
		identity.NextHop = netipAddr(row.Gw).String()
	} else {
		identity.NextHop = ""
	}
	if identity.Validate(prefix) != nil {
		// A normal row that cannot be fully represented by a valid identity
		// is foreign, never silently filtered away.
		return routing.SelectedKernelRow{Kind: routing.SelectedKernelForeign, Description: description}
	}
	return routing.SelectedKernelRow{Kind: routing.SelectedKernelRoute, Route: identity, Description: description}
}

// selectedRoutePlan decides the mutation for one prefix. The ownership and
// identity rules are the shared platform planner's, so both platforms
// enforce one contract.
func selectedRoutePlan(existing []netlink.Route, prefix netip.Prefix, record *routing.SelectedRoute) (routing.SelectedReconcileAction, error) {
	rows := make([]routing.SelectedKernelRow, 0, len(existing))
	for _, row := range existing {
		rows = append(rows, selectedKernelRow(row, prefix))
	}
	return routing.PlanSelectedRoute(rows, record)
}

// selectedRouteMetric normalizes the desired metric: the IPv6 kernel
// assigns its own default (1024) when a route is added without one, so a
// zero metric on an IPv6 route is made explicit and the read-back compares
// deterministically.
func selectedRouteMetric(prefix netip.Prefix) int {
	if !prefix.Addr().Is4() {
		// The IPv6 kernel assigns its own default (1024) when a route is
		// added without one; make it explicit so read-back is deterministic.
		return 1024
	}
	return 0
}

// selectedRouteFor builds the netlink add/replace request from the exact
// normalized identity. Scope follows the target: link for device routes,
// universe for gateway routes.
func selectedRouteFor(prefix netip.Prefix, identity routing.SelectedRouteIdentity) netlink.Route {
	mask := net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())
	route := netlink.Route{
		Dst:       &net.IPNet{IP: prefix.Addr().AsSlice(), Mask: mask},
		Table:     identity.Table,
		LinkIndex: identity.IfIndex,
		Priority:  int(identity.Metric),
		Protocol:  netlink.RouteProtocol(identity.Protocol),
	}
	if identity.NextHop != "" {
		if gw, err := netip.ParseAddr(identity.NextHop); err == nil {
			route.Gw = gw.AsSlice()
		}
		route.Scope = netlink.Scope(unix.RT_SCOPE_UNIVERSE)
	} else {
		route.Scope = netlink.Scope(unix.RT_SCOPE_LINK)
	}
	return route
}

// NormalizeSelectedRoute computes the exact Linux identity for the desired
// route without any mutation (08D1 §4.1): table 254, the desired ifindex,
// the platform-native RTPROT_STATIC protocol, the IPv6 metric
// normalization, and link/universe scope by target.
func (Executor) NormalizeSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error) {
	if !desired.Prefix.IsValid() || desired.Prefix != desired.Prefix.Masked() {
		return routing.SelectedRouteIdentity{}, fmt.Errorf("selected route prefix %v is not canonical", desired.Prefix)
	}
	if desired.IfIndex <= 0 {
		return routing.SelectedRouteIdentity{}, fmt.Errorf("selected route %v has no interface", desired.Prefix)
	}
	identity := routing.SelectedRouteIdentity{
		Table:    selectedRouteTable,
		IfIndex:  desired.IfIndex,
		Protocol: selectedRouteProtocolLinux,
		Metric:   uint32(selectedRouteMetric(desired.Prefix)),
		Scope:    routing.ScopeLink,
	}
	if desired.Gateway.IsValid() {
		identity.NextHop = desired.Gateway.String()
		identity.Scope = routing.ScopeUniverse
	}
	if err := identity.Validate(desired.Prefix); err != nil {
		return routing.SelectedRouteIdentity{}, err
	}
	return identity, nil
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
// match the desired route exactly (08D1 §8). record is the registry record
// for the destination; nil means the destination was never registered, which
// makes every same-prefix row foreign. EEXIST/EBUSY is re-read and
// classified, never unconditional success.
func (Executor) ReconcileSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired, record *routing.SelectedRoute) error {
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
	action, err := selectedRoutePlan(existing, desired.Prefix, record)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("selected route %v is not registered", desired.Prefix)
	}
	switch action {
	case routing.SelectedActionNone:
		return nil
	case routing.SelectedActionAdd:
		route := selectedRouteFor(desired.Prefix, record.Desired)
		if err := netlink.RouteAdd(&route); err != nil && !errors.Is(err, syscall.EEXIST) && !errors.Is(err, syscall.EBUSY) {
			return err
		}
	case routing.SelectedActionReplace:
		// RouteReplace runs only after the exact previous ownership proof in
		// the plan; create/verify the desired row before stale cleanup.
		route := selectedRouteFor(desired.Prefix, record.Desired)
		if err := netlink.RouteReplace(&route); err != nil {
			return err
		}
	}

	// Read back: only one exact Desired row with no stale Owned/foreign row
	// proves success.
	rows, err := selectedRouteRows(family, desired.Prefix)
	if err != nil {
		return err
	}
	current := *record
	if action == routing.SelectedActionReplace || action == routing.SelectedActionAdd {
		// The previous owned row may still exist when the platform cleans
		// stale rows after create; the planner called with the same record
		// proves the final state.
		_ = current
	}
	verified, err := selectedRoutePlan(rows, desired.Prefix, record)
	if err != nil {
		return fmt.Errorf("selected route %v is not exactly present after the apply: %w", desired.Prefix, err)
	}
	if verified != routing.SelectedActionNone {
		return fmt.Errorf("selected route %v still needs %d after the apply", desired.Prefix, verified)
	}
	return nil
}

// RemoveSelectedRoute is state-aware and exact (08D1 §8): it deletes the
// previous Owned when present; Applying additionally authorizes the exact
// Desired as an in-flight candidate; Pending never deletes Desired; Retiring
// deletes Owned plus Desired only when RetireDesired=true. Every candidate
// is re-read and identity-checked before deletion; foreign rows remain
// untouched; an already-absent authorized identity is success.
func (Executor) RemoveSelectedRoute(_ context.Context, record routing.SelectedRoute) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()

	if !record.Prefix.IsValid() || record.Prefix != record.Prefix.Masked() {
		return fmt.Errorf("selected route prefix %v is not canonical", record.Prefix)
	}
	family := netlink.FAMILY_V4
	if !record.Prefix.Addr().Is4() {
		family = netlink.FAMILY_V6
	}
	rows, err := selectedRouteRows(family, record.Prefix)
	if err != nil {
		return err
	}

	candidates := make([]routing.SelectedRouteIdentity, 0, 2)
	if record.Owned != nil {
		candidates = append(candidates, *record.Owned)
	}
	switch record.State {
	case routing.SelectedApplying:
		candidates = append(candidates, record.Desired)
	case routing.SelectedRetiring:
		if record.RetireDesired {
			candidates = append(candidates, record.Desired)
		}
	case routing.SelectedPending:
		// Pending never deletes the Desired: the mutation was never
		// authorized.
	case routing.SelectedActive:
	default:
		return fmt.Errorf("selected route %v has an unknown state %q", record.Prefix, record.State)
	}

	deleted := make(map[routing.SelectedRouteIdentity]bool, len(candidates))
	for _, candidate := range candidates {
		if deleted[candidate] {
			continue
		}
		deleted[candidate] = true
		for _, row := range rows {
			projected := selectedKernelRow(row, record.Prefix)
			if !routing.SelectedRowMatchesIdentity(projected, candidate) {
				continue
			}
			if err := netlink.RouteDel(&row); err != nil && !os.IsNotExist(err) {
				return err
			}
			break
		}
	}
	return nil
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
