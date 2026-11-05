package routing

import "testing"

// 08D1 stage skeletons. Each test is skipped until its owning stage lands;
// the skip message names that stage. Remove the skip at the start of the
// owning stage and drive the test to green there.

func TestSchema2CheckpointRoundTripSkeleton(t *testing.T) {
	t.Skip("stage K11: implement schema2 exact selected+park checkpoint round-trip")
}

func TestSchema2RejectsSchema1UnderActiveMarkerSkeleton(t *testing.T) {
	t.Skip("stage K11: schema1 decodes only to an explicit unsupported error")
}

func TestSchema2ParkTransactionValidationSkeleton(t *testing.T) {
	t.Skip("stage K11: independent park state machine validation (§2.2)")
}

func TestSchema2RetireDesiredSemanticsSkeleton(t *testing.T) {
	t.Skip("stage K11: RetireDesired false/true semantics for selected and park")
}

func TestExactIdentityPlannerForeignRowSkeleton(t *testing.T) {
	t.Skip("stage K11: exact SelectedRouteIdentity planner with explicit foreign rows (§3)")
}
