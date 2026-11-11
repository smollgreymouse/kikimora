//go:build kikimora_test_failpoints

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Environment protocol of the tagged recovery barrier (R2 §4.2). A test
// stages exactly one step boundary: when the driver finishes that step the
// core creates the .waiting marker and blocks until the test drops the
// .continue file. Release binaries never contain this protocol.
const (
	recoveryBarrierStepEnv = "KK_RECOVERY_BARRIER_STEP"
	recoveryBarrierRoleEnv = "KK_RECOVERY_BARRIER_ROLE"
	recoveryBarrierDirEnv  = "KK_RECOVERY_BARRIER_DIR"
)

const (
	recoveryBarrierPollInterval = 50 * time.Millisecond
	recoveryBarrierTimeout      = 30 * time.Second
)

// processRecoveryBarrier runs after a successful RecoveryStep and before the
// next step advances. Rules:
//
//   - an empty/non-matching KK_RECOVERY_BARRIER_STEP or a non-matching
//     KK_RECOVERY_BARRIER_ROLE returns immediately;
//   - a matching step requires KK_RECOVERY_BARRIER_DIR (missing is an error);
//   - the .waiting marker is created with O_CREATE|O_EXCL so a pre-existing
//     marker is an error, never an automatic cleanup;
//   - the call polls for the .continue file and removes both markers when it
//     appears; on timeout or cancellation the .waiting marker is left in
//     place for diagnostics;
//   - the hook performs zero route/state mutation beyond its own markers.
func processRecoveryBarrier(ctx context.Context, role string, step RecoveryStep) error {
	wantStep := os.Getenv(recoveryBarrierStepEnv)
	if wantStep == "" || wantStep != string(step) {
		return nil
	}
	if wantRole := os.Getenv(recoveryBarrierRoleEnv); wantRole != "" && wantRole != role {
		return nil
	}
	dir := os.Getenv(recoveryBarrierDirEnv)
	if dir == "" {
		return fmt.Errorf("recovery barrier for step %q requires %s", step, recoveryBarrierDirEnv)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("recovery barrier directory: %w", err)
	}
	waiting := filepath.Join(dir, role+"."+string(step)+".waiting")
	cont := filepath.Join(dir, role+"."+string(step)+".continue")
	file, err := os.OpenFile(waiting, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("recovery barrier .waiting marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("recovery barrier .waiting marker: %w", err)
	}

	timeout := recoveryBarrierTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(recoveryBarrierPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// The .waiting marker stays for diagnostics.
			return fmt.Errorf("recovery barrier cancelled while waiting for %s: %w", cont, ctx.Err())
		case <-timer.C:
			// The .waiting marker stays for diagnostics.
			return fmt.Errorf("recovery barrier timed out waiting for %s", cont)
		case <-ticker.C:
			if _, err := os.Stat(cont); err == nil {
				if err := os.Remove(cont); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("recovery barrier continue removal: %w", err)
				}
				if err := os.Remove(waiting); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("recovery barrier waiting removal: %w", err)
				}
				return nil
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("recovery barrier continue probe: %w", err)
			}
		}
	}
}
