package control

import (
	"errors"
	"path/filepath"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// RuntimePaths overrides the four mutable generated-config surfaces of one
// Manager (U5, docs/roadmap.md §12.1). Production code never sets them: an
// empty field keeps the existing default. Hermetic desktop tests inject a
// temporary root through serve flags so a development core never touches
// /etc/kikimora, /var/lib/kikimora or /run/kikimora.
type RuntimePaths struct {
	// LeshyConfig is the generated router configuration path.
	LeshyConfig string
	// RoutingDir is where the routing lists live.
	RoutingDir string
	// BypassConfig is the global default-route decision file.
	BypassConfig string
	// LeshyPublication is the publication root shared by the generated zone
	// route_target paths and the leshy.FileBridge.
	LeshyPublication string
}

// ErrRuntimePathsClosed is returned when a caller tries to reconfigure the
// runtime paths after StartupManagedAuthority began.
var ErrRuntimePathsClosed = errors.New("runtime paths are closed once StartupManagedAuthority begins")

// SetRuntimePaths applies the overrides. It must be called before
// StartupManagedAuthority: transition recovery and the committed-mode
// restore read and write the generated Leshy config during that sequence,
// so the paths have to be final before it starts. SetRuntimePaths never
// creates files.
func (m *Manager) SetRuntimePaths(paths RuntimePaths) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimePathsClosed {
		return ErrRuntimePathsClosed
	}
	if paths.LeshyConfig != "" {
		m.leshyConfigPath = filepath.Clean(paths.LeshyConfig)
	}
	if paths.RoutingDir != "" {
		m.routingDir = filepath.Clean(paths.RoutingDir)
	}
	if paths.BypassConfig != "" {
		m.bypassConfigPath = filepath.Clean(paths.BypassConfig)
	}
	if paths.LeshyPublication != "" {
		m.leshyPublicationDir = filepath.Clean(paths.LeshyPublication)
	}
	return nil
}

// leshyPublicationDirectory is the one publication root for generated zone
// route_target paths. It must match the leshy.FileBridge directory so a
// generated config and the bridge never disagree about where zones live.
func (m *Manager) leshyPublicationDirectory() string {
	if m.leshyPublicationDir != "" {
		return m.leshyPublicationDir
	}
	return config.DefaultLeshyVPNDir()
}

// closeRuntimePaths freezes the overrides. StartupManagedAuthority calls it
// once at the beginning; later SetRuntimePaths calls fail closed.
func (m *Manager) closeRuntimePaths() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtimePathsClosed = true
}

// SetDiagnosticsHostSources turns the ambient host collectors (journal,
// event log, platform product-log globs, network state commands) on or off.
// Production keeps them enabled; the hermetic Layer-C fixture disables them
// so a diagnostics capture round trip reads nothing from the host. It is not
// exposed as a control API method.
func (m *Manager) SetDiagnosticsHostSources(enabled bool) error {
	m.mu.Lock()
	collector := m.diagnostics
	m.mu.Unlock()
	if collector == nil {
		return errors.New("diagnostics are not configured: call SetDiagnosticsDir first")
	}
	collector.SetHostSources(enabled)
	return nil
}
