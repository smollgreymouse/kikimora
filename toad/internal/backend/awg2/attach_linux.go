//go:build linux

package awg2

import (
	"fmt"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

func attachTunnel(tunnel platform.Tunnel) (awgtun.Device, error) {
	provider, ok := tunnel.(platform.FDProvider)
	if !ok {
		return nil, fmt.Errorf("platform tunnel %T does not provide a duplicate fd", tunnel)
	}

	duplicate, err := provider.DuplicateFile()
	if err != nil {
		return nil, fmt.Errorf("duplicate Toad TUN fd: %w", err)
	}
	closeDuplicate := true
	defer func() {
		if closeDuplicate {
			_ = duplicate.Close()
		}
	}()

	// DuplicateFile must set O_NONBLOCK before os.NewFile so Go marks the file
	// pollable, matching the official amneziawg-go supplied-fd lifecycle.
	awgTun, err := awgtun.CreateTUNFromFile(duplicate, tunnel.MTU())
	if err != nil {
		return nil, fmt.Errorf("attach official AWG TUN wrapper: %w", err)
	}
	closeDuplicate = false // awgTun now owns only the duplicate, never the Toad owner fd.
	return awgTun, nil
}
