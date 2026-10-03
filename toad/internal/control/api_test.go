package control

import (
	"context"
	"errors"
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

func TestSubscribeClientStreamsInitialAndNewRevision(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "core.sock")
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
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

	subCtx, cancelSub := context.WithCancel(context.Background())
	revisions := make(chan uint64, 4)
	done := make(chan error, 1)
	go func() {
		done <- Subscribe(subCtx, socket, Request{
			Version: APIVersion,
			ID:      "test-subscribe-client",
			Method:  "Subscribe",
		}, func(response Response) error {
			if response.Snapshot == nil {
				return errors.New("missing snapshot")
			}
			revisions <- response.Snapshot.Revision
			return nil
		})
	}()

	var initial uint64
	select {
	case initial = <-revisions:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial subscription snapshot")
	}

	if _, err := Call(socket, Request{Version: APIVersion, Method: "ConnectAll"}); err != nil {
		t.Fatal(err)
	}

	select {
	case next := <-revisions:
		if next <= initial {
			t.Fatalf("subscription revision did not advance: %d -> %d", initial, next)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for newer subscription snapshot")
	}

	cancelSub()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not stop after cancellation")
	}
}
