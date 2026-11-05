//go:build darwin

package platform

import (
	"context"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The managed selected-route/park surface is unsupported on darwin
// (08D1 §4): there is no writer, so managed activation must be denied, and
// there is no native observer, so the periodic audit contract would apply —
// but activation is refused before either matters.

func (m *darwinRouteManager) PreflightSelectedRoute(context.Context, netip.Prefix, routing.SelectedRouteIdentity, *routing.SelectedRoute) error {
	return ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) NormalizeManagedPark(context.Context, netip.Prefix) (routing.SelectedParkIdentity, error) {
	return routing.SelectedParkIdentity{}, ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) PreflightManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity, *routing.SelectedPark) error {
	return ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) ReconcileManagedPark(context.Context, string, netip.Prefix, *routing.SelectedPark) (routing.SelectedParkReconcileResult, error) {
	return routing.SelectedParkReconcileResult{}, ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) ObserveManagedPark(context.Context, netip.Prefix, routing.SelectedParkIdentity) (bool, error) {
	return false, ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) RemoveManagedPark(context.Context, string, netip.Prefix, routing.SelectedPark) error {
	return ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) SnapshotSelectedRoutes(context.Context) (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	return nil, ErrSelectedRouteUnsupported
}

func (m *darwinRouteManager) SubscribeSelectedRouteInvalidations(context.Context) (<-chan routing.SelectedRouteInvalidation, error) {
	return nil, ErrSelectedRouteObserverUnsupported
}
