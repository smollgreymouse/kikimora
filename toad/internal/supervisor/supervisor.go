// Package supervisor owns child-process lifetime. It deliberately has no
// knowledge of VPN protocols or routing policy.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type Process interface {
	PID() int
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

type Launcher interface {
	Start(context.Context, string) (Process, error)
}

type ExecLauncher struct {
	Binary     string
	ExtraArgs  []string
	StopGrace  time.Duration
	FinalGrace time.Duration
}

func (l ExecLauncher) Start(_ context.Context, configPath string) (Process, error) {
	if configPath == "" {
		return nil, errors.New("Toad config path is empty")
	}
	binary := l.Binary
	if binary == "" {
		binary = "kikimora-toad"
	}
	args := append([]string{"run", "-config", configPath}, l.ExtraArgs...)
	return startProcess(binary, args...)
}

func (l ExecLauncher) Stop(ctx context.Context, p Process) error {
	if p == nil {
		return nil
	}
	grace := l.StopGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	final := l.FinalGrace
	if final <= 0 {
		final = time.Second
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	wait := func(timeout time.Duration) bool {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-done:
			return true
		case <-t.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
	if err := p.Signal(os.Interrupt); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		killErr := p.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return fmt.Errorf("interrupt process %d: %v; kill fallback: %w", p.PID(), err, killErr)
		}
		if errors.Is(killErr, os.ErrProcessDone) || wait(final) {
			return nil
		}
		return fmt.Errorf("process %d did not stop after interrupt failed: %w", p.PID(), err)
	}
	if wait(grace) {
		return nil
	}
	_ = p.Signal(syscall.SIGTERM)
	if wait(final) {
		return nil
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("terminate process %d after %s: %w", p.PID(), grace+final, err)
	}
	if wait(final) {
		return nil
	}
	return fmt.Errorf("process %d did not stop within %s", p.PID(), grace+2*final)
}
