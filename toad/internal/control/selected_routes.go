package control

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// SelectedRouteManager is the single-writer transaction boundary for
// Leshy-selected routes (08D §5): it owns the explicit registry, the route
// generation per role, the freeze state and the in-flight counter, and it
// drives the platform reconciler through Reserve → apply → verify →
// MarkActive.
type SelectedRouteManager struct {
	mu       sync.Mutex
	registry routing.SelectedRouteRegistry
	routes   platform.RouteManager
	// routesResolved distinguishes "no writer on this platform" from "not
	// wired yet": both deny every intent fail-closed.
	routesResolved bool

	// roleRuntime exposes the authoritative product state per role.
	roleRuntime func(name string) (core.RoleRuntime, bool)
	// zoneRoles indexes the configured zones; a zone must map to exactly one
	// role, so ambiguous zones keep every candidate and Ensure fails hard.
	zoneRoles map[string][]string
	// stateDir resolves a role's state directory for its checkpoint.
	stateDir func(role string) (string, bool)
	// routeManagers per platform may be unavailable (darwin); nil routes
	// denies every intent.
	routesAvailable bool

	// per-role transaction state, guarded by mu + cond.
	roles map[string]*selectedRoleState
	cond  *sync.Cond

	capacity int
}

// selectedRouteProtocol is the existing selected-route protocol value
// (RTPROT_STATIC): the packet keeps it during migration so adoption needs
// no dedicated kernel-marker step.
const selectedRouteProtocol = 4

// SelectedRouteCapacity bounds the DNS host intents per role (08D §5.2).
// Static entries are separate and never evicted.
const SelectedRouteCapacity = 16384

type selectedRoleState struct {
	frozen          bool
	inFlight        int
	routeGeneration uint64
	// applyMu serializes one role's Ensure transactions so concurrent
	// duplicates coalesce: the first apply lands the row, the rest take the
	// registry fast path.
	applyMu sync.Mutex
}

// SelectedRouteDeniedError is a safety denial: the controller refused the
// intent, so the DNS layer must not answer with the address. It maps to
// LeshyRouteResult.State "denied".
type SelectedRouteDeniedError struct {
	Role   string
	Zone   string
	Prefix string
	Reason string
}

func (e *SelectedRouteDeniedError) Error() string {
	return fmt.Sprintf("route %s denied for role %q: %s", e.Prefix, e.Role, e.Reason)
}

// SelectedRouteConflictError reports a foreign row on the destination. It
// maps to LeshyRouteResult.State "conflict".
type SelectedRouteConflictError struct {
	Role   string
	Zone   string
	Prefix string
}

func (e *SelectedRouteConflictError) Error() string {
	return fmt.Sprintf("route %s conflicts with a foreign row for role %q", e.Prefix, e.Role)
}

// NewSelectedRouteManager wires the manager. routes may be nil on platforms
// without a selected-route writer; every intent is then denied fail-closed.
func NewSelectedRouteManager(
	registry routing.SelectedRouteRegistry,
	routes platform.RouteManager,
	roleRuntime func(name string) (core.RoleRuntime, bool),
	zoneRoles map[string][]string,
	stateDir func(role string) (string, bool),
) *SelectedRouteManager {
	m := &SelectedRouteManager{
		registry:       registry,
		roleRuntime:    roleRuntime,
		zoneRoles:      zoneRoles,
		stateDir:       stateDir,
		routesResolved: routes != nil,
		roles:          make(map[string]*selectedRoleState),
		capacity:       SelectedRouteCapacity,
	}
	if routes != nil {
		m.routes = routes
	}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// SetRoutes wires the platform writer after construction (main.go resolves
// it once per process; tests inject fakes).
func (m *SelectedRouteManager) SetRoutes(routes platform.RouteManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if routes == nil {
		return
	}
	m.routes = routes
	m.routesResolved = true
}

func (m *SelectedRouteManager) roleState(role string) *selectedRoleState {
	state, ok := m.roles[role]
	if !ok {
		state = &selectedRoleState{}
		m.roles[role] = state
	}
	return state
}

// Ensure applies one Leshy intent in the exact 08D §5.1 order. The returned
// generation is the controller's current route generation for the role.
func (m *SelectedRouteManager) Ensure(ctx context.Context, intent LeshyRouteIntent) (LeshyRouteResult, error) {
	result, err := m.ensure(ctx, intent)
	if err != nil {
		var denied *SelectedRouteDeniedError
		var conflict *SelectedRouteConflictError
		switch {
		case errors.As(err, &conflict):
			result.State = "conflict"
		case errors.As(err, &denied):
			result.State = "denied"
		}
	}
	return result, err
}

func (m *SelectedRouteManager) ensure(ctx context.Context, intent LeshyRouteIntent) (LeshyRouteResult, error) {
	// (1) zone -> exactly one role; an unknown or ambiguous zone is a hard
	// error with no route mutation.
	candidates, ok := m.zoneRoles[intent.Zone]
	if !ok || len(candidates) != 1 {
		return LeshyRouteResult{
			Zone:   intent.Zone,
			Prefix: intent.Prefix,
			State:  "denied",
		}, &SelectedRouteDeniedError{
			Zone:   intent.Zone,
			Prefix: intent.Prefix,
			Reason: fmt.Sprintf("zone maps to %d configured roles", len(candidates)),
		}
	}
	role := candidates[0]

	// (2) canonicalize/validate the prefix and the source. The request may
	// never name a role, interface, ifindex, gateway, table, metric or
	// protocol.
	prefix, err := canonicalIntentPrefix(intent)
	if err != nil {
		return LeshyRouteResult{
			Zone:   intent.Zone,
			Prefix: intent.Prefix,
			State:  "denied",
		}, &SelectedRouteDeniedError{Zone: intent.Zone, Prefix: intent.Prefix, Reason: err.Error()}
	}
	family := 4
	if !prefix.Addr().Is4() {
		family = 6
	}
	// The verdict is filled in as far as the pipeline got: a denial still
	// tells Leshy which role/family refused it.
	result := LeshyRouteResult{
		Role:   role,
		Zone:   intent.Zone,
		Prefix: prefix.String(),
		Family: family,
	}

	m.mu.Lock()
	state := m.roleState(role)
	// (3)+(4) acquire the role lock; a frozen role denies new intents (they
	// must wait for Unfreeze rather than race the recovery).
	for state.frozen {
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "role selected-route state is frozen for recovery",
		}
	}
	// (5) snapshot the authoritative product role.
	runtime, ok := m.roleRuntime(role)
	if !ok {
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "role is not configured in the product state",
		}
	}
	// (6) desired role, current validation, requested family Ready.
	if !runtime.Desired {
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "role is not desired",
		}
	}
	if !runtime.Validation.Healthy {
		reason := runtime.Validation.Reason
		if reason == "" {
			reason = "current validation is not healthy"
		}
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(), Reason: reason,
		}
	}
	var familyReady bool
	switch family {
	case 4:
		familyReady = runtime.Validation.Families.IPv4.Ready
	default:
		familyReady = runtime.Validation.Families.IPv6.Ready
	}
	if !familyReady {
		reason := runtime.Validation.Families.IPv6.Reason
		if family == 4 {
			reason = runtime.Validation.Families.IPv4.Reason
		}
		if reason == "" {
			reason = fmt.Sprintf("family %d is not ready for this role", family)
		}
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(), Reason: reason,
		}
	}
	// (7) derive the target from the current Toad snapshot: the interface is
	// authoritative controller state, the route is a device route on it.
	ifIndex := runtime.Toad.IfIndex
	iface := runtime.Toad.InterfaceName
	if ifIndex <= 0 {
		m.mu.Unlock()
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "the current Toad snapshot has no managed interface",
		}
	}
	// Capacity: a new unique DNS intent over the per-role bound is a safety
	// denial; existing intents keep reconciling and nothing is evicted.
	if intent.Source == LeshyRouteDNS {
		used := 0
		for _, entry := range m.registry.Snapshot(role) {
			if entry.Source == routing.SelectedDNS && entry.Prefix != prefix {
				used++
			}
		}
		if used >= m.capacity {
			m.mu.Unlock()
			return result, &SelectedRouteDeniedError{
				Role: role, Zone: intent.Zone, Prefix: prefix.String(),
				Reason: fmt.Sprintf("selected-route capacity %d reached for this role", m.capacity),
			}
		}
	}
	// (8) the in-flight counter is what Freeze waits on.
	state.inFlight++
	generation := state.routeGeneration
	m.mu.Unlock()

	// Ensure the destination is registered before any kernel mutation.
	source := routing.SelectedDNS
	if intent.Source == LeshyRouteStatic {
		source = routing.SelectedStatic
	}
	state.applyMu.Lock()
	ensureErr := m.applySelectedRoute(role, intent.Zone, prefix, source, ifIndex, iface, generation)
	state.applyMu.Unlock()
	result.Generation = generation
	if ensureErr == nil {
		result.State = "applied"
	}

	m.mu.Lock()
	state.inFlight--
	m.cond.Broadcast()
	m.mu.Unlock()

	return result, ensureErr
}

// applySelectedRoute runs steps 9-12: Reserve + checkpoint, kernel
// reconciliation with its exact read-back, MarkActive + checkpoint. A
// failure after the Reserve keeps the Pending ownership record so recovery
// can park the destination.
func (m *SelectedRouteManager) applySelectedRoute(role, zone string, prefix netip.Prefix, source routing.SelectedRouteSource, ifIndex int, iface string, generation uint64) error {
	// Fast path: an entry that is Active with the same identity was already
	// verified in the kernel — zero I/O. A dropped row is reconciled by the
	// monitor/audit, not by replayed DNS traffic.
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Prefix == prefix && entry.State == routing.SelectedActive &&
			entry.Zone == zone && entry.Source == source && entry.IfIndex == ifIndex {
			return nil
		}
	}
	if !m.routesResolved {
		return &SelectedRouteDeniedError{Role: role, Zone: zone, Prefix: prefix.String(), Reason: "this platform has no selected-route writer"}
	}
	if err := m.registry.Reserve(role, zone, prefix, source, 0, generation, iface, ifIndex); err != nil {
		return err
	}
	// (9) checkpoint the reserved ownership before the kernel mutation.
	if err := m.writeCheckpoint(role, generation); err != nil {
		return fmt.Errorf("checkpoint reserved ownership: %w", err)
	}
	desired := routing.SelectedRouteDesired{
		Role:     role,
		Prefix:   prefix,
		IfIndex:  ifIndex,
		Protocol: selectedRouteProtocol,
	}
	record, _ := m.registryEntry(role, prefix)
	// (10)+(11) reconcile with exact read-back.
	if err := m.routes.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
		if isForeignRowError(err) {
			return &SelectedRouteConflictError{Role: role, Zone: zone, Prefix: prefix.String()}
		}
		return err
	}
	// (12) the row is exactly verified: record it and checkpoint.
	if err := m.registry.MarkActive(role, prefix); err != nil {
		return err
	}
	return m.writeCheckpoint(role, generation)
}

// Forget retires one explicit intent: configuration removal, target
// reassignment or operator cleanup — never DNS TTL expiry.
func (m *SelectedRouteManager) Forget(ctx context.Context, intent LeshyRouteIntent) error {
	prefix, err := canonicalIntentPrefix(intent)
	if err != nil {
		return &SelectedRouteDeniedError{Zone: intent.Zone, Prefix: intent.Prefix, Reason: err.Error()}
	}
	candidates := m.zoneRoles[intent.Zone]
	if len(candidates) != 1 {
		return &SelectedRouteDeniedError{Zone: intent.Zone, Prefix: prefix.String(), Reason: fmt.Sprintf("zone maps to %d configured roles", len(candidates))}
	}
	role := candidates[0]

	m.mu.Lock()
	state := m.roleState(role)
	state.inFlight++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		state.inFlight--
		m.cond.Broadcast()
		m.mu.Unlock()
	}()

	record, ok := m.registryEntry(role, prefix)
	if !ok {
		return nil
	}
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	if err := m.routes.RemoveSelectedRoute(ctx, *record); err != nil {
		return err
	}
	m.registry.Remove(role, prefix)
	return m.writeCheckpoint(role, m.routeGenerationLocked(role))
}

// FlushZone retires every registered intent of one zone: explicit zone
// deletion or target reassignment.
func (m *SelectedRouteManager) FlushZone(ctx context.Context, zone string) error {
	candidates := m.zoneRoles[zone]
	if len(candidates) != 1 {
		return &SelectedRouteDeniedError{Zone: zone, Reason: fmt.Sprintf("zone maps to %d configured roles", len(candidates))}
	}
	role := candidates[0]
	m.mu.Lock()
	state := m.roleState(role)
	state.inFlight++
	generation := state.routeGeneration
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		state.inFlight--
		m.cond.Broadcast()
		m.mu.Unlock()
	}()

	var firstErr error
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Zone != zone {
			continue
		}
		if m.routesAvailable {
			if err := m.routes.RemoveSelectedRoute(ctx, entry); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		m.registry.Remove(role, entry.Prefix)
	}
	if err := m.writeCheckpoint(role, generation); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Freeze closes the role's selected-route surface for recovery: new Ensure
// calls are denied and the call blocks until every in-flight transaction
// finished. It returns the stable ownership set and the current generation.
func (m *SelectedRouteManager) Freeze(role string) ([]routing.SelectedRoute, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.roleState(role)
	state.frozen = true
	for state.inFlight > 0 {
		m.cond.Wait()
	}
	return m.registry.Snapshot(role), state.routeGeneration, nil
}

// Unfreeze re-opens the role under a new route generation.
func (m *SelectedRouteManager) Unfreeze(role string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.roleState(role)
	state.frozen = false
	state.routeGeneration++
	m.cond.Broadcast()
	return state.routeGeneration
}

// Restore re-applies the registered routes of the role's Ready families to
// the current Toad interface. Parks stay in place until the exact
// restoration is verified.
func (m *SelectedRouteManager) Restore(ctx context.Context, role string, v4Ready, v6Ready bool) error {
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	runtime, ok := m.roleRuntime(role)
	if !ok {
		return fmt.Errorf("role %q is not configured in the product state", role)
	}
	ifIndex := runtime.Toad.IfIndex
	if ifIndex <= 0 {
		return fmt.Errorf("role %q has no managed interface to restore onto", role)
	}
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Source == routing.SelectedDNS && !routing.IsHostPrefix(entry.Prefix) {
			continue
		}
		family := 4
		if !entry.Prefix.Addr().Is4() {
			family = 6
		}
		if (family == 4 && !v4Ready) || (family == 6 && !v6Ready) {
			// A family that is not Ready stays parked (08D §12): readiness
			// of one family never authorizes the other.
			continue
		}
		if err := m.registry.Reserve(role, entry.Zone, entry.Prefix, entry.Source, 0, m.routeGenerationLocked(role), runtime.Toad.InterfaceName, ifIndex); err != nil {
			return err
		}
		desired := routing.SelectedRouteDesired{
			Role:     role,
			Prefix:   entry.Prefix,
			IfIndex:  ifIndex,
			Protocol: selectedRouteProtocol,
		}
		record, _ := m.registryEntry(role, entry.Prefix)
		if err := m.routes.ReconcileSelectedRoute(ctx, desired, record); err != nil {
			return err
		}
		if err := m.registry.MarkActive(role, entry.Prefix); err != nil {
			return err
		}
	}
	return m.writeCheckpoint(role, m.routeGenerationLocked(role))
}

// RegistrySnapshot exposes the role's registered routes for diagnostics.
func (m *SelectedRouteManager) RegistrySnapshot(role string) []routing.SelectedRoute {
	return m.registry.Snapshot(role)
}

func (m *SelectedRouteManager) registryEntry(role string, prefix netip.Prefix) (*routing.SelectedRoute, bool) {
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Prefix == prefix {
			return &entry, true
		}
	}
	return nil, false
}

func (m *SelectedRouteManager) routeGenerationLocked(role string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roleState(role).routeGeneration
}

func (m *SelectedRouteManager) writeCheckpoint(role string, generation uint64) error {
	if m.stateDir == nil {
		return nil
	}
	dir, ok := m.stateDir(role)
	if !ok || dir == "" {
		return nil
	}
	entries := m.registry.Snapshot(role)
	checkpoint := routing.SelectedRoutesCheckpoint{
		Schema:          1,
		Role:            role,
		RouteGeneration: generation,
		Entries:         entries,
	}
	for _, entry := range entries {
		if entry.State == routing.SelectedParked {
			checkpoint.Parked = append(checkpoint.Parked, entry.Prefix.String())
		}
	}
	return routing.WriteSelectedRoutesCheckpoint(filepath.Join(dir, "selected-routes.json"), checkpoint)
}

// canonicalIntentPrefix validates the wire prefix: it must parse, be
// canonical, reject mapped IPv6, and a DNS intent must be a host prefix.
func canonicalIntentPrefix(intent LeshyRouteIntent) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(intent.Prefix)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("prefix %q does not parse", intent.Prefix)
	}
	if prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("prefix %q is not canonical", intent.Prefix)
	}
	addr := prefix.Addr()
	if addr.Is4In6() {
		return netip.Prefix{}, fmt.Errorf("mapped IPv6 prefix %q is rejected", intent.Prefix)
	}
	switch intent.Source {
	case LeshyRouteDNS:
		if !routing.IsHostPrefix(prefix) {
			return netip.Prefix{}, fmt.Errorf("DNS intents require a host prefix, got %q", intent.Prefix)
		}
	case LeshyRouteStatic:
	default:
		return netip.Prefix{}, fmt.Errorf("unknown route source %q", intent.Source)
	}
	return prefix, nil
}

// isForeignRowError recognises the reconciler's foreign-row refusal: the
// planner reports it with a stable message.
func isForeignRowError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "conflicts with a foreign row")
}

// newSelectedRouteManager builds the manager from the configured roles: the
// zone index comes from every role's effective endpoint policy, and the
// state-directory resolver follows the role configs.
func (m *Manager) newSelectedRouteManager(specs []core.RoleSpec) *SelectedRouteManager {
	zoneRoles := make(map[string][]string, len(specs))
	for _, spec := range specs {
		if spec.LeshyZone == "" {
			continue
		}
		zoneRoles[spec.LeshyZone] = append(zoneRoles[spec.LeshyZone], spec.ID)
	}
	roleRuntime := func(name string) (core.RoleRuntime, bool) {
		return m.product.Role(name)
	}
	stateDir := func(role string) (string, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		r, ok := m.roles[role]
		if !ok || r.cfg == nil || r.cfg.StateDir == "" {
			return "", false
		}
		return r.cfg.StateDir, true
	}
	return NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		nil,
		roleRuntime,
		zoneRoles,
		stateDir,
	)
}

// SetSelectedRouteWriter wires the platform writer once the platform is
// resolved. Without a writer every intent is denied fail-closed.
func (m *Manager) SetSelectedRouteWriter(routes platform.RouteManager) {
	if m.selectedRoutes != nil {
		m.selectedRoutes.SetRoutes(routes)
	}
}
