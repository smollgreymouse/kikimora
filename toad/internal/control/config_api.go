package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// loadRuntimePolicySources strict-reads the committed policy sources into the
// Manager caches (PC8A §1.3): bypass.conf and role-ipv6.json must parse, or
// startup fails closed before any transition recovery renders the generated
// config from permissive fallback policy. Caller holds no locks; the caller
// must invoke it before StartupManagedAuthority arms the mode gate.
func (m *Manager) loadRuntimePolicySources() error {
	bypass, err := config.ReadBypassModeStrict(m.bypassPath())
	if err != nil {
		return err
	}
	ipv6, err := config.ReadRoleIPv6SettingsStrict(m.routingDirectory())
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.bypassMode = bypass
	m.bypassModeLoaded = true
	m.roleIPv6 = make(map[string]bool, len(ipv6))
	for role, enabled := range ipv6 {
		m.roleIPv6[role] = enabled
	}
	m.roleIPv6Loaded = true
	m.mu.Unlock()
	return nil
}

// roleIPv6CacheLocked returns the committed IPv6 policy map. Callers hold m.mu.
func (m *Manager) roleIPv6CacheLocked() map[string]bool {
	out := make(map[string]bool, len(m.roleIPv6))
	for role, enabled := range m.roleIPv6 {
		out[role] = enabled
	}
	return out
}

// CreateTunnel writes a new toad configuration next to the ones the manager was started
// with and returns its path.
//
// The composition is fixed at construction, so the new tunnel appears in the snapshot only
// after the core restarts; the callers report that instead of pretending otherwise.
func (m *Manager) CreateTunnel(spec *config.TunnelSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("tunnel spec is required")
	}
	if m.configDir == "" {
		return "", fmt.Errorf("the core has no configuration directory")
	}
	return config.CreateTunnel(m.configDir, *spec)
}

// profileSettlement carries the fenced convergence a profile or membership
// mutation produced. The caller schedules it only after releasing policyMu:
// the policy lock is never held while a Toad process starts or stops.
type profileSettlement struct {
	epoch   uint64
	changed []string
	want    map[string]bool
}

func (s profileSettlement) schedule(m *Manager) {
	if len(s.changed) > 0 {
		m.scheduleProfileSettlement(s.epoch, s.changed, s.want)
	}
}

// SetTunnelProfiles rewrites which profiles an existing tunnel belongs to - the move path
// the UI uses when a role is dragged from one profile to another. The role TOML, the
// in-memory configuration and the desired-state composition commit as one policy
// transaction, so a failed call leaves disk, memory and desired state observationally
// unchanged (AUD1, 2026-11-16 post-e7ec9c2 audit §2.4). When the role is part of the
// running composition the snapshot follows at once, so the UI sees the new membership
// without a core restart. A role that only exists on disk (created after this core
// started) keeps its disk-only move and loads with it on the next start.
func (m *Manager) SetTunnelProfiles(name string, profiles []string) (string, error) {
	m.policyMu.Lock()
	path, settlement, err := m.setTunnelProfilesPolicyLocked(name, profiles)
	m.policyMu.Unlock()
	if err == nil {
		settlement.schedule(m)
	}
	return path, err
}

func (m *Manager) setTunnelProfilesPolicyLocked(name string, profiles []string) (string, profileSettlement, error) {
	if m.configDir == "" {
		return "", profileSettlement{}, fmt.Errorf("the core has no configuration directory")
	}
	if len(profiles) == 0 {
		return "", profileSettlement{}, fmt.Errorf("role %q needs at least one profile", name)
	}
	// Moving the role that carries the global default route out of the live
	// profile would leave the catch-all pointing at a disabled tunnel.
	m.mu.Lock()
	if r, ok := m.roles[name]; ok {
		mode := m.bypassModeLocked()
		if mode != "" && mode != config.BypassDirect && mode == name &&
			slices.Contains(r.cfg.Profiles, m.activeProfile) && !slices.Contains(profiles, m.activeProfile) {
			active := m.activeProfile
			m.mu.Unlock()
			return "", profileSettlement{}, fmt.Errorf("role %q carries the global default route in the active profile %q; set the default route to %q first", name, active, config.BypassDirect)
		}
	}
	m.mu.Unlock()

	tx, err := config.PrepareProfileComposition(m.configDir, config.ProfileCompositionPlan{
		Memberships: []config.ProfileMembership{{Role: name, Profiles: profiles}},
	})
	if err != nil {
		return "", profileSettlement{}, err
	}

	ctx := context.Background()
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		// A disk-only move: the role is not part of the running composition,
		// so there is no memory or desired state to reconcile.
		m.mu.Unlock()
		if err := tx.Commit(); err != nil {
			return "", profileSettlement{}, err
		}
		return filepath.Join(m.configDir, strings.TrimSpace(name)+".toml"), profileSettlement{}, nil
	}
	active := m.activeProfile
	oldProfiles := slices.Clone(r.cfg.Profiles)
	wasMember := slices.Contains(oldProfiles, active)
	m.mu.Unlock()

	if err := tx.Commit(); err != nil {
		return "", profileSettlement{}, err
	}

	m.mu.Lock()
	var settlement profileSettlement
	var applyErr error
	r, ok = m.roles[name]
	if ok {
		r.cfg.Profiles = profiles
		m.rebuildProfileMapLocked()
		if isMember := slices.Contains(profiles, active); wasMember != isMember {
			// Moving a role into or out of the live profile is a composition
			// change, not just a disk edit: the role must start or settle down
			// without a core restart (PROD1, 2026-11-16 audit §2.1).
			var changed []string
			var want map[string]bool
			var epoch uint64
			changed, want, epoch, applyErr = m.applyActiveProfileIntentLocked(ctx)
			settlement = profileSettlement{epoch: epoch, changed: changed, want: want}
		}
		if applyErr != nil {
			// applyActiveProfileIntentLocked already restored the desired
			// flags; restore the in-memory membership and the committed TOMLs
			// so no half-applied composition survives (AUD1 §2.4).
			r.cfg.Profiles = oldProfiles
			m.rebuildProfileMapLocked()
			m.bumpLocked()
			m.mu.Unlock()
			rollbackErr := tx.Rollback()
			return "", profileSettlement{}, errors.Join(
				fmt.Errorf("reconcile active profile %q: %w", active, applyErr), rollbackErr)
		}
	}
	m.bumpLocked()
	m.mu.Unlock()
	return filepath.Join(m.configDir, strings.TrimSpace(name)+".toml"), settlement, nil
}

// CreateProfile registers a profile and, when roles are given, makes them its members. It
// reports whether the name was new to the registry.
//
// A profile is an explicit set of roles, so a new one starts empty: cloning every role into
// it would silently duplicate the current profile instead of letting the operator choose.
// The name is registered in profiles.list, which is what lets an empty profile exist at all.
//
// The registry and the membership commit as one transaction: a failed membership edit
// must not leave a ghost declared profile behind (AUD1 §2.3/§2.4). Every requested role
// is validated before anything is written.
//
// No restart is involved: profiles are labels, not processes, so the snapshot lists the
// profile at once and SetActiveProfile accepts it.
func (m *Manager) CreateProfile(profile string, roles []string) (int, error) {
	if m.configDir == "" {
		return 0, fmt.Errorf("the core has no configuration directory")
	}
	m.policyMu.Lock()
	added, settlement, err := m.setProfileRolesPolicyLocked(context.Background(), profile, roles, true)
	m.policyMu.Unlock()
	// R8A §1.5: a failed call must never start/stop roles per the rejected
	// composition — schedule only a committed settlement.
	if err == nil {
		settlement.schedule(m)
	}
	return boolToInt(added), err
}

// SetProfileRoles replaces the profile's role set. Memberships are per role: a role that
// also belongs to other profiles keeps them, which is what lets one role serve several
// profiles. Every touched role TOML commits as one transaction together with the in-memory
// membership and the desired-state composition, so a failed call leaves the running
// composition untouched (AUD1 §2.4).
func (m *Manager) SetProfileRoles(profile string, roles []string) error {
	m.policyMu.Lock()
	_, settlement, err := m.setProfileRolesPolicyLocked(context.Background(), profile, roles, false)
	m.policyMu.Unlock()
	if err == nil {
		settlement.schedule(m)
	}
	return err
}

// membershipChange is one role whose profile list a transaction replaces.
type membershipChange struct {
	role     string
	profiles []string
	old      []string
}

// setProfileRolesPolicyLocked replaces the role set of one profile as one
// durable transaction: role TOMLs, the declared-profiles registry (when declare
// is set), in-memory membership and desired state commit together or not at all
// (AUD1, 2026-11-16 post-e7ec9c2 audit §2.4). The caller holds policyMu but not
// m.mu. declare registers the profile in profiles.list inside the same commit,
// which is how CreateProfile keeps a failed membership edit from leaving a
// ghost declared profile.
func (m *Manager) setProfileRolesPolicyLocked(ctx context.Context, profile string, roles []string, declare bool) (bool, profileSettlement, error) {
	name := strings.TrimSpace(profile)
	wanted := make(map[string]bool, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			return false, profileSettlement{}, fmt.Errorf("role name must not be empty")
		}
		wanted[role] = true
	}
	if name == "default" && (!declare || len(wanted) > 0) {
		return false, profileSettlement{}, fmt.Errorf("the default profile always contains every role")
	}

	var changes []membershipChange
	added := false
	m.mu.Lock()
	if _, ok := m.profileToRoles[name]; !ok && !declare {
		m.mu.Unlock()
		return false, profileSettlement{}, fmt.Errorf("unknown profile %q", name)
	}
	// Role validation happens before any file is written: a failed CreateProfile
	// must not leave a ghost declared profile (AUD1 §2.3).
	for role := range wanted {
		if _, ok := m.roles[role]; !ok {
			m.mu.Unlock()
			return false, profileSettlement{}, fmt.Errorf("unknown role %q", role)
		}
	}
	if name == m.activeProfile {
		// The bypass invariant is unconditional for the active profile
		// (PC8A §1.1): an empty active profile is legal only when the global
		// default route is direct, otherwise the live bypass owner would go
		// undesired while bypass.conf still names it.
		if err := m.requireBypassInProfileLocked(wanted); err != nil {
			m.mu.Unlock()
			return false, profileSettlement{}, err
		}
	}
	changes = make([]membershipChange, 0, len(m.roles))
	for roleName, r := range m.roles {
		has := slices.Contains(r.cfg.Profiles, name)
		switch {
		case wanted[roleName] && !has:
			changes = append(changes, membershipChange{
				role:     roleName,
				profiles: append(slices.Clone(r.cfg.Profiles), name),
				old:      slices.Clone(r.cfg.Profiles),
			})
		case !wanted[roleName] && has:
			next := make([]string, 0, len(r.cfg.Profiles))
			for _, p := range r.cfg.Profiles {
				if p != name {
					next = append(next, p)
				}
			}
			if len(next) == 0 {
				next = []string{"default"}
			}
			changes = append(changes, membershipChange{role: roleName, profiles: next, old: slices.Clone(r.cfg.Profiles)})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].role < changes[j].role })

	var plan config.ProfileCompositionPlan
	if len(changes) > 0 {
		memberships := make([]config.ProfileMembership, 0, len(changes))
		for _, change := range changes {
			memberships = append(memberships, config.ProfileMembership{Role: change.role, Profiles: change.profiles})
		}
		plan.Memberships = memberships
	}
	if declare {
		declared := slices.Clone(m.declaredProfiles)
		if !slices.Contains(declared, name) {
			declared = append(declared, name)
			added = true
		}
		plan.DeclaredProfiles = &declared
	}
	m.mu.Unlock()

	var tx *config.ProfileCompositionTxn
	// PC8A §1.4: the undo journal makes the multi-file edit crash-atomic.
	// The exact original desired.json image is captured when a
	// FileDesiredStateStore backs the manager, and the journal is durably
	// written BEFORE any policy mutation; it is removed only after the commit
	// point (successful desired-state persistence), so a journal at process
	// start always means "roll back".
	var undoFiles []config.ProfileUndoFile
	undoActive := false
	if m.configDir != "" && (len(plan.Memberships) > 0 || plan.DeclaredProfiles != nil) {
		prepared, err := config.PrepareProfileComposition(m.configDir, plan)
		if err != nil {
			return false, profileSettlement{}, err
		}
		// PC8A §1.4: the undo journal requires the hardened state root. A
		// development/test manager without a stateDir keeps the existing
		// in-process rollback and durable single-file writes (no cross-
		// directory desired authority to capture).
		if m.stateDir != "" {
			for _, snap := range prepared.Snapshots() {
				undoFiles = append(undoFiles, config.ProfileUndoFile{
					Kind:    "role",
					Name:    snap.Role,
					Existed: snap.Existed,
					Mode:    uint32(snap.Mode),
					Data:    snap.Data,
				})
			}
			if prepared.Registry() != nil {
				undoFiles = append(undoFiles, config.ProfileUndoFile{
					Kind:    "registry",
					Existed: prepared.Registry().Existed,
					Mode:    uint32(prepared.Registry().Mode),
					Data:    prepared.Registry().Data,
				})
			}
			desiredPath := filepath.Join(m.stateDir, "desired.json")
			snap, snapErr := snapshotPolicyFile(desiredPath)
			if snapErr != nil {
				// desired.json unreadable: fail before any mutation (PC8A §1.2).
				return false, profileSettlement{}, snapErr
			}
			undoFiles = append(undoFiles, config.ProfileUndoFile{
				Kind:    "desired",
				Existed: snap.Existed,
				Mode:    uint32(snap.Mode),
				Data:    snap.Data,
			})
			if err := config.WriteProfileCompositionUndo(m.stateDir, config.ProfileCompositionUndo{Files: undoFiles}); err != nil {
				return false, profileSettlement{}, fmt.Errorf("write the profile composition undo journal: %w", err)
			}
			undoActive = true
		}
		if err := prepared.Commit(); err != nil {
			if rollbackErr := prepared.Rollback(); rollbackErr != nil {
				// The rollback is unproved: keep the journal for startup
				// recovery and quarantine the policy authority.
				m.mu.Lock()
				m.quarantinePolicyAuthorityLocked("the profile composition rollback is unproved: " + rollbackErr.Error())
				m.mu.Unlock()
			} else if undoActive {
				_ = config.RemoveProfileCompositionUndo(m.stateDir)
			}
			return false, profileSettlement{}, err
		}
		tx = prepared
	}

	m.mu.Lock()
	var settlement profileSettlement
	var applyErr error
	if declare && !slices.Contains(m.declaredProfiles, name) {
		m.declaredProfiles = append(m.declaredProfiles, name)
	}
	for _, change := range changes {
		if r, ok := m.roles[change.role]; ok {
			r.cfg.Profiles = change.profiles
		}
	}
	m.rebuildProfileMapLocked()
	if name == m.activeProfile {
		// Editing the live profile is the same latest-intent operation as a
		// switch: removed members stop, added members start, and any older
		// settlement becomes stale (PROD1, 2026-11-16 audit §2.1).
		var changed []string
		var want map[string]bool
		var epoch uint64
		changed, want, epoch, applyErr = m.applyActiveProfileIntentLocked(ctx)
		settlement = profileSettlement{epoch: epoch, changed: changed, want: want}
	}
	if applyErr != nil {
		// applyActiveProfileIntentLocked already restored the desired flags;
		// restore the in-memory membership, the declared registry and the
		// committed files so no half-applied composition survives (AUD1 §2.4).
		for _, change := range changes {
			if r, ok := m.roles[change.role]; ok {
				r.cfg.Profiles = change.old
			}
		}
		if added {
			m.declaredProfiles = slices.DeleteFunc(m.declaredProfiles, func(p string) bool { return p == name })
		}
		m.rebuildProfileMapLocked()
		m.bumpLocked()
		m.mu.Unlock()
		rollbackErr := errors.New("no transaction")
		if tx != nil {
			rollbackErr = tx.Rollback()
		}
		if rollbackErr != nil {
			// PC8A §1.4: the rollback is unproved — keep the undo journal for
			// startup recovery and quarantine the policy authority.
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked("the profile composition rollback is unproved: " + rollbackErr.Error())
			m.mu.Unlock()
		} else if undoActive {
			if err := config.RemoveProfileCompositionUndo(m.stateDir); err != nil {
				// Rollback proven but the journal cannot be consumed: a
				// startup would undo an already-rolled-back state to the same
				// original images, which is idempotent — still, surface it.
				rollbackErr = fmt.Errorf("consume the profile composition undo journal: %w", err)
			}
		}
		return false, profileSettlement{}, errors.Join(
			fmt.Errorf("reconcile active profile %q: %w", name, applyErr), rollbackErr)
	}
	m.bumpLocked()
	m.mu.Unlock()
	// R8A §1.5: the durable LINEARIZATION POINT — the config files are
	// applied, the desired state is persisted and the memory is rebuilt.
	// Durably rewrite the journal phase to committed BEFORE exposing success:
	// from here the transaction effects are final and a startup that finds
	// the journal only cleans it up (never rolls the live composition back).
	if undoActive {
		if err := config.MarkProfileCompositionCommitted(m.stateDir); err != nil {
			return false, settlement, fmt.Errorf("mark the profile composition committed: %w", err)
		}
	}
	return added, settlement, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// RemoveTunnel deletes a toad configuration next to the loaded ones. Like CreateTunnel it
// only changes files on disk; the running composition drops the role after a restart.
func (m *Manager) RemoveTunnel(name string) error {
	if m.configDir == "" {
		return fmt.Errorf("the core has no configuration directory")
	}
	return config.RemoveTunnel(m.configDir, name)
}

// routingDir is where the Leshy lists live. Kept on the manager so tests can point it at
// a temporary directory.
func (m *Manager) routingDirectory() string {
	if m.routingDir != "" {
		return m.routingDir
	}
	return config.DefaultRoutingDir()
}

// RoutingListNames returns the lists the UI can edit: one per role, plus the global bypass.
func (m *Manager) RoutingListNames() []string {
	names := make([]string, 0, len(m.roles)+1)
	for name := range m.roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// leshyModeTarget is the explicit mode identity a render is asked for: the
// committed mode when nil, or the journal target during a mode transition.
type leshyModeTarget struct {
	Mode           ManagedLeshyMode
	Socket         string
	ActivationID   string
	ModeGeneration uint64
}

// leshyRenderPolicy is the complete explicit input set of one generated
// Leshy configuration (AUD2, 2026-11-16 post-e7ec9c2 audit §3.6). The
// renderer consumes these values and never mutates Manager state just to
// calculate bytes: every live policy transaction renders the committed
// policy, and a rollback re-renders from the captured old policy instead of
// re-reading a mixture of already-restored and not-yet-restored files.
type leshyRenderPolicy struct {
	Mode           ManagedLeshyMode
	Socket         string
	ActivationID   string
	ModeGeneration uint64
	BypassMode     string
	RoleIPv6       map[string]bool
	RoutingDir     string
	PublicationDir string
}

// captureLeshyRenderPolicyLocked snapshots the policy inputs from the manager
// state. When target is non-nil its mode identity overrides the committed one
// (mode-transition recovery); everything else stays the live committed state.
// The caller holds policyMu; m.mu is taken for short reads only.
func (m *Manager) captureLeshyRenderPolicyLocked(target *leshyModeTarget) (leshyRenderPolicy, error) {
	m.mu.Lock()
	socket := m.managedSocket
	activationID := m.managedActivationID
	generation := m.modeGeneration
	m.mu.Unlock()
	if target != nil {
		switch target.Mode {
		case ManagedLeshyManaged:
			if target.Socket == "" {
				return leshyRenderPolicy{}, fmt.Errorf("managed mode requires the kikimora_socket path")
			}
			socket = target.Socket
			activationID = target.ActivationID
			generation = target.ModeGeneration
		case ManagedLeshyLocal:
			socket = ""
			activationID = ""
			generation = 0
		default:
			return leshyRenderPolicy{}, fmt.Errorf("unknown committed mode %q", target.Mode)
		}
	}
	if socket != "" && m.stateDir == "" {
		return leshyRenderPolicy{}, fmt.Errorf("a managed configuration requires the core state directory")
	}
	mode := ManagedLeshyLocal
	if socket != "" {
		mode = ManagedLeshyManaged
	}
	routingDir := m.routingDirectory()
	// PC8A §1.3: render ONLY from the committed strict caches. bypassMode is
	// loaded (strictly) on first read; the IPv6 cache comes from the same
	// strict startup load or the committed live-policy mutations. A file
	// modified behind the running core never silently changes runtime truth.
	m.mu.Lock()
	if !m.bypassModeLoaded {
		strict, err := config.ReadBypassModeStrict(m.bypassPath())
		if err != nil {
			m.mu.Unlock()
			return leshyRenderPolicy{}, fmt.Errorf("load the bypass decision: %w", err)
		}
		m.bypassMode = strict
		m.bypassModeLoaded = true
	}
	bypass := m.bypassMode
	ipv6 := make(map[string]bool, len(m.roleIPv6))
	if m.roleIPv6Loaded {
		for role, enabled := range m.roleIPv6 {
			ipv6[role] = enabled
		}
	}
	m.mu.Unlock()
	return leshyRenderPolicy{
		Mode:           mode,
		Socket:         socket,
		ActivationID:   activationID,
		ModeGeneration: generation,
		BypassMode:     bypass,
		RoleIPv6:       ipv6,
		RoutingDir:     routingDir,
		PublicationDir: m.leshyPublicationDirectory(),
	}, nil
}

// renderLeshyConfig renders the exact generated-config bytes for one explicit
// policy. It is a pure function of the policy plus the role/list files: no
// Manager field is read or written here.
func (m *Manager) renderLeshyConfig(policy leshyRenderPolicy) ([]byte, error) {
	server, err := m.buildLeshyServer(m.leshyConfigFile(), policy.Socket, policy.ActivationID, policy.ModeGeneration)
	if err != nil {
		return nil, err
	}
	zones, err := config.BuildLeshyZones(policy.RoutingDir, policy.PublicationDir, policy.BypassMode)
	if err != nil {
		return nil, err
	}
	for i := range zones {
		enabled, ok := policy.RoleIPv6[zones[i].Name]
		if !ok {
			enabled = config.DefaultLeshyIPv6
		}
		zones[i].IPv6 = &enabled
	}
	return config.RenderLeshyConfig(server, zones)
}

// writeLeshyConfigPolicyLocked renders and atomically replaces the generated
// configuration for one explicit policy. The caller holds policyMu.
func (m *Manager) writeLeshyConfigPolicyLocked(policy leshyRenderPolicy) error {
	data, err := m.renderLeshyConfig(policy)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomicFile(m.leshyConfigFile(), data, 0o644)
}

// regenerateLeshyConfigPolicyLocked re-renders the generated configuration
// from the current committed policy. The caller holds policyMu.
func (m *Manager) regenerateLeshyConfigPolicyLocked() error {
	policy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}
	return m.writeLeshyConfigPolicyLocked(policy)
}

// RegenerateLeshyConfig writes the router configuration from the roles and their lists. The file
// is a product of the core: hand edits are lost by design, the lists are the source of truth.
// The write is part of the policyMu-serialized policy transaction (AUD2 §3.5).
func (m *Manager) RegenerateLeshyConfig() error {
	m.policyMu.Lock()
	defer m.policyMu.Unlock()
	return m.regenerateLeshyConfigPolicyLocked()
}

// buildLeshyServer composes the server section for one mode. An empty socket
// means local mode, which must not carry any managed-only field.
func (m *Manager) buildLeshyServer(path, socket, activationID string, generation uint64) (config.LeshyServer, error) {
	// PC8A §1.3: an existing generated config must parse — a malformed file
	// is a regeneration error, never silent default DNS/server settings.
	// Omitted fields still mean the current defaults.
	server, err := config.ReadLeshyServerForRegeneration(path)
	if err != nil {
		return server, err
	}
	if socket != "" {
		if m.stateDir == "" {
			return server, fmt.Errorf("a managed configuration requires the core state directory")
		}
		server.RoutingBackend = "kikimora"
		server.KikimoraSocket = socket
		server.KikimoraAuthFile = PrivilegedAuthKeyPath(m.stateDir)
		server.KikimoraActivationID = activationID
		server.KikimoraModeGeneration = generation
		return server, nil
	}
	// The rollback must strip every managed key even though they are still in
	// the file this generation reads back: the core owns the generated
	// configuration, hand edits are lost by design.
	server.RoutingBackend = ""
	server.KikimoraSocket = ""
	server.KikimoraAuthFile = ""
	server.KikimoraActivationID = ""
	server.KikimoraModeGeneration = 0
	return server, nil
}

// buildLeshyZones composes the zone list the generated configuration carries.
func (m *Manager) buildLeshyZones() ([]config.LeshyZoneSpec, error) {
	// One publication root shared with the leshy.FileBridge (U5): a direct
	// config.DefaultLeshyVPNDir() call here would route generated zones
	// outside an injected runtime root.
	vpnDir := m.leshyPublicationDirectory()

	// PC8A §1.3: the verification oracle mirrors runtime truth — the strict
	// committed bypass cache and the IPv6 policy cache, not permissive disk
	// reads.
	m.mu.Lock()
	if !m.bypassModeLoaded {
		strict, err := config.ReadBypassModeStrict(m.bypassPath())
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("load the bypass decision: %w", err)
		}
		m.bypassMode = strict
		m.bypassModeLoaded = true
	}
	bypass := m.bypassMode
	settings := make(map[string]bool, len(m.roleIPv6))
	if m.roleIPv6Loaded {
		for role, enabled := range m.roleIPv6 {
			settings[role] = enabled
		}
	}
	m.mu.Unlock()
	zones, err := config.BuildLeshyZones(m.routingDirectory(), vpnDir, bypass)
	if err != nil {
		return nil, err
	}
	// Per-role IPv6 policy (default: on). The controller's per-family
	// readiness still decides what actually routes; this flag only lets the
	// operator opt a role out of AAAA handling entirely.
	for i := range zones {
		enabled, ok := settings[zones[i].Name]
		if !ok {
			enabled = config.DefaultLeshyIPv6
		}
		zones[i].IPv6 = &enabled
	}
	return zones, nil
}

// RegenerateLeshyConfigForMode writes the router configuration for an
// explicit committed mode (08D1 §31.17): the transition-journal recovery
// (Stage 6) calls it with the journal target; ordinary startup keeps
// RegenerateLeshyConfig, which reads the restored committed mode. The target
// identity is passed explicitly — the renderer never temporarily mutates the
// live Manager mode fields to produce target bytes (AUD2 §3.6).
func (m *Manager) RegenerateLeshyConfigForMode(mode ManagedLeshyMode, socket, activationID string, generation uint64) error {
	m.policyMu.Lock()
	defer m.policyMu.Unlock()
	return m.regenerateLeshyConfigForModePolicyLocked(mode, socket, activationID, generation)
}

// regenerateLeshyConfigForModePolicyLocked is the policyMu-locked
// RegenerateLeshyConfigForMode body: mode transitions enter policyMu before
// their writer-lock transaction and call this directly.
func (m *Manager) regenerateLeshyConfigForModePolicyLocked(mode ManagedLeshyMode, socket, activationID string, generation uint64) error {
	policy, err := m.captureLeshyRenderPolicyLocked(&leshyModeTarget{
		Mode:           mode,
		Socket:         socket,
		ActivationID:   activationID,
		ModeGeneration: generation,
	})
	if err != nil {
		return err
	}
	return m.writeLeshyConfigPolicyLocked(policy)
}

// renderLeshyConfigForTarget renders the exact generated-config bytes for an
// explicit mode target without writing them: the mode transactions need the
// bytes for their journaled digest before anything is committed. The caller
// holds policyMu (Enable/Disable/Recover enter it before the writer lock).
func (m *Manager) renderLeshyConfigForTarget(target *leshyModeTarget) ([]byte, error) {
	policy, err := m.captureLeshyRenderPolicyLocked(target)
	if err != nil {
		return nil, err
	}
	return m.renderLeshyConfig(policy)
}

// RoleIPv6 returns the role's AAAA enable policy from the committed strict
// cache (PC8A §1.3): disk truth is loaded once at startup and moved only by
// committed live policy mutations.
func (m *Manager) RoleIPv6(role string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roleIPv6Locked(role)
}

// roleIPv6Locked is the lock-held read used by the snapshot composition.
// Callers hold m.mu.
func (m *Manager) roleIPv6Locked(role string) bool {
	if m.roleIPv6Loaded {
		if enabled, ok := m.roleIPv6[role]; ok {
			return enabled
		}
		return config.DefaultLeshyIPv6
	}
	// Cache not loaded (development/test manager without a startup strict
	// load): fall back to a strict read of the current policy file.
	settings, err := config.ReadRoleIPv6SettingsStrict(m.routingDirectory())
	if err != nil {
		return config.DefaultLeshyIPv6
	}
	if enabled, ok := settings[role]; ok {
		return enabled
	}
	return config.DefaultLeshyIPv6
}

// SetRoleIPv6 stores the role's AAAA enable policy as one fail-closed live
// policy transaction (AUD2B, 2026-11-16 post-e7ec9c2 audit §3.5): the
// setting file, the generated configuration and the running Leshy commit
// together, and a successful call reloads Leshy exactly once. On any failure
// the exact previous role-ipv6.json bytes/existence are restored, the old
// generated config is rewritten from the captured old policy and the old
// policy is reloaded, so the running router never keeps a policy the API
// reported as failed.
func (m *Manager) SetRoleIPv6(role string, enabled bool) error {
	m.mu.Lock()
	_, known := m.roles[role]
	m.mu.Unlock()
	if !known {
		return fmt.Errorf("unknown role %q", role)
	}

	m.policyMu.Lock()
	defer m.policyMu.Unlock()
	ctx := context.Background()
	routingDir := m.routingDirectory()
	path := config.RoleIPv6SettingsPath(routingDir)
	// PC8A §1.2: a pre-existing unreadable policy file is an error BEFORE any
	// mutation — never misclassified as absent.
	snapshot, err := snapshotPolicyFile(path)
	if err != nil {
		return err
	}

	oldPolicy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}

	if _, err := config.WriteRoleIPv6Setting(routingDir, role, enabled); err != nil {
		return err
	}
	// The committed cache moves to the target before the regenerate so the
	// renderer reflects the durable policy (PC8A §1.3); a rollback restores
	// the snapshot content into the cache.
	m.mu.Lock()
	if m.roleIPv6 == nil {
		m.roleIPv6 = make(map[string]bool)
	}
	m.roleIPv6[role] = enabled
	m.roleIPv6Loaded = true
	m.mu.Unlock()
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		rollbackErr := m.rollbackRoleIPv6Policy(ctx, snapshot, oldPolicy)
		if rollbackErr != nil {
			// PC8A §1.9: an unproved rollback quarantines the policy
			// authority — route_authority_ready=false until an explicit
			// reproof.
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked("the IPv6 policy rollback is unproved: " + rollbackErr.Error())
			m.mu.Unlock()
		}
		return errors.Join(
			fmt.Errorf("write the generated router configuration: %w", err), rollbackErr)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		rollbackErr := m.rollbackRoleIPv6Policy(ctx, snapshot, oldPolicy)
		if rollbackErr != nil {
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked("the IPv6 policy rollback is unproved: " + rollbackErr.Error())
			m.mu.Unlock()
		}
		return errors.Join(
			fmt.Errorf("reload Leshy after the IPv6 policy change: %w", err), rollbackErr)
	}
	// The mutation committed; notify the subscribers.
	m.mu.Lock()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// policyFileSnapshot is the exact-byte image of one authoritative policy
// source file (PC8A §1.2): existence, bytes and mode, with read errors
// reported as errors BEFORE any mutation instead of being misread as "the
// file is absent".
type policyFileSnapshot struct {
	Path    string
	Existed bool
	Data    []byte
	Mode    os.FileMode
}

// snapshotPolicyFile captures the exact state of one policy file. Any stat or
// read error other than a clean absence is an error: a rollback that misclassifies
// a pre-existing unreadable file as absent would durably delete user policy.
func snapshotPolicyFile(path string) (policyFileSnapshot, error) {
	info, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return policyFileSnapshot{Path: path}, nil
	}
	if statErr != nil {
		return policyFileSnapshot{}, fmt.Errorf("snapshot %s: %w", path, statErr)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return policyFileSnapshot{}, fmt.Errorf("snapshot %s: %w", path, readErr)
	}
	return policyFileSnapshot{Path: path, Existed: true, Data: data, Mode: info.Mode().Perm()}, nil
}

// Restore returns the file to its exact pre-transaction state durably
// (PC8A §1.2): exact bytes and mode through the shared atomic primitive, or a
// durable removal when the transaction created the file.
func (s policyFileSnapshot) Restore() error {
	if !s.Existed {
		if err := fsutil.RemoveDurable(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore absent %s: %w", s.Path, err)
		}
		return nil
	}
	if err := fsutil.WriteAtomicFile(s.Path, s.Data, s.Mode); err != nil {
		return fmt.Errorf("restore %s: %w", s.Path, err)
	}
	return nil
}

// rollbackRoleIPv6Policy restores the exact previous role-ipv6.json
// bytes/mode/existence through the durable snapshot primitive, rewrites the
// generated configuration from the captured old policy and reloads the old
// policy. Best effort: the caller's error stays visible, and any rollback
// step that cannot be proven quarantines the policy authority (PC8A §1.9).
func (m *Manager) rollbackRoleIPv6Policy(ctx context.Context, snapshot policyFileSnapshot, oldPolicy leshyRenderPolicy) error {
	var errs []error
	if err := snapshot.Restore(); err != nil {
		errs = append(errs, fmt.Errorf("restore the IPv6 policy file: %w", err))
	}
	// The strict cache follows the durable file: re-read the restored image.
	if ipv6, err := config.ReadRoleIPv6SettingsStrict(m.routingDirectory()); err == nil {
		m.mu.Lock()
		m.roleIPv6 = ipv6
		m.roleIPv6Loaded = true
		m.mu.Unlock()
	}
	if err := m.writeLeshyConfigPolicyLocked(oldPolicy); err != nil {
		errs = append(errs, fmt.Errorf("restore the generated router configuration: %w", err))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		errs = append(errs, fmt.Errorf("restore the Leshy reload: %w", err))
	}
	return errors.Join(errs...)
}

// bypassPath is where the global bypass decision lives; overridable in tests.
func (m *Manager) bypassPath() string {
	if m.bypassConfigPath != "" {
		return m.bypassConfigPath
	}
	return config.DefaultBypassPath()
}

// bypassModeLocked returns the cached global decision, loading it once from
// disk. The caller holds m.mu.
func (m *Manager) bypassModeLocked() string {
	if !m.bypassModeLoaded {
		m.bypassMode = config.ReadBypassMode(m.bypassPath())
		m.bypassModeLoaded = true
	}
	return m.bypassMode
}

// bypassOwnerLocked returns the role currently carrying the global catch-all
// zone. "direct" (or a role that is no longer loaded) means no owner.
func (m *Manager) bypassOwnerLocked() (string, bool) {
	mode := m.bypassModeLocked()
	if mode == "" || mode == config.BypassDirect {
		return "", false
	}
	if _, ok := m.roles[mode]; !ok {
		return "", false
	}
	return mode, true
}

// bypassTransitionActive reports whether a global default-route owner switch
// is in flight (AUD2 §3.3): while active, external bypass intents are
// fenced.
func (m *Manager) bypassTransitionActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bypassTransition.Active
}

// validateProfileBypassLocked rejects a profile switch that would leave the
// global default route pointing at a role the target profile disables. It runs
// before any desired-state mutation (PROD2B, 2026-11-16 audit §4).
func (m *Manager) validateProfileBypassLocked(profile string) error {
	mode := m.bypassModeLocked()
	if mode == "" || mode == config.BypassDirect {
		return nil
	}
	if !slices.Contains(m.profileToRoles[profile], mode) {
		return fmt.Errorf("profile %q does not contain the global default-route role %q; set the default route to %q first", profile, mode, config.BypassDirect)
	}
	return nil
}

// validateBypassRoleLocked rejects a default-route role that does not exist or
// that the active profile disables.
func (m *Manager) validateBypassRoleLocked(role string) error {
	r, ok := m.roles[role]
	if !ok {
		return fmt.Errorf("unknown role %q for the global default route", role)
	}
	if !slices.Contains(m.profileToRoles[m.activeProfile], role) {
		return fmt.Errorf("role %q is not enabled by the active profile %q", role, m.activeProfile)
	}
	// PC8A §1.5 / R8A §1.3: in managed mode EVERY default-route role must
	// carry a stable unique managed zone identity — the guard is gated only
	// on the committed socket, never on whether the broken role already made
	// it into the managed-role set.
	if m.managedSocket != "" {
		if err := m.validateManagedRouteIdentityLocked(role); err != nil {
			return err
		}
	}
	// R8B §2.7: a disconnected role must not become the durable default
	// route — the policy would point at a tunnel the operator turned off.
	if r.enabled == false {
		return fmt.Errorf(
			"role %q is disconnected; connect it before making it the global default route",
			role)
	}
	return nil
}

// validateManagedRouteIdentityLocked rejects a role that cannot own managed
// selected traffic: it must exist and carry a non-empty Leshy zone that is
// unique across the loaded roles (PC8A §1.5). Callers hold m.mu.
func (m *Manager) validateManagedRouteIdentityLocked(role string) error {
	r, ok := m.roles[role]
	if !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	zone := r.cfg.EffectiveEndpointPolicy().Zone
	if zone == "" {
		return fmt.Errorf(
			"role %q cannot own the global default route in managed mode because it has no leshy_zone; add leshy_zone = %q (or recreate/import the tunnel) and retry",
			role, role)
	}
	for name, other := range m.roles {
		if name == role {
			continue
		}
		if other.cfg.EffectiveEndpointPolicy().Zone == zone {
			return fmt.Errorf("managed zone %q is claimed by roles %q and %q; a managed zone must map to exactly one role", zone, role, name)
		}
	}
	return nil
}

// requireBypassInProfileLocked rejects a membership edit whose new role set
// would drop the current global default-route role from the active profile.
func (m *Manager) requireBypassInProfileLocked(wanted map[string]bool) error {
	mode := m.bypassModeLocked()
	if mode == "" || mode == config.BypassDirect {
		return nil
	}
	if !wanted[mode] {
		return fmt.Errorf("the active profile must keep the global default-route role %q; set the default route to %q first", mode, config.BypassDirect)
	}
	return nil
}

// BypassMode returns the current global decision: "direct" or a role name.
func (m *Manager) BypassMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bypassModeLocked()
}

// SetBypassMode moves the global default route as one fail-closed
// route-authority transaction (AUD2, 2026-11-16 post-e7ec9c2 audit §3.3):
// the switch arms a bypass transition fence, the old owner's synthetic zone
// is drained through the internal explicit-owner flush, the new owner is
// committed and Leshy reloads, and only then does the core accept intents for
// the new owner. While the transition is active no external bypass Ensure,
// Forget or Flush can re-add the old owner, because the resolver reports the
// transition instead of a routable owner. A failed rewrite or reload restores
// the previous mode, the bypass file, the captured old generated config and
// the old Leshy policy, and returns a visible error instead of exposing a
// half-switched default path.
func (m *Manager) SetBypassMode(mode string) error {
	// The bypass commit is part of the same policy transaction as the profile
	// and membership edits (AUD1 §2.4): validation and commit must observe one
	// serialized composition, otherwise a concurrent membership edit could
	// disable the new owner between validation and commit.
	m.policyMu.Lock()
	defer m.policyMu.Unlock()
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = config.BypassDirect
	}

	m.mu.Lock()
	if mode != config.BypassDirect {
		if err := m.validateBypassRoleLocked(mode); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	previous := m.bypassModeLocked()
	oldOwner, _ := m.bypassOwnerLocked()
	m.mu.Unlock()

	newOwner := ""
	if mode != config.BypassDirect {
		newOwner = mode
	}
	ctx := context.Background()

	// PC8A §1.2: capture the exact bypass.conf image BEFORE any mutation. A
	// pre-existing unreadable file is an error here, never "absent" — the
	// rollback would otherwise durably delete live user policy.
	bypassSnapshot, err := snapshotPolicyFile(m.bypassPath())
	if err != nil {
		return err
	}

	// Capture the old policy before any mutation: the rollback re-renders the
	// generated config from this snapshot instead of re-reading a mixture of
	// already-restored and not-yet-restored files (AUD2 §3.6).
	oldPolicy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}

	// Arm the transition fence: from here on the old owner is not routable for
	// external bypass intents, so the drain below is a real fence, not a
	// window an Ensure can slip into.
	m.mu.Lock()
	m.bypassTransition = bypassTransition{Active: true, From: oldOwner, To: newOwner}
	m.mu.Unlock()

	handover := oldOwner != "" && oldOwner != newOwner && m.selectedRoutes != nil && m.selectedRoutes.ManagesRole(oldOwner)
	if handover {
		// PC8B §2.2 (2026-11-17 post-X1 audit): protected A->B handover.
		// Every already-resolved catch-all prefix must keep protection at
		// every instant: install proven parks on A's bypass records and
		// remove the A rows while the park evidence stays — the new owner's
		// rows are applied AFTER the new policy commits, below. The zone
		// fence (bypassTransition) denies new external bypass intents; the
		// role is NEVER frozen wholesale (ordinary role-zone routes stay
		// live through the whole handover).
		if err := m.selectedRoutes.ParkAndWithdrawBypass(ctx, oldOwner); err != nil {
			// Nothing is committed yet: the old policy is still live, the
			// bypass file untouched. Clear the fence and report.
			m.mu.Lock()
			m.bypassTransition = bypassTransition{}
			m.mu.Unlock()
			return fmt.Errorf("the protected default-route handover from role %q: %w", oldOwner, err)
		}
	}

	if err := config.WriteBypassMode(m.bypassPath(), mode); err != nil {
		m.mu.Lock()
		m.bypassTransition = bypassTransition{}
		m.mu.Unlock()
		return err
	}
	// The committed bypass cache moves to the target BEFORE the regenerate:
	// the renderer must reflect the policy the durable file now carries, and
	// the rollback below restores the exact old image (and cache) or the
	// failure quarantines the authority (PC8A §1.9).
	m.mu.Lock()
	m.bypassMode = mode
	m.bypassModeLoaded = true
	m.mu.Unlock()
	restoreHandover := func() error {
		// PC8B §2.5: the parks are already Active and the A rows removed —
		// the rollback must restore the old rows under the parks, prove them,
		// retire the parks and reopen the admission, exactly like the
		// target==source crash recovery.
		product, _ := m.product.Role(oldOwner)
		if err := m.selectedRoutes.Restore(ctx, oldOwner,
			product.Validation.Families.IPv4.Ready,
			product.Validation.Families.IPv6.Ready); err != nil {
			return err
		}
		if err := m.selectedRoutes.ObserveRestoration(ctx, oldOwner); err != nil {
			return err
		}
		m.selectedRoutes.OpenPublication(oldOwner)
		return nil
	}
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		rollback := m.rollbackBypassPolicyLocked(ctx, previous, bypassSnapshot, oldPolicy)
		restoreErr := error(nil)
		if handover && rollback == nil {
			restoreErr = restoreHandover()
		}
		m.mu.Lock()
		if rollback != nil || restoreErr != nil {
			// PC8A §1.9 / PC8B §2.5: an unproved rollback keeps the fence
			// armed and quarantines the authority.
			m.bypassTransition = bypassTransition{Active: true, From: oldOwner, To: newOwner}
			reason := "the default-route rollback is unproved"
			if rollback != nil {
				reason += ": " + rollback.Error()
			}
			if restoreErr != nil {
				reason += "; the old rows are unproved: " + restoreErr.Error()
			}
			m.quarantinePolicyAuthorityLocked(reason)
		} else {
			m.bypassTransition = bypassTransition{}
		}
		m.bypassMode = previous
		m.bumpLocked()
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("write the generated router configuration: %w", err), rollback, restoreErr)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		rollback := m.rollbackBypassPolicyLocked(ctx, previous, bypassSnapshot, oldPolicy)
		restoreErr := error(nil)
		if handover && rollback == nil {
			restoreErr = restoreHandover()
		}
		m.mu.Lock()
		if rollback != nil || restoreErr != nil {
			m.bypassTransition = bypassTransition{Active: true, From: oldOwner, To: newOwner}
			reason := "the default-route rollback is unproved"
			if rollback != nil {
				reason += ": " + rollback.Error()
			}
			if restoreErr != nil {
				reason += "; the old rows are unproved: " + restoreErr.Error()
			}
			m.quarantinePolicyAuthorityLocked(reason)
		} else {
			m.bypassTransition = bypassTransition{}
		}
		m.bypassMode = previous
		m.bumpLocked()
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("reload Leshy after the global default-route change: %w", err), rollback, restoreErr)
	}

	if handover {
		// PC8B §2.2 steps 6-8: apply the captured prefixes on B (park
		// evidence stays until every B row is proven), then retire the A
		// parks and records through the canonical retirement. A failure here
		// leaves the handover pending: the fence stays armed, the authority
		// quarantined, and the durable bypass.conf + checkpoint evidence
		// resume the transfer on the next start (or via reproof).
		intents, snapErr := m.selectedRoutes.SnapshotBypassIntents(oldOwner)
		if snapErr != nil {
			snapErr = fmt.Errorf("snapshot the transferred intents from %q: %w", oldOwner, snapErr)
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked(snapErr.Error())
			m.bumpLocked()
			m.mu.Unlock()
			return snapErr
		}
		if err := m.selectedRoutes.ApplyBypassToOwner(ctx, newOwner, intents); err != nil {
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked(fmt.Sprintf("the default-route handover to %q is pending: %v", newOwner, err))
			m.bumpLocked()
			m.mu.Unlock()
			return fmt.Errorf("apply the transferred prefixes to role %q: %w", newOwner, err)
		}
		if err := m.selectedRoutes.CommitBypassHandover(ctx, oldOwner, intents); err != nil {
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked(fmt.Sprintf("the default-route handover cleanup for %q is pending: %v", oldOwner, err))
			m.bumpLocked()
			m.mu.Unlock()
			return fmt.Errorf("retire the transferred evidence from role %q: %w", oldOwner, err)
		}
		// The new owner is the live bypass surface: open its admission.
		m.selectedRoutes.OpenPublication(newOwner)
	}

	m.mu.Lock()
	m.bypassTransition = bypassTransition{}
	// R8B §2.4: a fully committed default-route switch (policy written,
	// Leshy reloaded, protected handover complete) is itself the authority
	// proof — clear any earlier rollback/handover quarantine.
	m.clearPolicyAuthorityAfterProofLocked()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// rollbackBypassPolicyLocked returns the default route to its previous owner
// after a failed commit: the bypass file, the generated configuration (from
// the captured old policy) and the running Leshy policy. The transition fence
// stays armed for the whole sequence, so no bypass intent can commit between
// the failure and the restored policy. Best effort: the caller's error stays
// visible even when the rollback itself fails. The caller holds policyMu.
func (m *Manager) rollbackBypassPolicyLocked(ctx context.Context, previous string, bypassSnapshot policyFileSnapshot, oldPolicy leshyRenderPolicy) error {
	if previous == "" {
		previous = config.BypassDirect
	}
	var errs []error
	// PC8A §1.2: restore the exact pre-transaction bypass.conf image durably —
	// an absent file is durably absent again, not rewritten as "direct".
	if err := bypassSnapshot.Restore(); err != nil {
		errs = append(errs, fmt.Errorf("restore the previous default route: %w", err))
	}
	if err := m.writeLeshyConfigPolicyLocked(oldPolicy); err != nil {
		errs = append(errs, fmt.Errorf("restore the generated router configuration: %w", err))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		errs = append(errs, fmt.Errorf("restore the Leshy reload: %w", err))
	}
	return errors.Join(errs...)
}

// RoutingList returns the current content of one list.
func (m *Manager) RoutingList(name string) (string, error) {
	return config.ReadRoutingList(m.routingDirectory(), name)
}

// SetRoutingListLive replaces one routing list as one fail-closed live policy
// transaction (AUD2B, 2026-11-16 post-e7ec9c2 audit §3.5): the list, the
// generated configuration and the running Leshy commit together. On a
// regenerate or reload failure the exact previous list bytes/existence are
// restored (a list the transaction created is removed again), the old
// generated config is rewritten from the captured old policy and the old
// policy is reloaded. There is no allowed "saved, but Leshy did not restart"
// result: a reload failure is a command failure.
func (m *Manager) SetRoutingListLive(ctx context.Context, name, content string) error {
	if name == config.BypassList {
		return fmt.Errorf("bypass is a global rule, not a list")
	}
	// R8A §1.4: every routing list belongs to exactly one loaded role — an
	// orphan list would become a live Leshy zone the core later denies.
	m.mu.Lock()
	_, known := m.roles[name]
	m.mu.Unlock()
	if !known {
		return fmt.Errorf("unknown role %q for the routing list", name)
	}
	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	dir := m.routingDirectory()
	listPath := filepath.Join(dir, name+".txt")
	// PC8A §1.2: a pre-existing unreadable list file is an error BEFORE any
	// mutation — never misclassified as absent.
	snapshot, err := snapshotPolicyFile(listPath)
	if err != nil {
		return err
	}
	oldPolicy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}

	if err := config.WriteRoutingList(dir, name, content); err != nil {
		return err
	}
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		rollbackErr := m.rollbackRoutingListPolicy(ctx, snapshot, oldPolicy)
		if rollbackErr != nil {
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked("the routing-list rollback is unproved: " + rollbackErr.Error())
			m.mu.Unlock()
		}
		return errors.Join(
			fmt.Errorf("write the generated router configuration: %w", err), rollbackErr)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		rollbackErr := m.rollbackRoutingListPolicy(ctx, snapshot, oldPolicy)
		if rollbackErr != nil {
			m.mu.Lock()
			m.quarantinePolicyAuthorityLocked("the routing-list rollback is unproved: " + rollbackErr.Error())
			m.mu.Unlock()
		}
		return errors.Join(
			fmt.Errorf("reload Leshy after the list change: %w", err), rollbackErr)
	}
	// The role drawer renders the saved list from the snapshot; the revision
	// bump is what tells every subscriber the policy moved.
	m.mu.Lock()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// rollbackRoutingListPolicy restores the exact previous list
// bytes/existence, rewrites the generated configuration from the captured old
// policy and reloads the old policy. Best effort: the caller's error stays
// visible even when the rollback itself fails.
func (m *Manager) rollbackRoutingListPolicy(ctx context.Context, snapshot policyFileSnapshot, oldPolicy leshyRenderPolicy) error {
	var errs []error
	if err := snapshot.Restore(); err != nil {
		errs = append(errs, fmt.Errorf("restore the routing list: %w", err))
	}
	if err := m.writeLeshyConfigPolicyLocked(oldPolicy); err != nil {
		errs = append(errs, fmt.Errorf("restore the generated router configuration: %w", err))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		errs = append(errs, fmt.Errorf("restore the Leshy reload: %w", err))
	}
	return errors.Join(errs...)
}

// reloadLeshy restarts the router so it re-reads the lists. The hook exists so tests do not
// restart a real service.
func (m *Manager) reloadLeshy(ctx context.Context) error {
	if m.reloadHook != nil {
		return m.reloadHook(ctx)
	}
	return config.ReloadLeshy(ctx)
}
