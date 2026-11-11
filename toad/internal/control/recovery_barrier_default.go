//go:build !kikimora_test_failpoints

package control

import "context"

// recoveryBarrier is a no-op in release builds (Phase F §D7).
func recoveryBarrierWait(_ context.Context, _ string) error { return nil }

// TestTriggerManagedRecovery is nil in release builds — the function is
// never exposed to a package consumer outside a test binary.
func init() {
	recoveryBarrier = recoveryBarrierWait
	TestTriggerManagedRecovery = nil
}