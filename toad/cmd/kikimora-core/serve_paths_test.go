package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// U5 (docs/roadmap.md §12): the development serve command must reach the
// Manager with its runtime-path overrides before StartupManagedAuthority, and
// a fully overridden hermetic serve must create nothing outside the temp root.

func TestResolvedDiagnosticsHostSources(t *testing.T) {
	if !resolvedDiagnosticsHostSources(nil) {
		t.Fatal("nil (packaged service) must resolve to true")
	}
	tr := true
	fa := false
	if !resolvedDiagnosticsHostSources(&tr) {
		t.Fatal("explicit true must resolve to true")
	}
	if resolvedDiagnosticsHostSources(&fa) {
		t.Fatal("explicit false must resolve to false")
	}
}

func hermeticServeConfig(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "configs"), 0o750); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(root, "oc.password")
	if err := os.WriteFile(password, []byte("lab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`name = "one"
protocol = "openconnect"
interface = "kkone"
mtu = 1380
state_dir = %q
address = ["10.0.0.1/24"]

[openconnect]
gateway = "vpn.example.test"
username = "tester"
vpn_protocol = "anyconnect"
auth_group = ""
password_file = %q
token_mode = "none"
token_secret_file = ""
user_agent = ""
server_cert = ""
disable_udp = false
disable_ipv6 = false
`, root, password)
	path := filepath.Join(root, "configs", "one.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// hermeticServeOptions builds the exact serve option set the Layer-C desktop
// fixture passes: every mutable path under one temp root.
func hermeticServeOptions(t *testing.T) (serveOptions, string) {
	t.Helper()
	root := t.TempDir()
	hermeticServeConfig(t, root)
	for _, dir := range []string{"providers", "publication"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	fa := false
	opts := serveOptions{
		socket:                 filepath.Join(root, "core.sock"),
		toadBinary:             filepath.Join(root, "no-toad-here"),
		configDir:              filepath.Join(root, "configs"),
		endpointProviderDir:    filepath.Join(root, "providers"),
		leshyPublicationDir:    filepath.Join(root, "publication"),
		leshyConfig:            filepath.Join(root, "leshy", "config.toml"),
		routingDir:             filepath.Join(root, "leshy", "domains"),
		bypassConfig:           filepath.Join(root, "leshy", "bypass.json"),
		diagnosticsHostSources: &fa,
	}
	return opts, root
}

// productRoots are the paths a hermetic test run must never touch.
var productRoots = []string{
	"/etc/kikimora",
	"/var/lib/kikimora",
	"/run/kikimora",
}

func snapshotProductRoots() map[string]string {
	snapshot := make(map[string]string)
	for _, root := range productRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			snapshot[root] = "<missing>"
			continue
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		snapshot[root] = fmt.Sprintf("%v", names)
	}
	return snapshot
}

func servePathsTest(t *testing.T, check func(t *testing.T, opts serveOptions, root string)) {
	t.Helper()
	opts, root := hermeticServeOptions(t)
	t.Setenv("KIKIMORA_RESTART_MARKER", filepath.Join(root, "core-restart.pending"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, opts) }()

	// Readiness: the serve path regenerates the generated Leshy config under
	// the injected root during startup, before Serve binds. When the file is
	// there, every override reached the Manager ahead of startup authority.
	leshyConfig := opts.leshyConfig
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(leshyConfig); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("hermetic serve never produced %s; last error: %v", leshyConfig, func() error {
				select {
				case err := <-done:
					return err
				default:
					return nil
				}
			}())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if check != nil {
		check(t, opts, root)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("hermetic serve exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hermetic serve did not stop after context cancel")
	}
}

func TestServeFlagsReachManagerBeforeStartupAuthority(t *testing.T) {
	// The generated config must land under the injected --leshy-config path,
	// and every other override stays inside the same temp root: that proves
	// the flags reached SetRuntimePaths before StartupManagedAuthority
	// regenerated anything. The routing/bypass files materialize lazily on
	// their first mutation, so only their location is pinned here.
	servePathsTest(t, func(t *testing.T, opts serveOptions, root string) {
		if _, err := os.Stat(opts.leshyConfig); err != nil {
			t.Fatalf("serve did not materialize %s: %v", opts.leshyConfig, err)
		}
		for _, path := range []string{opts.routingDir, opts.bypassConfig, opts.leshyPublicationDir} {
			if !strings.HasPrefix(path, root+string(filepath.Separator)) {
				t.Fatalf("override %s is not inside the temp root %s", path, root)
			}
		}
	})
}

func TestHermeticServeCreatesNothingOutsideTempRoot(t *testing.T) {
	before := snapshotProductRoots()
	servePathsTest(t, nil)
	after := snapshotProductRoots()
	for _, root := range productRoots {
		if before[root] != after[root] {
			t.Fatalf("hermetic serve changed %s: %q -> %q", root, before[root], after[root])
		}
	}
}
