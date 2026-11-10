//go:build windows

package control

import (
	"testing"

	"golang.org/x/sys/windows"
)

// blockFileRemoval makes the durable removal of path fail while the file
// stays readable: a handle opened WITHOUT FILE_SHARE_DELETE makes every
// other process's DeleteFile fail until it closes (P0-D1 failure seam).
func blockFileRemoval(t *testing.T, path string) {
	t.Helper()
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		t.Fatalf("block the marker removal: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
}
