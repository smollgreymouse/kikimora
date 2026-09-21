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
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/backend/awg2"
	openconnectbackend "github.com/smollgreymouse/kikimora/toad/internal/backend/openconnect"
	xraybackend "github.com/smollgreymouse/kikimora/toad/internal/backend/xray"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/profileimport"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
	"github.com/smollgreymouse/kikimora/toad/internal/toadruntime"
)

const (
	statePublishInterval            = 250 * time.Millisecond
	interfaceWaitTimeout            = 3 * time.Second
	openConnectInterfaceWaitTimeout = 30 * time.Second
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
	legacyVPNConfig := fs.String("legacy-vpn-config", "", "original shared vpn.conf")
	endpointProviderDir := fs.String("endpoint-provider-dir", "", "legacy endpoint-provider directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("-config is required")
	}
	cfg, err := config.LoadWithLegacy(*path, *legacyVPNConfig, *endpointProviderDir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var protocolBackend backend.Backend
	var ownedTunnel platform.Tunnel
	var interfaceInfo func() (toadruntime.Interface, error)

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
		interfaceInfo = func() (toadruntime.Interface, error) {
			return readManagedInterface(ownedTunnel.Name())
		}
	case config.ProtocolVLESSReality:
		protocolBackend = xraybackend.New(cfg)
		interfaceInfo = func() (toadruntime.Interface, error) {
			return readManagedInterface(cfg.Interface)
		}
	case config.ProtocolOpenConnect:
		protocolBackend = openconnectbackend.New(cfg)
		interfaceInfo = func() (toadruntime.Interface, error) {
			return readManagedInterface(cfg.Interface)
		}
	default:
		return fmt.Errorf("backend %q is not implemented", cfg.Protocol)
	}
	if ownedTunnel != nil {
		defer ownedTunnel.Close()
	}

	runtime := toadruntime.New(cfg, protocolBackend, ownedTunnel, interfaceInfo)
	defer runtime.Close()
	generation := uint64(time.Now().UnixNano())
	if err := runtime.Start(ctx, toadctl.StartRequest{Generation: generation}); err != nil {
		return err
	}
	go runtime.RunHealthLoop(ctx)
	return (toadctl.Server{Socket: filepath.Join(cfg.StateDir, "control.sock"), Handler: runtime}).Serve(ctx)
}

func readManagedInterface(name string) (toadruntime.Interface, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return toadruntime.Interface{}, err
	}
	rawAddrs, err := iface.Addrs()
	if err != nil {
		return toadruntime.Interface{}, fmt.Errorf("read addresses for %s: %w", name, err)
	}
	addresses := make([]string, 0, len(rawAddrs))
	for _, raw := range rawAddrs {
		prefix, err := netip.ParsePrefix(raw.String())
		if err != nil {
			continue
		}
		addresses = append(addresses, prefix.String())
	}
	sort.Strings(addresses)
	return toadruntime.Interface{Name: iface.Name, IfIndex: iface.Index, MTU: iface.MTU, Addresses: addresses}, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-toad <validate|import|run> [options]")
}
