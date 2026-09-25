//go:build !linux

package awg2

import (
	"errors"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

func attachTunnel(platform.Tunnel) (awgtun.Device, error) {
	return nil, errors.New("amneziawg2 attachment unsupported on this platform in Stage 0")
}
