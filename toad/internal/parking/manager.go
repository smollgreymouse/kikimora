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

// PrepareRegisteredWithdrawal parks exactly the explicitly registered
// destinations (08D §7). Every prefix came from Reserve/ACK ownership: DNS
// entries are host prefixes, static entries may be wider CIDRs. A prefix is
// parked only when its kernel row still carries the recorded identity, so
// the park can never claim a destination the registry does not own. The
// real lower-metric selected route stays in place until the withdrawal or
// restart removes it. Returns the verified prefixes that were parked.
func (m *Manager) PrepareRegisteredWithdrawal(ctx context.Context, role string, owned []routing.SelectedRoute) ([]netip.Prefix, error) {
	if m.Routes == nil {
		return nil, fmt.Errorf("parking route executor is nil")
	}
	kernel, err := m.Routes.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0, len(owned))
	seen := make(map[netip.Prefix]bool)
	for _, entry := range owned {
		if entry.Role != role || seen[entry.Prefix] {
			continue
		}
		if !entry.Prefix.IsValid() || !routing.IsCanonicalPrefix(entry.Prefix) {
			return nil, fmt.Errorf("registered withdrawal: prefix %q is not canonical", entry.Prefix)
		}
		switch entry.Source {
		case routing.SelectedDNS:
			if !routing.IsHostPrefix(entry.Prefix) {
				return nil, fmt.Errorf("registered withdrawal: DNS entry %q is not a host prefix", entry.Prefix)
			}
		case routing.SelectedStatic:
		default:
			return nil, fmt.Errorf("registered withdrawal: unknown source %q", entry.Source)
		}
		if registeredRowVerified(kernel, entry) {
			seen[entry.Prefix] = true
			prefixes = append(prefixes, entry.Prefix)
		}
	}
	// Install the unreachable protection directly: unlike the legacy
	// PrepareWithdrawal this path must keep non-host static entries, which
	// the host-prefix guard would silently drop.
	if m.Routes == nil {
		return nil, fmt.Errorf("parking route executor is nil")
	}
	ops := make([]routing.Operation, 0, len(prefixes))
	for _, prefix := range prefixes {
		ops = append(ops, routing.Operation{Kind: "park", Prefix: prefix.String(), Table: 254, Metric: 42760, Protocol: staticProtocol})
	}
	if err := m.Routes.Apply(ctx, routing.Transaction{Role: role, Operations: ops}); err != nil {
		// Fail closed: a partial apply leaves the state recording every
		// requested prefix, so the withdrawal retries cover them all.
		m.mu.Lock()
		m.States[role] = State{Active: len(prefixes) > 0, Count: len(prefixes), Prefixes: append([]netip.Prefix(nil), prefixes...)}
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Lock()
	m.States[role] = State{Active: len(prefixes) > 0, Count: len(prefixes), Prefixes: append([]netip.Prefix(nil), prefixes...)}
	m.mu.Unlock()
	return prefixes, nil
}

// registeredRowVerified reports whether the kernel still shows the exact
// owned row for the destination: same prefix, outside the endpoint table,
// on the recorded interface, with a protocol the managed stack writes.
func registeredRowVerified(kernel routing.KernelState, entry routing.SelectedRoute) bool {
	for _, route := range kernel.Routes {
		if route.Kind == "route" && route.Table != 51890 &&
			route.IfIndex == entry.IfIndex &&
			route.Prefix == entry.Prefix.String() &&
			(route.Protocol == 0 || route.Protocol == 3 || route.Protocol == 4) {
			return true
		}
	}
	return false
}

// RegisteredRestoration releases parks only where the registry's expected
// selected route is Active and the kernel read-back matches: exact prefix,
// the expected current interface, a managed protocol, and a metric that
// wins over 42760. A route through the physical interface or another VPN
// never releases a park.
func (m *Manager) RegisteredRestoration(ctx context.Context, role string, expected []routing.SelectedRoute) (int, error) {
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
		var record *routing.SelectedRoute
		for i := range expected {
			if expected[i].Prefix == prefix && expected[i].Role == role {
				if expected[i].State == routing.SelectedActive {
					record = &expected[i]
				}
				break
			}
		}
		if record == nil {
			// The registry does not claim this destination back yet: the
			// park stays.
			continue
		}
		winning := false
		for _, route := range kernel.Routes {
			if route.Kind == "route" && route.Table != 51890 &&
				route.Prefix == prefix.String() &&
				route.IfIndex == record.IfIndex &&
				(route.Protocol == 0 || route.Protocol == 3 || route.Protocol == 4) &&
				route.Metric < 42760 {
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
