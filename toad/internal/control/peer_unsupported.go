//go:build !linux && !darwin

package control

import "net"

func authorizePeer(net.Conn) error { return nil }
