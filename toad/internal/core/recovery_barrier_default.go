//go:build !kikimora_test_failpoints

package core

import "context"

// processRecoveryBarrier is the test-only failpoint hook at the recovery
// step boundary (08D1 post-739e1ab audit R2). The default build has no
// barrier: recovery steps run uninterrupted and release binaries carry no
// barrier protocol at all.
func processRecoveryBarrier(_ context.Context, _ string, _ RecoveryStep) error {
	return nil
}
