package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

// generateLeshy writes the router configuration from the role lists. It keeps the interface of
// the legacy shell generator (domains directory, config path, routing.conf) so the installer and
// the CLI can call the core instead of a second implementation.
func generateLeshy(args []string) error {
	fs := flag.NewFlagSet("generate-leshy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	domainsDir := fs.String("domains", "", "directory with <role>.txt lists")
	configPath := fs.String("config", "", "path of the generated configuration")
	vpnDir := fs.String("vpn", "", "directory with <role>.dev files")
	bypassPath := fs.String("bypass", "", "path of bypass.conf")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	dir := *domainsDir
	if dir == "" && len(rest) > 0 {
		dir = rest[0]
	}
	if dir == "" {
		dir = config.DefaultRoutingDir()
	}

	path := *configPath
	if path == "" && len(rest) > 1 {
		path = rest[1]
	}
	if path == "" {
		path = config.DefaultLeshyConfigPath()
	}

	vpn := *vpnDir
	if vpn == "" {
		vpn = config.DefaultLeshyVPNDir()
	}

	bypass := *bypassPath
	if bypass == "" {
		bypass = config.DefaultBypassPath()
	}

	server := config.ReadLeshyServer(path)
	zones, err := config.BuildLeshyZones(dir, vpn, config.ReadBypassMode(bypass))
	if err != nil {
		return err
	}
	if err := config.WriteLeshyConfig(path, server, zones); err != nil {
		return err
	}
	fmt.Printf("wrote %s with %d zone(s)\n", path, len(zones))
	return nil
}
