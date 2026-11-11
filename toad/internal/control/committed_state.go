package control

import (
	"fmt"

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
