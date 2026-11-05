//go:build !windows

package fsutil

import "os"

// renameDurable performs the final same-volume replacement on POSIX. The
// parent directory sync is done by the caller.
func renameDurable(from, to string) error {
	return os.Rename(from, to)
}
