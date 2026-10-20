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
// The restart is best effort: the lists are already on disk, and a failure here is reported to
// the operator instead of undoing the write.
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
