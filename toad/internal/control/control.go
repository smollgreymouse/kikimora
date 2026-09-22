// Package control implements the local Kikimora control plane. It supervises
// Toad processes; protocol implementations remain owned by kikimora-toad.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
	"github.com/smollgreymouse/kikimora/toad/internal/supervisor"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
	"github.com/smollgreymouse/kikimora/toad/internal/underlay"
)

const APIVersion = 1

// BuildVersion can be replaced at link time by a release build.
var BuildVersion = "v0.1.0-dev"

type Process interface {
	Wait() error
	Stop() error
}

type Launcher interface {
	Start(context.Context, string) (Process, error)
}

type RoleSnapshot struct {
	ID               string                   `json:"id"`
	Label            string                   `json:"label"`
	Protocol         string                   `json:"protocol"`
	State            string                   `json:"state"`
	Reason           string                   `json:"reason,omitempty"`
	RouteReady       bool                     `json:"route_ready"`
	Interface        state.InterfaceState     `json:"interface"`
	Session          state.SessionState       `json:"session"`
	AvailableActions []string                 `json:"available_actions"`
	DesiredEnabled   bool                     `json:"desired_enabled"`
	Operation        uint64                   `json:"operation"`
	ValidatedEpoch   uint64                   `json:"validated_underlay_epoch"`
	Endpoint         endpoint.State           `json:"endpoint"`
	Publication      leshy.PublicationState   `json:"publication"`
	Parking          parking.State            `json:"parking"`
	Recovery         core.RecoveryState       `json:"recovery"`
	Validation       toadctl.ValidationResult `json:"validation"`
}

type ObserverState struct {
	NetlinkHealthy bool   `json:"netlink_healthy"`
	SleepHealthy   bool   `json:"sleep_healthy"`
	LastError      string `json:"last_error,omitempty"`
}

type Snapshot struct {
	Schema          int               `json:"schema"`
	Revision        uint64            `json:"revision"`
	CoreState       string            `json:"core_state"`
	AggregateState  string            `json:"aggregate_state"`
	ActiveProfile   string            `json:"active_profile"`
	UnderlaySummary string            `json:"underlay_summary"`
	Underlay        netstate.Snapshot `json:"underlay"`
	LeshySupported  bool              `json:"leshy_supported"`
	Observers       ObserverState     `json:"observers"`
	Roles           []RoleSnapshot    `json:"roles"`
	BackendKind     string            `json:"backend_kind,omitempty"`
	Diagnostics     *Diagnostics      `json:"diagnostics,omitempty"`
}

// Diagnostics contains non-secret diagnostic information.
type Diagnostics struct {
	CoreVersion        string                                        `json:"core_version"`
	ProtocolVersion    string                                        `json:"protocol_version"`
	Platform           string                                        `json:"platform"`
	SocketPath         string                                        `json:"socket_path"`
	ManagedInterfaces  map[string]platform.ManagedInterfaceOwnership `json:"managed_interfaces,omitempty"`
	DesiredStateStatus string                                        `json:"desired_state_status,omitempty"`
}

type Manager struct {
	mu                    sync.Mutex
	launcher              Launcher
	roles                 map[string]*role
	revision              uint64
	activeProfile         string
	profileToRoles        map[string][]string
	socketPath            string
	changed               chan struct{}
	underlay              netstate.Snapshot
	product               *core.Controller
	autoRecovery          bool
	backoffs              map[string]*supervisor.Backoff
	recoveryBackoffs      map[string]*supervisor.Backoff
	recoveryRetryPending  map[string]bool
	monitorCancel         context.CancelFunc
	recoveryDriver        core.RecoveryDriver
	interfaceOwnership    platform.ManagedInterfaceVerifier
	managedOwnership      map[string]platform.ManagedInterfaceOwnership
	desiredStore          DesiredStateStore
	desiredStateStatus    string
	shuttingDown          bool
	underlayInvalidations chan netstate.Invalidation
	observers             ObserverState
	suspended             bool
}

type role struct {
	configPath         string
	cfg                *config.Config
	process            Process
	lastError          string
	enabled            bool // desired state: true = should be running
	observed           state.Snapshot
	stateValid         bool
	streamed           bool
	controlSocket      string
	operation          uint64
	validationInFlight bool
}

func NewManager(paths []string, launcher Launcher, socketPath string) (*Manager, error) {
	return newManager(paths, launcher, socketPath, "", "")
}

// NewManagerWithLegacy enables the compatibility reader for the original
// /etc/kikimora/leshy/vpn.conf and its endpoint-provider scripts.
func NewManagerWithLegacy(paths []string, launcher Launcher, socketPath, legacyPath, providerDir string) (*Manager, error) {
	return newManager(paths, launcher, socketPath, legacyPath, providerDir)
}

func newManager(paths []string, launcher Launcher, socketPath, legacyPath, providerDir string) (*Manager, error) {
	if launcher == nil {
		return nil, errors.New("control launcher is nil")
	}
	m := &Manager{
		launcher:              launcher,
		roles:                 make(map[string]*role),
		revision:              1,
		activeProfile:         "default",
		profileToRoles:        make(map[string][]string),
		socketPath:            socketPath,
		changed:               make(chan struct{}),
		underlayInvalidations: make(chan netstate.Invalidation, 64),
		backoffs:              make(map[string]*supervisor.Backoff),
		recoveryBackoffs:      make(map[string]*supervisor.Backoff),
		recoveryRetryPending:  make(map[string]bool),
		interfaceOwnership:    platform.DefaultManagedInterfaceVerifier(),
		managedOwnership:      make(map[string]platform.ManagedInterfaceOwnership),
	}
	for _, path := range paths {
		cfg, err := config.LoadWithLegacy(path, legacyPath, providerDir)
		if err != nil {
			return nil, fmt.Errorf("load Toad config %q: %w", path, err)
		}
		if _, exists := m.roles[cfg.Name]; exists {
			return nil, fmt.Errorf("duplicate Toad name %q", cfg.Name)
		}
		m.roles[cfg.Name] = &role{configPath: path, cfg: cfg}
	}
	if len(m.roles) == 0 {
		return nil, errors.New("at least one -config is required")
	}
	names := m.namesLocked()
	m.profileToRoles["default"] = names
	specs := make([]core.RoleSpec, 0, len(names))
	for _, name := range names {
		r := m.roles[name]
		specs = append(specs, core.RoleSpec{ID: name, Protocol: string(r.cfg.Protocol), Interface: r.cfg.Interface, LeshyZone: r.cfg.EffectiveEndpointPolicy().Zone})
	}
	m.product = core.NewController(specs)
	monitorCtx, cancel := context.WithCancel(context.Background())
	m.monitorCancel = cancel
	go m.watchUnderlay(monitorCtx)
	go m.product.Run(monitorCtx)
	go m.watchSleep(monitorCtx)
	return m, nil
}

func (m *Manager) SetDesiredStateStore(store DesiredStateStore) {
	m.mu.Lock()
	m.desiredStore = store
	m.mu.Unlock()
}

func (m *Manager) SetDesiredStateStatus(status string) {
	m.mu.Lock()
	if m.desiredStateStatus != status {
		m.desiredStateStatus = status
		m.bumpLocked()
	}
	m.mu.Unlock()
}

func (m *Manager) desiredStateLocked() PersistedDesiredState {
	roles := make(map[string]bool, len(m.roles))
	for name, r := range m.roles {
		roles[name] = r.enabled
	}
	return PersistedDesiredState{
		Schema:        DesiredStateSchema,
		ActiveProfile: m.activeProfile,
		Roles:         roles,
	}
}

func (m *Manager) persistDesiredLocked(ctx context.Context) error {
	if m.desiredStore == nil {
		return nil
	}
	if err := m.desiredStore.Save(ctx, m.desiredStateLocked()); err != nil {
		m.desiredStateStatus = "save failed: " + err.Error()
		return err
	}
	m.desiredStateStatus = "persisted"
	return nil
}

// RestoreDesiredState applies persisted operator intent without rewriting the
// store. Process startup uses the same ordinary role launcher as live commands.
func (m *Manager) RestoreDesiredState(ctx context.Context, desired PersistedDesiredState) error {
	m.mu.Lock()
	profile := desired.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	if _, ok := m.profileToRoles[profile]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("persisted active profile %q is not configured", profile)
	}
	m.activeProfile = profile
	names := m.namesLocked()
	start := make([]string, 0, len(names))
	for _, name := range names {
		r := m.roles[name]
		enabled := desired.Roles[name]
		r.enabled = enabled
		_ = m.product.SetRoleDesired(ctx, name, enabled)
		if enabled {
			start = append(start, name)
		}
	}
	m.desiredStateStatus = "restored"
	m.bumpLocked()
	m.mu.Unlock()

	var errs []error
	for _, name := range start {
		if err := m.startRoleProcess(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// ConnectAll enables and starts every configured role.
func (m *Manager) ConnectAll(ctx context.Context) error {
	m.mu.Lock()
	names := m.namesLocked()
	previous := make(map[string]bool, len(names))
	for _, name := range names {
		r := m.roles[name]
		previous[name] = r.enabled
		if !r.enabled {
			r.operation++
		}
		r.enabled = true
		_ = m.product.SetRoleDesired(ctx, name, true)
	}
	if err := m.persistDesiredLocked(ctx); err != nil {
		for _, name := range names {
			r := m.roles[name]
			if r.enabled != previous[name] {
				r.operation--
			}
			r.enabled = previous[name]
			_ = m.product.SetRoleDesired(ctx, name, previous[name])
		}
		m.mu.Unlock()
		return fmt.Errorf("persist desired state: %w", err)
	}
	m.bumpLocked()
	m.mu.Unlock()

	var errs []error
	for _, name := range names {
		if err := m.startRoleProcess(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// DisconnectAll disables and stops every configured role.
func (m *Manager) DisconnectAll() error {
	ctx := context.Background()
	m.mu.Lock()
	names := m.namesLocked()
	previous := make(map[string]bool, len(names))
	for _, name := range names {
		r := m.roles[name]
		previous[name] = r.enabled
		if r.enabled {
			r.operation++
		}
		r.enabled = false
		_ = m.product.SetRoleDesired(ctx, name, false)
	}
	if err := m.persistDesiredLocked(ctx); err != nil {
		for _, name := range names {
			r := m.roles[name]
			if r.enabled != previous[name] {
				r.operation--
			}
			r.enabled = previous[name]
			_ = m.product.SetRoleDesired(ctx, name, previous[name])
		}
		m.mu.Unlock()
		return fmt.Errorf("persist desired state: %w", err)
	}
	m.bumpLocked()
	m.mu.Unlock()

	var errs []error
	for _, name := range names {
		if err := m.stopRoleProcess(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// ConnectRole enables the role and starts its process if not already running.
func (m *Manager) ConnectRole(ctx context.Context, name string) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	wasEnabled, oldOperation := r.enabled, r.operation
	if !r.enabled {
		r.operation++
	}
	r.enabled = true
	_ = m.product.SetRoleDesired(ctx, name, true)
	if err := m.persistDesiredLocked(ctx); err != nil {
		r.enabled = wasEnabled
		r.operation = oldOperation
		_ = m.product.SetRoleDesired(ctx, name, wasEnabled)
		m.mu.Unlock()
		return fmt.Errorf("persist desired state: %w", err)
	}
	m.bumpLocked()
	m.mu.Unlock()
	return m.startRoleProcess(ctx, name)
}

func (m *Manager) startRoleProcess(ctx context.Context, name string) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	if !r.enabled || r.process != nil {
		m.mu.Unlock()
		return nil
	}
	_ = m.product.BeginToadGeneration(name)
	path := r.configPath
	statePath := filepath.Join(r.cfg.StateDir, "state.json")
	r.stateValid = false
	m.mu.Unlock()
	_ = os.Remove(statePath)

	p, err := m.launcher.Start(ctx, path)
	if err != nil {
		m.mu.Lock()
		if current, exists := m.roles[name]; exists {
			current.lastError = err.Error()
			m.bumpLocked()
		}
		m.mu.Unlock()
		return fmt.Errorf("start Toad %q: %w", name, err)
	}

	m.mu.Lock()
	r = m.roles[name]
	if r.process != nil || !r.enabled {
		m.mu.Unlock()
		_ = p.Stop()
		return nil
	}
	r.process = p
	r.controlSocket = filepath.Join(r.cfg.StateDir, "control.sock")
	r.lastError = ""
	m.bumpLocked()
	socket := r.controlSocket
	m.mu.Unlock()
	go m.wait(name, p)
	go m.subscribeToad(name, p, socket)
	return nil
}

// DisconnectRole disables the role and stops its process.
func (m *Manager) DisconnectRole(name string) error {
	ctx := context.Background()
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	wasEnabled, oldOperation := r.enabled, r.operation
	if r.enabled {
		r.operation++
	}
	r.enabled = false
	_ = m.product.SetRoleDesired(ctx, name, false)
	if err := m.persistDesiredLocked(ctx); err != nil {
		r.enabled = wasEnabled
		r.operation = oldOperation
		_ = m.product.SetRoleDesired(ctx, name, wasEnabled)
		m.mu.Unlock()
		return fmt.Errorf("persist desired state: %w", err)
	}
	r.lastError = ""
	m.bumpLocked()
	m.mu.Unlock()
	return m.stopRoleProcess(name)
}

func (m *Manager) stopRoleProcess(name string) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	p := r.process
	controlSocket := r.controlSocket
	r.process = nil
	m.bumpLocked()
	m.mu.Unlock()
	if p != nil {
		m.stopToad(controlSocket)
		if err := p.Stop(); err != nil {
			return fmt.Errorf("stop Toad %q: %w", name, err)
		}
	}
	return nil
}

// RetryRole attempts to restart a failed enabled role.
func (m *Manager) RetryRole(ctx context.Context, name string) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	if r.process != nil || !r.enabled || r.lastError == "" {
		m.mu.Unlock()
		return nil
	}
	r.lastError = ""
	m.bumpLocked()
	_ = m.product.BeginToadGeneration(name)
	path := r.configPath
	statePath := filepath.Join(r.cfg.StateDir, "state.json")
	r.stateValid = false
	m.mu.Unlock()
	_ = os.Remove(statePath)

	p, err := m.launcher.Start(ctx, path)
	if err != nil {
		m.mu.Lock()
		r.lastError = err.Error()
		m.bumpLocked()
		m.mu.Unlock()
		return fmt.Errorf("retry Toad %q: %w", name, err)
	}
	m.mu.Lock()
	if r.process != nil {
		m.mu.Unlock()
		_ = p.Stop()
		return nil
	}
	r.process = p
	m.bumpLocked()
	m.mu.Unlock()
	go m.wait(name, p)
	go m.subscribeToad(name, p, r.controlSocket)
	return nil
}

func (m *Manager) stopToad(socket string) {
	if socket == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = (toadctl.Client{Socket: socket}).Call(ctx, toadctl.Request{Version: toadctl.ProtocolVersion, Method: "Stop"})
}

func (m *Manager) subscribeToad(name string, p Process, socket string) {
	if socket == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The production Toad creates control.sock only after protocol startup and
	// route-target discovery. OpenConnect may legitimately take tens of
	// seconds, so a single early dial must not permanently demote the role to
	// state-file-only observation. Keep compatibility polling while retrying
	// the authoritative stream; it exits as soon as streaming takes over.
	go m.watchState(name, p)

	for {
		err := (toadctl.Client{Socket: socket}).Subscribe(ctx, "core-"+name, func(snapshot toadctl.Snapshot) error {
			live, accepted := m.observeToadSnapshotForProcess(name, p, snapshot)
			if !live {
				return context.Canceled
			}
			if accepted {
				m.scheduleValidation(name, p)
			}
			return nil
		})
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		m.mu.Lock()
		r, ok := m.roles[name]
		live := ok && r.process == p
		m.mu.Unlock()
		if !live {
			return
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) observeToadSnapshotForProcess(name string, p Process, snapshot toadctl.Snapshot) (live bool, accepted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.roles[name]
	if !ok || r.process != p {
		return false, false
	}
	r.observed = stateFromToadSnapshot(name, r.cfg, snapshot)
	r.stateValid = true
	r.streamed = true
	m.roles[name] = r
	m.bumpLocked()

	// Keep process identity and authoritative generation observation in one
	// critical section. Otherwise an old Validate/Subscribe reply can race a
	// replacement process after BeginToadGeneration reset the core generation.
	return true, m.product.ObserveToad(name, snapshot)
}

func (m *Manager) scheduleValidation(name string, p Process) {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok || r.process != p || !r.enabled || !r.stateValid || !r.observed.RouteReady || r.validationInFlight {
		m.mu.Unlock()
		return
	}
	underlay := m.underlay
	productRole, productOK := m.product.Role(name)
	if !productOK || productRole.State == core.RoleRecovering ||
		underlay.Epoch == 0 || (underlay.IPv4 == nil && underlay.IPv6 == nil) ||
		productRole.ValidatedEpoch == underlay.Epoch {
		m.mu.Unlock()
		return
	}
	r.validationInFlight = true
	m.roles[name] = r
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		m.mu.Lock()
		verifier := m.interfaceOwnership
		iface := ""
		if current, currentOK := m.roles[name]; currentOK && current.process == p && current.cfg != nil {
			iface = current.cfg.Interface
		}
		m.mu.Unlock()
		if verifier != nil && iface != "" {
			ownership, err := verifier.EnsureUnmanaged(ctx, iface)
			m.mu.Lock()
			m.managedOwnership[name] = ownership
			current, currentOK := m.roles[name]
			if currentOK && current.process == p && err != nil {
				current.lastError = err.Error()
				m.roles[name] = current
				m.bumpLocked()
			}
			m.mu.Unlock()
			if err != nil || (ownership.Present && ownership.Managed) {
				m.product.MarkRecovering(name, "NetworkManager owns managed Toad interface")
				m.finishValidationSchedule(name, p)
				return
			}
		}

		err := m.ValidateRole(ctx, name)
		if err == nil {
			err = m.activateReadyRole(ctx, name)
		}
		m.finishValidationSchedule(name, p)
		if err != nil {
			m.mu.Lock()
			autoRecovery, driver := m.autoRecovery, m.recoveryDriver
			epoch := m.underlay.Epoch
			m.mu.Unlock()
			if autoRecovery && driver != nil && m.product.MarkRecovering(name, err.Error()) {
				if role, ok := m.product.Role(name); ok {
					go m.recoverRole(driver, name, role.Operation, epoch)
				}
			}
		}
	}()
}

func (m *Manager) activateReadyRole(ctx context.Context, name string) error {
	m.mu.Lock()
	autoRecovery, driver := m.autoRecovery, m.recoveryDriver
	epoch := m.underlay.Epoch
	m.mu.Unlock()
	if !autoRecovery || driver == nil {
		return nil
	}
	role, ok := m.product.Role(name)
	if !ok {
		return fmt.Errorf("unknown product role %q", name)
	}
	if role.Endpoint.AppliedUnderlayEpoch == epoch && role.Endpoint.State == "ready" &&
		role.Publication.Published && !role.Parking.Active {
		return nil
	}
	// A core restart may inherit fail-closed parks/checkpoint state left by the
	// previous process. Reconstruct ownership before republishing the route
	// target, then release parks only after Leshy has restored winning routes.
	if err := driver.ObserveRoutes(ctx, name); err != nil {
		return fmt.Errorf("restore route ownership for %q: %w", name, err)
	}
	if err := driver.ApplyEndpoint(ctx, name); err != nil {
		return fmt.Errorf("apply initial endpoint policy for %q: %w", name, err)
	}
	if err := driver.Publish(ctx, name); err != nil {
		return fmt.Errorf("publish initial Leshy route target for %q: %w", name, err)
	}
	if err := driver.ResyncLeshy(ctx, name); err != nil {
		return fmt.Errorf("resync initial Leshy route target for %q: %w", name, err)
	}
	if err := driver.ObserveRestoration(ctx, name); err != nil {
		return fmt.Errorf("restore selected routes for %q: %w", name, err)
	}
	return nil
}

// SetActiveProfile validates and selects a loaded profile.
func (m *Manager) finishValidationSchedule(name string, p Process) {
	m.mu.Lock()
	current, currentOK := m.roles[name]
	if currentOK && current.process == p {
		current.validationInFlight = false
		m.roles[name] = current
	}
	m.mu.Unlock()
}

func (m *Manager) scheduleCurrentValidations() {
	m.mu.Lock()
	type candidate struct {
		name    string
		process Process
	}
	candidates := make([]candidate, 0, len(m.roles))
	for name, r := range m.roles {
		if r.enabled && r.process != nil && r.stateValid && r.observed.RouteReady {
			candidates = append(candidates, candidate{name: name, process: r.process})
		}
	}
	m.mu.Unlock()
	for _, candidate := range candidates {
		m.scheduleValidation(candidate.name, candidate.process)
	}
}

func (m *Manager) SetActiveProfile(name string) error {
	ctx := context.Background()
	m.mu.Lock()
	if _, ok := m.profileToRoles[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown profile %q", name)
	}
	if m.activeProfile == name {
		m.mu.Unlock()
		return nil
	}
	previous := m.activeProfile
	m.activeProfile = name
	if err := m.persistDesiredLocked(ctx); err != nil {
		m.activeProfile = previous
		m.mu.Unlock()
		return fmt.Errorf("persist active profile: %w", err)
	}
	m.bumpLocked()
	m.mu.Unlock()
	return nil
}

// RediscoverEndpoints re-reads observed state on the next snapshot.
func (m *Manager) RediscoverEndpoints(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[name]; !ok {
		return fmt.Errorf("unknown Toad %q", name)
	}
	m.bumpLocked()
	return nil
}

// ValidateRole asks the live per-Toad control endpoint to validate its current
// transport. The result is also fed through the authoritative core projection.
func (m *Manager) ValidateRole(ctx context.Context, name string) error {
	token, ok := m.product.BeginValidation(name)
	if !ok {
		return fmt.Errorf("Toad %q is not eligible for current-epoch validation", name)
	}

	m.mu.Lock()
	r, roleOK := m.roles[name]
	if !roleOK {
		m.mu.Unlock()
		err := fmt.Errorf("unknown Toad %q", name)
		_ = m.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: false, State: "failed", Reason: err.Error()})
		return err
	}
	socket := r.controlSocket
	p := r.process
	m.mu.Unlock()
	if p == nil || socket == "" {
		err := fmt.Errorf("Toad %q has no live control socket", name)
		_ = m.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: false, State: "failed", Reason: err.Error()})
		return err
	}

	response, err := (toadctl.Client{Socket: socket}).Call(ctx, toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		Method:     "Validate",
		Generation: token.ToadGeneration,
	})
	if err != nil {
		err = fmt.Errorf("validate Toad %q: %w", name, err)
		_ = m.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: false, State: "failed", Reason: err.Error()})
		return err
	}
	if !response.OK {
		var err error
		if response.Error != nil {
			err = response.Error
		} else {
			err = fmt.Errorf("validate Toad %q failed", name)
		}
		_ = m.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: false, State: "failed", Reason: err.Error()})
		return err
	}
	if response.Snapshot != nil {
		live, _ := m.observeToadSnapshotForProcess(name, p, *response.Snapshot)
		if !live {
			return fmt.Errorf("validation result for Toad %q became stale: process changed", name)
		}
	}
	if response.Validation == nil {
		err := fmt.Errorf("validate Toad %q returned no validation result", name)
		_ = m.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: false, State: "failed", Reason: err.Error()})
		return err
	}
	result := *response.Validation
	if !m.completeValidationForProcess(name, p, token, result) {
		return fmt.Errorf("validation result for Toad %q became stale", name)
	}
	if !result.Healthy {
		return fmt.Errorf("Toad %q is not healthy: %s", name, result.Reason)
	}
	return nil
}

func (m *Manager) completeValidationForProcess(name string, p Process, token core.ValidationToken, result toadctl.ValidationResult) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	currentRole, ok := m.roles[name]
	if !ok || currentRole.process != p {
		return false
	}
	if current, exists := m.product.Role(name); exists {
		_ = m.product.UpdateRoleResources(name, current.Endpoint, current.Publication, current.Parking, result)
	}
	return m.product.CompleteValidation(token, result)
}

func (m *Manager) wait(name string, p Process) {
	err := p.Wait()
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok || r.process != p {
		m.mu.Unlock()
		return
	}
	r.process = nil
	if err != nil {
		r.lastError = err.Error()
		if m.autoRecovery && r.enabled {
			backoff := m.backoffs[name]
			if backoff == nil {
				backoff = &supervisor.Backoff{}
				m.backoffs[name] = backoff
			}
			delay := backoff.Next()
			m.bumpLocked()
			m.mu.Unlock()
			go m.retryAfter(name, delay)
			return
		}
	} else {
		r.lastError = ""
	}
	m.bumpLocked()
	m.mu.Unlock()
}

// SetAutomaticRecovery enables bounded per-role restarts. It is a process-wide
// feature gate so callers can run the compatibility phase with it disabled.
func (m *Manager) SetAutomaticRecovery(enabled bool) {
	m.mu.Lock()
	m.autoRecovery = enabled
	m.mu.Unlock()
}

// SetRecoveryDriver installs the platform/protocol adapter used by the core
// recovery state machine. The adapter is optional during compatibility mode;
// without it no disruptive recovery is attempted.
func (m *Manager) SetRecoveryDriver(driver core.RecoveryDriver) {
	m.mu.Lock()
	m.recoveryDriver = driver
	m.mu.Unlock()
}

func (m *Manager) currentUnderlay() netstate.Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.underlay
}

func (m *Manager) restartRole(ctx context.Context, name string, _ *role) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	p, socket := r.process, r.controlSocket
	if p == nil {
		m.mu.Unlock()
		return fmt.Errorf("Toad %q is not running", name)
	}
	r.process = nil
	r.stateValid = false
	r.streamed = false
	r.operation++
	m.roles[name] = r
	m.bumpLocked()
	m.mu.Unlock()
	m.stopToad(socket)
	if err := p.Stop(); err != nil {
		return err
	}
	return m.ConnectRole(ctx, name)
}

func (m *Manager) retryAfter(name string, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	_ = m.RetryRole(context.Background(), name)
}

// watchState publishes a revision when a Toad writes or updates state.json.
// Process startup and state publication are separate events, so waiting only
// for process lifecycle changes would leave subscribers stuck at Connecting.
func (m *Manager) watchState(name string, p Process) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastMod time.Time
	var lastSize int64 = -1
	scan := func() bool {
		m.mu.Lock()
		r, ok := m.roles[name]
		if !ok || r.process != p || r.streamed {
			m.mu.Unlock()
			return false
		}
		path := filepath.Join(r.cfg.StateDir, "state.json")
		m.mu.Unlock()
		info, err := os.Stat(path)
		if err != nil || (info.ModTime().Equal(lastMod) && info.Size() == lastSize) {
			return true
		}
		lastMod, lastSize = info.ModTime(), info.Size()
		data, err := os.ReadFile(path)
		if err != nil {
			return true
		}
		var published state.Snapshot
		if err := json.Unmarshal(data, &published); err != nil || (published.Name != "" && published.Name != name) {
			return true
		}
		changed := false
		m.mu.Lock()
		if current, ok := m.roles[name]; ok && current.process == p {
			if !current.stateValid || !sameState(current.observed, published) {
				current.observed, current.stateValid = published, true
				if published.RouteReady && published.Session.Connected {
					if backoff := m.backoffs[name]; backoff != nil {
						backoff.MarkReady(time.Now())
					}
				}
				m.bumpLocked()
				changed = true
			}
			m.roles[name] = current
		}
		m.mu.Unlock()
		if changed {
			_ = m.product.ObserveToad(name, toadSnapshot(published))
		}
		return true
	}
	if !scan() {
		return
	}
	for range ticker.C {
		if !scan() {
			return
		}
	}
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) buildUnderlaySnapshot(ctx context.Context) (netstate.Snapshot, error) {
	return underlay.DefaultSnapshot(ctx, m.SnapshotUnderlayExclusions())
}

func (m *Manager) applyUnderlayChange(change netstate.Change) {
	m.mu.Lock()
	sameIdentity := netstate.IdentityEqual(m.underlay, change.Snapshot)
	materialChanged := !sameIdentity || change.Snapshot.Epoch > m.underlay.Epoch
	if !materialChanged && !change.Resume {
		m.mu.Unlock()
		return
	}
	if materialChanged {
		// Manager and product controller expose the same underlay epoch. Normalize
		// it once while Manager serializes observations, then publish that exact
		// snapshot to the controller before another observation can overtake it.
		if m.underlay.Epoch == 0 {
			change.Snapshot.Epoch = 1
		} else if change.Snapshot.Epoch <= m.underlay.Epoch {
			change.Snapshot.Epoch = m.underlay.Epoch + 1
		}
		m.underlay = change.Snapshot
		m.product.SetUnderlay(change.Snapshot, change.Reason)
	} else {
		// A resume invalidation carries a freshly rebuilt snapshot even when
		// route identity did not change. Keep its observation timestamp while
		// retaining the canonical epoch.
		m.underlay.ObservedAt = change.Snapshot.ObservedAt
		change.Snapshot = m.underlay
	}
	autoRecovery, driver := m.autoRecovery, m.recoveryDriver
	suspended := m.suspended
	m.bumpLocked()
	m.mu.Unlock()

	if change.Resume {
		// Invalidate only after the fresh post-resume snapshot has been built.
		// This prevents validation against the pre-suspend underlay sample.
		m.product.RequestResumeValidation()
	}
	if suspended {
		return
	}
	go m.scheduleCurrentValidations()
	if autoRecovery && driver != nil && materialChanged && (change.Snapshot.IPv4 != nil || change.Snapshot.IPv6 != nil) {
		go m.recoverStaleRoles(driver, change.Snapshot.Epoch)
	}
}

func (m *Manager) queueUnderlayInvalidation(source string) {
	select {
	case m.underlayInvalidations <- netstate.Invalidation{Source: source}:
	default:
		// Raw kernel events are coalescible. The 30s audit guarantees eventual
		// convergence even if a burst fills this bounded queue.
	}
}

func (m *Manager) setObserverHealth(kind string, healthy bool, err error) {
	m.mu.Lock()
	switch kind {
	case "netlink":
		m.observers.NetlinkHealthy = healthy
	case "sleep":
		m.observers.SleepHealthy = healthy
	}
	if err != nil {
		m.observers.LastError = err.Error()
	} else if healthy {
		m.observers.LastError = ""
	}
	m.bumpLocked()
	m.mu.Unlock()
}

func (m *Manager) recoverStaleRoles(driver core.RecoveryDriver, epoch uint64) {
	m.mu.Lock()
	roles := make([]struct {
		name string
		op   uint64
	}, 0, len(m.roles))
	for name, r := range m.roles {
		if !r.enabled || r.process == nil {
			continue
		}
		if productRole, ok := m.product.Role(name); ok && productRole.State == core.RoleRecovering {
			roles = append(roles, struct {
				name string
				op   uint64
			}{name: name, op: productRole.Operation})
		}
	}
	m.mu.Unlock()
	for _, role := range roles {
		m.recoverRole(driver, role.name, role.op, epoch)
	}
}

func (m *Manager) recoverRole(driver core.RecoveryDriver, name string, operation, epoch uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := (core.Engine{Controller: m.product, Driver: driver}).Recover(ctx, name, operation, epoch)
	cancel()
	if err == nil {
		m.mu.Lock()
		delete(m.recoveryBackoffs, name)
		delete(m.recoveryRetryPending, name)
		m.mu.Unlock()
		return
	}
	if errors.Is(err, parking.ErrRoutesStillParked) {
		m.scheduleRecoveryRetry(driver, name, operation, epoch)
	}
}

func (m *Manager) scheduleRecoveryRetry(driver core.RecoveryDriver, name string, operation, epoch uint64) {
	m.mu.Lock()
	if m.recoveryRetryPending[name] || m.underlay.Epoch != epoch {
		m.mu.Unlock()
		return
	}
	productRole, ok := m.product.Role(name)
	r, roleOK := m.roles[name]
	if !ok || !roleOK || !r.enabled || r.process == nil ||
		productRole.State != core.RoleRecovering || productRole.Operation != operation {
		m.mu.Unlock()
		return
	}
	backoff := m.recoveryBackoffs[name]
	if backoff == nil {
		backoff = &supervisor.Backoff{}
		m.recoveryBackoffs[name] = backoff
	}
	delay := backoff.Next()
	m.recoveryRetryPending[name] = true
	m.mu.Unlock()

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C

		m.mu.Lock()
		m.recoveryRetryPending[name] = false
		currentEpoch := m.underlay.Epoch
		r, roleOK := m.roles[name]
		productRole, productOK := m.product.Role(name)
		canRetry := roleOK && productOK && r.enabled && r.process != nil &&
			currentEpoch == epoch &&
			productRole.State == core.RoleRecovering &&
			productRole.Operation == operation
		m.mu.Unlock()
		if canRetry {
			m.recoverRole(driver, name, operation, epoch)
		}
	}()
}

// watchUnderlay owns canonical underlay convergence. Kernel invalidations only
// wake the coalescer; a periodic audit remains active even while subscriptions
// are healthy so a lost event cannot permanently strand desired state.
func (m *Manager) watchUnderlay(ctx context.Context) {
	changes := make(chan netstate.Change, 1)
	m.mu.Lock()
	initial := m.underlay
	m.mu.Unlock()
	coalescer := &netstate.Coalescer{
		Settle:  150 * time.Millisecond,
		Maximum: time.Second,
		Build:   m.buildUnderlaySnapshot,
		Changed: changes,
	}
	go func() {
		_ = coalescer.Run(ctx, m.underlayInvalidations, initial)
	}()
	go m.superviseUnderlayWatch(ctx)

	audit := time.NewTicker(30 * time.Second)
	defer audit.Stop()
	m.queueUnderlayInvalidation("initial")
	for {
		select {
		case <-ctx.Done():
			return
		case change := <-changes:
			m.applyUnderlayChange(change)
		case <-audit.C:
			m.queueUnderlayInvalidation("audit")
		}
	}
}

func (m *Manager) superviseUnderlayWatch(ctx context.Context) {
	backoff := &supervisor.Backoff{}
	for {
		if ctx.Err() != nil {
			return
		}
		m.setObserverHealth("netlink", true, nil)
		err := underlay.DefaultWatch(ctx, m.underlayInvalidations)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("underlay watcher exited")
		}
		m.setObserverHealth("netlink", false, err)
		delay := backoff.Next()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) watchSleep(ctx context.Context) {
	source := platform.DefaultSleepSource()
	if source == nil {
		return
	}
	backoff := &supervisor.Backoff{}
	for {
		if ctx.Err() != nil {
			return
		}
		events := make(chan platform.SleepEvent, 8)
		watchErr := make(chan error, 1)
		m.setObserverHealth("sleep", true, nil)
		go func() { watchErr <- source.Watch(ctx, events) }()

		restart := false
		for !restart {
			select {
			case <-ctx.Done():
				return
			case err := <-watchErr:
				if ctx.Err() != nil {
					return
				}
				if err == nil {
					err = errors.New("sleep watcher exited")
				}
				m.setObserverHealth("sleep", false, err)
				restart = true
			case event := <-events:
				if event.Preparing {
					m.mu.Lock()
					m.suspended = true
					m.mu.Unlock()
					continue
				}
				m.mu.Lock()
				m.suspended = false
				m.mu.Unlock()
				m.queueUnderlayInvalidation("resume")
			}
		}
		delay := backoff.Next()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) SnapshotUnderlayExclusions() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.roles))
	for _, r := range m.roles {
		if r.cfg != nil {
			out[r.cfg.Interface] = true
		}
	}
	return out
}

// refreshStates performs compatibility state reads outside the manager lock.
// The running Toad subscription supersedes this path; keeping the read here
// preserves the old state-file API during the N0/N1 migration window.
func (m *Manager) refreshStates() {
	type item struct {
		name, path string
		process    Process
	}
	m.mu.Lock()
	items := make([]item, 0, len(m.roles))
	for name, r := range m.roles {
		if r.process != nil && !r.streamed {
			items = append(items, item{name, filepath.Join(r.cfg.StateDir, "state.json"), r.process})
		}
	}
	m.mu.Unlock()
	for _, it := range items {
		data, err := os.ReadFile(it.path)
		if err != nil {
			continue
		}
		var published state.Snapshot
		if json.Unmarshal(data, &published) != nil || (published.Name != "" && published.Name != it.name) {
			continue
		}
		m.mu.Lock()
		r, ok := m.roles[it.name]
		if ok && r.process == it.process && (!r.stateValid || !sameState(r.observed, published)) {
			r.observed, r.stateValid = published, true
			m.product.ObserveToad(it.name, toadSnapshot(published))
			m.bumpLocked()
			m.roles[it.name] = r
		}
		m.mu.Unlock()
	}
}

// DiagnosticSnapshot returns a snapshot with runtime diagnostics and no secrets.
func (m *Manager) DiagnosticSnapshot() Snapshot {
	snap := m.Snapshot()
	snap.BackendKind = "real"
	m.mu.Lock()
	managed := make(map[string]platform.ManagedInterfaceOwnership, len(m.managedOwnership))
	for name, state := range m.managedOwnership {
		managed[name] = state
	}
	m.mu.Unlock()
	snap.Diagnostics = &Diagnostics{
		CoreVersion:        BuildVersion,
		ProtocolVersion:    fmt.Sprintf("api/%d", APIVersion),
		Platform:           runtime.GOOS,
		SocketPath:         m.socketPath,
		ManagedInterfaces:  managed,
		DesiredStateStatus: m.desiredStateStatus,
	}
	return snap
}

// WaitForRevision blocks until a newer revision is available or ctx is canceled.
func (m *Manager) WaitForRevision(ctx context.Context, revision uint64) (Snapshot, error) {
	for {
		m.mu.Lock()
		if m.revision > revision {
			snapshot := m.snapshotLocked()
			m.mu.Unlock()
			return snapshot, nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
}

func (m *Manager) snapshotLocked() Snapshot {
	rows := make([]RoleSnapshot, 0, len(m.roles))
	for _, name := range m.namesLocked() {
		rows = append(rows, m.snapshotRoleLocked(name, m.roles[name]))
	}
	product := m.product.Snapshot()
	return Snapshot{
		Schema:          2,
		Revision:        m.revision,
		CoreState:       product.CoreState,
		AggregateState:  product.AggregateState,
		ActiveProfile:   m.activeProfile,
		UnderlaySummary: underlaySummary(m.underlay),
		Underlay:        m.underlay,
		LeshySupported:  runtime.GOOS == "linux",
		Observers:       m.observers,
		Roles:           rows,
	}
}

func toadSnapshot(published state.Snapshot) toadctl.Snapshot {
	return toadctl.Snapshot{
		ProtocolVersion:    toadctl.ProtocolVersion,
		Generation:         published.Generation,
		State:              strings.ToLower(published.State),
		Reason:             published.Reason,
		InterfaceName:      published.Interface.Name,
		IfIndex:            published.Interface.IfIndex,
		MTU:                published.Interface.MTU,
		Addresses:          append([]string(nil), published.Interface.Addresses...),
		RouteReady:         published.RouteReady,
		SessionConnected:   published.Session.Connected,
		LastHandshakeAgeMS: published.Session.LastHandshakeAgeMS,
		RXBytes:            published.Session.RXBytes,
		TXBytes:            published.Session.TXBytes,
		Endpoint:           published.Session.Endpoint,
		UpdatedAt:          published.UpdatedAt,
	}
}

func stateFromToadSnapshot(name string, cfg *config.Config, snapshot toadctl.Snapshot) state.Snapshot {
	protocol := ""
	if cfg != nil {
		protocol = string(cfg.Protocol)
	}
	return state.Snapshot{
		Schema:     state.SchemaVersion,
		Name:       name,
		Protocol:   protocol,
		Generation: snapshot.Generation,
		State:      snapshot.State,
		Reason:     snapshot.Reason,
		RouteReady: snapshot.RouteReady,
		Interface:  state.InterfaceState{Name: snapshot.InterfaceName, IfIndex: snapshot.IfIndex, MTU: snapshot.MTU, Addresses: append([]string(nil), snapshot.Addresses...)},
		Session:    state.SessionState{Connected: snapshot.SessionConnected, LastHandshakeAgeMS: snapshot.LastHandshakeAgeMS, RXBytes: snapshot.RXBytes, TXBytes: snapshot.TXBytes, Endpoint: snapshot.Endpoint},
		UpdatedAt:  snapshot.UpdatedAt,
	}
}

func (m *Manager) snapshotRoleLocked(name string, r *role) RoleSnapshot {
	var observedState, reason string
	var routeReady bool
	var iface state.InterfaceState
	var session state.SessionState
	productRole, hasProductRole := m.product.Role(name)

	if r.process == nil {
		if r.enabled && r.lastError != "" {
			observedState, reason = "Failed", r.lastError
		} else {
			observedState = "Stopped"
		}
	} else {
		if !r.stateValid {
			observedState, reason = "Starting", "waiting for Toad state snapshot"
		} else {
			published := r.observed
			observedState, reason = titleState(published.State), published.Reason
			routeReady, iface, session = published.RouteReady, published.Interface, published.Session
		}
	}
	if hasProductRole && r.enabled && r.process != nil && r.streamed && (!r.stateValid || productRole.State != core.RoleStarting) {
		observedState = string(productRole.State)
		if productRole.Reason != "" {
			reason = productRole.Reason
		}
		if productRole.LastError != "" {
			reason = productRole.LastError
		}
	}

	actions := []string{"connect"}
	if r.enabled {
		switch observedState {
		case "Failed":
			actions = []string{"retry"}
		case "Online", "Ready", "Starting", "Degraded":
			actions = []string{"disconnect", "retry"}
		}
	}
	configured := make([]string, 0)
	for _, spec := range r.cfg.ConfiguredTransportEndpoints() {
		if spec.Address.IsValid() {
			configured = append(configured, spec.Address.String())
		} else {
			configured = append(configured, spec.Hostname)
		}
	}
	endpointState := endpoint.State{Configured: configured}
	if routeReady && session.Connected {
		endpointState.State = "ready"
		endpointState.AppliedUnderlayEpoch = m.underlay.Epoch
		endpointState.Live = []netip.AddrPort{}
	}
	validatedEpoch := uint64(0)
	if hasProductRole {
		validatedEpoch = productRole.ValidatedEpoch
		if productRole.Endpoint.State != "" {
			endpointState = productRole.Endpoint
		}
	}
	return RoleSnapshot{
		ID: name, Label: name, Protocol: string(r.cfg.Protocol), State: observedState,
		Reason: reason, RouteReady: routeReady, Interface: iface, Session: session,
		AvailableActions: actions, DesiredEnabled: r.enabled, Operation: r.operation,
		ValidatedEpoch: validatedEpoch, Endpoint: endpointState,
		Publication: func() leshy.PublicationState {
			if hasProductRole && productRole.Publication.Zone != "" {
				return productRole.Publication
			}
			return leshy.PublicationState{}
		}(),
		Parking: func() parking.State {
			if hasProductRole {
				return productRole.Parking
			}
			return parking.State{}
		}(),
		Recovery: func() core.RecoveryState {
			if hasProductRole {
				return productRole.Recovery
			}
			return core.RecoveryState{}
		}(),
		Validation: func() toadctl.ValidationResult {
			if hasProductRole {
				return productRole.Validation
			}
			return toadctl.ValidationResult{}
		}(),
	}
}

func sameState(a, b state.Snapshot) bool {
	a.UpdatedAt = time.Time{}
	b.UpdatedAt = time.Time{}
	return reflect.DeepEqual(a, b)
}

func (m *Manager) namesLocked() []string {
	names := make([]string, 0, len(m.roles))
	for name := range m.roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) bumpLocked() {
	m.revision++
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) Close() error {
	return m.ShutdownProduct(context.Background())
}

// ShutdownProduct tears down live processes without changing persisted operator
// intent. It is used for daemon shutdown/restart, not for a user disconnect.
func (m *Manager) ShutdownProduct(ctx context.Context) error {
	m.mu.Lock()
	if m.shuttingDown {
		m.mu.Unlock()
		return nil
	}
	m.shuttingDown = true
	if m.monitorCancel != nil {
		m.monitorCancel()
	}
	names := m.namesLocked()
	driver := m.recoveryDriver
	m.mu.Unlock()

	var errs []error
	for _, name := range names {
		m.mu.Lock()
		r := m.roles[name]
		needsSafety := r != nil && r.enabled && r.process != nil && driver != nil
		m.mu.Unlock()
		if needsSafety {
			if err := driver.ObserveRoutes(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("%s observe routes: %w", name, err))
			} else if err := driver.Park(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("%s park routes: %w", name, err))
			} else if err := driver.Withdraw(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("%s withdraw publication: %w", name, err))
			}
		}
		if err := m.stopRoleProcess(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func aggregateState(roles []RoleSnapshot) string {
	if len(roles) == 0 {
		return "Stopped"
	}
	allStopped, allReady, anyActive, anyFailed := true, true, false, false
	for _, role := range roles {
		allStopped = allStopped && role.State == "Stopped"
		allReady = allReady && (role.State == "Online" || role.State == "Ready")
		anyActive = anyActive || (role.State != "Stopped" && role.State != "Failed")
		anyFailed = anyFailed || role.State == "Failed" || role.State == "Degraded"
	}
	if allStopped {
		return "Stopped"
	}
	if allReady {
		return "Ready"
	}
	if anyFailed && anyActive {
		return "Degraded"
	}
	if anyFailed {
		return "Failed"
	}
	return "Connecting"
}

func underlaySummary(snapshot netstate.Snapshot) string {
	if snapshot.IPv4 != nil {
		if snapshot.IPv4.PreferredSrc.IsValid() {
			return fmt.Sprintf("%s · %s", snapshot.IPv4.Interface, snapshot.IPv4.PreferredSrc)
		}
		return snapshot.IPv4.Interface
	}
	if snapshot.IPv6 != nil {
		return snapshot.IPv6.Interface
	}
	return "Physical network unavailable"
}

func titleState(value string) string {
	switch strings.ToLower(value) {
	case "online":
		return "Online"
	case "ready":
		return "Ready"
	case "starting", "connecting":
		return "Starting"
	case "degraded", "recovering":
		if strings.ToLower(value) == "recovering" {
			return "Recovering"
		}
		return "Degraded"
	case "waitingforunderlay", "waiting_for_underlay":
		return "WaitingForUnderlay"
	case "failed":
		return "Failed"
	case "stopped", "disconnecting":
		return "Stopped"
	default:
		return "Starting"
	}
}
