// Package main implements the kikimora-core command line interface.
//
// The command surface mirrors the legacy Bash Kikimora VPN-lifecycle verbs
// (start/stop/restart/status/interfaces/profiles) and adds the per-role Toad
// commands. Leshy/DNS/domain/route/installer commands remain owned by the
// legacy Bash runtime and are intentionally not implemented here.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/control"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

type configPaths []string

func (p *configPaths) String() string         { return fmt.Sprint([]string(*p)) }
func (p *configPaths) Set(value string) error { *p = append(*p, value); return nil }

// configsFromDir returns the sorted absolute paths of every *.toml in dir.
// callers that require a present directory keep the returned error; the
// Windows service treats an unreadable directory as zero managed Toads.
func configsFromDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".toml" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

var coreVersion = "v0.1.0-dev"

func main() {
	if maybeRunService() {
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "service":
		err = serviceCommand(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "watch":
		err = watch(os.Args[2:])
	case "start", "connect-all":
		err = aggregateCommand("ConnectAll", os.Args[2:])
	case "stop", "disconnect-all":
		err = aggregateCommand("DisconnectAll", os.Args[2:])
	case "connect":
		err = roleCommand("ConnectRole", os.Args[2:])
	case "disconnect":
		err = roleCommand("DisconnectRole", os.Args[2:])
	case "retry":
		err = roleCommand("RetryRole", os.Args[2:])
	case "restart":
		err = restart(os.Args[2:])
	case "interfaces":
		err = interfaces(os.Args[2:])
	case "profiles":
		err = profiles(os.Args[2:])
	case "version":
		err = version(os.Args[2:])
	case "help":
		err = help(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kikimora-core:", err)
		os.Exit(1)
	}
}

// call performs one API request and converts a non-OK response into an error.
func call(socket, method, role, profile string) (control.Response, error) {
	response, err := control.Call(socket, control.Request{
		Version: control.APIVersion,
		Method:  method,
		Role:    role,
		Profile: profile,
	})
	if err != nil {
		return control.Response{}, err
	}
	if !response.OK {
		return control.Response{}, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}

// writeSnapshot prints the machine-readable snapshot for --json modes.
func writeSnapshot(snapshot *control.Snapshot) error {
	if snapshot == nil {
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(snapshot)
}

// serveOptions carries the serve configuration so both the console command
// and the Windows service hosting drive the same core startup path.
type serveOptions struct {
	socket              string
	toadBinary          string
	configDir           string
	ownershipConfig     string
	legacyVPNConfig     string
	endpointProviderDir string
	leshyPublicationDir string
	stateDir            string
	autoRecovery        bool
	configs             []string
}

func runServe(ctx context.Context, opts serveOptions) error {
	launcher := control.ExecLauncher{Binary: opts.toadBinary, LegacyVPNConfig: opts.legacyVPNConfig, ProviderDir: opts.endpointProviderDir}
	manager, err := control.NewManagerWithLegacy(opts.configs, launcher, opts.socket, opts.legacyVPNConfig, opts.endpointProviderDir)
	if err != nil {
		return err
	}
	defer manager.Close()

	var desiredStore control.DesiredStateStore
	if opts.stateDir != "" {
		desiredStore = control.FileDesiredStateStore{Path: filepath.Join(opts.stateDir, "desired.json")}
		manager.SetDesiredStateStore(desiredStore)
	}

	routeManager, executor := platform.DefaultRouteManager()
	manager.SetRecoveryDriver(control.NewRecoveryDriver(manager, control.RecoveryServices{
		Routes:   routeManager,
		Executor: executor,
		Resolver: endpoint.NetResolver{},
		Leshy:    leshy.FileBridge{Dir: opts.leshyPublicationDir},
	}))

	goOwnsLifecycle := false
	if opts.ownershipConfig != "" {
		ownership, err := config.LoadOwnership(opts.ownershipConfig)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// The atomic ownership file is the global cutover gate. An install
			// that has not created it yet keeps legacy ownership, so recovery
			// stays disabled until the operator stages the file.
		case err != nil:
			return err
		default:
			goOwnsLifecycle = ownership.RoutingOwner == "go" && ownership.TunnelOwner == "go"
			if opts.autoRecovery && !goOwnsLifecycle {
				return fmt.Errorf("automatic recovery requires routing_owner=go and tunnel_owner=go")
			}
			// The installed service intentionally omits a mutable feature flag.
			// Once the ownership file reaches go+go, that file is the single
			// global cutover gate and enables bounded recovery for all roles.
			if goOwnsLifecycle {
				opts.autoRecovery = true
			}
		}
	} else if opts.autoRecovery {
		return fmt.Errorf("--auto-recovery requires --ownership-config")
	}
	manager.SetAutomaticRecovery(opts.autoRecovery)

	if desiredStore != nil {
		if goOwnsLifecycle {
			desired, loadErr := desiredStore.Load(context.Background())
			switch {
			case loadErr == nil:
				if err := manager.RestoreDesiredState(context.Background(), desired); err != nil {
					return fmt.Errorf("restore desired state: %w", err)
				}
			case control.IsDesiredStateMissing(loadErr):
				manager.SetDesiredStateStatus("missing; all roles disabled")
			default:
				return fmt.Errorf("load desired state: %w", loadErr)
			}
		} else {
			manager.SetDesiredStateStatus("not restored: lifecycle ownership is not Go")
		}
	}
	return control.Serve(ctx, opts.socket, manager)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	toadBinary := fs.String("toad-binary", "kikimora-toad", "kikimora-toad executable")
	configDir := fs.String("config-dir", "", "directory containing per-Toad TOML configs")
	ownershipConfig := fs.String("ownership-config", "", "installation ownership TOML")
	legacyVPNConfig := fs.String("legacy-vpn-config", "", "original shared vpn.conf")
	endpointProviderDir := fs.String("endpoint-provider-dir", "/usr/local/libexec/kikimora/endpoint-providers", "legacy endpoint-provider directory")
	leshyPublicationDir := fs.String("leshy-publication-dir", "/run/kikimora/leshy/vpn", "directory for role publication state")
	stateDir := fs.String("state-dir", "", "persistent core state directory")
	autoRecovery := fs.Bool("auto-recovery", false, "enable controlled recovery after ownership cutover")
	var paths configPaths
	fs.Var(&paths, "config", "per-Toad TOML config; repeat for each managed Toad")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configDir != "" {
		configs, err := configsFromDir(*configDir)
		if err != nil {
			return fmt.Errorf("read config directory: %w", err)
		}
		paths = append(paths, configs...)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServe(ctx, serveOptions{
		socket:              *socket,
		toadBinary:          *toadBinary,
		configDir:           *configDir,
		ownershipConfig:     *ownershipConfig,
		legacyVPNConfig:     *legacyVPNConfig,
		endpointProviderDir: *endpointProviderDir,
		leshyPublicationDir: *leshyPublicationDir,
		stateDir:            *stateDir,
		autoRecovery:        *autoRecovery,
		configs:             paths,
	})
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	jsonFlag := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	response, err := call(*socket, "Snapshot", "", "")
	if err != nil {
		return err
	}
	if *jsonFlag {
		return writeSnapshot(response.Snapshot)
	}
	snapshot := response.Snapshot
	if snapshot == nil {
		return fmt.Errorf("core returned no snapshot")
	}
	fmt.Printf("core state: %s\n", snapshot.CoreState)
	fmt.Printf("aggregate state: %s\n", snapshot.AggregateState)
	fmt.Printf("roles:\n")
	for _, r := range snapshot.Roles {
		fmt.Printf("  %s: %s (%s) - %s\n", r.ID, r.State, r.Protocol, r.Reason)
	}
	return nil
}

func printHumanSnapshot(snapshot *control.Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("core returned no snapshot")
	}
	fmt.Printf("revision: %d\n", snapshot.Revision)
	fmt.Printf("core state: %s\n", snapshot.CoreState)
	fmt.Printf("aggregate state: %s\n", snapshot.AggregateState)
	fmt.Printf("roles:\n")
	for _, r := range snapshot.Roles {
		fmt.Printf("  %s: %s (%s) desired=%t route_ready=%t generation=%d pid=%d - %s\n",
			r.ID, r.State, r.Protocol, r.DesiredEnabled, r.RouteReady, r.Generation, r.PID, r.Reason)
	}
	return nil
}

// watch exposes the same revisioned Subscribe stream used by GUI clients.
// It reconnects after core/socket restarts so the console remains a useful
// long-lived observer across lifecycle acceptance events.
func watch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	jsonFlag := fs.Bool("json", false, "output one JSON snapshot per line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		err := control.Subscribe(ctx, *socket, control.Request{
			Version: control.APIVersion,
			ID:      "watch",
			Method:  "Subscribe",
		}, func(response control.Response) error {
			if response.Snapshot == nil {
				return fmt.Errorf("core returned no snapshot")
			}
			if *jsonFlag {
				return writeSnapshot(response.Snapshot)
			}
			return printHumanSnapshot(response.Snapshot)
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "kikimora-core: watch disconnected: %v; reconnecting\n", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// aggregateCommand maps the legacy start/stop verbs to ConnectAll/DisconnectAll.
func aggregateCommand(method string, args []string) error {
	fs := flag.NewFlagSet(method, flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	response, err := call(*socket, method, "", "")
	if err != nil {
		return err
	}
	return writeSnapshot(response.Snapshot)
}

// roleCommand maps connect/disconnect/retry to the per-role API.
func roleCommand(method string, args []string) error {
	fs := flag.NewFlagSet(method, flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	role := fs.String("role", "", "Toad name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *role == "" {
		return fmt.Errorf("-role is required")
	}
	response, err := call(*socket, method, *role, "")
	if err != nil {
		return err
	}
	return writeSnapshot(response.Snapshot)
}

func restart(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := call(*socket, "DisconnectAll", "", ""); err != nil {
		return err
	}
	response, err := call(*socket, "ConnectAll", "", "")
	if err != nil {
		return err
	}
	return writeSnapshot(response.Snapshot)
}

func interfaces(args []string) error {
	fs := flag.NewFlagSet("interfaces", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	jsonFlag := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	response, err := call(*socket, "Snapshot", "", "")
	if err != nil {
		return err
	}
	if *jsonFlag {
		return writeSnapshot(response.Snapshot)
	}
	snapshot := response.Snapshot
	if snapshot == nil {
		return fmt.Errorf("core returned no snapshot")
	}
	fmt.Printf("interfaces:\n")
	for _, r := range snapshot.Roles {
		iface := r.Interface
		if iface.Name == "" {
			fmt.Printf("  %s: <none>\n", r.ID)
			continue
		}
		fmt.Printf("  %s: %s (ifindex=%d, mtu=%d)\n", r.ID, iface.Name, iface.IfIndex, iface.MTU)
	}
	return nil
}

func profiles(args []string) error {
	if len(args) > 0 && args[0] == "use" {
		return profilesUse(args[1:])
	}
	fs := flag.NewFlagSet("profiles", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	jsonFlag := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	response, err := call(*socket, "Snapshot", "", "")
	if err != nil {
		return err
	}
	if *jsonFlag {
		return writeSnapshot(response.Snapshot)
	}
	snapshot := response.Snapshot
	if snapshot == nil {
		return fmt.Errorf("core returned no snapshot")
	}
	fmt.Printf("profiles: default\n")
	fmt.Printf("roles:\n")
	for _, r := range snapshot.Roles {
		fmt.Printf("  %s (%s)\n", r.ID, r.Protocol)
	}
	return nil
}

func profilesUse(args []string) error {
	fs := flag.NewFlagSet("profiles use", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("profile name is required")
	}
	response, err := call(*socket, "SetActiveProfile", "", fs.Arg(0))
	if err != nil {
		return err
	}
	return writeSnapshot(response.Snapshot)
}

func version(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Printf("kikimora-core version %s\n", coreVersion)
	fmt.Printf("backend: real\n")
	return nil
}

func help(args []string) error {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	usage()
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-core <command> [options]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  serve [--socket PATH] [--toad-binary PATH] [--ownership-config PATH] [--legacy-vpn-config PATH] [--endpoint-provider-dir PATH] [--leshy-publication-dir PATH] [--state-dir PATH] [--auto-recovery] --config FILE [--config FILE ...]")
	fmt.Fprintln(os.Stderr, "    Start the core daemon.")
	fmt.Fprintln(os.Stderr, "  service install|uninstall|start|stop")
	fmt.Fprintln(os.Stderr, "    Manage the Windows service hosting the core (windows only).")
	fmt.Fprintln(os.Stderr, "  status [--socket PATH] [--json]")
	fmt.Fprintln(os.Stderr, "    Show core status.")
	fmt.Fprintln(os.Stderr, "  watch [--socket PATH] [--json]")
	fmt.Fprintln(os.Stderr, "    Stream revisioned status and reconnect after core restarts.")
	fmt.Fprintln(os.Stderr, "  start|stop|restart [--socket PATH]")
	fmt.Fprintln(os.Stderr, "    Start all, stop all, or restart all Toads.")
	fmt.Fprintln(os.Stderr, "  connect|disconnect|retry --role NAME [--socket PATH]")
	fmt.Fprintln(os.Stderr, "    Connect, disconnect, or retry a specific Toad.")
	fmt.Fprintln(os.Stderr, "  interfaces [--socket PATH] [--json]")
	fmt.Fprintln(os.Stderr, "    Show interface information for all Toads.")
	fmt.Fprintln(os.Stderr, "  profiles [--socket PATH] [--json]")
	fmt.Fprintln(os.Stderr, "  profiles use NAME [--socket PATH]")
	fmt.Fprintln(os.Stderr, "    List configured roles or select the active profile.")
	fmt.Fprintln(os.Stderr, "  version")
	fmt.Fprintln(os.Stderr, "    Show version and backend kind.")
	fmt.Fprintln(os.Stderr, "  help")
	fmt.Fprintln(os.Stderr, "    Show this help.")
}
