// Command fake-toad is a network-free Toad replacement for end-to-end tests.
// It exercises the same config/state contract used by kikimora-toad.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/state"
)

func main() {
	if len(os.Args) != 4 || os.Args[1] != "run" || os.Args[2] != "-config" {
		fmt.Fprintln(os.Stderr, "usage: fake-toad run -config FILE")
		os.Exit(2)
	}
	cfg, err := config.Load(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writer := state.Writer{Dir: cfg.StateDir}
	if err := writer.Write(state.Snapshot{
		Name: cfg.Name, Protocol: string(cfg.Protocol), State: "online",
		Reason: "fake-toad online", RouteReady: true,
		Interface: state.InterfaceState{Name: cfg.Interface, IfIndex: 7, MTU: cfg.MTU},
		Session:   state.SessionState{Connected: true, Endpoint: endpoint(cfg.Protocol)},
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	<-signals
	_ = writer.Write(state.Snapshot{
		Name: cfg.Name, Protocol: string(cfg.Protocol), State: "stopped",
		Reason: "fake-toad stopped", Interface: state.InterfaceState{Name: cfg.Interface, MTU: cfg.MTU},
	})
}

func endpoint(protocol config.Protocol) string {
	if protocol == config.ProtocolOpenConnect {
		return "fake-openconnect.invalid:443"
	}
	return filepath.Base(string(protocol)) + ".invalid:443"
}
