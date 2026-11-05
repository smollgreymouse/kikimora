//go:build windows

package lockfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockExclusive takes a LockFileEx exclusive non-blocking lock on the open
// file (08D1 §31.2).
func lockExclusive(file *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
}

func unlock(file *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, ol)
}
