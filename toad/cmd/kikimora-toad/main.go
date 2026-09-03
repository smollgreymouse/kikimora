package main

import (
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/backend/awg2"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

const statePublishInterval = 250 * time.Millisecond

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
	if cfg.Protocol != config.ProtocolAWG2 {
		return fmt.Errorf("backend %q is not implemented in the current Stage 0 packet", cfg.Protocol)
	}

	addresses := make([]netip.Prefix, 0, len(cfg.Address))
	for _, raw := range cfg.Address {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return fmt.Errorf("parse validated tunnel address %q: %w", raw, err)
		}
		addresses = append(addresses, prefix)
	}

	tunnel, err := platform.CreateTunnel(platform.TunnelSpec{
		Name:      cfg.Interface,
		MTU:       cfg.MTU,
		Addresses: addresses,
	})
	if err != nil {
		return fmt.Errorf("create Toad-owned tunnel: %w", err)
	}
	defer tunnel.Close()

	protocolBackend := awg2.New(cfg, tunnel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := protocolBackend.Start(ctx); err != nil {
		return err
	}
	defer protocolBackend.Close()

	writer := state.Writer{Dir: cfg.StateDir}
	publish := func() error {
		snapshot := snapshotFromHealth(cfg, tunnel, protocolBackend.Health(ctx))
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

func snapshotFromHealth(cfg *config.Config, tunnel platform.Tunnel, health backend.Health) state.Snapshot {
	snapshot := state.New(cfg.Name, string(cfg.Protocol), tunnel.Name(), tunnel.MTU())
	snapshot.Generation = 1
	snapshot.State = health.State
	snapshot.Reason = health.Reason
	snapshot.RouteReady = true
	snapshot.Interface.IfIndex = tunnel.IfIndex()
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
	fmt.Fprintln(os.Stderr, "usage: kikimora-toad <validate|run> -config <path>")
}
