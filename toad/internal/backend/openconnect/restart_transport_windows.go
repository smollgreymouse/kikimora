//go:build windows

package openconnect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// RestartTransport on Windows performs a controlled full-process restart
// because the official openconnect.exe port does not expose a POSIX-style
// reconnect signal (SIGUSR2). The Toad-owned Wintun adapter persists across
// the child process lifecycle; the new openconnect.exe re-attaches to the
// same adapter by name. The route-free vpnc-script republishes
// openconnect-network.env only after the new data phase is established;
// waiting for that publication prevents the core from validating a stale
// but still-UP TUN.
func (b *Backend) RestartTransport(ctx context.Context, binding toadctl.UnderlayBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	cfg := b.cfg
	b.mu.Unlock()
	if cfg == nil || cfg.StateDir == "" {
		return fmt.Errorf("OpenConnect state directory is unavailable")
	}

	networkState := filepath.Join(cfg.StateDir, "openconnect-network.env")
	if err := os.Remove(networkState); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear stale OpenConnect network state: %w", err)
	}

	// Full process restart: Close kills the current openconnect.exe child,
	// Start launches a new one that re-attaches to the persistent TUN.
	if err := b.Close(); err != nil {
		return fmt.Errorf("close OpenConnect for transport restart: %w", err)
	}
	if err := b.Start(ctx); err != nil {
		return fmt.Errorf("restart OpenConnect transport: %w", err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(networkState)
		if err == nil && hasReconnectPublication(data) {
			b.mu.Lock()
			running := b.cmd != nil && b.exitErr == nil
			b.mu.Unlock()
			if !running {
				return fmt.Errorf("official OpenConnect client exited during transport restart")
			}
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read OpenConnect reconnect state: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
