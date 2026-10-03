package toadctl

import (
	"context"
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
