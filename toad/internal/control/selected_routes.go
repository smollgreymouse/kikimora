package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// failpoint is nil in production; tests inject the interruption seam.
	failpoint selectedFailpoint

	// observation is the §31.9 sequence source (nil when the platform has
	// no native observer).
	observation platform.SelectedRouteObservation
	// processedSequence gates the fast path (§31.9).
	processedSequence atomic.Uint64

	// reconciler diagnostics (§10.6).
	observerErrors   atomic.Uint64
	auditSkippedBusy atomic.Uint64
	auditFailures    atomic.Uint64
	audits           atomic.Uint64
	driftRepairs     atomic.Uint64

	// owner backreference for the recovery request path.
	owner *Manager

	// activationFence resolves the role's active schema2 activation fence
	// (D7R §6.2). Every checkpoint write requires it: an unfenced checkpoint
	// could be restored after a Disable + re-Enable of the same role/zone
	// (P0-4). Unit tests inject a deterministic fence.
	activationFence func(role string) (selectedActivationFence, bool)
	// managedRoleCheck reports whether the role is authorized by the active
	// schema2 marker (D8 §8.5): local mode is never a selected writer, and
	// routeAuthorityReady is never a substitute for mode authorization.
	// Production wires it from the Manager; nil denies everything.
	managedRoleCheck func(role string) bool

	// reconciler is the §10 owner of the audit loop and the §31.11 startup
	// gate.
	reconciler *selectedRouteReconciler
	// authorityReady reports the §31.11 route-authority gate; managed
	// Ensure stays retryable-denied until it opens.
	authorityReady atomic.Bool
}

// SetFailpoint installs the unit-test interruption seam (08D1 §22.1).
func (m *SelectedRouteManager) SetFailpoint(hook selectedFailpoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failpoint = hook
}

// runFailpoint invokes both the injectable unit hook and the process seam
// at one named durable boundary.
func (m *SelectedRouteManager) runFailpoint(point, role string, prefix netip.Prefix) error {
	if m.failpoint != nil {
		if err := m.failpoint(point, role, prefix); err != nil {
			return err
		}
	}
	processSelectedFailpoint(point, role, prefix)
	return nil
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
	// inFlightZero is the context-aware zero-in-flight signal (08D1 §31.20):
	// created open when inFlight goes 0 -> 1 and closed exactly once when it
	// reaches zero again. Freeze snapshots it before waiting, so a Freeze
	// that times out leaves the role frozen without wedging the caller.
	inFlightZero chan struct{}
	// restorePending is true only for non-empty ownership restored from
	// disk (08D1 §2.5): it keeps the role frozen until
	// CompleteStartupRestore or the normal recovery Unfreeze clears it.
	restorePending bool
	// integrityQuarantined is the §31.21 dedicated foreign-conflict state:
	// frozen, observed, never auto-recovered and never torn down.
	integrityQuarantined bool
	integrityReason      string
	// verification holds the in-memory zero-I/O fast-path tokens (§5.2):
	// checkpoint restore never creates one, and invalidation clears them.
	verification map[netip.Prefix]selectedVerification
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

// selectedVerification is the zero-I/O fast-path proof (08D1 §5.2): the
// exact kernel identity is still current for the current Toad generation
// and route generation.
type selectedVerification struct {
	ToadGeneration  uint64
	RouteGeneration uint64
	Identity        routing.SelectedRouteIdentity
}

// selectedActivationFence is the active schema2 activation identity a
// checkpoint is fenced to (D7R §6.2): the marker's ActivationID and the
// committed mode generation.
type selectedActivationFence struct {
	ActivationID   string
	ModeGeneration uint64
}

// SetActivationFence installs the checkpoint fence provider. Production wires
// it from the Manager's committed activation; tests inject a deterministic
// one. Without a provider no checkpoint can be written.
func (m *SelectedRouteManager) SetActivationFence(fence func(role string) (selectedActivationFence, bool)) {
	m.activationFence = fence
}

// SetManagedRoleCheck installs the mode-authorization check (D8 §8.5).
// Production wires it to the Manager's managedSelectedRoles; tests inject an
// explicit allow-list. Without a check every mutation is denied.
func (m *SelectedRouteManager) SetManagedRoleCheck(check func(role string) bool) {
	m.managedRoleCheck = check
}

// requireManagedRole is the shared §8.5 pre-mutation gate for Ensure, Forget
// and FlushZone: a role the active marker does not manage can never reach the
// registry or the kernel.
func (m *SelectedRouteManager) requireManagedRole(role, zone, prefix string) error {
	if m.managedRoleCheck != nil && m.managedRoleCheck(role) {
		return nil
	}
	return &SelectedRouteDeniedError{
		Role: role, Zone: zone, Prefix: prefix,
		Reason: "role is not managed by the active schema2 marker",
	}
}

// selectedRouteMutationDeadline bounds every controller route mutation
// inside the core (08D1 §31.20): it deliberately expires before Leshy's
// 5-second call timeout so the client sees a typed failure instead of
// abandoning a still-running controller operation.
const selectedRouteMutationDeadline = 4 * time.Second

// selectedFailpoint is the unit-test interruption seam (08D1 §22.1): a hook
// error simulates a process interruption at a named durable boundary
// without compensating rollback. Production constructors set nil.
type selectedFailpoint func(point, role string, prefix netip.Prefix) error

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
	m.attachObservationLocked()
}

// attachObservationLocked installs the §31.9 sequence source when the
// platform implements SelectedRouteObservation.
func (m *SelectedRouteManager) attachObservationLocked() {
	if observation, ok := m.routes.(platform.SelectedRouteObservation); ok {
		m.observation = observation
	}
}

// beginFlight takes one in-flight slot and (re)opens the zero signal.
func (m *SelectedRouteManager) beginFlight(state *selectedRoleState) {
	if state.inFlight == 0 {
		state.inFlightZero = make(chan struct{})
	}
	state.inFlight++
}

// endFlight releases one in-flight slot, closing the zero signal exactly
// once when the count reaches zero.
func (m *SelectedRouteManager) endFlight(state *selectedRoleState) {
	state.inFlight--
	if state.inFlight == 0 {
		close(state.inFlightZero)
	}
	m.cond.Broadcast()
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
	// The controller route-mutation deadline (08D1 §31.20): 4 seconds inside
	// the core, before Leshy's own 5-second outer timeout.
	ctx, cancel := context.WithTimeout(ctx, selectedRouteMutationDeadline)
	defer cancel()
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
		result := LeshyRouteResult{
			Zone:   intent.Zone,
			Prefix: intent.Prefix,
			State:  "denied",
		}
		return result, &SelectedRouteDeniedError{
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

	// D8 §8.5: local mode is never a Kikimora selected writer — the role must
	// be authorized by the active schema2 marker before any mutation.
	if err := m.requireManagedRole(role, intent.Zone, prefix.String()); err != nil {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return result, err
	}

	// D7R §7.1: route mutations deny while a leshy-mode transition gate is
	// active — config/marker/generation are changing underneath.
	if m.owner != nil && m.owner.modeTransitionGateActive() {
		m.countRoleStat(role, func(s *selectedRoleState) { s.denials.Add(1) })
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: "a leshy-mode transition is in flight",
		}
	}

	// §31.11: the route authority gate, read before the manager mutex (the
	// managed-ness lives behind the owner's lock). A managed role's Ensure
	// stays retryable-denied with zero mutation until the infrastructure
	// and the initial stable observation are coherent.
	isManaged := m.owner != nil && m.owner.managedSelected(role)
	authorityReady := m.authorityReady.Load()
	if isManaged && !authorityReady {
		reason := "route authority is initializing"
		if m.reconciler != nil {
			if why := m.reconciler.authorityErrorText(); why != "" {
				reason = why
			}
		}
		return result, &SelectedRouteDeniedError{
			Role: role, Zone: intent.Zone, Prefix: prefix.String(),
			Reason: reason,
		}
	}

	m.mu.Lock()
	state := m.roleState(role)
	state.ensures.Add(1)
	// (3)+(4) a frozen role denies new intents (they must wait for
	// Unfreeze rather than race the recovery).
	if state.frozen {
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
	// (8) the in-flight counter is what Freeze waits on (§31.20 channel
	// semantics).
	m.beginFlight(state)
	generation := state.routeGeneration
	toadGeneration := runtime.ToadGeneration
	m.mu.Unlock()

	// Ensure the destination is registered before any kernel mutation.
	source := routing.SelectedDNS
	if intent.Source == LeshyRouteStatic {
		source = routing.SelectedStatic
	}
	state.applyMu.Lock()
	fastPath, ensureErr := m.applySelectedRouteCanonical(ctx, role, intent.Zone, prefix, source, ifIndex, iface, generation, toadGeneration)
	state.applyMu.Unlock()
	result.Generation = generation
	if ensureErr == nil {
		if fastPath {
			// The exact row was already verified: zero I/O, not a mutation.
			// The wire state is already-applied (§5.1).
			state.duplicateHits.Add(1)
			result.State = "already-applied"
		} else {
			state.kernelMutations.Add(1)
			result.State = "applied"
		}
	}

	m.mu.Lock()
	m.endFlight(state)
	m.mu.Unlock()

	return result, ensureErr
}

// applySelectedRoute runs Reserve + Pending checkpoint, the kernel
// reconciliation with its exact read-back, MarkActive + Active checkpoint
// (08D1 section 5.1; the verification-token fast path lands with K13). A
// failure after the Reserve keeps the Pending ownership record so recovery
// can park the destination.
// Freeze closes the role's selected-route surface for recovery: new Ensure
// calls are denied and the call waits on the context-aware zero-in-flight
// signal (08D1 §31.20). A timeout or cancelled context leaves frozen=true —
// recovery never reopens admission merely because it could not drain — and
// a later retry may observe the eventual zero. It returns the stable
// ownership set and the current generation.
func (m *SelectedRouteManager) Freeze(ctx context.Context, role string) ([]routing.SelectedRoute, uint64, error) {
	m.mu.Lock()
	state := m.roleState(role)
	state.frozen = true
	zero := state.inFlightZero
	m.mu.Unlock()
	if zero != nil {
		select {
		case <-zero:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registry.Snapshot(role), state.routeGeneration, nil
}

// ClearVerification drops the role's zero-I/O tokens: native invalidations,
// observer loss and recovery Freeze all use it (§5.2).
func (m *SelectedRouteManager) ClearVerification(role string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.roles[role]; ok {
		state.verification = nil
	}
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
	// §40.1/§47.3: every mutation is the canonical transaction under the
	// role's applyMu — no direct Reserve/MarkApplying/MarkActive composition.
	state := m.roleState(role)
	state.applyMu.Lock()
	defer state.applyMu.Unlock()
	for _, entry := range m.registry.Snapshot(role) {
		if entry.Source == routing.SelectedDNS && !routing.IsHostPrefix(entry.Prefix) {
			continue
		}
		if entry.State == routing.SelectedRetiring {
			// Retirement owns the record; restore never re-applies it.
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
		if entry.Park == nil && entry.State == routing.SelectedActive && entry.Owned != nil &&
			*entry.Owned == entry.Desired && entry.Desired.IfIndex == ifIndex {
			// Already exactly verified on this interface with no park
			// transaction in flight: a re-apply would only churn. A record
			// carrying a park must fall through: the managed cycle's
			// WithdrawSelected removed the exact row by design and the §7.5
			// park cleanup cannot prove restoration until it is back.
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
		if _, err := m.reconcileRecordLocked(ctx, role, entry.Prefix, "restore"); err != nil {
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
	// D7R §6.2: every checkpoint write carries the active activation fence.
	// A managed write without a valid fence is an error — the fence is what
	// makes an old activation checkpoint unrestorable after a Disable +
	// re-Enable of the same role/zone.
	fence, fenced := m.activationFenceFor(role)
	if !fenced {
		return fmt.Errorf("role %q has no active managed activation fence; refusing to write an unfenced checkpoint", role)
	}
	checkpoint.ActivationID = fence.ActivationID
	checkpoint.ModeGeneration = fence.ModeGeneration
	return routing.WriteSelectedRoutesCheckpoint(filepath.Join(dir, "selected-routes.json"), checkpoint)
}

// activationFenceFor resolves the active fence through the provider; a nil
// provider denies every write.
func (m *SelectedRouteManager) activationFenceFor(role string) (selectedActivationFence, bool) {
	if m.activationFence == nil {
		return selectedActivationFence{}, false
	}
	return m.activationFence(role)
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
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		nil,
		roleRuntime,
		zoneRoles,
		stateDir,
	)
	// D7R §6.2: the production fence comes from the Manager's committed
	// activation — exactly the state the marker recorded and the mode
	// transaction maintains.
	manager.SetActivationFence(func(role string) (selectedActivationFence, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !m.managedSelectedRoles[role] || m.managedActivationID == "" || m.modeGeneration == 0 {
			return selectedActivationFence{}, false
		}
		return selectedActivationFence{ActivationID: m.managedActivationID, ModeGeneration: m.modeGeneration}, true
	})
	// D8 §8.5: the mode authorization follows the same marker-managed state.
	manager.SetManagedRoleCheck(func(role string) bool {
		return m.managedSelected(role)
	})
	manager.owner = m
	return manager
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
	Schema         int      `json:"schema"`
	Socket         string   `json:"socket"`
	CoreVersion    string   `json:"core_version"`
	ManagedZones   []string `json:"managed_zones"`
	ActivationID   string   `json:"activation_id"`
	ModeGeneration uint64   `json:"mode_generation"`
}

// managedModeMarkerV1 is the pre-schema2 record (08D1 plan D1). It is parsed
// only by upgrade detection; the runtime never applies it as managed
// authority, because it carries neither an activation id nor a committed
// generation.
type managedModeMarkerV1 struct {
	Schema       int      `json:"schema"`
	Socket       string   `json:"socket"`
	CoreVersion  string   `json:"core_version"`
	ManagedZones []string `json:"managed_zones"`
}

const (
	managedModeMarkerSchema       = 2
	managedModeMarkerSchemaLegacy = 1
)

// Validate enforces the schema2 marker contract: a managed socket, canonical
// sorted unique zone names, a 128-bit lowercase-hex activation id and a
// positive committed generation.
func (m *managedModeMarker) Validate() error {
	if m.Schema != managedModeMarkerSchema {
		return fmt.Errorf("managed marker schema %d is not %d", m.Schema, managedModeMarkerSchema)
	}
	if m.Socket == "" {
		return fmt.Errorf("managed marker has an empty socket")
	}
	if !lowerHex32.MatchString(m.ActivationID) {
		return fmt.Errorf("managed marker requires a 32 lowercase-hex activation id")
	}
	if m.ModeGeneration == 0 {
		return fmt.Errorf("managed marker requires a positive committed generation")
	}
	previous := ""
	seen := make(map[string]bool, len(m.ManagedZones))
	for i, zone := range m.ManagedZones {
		if zone == "" {
			return fmt.Errorf("managed marker has an empty managed zone at %d", i)
		}
		if seen[zone] {
			return fmt.Errorf("managed marker records zone %q twice", zone)
		}
		if i > 0 && zone <= previous {
			return fmt.Errorf("managed marker zones must be canonical sorted unique names")
		}
		seen[zone] = true
		previous = zone
	}
	return nil
}

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

	activationID, err := m.commitManagedActivation(socketPath, zones)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.managedActivationID = activationID
	m.activationIDFingerprint = activationIDFingerprint(activationID)
	m.mu.Unlock()
	return m.RegenerateLeshyConfig()
}

// commitManagedActivation writes the schema2 marker for one activation and
// commits the monotonic generation it carries. It is the single place that
// owns the marker/generation pair; the journaled transaction wraps it once the
// privileged API lands (plan D5).
func (m *Manager) commitManagedActivation(socket string, zones []string) (string, error) {
	if m.stateDir == "" {
		return "", fmt.Errorf("managed activation requires the core state directory")
	}
	from, err := ReadCommittedModeGeneration(m.stateDir)
	if err != nil {
		return "", err
	}
	if from == ^uint64(0) {
		return "", fmt.Errorf("mode generation overflow: committed generation is MaxUint64")
	}
	activationID, err := NewActivationID()
	if err != nil {
		return "", err
	}
	to := from + 1
	if err := m.writeManagedMarker(socket, zones, activationID, to); err != nil {
		return "", err
	}
	if err := WriteCommittedModeGeneration(m.stateDir, to); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.modeGeneration = to
	m.mu.Unlock()
	return activationID, nil
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

func (m *Manager) writeManagedMarker(socket string, zones []string, activationID string, generation uint64) error {
	path := m.managedMarkerPath()
	if path == "" {
		return fmt.Errorf("the core has no configuration directory")
	}
	// The zones arrive already canonical from the strict zone authority
	// (D10R-1, P0-D2): the marker copies and validates them but never runs an
	// in-place pseudo-deduplication that could leave empty zone slots and
	// turn a live Enable failure into a recovered unusable managed mode. The
	// copy stays non-nil so a zero-zone install serializes [] and not null.
	copied := make([]string, len(zones))
	copy(copied, zones)
	marker := managedModeMarker{
		Schema:         managedModeMarkerSchema,
		Socket:         socket,
		CoreVersion:    BuildVersion,
		ManagedZones:   copied,
		ActivationID:   activationID,
		ModeGeneration: generation,
	}
	if err := marker.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid managed marker: %w", err)
	}
	return writeAtomicJSON(path, marker)
}

func (m *Manager) clearManagedMarker() error {
	path := m.managedMarkerPath()
	if path == "" {
		return nil
	}
	// P0-D1: the Managed -> Local retirement is a durable transaction step.
	// os.Remove can succeed for the process while the directory update is
	// not yet on disk across a sudden reboot, resurrecting a managed marker
	// next to a committed local state. RemoveDurable syncs the parent
	// directory; a missing file stays a success.
	return fsutil.RemoveDurable(path)
}

// readManagedMarkerFile resolves the marker path and reads it strictly.
func (m *Manager) readManagedMarkerFile() (*managedModeMarker, bool, error) {
	path := m.managedMarkerPath()
	if path == "" {
		return nil, false, nil
	}
	return readManagedMarker(path)
}

// readManagedMarker reads the persisted mode record strictly (08D1 §2.3):
// a missing marker is (nil, false, nil); malformed JSON, an unsupported
// schema, an empty socket, or an unknown/duplicate managed zone is a
// startup error. Never silently fall back to local mode.
func readManagedMarker(path string) (*managedModeMarker, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read managed marker: %w", err)
	}
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, true, fmt.Errorf("managed marker %s is malformed: %w", path, err)
	}
	switch probe.Schema {
	case managedModeMarkerSchemaLegacy:
		// Upgrade detection only (plan D1): a schema1 marker is never applied
		// as managed authority. It has no activation id and no committed
		// generation, so the fence that every v2 intent carries cannot exist.
		var legacy managedModeMarkerV1
		if err := json.Unmarshal(data, &legacy); err != nil {
			return nil, true, fmt.Errorf("legacy managed marker %s is malformed: %w", path, err)
		}
		return nil, true, fmt.Errorf(
			"managed marker %s uses the legacy schema 1 (%d zones, socket %q); re-enable managed mode to upgrade it to schema 2",
			path, len(legacy.ManagedZones), legacy.Socket)
	case managedModeMarkerSchema:
		// fall through to the strict schema2 parse below
	default:
		return nil, true, fmt.Errorf("managed marker %s has unsupported schema %d", path, probe.Schema)
	}
	var marker managedModeMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, true, fmt.Errorf("managed marker %s is malformed: %w", path, err)
	}
	if err := marker.Validate(); err != nil {
		return nil, true, fmt.Errorf("managed marker %s is invalid: %w", path, err)
	}
	return &marker, true, nil
}

// applyManagedMarker arms the managed recovery sequence in memory for every
// role whose zone the marker recorded (§2.3: capability is support, not
// activation; Ensure/Forget/Flush require the validated marker role
// activation through managedSelected).
func (m *Manager) applyManagedMarker(marker *managedModeMarker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.managedSocket = marker.Socket
	m.managedActivationID = marker.ActivationID
	m.modeGeneration = marker.ModeGeneration
	// P1-1: a managed restart retains the activation fingerprint; later
	// diagnostics must never replace a valid one with an empty value.
	m.activationIDFingerprint = activationIDFingerprint(marker.ActivationID)
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
}

// restoreManagedCheckpoints performs the §2.4 startup steps 6-8 for every
// marker-managed role: read <StateDir>/selected-routes.json (missing is an
// empty role), restore valid schema-2 checkpoints all-or-nothing, corrupt
// or unsupported files fail core startup, seed
// routeGeneration = checkpoint.RouteGeneration + 1 (minimum 1), and restore
// no verification token. A non-empty checkpoint freezes the role with
// restorePending until CompleteStartupRestore or normal recovery.
//
// D7R §6.3: the restore path receives the committed marker and requires the
// checkpoint to be fenced to exactly that activation — activation id, mode
// generation, runtime platform, configured role and the marker's zones.
// Mismatch is a startup failure before any kernel mutation (P0-4).
func (m *Manager) restoreManagedCheckpoints(marker *managedModeMarker) error {
	if m.selectedRoutes == nil || marker == nil {
		return nil
	}
	m.mu.Lock()
	managed := make(map[string]bool, len(m.managedSelectedRoles))
	zones := make(map[string]string, len(m.roles))
	for name, on := range m.managedSelectedRoles {
		managed[name] = on
	}
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zones[name] = r.cfg.EffectiveEndpointPolicy().Zone
	}
	m.mu.Unlock()
	for name, on := range managed {
		if !on {
			continue
		}
		dir, ok := m.selectedRoutes.stateDir(name)
		if !ok || dir == "" {
			continue
		}
		path := filepath.Join(dir, "selected-routes.json")
		checkpoint, err := routing.ReadSelectedRoutesCheckpoint(path)
		if errors.Is(err, fs.ErrNotExist) {
			// Missing: an empty role (§2.4).
			continue
		}
		if err != nil {
			return fmt.Errorf("managed checkpoint %s for role %q: %w", path, name, err)
		}
		// Platform fencing (§31.26): a copied checkpoint from another OS
		// must fail startup before any mutation.
		if checkpoint.Platform != routing.RuntimeCheckpointPlatform() {
			return fmt.Errorf("managed checkpoint %s platform %q does not match this runtime", path, checkpoint.Platform)
		}
		// The activation fence: a checkpoint from a previous activation of
		// the same socket/zone must never restore (P0-4).
		if checkpoint.ActivationID != marker.ActivationID {
			return fmt.Errorf(
				"managed checkpoint %s activation %q does not match the committed marker activation %q",
				path, checkpoint.ActivationID, marker.ActivationID)
		}
		if checkpoint.ModeGeneration != marker.ModeGeneration {
			return fmt.Errorf(
				"managed checkpoint %s mode generation %d does not match the committed marker generation %d",
				path, checkpoint.ModeGeneration, marker.ModeGeneration)
		}
		if err := routing.ValidateSelectedRoutesCheckpoint(checkpoint, name, zones[name]); err != nil {
			return fmt.Errorf("managed checkpoint %s for role %q: %w", path, name, err)
		}
		if err := m.selectedRoutes.registry.RestoreRole(checkpoint); err != nil {
			return fmt.Errorf("restore managed checkpoint %s for role %q: %w", path, name, err)
		}
		// The seeded startup route generation (§2.4 step 7).
		m.selectedRoutes.mu.Lock()
		state := m.selectedRoutes.roleState(name)
		if seeded := checkpoint.RouteGeneration + 1; seeded > state.routeGeneration {
			state.routeGeneration = seeded
		}
		// A non-empty checkpoint enters the startup-frozen state (§2.5).
		if len(checkpoint.Entries) > 0 {
			state.frozen = true
			state.restorePending = true
		}
		// No verification token is restored (§0.2/§5.2).
		state.verification = nil
		m.selectedRoutes.mu.Unlock()
	}
	return nil
}

// CompleteStartupRestore clears the startup freeze without another
// generation increment only when every condition of §2.5 holds: every
// non-Retiring record is Active with a current verification token for the
// seeded startup generation, Park == nil for every record, and no Retiring
// record remains. The selected-route reconciler (§10) owns the gate.
func (m *SelectedRouteManager) CompleteStartupRestore(role string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.roles[role]
	if !ok {
		return false
	}
	generation := state.routeGeneration
	for _, entry := range m.registry.Snapshot(role) {
		if entry.State == routing.SelectedRetiring {
			return false
		}
		if entry.State != routing.SelectedActive || entry.Owned == nil || *entry.Owned != entry.Desired {
			return false
		}
		if entry.Park != nil {
			return false
		}
		token, has := state.verification[entry.Prefix]
		if !has || token.Identity != entry.Desired ||
			token.ToadGeneration != entry.ToadGeneration ||
			token.RouteGeneration != generation {
			return false
		}
	}
	state.frozen = false
	state.restorePending = false
	m.cond.Broadcast()
	return true
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
// routeAuthorityReadyLocked reports the §31.11 gate. Callers hold m.mu.
func (m *Manager) routeAuthorityReadyLocked() bool {
	if m.selectedRoutes == nil {
		return false
	}
	return m.selectedRoutes.authorityReady.Load()
}

// routeAuthorityErrorLocked reports the last gate error. Callers hold m.mu.
func (m *Manager) routeAuthorityErrorLocked() string {
	if m.selectedRoutes == nil || m.selectedRoutes.reconciler == nil {
		return ""
	}
	return m.selectedRoutes.reconciler.authorityErrorText()
}

// modeTransitionNameLocked reports the transition journal phase for
// snapshots (D9 §9.2): none | prepared | config_written | marker_written.
// Callers hold m.mu.
func (m *Manager) modeTransitionNameLocked() string {
	if m.modeTransitionInFlight {
		if m.modeTransitionPhase != "" {
			return m.modeTransitionPhase
		}
		return string(ManagedTransitionPrepared)
	}
	return "none"
}

// modeTransitionIDFingerprintLocked reports the in-flight transition id
// fingerprint; it survives until the journal is durably removed. Callers
// hold m.mu.
func (m *Manager) modeTransitionIDFingerprintLocked() string {
	if !m.modeTransitionInFlight {
		return ""
	}
	return m.modeTransitionIDFingerprint
}

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
	DriftRepairs    uint64 `json:"drift_repairs"`
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
	stats.DriftRepairs = m.driftRepairs.Load()
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
