//go:build linux

package netlink

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/vishvananda/netlink"
)

// Executor is the single Linux writer for endpoint and parking transactions.
type Executor struct{}

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
			state.Routes = append(state.Routes, routing.Operation{Kind: kind, Family: family, Prefix: prefix, Table: route.Table, IfIndex: route.LinkIndex, Metric: uint32(route.Priority), Protocol: int(route.Protocol)})
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
			state.Rules = append(state.Rules, routing.Operation{Kind: "rule", Family: family, Prefix: prefix, Table: rule.Table, Priority: rule.Priority})
		}
	}
	return state, nil
}
func (e Executor) ApplyEndpointPolicy(ctx context.Context, p endpoint.Policy) error {
	if !p.Valid() {
		return fmt.Errorf("invalid endpoint policy")
	}
	ops := []routing.Operation{
		{Kind: "unreachable", Family: netlink.FAMILY_V4, Prefix: "0.0.0.0/0", Table: 51890, Metric: 32767},
		{Kind: "unreachable", Family: netlink.FAMILY_V6, Prefix: "::/0", Table: 51890, Metric: 32767},
	}
	for _, route := range p.Routes {
		if !route.Prefix.IsValid() || route.IfIndex <= 0 {
			return fmt.Errorf("invalid endpoint route")
		}
		metric := route.Metric
		if metric == 0 {
			metric = 1
		}
		ops = append(ops,
			routing.Operation{Kind: "route", Family: familyOf(route.Prefix), Prefix: route.Prefix.String(), Table: 51890, IfIndex: route.IfIndex, Metric: metric, Gateway: route.Gateway.String()},
			routing.Operation{Kind: "rule", Family: familyOf(route.Prefix), Prefix: route.Prefix.String(), Priority: p.Priority, Table: 51890},
		)
	}
	return e.Apply(ctx, routing.Transaction{Role: p.Role, Operations: ops})
}
func (e Executor) ApplyParking(ctx context.Context, p parking.DesiredState) error {
	return e.Apply(ctx, routing.Transaction{Role: p.Role, Operations: []routing.Operation{{Kind: "park", Metric: 42760}}})
}
func applyOperation(op routing.Operation) error {
	family := netlink.FAMILY_V4
	if op.Prefix != "" {
		p, err := netip.ParsePrefix(op.Prefix)
		if err != nil {
			return err
		}
		if p.Addr().Is6() {
			family = netlink.FAMILY_V6
		}
	}
	if op.Kind == "rule" || op.Kind == "delete-rule" {
		rule := netlink.NewRule()
		rule.Priority, rule.Table, rule.Family = op.Priority, op.Table, family
		if op.Prefix != "" {
			_, dst, err := net.ParseCIDR(op.Prefix)
			if err != nil {
				return err
			}
			rule.Dst = dst
		}
		if op.Kind == "delete-rule" {
			return netlink.RuleDel(rule)
		}
		if err := netlink.RuleDel(rule); err != nil && !os.IsNotExist(err) {
			return err
		}
		return netlink.RuleAdd(rule)
	}
	route := netlink.Route{Table: op.Table, LinkIndex: op.IfIndex, Priority: int(op.Metric)}
	if op.Prefix != "" {
		_, dst, err := net.ParseCIDR(op.Prefix)
		if err != nil {
			return err
		}
		route.Dst = dst
	}
	if op.Gateway != "" {
		route.Gw = net.ParseIP(op.Gateway)
	}
	if op.Kind == "unreachable" || op.Kind == "park" {
		route.Type = rtnUnreachable
		route.LinkIndex = 0
	}
	if op.Kind == "delete-route" || op.Kind == "delete-park" {
		return netlink.RouteDel(&route)
	}
	return netlink.RouteReplace(&route)
}

const rtnUnreachable = 7 // linux RTN_UNREACHABLE

func familyOf(prefix netip.Prefix) int {
	if prefix.Addr().Is6() {
		return netlink.FAMILY_V6
	}
	return netlink.FAMILY_V4
}
