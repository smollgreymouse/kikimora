package control

import (
	"fmt"
	"os"
	"sort"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
	"github.com/smollgreymouse/kikimora/toad/internal/lockfile"
)

// The K17 schema2 activation transaction (Production Plan D3-D7).
//
// Enable and disable are the only two ways the installation changes managed
// mode. Both run under the shared Leshy writer lock, record every durable step
// in the core-owned journal first, and leave a recovery-able record if they
// stop halfway. The runtime never infers a mode from whichever of
// config/marker happens to exist.

// RequiredManagedLeshyCapabilities is the exact capability set a v2 activation
// needs from the installed Leshy (08D1 §31.26). A v1-only Leshy is rejected:
// without the privileged credential and the v2 fence the core cannot own the
// FIB safely.
var RequiredManagedLeshyCapabilities = []string{
	"af_unix_control",
	"managed_selected_routes_v2",
	"privileged_control_auth_v1",
}

// ValidateManagedLeshyCapabilities reports the first missing capability.
func ValidateManagedLeshyCapabilities(capabilities []string) error {
	present := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		present[capability] = true
	}
	for _, required := range RequiredManagedLeshyCapabilities {
		if !present[required] {
			return fmt.Errorf("installed Leshy does not advertise %q; managed mode requires %v", required, RequiredManagedLeshyCapabilities)
		}
	}
	return nil
}

// ManagedLeshyModeStatus is the read-only view of the committed mode.
type ManagedLeshyModeStatus struct {
	Mode                    string `json:"mode"`
	Socket                  string `json:"socket,omitempty"`
	Generation              uint64 `json:"generation"`
	ActivationIDFingerprint string `json:"activation_id_fingerprint,omitempty"`
	Transition              string `json:"transition,omitempty"`
}

// ManagedLeshyMode returns the committed mode without touching the kernel.
func (m *Manager) ManagedLeshyMode() ManagedLeshyModeStatus {
	m.mu.Lock()
	socket := m.managedSocket
	generation := m.modeGeneration
	activationID := m.managedActivationID
	pending := m.pendingManagedMarker
	m.mu.Unlock()
	if socket == "" && pending != nil {
		socket = pending.Socket
		generation = pending.ModeGeneration
		activationID = pending.ActivationID
	}
	status := ManagedLeshyModeStatus{Mode: string(ManagedLeshyLocal), Generation: generation}
	if socket != "" {
		status.Mode = string(ManagedLeshyManaged)
		status.Socket = socket
		status.ActivationIDFingerprint = activationIDFingerprint(activationID)
	}
	if m.stateDir != "" {
		if transition, found, err := ReadTransitionJournal(m.stateDir); err == nil && found {
			status.Transition = string(transition.Phase)
		}
	}
	return status
}

func (m *Manager) leshyConfigFile() string {
	if m.leshyConfigPath != "" {
		return m.leshyConfigPath
	}
	return config.DefaultLeshyConfigPath()
}

func (m *Manager) writerLockFile() string {
	if m.writerLockPath != "" {
		return m.writerLockPath
	}
	return config.LeshyWriterLockPath()
}

// managedZoneNames returns the canonical zone set a mode transaction records.
func (m *Manager) managedZoneNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	zones := make([]string, 0, len(m.roles))
	for _, role := range m.roles {
		if role.cfg == nil {
			continue
		}
		if zone := role.cfg.EffectiveEndpointPolicy().Zone; zone != "" {
			zones = append(zones, zone)
		}
	}
	sort.Strings(zones)
	return zones
}

// EnableManagedLeshy performs the schema2 Local -> Managed transaction.
func (m *Manager) EnableManagedLeshy(socket, authFile string, capabilities []string) (ManagedLeshyModeStatus, error) {
	if socket == "" {
		return ManagedLeshyModeStatus{}, fmt.Errorf("managed mode requires the kikimora_socket path")
	}
	if m.stateDir == "" {
		return ManagedLeshyModeStatus{}, fmt.Errorf("managed activation requires the core state directory")
	}
	if err := ValidateManagedLeshyCapabilities(capabilities); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	wantAuth := PrivilegedAuthKeyPath(m.stateDir)
	if authFile != "" && authFile != wantAuth {
		return ManagedLeshyModeStatus{}, fmt.Errorf("kikimora_auth_file %q does not match the core credential %q", authFile, wantAuth)
	}

	lock, err := lockfile.Acquire(m.writerLockFile())
	if err != nil {
		return ManagedLeshyModeStatus{}, fmt.Errorf("the Leshy writer lock is busy (stop Leshy before changing the mode): %w", err)
	}
	defer lock.Close()

	if _, found, err := ReadTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	} else if found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("a mode transition journal already exists; recover it before a new transition")
	}
	from, err := ReadCommittedModeGeneration(m.stateDir)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if from == ^uint64(0) {
		return ManagedLeshyModeStatus{}, fmt.Errorf("mode generation overflow: committed generation is MaxUint64")
	}
	to := from + 1
	activationID, err := NewActivationID()
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	path := m.leshyConfigFile()
	server, err := m.buildLeshyServer(path, socket, activationID, to)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	configZones, err := m.buildLeshyZones()
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	zoneNames := m.managedZoneNames()
	rendered, err := config.RenderLeshyConfig(server, configZones)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	digest := configDigest(rendered)
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged, socket, "", activationID, from, zoneNames, digest)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("enable", "after_prepared")

	if err := fsutil.WriteAtomicFile(path, rendered, 0o644); err != nil {
		return ManagedLeshyModeStatus{}, fmt.Errorf("write managed Leshy config: %w", err)
	}
	if err := verifyConfigDigest(path, digest); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionConfigWritten
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("enable", "after_config_written")

	if err := m.writeManagedMarker(socket, zoneNames, activationID, to); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionMarkerWritten
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("enable", "after_marker_written")

	m.mu.Lock()
	m.managedSocket = socket
	m.managedActivationID = activationID
	m.modeGeneration = to
	m.activationIDFingerprint = activationIDFingerprint(activationID)
	for name, role := range m.roles {
		if role.cfg == nil {
			continue
		}
		if zone := role.cfg.EffectiveEndpointPolicy().Zone; zone != "" {
			m.managedSelectedRoles[name] = true
		}
	}
	m.mu.Unlock()
	managedModeFailpoint("enable", "after_apply")

	if err := WriteCommittedModeGeneration(m.stateDir, to); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("enable", "after_generation_commit")

	if err := RemoveTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	return m.ManagedLeshyMode(), nil
}

// DisableManagedLeshy performs the schema2 Managed -> Local transaction.
func (m *Manager) DisableManagedLeshy(authFile string) (ManagedLeshyModeStatus, error) {
	if m.stateDir == "" {
		return ManagedLeshyModeStatus{}, fmt.Errorf("managed mode requires the core state directory")
	}
	if err := m.disablePreconditions(); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	wantAuth := PrivilegedAuthKeyPath(m.stateDir)
	if authFile != "" && authFile != wantAuth {
		return ManagedLeshyModeStatus{}, fmt.Errorf("kikimora_auth_file %q does not match the core credential %q", authFile, wantAuth)
	}

	lock, err := lockfile.Acquire(m.writerLockFile())
	if err != nil {
		return ManagedLeshyModeStatus{}, fmt.Errorf("the Leshy writer lock is busy (stop Leshy before changing the mode): %w", err)
	}
	defer lock.Close()

	if _, found, err := ReadTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	} else if found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("a mode transition journal already exists; recover it before a new transition")
	}
	from, err := ReadCommittedModeGeneration(m.stateDir)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if from == ^uint64(0) {
		return ManagedLeshyModeStatus{}, fmt.Errorf("mode generation overflow: committed generation is MaxUint64")
	}
	to := from + 1
	m.mu.Lock()
	fromActivation := m.managedActivationID
	socket := m.managedSocket
	m.mu.Unlock()
	path := m.leshyConfigFile()
	server, err := m.buildLeshyServer(path, "", "", 0)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	configZones, err := m.buildLeshyZones()
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	zoneNames := m.managedZoneNames()
	rendered, err := config.RenderLeshyConfig(server, configZones)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	digest := configDigest(rendered)
	transition, err := NewManagedLeshyTransition(ManagedLeshyManaged, ManagedLeshyLocal, socket, fromActivation, "", from, zoneNames, digest)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("disable", "after_prepared")

	if err := fsutil.WriteAtomicFile(path, rendered, 0o644); err != nil {
		return ManagedLeshyModeStatus{}, fmt.Errorf("write local Leshy config: %w", err)
	}
	if err := verifyConfigDigest(path, digest); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionConfigWritten
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("disable", "after_config_written")

	if err := m.clearManagedMarker(); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionMarkerWritten
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("disable", "after_marker_written")

	m.mu.Lock()
	m.managedSocket = ""
	m.managedActivationID = ""
	m.modeGeneration = to
	m.activationIDFingerprint = ""
	for name := range m.managedSelectedRoles {
		m.managedSelectedRoles[name] = false
	}
	m.mu.Unlock()
	managedModeFailpoint("disable", "after_apply")

	if err := WriteCommittedModeGeneration(m.stateDir, to); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("disable", "after_generation_commit")

	if err := RemoveTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	return m.ManagedLeshyMode(), nil
}

// disablePreconditions refuses a rollback while the managed writer is still in
// use: every role must be stopped, no parked prefix may exist and no recovery
// may be in flight.
func (m *Manager) disablePreconditions() error {
	snapshot := m.Snapshot()
	for _, role := range snapshot.Roles {
		switch role.State {
		case "Stopped", "Disconnected", "Failed":
		default:
			return fmt.Errorf("role %q is still %s; stop every role before disabling managed mode", role.ID, role.State)
		}
		if role.Parking.Active || role.Parking.Count > 0 {
			return fmt.Errorf("role %q still has parked prefixes", role.ID)
		}
		if role.Recovery.Step != "" {
			return fmt.Errorf("role %q has a recovery transaction in flight", role.ID)
		}
	}
	return nil
}

// verifyConfigDigest proves the durable file carries exactly the bytes the
// journal recorded.
func verifyConfigDigest(path, digest string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read back the generated Leshy config: %w", err)
	}
	if got := configDigest(data); got != digest {
		return fmt.Errorf("generated Leshy config digest %s does not match the journaled %s", got, digest)
	}
	return nil
}

// managedModeMethods are the privileged schema2 activation methods (API v4).
var managedModeMethods = map[string]bool{
	"GetManagedLeshyMode": true,
	"EnableManagedLeshy":  true,
	"DisableManagedLeshy": true,
}
