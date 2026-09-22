package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type recordingDriver struct {
	steps   []RecoveryStep
	fail    RecoveryStep
	failErr error
}

func (d *recordingDriver) call(step RecoveryStep) error {
	d.steps = append(d.steps, step)
	if d.fail == step {
		if d.failErr != nil {
			return d.failErr
		}
		return errors.New("driver failure")
	}
	return nil
}
func (d *recordingDriver) ObserveRoutes(context.Context, string) error {
	return d.call(RecoveryObserveRoutes)
}
func (d *recordingDriver) Park(context.Context, string) error     { return d.call(RecoveryPark) }
func (d *recordingDriver) Withdraw(context.Context, string) error { return d.call(RecoveryWithdraw) }
func (d *recordingDriver) Quiesce(context.Context, string) error  { return d.call(RecoveryQuiesce) }
func (d *recordingDriver) ApplyEndpoint(context.Context, string) error {
	return d.call(RecoveryApplyEndpoint)
}
func (d *recordingDriver) Rebind(context.Context, string) error { return d.call(RecoveryRebind) }
func (d *recordingDriver) StartTransport(context.Context, string) error {
	return d.call(RecoveryStartTransport)
}
func (d *recordingDriver) Validate(context.Context, string) error { return d.call(RecoveryValidate) }
func (d *recordingDriver) Publish(context.Context, string) error  { return d.call(RecoveryPublish) }
func (d *recordingDriver) ResyncLeshy(context.Context, string) error {
	return d.call(RecoveryResyncLeshy)
}
func (d *recordingDriver) ObserveRestoration(context.Context, string) error {
	return d.call(RecoveryObserveRestore)
}

func TestEngineRunsDriverInFailClosedOrder(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	d := &recordingDriver{}
	if err := (Engine{Controller: c, Driver: d}).Recover(context.Background(), "one", 2, 4); err != nil {
		t.Fatal(err)
	}
	if len(d.steps) != len(RecoverySequence()) || c.Snapshot().Roles["one"].State != RoleReady {
		t.Fatalf("recovery was not committed: steps=%v state=%#v", d.steps, c.Snapshot().Roles["one"])
	}
}

func TestEngineLeavesFailureStateAtFailedStep(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	d := &recordingDriver{fail: RecoveryPublish}
	if err := (Engine{Controller: c, Driver: d}).Recover(context.Background(), "one", 2, 4); err == nil {
		t.Fatal("driver failure was swallowed")
	}
	role := c.Snapshot().Roles["one"]
	if role.State != RoleFailed || role.Recovery.Step != RecoveryPublish {
		t.Fatalf("failed step was not exposed: %#v", role)
	}
}

func TestEngineKeepsRoleRecoveringWhileRoutesRemainParked(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	d := &recordingDriver{fail: RecoveryObserveRestore, failErr: parking.ErrRoutesStillParked}
	err := (Engine{Controller: c, Driver: d}).Recover(context.Background(), "one", 2, 1)
	if !errors.Is(err, parking.ErrRoutesStillParked) {
		t.Fatalf("Recover error = %v, want ErrRoutesStillParked", err)
	}
	role := c.Snapshot().Roles["one"]
	if role.State != RoleRecovering || role.Recovery.Step != RecoveryObserveRestore {
		t.Fatalf("parked routes did not keep role recovering: %#v", role)
	}
}

func TestEngineUsesStableTunnelRestartCapability(t *testing.T) {
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	c.SetUnderlay(netstate.Snapshot{Epoch: 1, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	c.ObserveToad("one", toadctl.Snapshot{
		Generation: 1,
		Revision:   1,
		State:      "online",
		RouteReady: true,
		Capabilities: toadctl.Capabilities{
			Validate:                   true,
			RestartTransportKeepingTUN: true,
		},
	})
	token, ok := c.BeginValidation("one")
	if !ok {
		t.Fatal("current validation did not begin")
	}
	if !c.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"}) {
		t.Fatal("current validation did not commit")
	}
	c.SetUnderlay(netstate.Snapshot{Epoch: 2, IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth1"}}, netstate.ChangeInterface)
	d := &recordingDriver{}
	if err := (Engine{Controller: c, Driver: d}).Recover(context.Background(), "one", 2, 2); err != nil {
		t.Fatal(err)
	}
	want := RecoverySequenceForAction(ActionRestartTransport)
	if !reflect.DeepEqual(d.steps, want) {
		t.Fatalf("stable-TUN capability did not select minimal recovery: got=%v want=%v", d.steps, want)
	}
}
