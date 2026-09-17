package core

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
	"testing"
	"time"
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
	c.Submit(ResumeValidation{})
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
	c.Submit(ResumeValidation{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go c.Run(ctx)
	time.Sleep(10 * time.Millisecond)
	if c.Snapshot().Roles["one"].ValidatedEpoch != 0 {
		t.Fatal("resume kept validation")
	}
}

func TestValidationCompletionRequiresCurrentEpoch(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	r := c.Snapshot().Roles["one"]
	if !c.Complete("one", r.Operation, 0, OperationResult{}) {
		t.Fatal("initial completion rejected")
	}
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	r = c.Snapshot().Roles["one"]
	if !c.Complete("one", r.Operation, c.Snapshot().Underlay.Epoch, OperationResult{}) {
		t.Fatal("underlay completion rejected")
	}
	c.Submit(ResumeValidation{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go c.Run(ctx)
	time.Sleep(10 * time.Millisecond)
	if c.CompleteValidation("one", 0, true, "late") {
		t.Fatal("late validation committed")
	}
	if !c.CompleteValidation("one", c.Snapshot().Underlay.Epoch, true, "resume validation complete") {
		t.Fatal("current validation rejected")
	}
	if got := c.Snapshot().Roles["one"]; got.State != RoleReady || got.ValidatedEpoch != 1 {
		t.Fatalf("validation did not restore readiness: %#v", got)
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
