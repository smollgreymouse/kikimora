//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyCoreState stages one legacy root with the given authority-file
// contents and returns the root. A nil value means the file must not exist.
func writeLegacyCoreState(t *testing.T, root string, contents map[string]*string) {
	t.Helper()
	legacyRoot := legacyWindowsCoreStateRoot(root)
	if err := os.MkdirAll(legacyRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range windowsCoreStateLayoutFiles {
		value, ok := contents[name]
		if !ok || value == nil {
			continue
		}
		path := filepath.Join(legacyRoot, name)
		if err := os.WriteFile(path, []byte(*value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func strPtr(s string) *string { return &s }

func readOrFail(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestWindowsCoreStateMigrationFresh: a root with no legacy files and no new
// root produces state\core plus the completion marker and nothing else.
func TestWindowsCoreStateMigrationFresh(t *testing.T) {
	root := t.TempDir()
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	marker := readOrFail(t, filepath.Join(windowsCoreStateRoot(root), coreStateLayoutMarkerName))
	if !strings.Contains(string(marker), `"schema":1`) || !strings.Contains(string(marker), `"layout":2`) {
		t.Fatalf("completion marker = %s, want schema 1 layout 2", marker)
	}
	for _, name := range windowsCoreStateLayoutFiles {
		if _, err := os.Stat(filepath.Join(windowsCoreStateRoot(root), name)); !os.IsNotExist(err) {
			t.Fatalf("%s must not exist after a fresh migration", name)
		}
	}
}

// TestWindowsCoreStateMigrationLegacyLocal copies desired.json byte-for-byte
// and keeps the legacy source in place.
func TestWindowsCoreStateMigrationLegacyLocal(t *testing.T) {
	root := t.TempDir()
	desired := `{"roles":{"alpha":{"desired":true}}}`
	writeLegacyCoreState(t, root, map[string]*string{"desired.json": strPtr(desired)})

	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	got := readOrFail(t, filepath.Join(windowsCoreStateRoot(root), "desired.json"))
	if string(got) != desired {
		t.Fatalf("migrated desired.json = %q, want %q", got, desired)
	}
	// The legacy source survives for package rollback.
	if string(readOrFail(t, filepath.Join(legacyWindowsCoreStateRoot(root), "desired.json"))) != desired {
		t.Fatal("legacy desired.json was removed")
	}
}

// TestWindowsCoreStateMigrationLegacyManaged copies the full managed set:
// generation file, auth key and desired state.
func TestWindowsCoreStateMigrationLegacyManaged(t *testing.T) {
	root := t.TempDir()
	files := map[string]*string{
		"desired.json":               strPtr(`{"roles":{"beta":{"desired":true}}}`),
		"leshy-mode-generation.json": strPtr(`{"generation":7}`),
		"control-route-auth.key":     strPtr(strings.Repeat("ab", 32) + "\n"),
	}
	writeLegacyCoreState(t, root, files)

	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"desired.json", "leshy-mode-generation.json", "control-route-auth.key"} {
		got := readOrFail(t, filepath.Join(windowsCoreStateRoot(root), name))
		if string(got) != *files[name] {
			t.Fatalf("migrated %s = %q, want %q", name, got, *files[name])
		}
	}
	// A valid completion marker requires every authority file to have passed.
	readOrFail(t, filepath.Join(windowsCoreStateRoot(root), coreStateLayoutMarkerName))
}

// TestWindowsCoreStateMigrationResumesPartialCopy: a crash after copying one
// file leaves it byte-identical in the new root; the rerun completes without
// rewriting it and keeps the source.
func TestWindowsCoreStateMigrationResumesPartialCopy(t *testing.T) {
	root := t.TempDir()
	generation := `{"generation":3}`
	writeLegacyCoreState(t, root, map[string]*string{
		"leshy-mode-generation.json": strPtr(generation),
		"desired.json":               strPtr(`{}`),
	})
	// Simulate the partial copy: the new root already holds generation bytes.
	newRoot := windowsCoreStateRoot(root)
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "leshy-mode-generation.json"), []byte(generation), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatalf("resume after partial copy: %v", err)
	}
	if string(readOrFail(t, filepath.Join(newRoot, "leshy-mode-generation.json"))) != generation {
		t.Fatal("partial copy was not preserved byte-for-byte")
	}
	// Rerun is idempotent.
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	readOrFail(t, filepath.Join(newRoot, coreStateLayoutMarkerName))
}

// TestWindowsCoreStateMigrationRejectsConflict: a destination that already
// differs from the legacy source fails closed and writes no completion marker.
func TestWindowsCoreStateMigrationRejectsConflict(t *testing.T) {
	root := t.TempDir()
	writeLegacyCoreState(t, root, map[string]*string{
		"desired.json": strPtr(`{"legacy":true}`),
	})
	newRoot := windowsCoreStateRoot(root)
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "desired.json"), []byte(`{"diverged":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := migrateLegacyWindowsCoreState(root)
	if err == nil {
		t.Fatal("conflicting partial migration must fail")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("error = %v, want a conflict failure", err)
	}
	if _, err := os.Stat(filepath.Join(newRoot, coreStateLayoutMarkerName)); !os.IsNotExist(err) {
		t.Fatal("completion marker must not exist after a conflicting migration")
	}
}

// TestWindowsCoreStateMigrationCompletionMarkerIsAuthoritative: once the
// marker exists, stale parent copies (even differing ones) never overwrite
// the new root.
func TestWindowsCoreStateMigrationCompletionMarkerIsAuthoritative(t *testing.T) {
	root := t.TempDir()
	desired := `{"committed":true}`
	// Equal legacy and migrated bytes so the first migration completes.
	writeLegacyCoreState(t, root, map[string]*string{"desired.json": strPtr(desired)})
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	newRoot := windowsCoreStateRoot(root)
	readOrFail(t, filepath.Join(newRoot, coreStateLayoutMarkerName))

	// Diverge the legacy copy after completion.
	stale := `{"rewritten-after-completion":true}`
	writeLegacyCoreState(t, root, map[string]*string{"desired.json": strPtr(stale)})
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatalf("rerun with a valid marker must succeed: %v", err)
	}
	got := string(readOrFail(t, filepath.Join(newRoot, "desired.json")))
	if got != desired {
		t.Fatalf("stale legacy copy overwrote the new root: %q", got)
	}
}

// TestWindowsCoreStateMigrationCopiesInFlightTransition: a staged in-flight
// leshy-mode-transition.json reaches the new root unchanged so
// RecoverManagedModeTransition can converge from the new generation root.
func TestWindowsCoreStateMigrationCopiesInFlightTransition(t *testing.T) {
	root := t.TempDir()
	transition := `{"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","from":"local","to":"managed","phase":"config_written"}`
	writeLegacyCoreState(t, root, map[string]*string{
		"leshy-mode-transition.json": strPtr(transition),
		"control-route-auth.key":     strPtr(strings.Repeat("cd", 32) + "\n"),
	})

	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	got := string(readOrFail(t, filepath.Join(windowsCoreStateRoot(root), "leshy-mode-transition.json")))
	if got != transition {
		t.Fatalf("in-flight transition = %q, want %q", got, transition)
	}
}

// PC8C §3.1 (2026-11-17 post-X1 audit): the documented crash-resume contract
// — legacy source absent, destination present — must keep the destination.
func TestWindowsCoreStateMigrationKeepsDestinationWhenLegacySourceMissing(t *testing.T) {
	root := t.TempDir()
	generation := `{"generation":9}`
	// Only the destination exists: the legacy generation file was already
	// removed by a partial migration that reached the rename.
	newRoot := windowsCoreStateRoot(root)
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "leshy-mode-generation.json"), []byte(generation), 0o600); err != nil {
		t.Fatal(err)
	}
	// Another authority file stays legacy-only.
	desired := `{}`
	writeLegacyCoreState(t, root, map[string]*string{"desired.json": strPtr(desired)})

	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatalf("destination-only migration: %v", err)
	}
	if string(readOrFail(t, filepath.Join(newRoot, "leshy-mode-generation.json"))) != generation {
		t.Fatal("the destination bytes were disturbed by the migration")
	}
	if string(readOrFail(t, filepath.Join(newRoot, "desired.json"))) != desired {
		t.Fatal("the remaining legacy file was not migrated")
	}
	readOrFail(t, filepath.Join(newRoot, coreStateLayoutMarkerName))
}

// PC8C §3.1: the destination-only rerun is idempotent.
func TestWindowsCoreStateMigrationDestinationOnlyRerunIsIdempotent(t *testing.T) {
	root := t.TempDir()
	newRoot := windowsCoreStateRoot(root)
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "control-route-auth.key"), []byte("k1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsCoreState(root); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	readOrFail(t, filepath.Join(newRoot, coreStateLayoutMarkerName))
}

// PC8C §3.1: a legacy file that cannot be READ is fatal, not silent absence.
// The portable probe is a directory at the authority-file path: os.ReadFile
// on a directory errors on every platform.
func TestWindowsCoreStateMigrationLegacyReadErrorIsFatal(t *testing.T) {
	root := t.TempDir()
	writeLegacyCoreState(t, root, map[string]*string{"desired.json": strPtr(`{}`)})
	legacyRoot := legacyWindowsCoreStateRoot(root)
	if err := os.Remove(filepath.Join(legacyRoot, "desired.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(legacyRoot, "desired.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsCoreState(root); err == nil {
		t.Fatal("an unreadable legacy authority file must fail the migration")
	}
}

// PC8C §3.1: a destination file that cannot be READ is fatal, not absence.
func TestWindowsCoreStateMigrationDestinationReadErrorIsFatal(t *testing.T) {
	root := t.TempDir()
	generation := `{"generation":2}`
	writeLegacyCoreState(t, root, map[string]*string{"leshy-mode-generation.json": strPtr(generation)})
	newRoot := windowsCoreStateRoot(root)
	if err := os.MkdirAll(newRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(newRoot, "leshy-mode-generation.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsCoreState(root); err == nil {
		t.Fatal("an unreadable destination authority file must fail the migration")
	}
}
