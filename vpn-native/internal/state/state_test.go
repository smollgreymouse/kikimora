package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterPublishes0600JSON(t *testing.T) {
	dir := t.TempDir()
	writer := Writer{Dir: filepath.Join(dir, "state")}
	snapshot := New("awg-main", "amneziawg2", "kk-awg0", 1380)
	snapshot.Generation = 7
	snapshot.RouteReady = true
	snapshot.State = "online"
	snapshot.Reason = "handshake-established"
	snapshot.Interface.IfIndex = 42
	snapshot.Session.Connected = true
	snapshot.Session.Endpoint = "192.0.2.1:51820"

	if err := writer.Write(snapshot); err != nil {
		t.Fatalf("write state: %v", err)
	}

	path := filepath.Join(writer.Dir, "state.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %o, want 600", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if decoded.Schema != SchemaVersion || decoded.Interface.IfIndex != 42 || !decoded.RouteReady {
		t.Fatalf("unexpected state: %+v", decoded)
	}
}

func TestSnapshotHasNoSecretFields(t *testing.T) {
	snapshot := New("xray-main", "vless-reality", "kk-xray0", 1380)
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"private_key", "peer_public_key", "preshared_key", "uuid", "public_key", "short_id"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("state schema unexpectedly exposes secret/config field %q: %s", forbidden, text)
		}
	}
}
