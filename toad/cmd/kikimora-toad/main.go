package main

import (
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"

	"github.com/smollgreymouse/kikimora/toad/internal/backend/awg2"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

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

	backend := awg2.New(cfg, tunnel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := backend.Start(ctx); err != nil {
		return err
	}
	defer backend.Close()

	<-ctx.Done()
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-toad <validate|run> -config <path>")
}
