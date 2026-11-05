package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
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
	// counters are the K10 diagnostics; atomics because the apply section
	// runs outside mu.
	ensures         atomic.Uint64
	duplicateHits   atomic.Uint64
	kernelMutations atomic.Uint64
	denials         atomic.Uint64
	conflicts       atomic.Uint64
	capacityDenials atomic.Uint64
	forgets         atomic.Uint64
	restores        atomic.Uint64
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
		// The seeded startup generation is 1 (08D1 section 2.4 step 7:
		// checkpoint.RouteGeneration + 1, minimum 1); schema2 records carry
		// a positive route generation.
		state = &selectedRoleState{routeGeneration: 1}
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
			m.countRoleStat(result.Role, func(s *selectedRoleState) { s.conflicts.Add(1) })
		case errors.As(err, &denied):
			result.State = "denied"
			m.countRoleStat(result.Role, func(s *selectedRoleState) {
				s.denials.Add(1)
				if strings.Contains(err.Error(), "capacity") {
					s.capacityDenials.Add(1)
				}
			})
		}
	}
	return result, err
}

// countRoleStat runs fn on the role's counter block; the result carries the
// role even when the denial happened before the transaction started.
func (m *SelectedRouteManager) countRoleStat(role string, fn func(*selectedRoleState)) {
	if role == "" {
		return
	}
	m.mu.Lock()
	fn(m.roleState(role))
	m.mu.Unlock()
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
	state.ensures.Add(1)
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
		// The stable "family N is not ready" marker is the contract Leshy
		// uses to suppress that family with NODATA instead of SERVFAIL.
		reason := fmt.Sprintf("family %d is not ready for this role", family)
		if specific := runtime.Validation.Families.IPv6.Reason; family == 6 && specific != "" {
			reason += ": " + specific
		}
		if specific := runtime.Validation.Families.IPv4.Reason; family == 4 && specific != "" {
			reason += ": " + specific
		}
		m.mu.Unlock()
		result.Reason = reason
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
	toadGeneration := runtime.ToadGeneration
	m.mu.Unlock()

	// Ensure the destination is registered before any kernel mutation.
	source := routing.SelectedDNS
	if intent.Source == LeshyRouteStatic {
		source = routing.SelectedStatic
	}
	state.applyMu.Lock()
	fastPath, ensureErr := m.applySelectedRoute(role, intent.Zone, prefix, source, ifIndex, iface, generation, toadGeneration)
	state.applyMu.Unlock()
	result.Generation = generation
	if ensureErr == nil {
		if fastPath {
			// The exact row was already verified: zero I/O, not a mutation.
			state.duplicateHits.Add(1)
		} else {
			state.kernelMutations.Add(1)
		}
	}
	if ensureErr == nil {
		result.State = "applied"
	}

	m.mu.Lock()
	state.inFlight--
	m.cond.Broadcast()
	m.mu.Unlock()

	return result, ensureErr
}

// applySelectedRoute runs Reserve + Pending checkpoint, the kernel
// reconciliation with its exact read-back, MarkActive + Active checkpoint
// (08D1 section 5.1; the verification-token fast path lands with K13). A
// failure after the Reserve keeps the Pending ownership record so recovery
// can park the destination.
func (m *SelectedRouteManager) applySelectedRoute(role, zone string, prefix netip.Prefix, source routing.SelectedRouteSource, ifIndex int, iface string, generation uint64, toadGeneration uint64) (bool, error) {
	// Fast path: an entry that is Active with Owned == Desired on the
	// current interface was already verified in the kernel — zero I/O. A
	// dropped row is reconciled by the monitor/audit, not by replayed DNS
	// traffic.
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Prefix == prefix && entry.State == routing.SelectedActive &&
			entry.Zone == zone && entry.Source == source &&
			entry.Desired.IfIndex == ifIndex && entry.Owned != nil && *entry.Owned == entry.Desired {
			return true, nil
		}
	}
	if !m.routesResolved {
		return false, &SelectedRouteDeniedError{Role: role, Zone: zone, Prefix: prefix.String(), Reason: "this platform has no selected-route writer"}
	}
	desired := routing.SelectedRouteDesired{Role: role, Prefix: prefix, IfIndex: ifIndex}
	identity, err := m.routes.NormalizeSelectedRoute(context.Background(), desired)
	if err != nil {
		return false, err
	}
	if err := m.registry.Reserve(role, zone, prefix, source, toadGeneration, generation, iface, identity); err != nil {
		return false, err
	}
	// Checkpoint the reserved ownership before the kernel mutation.
	if err := m.writeCheckpoint(role, generation); err != nil {
		return false, fmt.Errorf("checkpoint reserved ownership: %w", err)
	}
	// The preflight precedes MarkApplying in the final 5.1 order; the exact
	// preflight surface lands with K12. MarkApplying authorizes the kernel
	// mutation and is checkpointed before it starts.
	if err := m.registry.MarkApplying(role, prefix); err != nil {
		return false, err
	}
	if err := m.writeCheckpoint(role, generation); err != nil {
		return false, fmt.Errorf("checkpoint applying ownership: %w", err)
	}
	record, _ := m.registryEntry(role, prefix)
	// Reconcile with exact read-back.
	if err := m.routes.ReconcileSelectedRoute(context.Background(), desired, record); err != nil {
		if isForeignRowError(err) {
			return false, &SelectedRouteConflictError{Role: role, Zone: zone, Prefix: prefix.String()}
		}
		return false, err
	}
	// The row is exactly verified: record it and checkpoint.
	if err := m.registry.MarkActive(role, prefix, identity); err != nil {
		return false, err
	}
	return false, m.writeCheckpoint(role, generation)
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

	m.countForgetState(role)
	record, ok := m.registryEntry(role, prefix)
	if !ok {
		return nil
	}
	if !m.routesResolved {
		return fmt.Errorf("this platform has no selected-route writer")
	}
	// Durable retirement evidence first (08D1 section 7.6): RetireDesired
	// records whether the syscall was authorized from Applying.
	if err := m.registry.MarkRetiring(role, prefix); err != nil {
		return err
	}
	if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
		return err
	}
	record, _ = m.registryEntry(role, prefix)
	if err := m.routes.RemoveSelectedRoute(ctx, *record); err != nil {
		// The Retiring evidence stays: a restart continues the exact
		// deletion from the checkpoint.
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
		// Same retirement helper per entry (08D1 section 5.5): a failed
		// delete keeps the Retiring record and the checkpoint evidence.
		if err := m.registry.MarkRetiring(role, entry.Prefix); err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		if err := m.writeCheckpoint(role, generation); err != nil && firstErr == nil {
			firstErr = err
		}
		if m.routesResolved {
			current, _ := m.registryEntry(role, entry.Prefix)
			if err := m.routes.RemoveSelectedRoute(ctx, *current); err != nil && firstErr == nil {
				firstErr = err
				continue
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
	m.countRestoreState(role)
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
			// A family that is not Ready stays parked (08D section 12):
			// readiness of one family never authorizes the other.
			continue
		}
		if entry.State == routing.SelectedActive && entry.Owned != nil &&
			*entry.Owned == entry.Desired && entry.Desired.IfIndex == ifIndex {
			// Already exactly verified on this interface: the park cleanup,
			// not a re-apply, owns the next transition.
			continue
		}
		desired := routing.SelectedRouteDesired{Role: role, Prefix: entry.Prefix, IfIndex: ifIndex}
		identity, err := m.routes.NormalizeSelectedRoute(ctx, desired)
		if err != nil {
			return err
		}
		if err := m.registry.Reserve(role, entry.Zone, entry.Prefix, entry.Source, runtime.ToadGeneration, m.routeGenerationLocked(role), runtime.Toad.InterfaceName, identity); err != nil {
			return err
		}
		record, _ := m.registryEntry(role, entry.Prefix)
		if err := m.registry.MarkApplying(role, entry.Prefix); err != nil {
			return err
		}
		if err := m.routes.ReconcileSelectedRoute(ctx, desired, record); err != nil {
			return err
		}
		if err := m.registry.MarkActive(role, entry.Prefix, identity); err != nil {
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
	checkpoint := routing.SelectedRoutesCheckpoint{
		Schema:          routing.SelectedRoutesCheckpointSchema,
		Role:            role,
		RouteGeneration: generation,
		Entries:         m.registry.Snapshot(role),
		Platform:        routing.RuntimeCheckpointPlatform(),
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

// isConflictErr reports whether the error is the typed conflict verdict.
func isConflictErr(err error) bool {
	var conflict *SelectedRouteConflictError
	return errors.As(err, &conflict)
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

// SetManagedSelectedRole flips the managed recovery gate for the role.
func (m *Manager) SetManagedSelectedRole(role string, managed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.managedSelectedRoles == nil {
		m.managedSelectedRoles = make(map[string]bool)
	}
	m.managedSelectedRoles[role] = managed
}

func (m *Manager) managedSelected(role string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.managedSelectedRoles[role] && m.selectedRoutes != nil
}

// parkExecutor returns the routing.Executor surface when the wired writer
// also implements it (the Linux/Windows executors do).
func (m *SelectedRouteManager) parkExecutor() routing.Executor {
	if executor, ok := m.routes.(routing.Executor); ok {
		return executor
	}
	return nil
}

// MarkParked installs the verified parks through the registry park
// transaction and checkpoints every transition, so a crash after parking
// still knows the protection exists (08D1 section 7.2 lite; the full
// ParkRegistered resume semantics land with K14).
func (m *SelectedRouteManager) MarkParked(role string, prefixes []netip.Prefix) error {
	for _, prefix := range prefixes {
		parkDesired := routing.SelectedParkIdentity{
			Kind:     routing.SelectedParkUnreachable,
			Table:    routing.ManagedSelectedRouteTable,
			Protocol: selectedRouteProtocol,
			Metric:   routing.ManagedParkMetric,
		}
		if err := m.registry.ReservePark(role, prefix, parkDesired, false); err != nil {
			return err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return err
		}
		if err := m.registry.MarkParkApplying(role, prefix); err != nil {
			return err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return err
		}
		if m.routesResolved {
			// The generic parking apply is the only platform surface that
			// writes unreachable rows today; K14 replaces this call with the
			// dedicated managed-park platform methods.
			executor := m.parkExecutor()
			if executor == nil {
				return fmt.Errorf("this platform has no parking writer")
			}
			if err := executor.Apply(context.Background(), routing.Transaction{Role: role, Operations: []routing.Operation{{
				Kind:     "park",
				Prefix:   prefix.String(),
				Table:    routing.ManagedSelectedRouteTable,
				Metric:   routing.ManagedParkMetric,
				Protocol: selectedRouteProtocol,
			}}}); err != nil {
				return err
			}
		}
		if err := m.registry.MarkParkActive(role, prefix, parkDesired); err != nil {
			return err
		}
		if err := m.writeCheckpoint(role, m.routeGenerationLocked(role)); err != nil {
			return err
		}
	}
	return nil
}

// currentGeneration exposes the role's current route generation for the
// managed publication.
func (m *SelectedRouteManager) currentGeneration(role string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roleState(role).routeGeneration
}

// ---------------------------------------------------------------------------
// Managed-mode activation gate (08D §11): the generated Leshy configuration
// switches to routing_backend = "kikimora" only through this explicit gate.
// If any check fails the install stays entirely in legacy-file mode; there
// is no automatic fallback in either direction.
// ---------------------------------------------------------------------------

// managedModeMarker is the persisted activation record; the boot path reads
// it so a restarted core keeps the mode.
type managedModeMarker struct {
	Schema       int      `json:"schema"`
	Socket       string   `json:"socket"`
	CoreVersion  string   `json:"core_version"`
	ManagedZones []string `json:"managed_zones"`
}

const managedModeMarkerSchema = 1

// EnableManagedSelectedRoutes flips the install into managed mode. The gate
// requires a non-empty socket and an explicitly verified managed-capable
// Leshy installation: the installer confirms both capability handshakes per
// 08D §18 before calling this. Every role with a Leshy zone flips to the
// managed recovery sequence and the router configuration is regenerated.
func (m *Manager) EnableManagedSelectedRoutes(socketPath string, leshySupportsManaged bool) error {
	if socketPath == "" {
		return fmt.Errorf("managed mode requires the kikimora_socket path")
	}
	if !leshySupportsManaged {
		return fmt.Errorf("the installed Leshy does not advertise managed backend support; staying in legacy-file mode")
	}
	m.mu.Lock()
	m.managedSocket = socketPath
	zones := make([]string, 0, len(m.roles))
	for name, r := range m.roles {
		if r.cfg == nil || r.cfg.EffectiveEndpointPolicy().Zone == "" {
			continue
		}
		zones = append(zones, r.cfg.EffectiveEndpointPolicy().Zone)
		m.managedSelectedRoles[name] = true
	}
	m.mu.Unlock()

	if err := m.writeManagedMarker(socketPath, zones); err != nil {
		return err
	}
	return m.RegenerateLeshyConfig()
}

// DisableManagedSelectedRoutes is the explicit rollback: one transaction
// back to legacy-file mode. Both writers must never run concurrently
// (08D §18).
func (m *Manager) DisableManagedSelectedRoutes() error {
	m.mu.Lock()
	m.managedSocket = ""
	for name := range m.managedSelectedRoles {
		m.managedSelectedRoles[name] = false
	}
	m.mu.Unlock()
	if err := m.clearManagedMarker(); err != nil {
		return err
	}
	return m.RegenerateLeshyConfig()
}

func (m *Manager) managedMarkerPath() string {
	if m.configDir == "" {
		return ""
	}
	return filepath.Join(m.configDir, "leshy-managed.json")
}

func (m *Manager) writeManagedMarker(socket string, zones []string) error {
	path := m.managedMarkerPath()
	if path == "" {
		return fmt.Errorf("the core has no configuration directory")
	}
	marker := managedModeMarker{
		Schema:       managedModeMarkerSchema,
		Socket:       socket,
		CoreVersion:  BuildVersion,
		ManagedZones: zones,
	}
	return writeAtomicJSON(path, marker)
}

func (m *Manager) clearManagedMarker() error {
	path := m.managedMarkerPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadManagedMarker restores the persisted mode at boot and re-arms the
// managed recovery sequence for the recorded roles.
func (m *Manager) loadManagedMarker() {
	path := m.managedMarkerPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return // a fresh install has no marker: legacy-file mode
	}
	var marker managedModeMarker
	if json.Unmarshal(data, &marker) != nil || marker.Schema != managedModeMarkerSchema || marker.Socket == "" {
		return
	}
	m.mu.Lock()
	m.managedSocket = marker.Socket
	// Re-arm the managed recovery sequence for every role whose zone the
	// marker recorded.
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zone := r.cfg.EffectiveEndpointPolicy().Zone
		for _, managed := range marker.ManagedZones {
			if zone != "" && zone == managed {
				m.managedSelectedRoles[name] = true
			}
		}
	}
	m.mu.Unlock()
}

// writeAtomicJSON stores small control-plane records through the shared
// durable primitive (08D1 §31.7): same temp-file + rename + directory-sync
// discipline as the checkpoints, with the Windows MoveFileExW
// replace-existing/write-through contract.
func writeAtomicJSON(path string, value any) error {
	return fsutil.WriteAtomicJSON(path, value, 0o600)
}

// managedSocketForGeneration returns the activated socket when the install
// is in managed mode.
// ManagedModeActive reports whether the strict marker load found this
// install in managed mode (08D1 §31.6): the privileged auth key contract is
// fail-closed for managed installs, regenerating for local ones.
func (m *Manager) ManagedModeActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.managedSocket != ""
}

func (m *Manager) managedSocketForGeneration() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.managedSocket
}

// SelectedRouteStats is the per-role diagnostics view (08D K10). Successes
// are counted, never logged per event: a duplicate Ensure hit is a number,
// not a log line.
type SelectedRouteStats struct {
	Registered      int    `json:"registered"`
	Active          int    `json:"active"`
	Parked          int    `json:"parked"`
	Retiring        int    `json:"retiring"`
	ParkPending     int    `json:"park_pending"`
	ParkApplying    int    `json:"park_applying"`
	ParkActive      int    `json:"park_active"`
	ParkRetiring    int    `json:"park_retiring"`
	Ensures         uint64 `json:"ensures"`
	DuplicateHits   uint64 `json:"duplicate_hits"`
	KernelMutations uint64 `json:"kernel_mutations"`
	Denials         uint64 `json:"denials"`
	Conflicts       uint64 `json:"conflicts"`
	CapacityDenials uint64 `json:"capacity_denials"`
	Forgets         uint64 `json:"forgets"`
	Restores        uint64 `json:"restores"`
}

// Diagnostics returns the role's selected-route counters.
func (m *SelectedRouteManager) Diagnostics(role string) SelectedRouteStats {
	stats := SelectedRouteStats{}
	for _, entry := range m.registry.Snapshot(role) {
		stats.Registered++
		switch entry.State {
		case routing.SelectedActive:
			stats.Active++
		case routing.SelectedRetiring:
			stats.Retiring++
		}
		if entry.Park != nil {
			switch entry.Park.State {
			case routing.SelectedParkPending:
				stats.ParkPending++
			case routing.SelectedParkApplying:
				stats.ParkApplying++
			case routing.SelectedParkActive:
				stats.ParkActive++
			case routing.SelectedParkRetiring:
				stats.ParkRetiring++
			}
		}
	}
	m.mu.Lock()
	state, ok := m.roles[role]
	m.mu.Unlock()
	if !ok {
		return stats
	}
	stats.Ensures = state.ensures.Load()
	stats.DuplicateHits = state.duplicateHits.Load()
	stats.KernelMutations = state.kernelMutations.Load()
	stats.Denials = state.denials.Load()
	stats.Conflicts = state.conflicts.Load()
	stats.CapacityDenials = state.capacityDenials.Load()
	stats.Forgets = state.forgets.Load()
	stats.Restores = state.restores.Load()
	return stats
}

func (m *SelectedRouteManager) countForgetState(role string) {
	m.mu.Lock()
	m.roleState(role).forgets.Add(1)
	m.mu.Unlock()
}

func (m *SelectedRouteManager) countRestoreState(role string) {
	m.mu.Lock()
	m.roleState(role).restores.Add(1)
	m.mu.Unlock()
}
