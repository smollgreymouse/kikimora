package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// PC8A §1.4 (2026-11-17 post-X1 audit): a surviving profile-composition undo
// journal means the previous composition did not reach its commit point.
// Recovery must restore the exact ORIGINAL images and consume the journal;
// the rerun is idempotent.

func stageUndoFixture(t *testing.T) (stateDir, configDir string, original map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	stateDir = filepath.Join(root, "state")
	configDir = filepath.Join(root, "configs")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original = map[string][]byte{
		"alpha":   []byte("name = \"alpha\"\nprofiles = [\"default\"]\nstate_dir = " + filepath.ToSlash(filepath.Join(stateDir, "alpha")) + "\ninterface = \"kka0\"\nmtu = 1380\n\n[openconnect]\ngateway = \"198.51.100.1\"\nusername = \"t\"\npassword_file = \"pw\"\n"),
		"beta":    []byte("name = \"beta\"\nprofiles = [\"default\"]\nstate_dir = " + filepath.ToSlash(filepath.Join(stateDir, "beta")) + "\ninterface = \"kkb0\"\nmtu = 1380\n\n[openconnect]\ngateway = \"198.51.100.1\"\nusername = \"t\"\npassword_file = \"pw\"\n"),
		"desired": []byte(`{"schema":2,"active_profile":"default","roles":{"alpha":true,"beta":true}}`),
	}
	for name, data := range original {
		if name == "desired" {
			continue
		}
		if err := os.WriteFile(filepath.Join(configDir, name+".toml"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stateDir, "desired.json"), original["desired"], 0o600); err != nil {
		t.Fatal(err)
	}
	return stateDir, configDir, original
}

func stageUndoJournal(t *testing.T, stateDir, configDir string, original map[string][]byte) {
	t.Helper()
	undo := config.ProfileCompositionUndo{
		Schema: config.ProfileCompositionUndoSchema,
		Files: []config.ProfileUndoFile{
			{Kind: "role", Name: "alpha", Existed: true, Mode: 0o600, Data: original["alpha"]},
			{Kind: "role", Name: "beta", Existed: true, Mode: 0o600, Data: original["beta"]},
			{Kind: "registry", Existed: false, Mode: 0o600},
			{Kind: "desired", Existed: true, Mode: 0o600, Data: original["desired"]},
		},
	}
	if err := config.WriteProfileCompositionUndo(stateDir, undo); err != nil {
		t.Fatal(err)
	}
}

// The committed (half-applied) images a crash left behind: memberships moved
// to a new profile, a created registry, updated desired.json.
func stageHalfCommitted(t *testing.T, stateDir, configDir string) {
	t.Helper()
	committedAlpha := []byte("name = \"alpha\"\nprofiles = [\"default\",\"work\"]\nstate_dir = \"x\"\ninterface = \"kka0\"\nmtu = 1380\n\n[openconnect]\ngateway = \"198.51.100.1\"\nusername = \"t\"\npassword_file = \"pw\"\n")
	committedBeta := []byte("name = \"beta\"\nprofiles = [\"default\",\"work\"]\nstate_dir = \"y\"\ninterface = \"kkb0\"\nmtu = 1380\n\n[openconnect]\ngateway = \"198.51.100.1\"\nusername = \"t\"\npassword_file = \"pw\"\n")
	if err := os.WriteFile(filepath.Join(configDir, "alpha.toml"), committedAlpha, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "beta.toml"), committedBeta, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles.list"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "desired.json"), []byte(`{"schema":2,"active_profile":"work","roles":{"alpha":true,"beta":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileCompositionUndoRecoveryRestoresOriginalImages(t *testing.T) {
	stateDir, configDir, original := stageUndoFixture(t)
	stageUndoJournal(t, stateDir, configDir, original)
	stageHalfCommitted(t, stateDir, configDir)

	// The recovery is what runServe invokes after the state lock, before any
	// role TOML or registry is loaded.
	if err := config.RecoverProfileCompositionUndo(stateDir, configDir); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	for name, want := range original {
		if name == "desired" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(configDir, name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("role %s was not restored byte-exactly:\n%s", name, got)
		}
	}
	// The created registry is durably absent again.
	if _, err := os.Stat(filepath.Join(configDir, "profiles.list")); !os.IsNotExist(err) {
		t.Fatalf("the created registry must be removed by the recovery: %v", err)
	}
	// desired.json returned to its original image.
	got, err := os.ReadFile(filepath.Join(stateDir, "desired.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original["desired"]) {
		t.Fatalf("desired.json was not restored:\n%s", got)
	}
	// The journal is consumed after a proven rollback.
	if pending, err := config.ReadProfileCompositionUndo(stateDir); err != nil || pending != nil {
		t.Fatalf("the journal must be consumed: pending=%v err=%v", pending, err)
	}

	// Idempotent rerun.
	if err := config.RecoverProfileCompositionUndo(stateDir, configDir); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
}

func TestProfileCompositionUndoRecoveryWithoutJournalIsNoop(t *testing.T) {
	stateDir, configDir, original := stageUndoFixture(t)
	if err := config.RecoverProfileCompositionUndo(stateDir, configDir); err != nil {
		t.Fatalf("recovery without a journal: %v", err)
	}
	for name, want := range original {
		if name == "desired" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(configDir, name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("a no-journal recovery must not touch the configs: %s", name)
		}
	}
}
