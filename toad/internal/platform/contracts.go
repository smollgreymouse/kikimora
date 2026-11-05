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
	"net/netip"
)

type UnderlaySource interface {
	Watch(context.Context, chan<- netstate.Invalidation) error
	Snapshot(context.Context, map[string]bool) (netstate.Snapshot, error)
}

// The reconcile results live in the routing package (same import-cycle
// reasoning as the invalidation model) and surface here under the
// platform names.
type (
	SelectedReconcileResult     = routing.SelectedReconcileResult
	SelectedParkReconcileResult = routing.SelectedParkReconcileResult
)

// SelectedRouteInvalidationKind and SelectedRouteInvalidation live in the
// routing package so the platform writers can emit them without an import
// cycle; they surface through this contract under the platform name.
type (
	SelectedRouteInvalidationKind = routing.SelectedRouteInvalidationKind
	SelectedRouteInvalidation     = routing.SelectedRouteInvalidation
)

// The invalidation classes (08D1 §4).
const (
	SelectedInvalidatePrefix    = routing.SelectedInvalidatePrefix
	SelectedInvalidateInterface = routing.SelectedInvalidateInterface
	SelectedInvalidateFull      = routing.SelectedInvalidateFull
)

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
	// PreflightSelectedRoute reads only the exact prefix rows and refuses
	// any unauthorized same-prefix row before the Applying checkpoint
	// (08D1 §4.2).
	PreflightSelectedRoute(context.Context, netip.Prefix, routing.SelectedRouteIdentity, *routing.SelectedRoute) error
	ReconcileSelectedRoute(context.Context, routing.SelectedRouteDesired, *routing.SelectedRoute) (SelectedReconcileResult, error)
	RemoveSelectedRoute(context.Context, routing.SelectedRoute) error

	// Managed-park surface (08D1 §4): the deterministic unreachable/loopback
	// protection rows, transactional exactly like the selected routes.
	NormalizeManagedPark(context.Context, netip.Prefix) (routing.SelectedParkIdentity, error)
	PreflightManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity, *routing.SelectedPark) error
	ReconcileManagedPark(context.Context, string, netip.Prefix, *routing.SelectedPark) (SelectedParkReconcileResult, error)
	ObserveManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity) (bool, error)
	RemoveManagedPark(context.Context, string, netip.Prefix, routing.SelectedPark) error

	// SnapshotSelectedRoutes is the live full-kernel observation for the
	// safety audit and recovery (08D1 §4.3): exact selected rows, parks and
	// foreign same-prefix rows grouped by canonical prefix. It is never a
	// request-path helper.
	SnapshotSelectedRoutes(context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error)
	// SubscribeSelectedRouteInvalidations delivers native route events; a
	// platform without an observer returns
	// ErrSelectedRouteObserverUnsupported and the periodic audit covers it.
	SubscribeSelectedRouteInvalidations(context.Context) (<-chan SelectedRouteInvalidation, error)
}

// ErrSelectedRouteUnsupported marks a platform without a selected-route
// writer (08D1 §4): managed activation must be denied there.
var ErrSelectedRouteUnsupported = errors.New("selected-route writer unsupported")

// ErrSelectedRouteObserverUnsupported marks a platform without a native
// selected-route observer (08D1 §4): the periodic audit covers drift there.
var ErrSelectedRouteObserverUnsupported = errors.New("selected-route observer unsupported")

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
