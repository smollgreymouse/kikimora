//go:build !windows

package openconnect

import (
	"os/exec"
	"syscall"
)

// configureChildProcess places the official OpenConnect child in its own
// process group so SIGINT/SIGTERM delivered to the child never reaches
// the parent daemon. Pdeathsig is wired separately through the supervisor
// package on Linux; the process-group split is the portable safety net.
func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
