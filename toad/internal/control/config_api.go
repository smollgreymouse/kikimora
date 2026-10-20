package control

import (
	"fmt"

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

// RoutingList returns the current content of one list.
func (m *Manager) RoutingList(list config.RoutingList) (string, error) {
	return config.ReadRoutingList(m.routingDirectory(), list)
}

// SetRoutingList validates and replaces one list.
func (m *Manager) SetRoutingList(list config.RoutingList, content string) error {
	return config.WriteRoutingList(m.routingDirectory(), list, content)
}
