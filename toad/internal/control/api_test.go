package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServeCallRoundTrip(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "core.sock")
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, socket, manager) }()

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}

	handshake, err := Call(socket, Request{Version: APIVersion, Method: "Handshake"})
	if err != nil {
		t.Fatal(err)
	}
	if !handshake.OK || len(handshake.Capabilities) == 0 {
		t.Fatalf("bad handshake: %#v", handshake)
	}

	connect, err := Call(socket, Request{Version: APIVersion, Method: "ConnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	if !connect.OK || connect.Snapshot == nil {
		t.Fatalf("ConnectAll must return a snapshot: %#v", connect)
	}

	disconnect, err := Call(socket, Request{Version: APIVersion, Method: "DisconnectAll"})
	if err != nil {
		t.Fatal(err)
	}
	if !disconnect.OK || disconnect.Snapshot == nil {
		t.Fatalf("DisconnectAll must return a snapshot: %#v", disconnect)
	}
	if disconnect.Snapshot.Revision <= connect.Snapshot.Revision {
		t.Fatalf("revision did not advance: %d -> %d", connect.Snapshot.Revision, disconnect.Snapshot.Revision)
	}
}

func TestAPIV2HandshakeNegotiatesRangeAndStructuredErrors(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, filepath.Join(dir, "core.sock"))
	if err != nil {
		t.Fatal(err)
	}
	handshake := manager.Handle(context.Background(), Request{Version: 2, Method: "Handshake"})
	if !handshake.OK || handshake.Version != 2 || handshake.MinVersion != 1 || handshake.MaxVersion != 2 {
		t.Fatalf("bad v2 handshake: %#v", handshake)
	}
	missing := manager.Handle(context.Background(), Request{Version: 2, Method: "ConnectRole", Role: "missing"})
	if missing.OK || missing.APIError == nil || missing.APIError.Code != "command_failed" || !missing.APIError.Retryable {
		t.Fatalf("structured command error missing: %#v", missing)
	}
}
