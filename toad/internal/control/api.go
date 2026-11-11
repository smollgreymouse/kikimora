// Package control implements the local Kikimora control plane.  It supervises
// Toad processes; protocol implementations remain owned by kikimora-toad.
package control

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Request represents an incoming API request.
type Request struct {
	Version int    `json:"version"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Role    string `json:"role,omitempty"`
	Profile string `json:"profile,omitempty"`

	// Tunnel carries the spec for CreateTunnel.
	Tunnel *config.TunnelSpec `json:"tunnel,omitempty"`

	// Profiles is the target membership for SetTunnelProfiles.
	Profiles []string `json:"profiles,omitempty"`

	// Roles is the role set of a profile created with CreateProfile. Omitted means the
	// profile starts empty.
	Roles []string `json:"roles,omitempty"`

	// List names the routing list for GetRoutingLists/SetRoutingList and Content carries it.
	List    string `json:"list,omitempty"`
	Content string `json:"content,omitempty"`

	// Mode carries the bypass mode for SetBypassMode.
	Mode string `json:"mode,omitempty"`

	// Enabled carries the operator switch for SetDiagnosticsCapture.
	Enabled *bool `json:"enabled,omitempty"`

	// LeshyRoute carries the intent for EnsureLeshyRoute/ForgetLeshyRoute
	// (API v3, managed selected routes).
	LeshyRoute *LeshyRouteIntent `json:"leshy_route,omitempty"`

	// PrivilegedAuth carries the service-to-service bearer credential for
	// privileged control methods (08D1 §31.6). The token is never accepted
	// as a command-line literal; process listings would expose it.
	PrivilegedAuth string `json:"privileged_auth,omitempty"`

	// Socket/AuthFile/Capabilities carry the leshy-mode transaction request
	// (API v4). Capabilities come from the installed Leshy's own
	// `leshy capabilities --json`; the core re-validates them.
	Socket       string   `json:"socket,omitempty"`
	AuthFile     string   `json:"auth_file,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Response represents an outgoing API response.
type Response struct {
	Version      int       `json:"version"`
	MinVersion   int       `json:"min_version,omitempty"`
	MaxVersion   int       `json:"max_version,omitempty"`
	ID           string    `json:"id,omitempty"`
	OK           bool      `json:"ok"`
	Error        string    `json:"error,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Snapshot     *Snapshot `json:"snapshot,omitempty"`
	APIError     *APIError `json:"error_detail,omitempty"`

	// Message explains a command that only changes files on disk.
	Message string `json:"message,omitempty"`

	// Lists carries the routing lists for GetRoutingLists.
	Lists map[string]string `json:"lists,omitempty"`

	// Zones explains where each list goes: the router resolves route_target at runtime.
	Zones []config.LeshyZone `json:"zones,omitempty"`

	// BypassMode is the global rule for traffic no role claims.
	BypassMode string `json:"bypass_mode,omitempty"`

	// LeshyRoute carries the controller's verdict for a managed route call.
	LeshyRoute *LeshyRouteResult `json:"leshy_route,omitempty"`

	// ManagedMode carries the committed managed-mode status (API v4).
	ManagedMode *ManagedLeshyModeStatus `json:"managed_mode,omitempty"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Role      string `json:"role,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Serve listens on the local control-plane endpoint and handles connections.
func Serve(ctx context.Context, socket string, manager *Manager) error {
	return ServeWithReady(ctx, socket, manager, nil)
}

// ServeWithReady listens on the local control-plane endpoint and handles
// connections, invoking ready exactly once after the socket is bound and
// protected but before the accept loop starts (08D1 §31.22). The callback is
// the application-readiness barrier: service managers consider the core
// started only when it has fired, so dependents never observe a half-started
// authority. Serve remains a compatibility wrapper with no callback.
func ServeWithReady(ctx context.Context, socket string, manager *Manager, ready func()) error {
	address, err := prepareControlAddress(socket)
	if err != nil {
		return err
	}
	listener, err := listenControl(address)
	if err != nil {
		return err
	}
	defer listener.Close()
	if ready != nil {
		ready()
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		if err := authorizePeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		go serveConnection(ctx, conn, manager)
	}
}

func serveConnection(ctx context.Context, conn net.Conn, manager *Manager) {
	defer conn.Close()
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	var writeMu sync.Mutex
	write := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(conn, value)
	}
	subscribed := false
	for {
		var request Request
		if err := readFrame(conn, &request); err != nil {
			return
		}
		response := manager.Handle(ctx, request)
		if err := write(response); err != nil {
			return
		}
		if request.Method == "Subscribe" && response.OK && response.Snapshot != nil && !subscribed {
			subscribed = true
			go serveSubscription(connCtx, write, manager, request.ID, request.Version, response.Snapshot.Revision)
		}
	}
}

func serveSubscription(ctx context.Context, write func(any) error, manager *Manager, id string, version int, revision uint64) {
	last := ""
	for {
		snapshot, err := manager.WaitForRevision(ctx, revision)
		if err != nil {
			return
		}
		// Revisions move several times a second (handshake ages, observer ticks). Sending a
		// full snapshot each time forces every client to re-render for nothing, so only
		// material changes are published.
		fingerprint := subscriptionFingerprint(snapshot)
		if fingerprint == last {
			revision = snapshot.Revision
			continue
		}
		last = fingerprint
		if err := write(Response{Version: version, ID: id, OK: true, Snapshot: &snapshot}); err != nil {
			return
		}
		revision = snapshot.Revision
	}
}

// subscriptionFingerprint covers what a client renders: the aggregate state, the profile
// selection, the underlay summary and the per-role state and reason. Volatile counters
// (rx/tx, handshake age) stay out of it on purpose, but underlay changes and role
// resource flips must reach the UI even when no role State moves.
func subscriptionFingerprint(snapshot Snapshot) string {
	parts := []string{snapshot.CoreState, snapshot.AggregateState, snapshot.ActiveProfile,
		snapshot.UnderlaySummary, strings.Join(snapshot.Profiles, ",")}
	for _, role := range snapshot.Roles {
		parts = append(parts, role.ID, role.State, role.Reason,
			role.Endpoint.State,
			fmt.Sprintf("%v", role.Publication.Published),
			fmt.Sprintf("%v", role.Parking.Active))
	}
	return strings.Join(parts, "\x1f")
}

// readFrame reads a length-prefixed JSON frame from r.
func readFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > 1024*1024 {
		return fmt.Errorf("invalid frame size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, value)
}

// writeFrame writes a length-prefixed JSON frame to w.
func writeFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > 1024*1024 {
		return fmt.Errorf("invalid response size")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// Call performs a single request/response transaction over the local
// control-plane endpoint.
func Call(socket string, request Request) (Response, error) {
	conn, err := dialControl(context.Background(), socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := writeFrame(conn, request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := readFrame(conn, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

// Subscribe opens a revisioned streaming request and invokes fn for the initial
// snapshot and every subsequent response until ctx is cancelled or the
// connection is lost. Callers that need process-restart resilience should
// reconnect and issue a fresh Subscribe request.
func Subscribe(ctx context.Context, socket string, request Request, fn func(Response) error) error {
	conn, err := dialControl(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()

	if request.Method == "" {
		request.Method = "Subscribe"
	}
	if request.Method != "Subscribe" {
		return fmt.Errorf("stream request must use Subscribe, got %q", request.Method)
	}
	if err := writeFrame(conn, request); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		var response Response
		if err := readFrame(conn, &response); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if !response.OK {
			if response.Error == "" {
				response.Error = "subscription failed"
			}
			return fmt.Errorf("%s", response.Error)
		}
		if fn != nil {
			if err := fn(response); err != nil {
				return err
			}
		}
	}
}

// Handle processes an API request and returns a response.
func (m *Manager) Handle(ctx context.Context, request Request) Response {
	response := Response{Version: request.Version, ID: request.ID}
	if request.Version < 1 || request.Version > APIVersion {
		response.Error = fmt.Sprintf("unsupported API version %d", request.Version)
		response.APIError = &APIError{Code: "unsupported_version", Message: response.Error, Retryable: false}
		return response
	}
	// Managed selected routes are a v3 surface: older clients must see them
	// as unsupported instead of silently ignoring route intents.
	if request.Version < 3 && leshyRouteMethods[request.Method] {
		response.Error = fmt.Sprintf("method %q requires API version 3", request.Method)
		response.APIError = &APIError{Code: "unsupported_version", Message: response.Error, Retryable: false}
		return response
	}
	// Privileged control methods authenticate before any intent parsing or
	// mutation (08D1 §31.6, D8 §8.3). A wrong token is a non-retryable denial
	// with zero registry/kernel mutation.
	if leshyRouteMethods[request.Method] || managedModeMethods[request.Method] {
		if apiErr := m.checkPrivilegedAuth(request.Method, request.PrivilegedAuth, request.LeshyRoute); apiErr != nil {
			response.Error = apiErr.Message
			response.APIError = apiErr
			return response
		}
	}
	// The tagged test control surface (R3) runs after version/auth
	// validation and before the production switch. The release build's
	// handler never handles anything, so the test method falls through to
	// the production default case and fails as unsupported there.
	if handled, testErr := m.handleTestControlMethod(ctx, request); handled {
		started := time.Now()
		debugRequest(request.Method, request.Role, request.Profile, started, testErr)
		if testErr != nil {
			response.Error = testErr.Error()
			response.APIError = &APIError{Code: "command_failed", Message: testErr.Error(), Role: request.Role, Retryable: false}
			return response
		}
		response.OK = true
		snapshot := m.Snapshot()
		response.Snapshot = &snapshot
		return response
	}
	started := time.Now()
	var err error
	switch request.Method {
	case "Handshake":
		response.Capabilities = m.handshakeCapabilities()
		response.MinVersion, response.MaxVersion = 1, APIVersion
	case "Snapshot", "GetSnapshot":
		respSnap := m.Snapshot()
		response.Snapshot = &respSnap
	case "ConnectAll":
		err = m.ConnectAll(ctx)
	case "DisconnectAll":
		err = m.DisconnectAll()
	case "ConnectRole":
		err = m.ConnectRole(ctx, request.Role)
	case "DisconnectRole":
		err = m.DisconnectRole(request.Role)
	case "RetryRole":
		err = m.RetryRole(ctx, request.Role)
	case "GetManagedLeshyMode":
		status := m.ManagedLeshyMode()
		response.ManagedMode = &status
	case "EnableManagedLeshy":
		status, enableErr := m.EnableManagedLeshy(request.Socket, request.AuthFile, request.Capabilities)
		if enableErr != nil {
			err = enableErr
			break
		}
		response.ManagedMode = &status
		response.Message = "managed Leshy mode enabled"
	case "DisableManagedLeshy":
		status, disableErr := m.DisableManagedLeshy(request.AuthFile)
		if disableErr != nil {
			err = disableErr
			break
		}
		response.ManagedMode = &status
		response.Message = "managed Leshy mode disabled"
	case "Subscribe":
		// serveConnection continues this request as a revisioned stream.
		respSnap := m.Snapshot()
		response.Snapshot = &respSnap
	case "SetActiveProfile":
		err = m.SetActiveProfile(request.Profile)
	case "RediscoverEndpoints":
		err = m.RediscoverEndpoints(request.Role)
	case "ValidateRole":
		err = m.ValidateRole(ctx, request.Role)
	case "CreateTunnel":
		var path string
		path, err = m.CreateTunnel(request.Tunnel)
		if err == nil {
			response.Message = fmt.Sprintf("created %s; restart the core to load it", path)
		}
	case "GetRoutingLists":
		// The same generated-config path every other read/write of this run
		// uses (U5): a direct default-path call would read past an injected
		// runtime root.
		response.Zones, _ = config.ReadLeshyZones(m.leshyConfigFile())
		response.BypassMode = m.BypassMode()
		names := m.RoutingListNames()
		response.Lists = make(map[string]string, len(names))
		for _, list := range names {
			content, readErr := m.RoutingList(list)
			if readErr != nil {
				err = readErr
				break
			}
			response.Lists[list] = content
		}
	case "SetBypassMode":
		err = m.SetBypassMode(request.Mode)
		if err == nil {
			if reloadErr := m.reloadLeshy(context.Background()); reloadErr != nil {
				response.Message = fmt.Sprintf("bypass mode saved, but Leshy did not restart: %v", reloadErr)
			} else {
				response.Message = fmt.Sprintf("bypass mode saved: %s", request.Mode)
			}
		}
	case "SetRoutingList":
		if request.List == config.BypassList {
			err = fmt.Errorf("bypass is a global rule, not a list")
			break
		}
		err = m.SetRoutingList(request.List, request.Content)
		if err == nil {
			if genErr := m.RegenerateLeshyConfig(); genErr != nil {
				response.Message = fmt.Sprintf("%s list saved, but the router config was not rewritten: %v", request.List, genErr)
				break
			}
			if reloadErr := m.reloadLeshy(context.Background()); reloadErr != nil {
				response.Message = fmt.Sprintf("%s list saved, but Leshy did not restart: %v", request.List, reloadErr)
			} else {
				response.Message = fmt.Sprintf("%s list saved; Leshy restarted", request.List)
			}
		}
	case "RemoveTunnel":
		err = m.RemoveTunnel(request.Role)
		if err == nil {
			response.Message = fmt.Sprintf("removed %s; restart the core to unload it", request.Role)
		}
	case "CreateProfile":
		var added int
		added, err = m.CreateProfile(request.Profile, request.Roles)
		if err == nil {
			if added == 0 {
				response.Message = fmt.Sprintf("profile %q is already registered", request.Profile)
			} else {
				response.Message = fmt.Sprintf("profile %q created", request.Profile)
			}
			if len(request.Roles) > 0 {
				response.Message += fmt.Sprintf(" with %d role(s)", len(request.Roles))
			}
		}
	case "SetProfileRoles":
		err = m.SetProfileRoles(request.Profile, request.Roles)
		if err == nil {
			response.Message = fmt.Sprintf("profile %q now contains %d role(s)",
				request.Profile, len(request.Roles))
		}
	case "SetTunnelProfiles":
		_, err = m.SetTunnelProfiles(request.Role, request.Profiles)
		if err == nil {
			response.Message = fmt.Sprintf("moved %s to [%s]",
				request.Role, strings.Join(request.Profiles, ", "))
		}
	case "GetDiagnosticsSnapshot":
		diagSnap := m.DiagnosticSnapshot()
		response.Snapshot = &diagSnap
	case "SetDiagnosticsCapture":
		if request.Enabled == nil {
			err = fmt.Errorf("SetDiagnosticsCapture requires enabled")
			break
		}
		err = m.SetDiagnosticsCapture(ctx, *request.Enabled)
	case "CreateDiagnosticsBundle":
		var path string
		path, err = m.BundleDiagnostics("")
		if err == nil {
			response.Message = path
		}
	case "SetRoleIPv6":
		if request.Enabled == nil {
			err = fmt.Errorf("SetRoleIPv6 requires enabled")
			break
		}
		err = m.SetRoleIPv6(request.Role, *request.Enabled)
	case "EnsureLeshyRoute", "ForgetLeshyRoute", "FlushLeshyZone":
		if m.selectedRoutes == nil {
			err = fmt.Errorf("managed selected routes are not wired")
			break
		}
		if request.LeshyRoute == nil {
			// P0-14: a malformed request is an API error, never a panic —
			// FlushLeshyZone used to read Zone through a nil intent.
			err = fmt.Errorf("%s requires leshy_route", request.Method)
			break
		}
		// D8 §8.4: the strict validation order runs before any mutation —
		// auth already ran in Handle; steps 2-6 are zero-mutation gates.
		if apiErr := m.authorizeLeshyRouteIntent(request.LeshyRoute); apiErr != nil {
			response.Error = apiErr.Message
			response.APIError = apiErr
			return response
		}
		switch request.Method {
		case "EnsureLeshyRoute":
			result, ensureErr := m.selectedRoutes.Ensure(ctx, *request.LeshyRoute)
			// D8 §8.2: a current v2 request receives the active fence echo;
			// Leshy fails closed on any mismatch with its own identity.
			if request.LeshyRoute.carriesActivationFence() {
				result.ActivationID, result.ModeGeneration = m.activeActivationFenceSnapshot()
			}
			response.LeshyRoute = &result
			if ensureErr != nil {
				if !isLeshyRouteVerdictError(ensureErr) {
					err = ensureErr
					break
				}
				// A verdict (denied/conflict) travels inside an OK response:
				// the DNS layer turns it into a fail-closed SERVFAIL, not a
				// transport failure. The result already carries the state.
				debugRequest(request.Method, request.Role, request.Profile, started, nil)
				response.OK = true
				return response
			}
		case "ForgetLeshyRoute":
			err = m.selectedRoutes.Forget(ctx, *request.LeshyRoute)
		case "FlushLeshyZone":
			if request.LeshyRoute.Zone == "" {
				err = fmt.Errorf("FlushLeshyZone requires leshy_route.zone")
				break
			}
			err = m.selectedRoutes.FlushZone(ctx, request.LeshyRoute.Zone)
		}
	default:
		err = fmt.Errorf("unsupported method %q", request.Method)
	}
	debugRequest(request.Method, request.Role, request.Profile, started, err)
	if err != nil {
		response.Error = err.Error()
		response.APIError = &APIError{Code: "command_failed", Message: err.Error(), Role: request.Role, Retryable: true}
		return response
	}
	response.OK = true
	if response.Snapshot == nil && request.Method != "Handshake" {
		snapshot := m.Snapshot()
		response.Snapshot = &snapshot
	}
	return response
}

// isLeshyRouteVerdictError reports whether the error is a controller
// verdict (denied/conflict) rather than a transport failure: verdicts are
// delivered inside an OK response with the exact state.
func isLeshyRouteVerdictError(err error) bool {
	var denied *SelectedRouteDeniedError
	var conflict *SelectedRouteConflictError
	return errors.As(err, &denied) || errors.As(err, &conflict)
}

// handshakeCapabilities generates the Handshake capability list from runtime
// support (D8 §8.7): AF_UNIX control is always supported; the v2 privileged
// managed-routing capabilities are claimed only by a persistent install with
// the auth machinery; the legacy v1 string is advertised only while the
// explicit compatibility branch is available — never against an active
// schema2 authority. A development core without a StateDir claims neither v2
// nor privileged auth.
func (m *Manager) handshakeCapabilities() []string {
	capabilities := []string{
		"Handshake", "GetSnapshot", "Subscribe", "ConnectAll", "DisconnectAll",
		"ConnectRole", "DisconnectRole", "RetryRole", "SetActiveProfile",
		"RediscoverEndpoints", "ValidateRole", "GetDiagnosticsSnapshot",
		"SetDiagnosticsCapture", "CreateDiagnosticsBundle", "CreateTunnel",
		"RemoveTunnel", "CreateProfile", "SetProfileRoles", "SetTunnelProfiles",
		"GetRoutingLists", "SetRoutingList", "SetBypassMode",
	}
	m.mu.Lock()
	schema2Active := m.managedSocket != ""
	authMachinery := m.privilegedAuth != nil
	m.mu.Unlock()
	capabilities = append(capabilities, CapabilityAFUnixControl)
	if authMachinery {
		capabilities = append(capabilities,
			CapabilityManagedSelectedRoutesV2, CapabilityPrivilegedControlAuthV1)
	}
	if !schema2Active {
		capabilities = append(capabilities, CapabilityManagedSelectedRoutesV1)
	}
	capabilities = append(capabilities,
		"GetManagedLeshyMode", "EnableManagedLeshy", "DisableManagedLeshy")
	return capabilities
}

// authorizeLeshyRouteIntent is the D8 §8.4 strict validation order, steps 2-6
// (step 1, the privileged auth, already ran in Handle; steps 7-8 are the
// route-authority gate and the selected-route transaction). Every failure
// here performs zero registry, checkpoint or kernel mutation, and the stale
// fences answer with stable non-retryable API errors.
func (m *Manager) authorizeLeshyRouteIntent(intent *LeshyRouteIntent) *APIError {
	m.mu.Lock()
	socket := m.managedSocket
	transition := m.modeTransitionInFlight
	activation := m.managedActivationID
	generation := m.modeGeneration
	managed := make(map[string]bool, len(m.managedSelectedRoles))
	zones := make(map[string][]string, len(m.roles))
	for name, on := range m.managedSelectedRoles {
		managed[name] = on
	}
	for name, r := range m.roles {
		if r.cfg == nil {
			continue
		}
		zone := r.cfg.EffectiveEndpointPolicy().Zone
		if zone != "" {
			zones[zone] = append(zones[zone], name)
		}
	}
	m.mu.Unlock()

	apiErr := func(code, message string) *APIError {
		return &APIError{Code: code, Message: message, Retryable: false}
	}
	// 2: a committed schema2 marker exists.
	if socket == "" {
		return apiErr("managed_mode_required",
			"managed selected routes require an active schema2 marker")
	}
	// 3: no mode transition in flight.
	if transition {
		return apiErr("mode_transition_in_flight",
			"a leshy-mode transition is in flight; route mutations are denied")
	}
	// 4: the exact activation id.
	if intent.ActivationID != activation {
		return apiErr("stale_activation",
			"the request activation id does not match the active managed activation")
	}
	// 5: the exact mode generation.
	if intent.ModeGeneration != generation {
		return apiErr("stale_mode_generation",
			"the request mode generation does not match the committed generation")
	}
	// 6: the zone maps to exactly one marker-managed role.
	candidates := zones[intent.Zone]
	if len(candidates) != 1 {
		return apiErr("zone_not_managed",
			fmt.Sprintf("zone %q maps to %d configured roles", intent.Zone, len(candidates)))
	}
	if !managed[candidates[0]] {
		return apiErr("zone_not_managed",
			fmt.Sprintf("zone %q is not managed by the active schema2 marker", intent.Zone))
	}
	return nil
}

// activeActivationFenceSnapshot returns the applied managed fence for the
// result echo (D8 §8.2).
func (m *Manager) activeActivationFenceSnapshot() (string, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.managedActivationID, m.modeGeneration
}
