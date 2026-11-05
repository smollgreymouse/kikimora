// Package lockfile provides real OS-backed exclusive lifetime locks
// (08D1 §31.2/§31.15). The lock content is never authority: exclusivity
// comes from the OS file lock, so an abnormal process exit releases it.
package lockfile

import (
	"fmt"
	"os"
)

// Lock is a held exclusive lock. It is released by Close (or by process
// death, which is the point).
type Lock struct {
	file *os.File
}

// Acquire takes an exclusive non-blocking OS lock on path, creating the file
// when missing. A second Acquire on the same path from any process (or
// thread) fails immediately.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(dirOf(path), 0o750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{file: file}, nil
}

// Close releases the lock and removes the backing file when possible. A
// leftover file without a lock holder is harmless: Acquire never trusts it.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlock(l.file)
	err := l.file.Close()
	l.file = nil
	return err
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if os.IsPathSeparator(path[i]) {
			return path[:i]
		}
	}
	return "."
}
