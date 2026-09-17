//go:build linux

package supervisor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type execProcess struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	waitErr  error
}

func startProcess(binary string, args ...string) (Process, error) {
	cmd := exec.Command(binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}
func (p *execProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
func (p *execProcess) Wait() error {
	p.waitOnce.Do(func() { p.waitErr = p.cmd.Wait() })
	return p.waitErr
}
func (p *execProcess) Signal(sig os.Signal) error {
	if p.PID() == 0 {
		return os.ErrProcessDone
	}
	if s, ok := sig.(syscall.Signal); ok {
		return syscall.Kill(-p.PID(), s)
	}
	return p.cmd.Process.Signal(sig)
}
func (p *execProcess) Kill() error { return p.Signal(os.Kill) }
