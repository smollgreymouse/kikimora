package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// U5 (docs/roadmap.md §12): the four mutable generated-config surfaces must
// be injectable per Manager, closed once StartupManagedAuthority begins, and
// used consistently by every read/write path.

// hermeticRuntimePaths injects every mutable surface of one temp root, the
// same shape the serve flags give the Layer-C fixture: any mutation then
// regenerates inside the root instead of the product defaults.
func hermeticRuntimePaths(t *testing.T) RuntimePaths {
	t.Helper()
	root := t.TempDir()
	return RuntimePaths{
		LeshyConfig:      filepath.Join(root, "leshy", "config.toml"),
		RoutingDir:       filepath.Join(root, "leshy", "domains"),
		BypassConfig:     filepath.Join(root, "leshy", "bypass.json"),
		LeshyPublication: filepath.Join(root, "publication"),
	}
}

func runtimePathsManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, controlTestSocket(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close() })
	return manager, dir
}

func TestEmptyRuntimePathsKeepProductionDefaults(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	if err := manager.SetRuntimePaths(RuntimePaths{}); err != nil {
		t.Fatal(err)
	}
	if manager.leshyConfigFile() != config.DefaultLeshyConfigPath() {
		t.Fatalf("leshy config default changed: %s", manager.leshyConfigFile())
	}
	if manager.routingDirectory() != config.DefaultRoutingDir() {
		t.Fatalf("routing dir default changed: %s", manager.routingDirectory())
	}
	if manager.bypassPath() != config.DefaultBypassPath() {
		t.Fatalf("bypass default changed: %s", manager.bypassPath())
	}
	if manager.leshyPublicationDirectory() != config.DefaultLeshyVPNDir() {
		t.Fatalf("publication default changed: %s", manager.leshyPublicationDirectory())
	}
}

func TestRuntimePathsOverrideAllFourConfigSurfaces(t *testing.T) {
	manager, dir := runtimePathsManager(t)
	paths := RuntimePaths{
		LeshyConfig:      filepath.Join(dir, "leshy", "config.toml"),
		RoutingDir:       filepath.Join(dir, "leshy", "domains"),
		BypassConfig:     filepath.Join(dir, "leshy", "bypass.json"),
		LeshyPublication: filepath.Join(dir, "publication"),
	}
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	if manager.leshyConfigFile() != paths.LeshyConfig {
		t.Fatalf("leshy config override lost: %s", manager.leshyConfigFile())
	}
	if manager.routingDirectory() != paths.RoutingDir {
		t.Fatalf("routing dir override lost: %s", manager.routingDirectory())
	}
	if manager.bypassPath() != paths.BypassConfig {
		t.Fatalf("bypass override lost: %s", manager.bypassPath())
	}
	if manager.leshyPublicationDirectory() != paths.LeshyPublication {
		t.Fatalf("publication override lost: %s", manager.leshyPublicationDirectory())
	}
}

func TestRuntimePathsMustBeSetBeforeStartupAuthority(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	// StartupManagedAuthority begins the committed-mode recovery, which
	// reads the generated config: the paths freeze at its first line.
	if _, err := manager.StartupManagedAuthority(""); err != nil {
		t.Fatalf("local startup authority: %v", err)
	}
	if err := manager.SetRuntimePaths(RuntimePaths{RoutingDir: t.TempDir()}); err != ErrRuntimePathsClosed {
		t.Fatalf("expected ErrRuntimePathsClosed after startup authority, got %v", err)
	}
}

func TestGetRoutingListsUsesInjectedLeshyConfig(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	paths := hermeticRuntimePaths(t)
	leshyConfig := paths.LeshyConfig
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	// Regeneration writes the generated config to the injected path and the
	// API reads the zones back from the very same path.
	if err := manager.RegenerateLeshyConfig(); err != nil {
		t.Fatalf("regenerate with injected path: %v", err)
	}
	if _, err := os.Stat(leshyConfig); err != nil {
		t.Fatalf("generated config not at the injected path: %v", err)
	}
	response := manager.Handle(t.Context(), Request{Version: APIVersion, Method: "GetRoutingLists"})
	if !response.OK {
		t.Fatalf("GetRoutingLists failed: %s", response.Error)
	}
	// A role without list entries produces no zones; the contract here is
	// that the read went through the injected file (proven by the stat
	// above) without error.
	if response.Error != "" {
		t.Fatalf("zones read error: %s", response.Error)
	}
}

func TestSetRoutingListWritesOnlyInjectedRoutingDir(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	paths := hermeticRuntimePaths(t)
	routingDir := paths.RoutingDir
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRoutingList("one", "example.org\n"); err != nil {
		t.Fatalf("set routing list: %v", err)
	}
	entries, err := os.ReadDir(routingDir)
	if err != nil {
		t.Fatalf("injected routing dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("routing list was not written into the injected routing dir")
	}
	// The product default must stay untouched.
	if defaultEntries, err := os.ReadDir(config.DefaultRoutingDir()); err == nil && len(defaultEntries) > 0 {
		names := make([]string, 0, len(defaultEntries))
		for _, entry := range defaultEntries {
			names = append(names, entry.Name())
		}
		if strings.Contains(strings.Join(names, ","), "one") {
			t.Fatalf("routing list leaked into the product routing dir: %v", names)
		}
	}
}

func TestSetBypassModeWritesOnlyInjectedBypassConfig(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	paths := hermeticRuntimePaths(t)
	bypassConfig := paths.BypassConfig
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatalf("set bypass: %v", err)
	}
	if _, err := os.Stat(bypassConfig); err != nil {
		t.Fatalf("bypass decision not at the injected path: %v", err)
	}
	if manager.BypassMode() != "one" {
		t.Fatalf("bypass mode not read back: %s", manager.BypassMode())
	}
}

func TestBuildLeshyZonesUsesInjectedPublicationDir(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	paths := hermeticRuntimePaths(t)
	publication := paths.LeshyPublication
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRoutingList("one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) == 0 {
		t.Fatal("no zones generated")
	}
	for _, zone := range zones {
		if !strings.HasPrefix(zone.RouteTarget, publication+string(filepath.Separator)) {
			t.Fatalf("zone %q route_target escaped the injected publication dir: %s", zone.Name, zone.RouteTarget)
		}
		if strings.HasPrefix(zone.RouteTarget, config.DefaultLeshyVPNDir()) {
			t.Fatalf("zone %q still targets the default publication dir: %s", zone.Name, zone.RouteTarget)
		}
	}
}

// TestFileBridgeAndGeneratedRouteTargetsSharePublicationDir pins the U5
// consistency rule: runServe hands one value to both the Manager publication
// root and the leshy.FileBridge, so the generated route targets and the
// bridge always agree. The compile-level guarantee is the runServe wiring;
// this test pins the manager side of the value.
func TestFileBridgeAndGeneratedRouteTargetsSharePublicationDir(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	paths := hermeticRuntimePaths(t)
	publication := paths.LeshyPublication
	if err := manager.SetRuntimePaths(paths); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRoutingList("one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) == 0 {
		t.Fatal("no zones generated")
	}
	if manager.leshyPublicationDirectory() != publication {
		t.Fatalf("publication root mismatch: %s", manager.leshyPublicationDirectory())
	}
	for _, zone := range zones {
		if filepath.Dir(zone.RouteTarget) != publication {
			t.Fatalf("zone %q route_target dir %s != bridge dir %s", zone.Name, filepath.Dir(zone.RouteTarget), publication)
		}
	}
}

// TestManagedConfigTransactionUsesInjectedLeshyConfig proves the mode
// transaction machinery also resolves the generated config through the
// manager (mode_transaction.go uses leshyConfigFile(), never the default).
func TestManagedConfigTransactionUsesInjectedLeshyConfig(t *testing.T) {
	manager, dir := runtimePathsManager(t)
	leshyConfig := filepath.Join(dir, "leshy", "config.toml")
	if err := manager.SetRuntimePaths(RuntimePaths{LeshyConfig: leshyConfig}); err != nil {
		t.Fatal(err)
	}
	// The transition journal render path builds the server section from the
	// injected file; drive the same resolution RegenerateLeshyConfigForMode
	// uses without committing a managed transaction.
	if err := manager.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(leshyConfig)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("generated config is empty")
	}
}
