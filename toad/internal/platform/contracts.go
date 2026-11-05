package platform

import (
	"context"
	"errors"
	"fmt"
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
	// Selected-route surface (08D §6, 08D1 §4): the single-writer path for
	// Leshy-selected destinations. Ownership comes from the explicit
	// registry, never from kernel discovery, and a foreign row on the same
	// prefix is never replaced or deleted.
	// NormalizeSelectedRoute computes the exact platform identity for the
	// desired route without any mutation (08D1 §4.1): the registry stores
	// what the kernel will read back.
	NormalizeSelectedRoute(context.Context, routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error)
	ReconcileSelectedRoute(context.Context, routing.SelectedRouteDesired, *routing.SelectedRoute) error
	RemoveSelectedRoute(context.Context, routing.SelectedRoute) error
}

// ErrSelectedRouteUnsupported marks a platform without a selected-route
// writer (08D1 §4): managed activation must be denied there.
var ErrSelectedRouteUnsupported = errors.New("selected-route writer unsupported")

type SleepEvent struct{ Preparing bool }
type SleepSource interface {
	Watch(context.Context, chan<- SleepEvent) error
}

type ManagedInterfaceIdentityError struct {
	Name            string
	ExpectedIfIndex int
	ActualIfIndex   int
}

func (e *ManagedInterfaceIdentityError) Error() string {
	return fmt.Sprintf("managed interface %q identity changed: ifindex=%d, expected=%d", e.Name, e.ActualIfIndex, e.ExpectedIfIndex)
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

type ManagedInterfaceWatcher interface {
	WatchManagedInterfaces(context.Context, chan<- struct{}) error
}
