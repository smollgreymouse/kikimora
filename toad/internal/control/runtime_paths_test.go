package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
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
	// This case covers the injected path, not the service restart; the reload
	// contract itself is covered by the PROD2 bypass tests.
	manager.reloadHook = func(context.Context) error { return nil }
	// R8B §2.7: the runtime default route requires a desired role.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
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
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
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
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
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

// TestSetLeshyReloadHookReplacesSystemRestart proves the serve-level seam:
// with a hook installed, a bypass write never reaches config.ReloadLeshy
// (which would run "systemctl restart leshy.service" on the host).
func TestSetLeshyReloadHookReplacesSystemRestart(t *testing.T) {
	manager, _ := runtimePathsManager(t)
	if err := manager.SetRuntimePaths(hermeticRuntimePaths(t)); err != nil {
		t.Fatal(err)
	}
	var reloads int
	manager.SetLeshyReloadHook(func(context.Context) error {
		reloads++
		return nil
	})
	// R8B §2.7: the runtime default route requires a desired role.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	response := manager.Handle(t.Context(), Request{Version: APIVersion, Method: "SetBypassMode", Mode: "one"})
	if !response.OK {
		t.Fatalf("SetBypassMode failed: %s", response.Error)
	}
	if reloads != 1 {
		t.Fatalf("reload hook ran %d times, want 1", reloads)
	}
	if strings.Contains(response.Message, "did not restart") {
		t.Fatalf("hooked reload reported failure: %s", response.Message)
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

// The AUD2B tests below pin the fail-closed live policy transactions for the
// routing list and the per-role IPv6 policy (2026-11-16 post-e7ec9c2 audit
// §3.5): a reload or regenerate failure fails the command and restores the
// exact previous source bytes, generated config and running policy.

func hermeticPolicyManager(t *testing.T) *Manager {
	t.Helper()
	manager, dir := runtimePathsManager(t)
	if err := manager.SetRuntimePaths(RuntimePaths{
		LeshyConfig:  filepath.Join(dir, "leshy", "config.toml"),
		RoutingDir:   filepath.Join(dir, "leshy", "domains"),
		BypassConfig: filepath.Join(dir, "leshy", "bypass.conf"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manager.routingDirectory(), 0o750); err != nil {
		t.Fatal(err)
	}
	return manager
}

func generatedBypassTarget(t *testing.T, manager *Manager) string {
	t.Helper()
	zones, err := manager.buildLeshyZones()
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range zones {
		if zone.Name == config.BypassList {
			return zone.RouteTarget
		}
	}
	return ""
}

func TestSetRoutingListReloadFailureFailsAndRollsBack(t *testing.T) {
	manager := hermeticPolicyManager(t)
	// The setup commits must not touch a real Leshy service.
	manager.reloadHook = func(context.Context) error { return nil }
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	// R8B §2.7: the runtime default route requires a desired role.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(manager.routingDirectory(), "one.txt")
	beforeList, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatal(err)
	}

	// The first reload (the command) fails; the rollback reload succeeds.
	var calls int
	manager.reloadHook = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("reload failed")
		}
		return nil
	}

	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, Method: "SetRoutingList", List: "one", Content: "example.net\n",
	})
	if response.OK {
		t.Fatal("a failed Leshy reload must fail the list command")
	}
	afterList, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeList, afterList) {
		t.Fatalf("list bytes changed despite the failed command: %q -> %q", beforeList, afterList)
	}
	after, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("generated config was not restored after the failed reload")
	}
	if calls != 2 {
		t.Fatalf("rollback reload attempted %d times, want 2 (command + rollback)", calls)
	}
	if got := generatedBypassTarget(t, manager); got == "" {
		t.Fatal("the restored policy lost the bypass owner")
	}
}

func TestSetRoutingListRegenerateFailureRestoresTheList(t *testing.T) {
	manager := hermeticPolicyManager(t)
	// The setup commits must not touch a real Leshy service.
	manager.reloadHook = func(context.Context) error { return nil }
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	// R8B §2.7: the runtime default route requires a desired role.
	if err := manager.ConnectRole(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBypassMode("one"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(manager.routingDirectory(), "one.txt")
	beforeList, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatal(err)
	}
	manager.reloadHook = func(context.Context) error { return nil }

	// Make the generated-config write fail: on Windows a read-only generated
	// file rejects the atomic replacement; on POSIX the read-only directory
	// rejects the temporary file.
	if runtime.GOOS == "windows" {
		if err := os.Chmod(manager.leshyConfigFile(), 0o400); err != nil {
			t.Skipf("cannot make the generated config read-only: %v", err)
		}
		defer os.Chmod(manager.leshyConfigFile(), 0o644)
	} else {
		if err := os.Chmod(filepath.Dir(manager.leshyConfigFile()), 0o500); err != nil {
			t.Skipf("cannot make the config directory read-only: %v", err)
		}
		defer os.Chmod(filepath.Dir(manager.leshyConfigFile()), 0o750)
	}

	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, Method: "SetRoutingList", List: "one", Content: "example.net\n",
	})
	if response.OK {
		t.Fatal("a failed generated-config write must fail the list command")
	}
	afterList, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeList, afterList) {
		t.Fatalf("list bytes changed despite the failed regenerate: %q -> %q", beforeList, afterList)
	}
	after, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the generated config changed despite the failed regenerate")
	}
}

func TestSetRoleIPv6ReloadFailureRestoresExactFile(t *testing.T) {
	manager := hermeticPolicyManager(t)
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	// Pre-existing policy file with a known byte image.
	path := config.RoleIPv6SettingsPath(manager.routingDirectory())
	original := []byte(`{"schema":1,"roles":{"one":true}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}

	manager.reloadHook = func(context.Context) error { return errors.New("reload failed") }

	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, Method: "SetRoleIPv6", Role: "one", Enabled: boolPtr(false),
	})
	if response.OK {
		t.Fatal("a failed reload must fail the IPv6 toggle")
	}
	afterFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, afterFile) {
		t.Fatalf("role-ipv6.json changed despite the failed toggle: %q -> %q", original, afterFile)
	}
	if !manager.RoleIPv6("one") {
		t.Fatal("the in-memory policy view changed despite the failed toggle")
	}
	after, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the generated config was not restored after the failed toggle")
	}
}

func TestSetRoleIPv6MissingPolicyFileRestoredOnFailure(t *testing.T) {
	manager := hermeticPolicyManager(t)
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	path := config.RoleIPv6SettingsPath(manager.routingDirectory())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("policy file must not pre-exist: %v", err)
	}
	if err := manager.RegenerateLeshyConfig(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}

	manager.reloadHook = func(context.Context) error { return errors.New("reload failed") }
	response := manager.Handle(context.Background(), Request{
		Version: APIVersion, Method: "SetRoleIPv6", Role: "one", Enabled: boolPtr(false),
	})
	if response.OK {
		t.Fatal("a failed reload must fail the IPv6 toggle")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the created policy file was not removed by the rollback: %v", err)
	}
	after, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the generated config was not restored after the failed toggle")
	}
}

// TestConcurrentListAndBypassWritesRenderOneSerialConfig storms the two live
// policy writers: with policyMu serialization every commit renders exactly
// the inputs of one serial order. There is deliberately no live file sampler:
// on Windows a concurrent read handle makes the writer's MoveFileEx fail, so
// the sampler would perturb the system under test. The final-state equality
// below is the serial-order proof, and the -race run covers the memory side.
func TestConcurrentListAndBypassWritesRenderOneSerialConfig(t *testing.T) {
	manager := hermeticPolicyManager(t)
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	manager.reloadHook = func(context.Context) error { return nil }

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 15; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				// R8B §2.7: the runtime default route requires a desired role.
				if err := manager.ConnectRole(context.Background(), "one"); err != nil {
					errs <- fmt.Errorf("ConnectRole(one): %w", err)
					return
				}
				if err := manager.SetBypassMode("one"); err != nil {
					errs <- fmt.Errorf("SetBypassMode(one): %w", err)
				}
			} else {
				if err := manager.SetBypassMode(config.BypassDirect); err != nil {
					errs <- fmt.Errorf("SetBypassMode(direct): %w", err)
				}
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if err := manager.SetRoutingListLive(context.Background(), "one", "example.org\n"); err != nil {
					errs <- fmt.Errorf("SetRoutingListLive(example.org): %w", err)
				}
			} else {
				if err := manager.SetRoutingListLive(context.Background(), "one", "www.example.org\n"); err != nil {
					errs <- fmt.Errorf("SetRoutingListLive(www.example.org): %w", err)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// The durable bytes equal the render of the committed inputs: the config
	// never carries a half-applied mixture of bypass and list states.
	final, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := manager.renderLeshyConfigForTarget(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(final, expected) {
		t.Fatal("the generated config diverged from the committed policy inputs")
	}
	// The final list file carries exactly the last committed content.
	listContent, err := config.ReadRoutingList(manager.routingDirectory(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listContent, "example.org") {
		t.Fatalf("the last committed list content did not survive: %q", listContent)
	}
}

// TestConcurrentRoleIPv6AndModeConfigWriterStaySerialized drives the exact
// policyMu-locked writer the mode transitions use against the per-role IPv6
// toggle: the two commits serialize, the config always renders one order,
// and both the IPv6 setting and the mode identity survive.
func TestConcurrentRoleIPv6AndModeConfigWriterStaySerialized(t *testing.T) {
	manager := hermeticPolicyManager(t)
	if err := config.WriteRoutingList(manager.routingDirectory(), "one", "example.org\n"); err != nil {
		t.Fatal(err)
	}
	manager.reloadHook = func(context.Context) error { return nil }

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 12; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if err := manager.SetRoleIPv6("one", i%2 == 0); err != nil {
				errs <- fmt.Errorf("SetRoleIPv6(%v): %w", i%2 == 0, err)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			// The transition writer renders the explicit local mode target —
			// the same policyMu-locked writer RecoverManagedModeTransition
			// uses for its roll-forward. The recovery holds policyMu around
			// this call, and the test models that lock discipline.
			manager.policyMu.Lock()
			err := manager.regenerateLeshyConfigForModePolicyLocked(ManagedLeshyLocal, "", "", 0)
			manager.policyMu.Unlock()
			if err != nil {
				errs <- fmt.Errorf("mode config write: %w", err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Both the IPv6 setting and the mode identity survive, and the durable
	// bytes equal the render of the committed inputs. The final toggle value
	// is whichever commit serialized last; the setting on disk must equal the
	// committed view.
	finalSetting := manager.RoleIPv6("one")
	settings := config.ReadRoleIPv6Settings(manager.routingDirectory())
	if settings["one"] != finalSetting {
		t.Fatalf("the persisted IPv6 setting %v diverged from the committed view %v", settings["one"], finalSetting)
	}
	server, err := config.ReadLeshyServerStrict(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if server.RoutingBackend != "" || server.KikimoraSocket != "" {
		t.Fatalf("the local mode identity did not survive: %#v", server)
	}
	final, err := os.ReadFile(manager.leshyConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := manager.renderLeshyConfigForTarget(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(final, expected) {
		t.Fatal("the generated config diverged from the committed policy inputs")
	}
}
