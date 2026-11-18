package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// PC8A §1.4: the profile-composition undo journal makes a multi-file profile
// edit crash-atomic. ProfileCompositionTxn's ordinary rollback covers
// returned errors only; a process or power loss between the file writes and
// the desired-state persistence can otherwise leave a half-committed
// composition that startup would accept. The journal stores the exact
// ORIGINAL images needed to undo one incomplete transaction, is written
// durably BEFORE any policy mutation, and a journal that still exists at
// process start always means "the previous composition did not reach its
// commit point": recovery rolls the files back and consumes the journal.

const ProfileCompositionUndoSchema = 2

// ProfileCompositionUndoPhase is the durable commit-phase marker (R8A §1.5,
// 2026-11-17 residual audit): prepared = the transaction has NOT reached its
// commit point and startup rolls it back; committed = the transaction is
// fully live and startup only cleans up the stale journal. Schema-1 journals
// (from the first PC8A build) are always treated as prepared.
type ProfileCompositionUndoPhase string

const (
	ProfileUndoPrepared  ProfileCompositionUndoPhase = "prepared"
	ProfileUndoCommitted ProfileCompositionUndoPhase = "committed"
)

// ProfileUndoFile is one original file image in the journal. Kind selects the
// reconstruction path at recovery time; no arbitrary absolute paths are
// persisted.
type ProfileUndoFile struct {
	Kind    string `json:"kind"` // "role" | "registry" | "desired"
	Name    string `json:"name,omitempty"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode"`
	Data    []byte `json:"data,omitempty"` // base64 in JSON
}

// ProfileCompositionUndo is the durable undo record.
type ProfileCompositionUndo struct {
	Schema int                         `json:"schema"`
	Phase  ProfileCompositionUndoPhase `json:"phase,omitempty"`
	Files  []ProfileUndoFile           `json:"files"`
}

// ProfileCompositionUndoPath is the journal location under the hardened core
// state root. The payload may contain private VPN credentials (original
// tunnel TOMLs), so the file is always written mode 0600 and its content
// must never reach diagnostics bundles (presence/schema only).
func ProfileCompositionUndoPath(stateDir string) string {
	return filepath.Join(stateDir, "profile-composition-undo.json")
}

// WriteProfileCompositionUndo durably writes the undo journal (mode 0600)
// before any policy mutation.
func WriteProfileCompositionUndo(stateDir string, undo ProfileCompositionUndo) error {
	if stateDir == "" {
		return errors.New("profile composition undo journal requires the core state directory")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create the core state directory: %w", err)
	}
	undo.Schema = ProfileCompositionUndoSchema
	if undo.Phase == "" {
		undo.Phase = ProfileUndoPrepared
	}
	return fsutil.WriteAtomicJSON(ProfileCompositionUndoPath(stateDir), undo, 0o600)
}

// MarkProfileCompositionCommitted durably rewrites the journal phase to
// committed — the LINEARIZATION POINT of the profile transaction (R8A §1.5).
// After this succeeds the transaction effects are final: a startup that finds
// the journal only removes it, never rolls the live composition back.
func MarkProfileCompositionCommitted(stateDir string) error {
	undo, err := ReadProfileCompositionUndo(stateDir)
	if err != nil {
		return err
	}
	if undo == nil {
		return nil
	}
	undo.Phase = ProfileUndoCommitted
	return fsutil.WriteAtomicJSON(ProfileCompositionUndoPath(stateDir), undo, 0o600)
}

// RemoveProfileCompositionUndo durably removes the journal after a proven
// commit or a proven rollback.
func RemoveProfileCompositionUndo(stateDir string) error {
	return fsutil.RemoveDurable(ProfileCompositionUndoPath(stateDir))
}

// ReadProfileCompositionUndo loads a pending journal. A missing journal is
// (nil, nil).
func ReadProfileCompositionUndo(stateDir string) (*ProfileCompositionUndo, error) {
	data, err := os.ReadFile(ProfileCompositionUndoPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the profile composition undo journal: %w", err)
	}
	var undo ProfileCompositionUndo
	if err := json.Unmarshal(data, &undo); err != nil {
		return nil, fmt.Errorf("parse the profile composition undo journal: %w", err)
	}
	// R8A §1.5: schema1 journals (from the first PC8A build, before the
	// committed-phase field existed) are rollback-compatible — recovery
	// treats them as prepared. Schema2 is the live format.
	if undo.Schema != ProfileCompositionUndoSchema && undo.Schema != 1 {
		return nil, fmt.Errorf("profile composition undo journal schema %d is not supported", undo.Schema)
	}
	return &undo, nil
}

// RecoverProfileCompositionUndo rolls an incomplete transaction back to the
// original images and consumes the journal. It must run after the core state
// lock is acquired and before NewManager loads any role TOML or the registry,
// so the manager reads one coherent old-or-committed set.
func RecoverProfileCompositionUndo(stateDir, configDir string) error {
	undo, err := ReadProfileCompositionUndo(stateDir)
	if err != nil {
		return err
	}
	if undo == nil {
		return nil
	}
	// R8A §1.5: schema1 (from the first PC8A build) and schema2 prepared both
	// mean "the transaction did not reach its commit point" — roll back. A
	// schema2 committed journal means the transaction IS live: only clean up
	// the stale journal, never roll the running composition back.
	committed := undo.Schema == ProfileCompositionUndoSchema && undo.Phase == ProfileUndoCommitted
	if committed {
		if err := RemoveProfileCompositionUndo(stateDir); err != nil {
			return fmt.Errorf("consume the committed profile composition undo journal: %w", err)
		}
		return nil
	}
	for _, file := range undo.Files {
		path, err := undoFilePath(stateDir, configDir, file)
		if err != nil {
			return err
		}
		if !file.Existed {
			if err := fsutil.RemoveDurable(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("undo journal: remove %s: %w", path, err)
			}
			continue
		}
		if err := fsutil.WriteAtomicFile(path, file.Data, os.FileMode(file.Mode)); err != nil {
			return fmt.Errorf("undo journal: restore %s: %w", path, err)
		}
	}
	// The rollback is proven: consume the journal. Removal happens only after
	// every original image is restored, so a crash mid-rollback leaves the
	// journal for the next start (idempotent re-undo).
	if err := RemoveProfileCompositionUndo(stateDir); err != nil {
		return fmt.Errorf("consume the profile composition undo journal: %w", err)
	}
	return nil
}

// undoFilePath reconstructs the durable path for one journal entry. No
// arbitrary absolute paths are persisted: role names are validated against
// the instance-name rule before they reach a file path.
func undoFilePath(stateDir, configDir string, file ProfileUndoFile) (string, error) {
	switch file.Kind {
	case "role":
		name := file.Name
		if !instanceNameRE.MatchString(name) {
			return "", fmt.Errorf("undo journal: invalid role name %q", name)
		}
		return filepath.Join(configDir, name+".toml"), nil
	case "registry":
		return declaredProfilesPath(configDir), nil
	case "desired":
		return filepath.Join(stateDir, "desired.json"), nil
	default:
		return "", fmt.Errorf("undo journal: unknown file kind %q", file.Kind)
	}
}
