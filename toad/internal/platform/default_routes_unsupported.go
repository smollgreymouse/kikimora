//go:build !linux && !darwin && !windows

package platform

import (
	"context"
	"errors"

	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

var ErrRoutingUnsupported = errors.New("canonical route ownership is unsupported on this platform")

type unsupportedRouteManager struct{}

func (unsupportedRouteManager) ReconcileEndpointPolicy(context.Context, endpoint.Policy) error {
	return ErrRoutingUnsupported
}
func (unsupportedRouteManager) RemoveEndpointPolicy(context.Context, endpoint.Policy) error {
	return ErrRoutingUnsupported
}
func (unsupportedRouteManager) ApplyParking(context.Context, parking.DesiredState) error {
	return ErrRoutingUnsupported
}
func (unsupportedRouteManager) NormalizeSelectedRoute(context.Context, routing.SelectedRouteDesired) (routing.SelectedRouteIdentity, error) {
	return routing.SelectedRouteIdentity{}, ErrSelectedRouteUnsupported
}

func (unsupportedRouteManager) ReconcileSelectedRoute(context.Context, routing.SelectedRouteDesired, *routing.SelectedRoute) (routing.SelectedReconcileResult, error) {
	return routing.SelectedReconcileResult{}, fmt.Errorf("selected-route reconciliation is not supported on this platform")
}

func (unsupportedRouteManager) RemoveSelectedRoute(context.Context, routing.SelectedRoute) error {
	return fmt.Errorf("selected-route removal is not supported on this platform")
}

func (unsupportedRouteManager) Snapshot(context.Context) (routing.KernelState, error) {
	return routing.KernelState{}, ErrRoutingUnsupported
}
func (unsupportedRouteManager) Apply(context.Context, routing.Transaction) error {
	return ErrRoutingUnsupported
}

func DefaultRouteManager() (RouteManager, routing.Executor) {
	manager := unsupportedRouteManager{}
	return manager, manager
}
