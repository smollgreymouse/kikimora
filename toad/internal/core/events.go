package core

import (
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type Event interface{ isCoreEvent() }
type UnderlayInvalidated struct{ Source string }

func (UnderlayInvalidated) isCoreEvent() {}

type UnderlayChanged struct {
	Old, New netstate.Snapshot
	Reason   netstate.ChangeReason
}

func (UnderlayChanged) isCoreEvent() {}

type ResumeValidation struct{}

func (ResumeValidation) isCoreEvent() {}

type DesiredRoleChanged struct {
	Role    string
	Enabled bool
}

func (DesiredRoleChanged) isCoreEvent() {}

type ToadStateChanged struct {
	Role       string
	Generation uint64
	Snapshot   toadctl.Snapshot
}

func (ToadStateChanged) isCoreEvent() {}

type OperationResult struct {
	Err    error
	Reason string
}
type OperationCompleted struct {
	Role             string
	Operation, Epoch uint64
	Result           OperationResult
}

func (OperationCompleted) isCoreEvent() {}

type RouteStateChanged struct{}

func (RouteStateChanged) isCoreEvent() {}

type RetryDue struct {
	Role      string
	Operation uint64
}

func (RetryDue) isCoreEvent() {}
