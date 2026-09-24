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

func TestRecoveryDecisionStructuralRouteLossOverridesRebind(t *testing.T) {
	u := netstate.Snapshot{Epoch: 5, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}
	r := RoleRuntime{
		Desired:        true,
		State:          RoleRecovering,
		ValidatedEpoch: 0,
		ToadGeneration: 10,
		Toad: toadctl.Snapshot{
			Generation: 10,
			RouteReady: false,
		},
	}

	if got := DecideRole(r, u, toadctl.Capabilities{Rebind: true, Validate: true}); got != ActionRestartToad {
		t.Fatalf("structural route loss incorrectly selected rebind: %q", got)
	}
	if got := DecideRole(r, u, toadctl.Capabilities{Rebind: true, RestartTransportKeepingTUN: true, Validate: true}); got != ActionRestartTransport {
		t.Fatalf("stable-TUN structural recovery not preferred: %q", got)
	}

	r.Toad.RouteReady = true
	if got := DecideRole(r, u, toadctl.Capabilities{Rebind: true, Validate: true}); got != ActionRebind {
		t.Fatalf("healthy route target under stale underlay should rebind: %q", got)
	}
}

func TestInitialUnderlayKeepsStartingRoleOutOfRecovery(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().Roles["one"].State; got != RoleStarting {
		t.Fatalf("role did not start in starting state: %s", got)
	}

	c.SetUnderlay(netstate.Snapshot{
		IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"},
	}, netstate.ChangeInitial)

	snapshot := c.Snapshot()
	role := snapshot.Roles["one"]
	if snapshot.Underlay.Epoch != 1 {
		t.Fatalf("initial zero underlay epoch = %d, want 1", snapshot.Underlay.Epoch)
	}
	if role.State != RoleStarting || role.ValidatedEpoch != 0 {
		t.Fatalf("initial underlay was treated as recovery: %#v", role)
	}
}

func TestControllerPreservesCanonicalNonzeroUnderlayEpoch(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	c.SetUnderlay(netstate.Snapshot{
		Epoch: 7,
		IPv4:  &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"},
	}, netstate.ChangeInitial)
	if got := c.Snapshot().Underlay.Epoch; got != 7 {
		t.Fatalf("controller renumbered canonical epoch: got=%d want=7", got)
	}

	c.SetUnderlay(netstate.Snapshot{
		Epoch: 8,
		IPv4:  &netstate.Path{Family: 4, IfIndex: 3, Interface: "wlan0"},
	}, netstate.ChangeInterface)
	if got := c.Snapshot().Underlay.Epoch; got != 8 {
		t.Fatalf("controller renumbered next canonical epoch: got=%d want=8", got)
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
	pending := toadctl.ValidationResult{Healthy: false, State: "degraded", Reason: "protocol session not healthy yet"}

	t.Run("epoch", func(t *testing.T) {
		c, token := newReadyController(t)
		c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 3, Interface: "wlan0"}}, netstate.ChangeInterface)
		if c.CompleteValidation(token, result) {
			t.Fatal("old-epoch validation committed")
		}
		if c.CompleteValidationPending(token, pending) {
			t.Fatal("old-epoch pending validation committed")
		}
	})

	t.Run("operation", func(t *testing.T) {
		c, token := newReadyController(t)
		_ = c.SetRoleDesired(context.Background(), "one", false)
		_ = c.SetRoleDesired(context.Background(), "one", true)
		if c.CompleteValidation(token, result) {
			t.Fatal("old-operation validation committed")
		}
		if c.CompleteValidationPending(token, pending) {
			t.Fatal("old-operation pending validation committed")
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
		if c.CompleteValidationPending(token, pending) {
			t.Fatal("old-generation pending validation committed")
		}
	})
}

func TestValidationPendingCommitsRecoveringCurrentResult(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	_ = c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "online", RouteReady: true})
	token, ok := c.BeginValidation("one")
	if !ok {
		t.Fatal("validation did not begin")
	}
	result := toadctl.ValidationResult{Healthy: false, State: "degraded", Reason: "protocol session not healthy yet"}
	if !c.CompleteValidationPending(token, result) {
		t.Fatal("current pending validation rejected")
	}
	got := c.Snapshot().Roles["one"]
	if got.State != RoleRecovering || got.ValidatedEpoch != 0 {
		t.Fatalf("pending validation did not remain recoverable: %#v", got)
	}
	if got.Validation.Healthy || got.Validation.Reason != result.Reason || got.LastError != result.Reason {
		t.Fatalf("pending validation evidence not preserved: %#v", got)
	}
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

func TestPendingValidationStaysRecoverableAndRejectsStaleToken(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	_ = c.SetRoleDesired(context.Background(), "one", true)
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	_ = c.ObserveToad("one", toadctl.Snapshot{Generation: 10, Revision: 1, State: "connecting", RouteReady: true})

	token, ok := c.BeginValidation("one")
	if !ok {
		t.Fatal("validation did not begin")
	}
	pending := toadctl.ValidationResult{Healthy: false, State: "degraded", Reason: "protocol session not healthy yet"}
	if !c.CompleteValidationPending(token, pending) {
		t.Fatal("current pending validation rejected")
	}
	got := c.Snapshot().Roles["one"]
	if got.State != RoleRecovering || got.ValidatedEpoch != 0 || got.Validation.Healthy {
		t.Fatalf("pending validation became terminal/current: %#v", got)
	}

	stale := token
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 3, Interface: "wlan0"}}, netstate.ChangeInterface)
	if c.CompleteValidationPending(stale, pending) {
		t.Fatal("old-epoch pending validation committed")
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

func TestSemanticEventQueueReportsBackpressureInsteadOfDropping(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	for i := 0; i < cap(c.events); i++ {
		if err := c.Submit(context.Background(), DesiredRoleChanged{Role: "one", Enabled: i%2 == 0}); err != nil {
			t.Fatalf("fill event %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Submit(ctx, DesiredRoleChanged{Role: "one", Enabled: true}); err == nil {
		t.Fatal("full semantic queue silently accepted/dropped event")
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
