package core

import (
	"context"
	"errors"
)

// RecoveryWork is the dependency-injected boundary for route, Toad and Leshy
// drivers. Keeping each step visible makes recovery auditable and retryable.
type RecoveryWork func(context.Context, RecoveryStep) error

// RecoverySequence is deliberately protocol-neutral; capability selection is
// done by the caller before choosing the transport step.
func RecoverySequence() []RecoveryStep {
	return []RecoveryStep{
		RecoveryObserveRoutes,
		RecoveryPark,
		RecoveryWithdraw,
		RecoveryQuiesce,
		RecoveryApplyEndpoint,
		RecoveryStartTransport,
		RecoveryValidate,
		RecoveryPublish,
		RecoveryResyncLeshy,
		RecoveryObserveRestore,
	}
}

// RecoverySequenceForAction keeps the safety boundary explicit while allowing
// the capability selector to avoid disruptive work for a healthy resume.
func RecoverySequenceForAction(action RecoveryAction) []RecoveryStep {
	switch action {
	case ActionValidate:
		return []RecoveryStep{RecoveryValidate}
	case ActionRebind:
		return []RecoveryStep{
			RecoveryObserveRoutes,
			RecoveryPark,
			RecoveryWithdraw,
			RecoveryQuiesce,
			RecoveryApplyEndpoint,
			RecoveryRebind,
			RecoveryValidate,
			RecoveryPublish,
			RecoveryResyncLeshy,
			RecoveryObserveRestore,
		}
	case ActionRestartTransport:
		return []RecoveryStep{
			RecoveryObserveRoutes,
			RecoveryPark,
			RecoveryWithdraw,
			RecoveryQuiesce,
			RecoveryApplyEndpoint,
			RecoveryStartTransport,
			RecoveryValidate,
			RecoveryPublish,
			RecoveryResyncLeshy,
			RecoveryObserveRestore,
		}
	case ActionRestartToad:
		return RecoverySequence()
	default:
		return nil
	}
}

// RunRecovery records the current step before invoking its driver. A failed
// step leaves the role unpublished/parked state to the caller and preserves
// enough identity to resume after a core restart.
func RunRecovery(ctx context.Context, role *RoleRuntime, operation, epoch uint64, work RecoveryWork) error {
	return RunRecoverySteps(ctx, role, operation, epoch, RecoverySequence(), work)
}

func RunRecoverySteps(ctx context.Context, role *RoleRuntime, operation, epoch uint64, steps []RecoveryStep, work RecoveryWork) error {
	if role == nil || work == nil {
		return errors.New("recovery role and work are required")
	}
	role.Recovery.Operation = operation
	role.Recovery.Epoch = epoch
	role.Recovery.Attempt++
	for _, step := range steps {
		role.Recovery.Step = step
		role.Recovery.LastError = ""
		if err := work(ctx, step); err != nil {
			role.Recovery.LastError = err.Error()
			return err
		}
	}
	role.Recovery.Step = ""
	role.Recovery.LastError = ""
	return nil
}
