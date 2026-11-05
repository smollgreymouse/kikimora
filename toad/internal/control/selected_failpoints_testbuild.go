//go:build kikimora_test_failpoints

package control

import (
	"net/netip"
	"os"
)

// processSelectedFailpoint implements the process-crash seam for acceptance
// runs (08D1 §22.2): on an exact match of the stable boundary name it exits
// immediately with code 86, no deferred cleanup, preserving whatever
// checkpoint/kernel state exists at that point.
//
// KK_SELECTED_FAILPOINT=<stable name>
// KK_SELECTED_FAILPOINT_ROLE=<optional role>
// KK_SELECTED_FAILPOINT_PREFIX=<optional canonical prefix>
func processSelectedFailpoint(point, role string, prefix netip.Prefix) {
	if os.Getenv("KK_SELECTED_FAILPOINT") != point {
		return
	}
	if want := os.Getenv("KK_SELECTED_FAILPOINT_ROLE"); want != "" && want != role {
		return
	}
	if want := os.Getenv("KK_SELECTED_FAILPOINT_PREFIX"); want != "" && want != prefix.String() {
		return
	}
	os.Exit(86)
}
