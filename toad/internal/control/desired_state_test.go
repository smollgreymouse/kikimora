package control

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileDesiredStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core", "desired.json")
	store := FileDesiredStateStore{Path: path}
	want := PersistedDesiredState{
		Schema:        DesiredStateSchema,
		ActiveProfile: "default",
		Roles:         map[string]bool{"primary": true, "secondary": false},
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != DesiredStateSchema || got.ActiveProfile != "default" ||
		!got.Roles["primary"] || got.Roles["secondary"] {
		t.Fatalf("round trip mismatch: %#v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if gotMode := info.Mode().Perm(); gotMode != 0o600 {
			t.Fatalf("desired-state mode=%#o want 0600", gotMode)
		}
	}
}

func TestFileDesiredStateStoreRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileDesiredStateStore{Path: path}).Load(context.Background()); err == nil {
		t.Fatal("corrupt desired state was accepted")
	}
}

func TestDesiredStateSurvivesProductShutdownAndRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "one", "openconnect")
	store := FileDesiredStateStore{Path: filepath.Join(dir, "core", "desired.json")}

	firstLauncher := &fakeLauncher{}
	first, err := NewManager([]string{configPath}, firstLauncher, "")
	if err != nil {
		t.Fatal(err)
	}
	first.SetDesiredStateStore(store)
	if err := first.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := first.ShutdownProduct(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Roles["one"] {
		t.Fatalf("product shutdown cleared desired intent: %#v", persisted)
	}

	secondLauncher := &fakeLauncher{}
	second, err := NewManager([]string{configPath}, secondLauncher, "")
	if err != nil {
		t.Fatal(err)
	}
	second.SetDesiredStateStore(store)
	if err := second.RestoreDesiredState(context.Background(), persisted); err != nil {
		t.Fatal(err)
	}
	if len(secondLauncher.started) != 1 {
		t.Fatalf("restart launched %d roles, want 1", len(secondLauncher.started))
	}
	if snap := second.Snapshot(); len(snap.Roles) != 1 || !snap.Roles[0].DesiredEnabled {
		t.Fatalf("restored desired role missing: %#v", snap)
	}
	if err := second.DisconnectRole("one"); err != nil {
		t.Fatal(err)
	}
	if err := second.ShutdownProduct(context.Background()); err != nil {
		t.Fatal(err)
	}

	disabled, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Roles["one"] {
		t.Fatalf("explicit disconnect was not persisted: %#v", disabled)
	}

	thirdLauncher := &fakeLauncher{}
	third, err := NewManager([]string{configPath}, thirdLauncher, "")
	if err != nil {
		t.Fatal(err)
	}
	third.SetDesiredStateStore(store)
	if err := third.RestoreDesiredState(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	defer third.ShutdownProduct(context.Background())
	if len(thirdLauncher.started) != 0 {
		t.Fatalf("disabled persisted role relaunched: %v", thirdLauncher.started)
	}
}

type failingDesiredStore struct{}

func (failingDesiredStore) Load(context.Context) (PersistedDesiredState, error) {
	return PersistedDesiredState{}, os.ErrNotExist
}
func (failingDesiredStore) Save(context.Context, PersistedDesiredState) error {
	return os.ErrPermission
}

func TestDesiredPersistenceFailureRollsBackIntent(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.ShutdownProduct(context.Background())
	manager.SetDesiredStateStore(failingDesiredStore{})
	if err := manager.ConnectRole(context.Background(), "one"); err == nil {
		t.Fatal("connect succeeded despite desired-state persistence failure")
	}
	snap := manager.Snapshot()
	if len(snap.Roles) != 1 || snap.Roles[0].DesiredEnabled {
		t.Fatalf("failed persistence changed desired state: %#v", snap)
	}
}
