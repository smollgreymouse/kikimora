//go:build linux

package awg2

import (
	"fmt"
	"os"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

func attachLinux(file *os.File, mtu int) (func() error, error) {
	if file == nil {
		return nil, fmt.Errorf("nil tun duplicate")
	}

	dev, err := awgtun.CreateTUNFromFile(file, mtu)
	if err != nil {
		return nil, err
	}

	return dev.Close, nil
}
