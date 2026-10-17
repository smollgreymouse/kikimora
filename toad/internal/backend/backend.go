package backend

import (
	"context"
	"net/netip"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type Health struct {
	State            string
	Reason           string
	Connected        bool
	LastHandshakeAge *time.Duration
	RXBytes          uint64
	TXBytes          uint64
	Endpoint         string
}

// Backend is the protocol-core boundary used by the runtime.
// Implementations must not make routing-policy decisions. Ordinary transport
// recovery must happen without requiring the runtime to recreate the route-target TUN.
type Backend interface {
	Start(context.Context) error
	Health(context.Context) Health
	Close() error
}

type Validation struct {
	Healthy bool
	State   string
	Reason  string
}
type Validator interface {
	Validate(context.Context) Validation
}
type LocalInterfaceReporter interface {
	LocalInterfaceExpectation(context.Context) (interfaceinfo.Expectation, error)
}
type TransportEndpoint struct {
	Network    string
	Address    netip.AddrPort
	Hostname   string
	Port       uint16
	Active     bool
	ObservedAt time.Time
}
type EndpointReporter interface {
	TransportEndpoints(context.Context) ([]TransportEndpoint, error)
}
type Rebindable interface {
	Rebind(context.Context, toadctl.UnderlayBinding) error
}
type TransportRestarter interface {
	RestartTransport(context.Context, toadctl.UnderlayBinding) error
}
