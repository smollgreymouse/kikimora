//go:build linux || darwin

package openconnect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// RestartTransport asks the official OpenConnect child to reconnect in place.
// OpenConnect documents SIGUSR2 specifically for recovering after a LAN IP
// address change. The route-free vpnc-script republishes openconnect-network.env
// only after the new data phase is established; waiting for that publication
// prevents the core from validating a stale but still-UP TUN.
func (b *Backend) RestartTransport(ctx context.Context, _ toadctl.UnderlayBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	cmd := b.cmd
	cfg := b.cfg
	exitErr := b.exitErr
	b.mu.Unlock()
	if cmd == nil || cmd.Process == nil || exitErr != nil {
		return fmt.Errorf("official OpenConnect client is not running")
	}
	if cfg == nil || cfg.StateDir == "" {
		return fmt.Errorf("OpenConnect state directory is unavailable")
	}

	networkState := filepath.Join(cfg.StateDir, "openconnect-network.env")
	if err := os.Remove(networkState); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear stale OpenConnect network state: %w", err)
	}
	if err := signalReconnect(cmd.Process); err != nil {
		return fmt.Errorf("request OpenConnect reconnect: %w", err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(networkState)
		if err == nil && hasReconnectPublication(data) {
			b.mu.Lock()
			sameProcess := b.cmd == cmd && b.exitErr == nil
			b.mu.Unlock()
			if !sameProcess {
				return fmt.Errorf("official OpenConnect client exited during reconnect")
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
