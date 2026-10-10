//go:build windows

package awg2

import (
	"fmt"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"golang.org/x/sys/windows"
)

// guidProvider is the windows-only contract the Toad-owned tunnel implements
// so the protocol core can open its data session on the very same persistent
// adapter (one ring session per adapter, owned by the protocol core).
type guidProvider interface {
	TunnelGUID() windows.GUID
}

// openAWGTunnel is a seam for deterministic tests of the attachment contract.
var openAWGTunnel = func(name string, guid *windows.GUID, mtu int) (awgtun.Device, error) {
	return awgtun.CreateTUNWithRequestedGUID(name, guid, mtu)
}

// attachTunnel opens the official AWG TUN wrapper on the Toad-owned adapter.
// The tunnel holds the adapter without a ring session; amneziawg-go reuses
// the same deterministic-GUID adapter and owns its data session until the
// backend closes. On the next recovery the adapter is reused again — no
// duplicate adapters can accumulate.
func attachTunnel(tunnel platform.Tunnel) (awgtun.Device, error) {
	provider, ok := tunnel.(guidProvider)
	if !ok {
		return nil, fmt.Errorf("platform tunnel %T does not expose its deterministic GUID", tunnel)
	}
	guid := provider.TunnelGUID()
	awgTun, err := openAWGTunnel(tunnel.Name(), &guid, tunnel.MTU())
	if err != nil {
		return nil, fmt.Errorf("attach official AWG TUN wrapper on %q: %w", tunnel.Name(), err)
	}
	return awgTun, nil
}
