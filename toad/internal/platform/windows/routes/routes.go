//go:build windows

// Package routes is the single Windows writer for endpoint-exception and
// parking route transactions. Windows has no policy tables or ip rules: the
// endpoint exceptions are host-prefix routes that win by longest-prefix match,
// and fail-closed parking points selected traffic at the loopback interface.
// Every route this executor creates is tagged with ownedRouteProtocol so
// cleanup can never remove unrelated administrator routes.
package routes

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const (
	// ownedRouteProtocol tags every route this executor creates. It matches
	// parking's RTPROT_STATIC constant on purpose: parked and selected routes
	// share one ownership tag.
	ownedRouteProtocol = 4

	// loopbackIfIndex is the standard Windows loopback interface index; park
	// routes send selected traffic there while the transport is unavailable.
	loopbackIfIndex = 1

	endpointRouteMetric = 1
	parkRouteMetric     = 42760

	// kernelTable mirrors the Linux "main" table number for snapshot rows.
	kernelTable = 254
)

// Executor serializes endpoint and parking mutations inside the process, the
// same contract the Linux netlink executor provides.
type Executor struct {
	mu sync.Mutex
}

// NewExecutor returns one shared executor usable as both RouteManager and
// routing.Executor.
func NewExecutor() *Executor {
	return &Executor{}
}

// platform seams for deterministic tests.
var (
	routeTable  = winipcfg.GetIPForwardTable2
	createRoute = func(row *winipcfg.MibIPforwardRow2) error { return row.Create() }
	deleteRoute = func(row *winipcfg.MibIPforwardRow2) error { return row.Delete() }
	unicastAddr = func(family winipcfg.AddressFamily, ifIndex uint32) (netip.Addr, error) {
		addresses, err := winipcfg.GetUnicastIPAddressTable(family)
		if err != nil {
			return netip.Addr{}, err
		}
		for i := range addresses {
			row := &addresses[i]
			if row.InterfaceIndex != ifIndex || row.SkipAsSource || row.DadState != winipcfg.DadStatePreferred {
				continue
			}
			if addr := row.Address.Addr(); addr.IsValid() {
				return addr, nil
			}
		}
		return netip.Addr{}, fmt.Errorf("interface %d has no usable source address", ifIndex)
	}
)

// Apply runs one transaction. Operations are applied in order; a failed
// operation aborts the rest exactly like the Linux executor.
func (e *Executor) Apply(_ context.Context, tx routing.Transaction) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, op := range tx.Operations {
		if err := e.applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) applyOperation(op routing.Operation) error {
	switch op.Kind {
	case "rule", "delete-rule":
		// Windows has no policy rules; planners must not emit them here.
		return fmt.Errorf("policy rules do not exist on windows: %s %s", op.Kind, op.Prefix)
	case "delete-route", "delete-park":
		row, err := rowForOperation(op)
		if err != nil {
			return err
		}
		if err := deleteRoute(&row); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return fmt.Errorf("delete %s on windows: %w", op.Prefix, err)
		}
		return nil
	case "park":
		row, err := rowForOperation(op)
		if err != nil {
			return err
		}
		if err := createRoute(&row); err != nil {
			if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
				return nil
			}
			return fmt.Errorf("park %s on windows: %w", op.Prefix, err)
		}
		return nil
	default: // route, unreachable
		row, err := rowForOperation(op)
		if err != nil {
			return err
		}
		if err := createRoute(&row); err != nil {
			if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
				// Replace semantics: drop the stale row and recreate it with
				// the desired parameters so repeated recovery never drifts.
				if derr := deleteRoute(&row); derr == nil || errors.Is(derr, windows.ERROR_NOT_FOUND) {
					if cerr := createRoute(&row); cerr != nil && !errors.Is(cerr, windows.ERROR_OBJECT_ALREADY_EXISTS) {
						return fmt.Errorf("replace %s on windows: %w", op.Prefix, cerr)
					}
				}
				return nil
			}
			return fmt.Errorf("apply %s on windows: %w", op.Prefix, err)
		}
		return nil
	}
}

// ReconcileEndpointPolicy installs the endpoint host routes on the physical
// underlay interface. Windows needs no sentinel unreachable defaults and no
// policy rules: a /32 (or /128) host route always wins the longest-prefix
// match over any default route. Routes already present with identical
// parameters are left untouched, so repeated recovery is a no-op.
func (e *Executor) ReconcileEndpointPolicy(_ context.Context, p endpoint.Policy) error {
	if !p.Valid() {
		return fmt.Errorf("invalid endpoint policy")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	current, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	for _, route := range p.Routes {
		if !routing.IsHostPrefix(route.Prefix) || route.IfIndex <= 0 {
			return fmt.Errorf("invalid endpoint route")
		}
		metric := route.Metric
		if metric == 0 {
			metric = endpointRouteMetric
		}
		op := routing.Operation{
			Kind:     "route",
			Prefix:   route.Prefix.String(),
			Table:    kernelTable,
			IfIndex:  route.IfIndex,
			Metric:   metric,
			Protocol: ownedRouteProtocol,
		}
		if route.Prefix.Addr().Is6() {
			op.Family = 6
		} else {
			op.Family = 4
		}
		if route.Gateway.IsValid() {
			op.Gateway = route.Gateway.String()
		}
		if !containsRoute(current.Routes, op) {
			if err := e.applyOperation(op); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveEndpointPolicy deletes exactly the routes this package owns for the
// policy's prefixes. Unrelated administrator routes — including owned routes
// for other prefixes — are never touched.
func (e *Executor) RemoveEndpointPolicy(_ context.Context, p endpoint.Policy) error {
	if !p.Valid() {
		return fmt.Errorf("invalid endpoint policy")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	current, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	for _, route := range p.Routes {
		for _, existing := range current.Routes {
			if existing.Prefix != route.Prefix.String() || existing.Protocol != ownedRouteProtocol {
				continue
			}
			if route.IfIndex > 0 && existing.IfIndex != route.IfIndex {
				continue
			}
			if err := e.applyOperation(routing.Operation{
				Kind:     "delete-route",
				Prefix:   existing.Prefix,
				IfIndex:  existing.IfIndex,
				Metric:   existing.Metric,
				Protocol: existing.Protocol,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ApplyParking mirrors the Linux parking semantics: host prefixes of the
// selected traffic are held by high-metric loopback routes while the
// transport is unavailable, so nothing leaks through the physical default.
func (e *Executor) ApplyParking(ctx context.Context, p parking.DesiredState) error {
	ops := make([]routing.Operation, 0, len(p.Prefixes))
	for _, prefix := range p.Prefixes {
		if !routing.IsHostPrefix(prefix) {
			continue
		}
		ops = append(ops, routing.Operation{
			Kind:     "park",
			Prefix:   prefix.String(),
			Table:    kernelTable,
			Metric:   parkRouteMetric,
			Protocol: ownedRouteProtocol,
		})
	}
	return e.Apply(ctx, routing.Transaction{Role: p.Role, Operations: ops})
}

// Snapshot exposes the kernel forwarding table for both families. Windows has
// no policy rules, so KernelState.Rules is always empty.
func (e *Executor) Snapshot(_ context.Context) (routing.KernelState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotUnlocked()
}

func (e *Executor) snapshotUnlocked() (routing.KernelState, error) {
	state := routing.KernelState{}
	for _, family := range []winipcfg.AddressFamily{winipcfg.AddressFamily(windows.AF_INET), winipcfg.AddressFamily(windows.AF_INET6)} {
		rows, err := routeTable(family)
		if err != nil {
			return routing.KernelState{}, fmt.Errorf("read IP forwarding table: %w", err)
		}
		number := familyNumber(family)
		for i := range rows {
			row := &rows[i]
			prefix := row.DestinationPrefix.Prefix()
			op := routing.Operation{
				Kind:     "route",
				Family:   number,
				Prefix:   prefix.String(),
				Table:    kernelTable,
				IfIndex:  int(row.InterfaceIndex),
				Metric:   row.Metric,
				Protocol: int(row.Protocol),
			}
			if gateway := row.NextHop.Addr(); gateway.IsValid() && !gateway.IsUnspecified() {
				op.Gateway = gateway.String()
			}
			state.Routes = append(state.Routes, op)
		}
	}
	return state, nil
}

// rowForOperation builds the forwarding row for one operation. Park and
// unreachable routes point at the loopback interface with the loopback
// gateway, which blackholes the traffic without leaking it.
func rowForOperation(op routing.Operation) (winipcfg.MibIPforwardRow2, error) {
	row := winipcfg.MibIPforwardRow2{
		InterfaceIndex: uint32(op.IfIndex),
		Metric:         op.Metric,
		Protocol:       winipcfg.RouteProtocol(ownedRouteProtocol),
	}
	prefix, err := netip.ParsePrefix(op.Prefix)
	if err != nil {
		return row, fmt.Errorf("parse route prefix %q: %w", op.Prefix, err)
	}
	if err := row.DestinationPrefix.SetPrefix(prefix); err != nil {
		return row, fmt.Errorf("encode route prefix %q: %w", op.Prefix, err)
	}
	if op.Kind == "park" || op.Kind == "unreachable" || op.Kind == "delete-park" {
		// Parked routes (and their deletions) must carry the exact loopback
		// next hop the create path used: Windows identifies a forwarding row
		// by prefix, interface and next hop together.
		row.InterfaceIndex = loopbackIfIndex
		next := netip.MustParseAddr("127.0.0.1")
		if prefix.Addr().Is6() {
			next = netip.MustParseAddr("::1")
		}
		if err := row.NextHop.SetAddrPort(netip.AddrPortFrom(next, 0)); err != nil {
			return row, fmt.Errorf("encode loopback next hop for %q: %w", op.Prefix, err)
		}
		return row, nil
	}
	if op.Gateway != "" {
		gateway, err := netip.ParseAddr(op.Gateway)
		if err != nil {
			return row, fmt.Errorf("invalid route gateway %q: %w", op.Gateway, err)
		}
		if err := row.NextHop.SetAddrPort(netip.AddrPortFrom(gateway, 0)); err != nil {
			return row, fmt.Errorf("encode next hop %q: %w", op.Gateway, err)
		}
		return row, nil
	}
	// On-link route: Windows wants the interface's own address as the next
	// hop for directly attached destinations.
	family := windows.AF_INET
	if prefix.Addr().Is6() {
		family = windows.AF_INET6
	}
	source, err := unicastAddr(winipcfg.AddressFamily(family), row.InterfaceIndex)
	if err != nil {
		return row, fmt.Errorf("resolve on-link next hop for %q: %w", op.Prefix, err)
	}
	if err := row.NextHop.SetAddrPort(netip.AddrPortFrom(source, 0)); err != nil {
		return row, fmt.Errorf("encode on-link next hop for %q: %w", op.Prefix, err)
	}
	return row, nil
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

func familyNumber(family winipcfg.AddressFamily) int {
	if family == winipcfg.AddressFamily(windows.AF_INET6) {
		return 6
	}
	return 4
}
