package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const SchemaVersion = 1

type Snapshot struct {
	Schema     int            `json:"schema"`
	Name       string         `json:"name"`
	Protocol   string         `json:"protocol"`
	Generation uint64         `json:"generation"`
	State      string         `json:"state"`
	Reason     string         `json:"reason"`
	RouteReady bool           `json:"route_ready"`
	Interface  InterfaceState `json:"interface"`
	Session    SessionState   `json:"session"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type InterfaceState struct {
	Name    string `json:"name"`
	IfIndex int    `json:"ifindex"`
	MTU     int    `json:"mtu"`
}

type SessionState struct {
	Connected          bool   `json:"connected"`
	LastHandshakeAgeMS *int64 `json:"last_handshake_age_ms,omitempty"`
	RXBytes            uint64 `json:"rx_bytes,omitempty"`
	TXBytes            uint64 `json:"tx_bytes,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
}

func New(name, protocol, iface string, mtu int) Snapshot {
	return Snapshot{
		Schema:    SchemaVersion,
		Name:      name,
		Protocol:  protocol,
		State:     "starting",
		Reason:    "process-started",
		Interface: InterfaceState{Name: iface, MTU: mtu},
		UpdatedAt: time.Now().UTC(),
	}
}

type Writer struct {
	Dir string
}

func (w Writer) Write(snapshot Snapshot) error {
	if w.Dir == "" {
		return fmt.Errorf("state directory is empty")
	}
	if err := os.MkdirAll(w.Dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	snapshot.Schema = SchemaVersion
	snapshot.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(w.Dir, ".state.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary state file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}

	finalPath := filepath.Join(w.Dir, "state.json")
	if err := os.Rename(tmpName, finalPath); err != nil {
		return fmt.Errorf("publish state: %w", err)
	}
	return nil
}
