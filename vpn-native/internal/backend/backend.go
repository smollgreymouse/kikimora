package backend

import (
	"context"
	"time"
)

type Health struct {
	State               string
	Reason              string
	Connected           bool
	LastHandshakeAge    *time.Duration
	RXBytes             uint64
	TXBytes             uint64
	Endpoint            string
}

// Backend is the protocol-core boundary used by the runtime.
// Implementations must not make routing-policy decisions. Ordinary transport
// recovery must happen without requiring the runtime to recreate the route-target TUN.
type Backend interface {
	Start(context.Context) error
	Health(context.Context) Health
	Close() error
}
