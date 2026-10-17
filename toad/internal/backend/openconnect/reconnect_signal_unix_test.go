//go:build linux || darwin

package openconnect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

func TestRestartTransportSignalsAndWaitsForReconnectPublication(t *testing.T) {
	dir := t.TempDir()
	networkState := filepath.Join(dir, "openconnect-network.env")
	ready := filepath.Join(dir, "ready")
	if err := os.WriteFile(networkState, []byte("reason=connect\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(dir, "signal-fixture.sh")
	script := "#!/usr/bin/env bash\n" +
		"trap 'printf \"reason=reconnect\\n\" > \"$1\"' USR2\n" +
		"printf ready > \"$2\"\n" +
		"while :; do sleep 1; done\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", scriptPath, networkState, ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("signal fixture did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	b := &Backend{
		cfg: &config.Config{StateDir: dir},
		cmd: cmd,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.RestartTransport(ctx, toadctl.UnderlayBinding{}); err != nil {
		t.Fatalf("RestartTransport() error = %v", err)
	}
	data, err := os.ReadFile(networkState)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "reason=reconnect\n" {
		t.Fatalf("unexpected reconnect publication: %q", data)
	}
}
