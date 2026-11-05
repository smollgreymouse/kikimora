//go:build !windows

package control

import (
	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// writePrivilegedAuthFile stores the credential with Unix mode 0600 through
// the shared durable primitive (08D1 §31.6). There is no peer-based or group
// bypass on Linux: service clients use the same bearer protocol.
func writePrivilegedAuthFile(path string, data []byte) error {
	return fsutil.WriteAtomicFile(path, data, 0o600)
}
