//go:build linux

package logind

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Event struct{ Preparing bool }
type Source struct{ Command string }

func (s Source) Watch(ctx context.Context, out chan<- Event) error {
	command := s.Command
	if command == "" {
		command = "dbus-monitor"
	}
	cmd := exec.CommandContext(ctx, command, "--system", "type='signal',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start logind sleep monitor: %w", err)
	}
	defer cmd.Process.Kill()

	scanner := bufio.NewScanner(stdout)
	pending := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "member=PrepareForSleep") {
			pending = true
			continue
		}
		if !pending || !strings.Contains(scanner.Text(), "boolean") {
			continue
		}
		line := strings.ToLower(scanner.Text())
		event := Event{Preparing: strings.Contains(line, "true")}
		pending = false
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
		return fmt.Errorf("logind sleep monitor exited: %w", err)
	}
	return ctx.Err()
}
