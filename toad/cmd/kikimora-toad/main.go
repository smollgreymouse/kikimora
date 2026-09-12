package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/backend/awg2"
	xraybackend "github.com/smollgreymouse/kikimora/toad/internal/backend/xray"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/profileimport"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

const (
	statePublishInterval = 250 * time.Millisecond
	interfaceWaitTimeout = 3 * time.Second
)

type managedInterface struct {
	name    string
	ifIndex int
	mtu     int
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "validate":
		if err := validateCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kikimora-toad:", err)
			os.Exit(1)
		}
	case "import":
		if err := importCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kikimora-toad:", err)
			os.Exit(1)
		}
	case "run":
		if err := runCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kikimora-toad:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func validateCommand(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	path := fs.String("config", "", "path to per-instance TOML config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("-config is required")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	fmt.Printf("configuration OK: name=%s protocol=%s interface=%s\n", cfg.Name, cfg.Protocol, cfg.Interface)
	return nil
}

func importCommand(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	link := fs.String("link", "", "share link or profile; omit to read stdin")
	filePath := fs.String("file", "", "read share link/profile from file")
	name := fs.String("name", "", "override imported profile name")
	iface := fs.String("interface", "", "override managed interface name")
	address := fs.String("address", "", "override local TUN addresses, comma-separated CIDRs")
	mtu := fs.Int("mtu", 0, "override TUN MTU")
	stateDir := fs.String("state-dir", "", "override absolute state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *link != "" && *filePath != "" {
		return fmt.Errorf("use only one of -link or -file")
	}

	raw := *link
	if *filePath != "" {
		data, err := os.ReadFile(*filePath)
		if err != nil {
			return fmt.Errorf("read import file: %w", err)
		}
		raw = string(data)
	} else if raw == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read import from stdin: %w", err)
		}
		raw = string(data)
	}

	var addresses []string
	for _, item := range strings.Split(*address, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			addresses = append(addresses, item)
		}
	}
	cfg, err := profileimport.Parse(raw, profileimport.Options{
		Name:      *name,
		Interface: *iface,
		Address:   addresses,
		MTU:       *mtu,
		StateDir:  *stateDir,
	})
	if err != nil {
		return err
	}
	return config.Encode(os.Stdout, cfg)
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	path := fs.String("config", "", "path to per-instance TOML config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("-config is required")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var protocolBackend backend.Backend
	var ownedTunnel platform.Tunnel
	var interfaceInfo func() (managedInterface, error)

	switch cfg.Protocol {
	case config.ProtocolAWG2:
		addresses := make([]netip.Prefix, 0, len(cfg.Address))
		for _, raw := range cfg.Address {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return fmt.Errorf("parse validated tunnel address %q: %w", raw, err)
			}
			addresses = append(addresses, prefix)
		}
		ownedTunnel, err = platform.CreateTunnel(platform.TunnelSpec{
			Name:      cfg.Interface,
			MTU:       cfg.MTU,
			Addresses: addresses,
		})
		if err != nil {
			return fmt.Errorf("create Toad-owned tunnel: %w", err)
		}
		protocolBackend = awg2.New(cfg, ownedTunnel)
		interfaceInfo = func() (managedInterface, error) {
			return managedInterface{name: ownedTunnel.Name(), ifIndex: ownedTunnel.IfIndex(), mtu: ownedTunnel.MTU()}, nil
		}
	case config.ProtocolVLESSReality:
		protocolBackend = xraybackend.New(cfg)
		interfaceInfo = func() (managedInterface, error) {
			iface, err := net.InterfaceByName(cfg.Interface)
			if err != nil {
				return managedInterface{}, err
			}
			return managedInterface{name: iface.Name, ifIndex: iface.Index, mtu: iface.MTU}, nil
		}
	default:
		return fmt.Errorf("backend %q is not implemented", cfg.Protocol)
	}
	if ownedTunnel != nil {
		defer ownedTunnel.Close()
	}

	if err := protocolBackend.Start(ctx); err != nil {
		return err
	}
	defer protocolBackend.Close()

	if _, err := waitForManagedInterface(ctx, interfaceInfo, interfaceWaitTimeout); err != nil {
		return fmt.Errorf("managed interface %q did not become ready: %w", cfg.Interface, err)
	}

	writer := state.Writer{Dir: cfg.StateDir}
	publish := func() error {
		iface, err := interfaceInfo()
		if err != nil {
			return fmt.Errorf("read managed interface state: %w", err)
		}
		snapshot := snapshotFromHealth(cfg, iface, protocolBackend.Health(ctx))
		if err := writer.Write(snapshot); err != nil {
			return fmt.Errorf("publish Toad state: %w", err)
		}
		return nil
	}
	if err := publish(); err != nil {
		return err
	}

	ticker := time.NewTicker(statePublishInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := publish(); err != nil {
				return err
			}
		}
	}
}

func waitForManagedInterface(ctx context.Context, read func() (managedInterface, error), timeout time.Duration) (managedInterface, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if iface, err := read(); err == nil && iface.ifIndex > 0 {
			return iface, nil
		}
		select {
		case <-ctx.Done():
			return managedInterface{}, ctx.Err()
		case <-deadline.C:
			return managedInterface{}, fmt.Errorf("timeout after %s", timeout)
		case <-ticker.C:
		}
	}
}

func snapshotFromHealth(cfg *config.Config, iface managedInterface, health backend.Health) state.Snapshot {
	snapshot := state.New(cfg.Name, string(cfg.Protocol), iface.name, iface.mtu)
	snapshot.Generation = 1
	snapshot.State = health.State
	snapshot.Reason = health.Reason
	snapshot.RouteReady = iface.ifIndex > 0
	snapshot.Interface.IfIndex = iface.ifIndex
	snapshot.Session.Connected = health.Connected
	snapshot.Session.RXBytes = health.RXBytes
	snapshot.Session.TXBytes = health.TXBytes
	snapshot.Session.Endpoint = health.Endpoint
	if health.LastHandshakeAge != nil {
		ageMS := health.LastHandshakeAge.Milliseconds()
		snapshot.Session.LastHandshakeAgeMS = &ageMS
	}
	return snapshot
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-toad <validate|import|run> [options]")
}
