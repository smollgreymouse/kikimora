package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The D8 regression matrix (08D1 post-D7 audit §8.8): the one-writer boundary
// is fenced by the privileged auth, the active schema2 activation and the
// committed generation. Every denial proves zero mutation — counters,
// checkpoint bytes and fake-kernel rows stay unchanged.

// gateManager builds an activated schema2 manager driven through the
// privileged API: credential installed, Enable served through Handle, the
// selected-route surface wired to a fake writer with the authority ready.
func gateManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	manager, _ := transactionManager(t)
	token := armPrivilegedAuthForTest(t, manager)
	response := manager.Handle(context.Background(), Request{
		Version:        APIVersion,
		ID:             "gate",
		Method:         "EnableManagedLeshy",
		PrivilegedAuth: token,
		Socket:         "/run/kk-test.sock",
		Capabilities:   testCapabilities(),
	})
	if !response.OK {
		t.Fatalf("enable through the API: %s", response.Error)
	}
	fake := newSelectedRoutesFake()
	manager.selectedRoutes.SetRoutes(fake)
	manager.selectedRoutes.SetRouteAuthorityReady()
	manager.selectedRoutes.roleRuntime = func(string) (core.RoleRuntime, bool) {
		return readyRuntime("primary", true, true), true
	}
	activation, _ := manager.activeActivationFenceSnapshot()
	return manager, token, activation
}

// mutationProbe captures the observable mutation state of the manager.
type mutationProbe struct {
	reconciles int
	removes    int
	registry   int
	checkpoint string
}

func probeMutation(t *testing.T, manager *Manager) mutationProbe {
	t.Helper()
	probe := mutationProbe{
		registry: len(manager.selectedRoutes.RegistrySnapshot("primary")),
	}
	if fake, ok := manager.selectedRoutes.routes.(*selectedRoutesFake); ok {
		probe.reconciles = fake.reconcileCount()
		probe.removes = len(fake.removes)
	}
	raw, err := os.ReadFile(filepath.Join(manager.configDir, "primary", "selected-routes.json"))
	switch {
	case err == nil:
		probe.checkpoint = string(raw)
	case os.IsNotExist(err):
	default:
		t.Fatal(err)
	}
	return probe
}

func (p mutationProbe) equals(t *testing.T, other mutationProbe) {
	t.Helper()
	if p.reconciles != other.reconciles || p.removes != other.removes ||
		p.registry != other.registry || p.checkpoint != other.checkpoint {
		t.Fatalf("a denial must perform zero mutation: before=%#v after=%#v", other, p)
	}
}

func TestRouteGateMatrix(t *testing.T) {
	setup := func(t *testing.T) (*Manager, string, string, LeshyRouteIntent) {
		manager, token, activation := gateManager(t)
		_, generation := manager.activeActivationFenceSnapshot()
		return manager, token, activation, LeshyRouteIntent{
			Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
			ActivationID: activation, ModeGeneration: generation,
		}
	}

	t.Run("current activation and generation succeeds with the fence echo", func(t *testing.T) {
		manager, token, activation, intent := setup(t)
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if !response.OK || response.LeshyRoute == nil || response.LeshyRoute.State != "applied" {
			t.Fatalf("the current fence must apply: %#v (%s)", response, response.Error)
		}
		if response.LeshyRoute.ActivationID != activation || response.LeshyRoute.ModeGeneration != intent.ModeGeneration {
			t.Fatalf("the verdict must echo the active fence: %#v", response.LeshyRoute)
		}
		after := probeMutation(t, manager)
		if after.reconciles != before.reconciles+1 {
			t.Fatalf("one kernel mutation expected: %#v", after)
		}
	})

	t.Run("stale activation denied with zero mutation", func(t *testing.T) {
		manager, token, _, intent := setup(t)
		intent.ActivationID = "ffffffffffffffffffffffffffffffff"
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "stale_activation" {
			t.Fatalf("a stale activation must be a stable denial: %#v", response)
		}
		if response.APIError.Retryable {
			t.Fatal("a stale activation must be non-retryable")
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("stale generation denied with zero mutation", func(t *testing.T) {
		manager, token, _, intent := setup(t)
		intent.ModeGeneration++
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "stale_mode_generation" {
			t.Fatalf("a stale generation must be a stable denial: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("old request after Disable denied", func(t *testing.T) {
		manager, token, _, intent := setup(t)
		if response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "d", Method: "DisableManagedLeshy", PrivilegedAuth: token,
		}); !response.OK {
			t.Fatalf("disable: %s", response.Error)
		}
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "managed_mode_required" {
			t.Fatalf("a request after Disable must be denied by the mode gate: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("old request after Disable and re-Enable denied", func(t *testing.T) {
		manager, token, oldActivation, intent := setup(t)
		if response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "d", Method: "DisableManagedLeshy", PrivilegedAuth: token,
		}); !response.OK {
			t.Fatalf("disable: %s", response.Error)
		}
		if response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "e", Method: "EnableManagedLeshy",
			PrivilegedAuth: token, Socket: "/run/kk-test.sock", Capabilities: testCapabilities(),
		}); !response.OK {
			t.Fatalf("re-enable: %s", response.Error)
		}
		newActivation, _ := manager.activeActivationFenceSnapshot()
		if newActivation == oldActivation {
			t.Fatal("the re-enable must mint a fresh activation")
		}
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "stale_activation" {
			t.Fatalf("the old activation must be denied after the re-enable: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("local mode denies the mutation", func(t *testing.T) {
		manager, _ := transactionManager(t)
		token := armPrivilegedAuthForTest(t, manager)
		before := probeMutation(t, manager)
		intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
		for name, fenced := range map[string]*LeshyRouteIntent{"v1": &intent, "v2": func() *LeshyRouteIntent {
			i := intent
			i.ActivationID = "ffffffffffffffffffffffffffffffff"
			i.ModeGeneration = 1
			return &i
		}()} {
			response := manager.Handle(context.Background(), Request{
				Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
				PrivilegedAuth: token, LeshyRoute: fenced,
			})
			if response.OK || response.APIError == nil || response.APIError.Code != "managed_mode_required" {
				t.Fatalf("%s: local mode must deny the mutation: %#v", name, response)
			}
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("transition in flight denies the mutation", func(t *testing.T) {
		manager, token, _, intent := setup(t)
		manager.mu.Lock()
		manager.modeTransitionInFlight = true
		manager.mu.Unlock()
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "mode_transition_in_flight" {
			t.Fatalf("an in-flight transition must deny the mutation: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("zone not managed by the marker denied", func(t *testing.T) {
		manager, token, _, intent := setup(t)
		intent.Zone = "unknown"
		before := probeMutation(t, manager)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "zone_not_managed" {
			t.Fatalf("an unmanaged zone must be denied: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("empty auth denied for the transactions and the route methods", func(t *testing.T) {
		manager, _, _, intent := setup(t)
		before := probeMutation(t, manager)
		for _, tc := range []struct {
			method string
			check  func(Request) Request
		}{
			{"EnableManagedLeshy", func(r Request) Request { return r }},
			{"DisableManagedLeshy", func(r Request) Request { return r }},
			{"EnsureLeshyRoute", func(r Request) Request { r.LeshyRoute = &intent; return r }},
			{"ForgetLeshyRoute", func(r Request) Request { r.LeshyRoute = &intent; return r }},
			{"FlushLeshyZone", func(r Request) Request {
				r.LeshyRoute = &LeshyRouteIntent{Zone: "primary"}
				return r
			}},
		} {
			response := manager.Handle(context.Background(), tc.check(Request{
				Version: APIVersion, ID: "r", Method: tc.method,
			}))
			if response.OK || response.APIError == nil || response.APIError.Code != "privileged_auth_required" {
				t.Fatalf("%s: an empty token must be denied: %#v", tc.method, response)
			}
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("wrong auth denied before intent parsing", func(t *testing.T) {
		manager, _, _, _ := setup(t)
		// The intent is missing entirely: the auth denial must win.
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: "wrong-token",
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "privileged_auth_required" {
			t.Fatalf("a wrong token must be denied before intent parsing: %#v", response)
		}
		if response.APIError.Retryable {
			t.Fatal("the auth denial must be non-retryable")
		}
	})

	t.Run("v1 request under schema2 denied before mutation", func(t *testing.T) {
		manager, token, _, _ := setup(t)
		before := probeMutation(t, manager)
		intent := LeshyRouteIntent{Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS}
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "EnsureLeshyRoute",
			PrivilegedAuth: token, LeshyRoute: &intent,
		})
		if response.OK || response.APIError == nil || response.APIError.Code != "stale_activation" {
			t.Fatalf("a fenceless request under schema2 must be denied: %#v", response)
		}
		probeMutation(t, manager).equals(t, before)
	})

	t.Run("flush nil intent is an API error and the server survives", func(t *testing.T) {
		manager, token, _, _ := setup(t)
		response := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "r", Method: "FlushLeshyZone", PrivilegedAuth: token,
		})
		if response.OK || response.Error == "" {
			t.Fatalf("a nil leshy_route must be an API error: %#v", response)
		}
		snapshot := manager.Handle(context.Background(), Request{
			Version: APIVersion, ID: "s", Method: "Snapshot", PrivilegedAuth: token,
		})
		if !snapshot.OK || snapshot.Snapshot == nil {
			t.Fatalf("the server must survive the malformed request: %#v", snapshot)
		}
	})
}

func TestHandshakeCapabilityMatrix(t *testing.T) {
	t.Run("development core without a state dir claims no v2", func(t *testing.T) {
		dir := shortSocketDir(t)
		manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, controlTestSocket(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		handshake := manager.Handle(context.Background(), Request{Version: 3, Method: "Handshake"})
		if !handshake.OK {
			t.Fatalf("handshake: %#v", handshake)
		}
		if slices.Contains(handshake.Capabilities, CapabilityManagedSelectedRoutesV2) ||
			slices.Contains(handshake.Capabilities, CapabilityPrivilegedControlAuthV1) {
			t.Fatalf("a development core must not claim v2: %#v", handshake.Capabilities)
		}
		if !slices.Contains(handshake.Capabilities, CapabilityAFUnixControl) ||
			!slices.Contains(handshake.Capabilities, CapabilityManagedSelectedRoutesV1) {
			t.Fatalf("a development core keeps AF_UNIX and the v1 compatibility string: %#v", handshake.Capabilities)
		}
	})
	t.Run("persistent install with auth machinery claims v2", func(t *testing.T) {
		manager, _ := transactionManager(t)
		armPrivilegedAuthForTest(t, manager)
		handshake := manager.Handle(context.Background(), Request{Version: 3, Method: "Handshake"})
		for _, want := range []string{
			CapabilityAFUnixControl, CapabilityManagedSelectedRoutesV2, CapabilityPrivilegedControlAuthV1,
		} {
			if !slices.Contains(handshake.Capabilities, want) {
				t.Fatalf("a persistent install must advertise %s: %#v", want, handshake.Capabilities)
			}
		}
	})
	t.Run("active schema2 stops advertising v1", func(t *testing.T) {
		manager, _, _ := gateManager(t)
		handshake := manager.Handle(context.Background(), Request{Version: 3, Method: "Handshake"})
		if slices.Contains(handshake.Capabilities, CapabilityManagedSelectedRoutesV1) {
			t.Fatalf("an active schema2 authority must not advertise v1: %#v", handshake.Capabilities)
		}
		if !slices.Contains(handshake.Capabilities, CapabilityManagedSelectedRoutesV2) {
			t.Fatalf("an active schema2 authority keeps v2: %#v", handshake.Capabilities)
		}
	})
}

func TestLeshyRouteWireTypesRoundTrip(t *testing.T) {
	// The request side: exactly zone/prefix/source/activation_id/
	// mode_generation (D8 §8.1), mirroring the fields Leshy already sends.
	request := Request{
		Version: APIVersion,
		ID:      "r1",
		Method:  "EnsureLeshyRoute",
		Role:    "primary",
		LeshyRoute: &LeshyRouteIntent{
			Zone: "primary", Prefix: "2001:db8::10/128", Source: LeshyRouteDNS,
			ActivationID: "0123456789abcdef0123456789abcdef", ModeGeneration: 7,
		},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LeshyRoute == nil || decoded.LeshyRoute.Zone != "primary" ||
		decoded.LeshyRoute.Prefix != "2001:db8::10/128" || decoded.LeshyRoute.Source != LeshyRouteDNS ||
		decoded.LeshyRoute.ActivationID != "0123456789abcdef0123456789abcdef" || decoded.LeshyRoute.ModeGeneration != 7 {
		t.Fatalf("intent round-trip mismatch: %#v", decoded.LeshyRoute)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	var intent map[string]json.RawMessage
	if err := json.Unmarshal(raw["leshy_route"], &intent); err != nil {
		t.Fatal(err)
	}
	if len(intent) != 5 {
		t.Fatalf("the v2 wire intent must carry exactly the five fields, got %d keys: %v", len(intent), intent)
	}
	for _, key := range []string{"zone", "prefix", "source", "activation_id", "mode_generation"} {
		if _, ok := intent[key]; !ok {
			t.Fatalf("the v2 wire intent misses %q", key)
		}
	}

	// A legacy v1 intent serializes without the fence keys.
	legacy, err := json.Marshal(&LeshyRouteIntent{Zone: "z", Prefix: "10.0.0.1/32", Source: LeshyRouteDNS})
	if err != nil {
		t.Fatal(err)
	}
	var legacyMap map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &legacyMap); err != nil {
		t.Fatal(err)
	}
	if len(legacyMap) != 3 {
		t.Fatalf("the v1 wire intent must carry exactly zone/prefix/source, got %d", len(legacyMap))
	}

	// The exact JSON shape emitted by Leshy src/routing/kikimora.rs decodes.
	leshy := `{"zone":"corp","prefix":"203.0.113.10/32","source":"dns","activation_id":"0123456789abcdef0123456789abcdef","mode_generation":3}`
	var fromLeshy LeshyRouteIntent
	if err := json.Unmarshal([]byte(leshy), &fromLeshy); err != nil {
		t.Fatalf("the Leshy wire shape must decode: %v", err)
	}
	if fromLeshy.Zone != "corp" || fromLeshy.ActivationID != "0123456789abcdef0123456789abcdef" || fromLeshy.ModeGeneration != 3 {
		t.Fatalf("Leshy wire decode mismatch: %#v", fromLeshy)
	}

	// The response side: exact verdict with role, family, generation and the
	// activation fence echo.
	response := Response{
		Version: APIVersion,
		ID:      "r1",
		OK:      true,
		LeshyRoute: &LeshyRouteResult{
			Role: "primary", Zone: "primary", Prefix: "2001:db8::10/128",
			Family: 6, Generation: 7, State: "applied",
			ActivationID: "0123456789abcdef0123456789abcdef", ModeGeneration: 7,
		},
	}
	encoded, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResp Response
	if err := json.Unmarshal(encoded, &decodedResp); err != nil {
		t.Fatal(err)
	}
	if decodedResp.LeshyRoute == nil || decodedResp.LeshyRoute.Family != 6 ||
		decodedResp.LeshyRoute.Generation != 7 || decodedResp.LeshyRoute.State != "applied" ||
		decodedResp.LeshyRoute.ActivationID != "0123456789abcdef0123456789abcdef" ||
		decodedResp.LeshyRoute.ModeGeneration != 7 {
		t.Fatalf("result round-trip mismatch: %#v", decodedResp.LeshyRoute)
	}
}

// TestLocalRoleIsNeverASelectedWriter is the §8.5 defense-in-depth matrix:
// the manager itself denies a non-managed role before any writer call.
func TestLocalRoleIsNeverASelectedWriter(t *testing.T) {
	fake := newSelectedRoutesFake()
	manager := NewSelectedRouteManager(
		routing.NewMemorySelectedRegistry(),
		fake,
		func(string) (core.RoleRuntime, bool) { return readyRuntime("primary", true, true), true },
		map[string][]string{"primary": {"primary"}},
		func(string) (string, bool) { return t.TempDir(), true },
	)
	manager.SetActivationFence(testActivationFence)
	// No managed-role check installed: deny everything (fail-closed default).
	if _, err := manager.Ensure(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err == nil || !isLeshyRouteVerdictError(err) {
		t.Fatalf("an unauthorized role must be denied: %v", err)
	}
	if err := manager.Forget(context.Background(), LeshyRouteIntent{
		Zone: "primary", Prefix: "203.0.113.10/32", Source: LeshyRouteDNS,
	}); err == nil || !isLeshyRouteVerdictError(err) {
		t.Fatalf("an unauthorized forget must be denied: %v", err)
	}
	if err := manager.FlushZone(context.Background(), "primary"); err == nil || !isLeshyRouteVerdictError(err) {
		t.Fatalf("an unauthorized flush must be denied: %v", err)
	}
	if fake.reconcileCount() != 0 || len(fake.removes) != 0 {
		t.Fatalf("denied mutations must not touch the writer: reconciles=%d removes=%d",
			fake.reconcileCount(), len(fake.removes))
	}
	// The exact prefix on the denial is the API contract.
	err := manager.FlushZone(context.Background(), "primary")
	var denied *SelectedRouteDeniedError
	if !errors.As(err, &denied) || denied.Reason != "role is not managed by the active schema2 marker" {
		t.Fatalf("the denial must carry the mode-authorization marker: %v", err)
	}
}
