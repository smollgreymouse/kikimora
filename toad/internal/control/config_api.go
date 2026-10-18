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

// CreateProfile adds profile to every toad configuration the manager knows and reports how
// many files changed. Like CreateTunnel, the new profile needs a core restart to appear in
// the snapshot.
func (m *Manager) CreateProfile(profile string) (int, error) {
	if m.configDir == "" {
		return 0, fmt.Errorf("the core has no configuration directory")
	}
	return config.CreateProfile(m.configDir, profile)
}
