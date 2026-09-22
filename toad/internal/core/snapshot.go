package core

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

type Snapshot struct {
	Schema           int                    `json:"schema"`
	Revision         uint64                 `json:"revision"`
	CoreState        string                 `json:"core_state"`
	AggregateState   string                 `json:"aggregate_state"`
	ActiveProfile    string                 `json:"active_profile"`
	Underlay         netstate.Snapshot      `json:"underlay"`
	Roles            map[string]RoleRuntime `json:"roles"`
	LastChangeReason netstate.ChangeReason  `json:"last_change_reason,omitempty"`
	LastChangeAt     time.Time              `json:"last_change_at,omitempty"`
}
type Controller struct {
	mu            sync.Mutex
	roles         map[string]RoleRuntime
	specs         map[string]RoleSpec
	underlay      netstate.Snapshot
	revision      uint64
	activeProfile string
	changed       chan struct{}
	events        chan Event
	lastReason    netstate.ChangeReason
	lastChangeAt  time.Time
}

func NewController(specs []RoleSpec) *Controller {
	c := &Controller{roles: map[string]RoleRuntime{}, specs: map[string]RoleSpec{}, revision: 1, activeProfile: "default", changed: make(chan struct{}), events: make(chan Event, 64)}
	for _, s := range specs {
		c.specs[s.ID] = s
		c.roles[s.ID] = RoleRuntime{State: RoleStopped}
	}
	return c
}
func (c *Controller) Submit(ctx context.Context, e Event) error {
	select {
	case c.events <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Controller) Run(ctx context.Context) error {
	for {
		select {
		case e := <-c.events:
			c.apply(e)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (c *Controller) SetRoleDesired(_ context.Context, role string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	if r.Desired == enabled {
		return nil
	}
	r.Desired = enabled
	r.Operation++
	if enabled {
		r.State = RoleStarting
		r.Reason = "desired state enabled"
	} else {
		r.State = RoleStopped
		r.Reason = "desired state disabled"
	}
	c.roles[role] = r
	c.bump()
	return nil
}
func (c *Controller) SetUnderlay(next netstate.Snapshot, reason netstate.ChangeReason) {
	c.mu.Lock()
	defer c.mu.Unlock()

	initial := c.underlay.Epoch == 0
	if next.Epoch == 0 {
		merged, computedReason, changed := netstate.Compare(c.underlay, next)
		if !changed {
			return
		}
		next = merged
		if reason == "" {
			reason = computedReason
		}
	} else {
		// A nonzero epoch is already canonicalized by Manager/netstate.Compare.
		// Never renumber it here; reject stale or same-epoch different identity.
		if next.Epoch < c.underlay.Epoch {
			return
		}
		if next.Epoch == c.underlay.Epoch {
			if netstate.IdentityEqual(c.underlay, next) {
				c.underlay.ObservedAt = next.ObservedAt
			}
			return
		}
	}
	c.underlay = next
	available := next.IPv4 != nil || next.IPv6 != nil
	for id, role := range c.roles {
		if !role.Desired {
			continue
		}
		if !available {
			role.ValidatedEpoch = 0
			role.State = RoleWaitingForUnderlay
			role.Reason = "physical underlay unavailable"
		} else if !initial && role.State == RoleReady && role.ValidatedEpoch != next.Epoch {
			role.State = RoleRecovering
			role.ValidatedEpoch = 0
			role.Reason = string(reason)
		}
		c.roles[id] = role
	}
	c.bumpReason(reason)
}

// ObserveToad is the single product-state ingress for revisioned Toad
// snapshots. The control adapter may still read state.json during migration,
// but it must feed the same observation here.
func (c *Controller) ObserveToad(role string, snapshot toadctl.Snapshot) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observeToadLocked(role, snapshot)
}

func (c *Controller) observeToadLocked(role string, snapshot toadctl.Snapshot) bool {
	r, ok := c.roles[role]
	if !ok || (r.ToadGeneration != 0 && r.ToadGeneration != snapshot.Generation) {
		return false
	}
	if r.ToadGeneration == snapshot.Generation && reflect.DeepEqual(r.Toad, snapshot) {
		return true
	}
	r.ToadGeneration = snapshot.Generation
	r.Toad = snapshot

	if !snapshot.RouteReady {
		r.ValidatedEpoch = 0
		if r.Desired {
			if snapshot.State == "failed" {
				r.State = RoleFailed
				r.LastError = snapshot.Reason
			} else if snapshot.State == "stopped" {
				r.State = RoleStopped
			} else {
				r.State = RoleValidating
				r.Reason = "managed route target requires validation"
			}
		}
	} else {
		switch snapshot.State {
		case "failed":
			r.State = RoleFailed
			r.ValidatedEpoch = 0
			r.LastError = snapshot.Reason
		case "stopped":
			r.State = RoleStopped
			r.ValidatedEpoch = 0
		case "recovering", "quiesced":
			r.State = RoleRecovering
			r.ValidatedEpoch = 0
		case "ready", "online":
			// Positive transport observation is not validation authority.
			// Keep an already-current Ready role stable; otherwise require an
			// explicit epoch/generation-bound validation.
			if r.Desired && r.ValidatedEpoch != c.underlay.Epoch && r.State != RoleRecovering {
				r.State = RoleValidating
				r.Reason = "managed route target ready; validation required"
			}
		}
	}
	if r.Reason == "" {
		r.Reason = snapshot.Reason
	}
	c.roles[role] = r
	c.bump()
	return true
}
func (c *Controller) Snapshot() Snapshot { c.mu.Lock(); defer c.mu.Unlock(); return c.snapshotLocked() }
func (c *Controller) WaitForRevision(ctx context.Context, rev uint64) (Snapshot, error) {
	for {
		c.mu.Lock()
		if c.revision > rev {
			s := c.snapshotLocked()
			c.mu.Unlock()
			return s, nil
		}
		ch := c.changed
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
}
func (c *Controller) Complete(role string, op, epoch uint64, result OperationResult) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok || r.Operation != op || c.underlay.Epoch != epoch {
		return false
	}
	if result.Err != nil {
		r.State = RoleFailed
		r.LastError = result.Err.Error()
		r.Reason = result.Reason
	} else {
		r.State = RoleReady
		r.ValidatedEpoch = epoch
		r.LastError = ""
		r.Reason = result.Reason
	}
	c.roles[role] = r
	c.bump()
	return true
}
func (c *Controller) apply(e Event) {
	switch v := e.(type) {
	case UnderlayChanged:
		c.SetUnderlay(v.New, v.Reason)
	case ResumeValidation:
		c.mu.Lock()
		for id, r := range c.roles {
			if r.Desired && r.State == RoleReady {
				r.ValidatedEpoch = 0
				r.State = RoleValidating
				r.Reason = "resume validation requested"
				c.roles[id] = r
			}
		}
		c.bump()
		c.mu.Unlock()
	case DesiredRoleChanged:
		_ = c.SetRoleDesired(context.Background(), v.Role, v.Enabled)
	case ToadStateChanged:
		c.mu.Lock()
		_ = c.observeToadLocked(v.Role, v.Snapshot)
		c.mu.Unlock()
	case OperationCompleted:
		c.Complete(v.Role, v.Operation, v.Epoch, v.Result)
	}
}
func (c *Controller) snapshotLocked() Snapshot {
	roles := make(map[string]RoleRuntime, len(c.roles))
	for id, r := range c.roles {
		roles[id] = r
	}
	a := "Stopped"
	any := false
	ready := true
	readyCount := 0
	waiting := false
	recovering := false
	failed := false
	for _, r := range roles {
		if r.Desired {
			any = true
		}
		if r.Desired && r.State != RoleReady {
			ready = false
		}
		if r.Desired && r.State == RoleReady {
			readyCount++
		}
		waiting = waiting || r.State == RoleWaitingForUnderlay
		recovering = recovering || r.State == RoleRecovering
		failed = failed || r.State == RoleFailed
		if r.State != RoleStopped && r.State != RoleFailed {
			a = "Connecting"
		}
		if r.State == RoleRecovering {
			a = "Recovering"
		}
	}
	if any && ready {
		a = "Ready"
	} else if recovering {
		a = "Recovering"
	} else if waiting {
		a = "WaitingForUnderlay"
	} else if readyCount > 0 {
		a = "PartiallyReady"
	} else if failed {
		a = "Failed"
	}
	return Snapshot{Schema: 2, Revision: c.revision, CoreState: "Ready", AggregateState: a, ActiveProfile: c.activeProfile, Underlay: c.underlay, Roles: roles, LastChangeReason: c.lastReason, LastChangeAt: c.lastChangeAt}
}
func (c *Controller) bump() { c.revision++; close(c.changed); c.changed = make(chan struct{}) }
func (c *Controller) bumpReason(reason netstate.ChangeReason) {
	c.lastReason = reason
	c.lastChangeAt = time.Now().UTC()
	c.bump()
}
