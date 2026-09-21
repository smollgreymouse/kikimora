package fsutil

import (
	"os"
	"runtime"
)

// SyncDir synchronises the directory metadata for path. On Windows the sync is
// best-effort because the platform does not support directory fsync the same
// way POSIX does. Linux durability remains strict.
func SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	return nil
}
