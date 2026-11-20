package config

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// reloadStep is one command of the Leshy service restart sequence.
// ToleratedExit codes mean the step's intent is already satisfied: the
// Windows stop step's 1062 ("The service has not been started") says the
// service is stopped — exactly the state the start step needs. An operator
// or an acceptance harness that stopped Leshy before a live-policy
// transaction (the disable needs the writer lock free) must not turn the
// reload into an unproved rollback and quarantine the authority (H4/H5,
// 2026-11-20).
type reloadStep struct {
	Command       []string
	ToleratedExit map[int]bool
}

// ReloadLeshy restarts the Leshy service so it re-reads the routing lists. The service has no
// ExecReload, a full restart is the documented way to apply a new configuration; on Linux the
// DNS integration unit is PartOf=leshy.service, so it comes back on its own.
//
// The restart is part of the fail-closed live policy transaction (AUD2,
// 2026-11-16 post-e7ec9c2 audit §3.5/§3.6): a user-facing mutation — bypass,
// routing list, per-role IPv6 — only succeeds when the running Leshy
// actually reloaded the new policy. A reload failure is a command failure
// whose rollback restores the source file, the generated configuration and
// the old policy; it is no longer "best effort" on already-written files.
// A step that exits with a tolerated code counts as satisfied.
func ReloadLeshy(ctx context.Context) error {
	steps, err := leshyRestartSteps()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return runReloadSteps(runCtx, steps, func(command []string) ([]byte, error) {
		return exec.CommandContext(runCtx, command[0], command[1:]...).CombinedOutput()
	})
}

// runReloadSteps runs the platform restart sequence step by step; a step
// whose error carries a tolerated exit code counts as satisfied. The runner
// injection keeps the tolerance testable without a real service manager.
func runReloadSteps(ctx context.Context, steps []reloadStep, run func([]string) ([]byte, error)) error {
	for _, step := range steps {
		output, runErr := run(step.Command)
		if runErr == nil {
			continue
		}
		exitCode := -1
		var exitCoder interface{ ExitCode() int }
		if errors.As(runErr, &exitCoder) {
			exitCode = exitCoder.ExitCode()
		}
		if step.ToleratedExit[exitCode] {
			continue
		}
		return fmt.Errorf("%s: %w: %s",
			strings.Join(step.Command, " "), runErr, strings.TrimSpace(string(output)))
	}
	return nil
}
