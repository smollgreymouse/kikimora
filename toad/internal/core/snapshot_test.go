package core

import (
	"context"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

func TestStaleCompletionCannotCommit(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	r := c.Snapshot().Roles["one"]
	if c.Complete("one", r.Operation+1, c.Snapshot().Underlay.Epoch, OperationResult{Reason: "late"}) {
		t.Fatal("stale operation committed")
	}
	if c.Complete("one", r.Operation, c.Snapshot().Underlay.Epoch, OperationResult{Reason: "validated"}) == false {
		t.Fatal("current operation rejected")
	}
	if c.Snapshot().Roles["one"].State != RoleReady {
		t.Fatal("role not ready")
	}
}

func TestRecoveryDecisionWaitsWithoutUnderlay(t *testing.T) {
	r := RoleRuntime{Desired: true, State: RoleReady, ValidatedEpoch: 4}
	if got := DecideRole(r, netstate.Snapshot{Epoch: 5}, toadctl.Capabilities{Validate: true}); got != ActionWaitForUnderlay {
		t.Fatalf("got %q", got)
	}
}

func TestRecoveryDecisionDoesNotRevalidateHealthyRole(t *testing.T) {
	r := RoleRuntime{Desired: true, State: RoleReady, ValidatedEpoch: 4}
	u := netstate.Snapshot{Epoch: 4, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}
	if got := DecideRole(r, u, toadctl.Capabilities{Validate: true}); got != ActionNone {
		t.Fatalf("healthy role scheduled unnecessary work: %q", got)
	}
}

func TestRecoveryDecisionUsesCapabilitiesForStaleUnderlay(t *testing.T) {
	u := netstate.Snapshot{Epoch: 5, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}
	r := RoleRuntime{Desired: true, State: RoleRecovering, ValidatedEpoch: 4}
	if got := DecideRole(r, u, toadctl.Capabilities{Validate: true, RestartTransportKeepingTUN: true}); got != ActionRestartTransport {
		t.Fatalf("stable-TUN capability was not preferred: %q", got)
	}
	if got := DecideRole(r, u, toadctl.Capabilities{Validate: true}); got != ActionRestartToad {
		t.Fatalf("full restart fallback was not selected: %q", got)
	}
	r.State = RoleValidating
	if got := DecideRole(r, u, toadctl.Capabilities{RestartTransportKeepingTUN: true}); got != ActionValidate {
		t.Fatalf("resume validation was not selected: %q", got)
	}
}

func TestUnderlayLossMovesEnabledRolesToWaiting(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	r := c.Snapshot().Roles["one"]
	_ = c.Complete("one", r.Operation, 0, OperationResult{})
	c.SetUnderlay(netstate.Snapshot{Epoch: 1, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	c.SetUnderlay(netstate.Snapshot{}, netstate.ChangeAvailability)
	role := c.Snapshot().Roles["one"]
	if role.State != RoleWaitingForUnderlay || role.ValidatedEpoch != 0 {
		t.Fatalf("underlay loss not fail-closed: %#v", role)
	}
}

func TestResumeMovesReadyRolesToValidation(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	r := c.Snapshot().Roles["one"]
	_ = c.Complete("one", r.Operation, 0, OperationResult{})
	_ = c.Submit(context.Background(), ResumeValidation{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.Snapshot().Roles["one"].State == RoleValidating {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("resume did not request validation: %#v", c.Snapshot().Roles["one"])
}
func TestResumeInvalidatesReadyRoles(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	r := c.Snapshot().Roles["one"]
	_ = c.Complete("one", r.Operation, 0, OperationResult{})
	_ = c.Submit(context.Background(), ResumeValidation{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go c.Run(ctx)
	time.Sleep(10 * time.Millisecond)
	if c.Snapshot().Roles["one"].ValidatedEpoch != 0 {
		t.Fatal("resume kept validation")
	}
}

func TestPassiveOnlineSnapshotDoesNotCertifyCurrentEpoch(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	if !c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "online", RouteReady: true}) {
		t.Fatal("Toad observation rejected")
	}
	got := c.Snapshot().Roles["one"]
	if got.State == RoleReady || got.ValidatedEpoch != 0 {
		t.Fatalf("passive snapshot certified readiness: %#v", got)
	}
}

func TestValidationTokenRejectsStaleEpochOperationAndGeneration(t *testing.T) {
	newReadyController := func(t *testing.T) (*Controller, ValidationToken) {
		t.Helper()
		c := NewController([]RoleSpec{{ID: "one"}})
		_ = c.SetRoleDesired(context.Background(), "one", true)
		c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
		if !c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "online", RouteReady: true}) {
			t.Fatal("Toad observation rejected")
		}
		token, ok := c.BeginValidation("one")
		if !ok {
			t.Fatal("current validation could not begin")
		}
		return c, token
	}
	result := toadctl.ValidationResult{Healthy: true, State: "ready", Reason: "structural validation complete"}

	t.Run("epoch", func(t *testing.T) {
		c, token := newReadyController(t)
		c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 3, Interface: "wlan0"}}, netstate.ChangeInterface)
		if c.CompleteValidation(token, result) {
			t.Fatal("old-epoch validation committed")
		}
	})

	t.Run("operation", func(t *testing.T) {
		c, token := newReadyController(t)
		_ = c.SetRoleDesired(context.Background(), "one", false)
		_ = c.SetRoleDesired(context.Background(), "one", true)
		if c.CompleteValidation(token, result) {
			t.Fatal("old-operation validation committed")
		}
	})

	t.Run("generation", func(t *testing.T) {
		c, token := newReadyController(t)
		c.BeginToadGeneration("one")
		if !c.ObserveToad("one", toadctl.Snapshot{Generation: 11, Revision: 1, State: "online", RouteReady: true}) {
			t.Fatal("replacement generation rejected")
		}
		if c.CompleteValidation(token, result) {
			t.Fatal("old-generation validation committed")
		}
	})
}

func TestValidationTokenCommitsCurrentResult(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	_ = c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "online", RouteReady: true})
	token, ok := c.BeginValidation("one")
	if !ok {
		t.Fatal("validation did not begin")
	}
	result := toadctl.ValidationResult{Healthy: true, State: "ready", Reason: "validation complete"}
	if !c.CompleteValidation(token, result) {
		t.Fatal("current validation rejected")
	}
	got := c.Snapshot().Roles["one"]
	if got.State != RoleReady || got.ValidatedEpoch != c.Snapshot().Underlay.Epoch {
		t.Fatalf("validation did not restore readiness: %#v", got)
	}
}

func TestRouteReadyLossInvalidatesValidationImmediately(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	_ = c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "online", RouteReady: true})
	token, _ := c.BeginValidation("one")
	_ = c.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"})
	if !c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 2, State: "online", RouteReady: false}) {
		t.Fatal("route-readiness loss rejected")
	}
	got := c.Snapshot().Roles["one"]
	if got.ValidatedEpoch != 0 || got.State == RoleReady {
		t.Fatalf("route-readiness loss kept validation: %#v", got)
	}
}

func TestAggregateStateDescribesWaitingAndPartialReadiness(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}, {ID: "two"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	first := c.Snapshot().Roles["one"]
	if !c.Complete("one", first.Operation, 0, OperationResult{}) {
		t.Fatal("first role completion rejected")
	}
	if got := c.Snapshot().AggregateState; got != "Ready" {
		t.Fatalf("disabled second role changed aggregate: %s", got)
	}
	if err := c.SetRoleDesired(context.Background(), "two", true); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().AggregateState; got != "PartiallyReady" {
		t.Fatalf("partial readiness not exposed: %s", got)
	}
	second := c.Snapshot().Roles["two"]
	if second.State != RoleStarting {
		t.Fatalf("second role did not enter starting: %s", second.State)
	}
}
