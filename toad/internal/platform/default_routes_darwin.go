//go:build darwin

package platform

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// macOS has no Linux-style policy-routing tables. This adapter owns only
// exact host/network routes that it created and uses the BSD route database;
// it never changes the default route or flushes unrelated routes.
type darwinRouteManager struct {
	mu       sync.Mutex
	endpoint map[int][]endpoint.Route
}

func DefaultRouteManager() (RouteManager, routing.Executor) {
	manager := &darwinRouteManager{endpoint: make(map[int][]endpoint.Route)}
	return manager, manager
}

func (m *darwinRouteManager) ReconcileEndpointPolicy(ctx context.Context, policy endpoint.Policy) error {
	if !policy.Valid() || len(policy.Routes) == 0 {
		return fmt.Errorf("macOS endpoint policy is invalid or has no routes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := make(map[netip.Prefix]endpoint.Route, len(policy.Routes))
	for _, route := range policy.Routes {
		if !routing.IsHostPrefix(route.Prefix) || route.IfIndex <= 0 {
			return fmt.Errorf("macOS endpoint route is invalid")
		}
		iface, err := net.InterfaceByIndex(route.IfIndex)
		if err != nil {
			return fmt.Errorf("lookup macOS route interface %d: %w", route.IfIndex, err)
		}
		if err := m.replaceRoute(ctx, route.Prefix, route.Gateway, iface.Name); err != nil {
			return err
		}
		desired[route.Prefix] = route
	}
	for _, previous := range m.endpoint[policy.Priority] {
		if _, keep := desired[previous.Prefix]; keep {
			continue
		}
		if err := m.deleteRoute(ctx, previous.Prefix); err != nil {
			return err
		}
	}
	next := make([]endpoint.Route, 0, len(desired))
	for _, route := range desired {
		next = append(next, route)
	}
	m.endpoint[policy.Priority] = next
	return nil
}

func (m *darwinRouteManager) RemoveEndpointPolicy(ctx context.Context, policy endpoint.Policy) error {
	if !policy.Valid() {
		return fmt.Errorf("invalid macOS endpoint policy")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, route := range m.endpoint[policy.Priority] {
		if err := m.deleteRoute(ctx, route.Prefix); err != nil {
			return err
		}
	}
	delete(m.endpoint, policy.Priority)
	return nil
}

func (m *darwinRouteManager) ApplyParking(ctx context.Context, desired parking.DesiredState) error {
	ops := make([]routing.Operation, 0, len(desired.Prefixes))
	for _, prefix := range desired.Prefixes {
		if !routing.IsHostPrefix(prefix) {
			continue
		}
		ops = append(ops, routing.Operation{Kind: "park", Family: prefix.Addr().BitLen(), Prefix: prefix.String(), Metric: 42760, Protocol: 4})
	}
	return m.Apply(ctx, routing.Transaction{Role: desired.Role, Operations: ops})
}

func (m *darwinRouteManager) Apply(ctx context.Context, transaction routing.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, operation := range transaction.Operations {
		prefix, err := netip.ParsePrefix(operation.Prefix)
		if err != nil {
			return fmt.Errorf("parse macOS route prefix %q: %w", operation.Prefix, err)
		}
		target := routeTarget(prefix)
		var args []string
		switch operation.Kind {
		case "park":
			args = []string{"-n", "add", "-blackhole", target}
		case "delete-park":
			args = []string{"-n", "delete", target}
		case "delete":
			args = []string{"-n", "delete", target}
		default:
			continue
		}
		if err := runRoute(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (m *darwinRouteManager) deleteRoute(ctx context.Context, prefix netip.Prefix) error {
	args := []string{"-n"}
	if prefix.Addr().Is6() {
		args = append(args, "-inet6")
	}
	args = append(args, "delete", routeTarget(prefix))
	return runRoute(ctx, args...)
}

func (m *darwinRouteManager) replaceRoute(ctx context.Context, prefix netip.Prefix, gateway netip.Addr, iface string) error {
	target := routeTarget(prefix)
	via := "-interface"
	next := iface
	if gateway.IsValid() {
		via, next = gateway.String(), ""
	}
	args := []string{"-n"}
	if prefix.Addr().Is6() {
		args = append(args, "-inet6")
	}
	args = append(args, "change", target)
	if next != "" {
		args = append(args, via, next)
	} else {
		args = append(args, via)
	}
	if err := runRoute(ctx, args...); err == nil {
		return nil
	}
	for i, arg := range args {
		if arg == "change" {
			args[i] = "add"
			break
		}
	}
	return runRoute(ctx, args...)
}

func routeTarget(prefix netip.Prefix) string {
	if prefix.Bits() == prefix.Addr().BitLen() {
		return "-host " + prefix.Addr().String()
	}
	return "-net " + prefix.String()
}

func runRoute(ctx context.Context, args ...string) error {
	// routeTarget is expanded into two argv entries; do not invoke a shell.
	flat := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-host ") {
			flat = append(flat, "-host", strings.TrimPrefix(arg, "-host "))
		} else if strings.HasPrefix(arg, "-net ") {
			flat = append(flat, "-net", strings.TrimPrefix(arg, "-net "))
		} else {
			flat = append(flat, arg)
		}
	}
	if output, err := exec.CommandContext(ctx, "route", flat...).CombinedOutput(); err != nil {
		return fmt.Errorf("macOS route %s: %w (%s)", strings.Join(flat, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *darwinRouteManager) Snapshot(ctx context.Context) (routing.KernelState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := routing.KernelState{}
	for _, family := range []int{4, 6} {
		args := []string{"-rn", "-f", "inet"}
		if family == 6 {
			args[2] = "inet6"
		}
		out, err := exec.CommandContext(ctx, "netstat", args...).CombinedOutput()
		if err != nil {
			return routing.KernelState{}, fmt.Errorf("read macOS routes: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		result.Routes = append(result.Routes, parseNetstatRoutes(string(out), family)...)
	}
	return result, nil
}

func parseNetstatRoutes(text string, family int) []routing.Operation {
	var result []routing.Operation
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "Destination" || fields[0] == "Routing" {
			continue
		}
		prefix, ok := netstatPrefix(fields[0], family)
		if !ok {
			continue
		}
		gateway := ""
		if fields[1] != "link#" && !strings.HasPrefix(fields[1], "link#") {
			gateway = fields[1]
		}
		iface, err := net.InterfaceByName(fields[3])
		if err != nil {
			continue
		}
		result = append(result, routing.Operation{Kind: "route", Family: family, Prefix: prefix, Gateway: gateway, IfIndex: iface.Index, Protocol: 4})
	}
	return result
}

func netstatPrefix(raw string, family int) (string, bool) {
	if raw == "default" {
		if family == 4 {
			return "0.0.0.0/0", true
		}
		return "::/0", true
	}
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.String(), true
	}
	if addr, err := netip.ParseAddr(strings.TrimSuffix(raw, "%")); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()).String(), true
	}
	if family == 4 && strings.Count(raw, ".") == 2 {
		return raw + ".0/24", true
	}
	return "", false
}
