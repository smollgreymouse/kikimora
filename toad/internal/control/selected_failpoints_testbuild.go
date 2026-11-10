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

// managedModeFailpoint is the process-crash seam for the schema2 mode
// transaction (Production Plan D10, packet §10.1): KK_MODE_FAILPOINT=
// <enable|disable>/<boundary> exits immediately with code 86, no deferred
// cleanup, preserving whatever journal/config/marker/checkpoint state exists
// at that boundary. The boundaries, in transaction order:
//
//	before_journal          nothing durable recorded yet
//	after_prepared          journal prepared, nothing else written
//	after_config_write      raw config bytes written, journal still prepared
//	after_config_written    journal advanced past the config write
//	after_marker_operation  raw marker write/remove (+ drain) done
//	after_marker_written    journal advanced past the marker operation
//	after_apply             in-memory apply done (durable = marker_written)
//	after_generation_commit committed counter moved, journal still present
//	before_journal_delete   the verify-and-clean state of a committed target
func managedModeFailpoint(kind, point string) {
	if os.Getenv("KK_MODE_FAILPOINT") == kind+"/"+point {
		os.Exit(86)
	}
}
