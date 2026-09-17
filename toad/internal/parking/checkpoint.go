package parking

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Checkpoint struct {
	BootID         string       `json:"boot_id"`
	CoreInstanceID string       `json:"core_instance_id"`
	Role           string       `json:"role"`
	Interface      string       `json:"interface"`
	IfIndex        int          `json:"ifindex"`
	Baseline       []OwnedRoute `json:"baseline"`
	Observed       []OwnedRoute `json:"observed"`
	Parked         []OwnedRoute `json:"parked"`
}

func WriteCheckpoint(path string, value Checkpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".parking-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func ReadCheckpoint(path string) (Checkpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var c Checkpoint
	if err := json.Unmarshal(b, &c); err != nil {
		return Checkpoint{}, fmt.Errorf("decode parking checkpoint: %w", err)
	}
	return c, nil
}
