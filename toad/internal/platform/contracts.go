package platform

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

type UnderlaySource interface {
	Watch(context.Context, chan<- netstate.Invalidation) error
	Snapshot(context.Context, map[string]bool) (netstate.Snapshot, error)
}
type RouteManager interface {
	ReconcileEndpointPolicy(context.Context, endpoint.Policy) error
	RemoveEndpointPolicy(context.Context, endpoint.Policy) error
	ApplyParking(context.Context, parking.DesiredState) error
	Snapshot(context.Context) (routing.KernelState, error)
}
type SleepEvent struct{ Preparing bool }
type SleepSource interface {
	Watch(context.Context, chan<- SleepEvent) error
}

type InterfaceRepairer interface {
	RepairInterface(context.Context, string, interfaceinfo.Expectation) error
}

type ManagedInterfaceOwnership struct {
	Present bool `json:"present"`
	Managed bool `json:"managed"`
}

type ManagedInterfaceVerifier interface {
	EnsureUnmanaged(context.Context, string) (ManagedInterfaceOwnership, error)
}
