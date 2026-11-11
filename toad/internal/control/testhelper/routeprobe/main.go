// routeprobe is the native test client for the managed selected-route RPC
// surface (08D1 D10R-2, Phase E Row G): a VM harness drives it instead of
// injecting capability arrays or hand-crafted frames, so the stale-activation
// and stale-generation negative rows are proven through the real control
// endpoint, the real framing and the real privileged auth path.
//
// Usage:
//
//	routeprobe --socket PATH [--auth-file PATH] ensure --zone Z --prefix P \
//	    --activation A --generation G [--source dns|static]
//	routeprobe --socket PATH [--auth-file PATH] forget --zone Z --prefix P \
//	    --activation A --generation G
//	routeprobe --socket PATH [--auth-file PATH] flush --zone Z
//	routeprobe --socket PATH [--auth-file PATH] set-list --list NAME --file PATH
//	routeprobe --socket PATH call --method NAME [--role ROLE]
//
// set-list reads the file and calls SetRoutingList through the control API
// (the same path the UI and acceptance gates use). call issues one arbitrary
// control method (the managed-success harness uses it for the tagged
// TestTriggerManagedRecovery against a failpoint-tagged core). Exit codes:
// 0 the response was OK; 3 the response carried an APIError; 2
// transport/usage failure.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/control"
)

func main() {
	fs := flag.NewFlagSet("routeprobe", flag.ContinueOnError)
	socket := fs.String("socket", control.DefaultAddress, "local control endpoint")
	authFile := fs.String("auth-file", "", "privileged credential file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: routeprobe [--socket PATH] [--auth-file PATH] <ensure|forget|flush> [flags]")
		os.Exit(2)
	}

	request := control.Request{
		Version: control.APIVersion,
		ID:      "routeprobe",
	}
	if *authFile != "" {
		raw, err := os.ReadFile(*authFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read the credential: %v\n", err)
			os.Exit(2)
		}
		request.PrivilegedAuth = strings.TrimSpace(string(raw))
	}

	sub := fs.Args()[0]
	rest := fs.Args()[1:]
	switch sub {
	case "ensure", "forget":
		subFS := flag.NewFlagSet(sub, flag.ContinueOnError)
		zone := subFS.String("zone", "", "managed zone")
		prefix := subFS.String("prefix", "", "canonical prefix")
		activation := subFS.String("activation", "", "activation id fence")
		generation := subFS.Uint64("generation", 0, "mode generation fence")
		source := subFS.String("source", string(control.LeshyRouteDNS), "dns|static")
		if err := subFS.Parse(rest); err != nil {
			os.Exit(2)
		}
		request.Method = "EnsureLeshyRoute"
		if sub == "forget" {
			request.Method = "ForgetLeshyRoute"
		}
		request.LeshyRoute = &control.LeshyRouteIntent{
			Zone:           *zone,
			Prefix:         *prefix,
			Source:         control.LeshyRouteSource(*source),
			ActivationID:   *activation,
			ModeGeneration: *generation,
		}
	case "flush":
		subFS := flag.NewFlagSet("flush", flag.ContinueOnError)
		zone := subFS.String("zone", "", "managed zone")
		activation := subFS.String("activation", "", "activation id fence (required once the schema2 marker is active)")
		generation := subFS.Uint64("generation", 0, "mode generation fence")
		if err := subFS.Parse(rest); err != nil {
			os.Exit(2)
		}
		request.Method = "FlushLeshyZone"
		request.LeshyRoute = &control.LeshyRouteIntent{
			Zone:           *zone,
			ActivationID:   *activation,
			ModeGeneration: *generation,
		}
	case "set-list":
		subFS := flag.NewFlagSet("set-list", flag.ContinueOnError)
		name := subFS.String("list", "", "routing list name (role id)")
		filePath := subFS.String("file", "", "path to the list content file")
		if err := subFS.Parse(rest); err != nil {
			os.Exit(2)
		}
		if *name == "" || *filePath == "" {
			fmt.Fprintln(os.Stderr, "set-list requires --list NAME and --file PATH")
			os.Exit(2)
		}
		content, err := os.ReadFile(*filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read list file: %v\n", err)
			os.Exit(2)
		}
		request.Method = "SetRoutingList"
		request.List = *name
		request.Content = string(content)
	case "call":
		subFS := flag.NewFlagSet("call", flag.ContinueOnError)
		method := subFS.String("method", "", "control API method name")
		role := subFS.String("role", "", "role argument (optional)")
		if err := subFS.Parse(rest); err != nil {
			os.Exit(2)
		}
		if *method == "" {
			fmt.Fprintln(os.Stderr, "call requires --method NAME")
			os.Exit(2)
		}
		request.Method = *method
		request.Role = *role
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		os.Exit(2)
	}

	response, err := control.Call(*socket, request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transport: %v\n", err)
		os.Exit(2)
	}
	out, _ := json.Marshal(response)
	fmt.Println(string(out))
	if !response.OK {
		if response.APIError != nil {
			fmt.Printf("apierror %s\n", response.APIError.Code)
			if response.APIError.Code == "stale_activation" || response.APIError.Code == "stale_mode_generation" {
				os.Exit(3)
			}
		}
		os.Exit(3)
	}
}
