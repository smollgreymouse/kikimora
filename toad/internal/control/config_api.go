package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
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

// SetTunnelProfiles rewrites which profiles an existing tunnel belongs to - the move path
// the UI uses when a role is dragged from one profile to another. The file is rewritten
// first (a failed write leaves everything untouched); when the role is part of the running
// composition the in-memory configuration and the snapshot follow at once, so the UI sees
// the new membership without a core restart. A role that only exists on disk (created
// after this core started) keeps its disk-only move and loads with it on the next start.
func (m *Manager) SetTunnelProfiles(name string, profiles []string) (string, error) {
	if m.configDir == "" {
		return "", fmt.Errorf("the core has no configuration directory")
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("role %q needs at least one profile", name)
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
			return "", fmt.Errorf("role %q carries the global default route in the active profile %q; set the default route to %q first", name, active, config.BypassDirect)
		}
	}
	m.mu.Unlock()

	path, err := config.SetTunnelProfiles(m.configDir, name, profiles)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return path, nil
	}
	active := m.activeProfile
	wasMember := slices.Contains(r.cfg.Profiles, active)
	r.cfg.Profiles = profiles
	m.rebuildProfileMapLocked()
	var epoch uint64
	var changed []string
	var want map[string]bool
	var reconcileErr error
	if isMember := slices.Contains(profiles, active); wasMember != isMember {
		// Moving a role into or out of the live profile is a composition
		// change, not just a disk edit: the role must start or settle down
		// without a core restart (PROD1, 2026-11-16 audit §2.1).
		changed, want, epoch, reconcileErr = m.applyActiveProfileIntentLocked(ctx)
	}
	m.bumpLocked()
	m.mu.Unlock()
	if reconcileErr != nil {
		return path, fmt.Errorf("reconcile active profile %q: %w", active, reconcileErr)
	}
	m.scheduleProfileSettlement(epoch, changed, want)
	return path, nil
}

// CreateProfile registers a profile and, when roles are given, makes them its members. It
// reports whether the name was new to the registry.
//
// A profile is an explicit set of roles, so a new one starts empty: cloning every role into
// it would silently duplicate the current profile instead of letting the operator choose.
// The name is registered in profiles.list, which is what lets an empty profile exist at all.
//
// No restart is involved: profiles are labels, not processes, so the snapshot lists the
// profile at once and SetActiveProfile accepts it.
func (m *Manager) CreateProfile(profile string, roles []string) (int, error) {
	if m.configDir == "" {
		return 0, fmt.Errorf("the core has no configuration directory")
	}
	name := strings.TrimSpace(profile)
	added, err := config.AddDeclaredProfile(m.configDir, name)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	if !slices.Contains(m.declaredProfiles, name) {
		m.declaredProfiles = append(m.declaredProfiles, name)
	}
	m.rebuildProfileMapLocked()
	m.bumpLocked()
	m.mu.Unlock()

	if len(roles) == 0 {
		return boolToInt(added), nil
	}
	if err := m.SetProfileRoles(name, roles); err != nil {
		return boolToInt(added), err
	}
	return boolToInt(added), nil
}

// SetProfileRoles replaces the profile's role set. Memberships are per role: a role that
// also belongs to other profiles keeps them, which is what lets one role serve several
// profiles. Each changed role configuration is rewritten on disk before memory follows, so
// a failed write leaves the running composition untouched.
func (m *Manager) SetProfileRoles(profile string, roles []string) error {
	name := strings.TrimSpace(profile)
	if name == "default" {
		return fmt.Errorf("the default profile always contains every role")
	}
	wanted := make(map[string]bool, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			return fmt.Errorf("role name must not be empty")
		}
		wanted[role] = true
	}

	m.mu.Lock()
	if _, ok := m.profileToRoles[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown profile %q", name)
	}
	for role := range wanted {
		if _, ok := m.roles[role]; !ok {
			m.mu.Unlock()
			return fmt.Errorf("unknown role %q", role)
		}
	}
	if name == m.activeProfile {
		if err := m.requireBypassInProfileLocked(wanted); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	type membership struct {
		role     string
		profiles []string
	}
	changes := make([]membership, 0, len(m.roles))
	for roleName, r := range m.roles {
		has := slices.Contains(r.cfg.Profiles, name)
		switch {
		case wanted[roleName] && !has:
			changes = append(changes, membership{
				role:     roleName,
				profiles: append(slices.Clone(r.cfg.Profiles), name),
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
			changes = append(changes, membership{role: roleName, profiles: next})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].role < changes[j].role })
	m.mu.Unlock()

	if m.configDir != "" && len(changes) > 0 {
		memberships := make([]config.ProfileMembership, 0, len(changes))
		for _, change := range changes {
			memberships = append(memberships, config.ProfileMembership{Role: change.role, Profiles: change.profiles})
		}
		// One transaction for every touched TOML: a mid-write failure must not
		// leave the disk with a half-applied role set while memory keeps the
		// old one (PROD1, 2026-11-16 audit §2.1).
		if err := config.SetProfileMemberships(m.configDir, memberships); err != nil {
			return err
		}
	}

	ctx := context.Background()
	m.mu.Lock()
	for _, change := range changes {
		if r, ok := m.roles[change.role]; ok {
			r.cfg.Profiles = change.profiles
		}
	}
	m.rebuildProfileMapLocked()
	var epoch uint64
	var changed []string
	var want map[string]bool
	var reconcileErr error
	if name == m.activeProfile {
		// Editing the live profile is the same latest-intent operation as a
		// switch: removed members stop, added members start, and any older
		// settlement becomes stale (PROD1, 2026-11-16 audit §2.1).
		changed, want, epoch, reconcileErr = m.applyActiveProfileIntentLocked(ctx)
	}
	m.bumpLocked()
	m.mu.Unlock()
	if reconcileErr != nil {
		return fmt.Errorf("reconcile active profile %q: %w", name, reconcileErr)
	}
	m.scheduleProfileSettlement(epoch, changed, want)
	return nil
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

// RegenerateLeshyConfig writes the router configuration from the roles and their lists. The file
// is a product of the core: hand edits are lost by design, the lists are the source of truth.
func (m *Manager) RegenerateLeshyConfig() error {
	path := m.leshyConfigPath
	if path == "" {
		path = config.DefaultLeshyConfigPath()
	}
	// The generated configuration reflects the persisted managed-mode
	// decision; legacy-file installs keep the local backend.
	socket := m.managedSocketForGeneration()
	m.mu.Lock()
	activationID := m.managedActivationID
	generation := m.modeGeneration
	m.mu.Unlock()
	server, err := m.buildLeshyServer(path, socket, activationID, generation)
	if err != nil {
		return err
	}
	zones, err := m.buildLeshyZones()
	if err != nil {
		return err
	}
	return config.WriteLeshyConfig(path, server, zones)
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
// RegenerateLeshyConfig, which reads the restored committed mode.
func (m *Manager) RegenerateLeshyConfigForMode(mode ManagedLeshyMode, socket, activationID string, generation uint64) error {
	m.mu.Lock()
	previousSocket := m.managedSocket
	previousActivation := m.managedActivationID
	previousGeneration := m.modeGeneration
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.managedSocket = previousSocket
		m.managedActivationID = previousActivation
		m.modeGeneration = previousGeneration
		m.mu.Unlock()
	}()
	switch mode {
	case ManagedLeshyManaged:
		if socket == "" {
			return fmt.Errorf("managed mode requires the kikimora_socket path")
		}
		m.mu.Lock()
		m.managedSocket = socket
		m.managedActivationID = activationID
		m.modeGeneration = generation
		m.mu.Unlock()
	case ManagedLeshyLocal:
		m.mu.Lock()
		m.managedSocket = ""
		m.managedActivationID = ""
		m.modeGeneration = 0
		m.mu.Unlock()
	default:
		return fmt.Errorf("unknown committed mode %q", mode)
	}
	return m.RegenerateLeshyConfig()
}

// RoleIPv6 returns the role's AAAA enable policy for the generated zones.
func (m *Manager) RoleIPv6(role string) bool {
	settings := config.ReadRoleIPv6Settings(m.routingDirectory())
	if enabled, ok := settings[role]; ok {
		return enabled
	}
	return config.DefaultLeshyIPv6
}

// SetRoleIPv6 stores the role's AAAA enable policy and regenerates the
// router configuration. The UI exposes this per role.
func (m *Manager) SetRoleIPv6(role string, enabled bool) error {
	m.mu.Lock()
	_, known := m.roles[role]
	m.mu.Unlock()
	if !known {
		return fmt.Errorf("unknown role %q", role)
	}
	if _, err := config.WriteRoleIPv6Setting(m.routingDirectory(), role, enabled); err != nil {
		return err
	}
	if err := m.RegenerateLeshyConfig(); err != nil {
		return err
	}
	// Subscribers read the policy back from the snapshot: without the bump a
	// UI switch showed its own optimistic value until an unrelated change
	// happened to refresh the revision.
	m.mu.Lock()
	m.bumpLocked()
	m.mu.Unlock()
	return nil
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

// SetBypassMode moves the global default route as one route-authority
// transaction: the old synthetic zone is drained under its old owner first, the
// new owner is committed and Leshy reloads, and only then does the core accept
// intents for the new owner. A failed rewrite or reload restores the previous
// mode and returns a visible error instead of exposing a half-switched default
// path (PROD2A, 2026-11-16 audit §3).
func (m *Manager) SetBypassMode(mode string) error {
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
	if oldOwner != "" && oldOwner != newOwner && m.selectedRoutes != nil && m.selectedRoutes.ManagesRole(oldOwner) {
		if err := m.selectedRoutes.FlushZone(ctx, config.BypassList); err != nil {
			return fmt.Errorf("drain the global default-route zone from role %q: %w", oldOwner, err)
		}
	}

	if err := config.WriteBypassMode(m.bypassPath(), mode); err != nil {
		return err
	}
	if err := m.RegenerateLeshyConfig(); err != nil {
		return errors.Join(fmt.Errorf("write the generated router configuration: %w", err),
			m.restoreBypassMode(ctx, previous))
	}
	if err := m.reloadLeshy(ctx); err != nil {
		return errors.Join(fmt.Errorf("reload Leshy after the global default-route change: %w", err),
			m.restoreBypassMode(ctx, previous))
	}

	m.mu.Lock()
	m.bypassMode = mode
	m.bypassModeLoaded = true
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// restoreBypassMode returns the default route to its previous owner after a
// failed commit. Best effort: the caller's error stays visible even when the
// rollback itself fails.
func (m *Manager) restoreBypassMode(ctx context.Context, previous string) error {
	if previous == "" {
		previous = config.BypassDirect
	}
	if err := config.WriteBypassMode(m.bypassPath(), previous); err != nil {
		return fmt.Errorf("restore the previous default route: %w", err)
	}
	if err := m.RegenerateLeshyConfig(); err != nil {
		return fmt.Errorf("restore the generated router configuration: %w", err)
	}
	if err := m.reloadLeshy(ctx); err != nil {
		return fmt.Errorf("restore the Leshy reload: %w", err)
	}
	return nil
}

// RoutingList returns the current content of one list.
func (m *Manager) RoutingList(name string) (string, error) {
	return config.ReadRoutingList(m.routingDirectory(), name)
}

// SetRoutingList validates and replaces one list.
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

// reloadLeshy restarts the router so it re-reads the lists. The hook exists so tests do not
// restart a real service.
func (m *Manager) reloadLeshy(ctx context.Context) error {
	if m.reloadHook != nil {
		return m.reloadHook(ctx)
	}
	return config.ReloadLeshy(ctx)
}
