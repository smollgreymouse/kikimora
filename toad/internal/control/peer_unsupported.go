//go:build !linux && !darwin && !windows

package control

import "net"

func authorizePeer(net.Conn) error { return nil }
