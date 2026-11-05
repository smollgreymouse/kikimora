//go:build !windows

package lockfile

import (
	"os"
	"syscall"
)

// lockExclusive takes an flock LOCK_EX|LOCK_NB on the open file (08D1 §31.2).
func lockExclusive(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
