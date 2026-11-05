//go:build windows

package fsutil

import (
	"golang.org/x/sys/windows"
)

// renameDurable performs the final same-volume replacement on Windows.
// Plain os.Rename is not accepted here (08D1 §31.7): the durable-replacement
// contract requires MoveFileExW with MOVEFILE_REPLACE_EXISTING |
// MOVEFILE_WRITE_THROUGH so an existing destination is replaced and the
// write reaches storage before the call returns.
func renameDurable(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
