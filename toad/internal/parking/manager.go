package parking

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

var ErrRoutesStillParked = errors.New("selected routes still parked")

type State struct {
	Active   bool           `json:"active"`
	Count    int            `json:"count"`
	Prefixes []netip.Prefix `json:"prefixes,omitempty"`
}
type DesiredState struct {
	Role     string
	Prefixes []netip.Prefix
}
type Manager struct {
	mu     sync.Mutex
	Routes routing.Executor
	States map[string]State
}

// RestoreCheckpoint reconstructs only parks still present in the routing
// executor. A stale checkpoint is never treated as proof that a route exists.
func (m *Manager) RestoreCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	if m.Routes == nil {
		return fmt.Errorf("parking route executor is nil")
	}
	kernel, err := m.Routes.Snapshot(ctx)
	if err != nil {
		return err
	}
	verified := make([]netip.Prefix, 0, len(checkpoint.Parked))
	for _, parked := range checkpoint.Parked {
		for _, route := range kernel.Routes {
			if (route.Kind == "park" || route.Kind == "unreachable") && route.Prefix == parked.Prefix.String() && route.Metric >= 42760 {
				verified = append(verified, parked.Prefix)
				break
			}
		}
	}
	m.mu.Lock()
	if len(verified) == 0 {
		delete(m.States, checkpoint.Role)
	} else {
		m.States[checkpoint.Role] = State{Active: true, Count: len(verified), Prefixes: verified}
	}
	m.mu.Unlock()
	return nil
}

func NewManager(routes routing.Executor) *Manager {
	return &Manager{Routes: routes, States: make(map[string]State)}
}
func (m *Manager) PrepareWithdrawal(ctx context.Context, role string, prefixes []netip.Prefix) error {
	if role == "" {
		return nil
	}
	valid := make([]netip.Prefix, 0, len(prefixes))
	ops := make([]routing.Operation, 0, len(prefixes))
	for _, prefix := range prefixes {
		if !routing.IsHostPrefix(prefix) {
			continue
		}
		valid = append(valid, prefix)
		ops = append(ops, routing.Operation{Kind: "park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: staticProtocol})
	}
	if len(ops) == 0 {
		m.mu.Lock()
		m.States[role] = State{}
		m.mu.Unlock()
		return nil
	}
	if m.Routes == nil {
		return context.Canceled
	}
	if err := m.Routes.Apply(ctx, routing.Transaction{Role: role, Operations: ops}); err != nil {
		return err
	}
	m.mu.Lock()
	m.States[role] = State{Active: true, Count: len(valid), Prefixes: append([]netip.Prefix(nil), valid...)}
	m.mu.Unlock()
	return nil
}

// PrepareOwnedWithdrawal parks only routes whose ownership was established
// outside the kernel read-back path. The kernel snapshot is used solely to
// verify that each owned route still exists with the expected interface,
// host-prefix and protocol before installing fail-closed protection.
func (m *Manager) PrepareOwnedWithdrawal(ctx context.Context, role string, owned []routing.SelectedRouteOwner) error {
	if m.Routes == nil {
		return fmt.Errorf("parking route executor is nil")
	}
	kernel, err := m.Routes.Snapshot(ctx)
	if err != nil {
		return err
	}
	prefixes := make([]netip.Prefix, 0, len(owned))
	seen := make(map[netip.Prefix]bool)
	for _, owner := range owned {
		if owner.Role != role || owner.IfIndex <= 0 || !routing.IsHostPrefix(owner.Prefix) || seen[owner.Prefix] {
			continue
		}
		verified := false
		for _, route := range kernel.Routes {
			if route.Kind == "route" &&
				route.Table != 51890 &&
				route.IfIndex == owner.IfIndex &&
				route.Protocol == staticProtocol &&
				route.Prefix == owner.Prefix.String() {
				verified = true
				break
			}
		}
		if verified {
			seen[owner.Prefix] = true
			prefixes = append(prefixes, owner.Prefix)
		}
	}
	return m.PrepareWithdrawal(ctx, role, prefixes)
}
func (m *Manager) ObserveRestoration(ctx context.Context, role string, prefix netip.Prefix) bool {
	m.mu.Lock()
	s := m.States[role]
	found := false
	remaining := s.Prefixes[:0]
	for _, p := range s.Prefixes {
		if p == prefix {
			found = true
			continue
		}
		remaining = append(remaining, p)
	}
	if !found {
		m.mu.Unlock()
		return false
	}
	s.Prefixes = append([]netip.Prefix(nil), remaining...)
	s.Count = len(s.Prefixes)
	s.Active = s.Count != 0
	m.States[role] = s
	m.mu.Unlock()
	if m.Routes != nil {
		_ = m.Routes.Apply(ctx, routing.Transaction{Role: role, Operations: []routing.Operation{{Kind: "delete-park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: staticProtocol}}})
	}
	return true
}

// ObserveRestorationFromKernel releases a park only after read-back confirms
// that a real non-endpoint route for the same destination wins over metric
// 42760. Merely recreating the tunnel interface is insufficient.
func (m *Manager) ObserveRestorationFromKernel(ctx context.Context, role string) (int, error) {
	if m.Routes == nil {
		return 0, fmt.Errorf("parking route executor is nil")
	}
	kernel, err := m.Routes.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	state := m.Snapshot(role)
	released := 0
	for _, prefix := range state.Prefixes {
		winning := false
		for _, route := range kernel.Routes {
			if route.Kind == "route" && route.Table != 51890 && route.Prefix == prefix.String() && route.Metric < 42760 {
				winning = true
				break
			}
		}
		if winning && m.ObserveRestoration(ctx, role, prefix) {
			released++
		}
	}
	return released, nil
}

// Clear removes all known parks for a role during an explicit product stop.
func (m *Manager) Clear(ctx context.Context, role string) error {
	m.mu.Lock()
	s := m.States[role]
	delete(m.States, role)
	m.mu.Unlock()
	if m.Routes == nil || !s.Active {
		return nil
	}
	ops := make([]routing.Operation, 0, len(s.Prefixes))
	for _, prefix := range s.Prefixes {
		ops = append(ops, routing.Operation{Kind: "delete-park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: staticProtocol})
	}
	return m.Routes.Apply(ctx, routing.Transaction{Role: role, Operations: ops})
}
func (m *Manager) Snapshot(role string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.States[role]
	s.Prefixes = append([]netip.Prefix(nil), s.Prefixes...)
	return s
}

const staticProtocol = 4 // RTPROT_STATIC, kept here to avoid a Linux dependency.
