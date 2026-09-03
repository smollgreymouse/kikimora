//go:build linux

package awg2

import (
	"fmt"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"golang.org/x/sys/unix"
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

	// The official Linux TUN implementation requires a nonblocking fd before
	// CreateTUNFromFile hands it to Go's netpoll machinery.
	if err := unix.SetNonblock(int(duplicate.Fd()), true); err != nil {
		return nil, fmt.Errorf("set duplicate TUN fd nonblocking: %w", err)
	}

	awgTun, err := awgtun.CreateTUNFromFile(duplicate, tunnel.MTU())
	if err != nil {
		return nil, fmt.Errorf("attach official AWG TUN wrapper: %w", err)
	}
	closeDuplicate = false // awgTun now owns only the duplicate, never the Toad owner fd.
	return awgTun, nil
}
