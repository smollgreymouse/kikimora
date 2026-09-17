//go:build !linux

package supervisor

import (
	"os"
	"os/exec"
	"sync"
)

type execProcess struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	waitErr  error
}

func startProcess(binary string, args ...string) (Process, error) {
	cmd := exec.Command(binary, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}
func (p *execProcess) PID() int { return p.cmd.Process.Pid }
func (p *execProcess) Wait() error {
	p.waitOnce.Do(func() { p.waitErr = p.cmd.Wait() })
	return p.waitErr
}
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *execProcess) Kill() error                { return p.cmd.Process.Kill() }
