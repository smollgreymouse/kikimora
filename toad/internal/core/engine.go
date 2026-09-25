package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// RecoveryDriver is the only contract the product controller needs from
// platform and protocol adapters. Drivers own resources; core owns ordering.
type RecoveryDriver interface {
	ObserveRoutes(context.Context, string) error
	Park(context.Context, string) error
	Withdraw(context.Context, string) error
	Quiesce(context.Context, string) error
	ApplyEndpoint(context.Context, string) error
	Rebind(context.Context, string) error
	StartTransport(context.Context, string) error
	Validate(context.Context, string) error
	Publish(context.Context, string) error
	ResyncLeshy(context.Context, string) error
	ObserveRestoration(context.Context, string) error
}

type Engine struct {
	Controller *Controller
	Driver     RecoveryDriver
}

// Recover selects the least disruptive action from the current Toad capability
// report and underlay identity. Protocol names never enter this decision.
func (e Engine) Recover(ctx context.Context, role string, operation, epoch uint64) error {
	if e.Controller == nil || e.Driver == nil {
		return fmt.Errorf("recovery engine is not configured")
	}
	if _, ok := e.Controller.Role(role); !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	current, ok := e.Controller.Role(role)
	if !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	underlay := e.Controller.Snapshot().Underlay
	action := SelectTransportAction(current, underlay, current.Toad.Capabilities)
	steps := RecoverySequenceForAction(action)
	if len(steps) == 0 {
		return nil
	}
	state := &current
	work := func(ctx context.Context, step RecoveryStep) error {
		switch step {
		case RecoveryObserveRoutes:
			return e.Driver.ObserveRoutes(ctx, role)
		case RecoveryPark:
			return e.Driver.Park(ctx, role)
		case RecoveryWithdraw:
			return e.Driver.Withdraw(ctx, role)
		case RecoveryQuiesce:
			return e.Driver.Quiesce(ctx, role)
		case RecoveryApplyEndpoint:
			return e.Driver.ApplyEndpoint(ctx, role)
		case RecoveryRebind:
			return e.Driver.Rebind(ctx, role)
		case RecoveryStartTransport:
			return e.Driver.StartTransport(ctx, role)
		case RecoveryValidate:
			return e.Driver.Validate(ctx, role)
		case RecoveryPublish:
			return e.Driver.Publish(ctx, role)
		case RecoveryResyncLeshy:
			return e.Driver.ResyncLeshy(ctx, role)
		case RecoveryObserveRestore:
			return e.Driver.ObserveRestoration(ctx, role)
		default:
			return fmt.Errorf("unknown recovery step %q", step)
		}
	}
	if err := RunRecoverySteps(ctx, state, operation, epoch, steps, work); err != nil {
		failedState := RoleFailed
		switch {
		case errors.Is(err, parking.ErrRoutesStillParked):
			failedState = RoleRecovering
		case errors.Is(err, ErrValidationPending):
			failedState = RoleRecovering
		case errors.Is(err, ErrToadRestartPending):
			// A full process replacement has begun successfully. The old
			// generation is invalidated; the replacement will continue through the
			// normal RouteReady -> validation -> activation path.
			failedState = RoleStarting
		}
		_ = e.Controller.SetRecovery(role, state.Recovery, failedState, state.Recovery.LastError)
		return err
	}
	_ = e.Controller.SetRecovery(role, state.Recovery, RoleReady, "recovery complete")
	return nil
}

// SelectTransportAction uses only the Toad capability report and underlay
// identity, never protocol names.
func SelectTransportAction(role RoleRuntime, underlay netstate.Snapshot, caps toadctl.Capabilities) RecoveryAction {
	return DecideRole(role, underlay, caps)
}
