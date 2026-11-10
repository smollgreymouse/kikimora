package routing

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"runtime"
	"sort"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// lowerHex32 is the exact activation-id shape every schema2 checkpoint
// carries (§31.4).
var lowerHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Shared managed-route constants (08D1 §1.1). One definition serves registry
// validation, control, parking and both platform backends. There is
// deliberately no shared managed protocol constant: §31.26 — Linux selected
// and park rows use RTPROT_STATIC (4) while Windows uses
// RouteProtocolNetMgmt (3); the exact value is a platform contract, never a
// cross-platform number.
const (
	// ManagedSelectedRouteTable is the main table (synthetic 254 on Windows).
	ManagedSelectedRouteTable = 254
	// ManagedParkMetric is the deterministic park route metric.
	ManagedParkMetric uint32 = 42760
)

// Linux route scope values the managed writer uses; Windows writes 0.
const (
	ScopeUniverse = 0
	ScopeLink     = 253
)

// SelectedRouteSource tells the admission policy apart: DNS intents are host
// prefixes (/32 or /128), static intents may be wider CIDRs.
type SelectedRouteSource string

const (
	SelectedDNS    SelectedRouteSource = "dns"
	SelectedStatic SelectedRouteSource = "static"
)

// SelectedRouteState is the selected-route transaction state machine
// (08D1 §1.2). It is independent of the park transaction machine.
type SelectedRouteState string

const (
	// SelectedPending: Desired checkpointed; the selected-route mutation is
	// not authorized.
	SelectedPending SelectedRouteState = "pending"
	// SelectedApplying: the exact preflight succeeded and is durable; the
	// selected-route mutation may have started.
	SelectedApplying SelectedRouteState = "applying"
	// SelectedActive: the exact Desired was read back and stored as Owned.
	SelectedActive SelectedRouteState = "active"
	// SelectedRetiring: explicit selected-route retirement is durable.
	SelectedRetiring SelectedRouteState = "retiring"
)

// SelectedParkState is the independent park transaction state machine
// (08D1 §1.2).
type SelectedParkState string

const (
	SelectedParkPending  SelectedParkState = "pending"
	SelectedParkApplying SelectedParkState = "applying"
	SelectedParkActive   SelectedParkState = "active"
	SelectedParkRetiring SelectedParkState = "retiring"
)

// SelectedParkKind distinguishes the platform park shapes (§1.1): Linux
// parks are RTN_UNREACHABLE rows with no interface, Windows parks are
// loopback next-hop rows.
type SelectedParkKind string

const (
	SelectedParkUnreachable SelectedParkKind = "unreachable"
	SelectedParkLoopback    SelectedParkKind = "loopback"
)

// SelectedRouteIdentity is the exact kernel identity of one selected route
// (§1.1, §31.19). Same prefix + same ifindex + proto class + lower metric is
// never an ownership proof: every mutation/removal decision uses this exact
// recorded identity. InterfaceLuid is Windows-only (§31.19); Linux writes 0.
type SelectedRouteIdentity struct {
	Table         int    `json:"table"`
	IfIndex       int    `json:"ifindex"`
	NextHop       string `json:"next_hop,omitempty"`
	Protocol      int    `json:"protocol"`
	Metric        uint32 `json:"metric"`
	Scope         int    `json:"scope,omitempty"`
	InterfaceLuid uint64 `json:"interface_luid,omitempty"`
}

// Validate rejects identities that cannot be a managed selected route.
// Protocol is checked only for positivity here; the platform restore/writer
// validation enforces the exact platform value (§31.26).
func (i SelectedRouteIdentity) Validate(prefix netip.Prefix) error {
	if !prefix.IsValid() || !IsCanonicalPrefix(prefix) {
		return fmt.Errorf("identity prefix %q is not canonical", prefix)
	}
	if i.Table != ManagedSelectedRouteTable {
		return fmt.Errorf("identity table %d must be %d", i.Table, ManagedSelectedRouteTable)
	}
	if i.IfIndex <= 0 {
		return fmt.Errorf("identity ifindex %d must be positive", i.IfIndex)
	}
	if i.NextHop != "" {
		hop, err := netip.ParseAddr(i.NextHop)
		if err != nil {
			return fmt.Errorf("identity next hop %q does not parse", i.NextHop)
		}
		if hop.Is4In6() || hop.Is4() != prefix.Addr().Is4() {
			return fmt.Errorf("identity next hop %q does not match the prefix family", i.NextHop)
		}
	}
	if i.Protocol <= 0 {
		return fmt.Errorf("identity protocol %d must be positive", i.Protocol)
	}
	switch i.Scope {
	case 0, ScopeLink:
	default:
		return fmt.Errorf("identity scope %d is not a valid Linux route scope", i.Scope)
	}
	return nil
}

// Equal is exact field equality: every writer-controlled kernel field.
func (i SelectedRouteIdentity) Equal(other SelectedRouteIdentity) bool {
	return i == other
}

// SelectedParkIdentity is the exact kernel identity of one managed park row
// (§1.1). Park validation is platform-neutral but exact.
type SelectedParkIdentity struct {
	Kind          SelectedParkKind `json:"kind"`
	Table         int              `json:"table"`
	IfIndex       int              `json:"ifindex"`
	NextHop       string           `json:"next_hop,omitempty"`
	Protocol      int              `json:"protocol"`
	Metric        uint32           `json:"metric"`
	Scope         int              `json:"scope,omitempty"`
	InterfaceLuid uint64           `json:"interface_luid,omitempty"`
}

// Validate enforces the deterministic park identity: table 254, the managed
// park metric, and kind-dependent interface/next-hop rules. Unreachable
// parks have no interface at all; loopback parks point at a loopback address
// of the prefix family on a real interface.
func (i SelectedParkIdentity) Validate(prefix netip.Prefix) error {
	if !prefix.IsValid() || !IsCanonicalPrefix(prefix) {
		return fmt.Errorf("park prefix %q is not canonical", prefix)
	}
	if i.Table != ManagedSelectedRouteTable {
		return fmt.Errorf("park table %d must be %d", i.Table, ManagedSelectedRouteTable)
	}
	if i.Protocol <= 0 {
		return fmt.Errorf("park protocol %d must be positive", i.Protocol)
	}
	if i.Metric != ManagedParkMetric {
		return fmt.Errorf("park metric %d must be %d", i.Metric, ManagedParkMetric)
	}
	switch i.Kind {
	case SelectedParkUnreachable:
		if i.IfIndex != 0 || i.NextHop != "" || i.InterfaceLuid != 0 {
			return fmt.Errorf("unreachable park must have no interface and no next hop")
		}
	case SelectedParkLoopback:
		if i.IfIndex <= 0 {
			return fmt.Errorf("loopback park requires a positive ifindex")
		}
		hop, err := netip.ParseAddr(i.NextHop)
		if err != nil {
			return fmt.Errorf("loopback park next hop %q does not parse", i.NextHop)
		}
		if hop.Is4In6() || hop.Is4() != prefix.Addr().Is4() {
			return fmt.Errorf("loopback park next hop %q does not match the prefix family", i.NextHop)
		}
		if hop.IsLoopback() == false {
			return fmt.Errorf("loopback park next hop %q is not a loopback address", i.NextHop)
		}
	default:
		return fmt.Errorf("unknown park kind %q", i.Kind)
	}
	switch i.Scope {
	case 0, ScopeLink:
	default:
		return fmt.Errorf("park scope %d is not a valid Linux route scope", i.Scope)
	}
	return nil
}

// Equal is exact field equality including the platform LUID (§31.19).
func (i SelectedParkIdentity) Equal(other SelectedParkIdentity) bool {
	return i == other
}

// SelectedPark is the independent park transaction (§1.2): Desired is
// checkpointed before any park mutation, Owned is the last exact kernel
// identity proven by read-back.
type SelectedPark struct {
	State         SelectedParkState     `json:"state"`
	Desired       SelectedParkIdentity  `json:"desired"`
	Owned         *SelectedParkIdentity `json:"owned,omitempty"`
	RetireDesired bool                  `json:"retire_desired"`
}

// SelectedRoute is one registered destination under a role (§1.3). Ownership
// is the (role, canonical prefix) pair. Desired is checkpointed before
// mutation; Owned is the last exact kernel identity proven by read-back.
// Target changes update Desired but retain Owned until replacement is
// verified. There is no second authoritative IfIndex outside Desired.
type SelectedRoute struct {
	Role            string                 `json:"role"`
	Zone            string                 `json:"zone"`
	Prefix          netip.Prefix           `json:"prefix"`
	Source          SelectedRouteSource    `json:"source"`
	ToadGeneration  uint64                 `json:"toad_generation"`
	RouteGeneration uint64                 `json:"route_generation"`
	Interface       string                 `json:"interface,omitempty"`
	Desired         SelectedRouteIdentity  `json:"desired"`
	Owned           *SelectedRouteIdentity `json:"owned,omitempty"`
	State           SelectedRouteState     `json:"state"`
	RetireDesired   bool                   `json:"retire_desired"`
	Park            *SelectedPark          `json:"park,omitempty"`
}

// SelectedRouteRegistry is the explicit selected-route ownership store with
// two independent crash-safe transaction machines (§1.4). Invalid
// transitions are errors, never coerced.
type SelectedRouteRegistry interface {
	// Reserve always writes Desired. A Desired/generation change demotes
	// Active to Pending and keeps the old Owned.
	Reserve(role, zone string, prefix netip.Prefix, source SelectedRouteSource,
		toadGeneration, routeGeneration uint64, iface string, desired SelectedRouteIdentity) error
	// MarkApplying is only Pending -> Applying.
	MarkApplying(role string, prefix netip.Prefix) error
	// MarkActive is only Applying/Active -> Active; observed must equal
	// Desired; then Owned=observed.
	MarkActive(role string, prefix netip.Prefix, observed SelectedRouteIdentity) error
	// MarkRetiring records the previous state, sets RetireDesired only when
	// the previous state was Applying, and keeps Desired/Owned/Park evidence.
	MarkRetiring(role string, prefix netip.Prefix) error
	// BeginRepair demotes an exactly-verified Active record back to Pending
	// so the canonical reconcile can re-drive it (08D1 §37.4). Legal only
	// from SelectedActive; Desired/Owned/Park evidence is preserved.
	BeginRepair(role string, prefix netip.Prefix) error

	// ReservePark creates/updates Park.Desired. A changed Desired or
	// forcePending preserves the previous Park.Owned and sets Pending; an
	// identical Active park reserved with forcePending=false stays Active.
	ReservePark(role string, prefix netip.Prefix, desired SelectedParkIdentity, forcePending bool) error
	// MarkParkApplying is only ParkPending -> ParkApplying.
	MarkParkApplying(role string, prefix netip.Prefix) error
	// MarkParkActive is only ParkApplying/ParkActive -> ParkActive; observed
	// must equal Park.Desired; then Park.Owned=observed.
	MarkParkActive(role string, prefix netip.Prefix, observed SelectedParkIdentity) error
	// MarkParkRetiring is legal from ParkApplying or ParkActive.
	MarkParkRetiring(role string, prefix netip.Prefix) error
	// ClearPark is legal after exact absence was proven, or directly from
	// ParkPending when no park mutation was authorized; it sets Park=nil.
	ClearPark(role string, prefix netip.Prefix) error

	// RestoreRole validates both machines before mutating the map: restore
	// is all-or-nothing.
	RestoreRole(checkpoint SelectedRoutesCheckpoint) error

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
	toadGeneration, routeGeneration uint64, iface string, desired SelectedRouteIdentity) error {
	if role == "" {
		return fmt.Errorf("selected route reserve: role is required")
	}
	if err := desired.Validate(prefix); err != nil {
		return fmt.Errorf("selected route reserve: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selectedRouteKey{role: role, prefix: prefix}
	existing, ok := r.entries[key]
	if ok && existing.Desired == desired && existing.Zone == zone && existing.Source == source &&
		existing.Interface == iface && existing.ToadGeneration == toadGeneration &&
		existing.RouteGeneration == routeGeneration {
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
	next.Desired = desired
	if ok && next.State == SelectedActive && existing.Desired != desired {
		// The previously verified row carries the old identity; ownership
		// stays reserved but the row must be reconciled again. The old Owned
		// remains as the replacement candidate (§1.4).
		next.State = SelectedPending
	}
	r.entries[key] = next
	return nil
}

func (r *MemorySelectedRegistry) MarkApplying(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		switch entry.State {
		case SelectedPending:
			entry.State = SelectedApplying
		case SelectedApplying:
			// Idempotent re-mark of an already-authorized transaction: the
			// durable Applying record resumes without coercion (§1.4).
		default:
			return fmt.Errorf("MarkApplying requires state pending or applying, got %q", entry.State)
		}
		return nil
	})
}

func (r *MemorySelectedRegistry) MarkActive(role string, prefix netip.Prefix, observed SelectedRouteIdentity) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.State != SelectedApplying && entry.State != SelectedActive {
			return fmt.Errorf("MarkActive requires state applying or active, got %q", entry.State)
		}
		if observed != entry.Desired {
			return fmt.Errorf("MarkActive observed identity does not equal the desired identity")
		}
		owned := observed
		entry.Owned = &owned
		entry.State = SelectedActive
		return nil
	})
}

func (r *MemorySelectedRegistry) MarkRetiring(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		switch entry.State {
		case SelectedPending, SelectedApplying, SelectedActive:
		default:
			return fmt.Errorf("MarkRetiring is illegal from state %q", entry.State)
		}
		// RetireDesired=true means the exact Desired may have been created by
		// an authorized interrupted syscall (§1.4) and must be considered
		// during deletion.
		entry.RetireDesired = entry.State == SelectedApplying
		entry.State = SelectedRetiring
		return nil
	})
}

// BeginRepair demotes an exactly-verified Active record back to Pending so
// the canonical reconcile can re-drive it (08D1 §37.4): Desired is
// preserved, the exact previous Owned is preserved, RetireDesired stays
// false, and the park transaction is untouched. Legal only from
// SelectedActive.
func (r *MemorySelectedRegistry) BeginRepair(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.State != SelectedActive {
			return fmt.Errorf("BeginRepair requires state active, got %q", entry.State)
		}
		entry.State = SelectedPending
		entry.RetireDesired = false
		return nil
	})
}

func (r *MemorySelectedRegistry) ReservePark(role string, prefix netip.Prefix, desired SelectedParkIdentity, forcePending bool) error {
	if err := desired.Validate(prefix); err != nil {
		return fmt.Errorf("reserve park: %w", err)
	}
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.Park == nil {
			entry.Park = &SelectedPark{
				State:   SelectedParkPending,
				Desired: desired,
			}
			return nil
		}
		if entry.Park.Desired == desired && !forcePending && entry.Park.State == SelectedParkActive {
			// An identical Active park stays Active so an exact observation
			// can remain a no-op (§1.4).
			return nil
		}
		if entry.Park.Desired != desired || forcePending {
			// Preserve the previous exact ownership as the replacement
			// candidate; the new mutation is not authorized yet.
			entry.Park.State = SelectedParkPending
			entry.Park.RetireDesired = false
			entry.Park.Desired = desired
		}
		return nil
	})
}

func (r *MemorySelectedRegistry) MarkParkApplying(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.Park == nil {
			return fmt.Errorf("MarkParkApplying without a park transaction")
		}
		if entry.Park.State != SelectedParkPending {
			return fmt.Errorf("MarkParkApplying requires park state pending, got %q", entry.Park.State)
		}
		entry.Park.State = SelectedParkApplying
		return nil
	})
}

func (r *MemorySelectedRegistry) MarkParkActive(role string, prefix netip.Prefix, observed SelectedParkIdentity) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.Park == nil {
			return fmt.Errorf("MarkParkActive without a park transaction")
		}
		if entry.Park.State != SelectedParkApplying && entry.Park.State != SelectedParkActive {
			return fmt.Errorf("MarkParkActive requires park state applying or active, got %q", entry.Park.State)
		}
		if observed != entry.Park.Desired {
			return fmt.Errorf("MarkParkActive observed identity does not equal the desired park identity")
		}
		owned := observed
		entry.Park.Owned = &owned
		entry.Park.State = SelectedParkActive
		return nil
	})
}

func (r *MemorySelectedRegistry) MarkParkRetiring(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.Park == nil {
			return fmt.Errorf("MarkParkRetiring without a park transaction")
		}
		switch entry.Park.State {
		case SelectedParkApplying, SelectedParkActive:
		default:
			return fmt.Errorf("MarkParkRetiring is illegal from park state %q", entry.Park.State)
		}
		entry.Park.RetireDesired = entry.Park.State == SelectedParkApplying
		entry.Park.State = SelectedParkRetiring
		return nil
	})
}

func (r *MemorySelectedRegistry) ClearPark(role string, prefix netip.Prefix) error {
	return r.transition(role, prefix, func(entry *SelectedRoute) error {
		if entry.Park == nil {
			return nil
		}
		switch entry.Park.State {
		case SelectedParkPending, SelectedParkRetiring:
		default:
			return fmt.Errorf("ClearPark is illegal from park state %q", entry.Park.State)
		}
		entry.Park = nil
		return nil
	})
}

// RestoreRole validates every entry before mutating the map: all-or-nothing
// (§1.4).
func (r *MemorySelectedRegistry) RestoreRole(checkpoint SelectedRoutesCheckpoint) error {
	if checkpoint.Role == "" {
		return fmt.Errorf("restore: checkpoint role is required")
	}
	restored := make(map[selectedRouteKey]SelectedRoute, len(checkpoint.Entries))
	seen := make(map[selectedRouteKey]bool, len(checkpoint.Entries))
	for _, entry := range checkpoint.Entries {
		key := selectedRouteKey{role: entry.Role, prefix: entry.Prefix}
		if seen[key] {
			return fmt.Errorf("restore: duplicate prefix %s", entry.Prefix)
		}
		seen[key] = true
		if entry.State != SelectedPending && entry.State != SelectedApplying &&
			entry.State != SelectedActive && entry.State != SelectedRetiring {
			return fmt.Errorf("restore: unknown selected state %q", entry.State)
		}
		if entry.Desired.Validate(entry.Prefix) != nil {
			return fmt.Errorf("restore: entry %s has an invalid desired identity", entry.Prefix)
		}
		if entry.State == SelectedActive {
			if entry.Owned == nil || *entry.Owned != entry.Desired {
				return fmt.Errorf("restore: active entry %s requires owned == desired", entry.Prefix)
			}
		}
		if entry.State != SelectedRetiring && entry.RetireDesired {
			return fmt.Errorf("restore: retire_desired must be false for state %q", entry.State)
		}
		if entry.Park != nil {
			if err := validateParkTransaction(entry.Park, entry.Prefix); err != nil {
				return fmt.Errorf("restore: entry %s park transaction: %w", entry.Prefix, err)
			}
		}
		restored[key] = entry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, entry := range restored {
		r.entries[key] = entry
	}
	return nil
}

// validateParkTransaction enforces the independent park machine invariants
// (§2.2) for one restored record.
func validateParkTransaction(park *SelectedPark, prefix netip.Prefix) error {
	if park.Desired.Validate(prefix) != nil {
		return fmt.Errorf("invalid desired park identity")
	}
	switch park.State {
	case SelectedParkPending:
		if park.RetireDesired {
			return fmt.Errorf("park pending must not carry retire_desired")
		}
	case SelectedParkApplying:
		if park.RetireDesired {
			return fmt.Errorf("park applying must not carry retire_desired")
		}
	case SelectedParkActive:
		if park.Owned == nil || *park.Owned != park.Desired {
			return fmt.Errorf("park active requires owned == desired")
		}
		if park.RetireDesired {
			return fmt.Errorf("park active must not carry retire_desired")
		}
	case SelectedParkRetiring:
		// Desired/Owned are retained; RetireDesired records whether
		// retirement began from ParkApplying.
	default:
		return fmt.Errorf("unknown park state %q", park.State)
	}
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

func (r *MemorySelectedRegistry) transition(role string, prefix netip.Prefix, mutate func(*SelectedRoute) error) error {
	if !prefix.IsValid() || !IsCanonicalPrefix(prefix) {
		return fmt.Errorf("selected route transition: prefix %q is not canonical", prefix)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selectedRouteKey{role: role, prefix: prefix}
	entry, ok := r.entries[key]
	if !ok {
		return fmt.Errorf("selected route %s/%s is not registered", role, prefix)
	}
	if err := mutate(&entry); err != nil {
		return err
	}
	r.entries[key] = entry
	return nil
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
// Checkpoint schema 2 (08D1 §2.1, §31.4, §31.26): <role StateDir>/selected-routes.json.
//
// Every transaction transition is checkpointed, so a crash leaves a durable
// Pending/Applying/Active/Retiring record plus the independent park
// transaction. Schema 1 decodes only to an explicit unsupported error:
// exact identity is never inferred from it.
// ---------------------------------------------------------------------------

const (
	// SelectedRoutesCheckpointSchema is the current on-disk schema.
	SelectedRoutesCheckpointSchema = 2
	selectedRoutesCheckpointSchema = 2
	// CheckpointPlatformLinux/Windows are the exact Platform values
	// (§31.26); checkpoints are not portable between OSes.
	CheckpointPlatformLinux   = "linux"
	CheckpointPlatformWindows = "windows"
)

// SelectedRoutesCheckpoint is the schema-2 persistence record.
type SelectedRoutesCheckpoint struct {
	Schema          int             `json:"schema"`
	Role            string          `json:"role"`
	RouteGeneration uint64          `json:"route_generation"`
	Entries         []SelectedRoute `json:"entries"`
	// ActivationID/ModeGeneration fence the checkpoint to one managed
	// activation (§31.4): restore requires exact equality with the active
	// marker once schema2 activation lands.
	ActivationID   string `json:"activation_id,omitempty"`
	ModeGeneration uint64 `json:"mode_generation,omitempty"`
	// Platform prevents cross-OS reinterpretation of protocol values
	// (§31.26): a copied Windows checkpoint never restores under Linux
	// rules.
	Platform string `json:"platform"`
}

// RuntimeCheckpointPlatform names the running platform for checkpoint
// fencing.
func RuntimeCheckpointPlatform() string {
	if isWindowsRuntime() {
		return CheckpointPlatformWindows
	}
	return CheckpointPlatformLinux
}

// ValidateSelectedRoutesCheckpoint enforces the §2.2 invariants on a decoded
// checkpoint. The schema2 activation fence is mandatory (D7R §6.1): an
// unfenced checkpoint could be restored after a Disable + re-Enable of the
// same role/zone (P0-4).
func ValidateSelectedRoutesCheckpoint(
	c SelectedRoutesCheckpoint,
	expectedRole string,
	expectedZone string,
) error {
	if c.Schema != selectedRoutesCheckpointSchema {
		return fmt.Errorf("selected-routes checkpoint schema %d is not supported", c.Schema)
	}
	if c.Role != expectedRole {
		return fmt.Errorf("checkpoint role %q does not match expected %q", c.Role, expectedRole)
	}
	if !lowerHex32.MatchString(c.ActivationID) {
		return fmt.Errorf("checkpoint requires a 32 lowercase-hex activation id")
	}
	if c.ModeGeneration == 0 {
		return fmt.Errorf("checkpoint requires a positive mode generation")
	}
	switch c.Platform {
	case CheckpointPlatformLinux, CheckpointPlatformWindows:
	default:
		return fmt.Errorf("checkpoint platform %q is not supported", c.Platform)
	}
	if c.Entries == nil {
		return fmt.Errorf("checkpoint entries must be present")
	}
	seen := make(map[netip.Prefix]bool, len(c.Entries))
	for i := range c.Entries {
		entry := &c.Entries[i]
		if entry.Role != expectedRole {
			return fmt.Errorf("entry %d role %q does not match the checkpoint role", i, entry.Role)
		}
		if expectedZone != "" && entry.Zone != expectedZone {
			return fmt.Errorf("entry %d zone %q does not match the configured zone %q", i, entry.Zone, expectedZone)
		}
		if !IsCanonicalPrefix(entry.Prefix) {
			return fmt.Errorf("entry %d prefix %q is not canonical", i, entry.Prefix)
		}
		switch entry.Source {
		case SelectedDNS:
			if !IsHostPrefix(entry.Prefix) {
				return fmt.Errorf("dns entry %d must be a host prefix", i)
			}
		case SelectedStatic:
		default:
			return fmt.Errorf("entry %d has an unknown source %q", i, entry.Source)
		}
		switch entry.State {
		case SelectedPending, SelectedApplying, SelectedActive, SelectedRetiring:
		default:
			return fmt.Errorf("entry %d has an unknown state %q", i, entry.State)
		}
		if entry.ToadGeneration == 0 {
			return fmt.Errorf("entry %d must carry a real toad generation", i)
		}
		if entry.RouteGeneration == 0 {
			return fmt.Errorf("entry %d must carry a route generation", i)
		}
		if err := entry.Desired.Validate(entry.Prefix); err != nil {
			return fmt.Errorf("entry %d desired identity: %w", i, err)
		}
		if entry.State == SelectedActive && (entry.Owned == nil || *entry.Owned != entry.Desired) {
			return fmt.Errorf("active entry %d requires owned == desired", i)
		}
		if entry.State != SelectedRetiring && entry.RetireDesired {
			return fmt.Errorf("entry %d retire_desired must be false for state %q", i, entry.State)
		}
		if entry.Park != nil {
			if err := validateParkTransaction(entry.Park, entry.Prefix); err != nil {
				return fmt.Errorf("entry %d park transaction: %w", i, err)
			}
		}
		if seen[entry.Prefix] {
			return fmt.Errorf("duplicate prefix %s", entry.Prefix)
		}
		seen[entry.Prefix] = true
	}
	return nil
}

// WriteSelectedRoutesCheckpoint persists the schema-2 record through the
// shared durable primitive (§31.7).
func WriteSelectedRoutesCheckpoint(path string, value SelectedRoutesCheckpoint) error {
	return fsutil.WriteAtomicJSON(path, value, 0o600)
}

// ReadSelectedRoutesCheckpoint decodes and validates a schema-2 checkpoint.
// Schema 1 decodes only to an explicit unsupported error (§2.1).
func ReadSelectedRoutesCheckpoint(path string) (SelectedRoutesCheckpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SelectedRoutesCheckpoint{}, err
	}
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("decode selected-routes checkpoint: %w", err)
	}
	if probe.Schema == 1 {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("selected-routes checkpoint schema 1 is not supported; exact identity cannot be inferred from it")
	}
	var c SelectedRoutesCheckpoint
	if err := json.Unmarshal(b, &c); err != nil {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("decode selected-routes checkpoint: %w", err)
	}
	if err := ValidateSelectedRoutesCheckpoint(c, c.Role, ""); err != nil {
		return SelectedRoutesCheckpoint{}, fmt.Errorf("selected-routes checkpoint is invalid: %w", err)
	}
	return c, nil
}

// isWindowsRuntime is overridden in platform-specific tests.
var isWindowsRuntime = func() bool {
	return runtime.GOOS == "windows"
}
