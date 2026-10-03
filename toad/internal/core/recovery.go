package core

import "errors"

var ErrToadRestartPending = errors.New("Toad restart started; awaiting replacement generation")
var ErrValidationPending = errors.New("Toad validation is temporarily unhealthy")
var ErrUnderlayPathUnavailable = errors.New("required physical underlay path is temporarily unavailable")

type RecoveryStep string

const (
	RecoveryObserveRoutes  RecoveryStep = "observe-routes"
	RecoveryPark           RecoveryStep = "park"
	RecoveryWithdraw       RecoveryStep = "withdraw"
	RecoveryQuiesce        RecoveryStep = "quiesce"
	RecoveryApplyEndpoint  RecoveryStep = "apply-endpoint"
	RecoveryRebind         RecoveryStep = "rebind"
	RecoveryStartTransport RecoveryStep = "start-transport"
	RecoveryValidate       RecoveryStep = "validate"
	RecoveryPublish        RecoveryStep = "publish"
	RecoveryResyncLeshy    RecoveryStep = "resync-leshy"
	RecoveryObserveRestore RecoveryStep = "observe-restoration"
)

type ShutdownIntent string

const (
	ShutdownRole        ShutdownIntent = "role-disconnect"
	ShutdownProfile     ShutdownIntent = "profile-switch"
	ShutdownCoreRestart ShutdownIntent = "core-restart"
	ShutdownProduct     ShutdownIntent = "product-stop"
)
