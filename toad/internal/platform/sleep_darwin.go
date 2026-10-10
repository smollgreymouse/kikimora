//go:build darwin

package platform

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type darwinSleepSource struct{ command string }

func DefaultSleepSource() SleepSource { return darwinSleepSource{} }

func (s darwinSleepSource) Watch(ctx context.Context, out chan<- SleepEvent) error {
	command := s.command
	if command == "" {
		command = "/usr/bin/log"
	}
	cmd := exec.CommandContext(ctx, command, "stream", "--style", "syslog", "--predicate", "subsystem == 'com.apple.powerd'")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start macOS power monitor: %w", err)
	}
	defer cmd.Process.Kill()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		event, ok := parseDarwinPowerEvent(scanner.Text())
		if !ok {
			continue
		}
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("macOS power monitor exited: %w", err)
	}
	return ctx.Err()
}

func parseDarwinPowerEvent(line string) (SleepEvent, bool) {
	line = strings.ToLower(line)
	if strings.Contains(line, "entering sleep") || strings.Contains(line, "sleeping now") {
		return SleepEvent{Preparing: true}, true
	}
	if strings.Contains(line, "wake from") || strings.Contains(line, "wake reason") || strings.Contains(line, "waking") {
		return SleepEvent{}, true
	}
	return SleepEvent{}, false
}
