//go:build !linux && !darwin

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

func (unsupportedRouteManager) ApplyEndpointPolicy(context.Context, endpoint.Policy) error {
	return ErrRoutingUnsupported
}
func (unsupportedRouteManager) ApplyParking(context.Context, parking.DesiredState) error {
	return ErrRoutingUnsupported
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
