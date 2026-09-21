//go:build linux

package netlink

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/vishvananda/netlink"
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
		routes, err := netlink.RouteList(nil, family)
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

func familyOf(prefix netip.Prefix) int {
	if prefix.Addr().Is6() {
		return netlink.FAMILY_V6
	}
	return netlink.FAMILY_V4
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
