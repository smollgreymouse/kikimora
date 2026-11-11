// Package control implements the core reconciliation and recovery driver.
//
// recoveryBarrier and TestTriggerManagedRecovery are test-only seams — see
// recovery_barrier_failpoints_testbuild.go and the default no-op variant.
package control

import "context"

// recoveryBarrier is set by tests to block managed recovery at a named phase.
// When non-nil, ObserveRoutes, Park and similar recovery steps call wait(ctx)
// on the barrier. The test driving the barrier can trigger a
// TestTriggerManagedRecovery call afterwards.
//
// The barrier is never read in release builds (the default file compiles it
// away). Tests use the kikimora_test_failpoints build tag.
var recoveryBarrier func(ctx context.Context, phase string) error

// TestTriggerManagedRecovery is set by tests to inject a managed-recovery
// pass from outside the normal driver loop. The test binary publishes it
// under kikimora_test_failpoints; release binaries do not expose it.
var TestTriggerManagedRecovery func(ctx context.Context, role string) error