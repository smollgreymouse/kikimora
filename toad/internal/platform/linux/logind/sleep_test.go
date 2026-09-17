//go:build linux

package logind

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceWatchParsesPrepareForSleepSignals(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "dbus-monitor")
	data := "#!/bin/sh\nprintf '%s\\n' 'signal sender=org.freedesktop.login1 interface=org.freedesktop.login1.Manager member=PrepareForSleep' '   boolean true' 'signal sender=org.freedesktop.login1 interface=org.freedesktop.login1.Manager member=PrepareForSleep' '   boolean false'\n"
	if err := os.WriteFile(script, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 2)
	if err := (Source{Command: script}).Watch(ctx, events); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	if got := <-events; !got.Preparing {
		t.Fatalf("first event = %#v, want preparing", got)
	}
	if got := <-events; got.Preparing {
		t.Fatalf("second event = %#v, want resume", got)
	}
}
