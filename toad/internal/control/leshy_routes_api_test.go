package control

import (
	"context"
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
