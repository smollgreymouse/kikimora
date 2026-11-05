//go:build windows

package control

import (
	"golang.org/x/sys/windows"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// writePrivilegedAuthFile stores the credential with a DACL granting
// SYSTEM and Administrators only (08D1 §31.6). The file lands through the
// shared durable primitive after the ACL is applied to the path.
func writePrivilegedAuthFile(path string, data []byte) error {
	if err := fsutil.WriteAtomicFile(path, data, 0o600); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
