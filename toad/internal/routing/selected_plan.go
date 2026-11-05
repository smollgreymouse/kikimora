package routing

import (
	"fmt"
	"net/netip"
)

// Exact selected-route reconciliation rules shared by every platform
// (08D1 §3). The rules are pure so both platforms enforce one contract and
// the ownership decisions are unit-testable without a kernel.
//
// Trust boundary (§31.24): a "foreign" row is any same-prefix row whose
// complete kernel-exposed identity does not match a checkpoint-authorized
// Desired/Owned/Park identity for the current transaction state. A
// byte-identical privileged replacement of an owned row is inside the
// trusted-host boundary and is out of scope for provenance detection.

// SelectedRouteDesired is the route intent the controller normalizes into an
// exact identity. A zero Gateway selects the device route through IfIndex.
type SelectedRouteDesired struct {
	Role    string
	Prefix  netip.Prefix
	IfIndex int
	Gateway netip.Addr
}

// SelectedKernelRowKind projects a same-prefix kernel row into exactly one
// of three classes. Any row that cannot be projected becomes Foreign and is
// never dropped (§3): the planner treats it as an unconditional conflict.
type SelectedKernelRowKind string

const (
	SelectedKernelRoute   SelectedKernelRowKind = "route"
	SelectedKernelPark    SelectedKernelRowKind = "park"
	SelectedKernelForeign SelectedKernelRowKind = "foreign"
)

// SelectedKernelRow is the neutral exact view of one same-prefix kernel row.
// For Kind==SelectedKernelRoute, Route validates against the prefix and Park
// is zero; for Kind==SelectedKernelPark the mirror holds. Description carries
// a stable diagnostic for foreign rows.
type SelectedKernelRow struct {
	Kind        SelectedKernelRowKind
	Route       SelectedRouteIdentity
	Park        SelectedParkIdentity
	Description string
}

// SelectedReconcileAction is the planner verdict for the selected route.
type SelectedReconcileAction int

const (
	SelectedActionNone SelectedReconcileAction = iota
	SelectedActionAdd
	SelectedActionReplace
)

// SelectedRowMatchesIdentity reports whether a projected row is exactly the
// given selected identity.
func SelectedRowMatchesIdentity(row SelectedKernelRow, identity SelectedRouteIdentity) bool {
	return row.Kind == SelectedKernelRoute && row.Route == identity
}

// SelectedParkMatchesIdentity reports whether a projected row is exactly the
// given park identity.
func SelectedParkMatchesIdentity(row SelectedKernelRow, identity SelectedParkIdentity) bool {
	return row.Kind == SelectedKernelPark && row.Park == identity
}

// PlanSelectedRoute classifies the same-prefix kernel rows for one registry
// record and returns the reconciliation verdict (§3 rules in order). A nil
// record means the prefix is not ours: every existing row is foreign.
func PlanSelectedRoute(rows []SelectedKernelRow, record *SelectedRoute) (SelectedReconcileAction, error) {
	if record == nil {
		if len(rows) == 0 {
			return SelectedActionAdd, nil
		}
		return 0, fmt.Errorf("prefix conflicts with %d foreign row(s): no registry record owns it", len(rows))
	}
	if record.State == SelectedRetiring {
		return 0, fmt.Errorf("selected route %s is retiring; retirement owns its cleanup", record.Prefix)
	}

	var desiredCount, ownedCount int
	for _, row := range rows {
		// Rule 0: any foreign row is an unconditional conflict.
		if row.Kind == SelectedKernelForeign {
			return 0, fmt.Errorf("prefix conflicts with a foreign row: %s", row.Description)
		}
		if row.Kind == SelectedKernelPark {
			// Park-row recognition is state-dependent and exact (§3): only
			// checkpoint-authorized park rows may coexist; they are excluded
			// from the selected-route classification and never satisfy the
			// selected Desired or count as selected Owned.
			if !parkRowAuthorized(row, record) {
				return 0, fmt.Errorf("prefix conflicts with an unauthorized park row: %s", row.Description)
			}
			continue
		}
		// Kind == route: classify as Desired exact, previous Owned exact, or
		// foreign.
		switch {
		case SelectedRowMatchesIdentity(row, record.Desired):
			desiredCount++
		case record.Owned != nil && SelectedRowMatchesIdentity(row, *record.Owned):
			ownedCount++
		default:
			return 0, fmt.Errorf("prefix conflicts with a foreign row: %s", row.Description)
		}
	}

	// Rule 1: more than one Desired-exact row is a conflict.
	if desiredCount > 1 {
		return 0, fmt.Errorf("prefix has %d identical desired rows", desiredCount)
	}
	// Rule 8/9: with no Owned proof, every existing selected row is foreign
	// except a durable Applying record may adopt exactly its Desired.
	if record.Owned == nil && ownedCount == 0 && desiredCount > 0 {
		if record.State != SelectedApplying {
			return 0, fmt.Errorf("prefix has an unauthorized existing row: %q state may not adopt it", record.State)
		}
	}
	// Rules 3-6.
	switch {
	case desiredCount == 1 && ownedCount == 0:
		return SelectedActionNone, nil
	case desiredCount == 0 && ownedCount == 0:
		return SelectedActionAdd, nil
	case desiredCount == 0 && ownedCount > 0:
		return SelectedActionReplace, nil
	default: // desiredCount >= 1 && ownedCount > 0: clean the stale Owned.
		return SelectedActionReplace, nil
	}
}

// parkRowAuthorized decides whether a kernel park row may coexist with the
// record under the persisted park transaction state (§3).
func parkRowAuthorized(row SelectedKernelRow, record *SelectedRoute) bool {
	park := record.Park
	if park == nil {
		// Rule: record.Park == nil — every park row is foreign.
		return false
	}
	switch park.State {
	case SelectedParkPending:
		// Exact previous Park.Owned is recognized (it was verified before
		// this Pending transaction). A distinct exact Park.Desired is
		// foreign because the new mutation was not authorized; with nil
		// Park.Owned every park row is foreign.
		return park.Owned != nil && SelectedParkMatchesIdentity(row, *park.Owned)
	case SelectedParkApplying:
		// A row equal to Park.Desired is adoptable; a row equal to previous
		// Park.Owned is recognized previous ownership; anything else is
		// foreign.
		return SelectedParkMatchesIdentity(row, park.Desired) ||
			(park.Owned != nil && SelectedParkMatchesIdentity(row, *park.Owned))
	case SelectedParkActive:
		// Only a row equal to Park.Owned (which checkpoint validation
		// requires to equal Park.Desired) is the authorized current park.
		return park.Owned != nil && SelectedParkMatchesIdentity(row, *park.Owned)
	case SelectedParkRetiring:
		// Exact Park.Owned is a retirement candidate; exact Park.Desired is
		// an additional retirement candidate only when RetireDesired=true.
		if park.Owned != nil && SelectedParkMatchesIdentity(row, *park.Owned) {
			return true
		}
		return park.RetireDesired && SelectedParkMatchesIdentity(row, park.Desired)
	default:
		return false
	}
}
