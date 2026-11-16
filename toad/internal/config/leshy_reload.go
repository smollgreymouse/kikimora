package config

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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
func ReloadLeshy(ctx context.Context) error {
	commands, err := leshyRestartCommands()
	if err != nil {
		return err
	}
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, command := range commands {
		output, runErr := exec.CommandContext(timeout, command[0], command[1:]...).CombinedOutput()
		if runErr != nil {
			return fmt.Errorf("%s: %w: %s",
				strings.Join(command, " "), runErr, strings.TrimSpace(string(output)))
		}
	}
	return nil
}
