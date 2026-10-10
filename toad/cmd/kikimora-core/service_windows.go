//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/control"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsServiceName = "KikimoraCore"

// windowsProgramDataRoot is the crash-safe state root; tests redirect it to a
// temporary directory.
var windowsProgramDataRoot = `C:\ProgramData\Kikimora`

// serviceShutdownBudget bounds the graceful stop before the service reports
// Stopped regardless of pending Toad teardown.
const serviceShutdownBudget = 20 * time.Second

// defaultOwnershipConf is the installed cutover gate: legacy routing and
// endpoint ownership with an external tunnel keeps every VPN role fail-closed
// and recovery disabled until an operator stages go+go.
const defaultOwnershipConf = "routing_owner = \"legacy\"\ntunnel_owner = \"external\"\nendpoint_owner = \"legacy\"\n"

// ensureOwnershipFile writes the default ownership file when it is missing. It
// never overwrites an operator-staged cutover.
func ensureOwnershipFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(defaultOwnershipConf), 0o644)
}

// maybeRunService hosts the core under the Service Control Manager when the
// process was launched by the SCM; it returns true only in that case.
func maybeRunService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	if err := svc.Run(windowsServiceName, &kikimoraService{}); err != nil {
		fmt.Fprintf(os.Stderr, "kikimora-core service: %v\n", err)
		os.Exit(1)
	}
	return true
}

type kikimoraService struct {
	// opts overrides the ProgramData defaults; tests inject temporary paths.
	opts *serveOptions
}

// serviceOptions derives the installed-service defaults: crash-safe desired
// state under ProgramData, per-Toad TOMLs from the managed configs directory
// and the toad binary next to the core executable.
func serviceOptions() serveOptions {
	exePath, err := os.Executable()
	exeDir := "."
	if err == nil {
		exeDir = filepath.Dir(exePath)
	}
	opts := serveOptions{
		socket:              control.DefaultAddress,
		toadBinary:          filepath.Join(exeDir, "kikimora-toad.exe"),
		configDir:           filepath.Join(windowsProgramDataRoot, "toads"),
		ownershipConfig:     filepath.Join(windowsProgramDataRoot, "ownership.conf"),
		endpointProviderDir: filepath.Join(windowsProgramDataRoot, "endpoint-providers"),
		leshyPublicationDir: filepath.Join(windowsProgramDataRoot, "leshy", "vpn"),
		stateDir:            filepath.Join(windowsProgramDataRoot, "state"),
	}
	configs, err := configsFromDir(opts.configDir)
	if err != nil {
		// An absent configs directory means zero managed Toads; the service
		// still starts and serves snapshots with fail-closed empty roles.
		opts.configDir = ""
	} else {
		opts.configs = configs
	}
	return opts
}

func (s *kikimoraService) options() serveOptions {
	if s.opts != nil {
		return *s.opts
	}
	return serviceOptions()
}

// Execute implements svc.Handler: it runs the same core startup path as the
// console serve command, redirects output to a documented file sink and
// honors bounded stop requests.
func (s *kikimoraService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	// The MSI registers the service declaratively; the staged restart recovery
	// policy is applied by the service itself on its first SCM start.
	ensureServiceRecovery()

	logFile, err := setupServiceLogging()
	if err != nil {
		return true, 1
	}
	defer logFile.Close()

	opts := s.options()
	stopReq := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stopReq
		cancel()
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- runServe(ctx, opts) }()
	status <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		change := <-req
		switch change.Cmd {
		case svc.Interrogate:
			status <- change.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			close(stopReq)
			select {
			case <-serveErr:
			case <-time.After(serviceShutdownBudget):
			}
			return false, 0
		default:
			// Unrecognized control request; keep serving.
		}
	}
}

// ensureServiceRecovery applies the staged restart policy (5s/30s/60s with a
// one-hour failure counter reset) to the registered service. Best-effort: the
// SCM-hosted service runs as LocalSystem and may re-run this on every start.
func ensureServiceRecovery() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	service, err := m.OpenService(windowsServiceName)
	if err != nil {
		return
	}
	defer service.Close()
	_ = service.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 3600)
}

// setupServiceLogging redirects the process sink to a documented file so the
// SCM-hosted core keeps structured logs without a console.
func setupServiceLogging() (*os.File, error) {
	logDir := filepath.Join(windowsProgramDataRoot, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create service log directory: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "kikimora-core.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open service log file: %w", err)
	}
	os.Stdout = logFile
	os.Stderr = logFile
	return logFile, nil
}

// serviceCommand implements the install/uninstall/start/stop management verbs.
// Installation pre-creates the ProgramData layout, registers automatic start
// with staged restart recovery and starts the service immediately; fresh
// installs keep every VPN role disabled (no desired state is written).
func serviceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("service subcommand required: install|uninstall|start|stop")
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	switch args[0] {
	case "install":
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		for _, dir := range []string{
			windowsProgramDataRoot,
			filepath.Join(windowsProgramDataRoot, "state"),
			filepath.Join(windowsProgramDataRoot, "toads"),
			filepath.Join(windowsProgramDataRoot, "logs"),
			filepath.Join(windowsProgramDataRoot, "leshy", "vpn"),
		} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
		}
		if err := ensureOwnershipFile(filepath.Join(windowsProgramDataRoot, "ownership.conf")); err != nil {
			return fmt.Errorf("write ownership config: %w", err)
		}
		service, err := m.CreateService(windowsServiceName, exePath, mgr.Config{
			DisplayName: "Kikimora Core",
			Description: "Kikimora VPN control plane service (supervises Toad processes and the local control IPC)",
			StartType:   mgr.StartAutomatic,
		}, "serve")
		if err != nil {
			return fmt.Errorf("create service: %w", err)
		}
		defer service.Close()
		if err := service.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
		}, 3600); err != nil {
			return fmt.Errorf("set recovery actions: %w", err)
		}
		if err := service.Start(); err != nil {
			return fmt.Errorf("start service: %w", err)
		}
		return nil
	case "uninstall", "remove":
		service, err := m.OpenService(windowsServiceName)
		if err != nil {
			return fmt.Errorf("open service: %w", err)
		}
		defer service.Close()
		if err := service.Delete(); err != nil {
			return fmt.Errorf("delete service: %w", err)
		}
		return nil
	case "start":
		service, err := m.OpenService(windowsServiceName)
		if err != nil {
			return fmt.Errorf("open service: %w", err)
		}
		defer service.Close()
		return service.Start()
	case "stop":
		service, err := m.OpenService(windowsServiceName)
		if err != nil {
			return fmt.Errorf("open service: %w", err)
		}
		defer service.Close()
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("query service: %w", err)
		}
		if status.State == svc.Stopped {
			return nil
		}
		_, err = service.Control(svc.Stop)
		return err
	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
}
