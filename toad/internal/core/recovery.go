package core

import "errors"

var ErrToadRestartPending = errors.New("Toad restart started; awaiting replacement generation")
var ErrValidationPending = errors.New("Toad validation is temporarily unhealthy")
var ErrUnderlayPathUnavailable = errors.New("required physical underlay path is temporarily unavailable")
var ErrEndpointResolutionPending = errors.New("endpoint resolution is temporarily unavailable")
var ErrRecoveryCancelled = errors.New("recovery transaction was cancelled")

type RecoveryStep string

const (
	RecoveryObserveRoutes RecoveryStep = "observe-routes"
	RecoveryPark          RecoveryStep = "park"
	// RecoveryWithdrawSelected removes only the exact registered selected
	// identities after the parks are proven; the park then becomes the
	// winning same-prefix protection (08D1 §7.3). It runs between Park and
	// the legacy publication Withdraw.
	RecoveryWithdrawSelected RecoveryStep = "withdraw-selected"
	RecoveryWithdraw         RecoveryStep = "withdraw"
	RecoveryQuiesce          RecoveryStep = "quiesce"
	RecoveryApplyEndpoint    RecoveryStep = "apply-endpoint"
	RecoveryRebind           RecoveryStep = "rebind"
	RecoveryStartTransport   RecoveryStep = "start-transport"
	RecoveryValidate         RecoveryStep = "validate"
	RecoveryPublish          RecoveryStep = "publish"
	RecoveryResyncLeshy      RecoveryStep = "resync-leshy"
	RecoveryObserveRestore   RecoveryStep = "observe-restoration"
	// Managed selected-route steps (08D §8): the freeze closes the
	// ObserveRoutes→Park→Withdraw race by denying new Ensure calls and
	// waiting for in-flight ones; the restore re-applies the explicit
	// registry onto the current interface; the unfreeze re-opens the role
	// under a new route generation, always last.
	RecoveryFreezeSelected   RecoveryStep = "freeze-selected"
	RecoveryRestoreSelected  RecoveryStep = "restore-selected"
	RecoveryUnfreezeSelected RecoveryStep = "unfreeze-selected"
)

type ShutdownIntent string

const (
	ShutdownRole        ShutdownIntent = "role-disconnect"
	ShutdownProfile     ShutdownIntent = "profile-switch"
	ShutdownCoreRestart ShutdownIntent = "core-restart"
	ShutdownProduct     ShutdownIntent = "product-stop"
)
