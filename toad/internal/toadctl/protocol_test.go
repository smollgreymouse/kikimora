package toadctl

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type testHandler struct{ rev uint64 }

func (h testHandler) Handle(context.Context, Request) Response {
	switch {
	}
	return Response{Version: ProtocolVersion, OK: true, Snapshot: &Snapshot{Revision: h.rev}}
}
func (h testHandler) WaitForRevision(context.Context, uint64) (Snapshot, error) {
	return Snapshot{Revision: h.rev + 1}, nil
}
func TestClientServerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toad.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = (Server{Socket: path, Handler: testHandler{rev: 1}}).Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := net.Dial("unix", path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket not ready")
		}
		time.Sleep(time.Millisecond)
	}
	res, err := (Client{Socket: path}).Call(ctx, Request{Version: ProtocolVersion, Method: "Inspect"})
	if err != nil || !res.OK || res.Snapshot == nil {
		t.Fatalf("roundtrip: %#v %v", res, err)
	}
}

func TestSnapshotWithoutFamiliesDecodesAsZeroNotReady(t *testing.T) {
	// A pre-K1 snapshot has no families field: it must decode cleanly, keep
	// the legacy bit untouched and yield a zero (not-ready) family state —
	// never an inferred IPv6 readiness.
	old := `{"protocol_version":1,"revision":3,"generation":1,"state":"online","route_ready":true}`
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(old), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.RouteReady {
		t.Fatal("legacy route_ready must survive decoding")
	}
	if snapshot.Families != (RouteFamilies{}) {
		t.Fatalf("families must be zero, got %#v", snapshot.Families)
	}
	if snapshot.Families.IPv6.Ready || snapshot.Families.IPv6.Configured {
		t.Fatal("IPv6 readiness must never be inferred from the legacy bit")
	}
	if snapshot.Families.Ready() {
		t.Fatal("zero families are not ready")
	}
}

func TestFamiliesJSONRoundTrip(t *testing.T) {
	families := RouteFamilies{
		IPv4: FamilyReadiness{Configured: true, Ready: true},
		IPv6: FamilyReadiness{Configured: true, Ready: false, Reason: "no negotiated prefix"},
	}
	encoded, err := json.Marshal(families)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RouteFamilies
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != families {
		t.Fatalf("round-trip mismatch: %#v", decoded)
	}
	if !decoded.Ready() {
		t.Fatal("IPv4 ready alone satisfies the compatibility bridge")
	}
}

func TestValidationResultCarriesFamilies(t *testing.T) {
	validation := ValidationResult{Healthy: true, State: "healthy", Families: RouteFamilies{IPv4: FamilyReadiness{Configured: true, Ready: true}}}
	encoded, err := json.Marshal(validation)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ValidationResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Families.IPv4.Ready || decoded.Families.IPv6.Configured {
		t.Fatalf("validation families mismatch: %#v", decoded.Families)
	}
}
