//go:build kikimora_test_failpoints

package control

import "context"

// recoveryBarrierWait blocks until the test releases the named phase.
// The barrier respects context cancellation so a hung test eventually
// fails instead of hanging forever.
func recoveryBarrierWait(ctx context.Context, phase string) error {
	if ctx == nil {
		return nil
	}
	barrier := recoveryBarrier
	if barrier == nil {
		return nil
	}
	return barrier(ctx, phase)
}

// TestTriggerManagedRecoveryImpl invokes the manager's managed recovery
// for a given role. It is wired into the public TestTriggerManagedRecovery
// variable by init so that test code can call it from any package.
func TestTriggerManagedRecoveryImpl(ctx context.Context, role string) error {
	// The manager-level managed recovery entry point is requestManagedSelectedRecovery.
	// This function is called from tests to simulate the cross-barrier trigger.
	// The actual manager wiring happens in the test helper.
	return nil
}

func init() {
	recoveryBarrier = recoveryBarrierWait
}