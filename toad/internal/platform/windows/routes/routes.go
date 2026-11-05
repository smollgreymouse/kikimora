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

// loopbackIfIndex returns the actual loopback interface index instead of the
// conventional constant: the production audit flagged the hardcode, and a
// host with a non-standard loopback ifindex would otherwise park selected
// traffic onto an interface that does not exist.
func loopbackIfIndex() uint32 {
	adapters, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err == nil {
		for _, adapter := range adapters {
			if adapter.IfType == winipcfg.IfTypeSoftwareLoopback {
				return adapter.IfIndex
			}
		}
	}
	return 1
}

const (
	// ownedRouteProtocol tags every route this executor creates. It matches
	// parking's RTPROT_STATIC constant on purpose: parked and selected routes
	// share one ownership tag.
	ownedRouteProtocol = 4

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

// Selected-route reconciliation (08D §6). Ownership rules are the shared
// platform planner's; the I/O goes through the same tagged operations every
// other mutation uses, so table 51890 traffic and foreign rows are untouchable.

// selectedRowsFor projects same-prefix main-table rows onto the shared
// exact row model (08D1 section 9). The writer's deterministic loopback park
// shape (main table, loopback next hop of the matching family, the writer
// protocol and the managed park metric) projects to SelectedKernelPark; a
// normal forwarding row that is fully representable projects to
// SelectedKernelRoute; anything else becomes SelectedKernelForeign and is
// never dropped. Ownership is decided later by exact comparison against the
// persisted transaction state; the current loopbackIfIndex is never an
// ownership proof.
func selectedRowsFor(current *routing.KernelState, prefix netip.Prefix) []routing.SelectedKernelRow {
	rows := make([]routing.SelectedKernelRow, 0, 2)
	for _, row := range current.Routes {
		if row.Kind != "route" || row.Prefix != prefix.String() || row.Table != kernelTable {
			continue
		}
		description := fmt.Sprintf("ifindex %d gw %s proto %d metric %d", row.IfIndex, row.Gateway, row.Protocol, row.Metric)
		hop := netip.Addr{}
		if row.Gateway != "" {
			if parsed, err := netip.ParseAddr(row.Gateway); err == nil {
				hop = parsed
			}
		}
		if row.Protocol == ownedRouteProtocol && row.Metric == parkRouteMetric &&
			hop.IsValid() && hop.IsLoopback() {
			rows = append(rows, routing.SelectedKernelRow{
				Kind: routing.SelectedKernelPark,
				Park: routing.SelectedParkIdentity{
					Kind:     routing.SelectedParkLoopback,
					Table:    kernelTable,
					IfIndex:  row.IfIndex,
					NextHop:  hop.String(),
					Protocol: row.Protocol,
					Metric:   row.Metric,
				},
				Description: description,
			})
			continue
		}
		identity := routing.SelectedRouteIdentity{
			Table:    kernelTable,
			IfIndex:  row.IfIndex,
			Protocol: row.Protocol,
			Metric:   row.Metric,
		}
		if hop.IsValid() {
			identity.NextHop = hop.String()
		}
		if identity.Validate(prefix) != nil {
			rows = append(rows, routing.SelectedKernelRow{Kind: routing.SelectedKernelForeign, Description: description})
			continue
		}
		rows = append(rows, routing.SelectedKernelRow{Kind: routing.SelectedKernelRoute, Route: identity, Description: description})
	}
	return rows
}

func selectedRouteOperation(prefix netip.Prefix, identity routing.SelectedRouteIdentity) routing.Operation {
	op := routing.Operation{
		Kind:     "route",
		Prefix:   prefix.String(),
		Table:    identity.Table,
		IfIndex:  identity.IfIndex,
		Metric:   identity.Metric,
		Protocol: identity.Protocol,
	}
	if identity.NextHop != "" {
		op.Gateway = identity.NextHop
	}
	return op
}

func deleteRouteOperation(prefix netip.Prefix, identity routing.SelectedRouteIdentity) routing.Operation {
	return routing.Operation{
		Kind:     "delete-route",
		Prefix:   prefix.String(),
		Table:    identity.Table,
		IfIndex:  identity.IfIndex,
		Metric:   identity.Metric,
		Protocol: identity.Protocol,
		Gateway:  identity.NextHop,
	}
}

// NormalizeSelectedRoute builds the same row actual Create uses and projects
// the exact identity without mutation (08D1 section 4.1). Windows on-link
// rows keep their concrete next-hop representation; scope is 0 and the
// platform protocol value follows the writer (31.26 moves it to NetMgmt in
// stage 3A).
func (e *Executor) NormalizeSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error) {
	if !desired.Prefix.IsValid() || desired.Prefix != desired.Prefix.Masked() {
		return routing.SelectedRouteIdentity{}, fmt.Errorf("selected route prefix %v is not canonical", desired.Prefix)
	}
	if desired.IfIndex <= 0 {
		return routing.SelectedRouteIdentity{}, fmt.Errorf("selected route %v has no interface", desired.Prefix)
	}
	identity := routing.SelectedRouteIdentity{
		Table:    kernelTable,
		IfIndex:  desired.IfIndex,
		Protocol: ownedRouteProtocol,
		Metric:   0,
	}
	if desired.Gateway.IsValid() {
		identity.NextHop = desired.Gateway.String()
	}
	return identity, nil
}

// ReconcileSelectedRoute makes the kernel row for one registered destination
// match the desired route exactly. record is the registry record; nil makes
// every same-prefix row foreign. ERROR_OBJECT_ALREADY_EXISTS is re-read and
// classified, never broad deletion.
func (e *Executor) ReconcileSelectedRoute(_ context.Context, desired routing.SelectedRouteDesired, record *routing.SelectedRoute) error {
	if !desired.Prefix.IsValid() || desired.Prefix != desired.Prefix.Masked() {
		return fmt.Errorf("selected route prefix %v is not canonical", desired.Prefix)
	}
	if desired.IfIndex <= 0 {
		return fmt.Errorf("selected route %v has no interface", desired.Prefix)
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	current, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	action, err := routing.PlanSelectedRoute(selectedRowsFor(&current, desired.Prefix), record)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("selected route %v is not registered", desired.Prefix)
	}
	switch action {
	case routing.SelectedActionNone:
		return nil
	default:
		if err := e.applyOperation(selectedRouteOperation(desired.Prefix, record.Desired)); err != nil {
			return err
		}
	}

	// Read back: exactly one Desired row with no stale/foreign row proves
	// success.
	after, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	verified, err := routing.PlanSelectedRoute(selectedRowsFor(&after, desired.Prefix), record)
	if err != nil {
		return fmt.Errorf("selected route %v is not exactly present after the apply: %w", desired.Prefix, err)
	}
	if verified != routing.SelectedActionNone {
		return fmt.Errorf("selected route %v still needs %d after the apply", desired.Prefix, verified)
	}
	return nil
}

// RemoveSelectedRoute is state-aware and exact (08D1 section 9): Pending
// never deletes Desired; Applying may delete Desired; Active deletes Owned;
// Retiring deletes Owned plus Desired iff RetireDesired=true. Same-prefix
// foreign rows are untouched; an absent authorized identity is success.
func (e *Executor) RemoveSelectedRoute(_ context.Context, record routing.SelectedRoute) error {
	if !record.Prefix.IsValid() || record.Prefix != record.Prefix.Masked() {
		return fmt.Errorf("selected route prefix %v is not canonical", record.Prefix)
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	current, err := e.snapshotUnlocked()
	if err != nil {
		return err
	}
	rows := selectedRowsFor(&current, record.Prefix)

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
	case routing.SelectedPending, routing.SelectedActive:
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
			if !routing.SelectedRowMatchesIdentity(row, candidate) {
				continue
			}
			if err := e.applyOperation(deleteRouteOperation(record.Prefix, candidate)); err != nil {
				return err
			}
			break
		}
	}
	return nil
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
		row.InterfaceIndex = loopbackIfIndex()
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
