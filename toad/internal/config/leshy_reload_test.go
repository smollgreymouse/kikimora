package config

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// fakeExitError carries an exit code without needing a real process state:
// the tolerance check matches any error that can report its exit code (the
// runner contract exec.ExitError satisfies).
type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return fmt.Sprintf("exit %d", e.code) }
func (e fakeExitError) ExitCode() int { return e.code }

// The Windows stop step's 1062 ("The service has not been started") means
// the service is already stopped — the state the start step needs. An
// operator or a harness that stopped Leshy before a live-policy transaction
// (the disable needs the writer lock free) must not turn the reload into an
// unproved rollback (H4/H5, 2026-11-20).
func TestReloadStepsTolerateStoppedServiceStop(t *testing.T) {
	steps := []reloadStep{
		{Command: []string{"sc.exe", "stop", "leshy"}, ToleratedExit: map[int]bool{1062: true}},
		{Command: []string{"sc.exe", "start", "leshy"}},
	}
	var ran []string
	runner := func(command []string) ([]byte, error) {
		ran = append(ran, command[1])
		if command[1] == "stop" {
			return []byte("[SC] ControlService FAILED 1062"), fakeExitError{code: 1062}
		}
		return nil, nil
	}
	if err := runReloadSteps(context.Background(), steps, runner); err != nil {
		t.Fatalf("the tolerated stop failure must not fail the reload: %v", err)
	}
	if len(ran) != 2 || ran[0] != "stop" || ran[1] != "start" {
		t.Fatalf("the start step must still run after the tolerated stop: %v", ran)
	}
}

func TestReloadStepsFailOnUnexpectedExit(t *testing.T) {
	steps := []reloadStep{
		{Command: []string{"sc.exe", "stop", "leshy"}, ToleratedExit: map[int]bool{1062: true}},
		{Command: []string{"sc.exe", "start", "leshy"}},
	}
	runner := func(command []string) ([]byte, error) {
		return []byte("access denied"), fakeExitError{code: 5}
	}
	err := runReloadSteps(context.Background(), steps, runner)
	if err == nil {
		t.Fatal("an unexpected exit code must fail the reload")
	}
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) || ec.ExitCode() != 5 {
		t.Fatalf("the error must carry the failing exit code: %v", err)
	}
	if _, ok := errors.Unwrap(err).(*exec.ExitError); ok {
		t.Log("exec.ExitError unwrap is available")
	}
}
