package fsutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteAtomicFile durably replaces path with data. The parent directory is
// created when missing, the payload lands in a same-directory temporary file
// that is fsynced, and the final replacement is atomic: readers observe only
// the complete old or the complete new file (08D1 §31.7). On Windows the
// replacement uses MoveFileExW with MOVEFILE_REPLACE_EXISTING |
// MOVEFILE_WRITE_THROUGH; on POSIX it is rename(2) followed by a parent
// directory sync. mode applies to the created file.
func WriteAtomicFile(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameDurable(name, path); err != nil {
		return err
	}
	return SyncDir(dir)
}

// WriteAtomicJSON marshals value and durably replaces path with the JSON
// payload followed by one newline (08D1 §31.7). A marshal failure leaves no
// trace on disk.
func WriteAtomicJSON(path string, value any, mode fs.FileMode) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	return WriteAtomicFile(path, append(b, '\n'), mode)
}

// RemoveDurable removes path and syncs its parent directory. A missing file
// is success (08D1 §31.7).
func RemoveDurable(path string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return SyncDir(filepath.Dir(path))
}
