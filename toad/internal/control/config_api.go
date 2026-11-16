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
	settlement.schedule(m)
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
	settlement.schedule(m)
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
	settlement.schedule(m)
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
	if name == m.activeProfile && len(wanted) > 0 {
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
	if m.configDir != "" && (len(plan.Memberships) > 0 || plan.DeclaredProfiles != nil) {
		prepared, err := config.PrepareProfileComposition(m.configDir, plan)
		if err != nil {
			return false, profileSettlement{}, err
		}
		if err := prepared.Commit(); err != nil {
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
		return false, profileSettlement{}, errors.Join(
			fmt.Errorf("reconcile active profile %q: %w", name, applyErr), rollbackErr)
	}
	m.bumpLocked()
	m.mu.Unlock()
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
	return leshyRenderPolicy{
		Mode:           mode,
		Socket:         socket,
		ActivationID:   activationID,
		ModeGeneration: generation,
		// The generated configuration reflects the persisted bypass decision
		// on disk; policyMu serializes every writer of that file.
		BypassMode:     config.ReadBypassMode(m.bypassPath()),
		RoleIPv6:       config.ReadRoleIPv6Settings(routingDir),
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
	server := config.ReadLeshyServer(path)
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

	zones, err := config.BuildLeshyZones(m.routingDirectory(), vpnDir, config.ReadBypassMode(m.bypassPath()))
	if err != nil {
		return nil, err
	}
	// Per-role IPv6 policy (default: on). The controller's per-family
	// readiness still decides what actually routes; this flag only lets the
	// operator opt a role out of AAAA handling entirely.
	settings := config.ReadRoleIPv6Settings(m.routingDirectory())
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

// RoleIPv6 returns the role's AAAA enable policy for the generated zones.
func (m *Manager) RoleIPv6(role string) bool {
	settings := config.ReadRoleIPv6Settings(m.routingDirectory())
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
	oldData, oldMode, oldExisted := snapshotFileForPolicy(path)

	oldPolicy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}

	if _, err := config.WriteRoleIPv6Setting(routingDir, role, enabled); err != nil {
		return err
	}
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		return errors.Join(
			fmt.Errorf("write the generated router configuration: %w", err),
			m.rollbackRoleIPv6Policy(ctx, path, oldData, oldMode, oldExisted, oldPolicy))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("reload Leshy after the IPv6 policy change: %w", err),
			m.rollbackRoleIPv6Policy(ctx, path, oldData, oldMode, oldExisted, oldPolicy))
	}
	// Subscribers read the policy back from the snapshot: without the bump a
	// UI switch showed its own optimistic value until an unrelated change
	// happened to refresh the revision.
	m.mu.Lock()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// snapshotFileForPolicy captures the exact bytes, mode and existence of one
// policy file so a rollback can restore it byte-for-byte.
func snapshotFileForPolicy(path string) (data []byte, mode os.FileMode, existed bool) {
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, 0, false
	}
	if statErr != nil {
		return nil, 0, false
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, 0, false
	}
	return data, info.Mode().Perm(), true
}

// rollbackRoleIPv6Policy restores the exact previous role-ipv6.json
// bytes/existence, rewrites the generated configuration from the captured old
// policy and reloads the old policy. Best effort: the caller's error stays
// visible even when the rollback itself fails.
func (m *Manager) rollbackRoleIPv6Policy(ctx context.Context, path string, data []byte, mode os.FileMode, existed bool, oldPolicy leshyRenderPolicy) error {
	var errs []error
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("restore the IPv6 policy file: %w", err))
		}
	} else if err := os.WriteFile(path, data, mode); err != nil {
		errs = append(errs, fmt.Errorf("restore the IPv6 policy file: %w", err))
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
	if _, ok := m.roles[role]; !ok {
		return fmt.Errorf("unknown role %q for the global default route", role)
	}
	if !slices.Contains(m.profileToRoles[m.activeProfile], role) {
		return fmt.Errorf("role %q is not enabled by the active profile %q", role, m.activeProfile)
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

	if oldOwner != "" && oldOwner != newOwner && m.selectedRoutes != nil && m.selectedRoutes.ManagesRole(oldOwner) {
		if err := m.selectedRoutes.FlushBypassOwner(ctx, oldOwner); err != nil {
			// Nothing is committed yet: the old policy is still live, the
			// bypass file untouched. Clear the fence and report.
			m.mu.Lock()
			m.bypassTransition = bypassTransition{}
			m.mu.Unlock()
			return fmt.Errorf("drain the global default-route zone from role %q: %w", oldOwner, err)
		}
	}

	if err := config.WriteBypassMode(m.bypassPath(), mode); err != nil {
		m.mu.Lock()
		m.bypassTransition = bypassTransition{}
		m.mu.Unlock()
		return err
	}
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		rollback := m.rollbackBypassPolicyLocked(ctx, previous, oldPolicy)
		m.mu.Lock()
		m.bypassMode = previous
		m.bypassTransition = bypassTransition{}
		m.bumpLocked()
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("write the generated router configuration: %w", err), rollback)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		rollback := m.rollbackBypassPolicyLocked(ctx, previous, oldPolicy)
		m.mu.Lock()
		m.bypassMode = previous
		m.bypassTransition = bypassTransition{}
		m.bumpLocked()
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("reload Leshy after the global default-route change: %w", err), rollback)
	}

	m.mu.Lock()
	m.bypassMode = mode
	m.bypassModeLoaded = true
	m.bypassTransition = bypassTransition{}
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
func (m *Manager) rollbackBypassPolicyLocked(ctx context.Context, previous string, oldPolicy leshyRenderPolicy) error {
	if previous == "" {
		previous = config.BypassDirect
	}
	var errs []error
	if err := config.WriteBypassMode(m.bypassPath(), previous); err != nil {
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

// SetRoutingList validates and replaces one list on disk. This is the
// disk-only primitive: the API command is SetRoutingListLive, which commits
// the list, the generated configuration and the running Leshy as one
// fail-closed transaction (AUD2B §3.5).
func (m *Manager) SetRoutingList(name, content string) error {
	if err := config.WriteRoutingList(m.routingDirectory(), name, content); err != nil {
		return err
	}
	// The role drawer renders the saved list from the snapshot; the API
	// handler's GetRoutingLists resync covers the text round trip, but the
	// revision bump is what tells every subscriber the state moved.
	m.mu.Lock()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
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
	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	dir := m.routingDirectory()
	listPath := filepath.Join(dir, name+".txt")
	oldData, oldMode, oldExisted := snapshotFileForPolicy(listPath)
	oldPolicy, err := m.captureLeshyRenderPolicyLocked(nil)
	if err != nil {
		return err
	}

	if err := config.WriteRoutingList(dir, name, content); err != nil {
		return err
	}
	if err := m.regenerateLeshyConfigPolicyLocked(); err != nil {
		return errors.Join(
			fmt.Errorf("write the generated router configuration: %w", err),
			m.rollbackRoutingListPolicy(ctx, listPath, oldData, oldMode, oldExisted, oldPolicy))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("reload Leshy after the list change: %w", err),
			m.rollbackRoutingListPolicy(ctx, listPath, oldData, oldMode, oldExisted, oldPolicy))
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
func (m *Manager) rollbackRoutingListPolicy(ctx context.Context, listPath string, data []byte, mode os.FileMode, existed bool, oldPolicy leshyRenderPolicy) error {
	var errs []error
	if !existed {
		if err := os.Remove(listPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("restore the routing list: %w", err))
		}
	} else if err := os.WriteFile(listPath, data, mode); err != nil {
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
