//go:build windows

package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// DefaultAddress is the canonical local control-plane endpoint. Windows uses
// a bounded AF_UNIX socket under the service-owned ProgramData directory:
// named pipes via go-winio currently fail with ERROR_INVALID_FUNCTION in any
// binary that links this package (see 08a4a packet), and AF_UNIX connect()
// enforces the socket file's ACL, giving the same kernel-side authorization
// gate the packet requires. The Qt UI swap to this transport is deferred to
// the UI-integration phase, which will revisit named pipes.
const DefaultAddress = `C:\ProgramData\Kikimora\core.sock`

// prepareControlAddress creates the parent directory of the Unix socket and
// removes any stale socket file left behind by a previous run.
func prepareControlAddress(address string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(address), 0o755); err != nil {
		return "", fmt.Errorf("create control socket directory: %w", err)
	}
	_ = os.Remove(address)
	return address, nil
}

// listenControl binds the local control-plane endpoint. The 0660 mode keeps
// same-owner/group access; the installed service directory carries the
// restrictive ACL that gates unrelated local users at connect time.
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
