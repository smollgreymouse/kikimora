package routing

import (
	"net/netip"
	"strings"
	"testing"
)

func desiredFor(prefix string, ifindex int, gateway string, protocol int, metric uint32) SelectedRouteDesired {
	d := SelectedRouteDesired{
		Role:     "primary",
		Prefix:   netip.MustParsePrefix(prefix),
		IfIndex:  ifindex,
		Protocol: protocol,
		Metric:   metric,
	}
	if gateway != "" {
		d.Gateway = netip.MustParseAddr(gateway)
	}
	return d
}

func TestPlanSelectedRouteExactRowIsZeroMutation(t *testing.T) {
	desired := desiredFor("203.0.113.10/32", 7, "", 3, 0)
	rows := []SelectedKernelRow{{IfIndex: 7, Protocol: 3, Metric: 0}}
	action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 7})
	if err != nil {
		t.Fatal(err)
	}
	if action != SelectedReconcileNone {
		t.Fatalf("exact row must be a no-op, got %d", action)
	}
}

func TestPlanSelectedRouteGatewayIdentity(t *testing.T) {
	desired := desiredFor("203.0.113.10/32", 7, "10.55.0.254", 3, 0)
	rows := []SelectedKernelRow{{IfIndex: 7, Gateway: netip.MustParseAddr("10.55.0.254"), Protocol: 3, Metric: 0}}
	if action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 7}); err != nil || action != SelectedReconcileNone {
		t.Fatalf("exact gateway row must be a no-op: %d %v", action, err)
	}
	// A different gateway is not the desired row...
	rows[0].Gateway = netip.MustParseAddr("10.55.0.1")
	// ...but the same ifindex with an owned protocol is the previous owned
	// row, so replacing it is allowed.
	action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 7})
	if err != nil || action != SelectedReconcileReplace {
		t.Fatalf("an owned row with a moved gateway must be replaceable: %d %v", action, err)
	}
	// A device row where a gateway route is desired is likewise replaceable.
	rows[0].Gateway = netip.Addr{}
	action, err = PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 7})
	if err != nil || action != SelectedReconcileReplace {
		t.Fatalf("an owned device row must be replaceable: %d %v", action, err)
	}
}

func TestPlanSelectedRouteForeignRowConflicts(t *testing.T) {
	desired := desiredFor("203.0.113.10/32", 7, "", 3, 0)
	// A row through another interface is never ours.
	foreign := []SelectedKernelRow{{IfIndex: 2, Protocol: 2, Metric: 0}}
	_, err := PlanSelectedRoute(foreign, desired, &SelectedRoute{IfIndex: 7})
	if err == nil || !strings.Contains(err.Error(), "conflicts with a foreign row") {
		t.Fatalf("a foreign row must conflict: %v", err)
	}
	// A routing daemon's row on the same device (proto kernel) is foreign too.
	daemon := []SelectedKernelRow{{IfIndex: 7, Protocol: 2, Metric: 0}}
	if _, err := PlanSelectedRoute(daemon, desired, &SelectedRoute{IfIndex: 7}); err == nil {
		t.Fatal("a kernel-protocol row on the same device must not be replaced")
	}
	// nil ownership makes everything foreign.
	if _, err := PlanSelectedRoute(foreign, desired, nil); err == nil {
		t.Fatal("an unregistered destination must conflict with any row")
	}
}

func TestPlanSelectedRouteOwnedProtocols(t *testing.T) {
	desired := desiredFor("10.9.9.1/32", 5, "", 4, 100)
	for _, protocol := range []int{0, 3, 4} {
		rows := []SelectedKernelRow{{IfIndex: 5, Protocol: protocol, Metric: 0}}
		action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 5})
		if err != nil || action != SelectedReconcileReplace {
			t.Fatalf("protocol %d must be owned: %d %v", protocol, action, err)
		}
	}
	// The exact row (same protocol and metric) is still a no-op.
	rows := []SelectedKernelRow{{IfIndex: 5, Protocol: 4, Metric: 100}}
	if action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 5}); err != nil || action != SelectedReconcileNone {
		t.Fatalf("an exact owned row must be a no-op: %d %v", action, err)
	}
}

func TestPlanSelectedRouteMissingRowAdds(t *testing.T) {
	desired := desiredFor("2001:db8::10/128", 7, "10.55.0.254", 4, 0)
	action, err := PlanSelectedRoute(nil, desired, &SelectedRoute{IfIndex: 7})
	if err != nil || action != SelectedReconcileAdd {
		t.Fatalf("a missing route must be added: %d %v", action, err)
	}
}

func TestPlanSelectedRouteMetricIdentityMatters(t *testing.T) {
	desired := desiredFor("10.9.9.2/32", 5, "", 4, 100)
	rows := []SelectedKernelRow{{IfIndex: 5, Protocol: 4, Metric: 200}}
	action, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 5})
	if err != nil || action != SelectedReconcileReplace {
		t.Fatalf("a same-owner row with a different metric must be replaced: %d %v", action, err)
	}
	// ...but an exact metric/protocol match is a no-op even with a foreign
	// ifindex? No: ifindex is part of the identity, so a different ifindex
	// row is foreign regardless.
	rows[0] = SelectedKernelRow{IfIndex: 9, Protocol: 4, Metric: 100}
	if _, err := PlanSelectedRoute(rows, desired, &SelectedRoute{IfIndex: 5}); err == nil {
		t.Fatal("a row through another interface with matching metric is still foreign")
	}
}
