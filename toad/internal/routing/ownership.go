package routing

import (
	"net/netip"
	"sync"
)

// SelectedRouteOwner is explicit product ownership of one selected host route.
// Kernel discovery alone never creates this ownership record.
type SelectedRouteOwner struct {
	Role      string       `json:"role"`
	Interface string       `json:"interface"`
	IfIndex   int          `json:"ifindex"`
	Prefix    netip.Prefix `json:"prefix"`
}

type OwnershipRegistry interface {
	Snapshot(role string) []SelectedRouteOwner
	Replace(role string, routes []SelectedRouteOwner)
	Remove(role string)
}

type MemoryOwnership struct {
	mu     sync.Mutex
	routes map[string][]SelectedRouteOwner
}

func NewMemoryOwnership() *MemoryOwnership {
	return &MemoryOwnership{routes: make(map[string][]SelectedRouteOwner)}
}

func (r *MemoryOwnership) Snapshot(role string) []SelectedRouteOwner {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]SelectedRouteOwner(nil), r.routes[role]...)
	return out
}

func (r *MemoryOwnership) Replace(role string, routes []SelectedRouteOwner) {
	if r == nil || role == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routes == nil {
		r.routes = make(map[string][]SelectedRouteOwner)
	}
	if len(routes) == 0 {
		delete(r.routes, role)
		return
	}
	copyRoutes := append([]SelectedRouteOwner(nil), routes...)
	r.routes[role] = copyRoutes
}

func (r *MemoryOwnership) Remove(role string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, role)
}
