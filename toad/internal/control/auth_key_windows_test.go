//go:build windows

package control

import (
	"golang.org/x/sys/windows"
)

func getNamedSecurityInfoForTest(path string) (*windows.SECURITY_DESCRIPTOR, error) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	return sd, err
}
