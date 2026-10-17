//go:build windows

package openconnect

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// configureChildProcess isolates the official openconnect.exe child from
// the parent's console control events. Without CREATE_NEW_PROCESS_GROUP,
// os.Interrupt (CTRL_C_EVENT) delivered to the child also reaches the
// core service process sharing the same console, which can terminate
// the service unintentionally during graceful shutdown.
func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}
