package control

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// The committed managed-mode authority (08D1 post-D7 audit §5): the runtime
// never infers the mode from whichever of config/marker happens to exist and
// never trusts a marker snapshot taken before transition recovery. The
// committed state is read fresh from disk, after the journal recovery, and
// every consumer — the auth rule, the checkpoint restore, the config gate —
// works on that one descriptor.

// committedManagedState is the immutable post-recovery committed-mode
// descriptor (packet §5.2). Managed is true exactly when a schema2 marker
// exists whose generation equals the committed counter; Marker is nil for a
// local install.
type committedManagedState struct {
	Generation uint64
	Managed    bool
	Marker     *managedModeMarker
}

// LoadCommittedManagedState performs the fresh disk read of the committed
// mode (packet §5.2). It must run only after RecoverManagedModeTransition:
//
//	assert transition journal absent
//	read committed generation
//	strict-read current marker
//
//	marker absent  -> local descriptor (generation = committed counter)
//	marker present -> require marker.ModeGeneration == committed generation
//
// No pre-recovery marker snapshot participates: a Local->Managed roll-forward
// that created a marker and a Managed->Local roll-forward that removed one are
// always seen at their post-recovery values (P0-1/P0-2).
func (m *Manager) LoadCommittedManagedState() (committedManagedState, error) {
	if m.stateDir != "" {
		if _, found, err := ReadTransitionJournal(m.stateDir); err != nil {
			return committedManagedState{}, err
		} else if found {
			return committedManagedState{}, fmt.Errorf(
				"a mode transition journal still exists; the committed mode loads only after transition recovery")
		}
		generation, err := ReadCommittedModeGeneration(m.stateDir)
		if err != nil {
			return committedManagedState{}, err
		}
		marker, found, err := m.readManagedMarkerFile()
		if err != nil {
			return committedManagedState{}, err
		}
		if !found {
			return committedManagedState{Generation: generation}, nil
		}
		if marker.ModeGeneration != generation {
			return committedManagedState{}, fmt.Errorf(
				"managed marker generation %d does not equal the committed generation %d; refusing the stale marker",
				marker.ModeGeneration, generation)
		}
		// readManagedMarker already validated the schema2 contract: socket,
		// 32-hex activation id, positive generation, canonical zones.
		return committedManagedState{Generation: generation, Managed: true, Marker: marker}, nil
	}
	// Development core without persistent state: no journal and no committed
	// counter exist. A marker without the counter cannot prove its generation,
	// so it fails closed instead of silently arming managed authority.
	_, found, err := m.readManagedMarkerFile()
	if err != nil {
		return committedManagedState{}, err
	}
	if found {
		return committedManagedState{}, fmt.Errorf(
			"managed marker %s exists without a core state directory; the schema2 fence cannot be verified",
			m.managedMarkerPath())
	}
	return committedManagedState{}, nil
}

// StartupManagedAuthority runs the production managed-mode startup sequence
// (packet §5.3), in order:
//
//	RecoverManagedModeTransition
//	LoadCommittedManagedState
//	EnsurePrivilegedAuthKey(state.Managed)
//	RestoreCommittedManagedState(state)
//	RegenerateAndVerifyLeshyConfig(state)
//
// The caller may proceed to the route-authority proof, the reconciler and
// Serve only after it returns nil: every earlier failure must prevent
// readiness. With an empty stateDir (development core) the sequence skips the
// persistent authority and the config gate stays best-effort at the caller.
func (m *Manager) StartupManagedAuthority(stateDir string) (committedManagedState, error) {
	m.SetStateDir(stateDir)
	// The runtime paths must be final from here on: transition recovery and
	// the committed-state restore below read/write the generated Leshy config.
	m.closeRuntimePaths()
	// PC8A §1.3: the strict policy load sits BEFORE transition recovery —
	// recovery itself renders the generated config and must never render from
	// permissive fallback policy. A corrupt bypass.conf or role-ipv6.json
	// fails startup closed.
	if err := m.loadRuntimePolicySources(); err != nil {
		return committedManagedState{}, fmt.Errorf("load the committed policy sources: %w", err)
	}
	if _, err := m.RecoverManagedModeTransition(); err != nil {
		return committedManagedState{}, err
	}
	state, err := m.LoadCommittedManagedState()
	if err != nil {
		return committedManagedState{}, err
	}
	if stateDir != "" {
		key, err := EnsurePrivilegedAuthKey(stateDir, state.Managed)
		if err != nil {
			return state, err
		}
		m.SetPrivilegedAuth(key)
	}
	if err := m.RestoreCommittedManagedState(state); err != nil {
		return state, fmt.Errorf("restore committed managed state: %w", err)
	}
	// R8A §1.7 (2026-11-17 residual audit): extend the committed marker for
	// roles created since the marker was captured — add-only within one
	// activation, same activation ID and generation.
	if state.Managed && state.Marker != nil {
		state, err = m.extendManagedMarkerForCurrentRoles(state)
		if err != nil {
			return state, fmt.Errorf("extend the managed marker: %w", err)
		}
	}
	// R8B §2.8: the post-recovery descriptor is retained so the all-off
	// startup drain's config re-render can re-verify the exact marker
	// identity (CompleteStartupBypassDrain).
	m.mu.Lock()
	m.startupCommitted = state
	m.mu.Unlock()
	if stateDir == "" {
		return state, nil
	}
	if err := m.RegenerateAndVerifyLeshyConfig(state); err != nil {
		return state, err
	}
	return state, nil
}

// RegenerateAndVerifyLeshyConfig is the fatal startup consistency gate
// (packet §5.4): regenerate the router configuration from the restored
// committed mode, then read the durable bytes back and require their
// semantics. Any render, write, read-back or verification failure returns an
// error before the reconciler starts and before Serve binds a socket.
//
// For a managed install the read-back server section must carry exactly the
// marker identity; for a local install every managed-only field must be
// absent.
func (m *Manager) RegenerateAndVerifyLeshyConfig(state committedManagedState) error {
	if err := m.RegenerateLeshyConfig(); err != nil {
		return err
	}
	server, err := config.ReadLeshyServerStrict(m.leshyConfigFile())
	if err != nil {
		return fmt.Errorf("read back the generated Leshy config: %w", err)
	}
	if !state.Managed {
		if server.RoutingBackend != "" || server.KikimoraSocket != "" || server.KikimoraAuthFile != "" ||
			server.KikimoraActivationID != "" || server.KikimoraModeGeneration != 0 {
			return fmt.Errorf(
				"the generated Leshy config still carries managed-only fields for a local install (routing_backend %q, socket %q)",
				server.RoutingBackend, server.KikimoraSocket)
		}
		return nil
	}
	marker := state.Marker
	if marker == nil {
		return fmt.Errorf("the committed managed descriptor carries no marker")
	}
	if server.RoutingBackend != "kikimora" {
		return fmt.Errorf("generated Leshy config routing_backend %q is not \"kikimora\"", server.RoutingBackend)
	}
	if server.KikimoraSocket != marker.Socket {
		return fmt.Errorf("generated Leshy config socket %q does not match the marker socket %q",
			server.KikimoraSocket, marker.Socket)
	}
	if server.KikimoraAuthFile != PrivilegedAuthKeyPath(m.stateDir) {
		return fmt.Errorf("generated Leshy config auth file does not match the core credential")
	}
	if server.KikimoraActivationID != marker.ActivationID {
		return fmt.Errorf("generated Leshy config activation id does not match the marker activation id")
	}
	if server.KikimoraModeGeneration != marker.ModeGeneration {
		return fmt.Errorf("generated Leshy config mode generation %d does not match the marker generation %d",
			server.KikimoraModeGeneration, marker.ModeGeneration)
	}
	if server.WriterLock != config.LeshyWriterLockPath() {
		return fmt.Errorf("generated Leshy config writer lock %q does not match the platform writer lock %q",
			server.WriterLock, config.LeshyWriterLockPath())
	}
	return nil
}

// ValidateStartupDesiredPolicy validates the persisted desired/default
// policy against the loaded role/profile and the strict bypass cache BEFORE
// the route-authority reconciler starts (R8B §2.8, 2026-11-17 residual
// audit). No process or kernel mutation. A contradiction (non-direct bypass
// owner desired=false while a sibling stays desired, desired=true outside
// the resolved active profile) fails startup closed.
//
// An explicit all-off intent (or a missing desired.json, which runServe
// synthesizes as all-off) is NOT a contradiction: the configured owner
// stays dormant and the effective runtime bypass mode becomes direct
// (PrepareStartupRoutingIntent applies that decision).
func (m *Manager) ValidateStartupDesiredPolicy(desired PersistedDesiredState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile := desired.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	members, ok := m.profileToRoles[profile]
	if !ok {
		// The same deterministic fallback RestoreDesiredState applies.
		members, ok = m.profileToRoles["default"]
		if !ok {
			return fmt.Errorf("persisted active profile %q is not configured and no default profile exists", profile)
		}
		profile = "default"
	}
	memberSet := make(map[string]bool, len(members))
	for _, name := range members {
		memberSet[name] = true
	}

	bypass := m.bypassMode
	if !m.bypassModeLoaded {
		strict, err := config.ReadBypassModeStrict(m.bypassPath())
		if err != nil {
			return fmt.Errorf("read the committed bypass decision: %w", err)
		}
		bypass = strict
	}

	// R8B §2.8: missing intent (nil Roles) or every configured role
	// desired=false is an explicit all-off. Missing desired state is safe
	// all-off, never permission to activate the configured default owner.
	allOff := true
	for _, on := range desired.Roles {
		if on {
			allOff = false
			break
		}
	}

	// A non-direct bypass owner MUST be desired=true inside the resolved
	// active profile, unless the intent is all-off (dormant owner).
	if bypass != "" && bypass != config.BypassDirect {
		if _, ok := m.roles[bypass]; !ok {
			return fmt.Errorf("the committed bypass owner %q is not a loaded role", bypass)
		}
		if !allOff {
			if !memberSet[bypass] {
				return fmt.Errorf(
					"persisted policy inconsistency: the bypass owner %q is not part of the resolved active profile %q",
					bypass, profile)
			}
			if !desired.Roles[bypass] {
				return fmt.Errorf(
					"persisted policy inconsistency: the bypass owner %q is desired=false while sibling roles stay desired; the durable default-route policy contradicts the operator intent",
					bypass)
			}
		}
	}

	// A desired=true role outside the resolved active profile is stale
	// persisted state, not a valid manual override (R8B §2.8).
	for name, on := range desired.Roles {
		if on && !memberSet[name] {
			return fmt.Errorf(
				"persisted policy inconsistency: role %q is desired=true outside the resolved active profile %q",
				name, profile)
		}
	}
	return nil
}

// PrepareStartupRoutingIntent applies the R8B §2.8 startup split between
// read-only intent validation and process startup: an explicit all-off
// desired policy (or a missing desired.json, synthesized as all-off by the
// caller) keeps the configured bypass.conf owner DORMANT while the effective
// runtime bypass mode becomes direct. The operator configuration in
// bypass.conf is never rewritten — the dormancy is re-derived from the
// desired policy on every startup.
//
// No process start, no kernel mutation. The synthetic-bypass drain itself
// needs the selected-route writer and runs later
// (CompleteStartupBypassDrain); this only records the decision and reclassifies
// restored bypass evidence from handover-resume toward drain.
func (m *Manager) PrepareStartupRoutingIntent(desired PersistedDesiredState) error {
	m.policyMu.Lock()
	defer m.policyMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	allOff := true
	for _, on := range desired.Roles {
		if on {
			allOff = false
			break
		}
	}
	if !allOff {
		return nil
	}
	owner := m.bypassMode
	if owner == "" || owner == config.BypassDirect {
		return nil
	}
	m.configuredBypassOwner = owner
	m.bypassMode = config.BypassDirect
	m.startupBypassDrainOwner = owner
	m.desiredStateStatus = fmt.Sprintf(
		"all-off; the configured default-route owner %q stays dormant (bypass.conf unchanged)", owner)
	return nil
}

// CompleteStartupBypassDrain retires the dormant owner's restored synthetic
// bypass evidence before the route-authority reconciler starts (R8B §2.8:
// "restored bypass row/park in all-off → drained before route-authority
// Ready"). It needs only the selected-route writer — no process starts, no
// role enables. Every managed role's bypass-zone evidence drains (a crash
// can leave handover leftovers on any source role), the generated config is
// re-rendered from the effective-direct policy, re-verified against the
// committed marker and reloaded, and the pending-handover
// classification/quarantine from the checkpoint restore is cleared: the
// drain resolved it.
func (m *Manager) CompleteStartupBypassDrain(ctx context.Context) error {
	m.mu.Lock()
	owner := m.startupBypassDrainOwner
	committedManaged := m.startupCommitted.Managed
	managed := make([]string, 0, len(m.managedSelectedRoles))
	for name, on := range m.managedSelectedRoles {
		if on {
			managed = append(managed, name)
		}
	}
	m.mu.Unlock()
	if owner == "" {
		return nil
	}
	sort.Strings(managed)

	// Kernel/registry evidence exists only for a managed install: a local
	// install never owned synthetic bypass rows in this activation, but its
	// generated config still carries the dormant owner's zone and must be
	// re-rendered below.
	if committedManaged && m.selectedRoutes != nil {
		var errs []error
		for _, name := range managed {
			if len(m.selectedRoutes.RegistrySnapshot(name)) == 0 {
				continue
			}
			if err := m.selectedRoutes.FlushBypassOwner(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("drain the synthetic bypass evidence for %q: %w", name, err))
				continue
			}
			// A role whose registry is now empty has nothing left to resume:
			// clear the startup freeze (the recovery-driven path reaches the
			// same state through UnfreezeSelected, but a drained all-off role
			// never recovers — it is disabled).
			if len(m.selectedRoutes.RegistrySnapshot(name)) == 0 {
				m.selectedRoutes.CompleteStartupDrain(name)
			}
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}

	// Re-render the generated config from the effective-direct policy and
	// re-verify the committed marker identity: a generated config that still
	// carries the dormant owner's synthetic zone would resurrect the
	// catch-all on the next Leshy reload. The reload applies the new file to
	// a Leshy that may already run (transient restart).
	if err := m.RegenerateAndVerifyLeshyConfig(m.startupCommitted); err != nil {
		return fmt.Errorf("re-render the generated config after the all-off drain: %w", err)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		return fmt.Errorf("reload the router after the all-off drain: %w", err)
	}

	// The drain resolved every pending-handover classification the checkpoint
	// restore made: clear the fence and the quarantine so the initial
	// authority proof starts clean.
	m.mu.Lock()
	m.bypassRecoveryOwners = nil
	m.bypassTransition = bypassTransition{}
	m.startupBypassDrainOwner = ""
	m.clearPolicyAuthorityAfterProofLocked()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// extendManagedMarkerForCurrentRoles additively extends the committed
// schema2 marker's ManagedZones for roles created since the marker was
// captured (R8A §1.7, 2026-11-17 residual audit). Without this, a
// CreateTunnel'd role exists in the UI but managed selected-route intents
// for it are denied because the old marker never authorized its zone.
//
// The extension is add-only within one activation: removed-role zones may
// remain as inert tombstones until the next Managed→Local→Managed
// activation. The marker file is durably replaced with the union (same
// activation ID and generation) and the in-memory state is updated.
func (m *Manager) extendManagedMarkerForCurrentRoles(
	state committedManagedState,
) (committedManagedState, error) {
	m.mu.Lock()
	// Collect the canonical zone set of current loaded roles that can own
	// selected traffic.
	currentZones := make(map[string]string, len(m.roles)) // zone → role
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zone := r.cfg.EffectiveEndpointPolicy().Zone
		if zone == "" {
			continue
		}
		currentZones[zone] = name
	}
	m.mu.Unlock()

	missing := make([]string, 0)
	markerZones := make(map[string]bool, len(state.Marker.ManagedZones))
	for _, zone := range state.Marker.ManagedZones {
		markerZones[zone] = true
	}
	for zone := range currentZones {
		if !markerZones[zone] {
			missing = append(missing, zone)
		}
	}
	if len(missing) == 0 {
		return state, nil // no extension needed
	}
	sort.Strings(missing)

	// Acquire the writer lock and re-read the marker: prove the activation
	// and generation are unchanged, then durably write the union.
	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	m.mu.Lock()
	if m.managedActivationID != state.Marker.ActivationID || m.modeGeneration != state.Marker.ModeGeneration {
		m.mu.Unlock()
		return state, fmt.Errorf(
			"the committed marker changed during the extension; refusing to extend")
	}
	m.mu.Unlock()

	extended := append(append([]string(nil), state.Marker.ManagedZones...), missing...)
	sort.Strings(extended)

	if err := m.writeManagedMarker(state.Marker.Socket, extended, state.Marker.ActivationID, state.Marker.ModeGeneration); err != nil {
		return state, fmt.Errorf("write the extended managed marker: %w", err)
	}

	// Re-read and validate the extended marker.
	reread, found, err := m.readManagedMarkerFile()
	if err != nil || !found {
		return state, fmt.Errorf("re-read the extended managed marker: found=%v err=%v", found, err)
	}
	if reread.ModeGeneration != state.Marker.ModeGeneration || reread.ActivationID != state.Marker.ActivationID {
		return state, fmt.Errorf("the extended marker does not match the committed activation/generation")
	}

	// Update the committed state to carry the extended zone list.
	state.Marker.ManagedZones = extended
	return state, nil
}
