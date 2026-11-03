package control

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestAPIV3HandshakeAdvertisesManagedRoutesCapability(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	handshake := manager.Handle(context.Background(), Request{Version: 3, Method: "Handshake"})
	if !handshake.OK || handshake.Version != 3 || handshake.MinVersion != 1 || handshake.MaxVersion != 3 {
		t.Fatalf("bad v3 handshake: %#v", handshake)
	}
	if !slices.Contains(handshake.Capabilities, ManagedSelectedRoutesCapability) {
		t.Fatalf("v3 handshake must advertise %s: %#v", ManagedSelectedRoutesCapability, handshake.Capabilities)
	}
}

func TestManagedRouteMethodsRequireVersion3(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		for _, method := range []string{"EnsureLeshyRoute", "ForgetLeshyRoute", "FlushLeshyZone"} {
			response := manager.Handle(context.Background(), Request{
				Version:    version,
				ID:         "t",
				Method:     method,
				Role:       "primary",
				LeshyRoute: &LeshyRouteIntent{Zone: "primary", Prefix: "10.0.0.1/32", Source: LeshyRouteDNS},
			})
			if response.OK {
				t.Fatalf("v%d %s must not succeed: %#v", version, method, response)
			}
			if response.APIError == nil || response.APIError.Code != "unsupported_version" {
				t.Fatalf("v%d %s must be rejected as unsupported_version: %#v", version, method, response)
			}
		}
	}
}

func TestAPIV1V2UnchangedByManagedTypes(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// Old methods keep working on every version, and responses carry no
	// leshy_route payload.
	for _, version := range []int{1, 2, 3} {
		handshake := manager.Handle(context.Background(), Request{Version: version, Method: "Handshake"})
		if !handshake.OK || handshake.LeshyRoute != nil {
			t.Fatalf("v%d handshake changed: %#v", version, handshake)
		}
		snapshot := manager.Handle(context.Background(), Request{Version: version, Method: "GetSnapshot"})
		if !snapshot.OK || snapshot.Snapshot == nil || snapshot.LeshyRoute != nil {
			t.Fatalf("v%d snapshot changed: %#v", version, snapshot)
		}
	}
}

func TestLeshyRouteWireTypesRoundTrip(t *testing.T) {
	// The request side: only zone/prefix/source, mirroring the 08D contract.
	request := Request{
		Version:    3,
		ID:         "r1",
		Method:     "EnsureLeshyRoute",
		Role:       "primary",
		LeshyRoute: &LeshyRouteIntent{Zone: "primary", Prefix: "2001:db8::10/128", Source: LeshyRouteDNS},
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
		decoded.LeshyRoute.Prefix != "2001:db8::10/128" || decoded.LeshyRoute.Source != LeshyRouteDNS {
		t.Fatalf("intent round-trip mismatch: %#v", decoded.LeshyRoute)
	}
	if !slices.Contains([]string{"zone", "prefix", "source"}, "zone") {
		t.Fatal("unreachable")
	}
	// The intent may not grow controller-derived fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	var intent map[string]json.RawMessage
	if err := json.Unmarshal(raw["leshy_route"], &intent); err != nil {
		t.Fatal(err)
	}
	if len(intent) != 3 {
		t.Fatalf("the wire intent must carry exactly zone/prefix/source, got %d keys", len(intent))
	}

	// The response side: exact verdict with role, family and generation.
	response := Response{
		Version:    3,
		ID:         "r1",
		OK:         true,
		LeshyRoute: &LeshyRouteResult{Role: "primary", Zone: "primary", Prefix: "2001:db8::10/128", Family: 6, Generation: 7, State: "applied"},
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
		decodedResp.LeshyRoute.Generation != 7 || decodedResp.LeshyRoute.State != "applied" {
		t.Fatalf("result round-trip mismatch: %#v", decodedResp.LeshyRoute)
	}
}

func TestLeshyRouteFamilyDetection(t *testing.T) {
	cases := map[string]int{
		"10.0.0.1/32":     4,
		"10.0.0.0/8":      4,
		"2001:db8::1/128": 6,
		"fd00::/8":        6,
		"not-a-prefix":    0,
	}
	for prefix, want := range cases {
		if got := leshyRouteFamily(prefix); got != want {
			t.Fatalf("%s: family %d, want %d", prefix, got, want)
		}
	}
}
