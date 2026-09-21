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

type Snapshot struct {
	Schema          int               `json:"schema"`
	Revision        uint64            `json:"revision"`
	CoreState       string            `json:"core_state"`
	AggregateState  string            `json:"aggregate_state"`
	ActiveProfile   string            `json:"active_profile"`
	UnderlaySummary string            `json:"underlay_summary"`
	Underlay        netstate.Snapshot `json:"underlay"`
	LeshySupported  bool              `json:"leshy_supported"`
	Roles           []RoleSnapshot    `json:"roles"`
	BackendKind     string            `json:"backend_kind,omitempty"`
	Diagnostics     *Diagnostics      `json:"diagnostics,omitempty"`
}

// Diagnostics contains non-secret diagnostic information.
type Diagnostics struct {
	CoreVersion     string `json:"core_version"`
	ProtocolVersion string `json:"protocol_version"`
	Platform        string `json:"platform"`
	SocketPath      string `json:"socket_path"`
}

type Manager struct {
	mu             sync.Mutex
	launcher       Launcher
	roles          map[string]*role
	revision       uint64
	activeProfile  string
	profileToRoles map[string][]string
	socketPath     string
	changed        chan struct{}
	underlay       netstate.Snapshot
	product        *core.Controller
	autoRecovery   bool
	backoffs       map[string]*supervisor.Backoff
	monitorCancel  context.CancelFunc
	recoveryDriver core.RecoveryDriver
	suspended      bool
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
		launcher:       launcher,
		roles:          make(map[string]*role),
		revision:       1,
		activeProfile:  "default",
		profileToRoles: make(map[string][]string),
		socketPath:     socketPath,
		changed:        make(chan struct{}),
		backoffs:       make(map[string]*supervisor.Backoff),
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

// ConnectAll enables and starts every configured role.
func (m *Manager) ConnectAll(ctx context.Context) error {
	m.mu.Lock()
	names := m.namesLocked()
	for _, name := range names {
		m.roles[name].enabled = true
		_ = m.product.SetRoleDesired(ctx, name, true)
	}
	m.bumpLocked()
	m.mu.Unlock()
	var errs []error
	for _, name := range names {
		if err := m.ConnectRole(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// DisconnectAll disables and stops every configured role.
func (m *Manager) DisconnectAll() error {
	m.mu.Lock()
	names := m.namesLocked()
	m.mu.Unlock()
	var errs []error
	for _, name := range names {
		if err := m.DisconnectRole(name); err != nil {
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
	if !r.enabled {
		r.operation++
	}
	r.enabled = true
	_ = m.product.SetRoleDesired(ctx, name, true)
	if r.process != nil {
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
		r.lastError = err.Error()
		m.bumpLocked()
		m.mu.Unlock()
		return fmt.Errorf("start Toad %q: %w", name, err)
	}

	m.mu.Lock()
	if r.process != nil {
		m.mu.Unlock()
		_ = p.Stop()
		return nil
	}
	r.process = p
	r.controlSocket = filepath.Join(r.cfg.StateDir, "control.sock")
	r.lastError = ""
	m.bumpLocked()
	m.mu.Unlock()
	go m.wait(name, p)
	go m.subscribeToad(name, p, r.controlSocket)
	return nil
}

// DisconnectRole disables the role and stops its process.
func (m *Manager) DisconnectRole(name string) error {
	m.mu.Lock()
	r, ok := m.roles[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown Toad %q", name)
	}
	if r.enabled {
		r.operation++
	}
	r.enabled = false
	_ = m.product.SetRoleDesired(context.Background(), name, false)
	p := r.process
	controlSocket := r.controlSocket
	r.process = nil
	r.lastError = ""
	r.operation++
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
	err := (toadctl.Client{Socket: socket}).Subscribe(ctx, "core-"+name, func(snapshot toadctl.Snapshot) error {
		m.mu.Lock()
		r, ok := m.roles[name]
		if !ok || r.process != p {
			m.mu.Unlock()
			return context.Canceled
		}
		r.observed = stateFromToadSnapshot(name, r.cfg, snapshot)
		r.stateValid = true
		r.streamed = true
		m.roles[name] = r
		m.bumpLocked()
		m.mu.Unlock()

		if m.product.ObserveToad(name, snapshot) {
			m.scheduleValidation(name, p)
		}
		return nil
	})
	if err != nil {
		// Compatibility launchers may not expose a control socket. Live Toads
		// are streamed and do not use periodic state-file polling.
		m.watchState(name, p)
	}
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
		_ = m.ValidateRole(ctx, name)
		cancel()

		m.mu.Lock()
		current, currentOK := m.roles[name]
		if currentOK && current.process == p {
			current.validationInFlight = false
			m.roles[name] = current
		}
		m.mu.Unlock()
	}()
}

// SetActiveProfile validates and selects a loaded profile.
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.profileToRoles[name]; !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	if m.activeProfile != name {
		m.activeProfile = name
		m.bumpLocked()
	}
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
		return fmt.Errorf("unknown Toad %q", name)
	}
	socket := r.controlSocket
	p := r.process
	m.mu.Unlock()
	if p == nil || socket == "" {
		return fmt.Errorf("Toad %q has no live control socket", name)
	}

	response, err := (toadctl.Client{Socket: socket}).Call(ctx, toadctl.Request{
		Version:    toadctl.ProtocolVersion,
		Method:     "Validate",
		Generation: token.ToadGeneration,
	})
	if err != nil {
		return fmt.Errorf("validate Toad %q: %w", name, err)
	}
	if !response.OK {
		if response.Error != nil {
			return response.Error
		}
		return fmt.Errorf("validate Toad %q failed", name)
	}
	if response.Snapshot != nil {
		m.mu.Lock()
		if current, currentOK := m.roles[name]; currentOK && current.process == p {
			current.observed = stateFromToadSnapshot(name, current.cfg, *response.Snapshot)
			current.stateValid = true
			current.streamed = true
			m.roles[name] = current
			m.bumpLocked()
		}
		m.mu.Unlock()
		_ = m.product.ObserveToad(name, *response.Snapshot)
	}
	if response.Validation == nil {
		return fmt.Errorf("validate Toad %q returned no validation result", name)
	}
	result := *response.Validation
	if current, exists := m.product.Role(name); exists {
		_ = m.product.UpdateRoleResources(name, current.Endpoint, current.Publication, current.Parking, result)
	}
	if !m.product.CompleteValidation(token, result) {
		return fmt.Errorf("validation result for Toad %q became stale", name)
	}
	if !result.Healthy {
		return fmt.Errorf("Toad %q is not healthy: %s", name, result.Reason)
	}
	return nil
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
		if !ok || r.process != p {
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
	m.refreshUnderlay()
	m.refreshStates()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) refreshUnderlay() {
	current := m.SnapshotUnderlayExclusions()
	next, err := underlay.DefaultSnapshot(context.Background(), current)
	if err != nil {
		return
	}
	m.mu.Lock()
	changed := !netstate.IdentityEqual(m.underlay, next)
	if changed {
		if m.underlay.Epoch == 0 {
			next.Epoch = 1
		} else {
			next.Epoch = m.underlay.Epoch + 1
		}
		m.underlay = next
		m.product.SetUnderlay(next, netstate.ChangeInterface)
		m.bumpLocked()
	}
	autoRecovery, driver := m.autoRecovery, m.recoveryDriver
	epoch := m.underlay.Epoch
	m.mu.Unlock()
	if changed {
		go m.scheduleCurrentValidations()
	}
	if changed && autoRecovery && driver != nil && (next.IPv4 != nil || next.IPv6 != nil) {
		go m.recoverStaleRoles(driver, epoch)
	}
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
	engine := core.Engine{Controller: m.product, Driver: driver}
	for _, role := range roles {
		_ = engine.Recover(context.Background(), role.name, role.op, epoch, false)
	}
}

// watchUnderlay consumes platform invalidations. The bounded ticker is only a
// recovery path if netlink subscription cannot be established; every refresh
// is still compared by canonical identity.
func (m *Manager) watchUnderlay(ctx context.Context) {
	invalidations := make(chan netstate.Invalidation, 64)
	watchErr := make(chan error, 1)
	go func() { watchErr <- underlay.DefaultWatch(ctx, invalidations) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-invalidations:
			m.refreshUnderlay()
		case <-watchErr:
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-invalidations:
					m.refreshUnderlay()
				case <-ticker.C:
					m.refreshUnderlay()
				}
			}
		}
	}
}

func (m *Manager) watchSleep(ctx context.Context) {
	source := platform.DefaultSleepSource()
	if source == nil {
		return
	}
	events := make(chan platform.SleepEvent, 8)
	watchErr := make(chan error, 1)
	go func() { watchErr <- source.Watch(ctx, events) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-watchErr:
			return
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
			m.refreshUnderlay()
			m.product.RequestResumeValidation()
			go m.validateEnabledRolesAfterResume()
		}
	}
}

// validateEnabledRolesAfterResume performs the mandatory active validation
// after the core has invalidated the previous Ready states. It is deliberately
// per-role: one stale socket must not delay or rewrite another role's result.
func (m *Manager) validateEnabledRolesAfterResume() {
	m.mu.Lock()
	names := make([]string, 0, len(m.roles))
	for name, r := range m.roles {
		if r.enabled && r.process != nil && r.controlSocket != "" {
			names = append(names, name)
		}
	}
	m.mu.Unlock()
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := m.ValidateRole(ctx, name)
		cancel()
		if err != nil {
			m.mu.Lock()
			autoRecovery, driver := m.autoRecovery, m.recoveryDriver
			m.mu.Unlock()
			if autoRecovery && driver != nil {
				if m.product.MarkRecovering(name, err.Error()) {
					role, ok := m.product.Role(name)
					if ok {
						recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
						_ = (core.Engine{Controller: m.product, Driver: driver}).Recover(recoveryCtx, name, role.Operation, m.currentUnderlay().Epoch, false)
						recoveryCancel()
					}
				}
			}
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
	snap.Diagnostics = &Diagnostics{
		CoreVersion:     BuildVersion,
		ProtocolVersion: fmt.Sprintf("api/%d", APIVersion),
		Platform:        runtime.GOOS,
		SocketPath:      m.socketPath,
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
	if m.monitorCancel != nil {
		m.monitorCancel()
	}
	return m.DisconnectAll()
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
