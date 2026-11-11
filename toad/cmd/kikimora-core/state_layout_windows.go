//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// windowsCoreStateLayoutVersion is the authoritative layout generation of the
// installed core state root (Windows packaging policy, not cross-platform
// managed-mode policy).
const windowsCoreStateLayoutVersion = 2

// coreStateLayoutMarkerName is the completion marker inside the new root.
// Once it strict-reads as schema 1 / layout 2, the migration is complete and
// the legacy parent copies are never read for authority again.
const coreStateLayoutMarkerName = "core-state-layout.json"

// windowsCoreStateLayoutMarker is the exact on-disk marker contract:
// {"schema":1,"layout":2}.
type windowsCoreStateLayoutMarker struct {
	Schema int `json:"schema"`
	Layout int `json:"layout"`
}

// windowsCoreStateLayoutFiles lists exactly the legacy core-owned authority
// files that migrate from <root>\state into <root>\state\core. Per-Toad
// selected-routes state, the writer/core locks and diagnostics intentionally
// stay out of the copy set; leshy-writer.lock keeps living at
// <root>\state\leshy-writer.lock.
var windowsCoreStateLayoutFiles = []string{
	"desired.json",
	"leshy-mode-generation.json",
	"leshy-mode-transition.json",
	"control-route-auth.key",
}

// legacyWindowsCoreStateRoot returns the pre-Phase-E state root.
func legacyWindowsCoreStateRoot(root string) string {
	return filepath.Join(root, "state")
}

// windowsCoreStateRoot returns the authoritative post-Phase-E state root.
func windowsCoreStateRoot(root string) string {
	return filepath.Join(root, "state", "core")
}

// migrateLegacyWindowsCoreState performs the one-time, crash-resumable
// copy-and-verify migration of the legacy core state layout into the
// authoritative state\core root. The legacy files are never renamed or
// deleted: they remain available for package rollback, and only state\core is
// authoritative after the completion marker lands.
//
// Algorithm (R1 §3.1, in this order):
//  1. create state\core if missing;
//  2. a valid completion marker short-circuits success without reading any
//     legacy authority file;
//  3. for each of the four authority files: absent/absent skips,
//     source-present copies durably, both-present requires byte-for-byte
//     equality (a mismatch is fatal and is never auto-resolved),
//     source-absent keeps the destination;
//  4. the completion marker is written durably only after every file passed;
//  5. the parent copies always survive.
//
// A crash after copying any subset simply reruns step 3 on the next start. A
// conflicting partial migration fails closed.
func migrateLegacyWindowsCoreState(root string) error {
	newRoot := windowsCoreStateRoot(root)
	markerPath := filepath.Join(newRoot, coreStateLayoutMarkerName)

	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		return fmt.Errorf("create core state root: %w", err)
	}

	if data, err := os.ReadFile(markerPath); err == nil {
		var marker windowsCoreStateLayoutMarker
		if err := json.Unmarshal(data, &marker); err != nil {
			return fmt.Errorf("strict-read %s: %w", coreStateLayoutMarkerName, err)
		}
		if marker.Schema != 1 || marker.Layout != windowsCoreStateLayoutVersion {
			return fmt.Errorf("unsupported %s content schema=%d layout=%d",
				coreStateLayoutMarkerName, marker.Schema, marker.Layout)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", coreStateLayoutMarkerName, err)
	}

	legacyRoot := legacyWindowsCoreStateRoot(root)
	for _, name := range windowsCoreStateLayoutFiles {
		src := filepath.Join(legacyRoot, name)
		dst := filepath.Join(newRoot, name)
		if err := migrateOneLegacyFile(name, src, dst); err != nil {
			return err
		}
	}

	if err := fsutil.WriteAtomicJSON(markerPath, windowsCoreStateLayoutMarker{
		Schema: 1,
		Layout: windowsCoreStateLayoutVersion,
	}, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", coreStateLayoutMarkerName, err)
	}
	return nil
}

// migrateOneLegacyFile implements the four-case copy-and-verify contract for
// exactly one authority file.
func migrateOneLegacyFile(name, src, dst string) error {
	srcData, srcErr := os.ReadFile(src)
	dstData, dstErr := os.ReadFile(dst)

	switch {
	case os.IsNotExist(srcErr) && os.IsNotExist(dstErr):
		// Nothing to migrate in either generation.
		return nil
	case srcErr != nil:
		return fmt.Errorf("read legacy %s: %w", name, srcErr)
	case dstErr != nil && !os.IsNotExist(dstErr):
		return fmt.Errorf("read migrated %s: %w", name, dstErr)
	case os.IsNotExist(dstErr):
		// Source present, destination absent: durable identical copy. The
		// state\core tree is hardened by the installer; the copied credential
		// is loaded through the same EnsurePrivilegedAuthKey path as any
		// pre-existing key.
		if err := fsutil.WriteAtomicFile(dst, srcData, 0o600); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
		return nil
	case os.IsNotExist(srcErr):
		// Source absent, destination present: keep the destination.
		return nil
	case !bytes.Equal(srcData, dstData):
		// Both present with different bytes: a conflicting partial migration
		// fails closed instead of silently picking a winner.
		return fmt.Errorf("core state migration conflict for %s: legacy and migrated copies differ; refusing to auto-resolve", name)
	}
	return nil
}
