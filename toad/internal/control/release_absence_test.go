//go:build kikimora_test_failpoints

package control

import (
	"context"
	"testing"
)

// TestReleaseAbsence verifies that the failpoint-specific symbols exist
// in the test build and reports their type. The dual-build contract
// (Phase F §D7) is tested by the CI pipeline: a release build must never
// expose these symbols.  This file is compiled only under the
// kikimora_test_failpoints tag, so in a release build the whole file is
// absent; any attempt to reference TestTriggerManagedRecovery from
// production code would fail at link time.
//
// The test calls TestTriggerManagedRecoveryImpl and recoveryBarrierWait
// to verify they compile and are reachable.
func TestReleaseAbsenceContract(t *testing.T) {
	// TestTriggerManagedRecovery must be nil by default: the test
	// fixture sets it explicitly when needed.
	if TestTriggerManagedRecovery != nil {
		t.Error("TestTriggerManagedRecovery must be nil before a test assigns it")
	}

	// recoveryBarrierWait must be a no-op when recoveryBarrier is nil.
	if err := recoveryBarrierWait(context.Background(), "freeze_selected"); err != nil {
		t.Errorf("recoveryBarrierWait with nil barrier must not block: %v", err)
	}

	// TestTriggerManagedRecoveryImpl must not panic.
	if err := TestTriggerManagedRecoveryImpl(context.Background(), "test-role"); err != nil {
		t.Errorf("TestTriggerManagedRecoveryImpl must not fail on a test role: %v", err)
	}
}