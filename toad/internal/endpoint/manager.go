package endpoint

import (
	"context"
	"net/netip"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	Provider Provider
	State    State
}

// RefreshSpecs resolves a complete desired candidate set. A failed refresh
// never commits a partial set and keeps the last-known-good policy active.
func (m *Manager) RefreshSpecs(ctx context.Context, epoch uint64, configured []EndpointSpec, resolver Resolver) State {
	previous := m.Snapshot()
	if len(configured) == 0 {
		return m.keepDegraded(previous, "endpoint provider returned no endpoints")
	}
	resolved := make([]netip.AddrPort, 0, len(configured))
	for _, spec := range configured {
		if spec.Address.IsValid() {
			resolved = append(resolved, spec.Address)
			continue
		}
		if spec.Hostname == "" || resolver == nil {
			return m.keepDegraded(previous, "endpoint has no resolvable address")
		}
		port := specPort(spec)
		addresses, err := Resolve(ctx, resolver, spec.Hostname, port)
		if err != nil || len(addresses) == 0 {
			if err == nil {
				err = context.DeadlineExceeded
			}
			return m.keepDegraded(previous, err.Error())
		}
		resolved = append(resolved, addresses...)
	}
	return m.commit(epoch, resolved, configured)
}

func (m *Manager) Refresh(ctx context.Context, epoch uint64, configured []string) State {
	m.mu.Lock()
	previous := m.State
	m.mu.Unlock()
	if m.Provider == nil {
		previous.State = "degraded"
		previous.LastError = "endpoint provider is nil"
		m.mu.Lock()
		m.State = previous
		m.mu.Unlock()
		return previous
	}
	addresses, err := m.Provider.Resolve(ctx)
	if err != nil {
		previous.State = "degraded"
		previous.LastError = err.Error()
		m.mu.Lock()
		m.State = previous
		m.mu.Unlock()
		return previous
	}
	m.mu.Lock()
	m.State = State{State: "ready", AppliedUnderlayEpoch: epoch, Configured: append([]string(nil), configured...), Resolved: append([]netip.AddrPort(nil), addresses...), Live: append([]netip.AddrPort(nil), addresses...)}
	v := m.State
	m.mu.Unlock()
	return v
}

func (m *Manager) commit(epoch uint64, addresses []netip.AddrPort, configured []EndpointSpec) State {
	values := make([]string, 0, len(configured))
	for _, spec := range configured {
		if spec.Address.IsValid() {
			values = append(values, spec.Address.String())
		} else {
			values = append(values, spec.Hostname)
		}
	}
	m.mu.Lock()
	m.State = State{State: "ready", AppliedUnderlayEpoch: epoch, Configured: values, Resolved: append([]netip.AddrPort(nil), addresses...), Live: append([]netip.AddrPort(nil), addresses...)}
	v := m.State
	m.mu.Unlock()
	return v
}

func (m *Manager) keepDegraded(previous State, message string) State {
	previous.State = "degraded"
	previous.LastError = message
	m.mu.Lock()
	m.State = previous
	m.mu.Unlock()
	return previous
}

func specPort(spec EndpointSpec) uint16 {
	if spec.Address.IsValid() {
		return uint16(spec.Address.Port())
	}
	if spec.Port != 0 {
		return spec.Port
	}
	return 443
}
func (m *Manager) Snapshot() State { m.mu.Lock(); defer m.mu.Unlock(); return m.State }
