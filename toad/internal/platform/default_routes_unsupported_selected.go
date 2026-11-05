//go:build !linux && !darwin && !windows

package platform

import (
	"context"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The managed selected-route/park surface on platforms without any route
// manager (08D1 §4): every method fails with the typed unsupported errors.
// The signatures use fully qualified routing types so this build-tagged file
// never carries compile debt.

func (unsupportedRouteManager) PreflightSelectedRoute(context.Context, netip.Prefix, routing.SelectedRouteIdentity, *routing.SelectedRoute) error {
	return ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) NormalizeManagedPark(context.Context, netip.Prefix) (routing.SelectedParkIdentity, error) {
	return routing.SelectedParkIdentity{}, ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) PreflightManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity, *routing.SelectedRoute) error {
	return ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) ReconcileManagedPark(context.Context, string, netip.Prefix, *routing.SelectedRoute) (routing.SelectedParkReconcileResult, error) {
	return routing.SelectedParkReconcileResult{}, ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) ObserveManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity) (bool, error) {
	return false, ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) RemoveManagedPark(context.Context, string, netip.Prefix, routing.SelectedPark) error {
	return ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) SnapshotSelectedRoutes(context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	return nil, ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) SubscribeSelectedRouteInvalidations(context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	return nil, ErrSelectedRouteObserverUnsupported
}
