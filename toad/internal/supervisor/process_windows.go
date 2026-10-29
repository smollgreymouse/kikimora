//go:build windows

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// execProcess mirrors the non-Windows implementation and adds a kill-on-close
// job object: the core supervises SYSTEM-level tunnel processes, and a core
// crash (or an SCM kill) used to orphan them - the orphan kept the role's
// wintun adapter open, so the restarted core could never recreate it.
type execProcess struct {
	cmd      *exec.Cmd
	job      windows.Handle
	waitOnce sync.Once
	waitErr  error
}

func startProcess(binary string, args ...string) (Process, error) {
	cmd := exec.Command(binary, args...)
	if writer := toadLogWriter(binary); writer != nil {
		cmd.Stdout = writer
		cmd.Stderr = writer
	}
	// The child (and its own children - OpenConnect spawns helpers) joins a
	// job that the kernel tears down when this core process dies.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	limit := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limit)), uint32(unsafe.Sizeof(limit))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure job object: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	// Assign after Start: the job needs the child's process handle. The
	// handle is opened by pid because os.Process does not expose its own.
	child, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("open child for job assignment: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, child); err != nil {
		_ = windows.CloseHandle(child)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("assign to job object: %w", err)
	}
	_ = windows.CloseHandle(child)
	return &execProcess{cmd: cmd, job: job}, nil
}

func (p *execProcess) PID() int { return p.cmd.Process.Pid }
func (p *execProcess) Wait() error {
	p.waitOnce.Do(func() { p.waitErr = p.cmd.Wait() })
	return p.waitErr
}
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *execProcess) Kill() error                { return p.cmd.Process.Kill() }
func (p *execProcess) Stop() error {
	if err := p.cmd.Process.Kill(); err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
	return p.Wait()
}
