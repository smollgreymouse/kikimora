package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

const DesiredStateSchema = 1

type PersistedDesiredState struct {
	Schema        int             `json:"schema"`
	ActiveProfile string          `json:"active_profile"`
	Roles         map[string]bool `json:"roles"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type DesiredStateStore interface {
	Load(context.Context) (PersistedDesiredState, error)
	Save(context.Context, PersistedDesiredState) error
}

type FileDesiredStateStore struct {
	Path string
}

func (s FileDesiredStateStore) Load(ctx context.Context) (PersistedDesiredState, error) {
	if err := ctx.Err(); err != nil {
		return PersistedDesiredState{}, err
	}
	if s.Path == "" {
		return PersistedDesiredState{}, errors.New("desired-state path is empty")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return PersistedDesiredState{}, err
	}
	var state PersistedDesiredState
	if err := json.Unmarshal(data, &state); err != nil {
		return PersistedDesiredState{}, fmt.Errorf("decode desired state: %w", err)
	}
	if state.Schema != DesiredStateSchema {
		return PersistedDesiredState{}, fmt.Errorf("unsupported desired-state schema %d", state.Schema)
	}
	if state.ActiveProfile == "" {
		state.ActiveProfile = "default"
	}
	if state.Roles == nil {
		state.Roles = map[string]bool{}
	}
	return state, nil
}

func (s FileDesiredStateStore) Save(ctx context.Context, state PersistedDesiredState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Path == "" {
		return errors.New("desired-state path is empty")
	}
	dir := filepath.Dir(s.Path)
	if dir == "." || dir == "" {
		return errors.New("desired-state path must include a directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create desired-state directory: %w", err)
	}

	state.Schema = DesiredStateSchema
	if state.ActiveProfile == "" {
		state.ActiveProfile = "default"
	}
	if state.Roles == nil {
		state.Roles = map[string]bool{}
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode desired state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".desired.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary desired-state file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod desired-state file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write desired state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync desired-state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close desired-state file: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("publish desired state: %w", err)
	}
	if err := os.Chmod(s.Path, 0o600); err != nil {
		return fmt.Errorf("protect desired-state file: %w", err)
	}
	if err := fsutil.SyncDir(dir); err != nil {
		return fmt.Errorf("sync desired-state directory: %w", err)
	}
	return nil
}

func IsDesiredStateMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
