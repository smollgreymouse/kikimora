package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/smollgreymouse/kikimora/linux/vpn-native/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "validate":
		if err := validateCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kikimora-vpn:", err)
			os.Exit(1)
		}
	case "run":
		if err := runCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kikimora-vpn:", err)
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
	return fmt.Errorf("backend %q for instance %q is not wired yet; execute the next implementation packet", cfg.Protocol, cfg.Name)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-vpn <validate|run> -config <path>")
}
