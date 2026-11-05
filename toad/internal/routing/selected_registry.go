package routing

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// SelectedRouteSource tells the admission policy apart: DNS intents are host
// prefixes (/32 or /128), static intents may be wider CIDRs.
type SelectedRouteSource string

const (
	SelectedDNS    SelectedRouteSource = "dns"
	SelectedStatic SelectedRouteSource = "static"
)

// SelectedRouteState is the registry's view of one selected destination.
type SelectedRouteState string

const (
	// SelectedPending: ownership is reserved and checkpointed, the kernel
	// row is not yet verified. Safe to reconcile later.
	SelectedPending SelectedRouteState = "pending"
	// SelectedActive: the exact kernel row was verified after an apply.
	SelectedActive SelectedRouteState = "active"
	// SelectedParked: the destination is held by a verified unreachable
	// route while the real selected route is withdrawn.
	SelectedParked SelectedRouteState = "parked"
)

// SelectedRoute is one registered destination under a role. Ownership is the
// (role, canonical prefix) pair; the configured zone is stored and verified
// but is not the key.
type SelectedRoute struct {
	Role            string              `json:"role"`
	Zone            string              `json:"zone"`
	Prefix          netip.Prefix        `json:"prefix"`
	Source          SelectedRouteSource `json:"source"`
	ToadGeneration  uint64              `json:"toad_generation"`
	RouteGeneration uint64              `json:"route_generation"`
	Interface       string              `json:"interface,omitempty"`
	IfIndex         int                 `json:"ifindex"`
	State           SelectedRouteState  `json:"state"`
}

// SelectedRouteRegistry is the explicit selected-route ownership store. It
// replaces baseline/delta kernel discovery for managed zones: registration
// is checkpointed before any kernel mutation, so recovery never has to
// guess ownership from what happens to be in the FIB.
type SelectedRouteRegistry interface {
	// Reserve records intended ownership before a kernel mutation. It is
	// idempotent for an identical re-reserve and updates the record when the
	// identity changed.
	Reserve(role, zone string, prefix netip.Prefix, source SelectedRouteSource,
		toadGeneration, routeGeneration uint64, iface string, ifindex int) error
	// MarkActive records an exactly verified kernel row.
	MarkActive(role string, prefix netip.Prefix) error
	// MarkParked records a verified unreachable protection.
	MarkParked(role string, prefix netip.Prefix) error
	Snapshot(role string) []SelectedRoute
	SnapshotFamily(role string, family int) []SelectedRoute
	Remove(role string, prefix netip.Prefix)
	RemoveRole(role string)
}

type selectedRouteKey struct {
	role   string
	prefix netip.Prefix
}

// MemorySelectedRegistry is the production in-memory registry. Persistence
// goes through SelectedRoutesCheckpoint files owned by the caller.
type MemorySelectedRegistry struct {
	mu      sync.Mutex
	entries map[selectedRouteKey]SelectedRoute
}

func NewMemorySelectedRegistry() *MemorySelectedRegistry {
	return &MemorySelectedRegistry{entries: make(map[selectedRouteKey]SelectedRoute)}
}

func (r *MemorySelectedRegistry) Reserve(role, zone string, prefix netip.Prefix, source SelectedRouteSource,
	toadGeneration, routeGeneration uint64, iface string, ifindex int) error {
	if role == "" {
		return fmt.Errorf("selected route reserve: role is required")
	}
	if !prefix.IsValid() || !IsCanonicalPrefix(prefix) {
		return fmt.Errorf("selected route reserve: prefix %q is not canonical", prefix)
	}
	if ifindex <= 0 {
		return fmt.Errorf("selected route reserve: ifindex must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selectedRouteKey{role: role, prefix: prefix}
	existing, ok := r.entries[key]
	if ok && existing.Zone == zone && existing.Source == source &&
		existing.Interface == iface && existing.IfIndex == ifindex &&
		existing.ToadGeneration == toadGeneration && existing.RouteGeneration == routeGeneration {
		// Identical re-reserve: keep the state.
		return nil
	}
	next := existing
	if !ok {
		next = SelectedRoute{State: SelectedPending}
	}
	next.Role = role
	next.Zone = zone
	next.Prefix = prefix
	next.Source = source
	next.ToadGeneration = toadGeneration
	next.RouteGeneration = routeGeneration
	next.Interface = iface
	next.IfIndex = ifindex
	if ok && next.State == SelectedActive && existing.IfIndex != ifindex {
		// The previously verified row carries the old identity; ownership
		// stays reserved but the row must be reconciled again.
		next.State = SelectedPending
	}
	r.entries[key] = next
	return nil
}

func (r *MemorySelectedRegistry) MarkActive(role string, prefix netip.Prefix) error {
	return r.mark(role, prefix, SelectedActive)
}

func (r *MemorySelectedRegistry) MarkParked(role string, prefix netip.Prefix) error {
	return r.mark(role, prefix, SelectedParked)
}

func (r *MemorySelectedRegistry) mark(role string, prefix netip.Prefix, state SelectedRouteState) error {
	if !prefix.IsValid() || !IsCanonicalPrefix(prefix) {
		return fmt.Errorf("selected route mark: prefix %q is not canonical", prefix)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selectedRouteKey{role: role, prefix: prefix}
	entry, ok := r.entries[key]
	if !ok {
		return fmt.Errorf("selected route %s/%s is not registered", role, prefix)
	}
	entry.State = state
	r.entries[key] = entry
	return nil
}

func (r *MemorySelectedRegistry) Snapshot(role string) []SelectedRoute {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(func(entry SelectedRoute) bool { return entry.Role == role })
}

func (r *MemorySelectedRegistry) SnapshotFamily(role string, family int) []SelectedRoute {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(func(entry SelectedRoute) bool {
		return entry.Role == role && prefixFamily(entry.Prefix) == family
	})
}

func (r *MemorySelectedRegistry) snapshotLocked(keep func(SelectedRoute) bool) []SelectedRoute {
	out := make([]SelectedRoute, 0, len(r.entries))
	for _, entry := range r.entries {
		if keep(entry) {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Prefix.String() != out[j].Prefix.String() {
			return out[i].Prefix.String() < out[j].Prefix.String()
		}
		return out[i].Role < out[j].Role
	})
	return out
}

func (r *MemorySelectedRegistry) Remove(role string, prefix netip.Prefix) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, selectedRouteKey{role: role, prefix: prefix})
}

func (r *MemorySelectedRegistry) RemoveRole(role string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.entries {
		if key.role == role {
			delete(r.entries, key)
		}
	}
}

func prefixFamily(prefix netip.Prefix) int {
	if prefix.Addr().Is4() {
		return 4
	}
	return 6
}

// IsCanonicalPrefix reports whether the prefix is already masked: a
// non-canonical prefix would make the ownership key ambiguous.
func IsCanonicalPrefix(prefix netip.Prefix) bool {
	return prefix.IsValid() && prefix == prefix.Masked()
}

// ---------------------------------------------------------------------------
// Checkpoint (08D §4.2): <role StateDir>/selected-routes.json, schema 1.
//
// Reserve is checkpointed before the kernel mutation and MarkActive after
// exact verification, so a crash between the two leaves a Pending entry
// that recovery reconciles, and a crash after the kernel apply still has
// the reserved ownership record to park from.
// ---------------------------------------------------------------------------

const selectedRoutesCheckpointSchema = 1

type SelectedRoutesCheckpoint struct {
	Schema          int             `json:"schema"`
	Role            string          `json:"role"`
	RouteGeneration uint64          `json:"route_generation"`
	Entries         []SelectedRoute `json:"entries"`
	Parked          []string        `json:"parked,omitempty"`
}

func WriteSelectedRoutesCheckpoint(path string, value SelectedRoutesCheckpoint) error {
	return fsutil.WriteAtomicJSON(path, value, 0o600)
}

func ReadSelectedRoutesCheckpoint(path string) (SelectedRoutesCheckpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SelectedRoutesCheckpoint{}, err
	}
	var c SelectedRoutesCheckpoint
	if err := json.Unmarshal(b, &c); err != nil {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("decode selected-routes checkpoint: %w", err)
	}
	if c.Schema != selectedRoutesCheckpointSchema {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("selected-routes checkpoint schema %d is not supported", c.Schema)
	}
	return c, nil
}
