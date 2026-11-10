package control

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
	"github.com/smollgreymouse/kikimora/toad/internal/lockfile"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
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
	// TransitionIDFingerprint is the short hash of the in-flight transition
	// id (D9 §9.2). Never the full id, never a token.
	TransitionIDFingerprint string `json:"mode_transition_id_fingerprint,omitempty"`
}

// ManagedLeshyMode returns the committed mode without touching the kernel.
// It reports the applied in-memory state only: before StartupManagedAuthority
// completes there is no applied state, and a deferred install must not
// resurrect a pre-recovery marker snapshot (packet §5.1).
func (m *Manager) ManagedLeshyMode() ManagedLeshyModeStatus {
	m.mu.Lock()
	socket := m.managedSocket
	generation := m.modeGeneration
	activationID := m.managedActivationID
	transitionInFlight := m.modeTransitionInFlight
	transitionPhase := m.modeTransitionPhase
	transitionFingerprint := m.modeTransitionIDFingerprint
	m.mu.Unlock()
	status := ManagedLeshyModeStatus{Mode: string(ManagedLeshyLocal), Generation: generation}
	if socket != "" {
		status.Mode = string(ManagedLeshyManaged)
		status.Socket = socket
		status.ActivationIDFingerprint = activationIDFingerprint(activationID)
	}
	if transitionInFlight {
		// D9 §9.2: the live gate is the phase authority; the fingerprint
		// survives until the journal is durably removed.
		if transitionPhase == "" {
			transitionPhase = string(ManagedTransitionPrepared)
		}
		status.Transition = transitionPhase
		status.TransitionIDFingerprint = transitionFingerprint
	} else if m.stateDir != "" {
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

	// D7R §7.1/§7.2: the gate is armed right after the lock, and the strict
	// Enable preconditions run before anything durable is mutated. An Enable
	// while already Managed refuses with zero config/marker/generation
	// mutation (P0-11).
	m.activateModeTransitionGate()
	defer m.settleModeTransitionGate()

	if _, found, err := ReadTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	} else if found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("a mode transition journal already exists; recover it before a new transition")
	}
	if _, found, err := m.readManagedMarkerFile(); err != nil {
		return ManagedLeshyModeStatus{}, err
	} else if found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("managed mode is already committed; disable it before a new enable")
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
	if err := m.requireNoSelectedCheckpoints(zoneNames); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	rendered, err := config.RenderLeshyConfig(server, configZones)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	digest := configDigest(rendered)
	transition, err := NewManagedLeshyTransition(ManagedLeshyLocal, ManagedLeshyManaged, socket, "", activationID, from, zoneNames, digest)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	m.setModeTransitionProgress(transition)
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
	m.setModeTransitionProgress(transition)
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("enable", "after_config_written")

	if err := m.writeManagedMarker(socket, zoneNames, activationID, to); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionMarkerWritten
	m.setModeTransitionProgress(transition)
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
	wantAuth := PrivilegedAuthKeyPath(m.stateDir)
	if authFile != "" && authFile != wantAuth {
		return ManagedLeshyModeStatus{}, fmt.Errorf("kikimora_auth_file %q does not match the core credential %q", authFile, wantAuth)
	}

	lock, err := lockfile.Acquire(m.writerLockFile())
	if err != nil {
		return ManagedLeshyModeStatus{}, fmt.Errorf("the Leshy writer lock is busy (stop Leshy before changing the mode): %w", err)
	}
	defer lock.Close()

	// D7R §7.1/§7.3: the gate is armed after the lock; the final precondition
	// check runs while it is active. Only then may the Disable journal be
	// created.
	m.activateModeTransitionGate()
	defer m.settleModeTransitionGate()

	if _, found, err := ReadTransitionJournal(m.stateDir); err != nil {
		return ManagedLeshyModeStatus{}, err
	} else if found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("a mode transition journal already exists; recover it before a new transition")
	}
	if err := m.disablePreconditions(); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	// The committed descriptor must be Managed, and the durable marker must
	// equal it: a Disable without a marker is an explicit refusal (P0-11).
	marker, found, err := m.readManagedMarkerFile()
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if !found {
		return ManagedLeshyModeStatus{}, fmt.Errorf("managed mode is not committed; there is nothing to disable")
	}
	from, err := ReadCommittedModeGeneration(m.stateDir)
	if err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	if from == ^uint64(0) {
		return ManagedLeshyModeStatus{}, fmt.Errorf("mode generation overflow: committed generation is MaxUint64")
	}
	if marker.ModeGeneration != from {
		return ManagedLeshyModeStatus{}, fmt.Errorf(
			"the managed marker generation %d does not equal the committed generation %d", marker.ModeGeneration, from)
	}
	m.mu.Lock()
	if m.managedActivationID != marker.ActivationID || m.managedSocket != marker.Socket {
		m.mu.Unlock()
		return ManagedLeshyModeStatus{}, fmt.Errorf("the applied managed state does not match the committed marker")
	}
	fromActivation := marker.ActivationID
	socket := marker.Socket
	m.mu.Unlock()
	to := from + 1
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
	m.setModeTransitionProgress(transition)
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
	m.setModeTransitionProgress(transition)
	if err := WriteTransitionJournal(m.stateDir, *transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	managedModeFailpoint("disable", "after_config_written")

	if err := m.clearManagedMarker(); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	// D7R §6.4: the local target removes the now-empty current-activation
	// checkpoints after the marker operation is durable and before the target
	// generation is committed. A non-empty checkpoint fails the transaction
	// closed with the journal still in place.
	if err := m.drainEmptySelectedCheckpoints(transition); err != nil {
		return ManagedLeshyModeStatus{}, err
	}
	transition.Phase = ManagedTransitionMarkerWritten
	m.setModeTransitionProgress(transition)
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
// use: every role must be stopped, no parked prefix may exist, no recovery may
// be in flight, and the selected registry must be fully drained — no record,
// no park transaction, no in-flight mutation (D7R §7.3).
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
	// The parent contract: the selected registry is empty, Park is nil for
	// every record and selected inFlight is 0 for every managed role before
	// Managed -> Local.
	if m.selectedRoutes != nil {
		m.mu.Lock()
		managed := make([]string, 0, len(m.managedSelectedRoles))
		for name, on := range m.managedSelectedRoles {
			if on {
				managed = append(managed, name)
			}
		}
		m.mu.Unlock()
		for _, role := range managed {
			if err := m.selectedRoutes.selectedDrainViolation(role); err != nil {
				return err
			}
		}
	}
	return nil
}

// drainEmptySelectedCheckpoints enforces the D7R §6.4/§6.5 local-target
// cleanup: every selected-routes.json left behind by the From activation must
// be an EMPTY checkpoint fenced to exactly the journal's from-activation;
// such a file is durably removed. Missing files are fine. Non-empty ownership
// evidence, a foreign fence or a malformed file fails closed — ownership
// evidence is never discarded to satisfy a rollback.
func (m *Manager) drainEmptySelectedCheckpoints(transition *ManagedLeshyTransition) error {
	if transition.To != ManagedLeshyLocal || m.selectedRoutes == nil {
		return nil
	}
	m.mu.Lock()
	roles := make([]string, 0, len(m.roles))
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zone := r.cfg.EffectiveEndpointPolicy().Zone
		for _, managed := range transition.Zones {
			if zone != "" && zone == managed {
				roles = append(roles, name)
				break
			}
		}
	}
	m.mu.Unlock()
	for _, role := range roles {
		dir, ok := m.selectedRoutes.stateDir(role)
		if !ok || dir == "" {
			continue
		}
		path := filepath.Join(dir, "selected-routes.json")
		checkpoint, err := routing.ReadSelectedRoutesCheckpoint(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("drain the local target: managed checkpoint %s for role %q: %w", path, role, err)
		}
		if checkpoint.ActivationID != transition.FromActivationID || checkpoint.ModeGeneration != transition.FromGeneration {
			return fmt.Errorf(
				"drain the local target: checkpoint %s is fenced to activation %s generation %d, not the journal's from-activation %s generation %d",
				path, checkpoint.ActivationID, checkpoint.ModeGeneration,
				transition.FromActivationID, transition.FromGeneration)
		}
		if len(checkpoint.Entries) != 0 {
			return fmt.Errorf(
				"drain the local target: checkpoint %s still owns %d selected entries; refusing the rollback",
				path, len(checkpoint.Entries))
		}
		if err := fsutil.RemoveDurable(path); err != nil {
			return fmt.Errorf("drain the local target: remove %s: %w", path, err)
		}
	}
	return nil
}

// The live mode-transition gate (D7R §7.1). The gate is armed after the
// shared writer lock is acquired and before the first durable transition
// mutation; every route mutation API denies while it is active. It clears
// only when the attempt finished with no journal on disk — failed before
// journal creation, or the committed transaction removed the journal. An
// error after journal creation leaves the gate armed: the core stays
// fail-closed and requires recovery (P0-9).

// activateModeTransitionGate arms the gate in the prepared phase.
func (m *Manager) activateModeTransitionGate() {
	m.mu.Lock()
	m.modeTransitionInFlight = true
	m.modeTransitionPhase = string(ManagedTransitionPrepared)
	m.modeTransitionIDFingerprint = ""
	m.mu.Unlock()
}

// setModeTransitionProgress records the journal phase and the transition id
// fingerprint while the transaction advances.
func (m *Manager) setModeTransitionProgress(transition *ManagedLeshyTransition) {
	m.mu.Lock()
	m.modeTransitionPhase = string(transition.Phase)
	m.modeTransitionIDFingerprint = transitionIDFingerprint(transition.ID)
	m.mu.Unlock()
}

// settleModeTransitionGate clears the gate exactly when no journal exists on
// disk. A journal read error or a surviving journal keeps it armed.
func (m *Manager) settleModeTransitionGate() {
	if m.stateDir == "" {
		return
	}
	_, found, err := ReadTransitionJournal(m.stateDir)
	if err != nil || found {
		return
	}
	m.mu.Lock()
	m.modeTransitionInFlight = false
	m.modeTransitionPhase = ""
	m.modeTransitionIDFingerprint = ""
	m.mu.Unlock()
}

// modeTransitionGateActive reports the live gate for the route mutation
// APIs (§7.1).
func (m *Manager) modeTransitionGateActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modeTransitionInFlight
}

// transitionIDFingerprint renders the diagnostics-safe transition identity
// (D9 §9.2): the first 12 hex characters of SHA-256(transition.ID).
func transitionIDFingerprint(id string) string {
	if id == "" {
		return ""
	}
	return sha256Hex([]byte(id))[:12]
}

// requireNoSelectedCheckpoints denies an Enable while a selected checkpoint
// file from an earlier activation survives (§7.2): ownership evidence from a
// previous managed era must be resolved before a new activation is journaled.
func (m *Manager) requireNoSelectedCheckpoints(zones []string) error {
	if m.selectedRoutes == nil {
		return nil
	}
	m.mu.Lock()
	roles := make([]string, 0, len(m.roles))
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zone := r.cfg.EffectiveEndpointPolicy().Zone
		for _, managed := range zones {
			if zone != "" && zone == managed {
				roles = append(roles, name)
				break
			}
		}
	}
	m.mu.Unlock()
	for _, role := range roles {
		dir, ok := m.selectedRoutes.stateDir(role)
		if !ok || dir == "" {
			continue
		}
		path := filepath.Join(dir, "selected-routes.json")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf(
				"role %q still has a selected-routes checkpoint %s from an earlier activation; recover or drain it before enabling managed mode",
				role, path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
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

// RecoverManagedModeTransition rolls a journaled mode transition forward
// (Production Plan D7). Startup calls it before the manager restores the
// committed mode; there is no inference from timestamps and no guess at a
// rollback: the journal names the target, the committed counter decides
// whether the target still has to be applied or only verified.
func (m *Manager) RecoverManagedModeTransition() (bool, error) {
	if m.stateDir == "" {
		return false, nil
	}
	transition, found, err := ReadTransitionJournal(m.stateDir)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	lock, err := lockfile.Acquire(m.writerLockFile())
	if err != nil {
		return true, fmt.Errorf("recover the leshy-mode transition: the writer lock is busy: %w", err)
	}
	defer lock.Close()

	committed, err := ReadCommittedModeGeneration(m.stateDir)
	if err != nil {
		return true, err
	}
	decision, err := planTransitionRecovery(committed, transition)
	if err != nil {
		return true, err
	}
	path := m.leshyConfigFile()
	switch decision {
	case transitionRecoveryRollForward:
		if err := m.rollForwardTransition(path, transition); err != nil {
			return true, err
		}
	case transitionRecoveryVerifyAndClean:
		if err := m.verifyTransitionTarget(path, transition); err != nil {
			return true, err
		}
	}
	if err := RemoveTransitionJournal(m.stateDir); err != nil {
		return true, err
	}
	// D7R §7.1: the recovery durably removed the journal — the live gate may
	// clear. On the production startup path the fresh manager carries no
	// gate; this settles a live-core recovery.
	m.settleModeTransitionGate()
	return true, nil
}

// rollForwardTransition applies the journal target when the generation commit
// did not happen yet. Every step is idempotent so a repeated crash converges.
func (m *Manager) rollForwardTransition(path string, transition *ManagedLeshyTransition) error {
	mode := transition.To
	socket := ""
	activationID := ""
	if mode == ManagedLeshyManaged {
		socket = transition.Socket
		activationID = transition.ToActivationID
	}
	if err := m.RegenerateLeshyConfigForMode(mode, socket, activationID, transition.ToGeneration); err != nil {
		return fmt.Errorf("roll the mode transition forward: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("roll the mode transition forward: read back the config: %w", err)
	}
	if got := configDigest(data); got != transition.ConfigSHA256 {
		return fmt.Errorf("roll the mode transition forward: rendered digest %s does not match the journaled %s", got, transition.ConfigSHA256)
	}
	if mode == ManagedLeshyManaged {
		if err := m.writeManagedMarker(transition.Socket, transition.Zones, transition.ToActivationID, transition.ToGeneration); err != nil {
			return err
		}
	} else {
		if err := m.clearManagedMarker(); err != nil {
			return err
		}
		// D7R §6.5: the local-target recovery performs the same empty
		// checkpoint drain before the target generation is committed.
		if err := m.drainEmptySelectedCheckpoints(transition); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.managedSocket = socket
	m.managedActivationID = activationID
	m.modeGeneration = transition.ToGeneration
	m.mu.Unlock()
	if err := WriteCommittedModeGeneration(m.stateDir, transition.ToGeneration); err != nil {
		return err
	}
	return nil
}

// verifyTransitionTarget proves the durable state already matches a committed
// journal before the journal is removed. For a Managed target the marker must
// equal the journal exactly — schema, socket, managed zones, activation id and
// generation (D7R §7.4, P0-13). For a Local target the marker must be absent,
// the empty-checkpoint drain complete and the generated config local.
func (m *Manager) verifyTransitionTarget(path string, transition *ManagedLeshyTransition) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify the mode transition target: %w", err)
	}
	if got := configDigest(data); got != transition.ConfigSHA256 {
		return fmt.Errorf("verify the mode transition target: config digest %s does not match the journaled %s", got, transition.ConfigSHA256)
	}
	marker, found, err := m.readManagedMarkerFile()
	if err != nil {
		return err
	}
	if transition.To == ManagedLeshyManaged {
		if !found {
			return fmt.Errorf("verify the mode transition target: the schema2 marker is missing")
		}
		switch {
		case marker.Socket != transition.Socket:
			return fmt.Errorf("verify the mode transition target: marker socket %q does not match the journal %q", marker.Socket, transition.Socket)
		case !equalZoneSets(marker.ManagedZones, transition.Zones):
			return fmt.Errorf("verify the mode transition target: marker zones %v do not match the journal zones %v", marker.ManagedZones, transition.Zones)
		case marker.ActivationID != transition.ToActivationID:
			return fmt.Errorf("verify the mode transition target: marker activation does not match the journal")
		case marker.ModeGeneration != transition.ToGeneration:
			return fmt.Errorf("verify the mode transition target: marker generation %d does not match the journal generation %d", marker.ModeGeneration, transition.ToGeneration)
		}
	} else {
		if found {
			return fmt.Errorf("verify the mode transition target: a managed marker survived a rollback")
		}
		// The generated config must be local (D7R §7.4): the digest pins the
		// bytes, this pins the semantics.
		server, err := config.ReadLeshyServerStrict(path)
		if err != nil {
			return fmt.Errorf("verify the mode transition target: %w", err)
		}
		if server.RoutingBackend != "" || server.KikimoraSocket != "" || server.KikimoraAuthFile != "" ||
			server.KikimoraActivationID != "" || server.KikimoraModeGeneration != 0 {
			return fmt.Errorf("verify the mode transition target: the local config still carries managed-only fields")
		}
		// D7R §6.5: the committed local target requires the empty-checkpoint
		// drain to be complete before the journal may be removed.
		if err := m.drainEmptySelectedCheckpoints(transition); err != nil {
			return fmt.Errorf("verify the mode transition target: %w", err)
		}
	}
	return nil
}

// equalZoneSets compares two canonical sorted zone sets.
func equalZoneSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// managedModeMethods are the privileged schema2 activation methods (API v4).
var managedModeMethods = map[string]bool{
	"GetManagedLeshyMode": true,
	"EnableManagedLeshy":  true,
	"DisableManagedLeshy": true,
}
