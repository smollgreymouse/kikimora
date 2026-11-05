package routing

import (
	"net/netip"
	"testing"
)

func parkRow(identity SelectedParkIdentity) SelectedKernelRow {
	return SelectedKernelRow{Kind: SelectedKernelPark, Park: identity}
}

func routeRow(identity SelectedRouteIdentity) SelectedKernelRow {
	return SelectedKernelRow{Kind: SelectedKernelRoute, Route: identity}
}

func foreignRow(description string) SelectedKernelRow {
	return SelectedKernelRow{Kind: SelectedKernelForeign, Description: description}
}

func selectedRecord(prefix netip.Prefix, ifindex int, state SelectedRouteState) *SelectedRoute {
	desired := testIdentity(ifindex)
	return &SelectedRoute{
		Role: "primary", Zone: "corp", Prefix: prefix,
		Source: SelectedDNS, ToadGeneration: 1, RouteGeneration: 1,
		Desired: desired, State: state,
	}
}

func TestPlanForeignRowIsUnconditionalConflict(t *testing.T) {
	record := selectedRecord(testPrefix(t, "203.0.113.10/32"), 5, SelectedApplying)
	// Even with an exact Desired row present, one foreign row conflicts.
	rows := []SelectedKernelRow{routeRow(record.Desired), foreignRow("proto 2 row")}
	if _, err := PlanSelectedRoute(rows, record); err == nil {
		t.Fatal("foreign row must be an unconditional conflict")
	}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{foreignRow("anyone's row")}, nil); err == nil {
		t.Fatal("nil record with rows must conflict")
	}
}

func TestPlanNilRecordConflictsWithExactDesired(t *testing.T) {
	rows := []SelectedKernelRow{routeRow(testIdentity(5))}
	if _, err := PlanSelectedRoute(rows, nil); err == nil {
		t.Fatal("nil record + exact Desired must conflict")
	}
	if action, err := PlanSelectedRoute(nil, nil); err != nil || action != SelectedActionAdd {
		t.Fatalf("empty kernel with nil record adds: %v %v", action, err)
	}
}

func TestPlanPendingNilOwnedNeverAdopts(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedPending)
	rows := []SelectedKernelRow{routeRow(record.Desired)}
	if _, err := PlanSelectedRoute(rows, record); err == nil {
		t.Fatal("pending nil owned + exact Desired must conflict")
	}
	// An empty kernel adds: preflight authorizes the mutation first.
	if action, err := PlanSelectedRoute(nil, record); err != nil || action != SelectedActionAdd {
		t.Fatalf("pending with empty kernel adds: %v %v", action, err)
	}
}

func TestPlanApplyingNilOwnedAdoptsExactDesired(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedApplying)
	rows := []SelectedKernelRow{routeRow(record.Desired)}
	action, err := PlanSelectedRoute(rows, record)
	if err != nil || action != SelectedActionNone {
		t.Fatalf("applying may adopt exactly Desired: %v %v", action, err)
	}
	rows = []SelectedKernelRow{routeRow(record.Desired), routeRow(testIdentity(6))}
	if _, err := PlanSelectedRoute(rows, record); err == nil {
		t.Fatal("a second distinct row stays foreign")
	}
}

func TestPlanActiveExactIsNone(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedActive)
	record.Owned = idPtr(record.Desired)
	rows := []SelectedKernelRow{routeRow(record.Desired)}
	action, err := PlanSelectedRoute(rows, record)
	if err != nil || action != SelectedActionNone {
		t.Fatalf("active exact row is none: %v %v", action, err)
	}
}

func TestPlanOldOwnedReplaces(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 9, SelectedApplying)
	record.Owned = idPtr(testIdentity(5))
	rows := []SelectedKernelRow{routeRow(testIdentity(5))}
	action, err := PlanSelectedRoute(rows, record)
	if err != nil || action != SelectedActionReplace {
		t.Fatalf("previous owned only replaces: %v %v", action, err)
	}
	// Desired + distinct previous Owned: replace to clean the stale row.
	rows = append(rows, routeRow(record.Desired))
	action, err = PlanSelectedRoute(rows, record)
	if err != nil || action != SelectedActionReplace {
		t.Fatalf("desired + stale owned replaces: %v %v", action, err)
	}
}

func TestPlanWrongIdentityFieldsConflict(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedApplying)
	for name, mutate := range map[string]func(*SelectedRouteIdentity){
		"gateway":  func(i *SelectedRouteIdentity) { i.NextHop = "198.51.100.1" },
		"protocol": func(i *SelectedRouteIdentity) { i.Protocol = 2 },
		"metric":   func(i *SelectedRouteIdentity) { i.Metric = 512 },
		"scope":    func(i *SelectedRouteIdentity) { i.Scope = ScopeUniverse },
		"luid":     func(i *SelectedRouteIdentity) { i.InterfaceLuid = 42 },
	} {
		row := routeRow(record.Desired)
		mutate(&row.Route)
		if _, err := PlanSelectedRoute([]SelectedKernelRow{row}, record); err == nil {
			t.Fatalf("wrong %s must conflict", name)
		}
	}
	// Same ifindex + managed protocol without an exact Owned is foreign.
	row := routeRow(record.Desired)
	row.Route.Metric = 999
	if _, err := PlanSelectedRoute([]SelectedKernelRow{row}, record); err == nil {
		t.Fatal("same ifindex/proto without exact owned conflicts")
	}
}

func TestPlanDuplicateDesiredConflicts(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedApplying)
	rows := []SelectedKernelRow{routeRow(record.Desired), routeRow(record.Desired)}
	if _, err := PlanSelectedRoute(rows, record); err == nil {
		t.Fatal("duplicate Desired rows conflict")
	}
}

func TestPlanParkRowRecognition(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	park := SelectedParkIdentity{
		Kind: SelectedParkUnreachable, Table: ManagedSelectedRouteTable,
		Protocol: 4, Metric: ManagedParkMetric,
	}

	// Park nil: every park row is foreign.
	record := selectedRecord(prefix, 5, SelectedApplying)
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park)}, record); err == nil {
		t.Fatal("park row without a park transaction conflicts")
	}

	// Park Pending + nil Owned + exact Park.Desired present: conflict (the
	// new mutation was not authorized).
	record.Park = &SelectedPark{State: SelectedParkPending, Desired: park}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park)}, record); err == nil {
		t.Fatal("park pending may not adopt its desired row")
	}

	// Park Pending + exact previous Owned only: allowed preflight state.
	previous := park
	previous.Metric = ManagedParkMetric // same shape; differentiate via NextHop is impossible for unreachable — use loopback kind
	_ = previous
	loopPrevious := SelectedParkIdentity{
		Kind: SelectedParkLoopback, Table: ManagedSelectedRouteTable,
		IfIndex: 1, NextHop: "127.0.0.1", Protocol: 3, Metric: ManagedParkMetric,
	}
	record.Park = &SelectedPark{State: SelectedParkPending, Desired: park, Owned: parkIdPtr(loopPrevious)}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(loopPrevious)}, record); err != nil {
		t.Fatalf("park pending + previous owned allowed: %v", err)
	}
	record.Park = &SelectedPark{State: SelectedParkPending, Desired: park, Owned: parkIdPtr(loopPrevious)}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(loopPrevious), parkRow(park)}, record); err == nil {
		t.Fatal("park pending + previous owned + distinct desired conflicts")
	}

	// Park Applying: exact Park.Desired is adoptable; with an empty selected
	// set the selected route plans Add and the park is adopted separately.
	record.Park = &SelectedPark{State: SelectedParkApplying, Desired: park}
	action, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park)}, record)
	if err != nil || action != SelectedActionAdd {
		t.Fatalf("park applying adoptable + selected adds: %v %v", action, err)
	}

	// Park Active + exact Park.Owned + the Desired selected row: none.
	record.Park = &SelectedPark{State: SelectedParkActive, Desired: park, Owned: parkIdPtr(park)}
	record.Owned = idPtr(record.Desired)
	action, err = PlanSelectedRoute([]SelectedKernelRow{parkRow(park), routeRow(record.Desired)}, record)
	if err != nil || action != SelectedActionNone {
		t.Fatalf("park active coexists with the selected route: %v %v", action, err)
	}

	// Park Active + a different park identity: conflict.
	other := park
	other.Protocol = 2
	record.Park = &SelectedPark{State: SelectedParkActive, Desired: park, Owned: parkIdPtr(park)}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(other)}, record); err == nil {
		t.Fatal("a different park identity conflicts")
	}

	// Park Retiring: exact Owned is a retirement candidate; Desired only
	// with RetireDesired.
	record.Owned = nil
	record.Park = &SelectedPark{State: SelectedParkRetiring, Desired: park, Owned: parkIdPtr(park)}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park)}, record); err != nil {
		t.Fatalf("park retiring recognizes owned: %v", err)
	}
	distinct := SelectedParkIdentity{
		Kind: SelectedParkLoopback, Table: ManagedSelectedRouteTable,
		IfIndex: 2, NextHop: "127.0.0.1", Protocol: 3, Metric: ManagedParkMetric,
	}
	record.Park = &SelectedPark{State: SelectedParkRetiring, Desired: distinct, Owned: parkIdPtr(park), RetireDesired: true}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park), parkRow(distinct)}, record); err != nil {
		t.Fatalf("park retiring with retire_desired recognizes both: %v", err)
	}
	record.Park = &SelectedPark{State: SelectedParkRetiring, Desired: distinct, Owned: parkIdPtr(park)}
	if _, err := PlanSelectedRoute([]SelectedKernelRow{parkRow(park), parkRow(distinct)}, record); err == nil {
		t.Fatal("park retiring without retire_desired does not recognize the desired row")
	}
}

func TestPlanRetiringRecordIsNotReconciled(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	record := selectedRecord(prefix, 5, SelectedRetiring)
	if _, err := PlanSelectedRoute(nil, record); err == nil {
		t.Fatal("retiring records are owned by retirement, not the planner")
	}
}
