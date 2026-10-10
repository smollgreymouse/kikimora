//go:build unix

package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// DefaultAddress is the canonical local control-plane endpoint.
const DefaultAddress = "/run/kikimora/core.sock"

// prepareControlAddress creates the parent directory of a Unix socket and
// removes any stale socket file left behind by a previous run.
func prepareControlAddress(address string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(address), 0o755); err != nil {
		return "", err
	}
	_ = os.Remove(address)
	return address, nil
}

// listenControl binds the local control-plane endpoint. The Unix socket is
// chmod'ed 0660 so only same-group peers reach the authorization check.
func listenControl(address string) (net.Listener, error) {
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	if err := os.Chmod(address, 0o660); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("protect control socket: %w", err)
	}
	return listener, nil
}

// dialControl connects to the local control-plane endpoint.
func dialControl(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}
