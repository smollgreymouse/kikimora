package control

import (
	"context"
	"fmt"
	"sort"

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

// CreateProfile adds profile to every toad configuration the manager knows and reports how
// many files changed. Like CreateTunnel, the new profile needs a core restart to appear in
// the snapshot.
func (m *Manager) CreateProfile(profile string) (int, error) {
	if m.configDir == "" {
		return 0, fmt.Errorf("the core has no configuration directory")
	}
	return config.CreateProfile(m.configDir, profile)
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
	return append(names, config.BypassList)
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

	names := make([]string, 0, len(m.roles))
	for name := range m.roles {
		names = append(names, name)
	}
	sort.Strings(names)

	zones := make([]config.LeshyZoneSpec, 0, len(names))
	for _, name := range names {
		content, err := config.ReadRoutingList(m.routingDirectory(), name)
		if err != nil {
			return err
		}
		entries, err := config.ValidateRoutingContent(content)
		if err != nil {
			return fmt.Errorf("%s list: %w", name, err)
		}
		zones = append(zones, config.LeshyZoneSpec{
			Name:        name,
			RouteTarget: config.RoleZoneTarget(vpnDir, name),
			Domains:     entries,
		})
	}
	// The bypass mode decides what happens to traffic no zone claimed: straight out, or through
	// one role's tunnel as the default. Leshy matches patterns as regular expressions, so the
	// catch-all is ".*", not "*".
	mode := config.ReadBypassMode(m.bypassPath())
	if mode != config.BypassDirect {
		zones = append(zones, config.LeshyZoneSpec{
			Name:        config.BypassList,
			RouteTarget: config.RoleZoneTarget(vpnDir, mode),
			Patterns:    []string{".*"},
		})
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
