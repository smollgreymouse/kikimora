//go:build windows

package routes

import "testing"

// 08D1 stage skeletons. Each test is skipped until its owning stage lands;
// the skip message names that stage. Remove the skip at the start of the
// owning stage and drive the test to green there.

func TestSelectedExactReplacementNoBroadDeleteSkeleton(t *testing.T) {
	t.Skip("stage K15: exact-identity Windows selected replacement, no broad same-prefix deletion (§9)")
}

func TestSelectedParkLoopbackIdentitySkeleton(t *testing.T) {
	t.Skip("stage K15: exact persisted loopback park identity with InterfaceLuid (§9, §31.19)")
}

func TestWindowsEffectiveMetricRestorationProofSkeleton(t *testing.T) {
	t.Skip("stage 3A: effective route+interface metric proof before unpark (§31.25)")
}
