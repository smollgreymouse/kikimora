package control

import (
	"context"
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
// the UI uses when a role is dragged from one profile to another.
func (m *Manager) SetTunnelProfiles(name string, profiles []string) (string, error) {
	if m.configDir == "" {
		return "", fmt.Errorf("the core has no configuration directory")
	}
	return config.SetTunnelProfiles(m.configDir, name, profiles)
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
	m.mu.Unlock()

	for _, change := range changes {
		if m.configDir == "" {
			continue
		}
		if _, err := config.SetTunnelProfiles(m.configDir, change.role, change.profiles); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, change := range changes {
		if r, ok := m.roles[change.role]; ok {
			r.cfg.Profiles = change.profiles
		}
	}
	m.rebuildProfileMapLocked()
	m.bumpLocked()
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
	server := config.ReadLeshyServer(path)
	vpnDir := config.DefaultLeshyVPNDir()

	zones, err := config.BuildLeshyZones(m.routingDirectory(), vpnDir, config.ReadBypassMode(m.bypassPath()))
	if err != nil {
		return err
	}
	return config.WriteLeshyConfig(path, server, zones)
}

// bypassPath is where the global bypass decision lives; overridable in tests.
func (m *Manager) bypassPath() string {
	if m.bypassConfigPath != "" {
		return m.bypassConfigPath
	}
	return config.DefaultBypassPath()
}

// BypassMode returns the current global decision: "direct" or a role name.
func (m *Manager) BypassMode() string {
	return config.ReadBypassMode(m.bypassPath())
}

// SetBypassMode stores the global decision and rewrites the router configuration.
func (m *Manager) SetBypassMode(mode string) error {
	if err := config.WriteBypassMode(m.bypassPath(), mode); err != nil {
		return err
	}
	return m.RegenerateLeshyConfig()
}

// RoutingList returns the current content of one list.
func (m *Manager) RoutingList(name string) (string, error) {
	return config.ReadRoutingList(m.routingDirectory(), name)
}

// SetRoutingList validates and replaces one list.
func (m *Manager) SetRoutingList(name, content string) error {
	return config.WriteRoutingList(m.routingDirectory(), name, content)
}

// reloadLeshy restarts the router so it re-reads the lists. The hook exists so tests do not
// restart a real service.
func (m *Manager) reloadLeshy(ctx context.Context) error {
	if m.reloadHook != nil {
		return m.reloadHook(ctx)
	}
	return config.ReloadLeshy(ctx)
}
