package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 08D1 §31.17/§40-R3: the startup must not regenerate the Leshy config
// before the committed mode is restored. The manager constructor restores
// the committed mode (strict marker load) before NewManagerWithConfigDir
// returns, so a RegenerateLeshyConfig call at that point already observes
// the committed socket — the spy proves the ordering contract: no
// regeneration happens inside the constructor.
func TestConstructorDoesNotRegenerateBeforeModeRestore(t *testing.T) {
	dir := shortSocketDir(t)
	configPath := filepath.Join(dir, "leshy", "config.toml")

	// Stage a marker with a distinctive socket: the committed mode.
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	manager.leshyConfigPath = configPath
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")
	enableManagedForTest(t, manager, dir, "/run/kk-mode-restore.sock")
	markerPath := filepath.Join(dir, "leshy-managed.json")
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("the marker must be staged: %v", err)
	}

	// A fresh manager over the same directory restores the committed mode.
	restarted, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	restarted.monitorCancel()
	restarted.SetStateDir(dir)
	defer restarted.Close()
	restarted.leshyConfigPath = configPath
	restarted.routingDir = filepath.Join(dir, "leshy", "domains")
	restarted.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")

	if !restarted.ManagedModeActive() {
		t.Fatal("the committed managed mode must be restored by the constructor")
	}
	if restarted.managedSocketForGeneration() != "/run/kk-mode-restore.sock" {
		t.Fatalf("the committed socket must be active before any generation: %q", restarted.managedSocketForGeneration())
	}

	// The regeneration now reflects the committed mode: the generated
	// config selects the managed backend with the committed socket.
	if err := restarted.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `routing_backend = "kikimora"`) ||
		!strings.Contains(string(content), `kikimora_socket = "/run/kk-mode-restore.sock"`) {
		t.Fatalf("the regenerated config must reflect the committed mode:\n%s", content)
	}
}

// RegenerateLeshyConfigForMode is the §31.17 mode-explicit helper: the
// transition recovery (Stage 6) drives it with the journal target.
func TestRegenerateLeshyConfigForMode(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()
	manager.SetStateDir(dir)
	configPath := filepath.Join(dir, "leshy", "config.toml")
	manager.leshyConfigPath = configPath
	manager.routingDir = filepath.Join(dir, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(dir, "leshy", "bypass.json")

	// Managed target.
	if err := manager.RegenerateLeshyConfigForMode(ManagedLeshyManaged, "/run/kk-for-mode.sock",
		"0123456789abcdef0123456789abcdef", 3); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `kikimora_socket = "/run/kk-for-mode.sock"`) {
		t.Fatalf("the managed mode must reach the generated config:\n%s", content)
	}
	if manager.managedSocketForGeneration() != "" {
		t.Fatal("the explicit-mode generation must restore the previous in-memory mode")
	}

	// Local target strips the managed keys.
	if err := manager.RegenerateLeshyConfigForMode(ManagedLeshyLocal, "", "", 0); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "routing_backend") {
		t.Fatalf("the local mode must strip the managed keys:\n%s", content)
	}

	// Unknown mode is refused.
	if err := manager.RegenerateLeshyConfigForMode("bogus", "", "", 0); err == nil {
		t.Fatal("an unknown committed mode must be refused")
	}
}
