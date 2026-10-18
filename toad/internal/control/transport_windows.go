//go:build windows

package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
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
	if err := allowInteractiveUsers(address); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("grant control socket access: %w", err)
	}
	return listener, nil
}

// dialControl connects to the local control-plane endpoint.
func dialControl(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}

// allowInteractiveUsers grants the built-in Users group read+write on the socket file,
// merging the ACE into whatever DACL the file already carries.
//
// The service runs as LocalSystem, so the socket inherits that token's default DACL,
// where BUILTIN\Users holds only read+execute. AF_UNIX connect() needs write access,
// so a desktop UI started by an ordinary user could enumerate the socket but never
// connect and then showed no profile and no tunnel.
func allowInteractiveUsers(path string) error {
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return fmt.Errorf("users sid: %w", err)
	}

	var access windows.EXPLICIT_ACCESS
	access.AccessPermissions = windows.GENERIC_READ | windows.GENERIC_WRITE
	access.AccessMode = windows.GRANT_ACCESS
	access.Inheritance = windows.NO_INHERITANCE
	access.Trustee.MultipleTrusteeOperation = windows.NO_MULTIPLE_TRUSTEE
	access.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	access.Trustee.TrusteeType = windows.TRUSTEE_IS_GROUP
	access.Trustee.TrusteeValue = windows.TrusteeValueFromSID(usersSID)

	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read socket dacl: %w", err)
	}
	existing, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("decode socket dacl: %w", err)
	}

	merged, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{access}, existing)
	if err != nil {
		return fmt.Errorf("merge socket dacl: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, merged, nil); err != nil {
		return fmt.Errorf("write socket dacl: %w", err)
	}
	return nil
}
