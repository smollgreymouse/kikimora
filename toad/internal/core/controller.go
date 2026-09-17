package core

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// ControllerAPI is the narrow contract consumed by the local API adapter.
type ControllerAPI interface {
	SetRoleDesired(context.Context, string, bool) error
	Snapshot() Snapshot
	WaitForRevision(context.Context, uint64) (Snapshot, error)
}

// Role returns a copy of one product role for a recovery coordinator.
func (c *Controller) Role(role string) (RoleRuntime, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	return r, ok
}

// SetRecovery records progress produced by a driver without exposing the
// controller's mutable maps to adapters.
func (c *Controller) SetRecovery(role string, recovery RecoveryState, state RoleState, reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok {
		return false
	}
	r.Recovery = recovery
	r.State = state
	r.Reason = reason
	c.roles[role] = r
	c.bump()
	return true
}

// CompleteValidation commits the result of a validation requested by the
// controller. Epoch matching prevents a late response from an older underlay
// from restoring a role that is already stale.
func (c *Controller) CompleteValidation(role string, epoch uint64, healthy bool, reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok || !r.Desired || c.underlay.Epoch != epoch {
		return false
	}
	if healthy {
		r.State = RoleReady
		r.ValidatedEpoch = epoch
		r.LastError = ""
		if reason == "" {
			reason = "validation complete"
		}
	} else {
		r.State = RoleFailed
		r.ValidatedEpoch = 0
		r.LastError = reason
		if reason == "" {
			reason = "validation failed"
		}
	}
	r.Reason = reason
	c.roles[role] = r
	c.bump()
	return true
}

// RequestResumeValidation applies the resume invalidation synchronously. The
// sleep watcher uses this boundary so a validation cannot race the queued
// event and accidentally restore Ready before it is invalidated.
func (c *Controller) RequestResumeValidation() {
	c.apply(ResumeValidation{})
}

// MarkRecovering moves a desired role into the recovery state before an
// automatic resume recovery starts.
func (c *Controller) MarkRecovering(role, reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok || !r.Desired {
		return false
	}
	r.State = RoleRecovering
	r.ValidatedEpoch = 0
	r.Reason = reason
	c.roles[role] = r
	c.bump()
	return true
}

// BeginToadGeneration invalidates observations from a previous process
// instance before its replacement can publish a snapshot.
func (c *Controller) BeginToadGeneration(role string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok {
		return false
	}
	r.ToadGeneration = 0
	r.Toad = toadctl.Snapshot{}
	if r.Desired {
		r.State = RoleStarting
	}
	c.roles[role] = r
	c.bump()
	return true
}

// UpdateRoleResources keeps the API projection synchronized with the concrete
// endpoint, parking, publication and validation drivers.
func (c *Controller) UpdateRoleResources(role string, endpointState endpoint.State, publication leshy.PublicationState, parkingState parking.State, validation toadctl.ValidationResult) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.roles[role]
	if !ok {
		return false
	}
	r.Endpoint = endpointState
	r.Publication = publication
	r.Parking = parkingState
	r.Validation = validation
	if validation.State != "" || validation.Reason != "" || validation.Healthy {
		r.LastError = ""
		if !validation.Healthy && validation.Reason != "" {
			r.LastError = validation.Reason
		}
	}
	c.roles[role] = r
	c.bump()
	return true
}
