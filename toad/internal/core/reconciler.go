package core

import (
	"context"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type RecoveryAction string

const (
	ActionStop             RecoveryAction = "stop"
	ActionWaitForUnderlay  RecoveryAction = "wait-for-underlay"
	ActionValidate         RecoveryAction = "validate"
	ActionRebind           RecoveryAction = "rebind"
	ActionRestartTransport RecoveryAction = "restart-transport"
	ActionRestartToad      RecoveryAction = "restart-toad"
	ActionNone             RecoveryAction = "none"
)

func DecideRole(r RoleRuntime, underlay netstate.Snapshot, caps toadctl.Capabilities) RecoveryAction {
	if !r.Desired {
		return ActionStop
	}
	if underlay.IPv4 == nil && underlay.IPv6 == nil {
		return ActionWaitForUnderlay
	}
	if r.State == RoleStopped || r.State == RoleFailed {
		return ActionRestartToad
	}
	if r.State == RoleValidating {
		return ActionValidate
	}
	if r.ValidatedEpoch != underlay.Epoch {
		if caps.Rebind {
			return ActionRebind
		}
		if caps.RestartTransportKeepingTUN {
			return ActionRestartTransport
		}
		return ActionRestartToad
	}
	if r.State == RoleReady {
		return ActionNone
	}
	return ActionValidate
}

type Reconciler struct{ Controller *Controller }

func (r Reconciler) UnderlayChanged(old, next netstate.Snapshot, reason netstate.ChangeReason) {
	if r.Controller != nil {
		_ = r.Controller.Submit(context.Background(), UnderlayChanged{Old: old, New: next, Reason: reason})
	}
}
func (r Reconciler) Resume() {
	if r.Controller != nil {
		_ = r.Controller.Submit(context.Background(), ResumeValidation{})
	}
}
func (r Reconciler) Completion(role string, op, epoch uint64, result OperationResult) bool {
	if r.Controller == nil {
		return false
	}
	return r.Controller.Complete(role, op, epoch, result)
}
