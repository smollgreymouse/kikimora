package control

import "testing"

// 08D1 stage skeletons. Each test is skipped until its owning stage lands;
// the skip message names that stage. Remove the skip at the start of the
// owning stage and drive the test to green there.

func TestModeJournalCrashRecoverySkeleton(t *testing.T) {
	t.Skip("stage 1D/6.2: crash after every leshy-mode-transition.json boundary recovers to target mode (§31.1)")
}

func TestWriterFenceBlocksEnableWhileLeshyHoldsLockSkeleton(t *testing.T) {
	t.Skip("stage 1B: OS writer lease denies Enable/Disable while a Leshy process holds it (§31.2)")
}

func TestWriterFenceBlocksSecondLeshySkeleton(t *testing.T) {
	t.Skip("stage 1B: second Leshy on the same writer lock fails before DNS/routes (§31.2)")
}

func TestPrivilegedAuthDeniesUnauthenticatedMutationSkeleton(t *testing.T) {
	t.Skip("stage 1C: privileged route methods require the bearer credential with zero mutation on failure (§31.6)")
}

func TestCoreStateDirSingletonLockSkeleton(t *testing.T) {
	t.Skip("stage 1B: second core on one StateDir fails before socket/route mutation (§31.15)")
}

func TestProductionRecoverRoleWiresManagedSelectedRoutesSkeleton(t *testing.T) {
	t.Skip("stage K14: Manager.recoverRole engine carries ManagedSelectedRoutes and reaches the managed sequence (§0.8)")
}

func TestObservationSequenceSuppressesStaleFastPathSkeleton(t *testing.T) {
	t.Skip("stage 3B: no stale zero-I/O ACK once a native event reached the process but before debounce handling (§31.9)")
}

func TestServeWithReadyApplicationReadinessBarrierSkeleton(t *testing.T) {
	t.Skip("stage 1B: control listener reports readiness only after mode/marker/restore gates (§31.22)")
}
