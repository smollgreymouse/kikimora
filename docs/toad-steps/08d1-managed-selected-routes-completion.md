# Toad step 08D1 — managed selected-route completion packet

Status: **IMPLEMENTATION-READY REMEDIATION PLAN — NO ARCHITECTURAL CHOICES LEFT TO THE EXECUTOR**.

Audit date: 2026-11-05.

Reviewed baselines:

- Kikimora master before this packet expansion: 82dfe28;
- Leshy master: 84e6e9ab.

This packet is the authoritative completion plan for 08D. It replaces the earlier remediation checklist with exact data models, transaction ordering, code entry points, failure semantics, test cases, acceptance commands and landing order.

The executor must not invent a different ownership model, persistence model, transport, observer strategy, cutover sequence or retry policy. If a required API named below needs a small signature adjustment for language/library mechanics, preserve the semantics and naming; do not change the architecture.

The old feature branches are not an implementation source. Both repositories' current master branches contain the newest route-registry and 08D work.

---

# 0. Non-negotiable product invariants

## 0.1 One writer

Standalone Leshy:

~~~text
DNS
  -> Leshy RouteRegistry
  -> local platform RouteBackend
  -> kernel FIB
~~~

Kikimora-managed Leshy:

~~~text
DNS
  -> Leshy RouteRegistry
  -> KikimoraRouteBackend
  -> Kikimora control API v3
  -> Kikimora SelectedRouteManager
  -> Kikimora platform RouteManager
  -> kernel FIB
~~~

In managed mode Leshy never calls its local Linux/Windows route writer for selected or static routes. Loss of the Kikimora control socket never changes the backend to local.

## 0.2 Kernel proof, not registry belief

A registry entry saying Active is not by itself proof that the route still exists. A zero-I/O fast path is allowed only while an in-memory verification token proves that the exact kernel identity is still current for the current Toad generation and route generation.

Persisted checkpoints contain ownership evidence and transaction phase. They do not restore the in-memory verification token.

## 0.3 Exact ownership

Same prefix, same ifindex, proto static and metric lower than the park are not ownership proofs.

Every selected-route mutation, removal and unpark decision uses one exact recorded identity:

- canonical prefix;
- main table;
- output ifindex;
- next hop;
- protocol;
- metric;
- Linux route scope where relevant.

A same-prefix row that does not match either the current desired identity or the explicitly recorded previous owned identity is foreign and must never be replaced or deleted.

## 0.4 Crash evidence is never erased early

Every transaction has a persistent state before the first kernel mutation. Explicit retirement has a persistent tombstone before the kernel delete. A kernel/API failure keeps enough registry evidence for the next process to continue safely.

## 0.5 Family independence

IPv4 Ready never implies IPv6 Ready and IPv6 Ready never implies IPv4 Ready. Ensure, restore, parking, observer repair and DNS response policy always evaluate the requested family independently.

## 0.6 No request-path full FIB scan after initialization

A converged DNS request must not enumerate the full route table. For a new unique local route, the request path may perform a targeted prefix read and mutation/read-back, but not a full-family/full-table dump.

Full snapshots are reserved for initialization, Full invalidation, periodic safety audit, recovery/diagnostics and Windows cache resynchronization after notification bursts.

## 0.7 Foreign-route conflict is an integrity failure

When a foreign row occupies a managed prefix, neither Leshy nor Kikimora may overwrite it to restore connectivity. The managed role is frozen/failed, the conflict is exposed diagnostically, and no selected-route ACK is issued for that prefix. This cannot undo a route installed by an external actor; it prevents our stack from falsely claiming or deleting it.

## 0.8 Managed recovery must be reachable in production

The current core.Engine has a ManagedSelectedRoutes selector, but production Manager.recoverRole does not populate it. Therefore the existing managed recovery sequence is presently unreachable outside core tests. This is a P0 wiring defect.

Change the Engine construction in toad/internal/control/control.go::recoverRole to exactly include:

~~~go
err := (core.Engine{
    Controller: m.product,
    Driver: driver,
    Cancelled: transactionCancelled,
    ManagedSelectedRoutes: m.managedSelected,
}).Recover(ctx, name, operation, epoch)
~~~

Do not add a second recovery engine. All managed startup/resume and runtime recovery must reach ManagedRecoverySequenceForAction through this one production entry.

Add a control-level test that drives Manager.recoverRole with a marker-managed role and asserts the driver observes FreezeSelected/Park/WithdrawSelected/.../UnfreezeSelected. A second role without managed activation must continue to receive the legacy sequence.

---

# 1. Fixed Kikimora selected-route model

Entry points:

- toad/internal/routing/selected_registry.go
- toad/internal/routing/selected_registry_test.go
- toad/internal/routing/selected_plan.go
- toad/internal/routing/selected_plan_test.go

The existing schema-1 model stores only interface/ifindex and cannot prove exact ownership after restart.

## 1.1 Exact identity

Add:

~~~go
type SelectedRouteIdentity struct {
    Table    int
    IfIndex  int
    NextHop  string
    Protocol int
    Metric   uint32
    Scope    int
}
~~~

Use JSON names table, ifindex, next_hop, protocol, metric, scope. next_hop and scope may be omitted when zero/empty.

Move the shared managed-route constants out of control/parking packages into toad/internal/routing so registry validation, control, parking and both platform backends use one definition:

~~~go
const (
    ManagedSelectedRouteTable    = 254
    ManagedSelectedRouteProtocol = 4
    ManagedParkMetric     uint32 = 42760
)
~~~

Delete control.selectedRouteProtocol and parking.staticProtocol after all call sites use these shared constants. This avoids a routing -> control dependency and prevents protocol/park values from drifting between packages.

Semantics:

- Table is 254 on Linux and synthetic 254 on Windows.
- IfIndex must be positive.
- NextHop is a canonical IP string. Empty means no next hop.
- Protocol is the exact kernel protocol.
- Metric is exact read-back metric after platform normalization.
- Scope is Linux route scope; Windows writes 0.

Add:

~~~go
func (i SelectedRouteIdentity) Validate(prefix netip.Prefix) error
func (i SelectedRouteIdentity) Equal(other SelectedRouteIdentity) bool

type SelectedParkKind string

const (
    SelectedParkUnreachable SelectedParkKind = "unreachable"
    SelectedParkLoopback    SelectedParkKind = "loopback"
)

type SelectedParkIdentity struct {
    Kind     SelectedParkKind
    Table    int
    IfIndex  int
    NextHop  string
    Protocol int
    Metric   uint32
    Scope    int
}

func (i SelectedParkIdentity) Validate(prefix netip.Prefix) error
func (i SelectedParkIdentity) Equal(other SelectedParkIdentity) bool
~~~

Route identity validation rejects table != 254, IfIndex <= 0, malformed/family-mismatched NextHop, protocol <= 0, and invalid Linux scope.

Park validation is platform-neutral but exact: Table == ManagedSelectedRouteTable, Protocol == ManagedSelectedRouteProtocol, Metric == ManagedParkMetric. Kind unreachable requires IfIndex==0 and empty NextHop; Kind loopback requires IfIndex>0 and a loopback NextHop of the same family. Scope is persisted exactly.

Do not infer ownership from protocol classes.

## 1.2 Route and park are two independent crash-safe transactions

Do not encode parking as a mutually-exclusive selected-route state. During recovery an Active selected route and an Active park coexist until restoration is proven. Route transaction state and park transaction state therefore have separate state machines.

Selected-route state is exactly:

~~~go
const (
    SelectedPending  SelectedRouteState = "pending"
    SelectedApplying SelectedRouteState = "applying"
    SelectedActive   SelectedRouteState = "active"
    SelectedRetiring SelectedRouteState = "retiring"
)
~~~

Park transaction state is exactly:

~~~go
type SelectedParkState string

const (
    SelectedParkPending  SelectedParkState = "pending"
    SelectedParkApplying SelectedParkState = "applying"
    SelectedParkActive   SelectedParkState = "active"
    SelectedParkRetiring SelectedParkState = "retiring"
)

type SelectedPark struct {
    State          SelectedParkState
    Desired        SelectedParkIdentity
    Owned          *SelectedParkIdentity
    RetireDesired  bool
}
~~~

Use snake_case JSON names.

Route meanings:

- SelectedPending: Desired checkpointed; selected-route mutation not authorized.
- SelectedApplying: exact preflight succeeded and durable; selected-route mutation may have started.
- SelectedActive: exact Desired read back and stored as Owned.
- SelectedRetiring: explicit selected-route retirement is durable.

Park meanings:

- nil Park: no managed park transaction exists.
- SelectedParkPending: desired park identity checkpointed; park mutation not authorized.
- SelectedParkApplying: exact park preflight succeeded and durable; park creation/replacement may have started.
- SelectedParkActive: exact desired park read back and stored as Owned.
- SelectedParkRetiring: exact park removal was durably requested and must finish after restart.

The two machines are independent. A selected record may be SelectedActive while Park.State==SelectedParkActive. This is the required restoration state before the selected route is proven to win and the park is retired.

## 1.3 SelectedRoute

Replace with:

~~~go
type SelectedRoute struct {
    Role            string
    Zone            string
    Prefix          netip.Prefix
    Source          SelectedRouteSource
    ToadGeneration  uint64
    RouteGeneration uint64
    Interface       string
    Desired         SelectedRouteIdentity
    Owned           *SelectedRouteIdentity
    State           SelectedRouteState
    RetireDesired   bool
    Park            *SelectedPark
}
~~~

Keep snake_case JSON names.

Do not keep a second authoritative IfIndex outside Desired.

Desired is checkpointed before mutation. Owned is the last exact kernel identity proven by read-back. Target changes update Desired but retain Owned until replacement is verified.

## 1.4 Registry API

Use:

~~~go
type SelectedRouteRegistry interface {
    Reserve(
        role, zone string,
        prefix netip.Prefix,
        source SelectedRouteSource,
        toadGeneration, routeGeneration uint64,
        iface string,
        desired SelectedRouteIdentity,
    ) error

    MarkApplying(role string, prefix netip.Prefix) error
    MarkActive(role string, prefix netip.Prefix, observed SelectedRouteIdentity) error
    MarkRetiring(role string, prefix netip.Prefix) error

    ReservePark(role string, prefix netip.Prefix, desired SelectedParkIdentity, forcePending bool) error
    MarkParkApplying(role string, prefix netip.Prefix) error
    MarkParkActive(role string, prefix netip.Prefix, observed SelectedParkIdentity) error
    MarkParkRetiring(role string, prefix netip.Prefix) error
    ClearPark(role string, prefix netip.Prefix) error

    RestoreRole(checkpoint SelectedRoutesCheckpoint) error

    Snapshot(role string) []SelectedRoute
    SnapshotFamily(role string, family int) []SelectedRoute
    Remove(role string, prefix netip.Prefix)
    RemoveRole(role string)
}
~~~

Rules:

- Reserve always writes Desired.
- Desired/generation change makes Active -> Pending; old Owned remains.
- MarkApplying only SelectedPending -> SelectedApplying.
- MarkActive only SelectedApplying/SelectedActive; observed must equal Desired; then Owned=observed.
- MarkRetiring records the previous route State. It sets RetireDesired=true only when previous State==SelectedApplying; otherwise false. It then sets SelectedRetiring and keeps Desired/Owned/Park evidence. This flag means exact route Desired may have been created by an authorized interrupted syscall and must be considered during deletion.
- ReservePark creates/updates Park.Desired. If Desired changed or forcePending=true, preserve previous Park.Owned and set Park.State=SelectedParkPending. If an identical Active park is reserved with forcePending=false, keep it Active so an exact observation can remain a no-op.
- MarkParkApplying only SelectedParkPending -> SelectedParkApplying.
- MarkParkActive only SelectedParkApplying/SelectedParkActive; observed must equal Park.Desired; then Park.Owned=observed.
- MarkParkRetiring is legal from SelectedParkApplying or SelectedParkActive. It sets Park.RetireDesired=true only when previous park State==SelectedParkApplying; otherwise false; preserves Park.Desired/Owned and sets SelectedParkRetiring. ParkPending is cleared directly during explicit retirement because no park mutation was authorized.
- ClearPark is legal after exact absence was proven, or directly from SelectedParkPending when no park mutation was authorized; it sets Park=nil.
- RestoreRole validates both independent machines before mutating the map.
- Invalid transition is an error, never coerced.

---

# 2. Checkpoint schema 2 and strict restore

Entry points:

- toad/internal/routing/selected_registry.go
- toad/internal/control/selected_routes.go
- toad/internal/control/control.go
- selected_registry_test.go
- selected_routes_test.go
- control_test.go

## 2.1 Schema 2

Set:

~~~go
const selectedRoutesCheckpointSchema = 2

type SelectedRoutesCheckpoint struct {
    Schema          int
    Role            string
    RouteGeneration uint64
    Entries         []SelectedRoute
}
~~~

Remove the schema-1 Parked string array from new writes. Each entry Park transaction is authoritative and independent of the selected-route transaction.

Schema 1 may be decoded only to return an explicit unsupported error. Do not infer exact identity from schema 1.

## 2.2 Validation

Add:

~~~go
func ValidateSelectedRoutesCheckpoint(
    c SelectedRoutesCheckpoint,
    expectedRole string,
    expectedZone string,
) error
~~~

Validate:

- schema == 2;
- checkpoint/entry role match expected role;
- entry zone matches configured zone;
- canonical prefix;
- DNS is /32 or /128;
- static may be wider;
- valid source/state;
- Park is independent of selected route State; when non-nil, Park.State is valid and Park.Desired.Validate(prefix) succeeds;
- Desired.Validate;
- Desired.Protocol == ManagedSelectedRouteProtocol;
- ToadGeneration > 0 for new schema2 records;
- RouteGeneration > 0;
- Active requires Owned and Owned==Desired;
- RetireDesired is false for every non-SelectedRetiring route. For SelectedRetiring it records whether retirement began from SelectedApplying.
- Park != nil does not change selected-route State requirements. Park.Desired.Validate(prefix) always succeeds.
- SelectedParkPending: Park.Owned may contain previous exact ownership after an identity change, but Park.RetireDesired=false; mutation for the new Desired is not authorized.
- SelectedParkApplying: Park.Owned may be nil or previous exact ownership; Park.RetireDesired=false; exact Park.Desired mutation is authorized.
- SelectedParkActive: Park.Owned is non-nil and equals Park.Desired; Park.RetireDesired=false.
- SelectedParkRetiring: Park.Desired/Owned are retained; Park.RetireDesired records whether retirement began from ParkApplying.
- Applying nil Owned is legal;
- SelectedRetiring with Owned nil is valid; RetireDesired tells whether exact selected Desired was previously authorized and must be deleted. If Owned and RetireDesired are both present and Desired differs from Owned, both exact identities are retirement candidates;
- no duplicate prefix.

## 2.3 Strict marker

Replace silent loadManagedMarker with:

~~~go
func (m *Manager) readManagedMarker() (managedModeMarker, bool, error)
func (m *Manager) applyManagedMarker(marker managedModeMarker)
~~~

Missing marker -> exists=false, nil error.

Malformed JSON, unsupported schema, empty socket, unknown/duplicate managed zone -> startup error. Never silently fall back to local mode.

Handshake capability is support, not activation. Ensure/Forget/Flush require role activation from validated marker.

## 2.4 Startup order

Change Manager construction to:

~~~text
1 load/validate role configs
2 build specs
3 create core.Controller
4 strict read leshy-managed.json
5 construct SelectedRouteManager
6 for each marker-managed role:
    read <StateDir>/selected-routes.json
    missing -> empty role
    valid schema2 -> RestoreRole
    corrupt/unsupported -> fail core startup
7 seed role routeGeneration = checkpoint.RouteGeneration + 1, minimum 1
8 restore NO verification token
9 apply marker in memory
10 start product/underlay/sleep/interface observers
~~~

Schema 1:

- no marker -> leave stale file untouched and do not load;
- marker + schema1 -> startup failure with exact message;
- no automatic proto-static adoption.

## 2.5 Restart semantics and startup freeze

Add restorePending bool to selectedRoleState. Restoring any non-empty managed checkpoint sets frozen=true and restorePending=true before the control socket can accept Ensure calls. A role with an empty checkpoint starts with restorePending=false.

- restored Active: persisted owner, unverified in memory;
- Applying: controlled Desired adoption allowed;
- Pending: must preflight before mutation;
- Park Pending/Applying/Active/Retiring is restored exactly and remains startup-frozen until its transaction is reconciled; observer never silently clears it;
- Retiring: deletion completes before role Ready.

Add SelectedRouteManager.CompleteStartupRestore(role). It may clear restorePending/frozen without another generation increment only when: every non-Retiring record is Active, every Active record has a current verification token for the seeded startup route generation, every record has Park == nil, and no Retiring record remains. The seeded checkpoint.RouteGeneration+1 is already the new startup generation.

The selected-route reconciler owns this startup gate. On each audit, once RoleRuntime is Desired, Healthy and family-ready, it reconciles restored Active/Pending/Applying records. When the conditions above are satisfied it calls CompleteStartupRestore. If any record has Park != nil, the reconciler never clears startup freeze or repairs that park transaction in isolation. It calls Manager.requestManagedSelectedRecovery(role, "resume checkpointed managed park transaction") so the existing managed recovery engine re-enters at FreezeSelected -> ParkRegistered and preserves the full Park -> WithdrawSelected -> transport -> RestoreSelected -> ObserveRestore ordering. The 10-second audit retries this request until the role process/driver is available; recoveryInFlight prevents duplicates.

---

# 3. Exact shared planner

Rewrite selected_plan.go/tests. Delete selectedRowProtocolOwned and tests treating protocol 0/3/4 as ownership.

Use:

~~~go
type SelectedKernelRowKind string

const (
    SelectedKernelRoute   SelectedKernelRowKind = "route"
    SelectedKernelPark    SelectedKernelRowKind = "park"
    SelectedKernelForeign SelectedKernelRowKind = "foreign"
)

type SelectedKernelRow struct {
    Kind        SelectedKernelRowKind
    Route       SelectedRouteIdentity
    Park        SelectedParkIdentity
    Description string
}

func SelectedRowMatchesIdentity(
    row SelectedKernelRow,
    identity SelectedRouteIdentity,
) bool

func SelectedParkMatchesIdentity(
    row SelectedKernelRow,
    identity SelectedParkIdentity,
) bool
~~~

Projection invariant: for Kind==SelectedKernelRoute, Route.Validate(prefix) succeeds and Park is zero. For Kind==SelectedKernelPark, Park.Validate(prefix) succeeds and Route is zero. Any same-prefix platform row that cannot be projected into one of those exact forms becomes Kind==SelectedKernelForeign with a stable diagnostic Description. Never drop an unrecognized same-prefix row. The planner treats SelectedKernelForeign as an unconditional conflict.

Each platform classifies a managed park separately from a selected route. Linux projects an RTN_UNREACHABLE row into SelectedParkIdentity. Windows projects only the exact loopback park shape into SelectedParkIdentity.

Park-row recognition is state-dependent and exact:

- record.Park == nil: every park row is foreign;
- SelectedParkPending: exact previous Park.Owned is recognized and may remain in the kernel because it was verified before this Pending transaction; a distinct exact Park.Desired is foreign because the new mutation was not authorized; with nil Park.Owned every park row is foreign;
- SelectedParkApplying: a row equal to Park.Desired is adoptable; a row equal to previous Park.Owned is recognized previous ownership; anything else is foreign;
- SelectedParkActive: only a row equal to Park.Owned (which checkpoint validation requires to equal Park.Desired) is the authorized current park;
- SelectedParkRetiring: exact Park.Owned is a retirement candidate; exact Park.Desired is an additional retirement candidate only when Park.RetireDesired=true. Anything else is foreign.

Recognized park rows are excluded from selected-route ownership classification. They are never treated as Selected Owned and never satisfy route Desired. After excluding only those exact checkpoint-authorized park rows, classify every remaining row as selected Desired exact, previous selected Owned exact, or foreign.

Rules in order:

0. any SelectedKernelForeign -> conflict.
1. >1 Desired exact -> conflict.
2. any foreign -> conflict even if Desired exists.
3. one Desired and no stale Owned -> None.
4. no rows -> Add.
5. previous Owned only -> Replace.
6. Desired + distinct previous Owned -> Replace to clean stale Owned.
7. nil record -> every existing row foreign.
8. nil Owned -> every existing row foreign except durable Applying may adopt exactly Desired.
9. Pending nil Owned never adopts an existing selected row.
10. A managed park may coexist only under the park-state rules above; it never satisfies selected Desired and never counts as selected Owned.

Required tests:

- explicit SelectedKernelForeign row -> conflict;
- nil record + exact Desired -> conflict;
- Pending nil Owned + exact Desired -> conflict;
- Applying nil Owned + exact Desired -> None/adoptable;
- Active Owned==Desired + exact -> None;
- old Owned -> Replace;
- Desired + old Owned -> Replace;
- wrong gateway/protocol/metric/scope -> conflict;
- same ifindex/proto 4 without exact Owned -> conflict;
- duplicate Desired -> conflict;
- Desired + foreign -> conflict;
- Park nil + park row -> conflict;
- Park Pending + nil Owned + exact Park.Desired already present -> conflict;
- Park Pending + exact previous Park.Owned only -> allowed preflight state;
- Park Pending + previous Owned plus distinct pre-existing Desired -> conflict;
- Park Applying + exact Park.Desired + empty selected set -> Add for selected route; park is adoptable separately;
- Park Active + exact Park.Owned + Desired selected row -> None for selected route;
- Park Active + different park identity -> conflict;
- Park Retiring recognizes exact Park.Owned and, only when Park.RetireDesired=true, exact Park.Desired for retirement cleanup;
- empty -> Add.

---

# 4. Platform contract

Modify toad/internal/platform/contracts.go and every implementation/fake.

Add:

~~~go
type SelectedReconcileResult struct {
    Mutated  bool
    Verified SelectedRouteIdentity
}

type SelectedParkReconcileResult struct {
    Mutated  bool
    Verified SelectedParkIdentity
}

type SelectedRouteInvalidationKind string

const (
    SelectedInvalidatePrefix    SelectedRouteInvalidationKind = "prefix"
    SelectedInvalidateInterface SelectedRouteInvalidationKind = "interface"
    SelectedInvalidateFull      SelectedRouteInvalidationKind = "full"
)

type SelectedRouteInvalidation struct {
    Kind    SelectedRouteInvalidationKind
    Prefix  netip.Prefix
    IfIndex int
}
~~~

Selected RouteManager surface becomes:

~~~go
NormalizeSelectedRoute(
    context.Context,
    routing.SelectedRouteDesired,
) (routing.SelectedRouteIdentity, error)

PreflightSelectedRoute(
    context.Context,
    netip.Prefix,
    routing.SelectedRouteIdentity,
    *routing.SelectedRoute,
) error

ReconcileSelectedRoute(
    context.Context,
    routing.SelectedRouteDesired,
    *routing.SelectedRoute,
) (routing.SelectedReconcileResult, error)

RemoveSelectedRoute(context.Context, routing.SelectedRoute) error

NormalizeManagedPark(
    context.Context, netip.Prefix,
) (routing.SelectedParkIdentity, error)

PreflightManagedPark(
    context.Context, netip.Prefix, routing.SelectedParkIdentity, *routing.SelectedPark,
) error

ReconcileManagedPark(
    context.Context, string, netip.Prefix, *routing.SelectedPark,
) (routing.SelectedParkReconcileResult, error)

ObserveManagedPark(
    context.Context, netip.Prefix, routing.SelectedParkIdentity,
) (bool, error)

RemoveManagedPark(
    context.Context, string, netip.Prefix, routing.SelectedPark,
) error

SnapshotSelectedRoutes(
    context.Context,
) (map[netip.Prefix][]routing.SelectedKernelRow, error)

SubscribeSelectedRouteInvalidations(
    context.Context,
) (<-chan routing.SelectedRouteInvalidation, error)
~~~

Keep endpoint/parking/Snapshot methods.

Define both errors in toad/internal/platform/contracts.go:

~~~go
var ErrSelectedRouteUnsupported = errors.New("selected-route writer unsupported")
var ErrSelectedRouteObserverUnsupported = errors.New("selected-route observer unsupported")
~~~

Adapt every implementation in the same K13 commit so the repository cross-compiles:

- Linux and Windows implement the full surface.
- darwinRouteManager returns ErrSelectedRouteUnsupported from every selected-route/managed-park/SnapshotSelectedRoutes method and ErrSelectedRouteObserverUnsupported from Subscribe. Managed activation must reject Darwin because no writer exists.
- default_routes_unsupported.go does the same and must use fully qualified routing.SelectedRouteDesired plus the required imports; do not leave build-tagged compile debt.
- every test fake implements all new methods explicitly; no embedded silent no-op.

Unsupported observer means periodic audit only on a platform that has a writer; unsupported writer means managed activation denied.

## 4.1 Normalize

No mutation.

Linux exact Desired:

- table 254;
- desired IfIndex;
- gateway string/empty;
- protocol;
- selectedRouteMetric; IPv6 0 -> 1024;
- link scope for device, universe for gateway.

Windows exact Desired:

- build same MIB row actual Create uses;
- project actual InterfaceIndex/NextHop/Protocol/Metric;
- table 254;
- scope 0.

Windows on-link encoded source NextHop remains exact; do not turn it into empty.

## 4.2 Preflight

Selected-route Preflight reads only exact prefix/current cache.

- record Owned exists -> only selected Desired and previous selected Owned rows are allowed, plus checkpoint-authorized park rows under section 3;
- no selected Owned -> selected prefix must otherwise be empty; even Desired-identical pre-existing selected row conflicts unless selected State==Applying during crash recovery.

Park Preflight follows the same crash boundary independently:

- Park nil or Park Pending with no previous Owned -> no park row may already exist;
- Park Pending with previous Owned -> exact previous Owned is allowed; distinct Park.Desired is not allowed before Applying;
- Park Applying with previous Owned -> exact Park.Desired and exact previous Park.Owned are allowed;
- Park Applying during recovery may adopt exact Park.Desired;
- any unrecorded park/park-looking row conflicts.

Only after the respective Preflight succeeds may control persist SelectedApplying or SelectedParkApplying.

## 4.3 Full selected snapshot

SnapshotSelectedRoutes is a live full-kernel observation for safety audit/recovery, not a request-path helper. It returns exact selected-route and managed-park projections grouped by canonical prefix, refreshes the platform selected cache, and includes foreign same-prefix rows rather than filtering them away. The 10-second audit calls it exactly once per audit.

---

# 5. SelectedRouteManager exact transactions

Entry: toad/internal/control/selected_routes.go and tests.

## 5.1 Ensure exact order

~~~text
1 zone -> exactly one role
2 reject unless managed-enabled
3 canonicalize prefix/source
4 family
5 lock role
6 reject frozen
7 RoleRuntime
8 require Desired
9 require Validation.Healthy
10 require family Ready
11 require ToadGeneration > 0
12 require current ifindex > 0
13 capacity
14 inFlight++
15 unlock
16 applyMu lock
17 build SelectedRouteDesired
18 NormalizeSelectedRoute
19 inspect registry
20 valid verification token -> already-applied, zero I/O
21 Reserve(real ToadGeneration, RouteGeneration, Desired)
22 checkpoint Pending
23 PreflightSelectedRoute
24 MarkApplying
25 checkpoint Applying
26 ReconcileSelectedRoute
27 require Verified == Desired
28 MarkActive(Verified)
29 checkpoint Active
30 verification token
31 counters from Mutated
32 unlock applyMu
33 inFlight-- + broadcast
34 return applied/already-applied
~~~

Failure:

- before Reserve: no record change;
- after Pending: keep Pending;
- preflight conflict: Pending + conflict;
- after Applying: keep Applying;
- post-kernel checkpoint error: keep Applying evidence, return error;
- never erase failed transaction evidence.

Wire state:

- Mutated true -> applied;
- false -> already-applied;
- conflict -> conflict;
- safety denial -> denied.

## 5.2 Verification token

Add per-role map:

~~~go
type selectedVerification struct {
    ToadGeneration  uint64
    RouteGeneration uint64
    Identity        routing.SelectedRouteIdentity
}
~~~

selectedRoleState also gains restorePending bool. It is true only for non-empty ownership restored from disk and is cleared by CompleteStartupRestore or the normal recovery Unfreeze path.

Fast path requires Active, Owned==Desired, token exists, generations current, Identity==Owned.

Checkpoint restore creates no token. Prefix/Interface/Full invalidation clears matching tokens.

## 5.3 Real Toad generation

Replace Reserve(..., 0, ...). Use runtime.ToadGeneration. Zero generation denies Ensure/Restore.

## 5.4 Forget

Forget uses the single retirement helper specified in section 7.6. That helper owns selected-row deletion, independent park-transaction retirement, checkpoints after every transition, registry removal and verification cleanup. Do not maintain a second simplified Forget path.

Any failure leaves SelectedRetiring plus the exact remaining selected/Park transaction evidence. A Retiring prefix cannot be Ensure'd until retirement completes.

## 5.5 FlushZone

Use same retirement helper per entry. Failed delete keeps Retiring/checkpoint. Return first error.

## 5.6 Restore

Per Ready-family record:

~~~text
current runtime/generation
Desired on current ifindex
Normalize
Reserve -> Pending checkpoint
Preflight
MarkApplying -> checkpoint
Reconcile
MarkActive(Verified) -> checkpoint
verification token
~~~

When recovery reaches RestoreSelected, every protected record must have Park.State==SelectedParkActive with Park.Owned==Park.Desired. That park transaction remains Active throughout selected restore. Non-ready families are not restored and keep the Active park. SelectedRetiring records are never restored. ObserveRestoration owns the later ParkActive -> ParkRetiring -> nil transition.

## 5.7 WithdrawSelected manager method

Add exactly:

~~~go
func (m *SelectedRouteManager) WithdrawSelected(ctx context.Context, role string) error
~~~

Preconditions: role is frozen and every non-Retiring record has Park.State==SelectedParkActive with non-nil Park.Owned==Park.Desired. Validate all records and exact Park.Owned observations before deleting any selected row. The method acquires role applyMu, snapshots the registry, and removes only exact selected identities through platform.RemoveSelectedRoute. It does not MarkRetiring, remove registry ownership, clear selected Owned or mutate the park transaction. RemoveSelectedRoute verifies exact selected absence before success.

If any required record has no Active exact park, return an error before deleting any selected route; recovery must never create a window with neither selected route nor a verified park.

## 5.8 Unfreeze

Last recovery action; routeGeneration++; clear role verification; clear restorePending; publish generation; wake producers.

---

# 6. Crash semantics

Pending: mutation unauthorized. Preflight reruns; nil Owned + any existing row conflicts.

Applying: mutation authorized. Exact Desired may be adopted; previous exact Owned may be replaced; anything else conflicts.

Active: restored ownership has no fast proof until observer/audit re-verifies.

The park transaction is orthogonal to selected-route State:

- ParkPending: park mutation unauthorized; existing unrecorded park conflicts.
- ParkApplying: park mutation authorized; exact Park.Desired may be adopted/replaced from previous Park.Owned.
- ParkActive: exact Park.Owned==Park.Desired is proven.
- ParkRetiring: exact park removal was durably requested; absent completes safely.

Selected restore may move selected Pending/Applying -> Active while Park remains ParkActive. ObserveRestoration or explicit retirement cleanup performs ParkActive -> ParkRetiring -> nil.

SelectedRetiring: delete exact selected Owned and, only when RetireDesired=true, exact selected Desired; absent authorized identities succeed; foreign rows remain untouched; remove registry only after all authorized selected identities are absent and Park == nil.

---

# 7. Managed parking, selected withdrawal and exact restoration

Managed parking is part of SelectedRouteManager, not parking.Manager. This is a fixed architecture choice. The current parking.Manager remains the legacy baseline/delta parking implementation only. Delete the managed-only PrepareRegisteredWithdrawal, registeredRowVerified and RegisteredRestoration methods after their tests/callers move. Do not keep two managed parking state stores.

Entry points:

- toad/internal/control/selected_routes.go;
- selected_routes_test.go;
- toad/internal/control/recovery_driver.go;
- recovery_driver_test.go;
- toad/internal/parking/manager.go: remove managed registered-route helpers, leave legacy path;
- toad/internal/core/recovery.go;
- toad/internal/core/recovery_runner.go::ManagedRecoverySequenceForAction;
- toad/internal/core/engine.go and sequence tests.

## 7.1 Add managed park methods to SelectedRouteManager

Add exactly:

~~~go
func (m *SelectedRouteManager) ParkRegistered(ctx context.Context, role string) error
func (m *SelectedRouteManager) WithdrawSelected(ctx context.Context, role string) error
func (m *SelectedRouteManager) ObserveRestoration(ctx context.Context, role string) error
func (m *SelectedRouteManager) ParkingState(role string) parking.State
~~~

The control package already depends on parking elsewhere; using parking.State for product reporting creates no routing-package cycle. Registry/checkpoint remains the authority: ParkingState contains no independent mutable ownership map.

ParkingState derivation is fixed:

- Prefixes contains only records whose Park.State is SelectedParkActive or SelectedParkRetiring and Park.Owned != nil;
- Count == len(Prefixes);
- Active is true only when every non-Retiring selected record that recovery intends to protect has SelectedParkActive with exact Owned==Desired;
- ParkPending/ParkApplying are not reported as proven protection.

This prevents UI/product state from claiming fail-closed protection before exact park read-back.

## 7.2 ParkRegistered exact transaction

Precondition: role is frozen. Acquire the role applyMu for the whole operation. Iterate a stable registry snapshot in canonical prefix order. Ignore selected records in SelectedRetiring; retirement owns their cleanup.

For each remaining record, normalize the current platform park identity first:

~~~text
parkDesired = routes.NormalizeManagedPark(prefix)
~~~

Then resume according to the persisted park transaction state:

### Park == nil

~~~text
ReservePark(parkDesired, forcePending=false)
checkpoint ParkPending
PreflightManagedPark
MarkParkApplying
checkpoint ParkApplying
ReconcileManagedPark
require Verified == Park.Desired
MarkParkActive(Verified)
checkpoint ParkActive
~~~

### ParkPending

Use the checkpointed Park.Desired. If current NormalizeManagedPark differs, call ReservePark(newDesired, forcePending=true) and checkpoint the updated Pending record first. Then run Preflight -> MarkParkApplying/checkpoint -> Reconcile -> MarkParkActive/checkpoint.

### ParkApplying

Do not run the Pending preflight again: mutation was already authorized. Call ReconcileManagedPark with the Applying record. The platform may adopt exact Park.Desired, replace exact previous Park.Owned, or create Desired when absent. Any other row conflicts. Then MarkParkActive and checkpoint.

### ParkActive

If parkDesired == Park.Desired and routes.ObserveManagedPark(prefix,*Park.Owned) proves the exact Owned park still exists, continue with zero mutation.

If the exact Active park is missing or current parkDesired changed, call ReservePark(parkDesired, forcePending=true), preserving old Park.Owned, checkpoint Pending, then execute the full Pending transaction. The platform creates/verifies new Desired before deleting stale exact old Owned, so changing Windows loopback identity never opens a protection hole while the selected route still exists.

### ParkRetiring

Complete the interrupted retirement first: RemoveManagedPark with the persisted Park transaction, prove the exact old park absent, ClearPark, checkpoint. Then start the nil-Park transaction above because this recovery still requires protection before WithdrawSelected.

Every transition that changes Park state or identity is checkpointed before the next kernel mutation. There is no kernel park creation/deletion window without a durable Pending/Applying/Retiring record.

If a later prefix fails, previously Active parks remain checkpointed; recovery stays frozen and retry resumes by state. Never roll back already established fail-closed protection because a later prefix failed.

## 7.3 Add explicit WithdrawSelected recovery step

Current RecoveryWithdraw only removes legacy .dev publication. It does not remove old selected kernel rows. A high-metric park cannot become the winning fail-closed path while a lower-metric selected row still exists.

Add:

~~~go
RecoveryWithdrawSelected RecoveryStep = "withdraw-selected"
~~~

and extend RecoveryDriver with:

~~~go
WithdrawSelected(context.Context, string) error
~~~

Managed recovery order becomes exactly:

~~~text
FreezeSelected
ObserveRoutes
Park
WithdrawSelected
Withdraw
Quiesce
ApplyEndpoint
Rebind / StartTransport
Validate
RestoreSelected
ObserveRestore
Publish
ResyncLeshy
UnfreezeSelected
~~~

recoveryDriver.Park for a managed role calls selectedRoutes.ParkRegistered and publishes selectedRoutes.ParkingState(role). It never calls parking.Manager on the managed path.

recoveryDriver.WithdrawSelected is a thin adapter to selectedRoutes.WithdrawSelected.

WithdrawSelected preconditions: role frozen and every non-Retiring record has Park != nil, Park.State==SelectedParkActive, Park.Owned != nil and Park.Owned==Park.Desired. Validate all records and ObserveManagedPark every exact Park.Owned before deleting any selected row. Then, under applyMu, remove each exact selected identity through routes.RemoveSelectedRoute. It does not MarkRetiring, remove the registry record, clear Owned or clear Park. RemoveSelectedRoute itself verifies exact absence before success.

After all removals, verify every Park.Owned still exists through ObserveManagedPark. The step succeeds only when selected identities are absent and exact persisted parks are present. Then the park is the intended winning same-prefix protection.

Failure deleting any selected identity stops recovery before legacy publication withdrawal and before Quiesce.

## 7.4 Restore while Park remains persisted

RestoreSelected may move Pending/Applying -> Active while Park remains non-nil. At this stage Park must be SelectedParkActive with non-nil Owned==Desired; the selected planner ignores only a row exactly equal to Park.Owned. The park is never selected Owned and never satisfies selected Desired.

Non-ready families are not restored and keep their exact Park.

## 7.5 ObserveRestoration and exact unpark

SelectedRouteManager.ObserveRestoration iterates entries with Park != nil under applyMu. Park transaction state is handled explicitly.

### ParkPending or ParkApplying

These states mean protection setup was interrupted. Keep the role frozen and return parking.ErrRoutesStillParked. The next recovery retry must return through ParkRegistered to finish park establishment before any unpark decision. Do not clear or skip them.

### ParkActive

Require all before retiring the park:

1. selected record State == SelectedActive;
2. selected Owned != nil and Owned == Desired;
3. selected kernel row exactly equals Owned;
4. Park.Owned != nil and Park.Owned == Park.Desired;
5. routes.ObserveManagedPark(prefix,*Park.Owned) proves exact park present, or it is already absent as handled below;
6. no foreign same-prefix row exists;
7. selected identity wins over ManagedParkMetric.

Then:

~~~text
MarkParkRetiring
checkpoint ParkRetiring
RemoveManagedPark(ctx, role, prefix, *record.Park)
verify exact selected remains
verify persisted Park.Owned is absent
ClearPark
checkpoint Park=nil
~~~

If the expected Active park is already absent but the exact selected identity is Active/Owned/Desired and no foreign row exists, still MarkParkRetiring + checkpoint first, call RemoveManagedPark idempotently, then ClearPark + checkpoint. This records the interrupted/external absence before forgetting park ownership.

If selected exact identity is not proven, missing expected park is an integrity failure: keep the role frozen/non-Ready and do not ClearPark.

### ParkRetiring

Resume exact RemoveManagedPark. Absent is success. Once exact absence is proven while selected remains exact, ClearPark and checkpoint.

Return parking.ErrRoutesStillParked while any entry still has Park != nil.

recoveryDriver.ObserveRestoration for managed roles calls selectedRoutes.ObserveRestoration and publishes selectedRoutes.ParkingState(role). It never calls legacy parking.Manager registered-route helpers.

## 7.6 Forget/Flush retirement with Park

Retirement owns both selected and park transactions. Exact order for one record:

~~~text
MarkRetiring
checkpoint selected Retiring
RemoveSelectedRoute exact selected Owned plus Desired iff selected RetireDesired=true

if Park == nil:
    continue

if Park.State == SelectedParkPending:
    # park mutation was never authorized
    ClearPark
    checkpoint selected Retiring + Park=nil

if Park.State == SelectedParkApplying or SelectedParkActive:
    MarkParkRetiring
    checkpoint ParkRetiring
    RemoveManagedPark using persisted Park State/Owned/RetireDesired rules
    prove exact park absent
    ClearPark
    checkpoint selected Retiring + Park=nil

if Park.State == SelectedParkRetiring:
    resume RemoveManagedPark
    prove exact park absent
    ClearPark
    checkpoint selected Retiring + Park=nil

registry.Remove
checkpoint absence
clear verification
~~~

Any selected delete, park delete or checkpoint error keeps the remaining Retiring/Park evidence. Restart continues from it. Never delete the selected registry record while Park != nil.

FlushZone calls this helper record-by-record and retains every failed Retiring record.

releaseManagedSelected uses FlushZone and derives an empty ParkingState afterward. It never calls parking.Manager.Clear on the managed path.

## 7.7 Required tests

- Pending + no selected row -> exact park created and checkpointed;
- Applying + Desired exact -> park;
- Active + Owned exact -> park;
- per-prefix checkpoint after each park, with failure on second prefix;
- crash after ParkPending checkpoint before preflight -> no park row authorized; restart preflights again;
- crash after ParkApplying checkpoint before kernel create -> restart may create/adopt exact Park.Desired;
- crash after kernel park create before MarkParkActive -> ParkApplying persists and restart adopts exact Park.Desired;
- recorded Park missing before WithdrawSelected -> fail before any selected delete;
- park + selected coexist before WithdrawSelected;
- WithdrawSelected removes selected exact only and all persisted parks remain;
- failure deleting selected stops before Withdraw/Quiesce;
- wrong metric/gateway/protocol same-prefix -> conflict;
- foreign row never withdrawn;
- RestoreSelected succeeds while exact persisted Park coexists;
- Active restore keeps Park != nil;
- ObserveRestoration exact unpark + checkpoint per prefix;
- crash after MarkParkRetiring before park delete -> restart resumes exact delete;
- crash after park delete before ClearPark -> ParkRetiring persists and absent exact park completes safely;
- expected park absent + selected not exact -> integrity failure;
- crash after Park before WithdrawSelected resumes frozen;
- crash after WithdrawSelected before Quiesce sees selected absent + exact Park present;
- selected Retiring with ParkPending clears unstarted park state without a delete;
- selected Retiring with ParkApplying/Active/Retiring completes exact park retirement before registry removal;
- park delete failure retains Retiring/Park evidence;
- static /24 and /64 follow identical rules;
- managed recovery never calls legacy parking.Manager methods.

---

# 8. Kikimora Linux selected writer

Entry: toad/internal/platform/linux/netlink/routes.go. Keep kernelMu.

Project selected identity: table 254, LinkIndex, Gw, Protocol, Priority, Scope.

When projecting same-prefix rows, an RTN_UNREACHABLE row in main table with ManagedSelectedRouteProtocol and ManagedParkMetric projects to SelectedKernelPark with its full observed SelectedParkIdentity. Do not decide ownership during projection; section 3 compares that identity against persisted Park state. A normal route that can be fully represented by a valid SelectedRouteIdentity projects to SelectedKernelRoute even when it will later compare foreign. Any other same-prefix row, including an unreachable/prohibit/blackhole row with the wrong managed-park identity, projects to SelectedKernelForeign and is never dropped.

NormalizeSelectedRoute uses selectedRouteMetric + selectedRouteFor without mutation.

Preflight uses selectedRouteRows(family,prefix), already filtered; no full FIB.

Reconcile:

- re-read prefix;
- exact planner;
- None -> Mutated=false + Verified;
- Add -> RouteAdd;
- Replace -> only exact Owned proof then RouteReplace;
- re-read;
- exactly one Desired, no stale Owned/foreign;
- EEXIST/EBUSY -> re-read/classify, never unconditional success.

RemoveSelectedRoute is state-aware and exact: delete previous Owned when present; for SelectedApplying delete exact Desired as an authorized in-flight candidate; for SelectedPending never delete Desired; for SelectedRetiring delete exact Owned plus exact Desired only when RetireDesired=true. If Desired==Owned, issue one delete. Every candidate is re-read/identity-checked before deletion; foreign rows remain untouched.

Linux managed-park methods run under kernelMu:

- NormalizeManagedPark builds the deterministic RTN_UNREACHABLE identity for the prefix without mutation: table ManagedSelectedRouteTable, protocol ManagedSelectedRouteProtocol, metric ManagedParkMetric, IfIndex=0, empty NextHop and the same Scope the actual netlink route builder will use.
- PreflightManagedPark reads the exact prefix and applies the independent park-state rules from section 4.2.
- ReconcileManagedPark is called only with Park.State==SelectedParkApplying. It re-reads the prefix, permits exact Park.Desired and exact previous Park.Owned only, creates/replaces the unreachable row, requires exact read-back, and removes stale previous Park.Owned only after Desired is proven. EEXIST is re-read/classified, never unconditional success.
- ObserveManagedPark compares an expected persisted identity exactly.
- RemoveManagedPark accepts the persisted SelectedPark transaction. SelectedParkPending performs no delete. SelectedParkApplying may delete exact Desired and previous Owned because mutation was authorized. SelectedParkActive deletes exact Owned. SelectedParkRetiring deletes exact Owned plus exact Desired only when Park.RetireDesired=true. Deduplicate equal identities, re-read before delete and verify all authorized identities absent. Different same-prefix rows are untouched/conflict.
- SnapshotSelectedRoutes performs one live full kernel snapshot, projects both selected rows and parks, includes foreign rows, and refreshes any Linux selected cache used by targeted reads.

---

# 9. Kikimora Windows selected writer

Entry: toad/internal/platform/windows/routes/routes.go.

Do not use generic applyOperation replacement for selected routes.

Add selectedIdentityFromRow and selectedRowMatchesIdentity.

Add park projection that recognizes the writer's deterministic Windows park shape: main table, loopback NextHop 127.0.0.1 or ::1 of the matching family, Protocol == ManagedSelectedRouteProtocol and Metric == ManagedParkMetric. Project it to SelectedKernelPark with the observed loopback ifindex and full SelectedParkIdentity. Ownership is decided later by exact comparison with Park.Desired/Owned; do not use current loopbackIfIndex as ownership proof. Any normal forwarding row representable by a valid SelectedRouteIdentity becomes SelectedKernelRoute; any other same-prefix row becomes SelectedKernelForeign. Never filter it away.

NormalizeSelectedRoute builds the same row actual Create uses, preserving Windows on-link NextHop.

## 9.1 Selected cache

Executor fields:

~~~go
selectedCache map[netip.Prefix][]routing.SelectedKernelRow
selectedCacheReady bool
selectedCacheDirty bool
~~~

Protected by Executor.mu.

Initialization:

1. register callback;
2. one AF_UNSPEC snapshot;
3. build cache;
4. callback during snapshot -> repeat;
5. ready only when no outstanding dirty.

Use existing winipcfg.RegisterRouteChangeCallback; it wraps NotifyRouteChange2(AF_UNSPEC) and Unregister. Do not implement a second native callback.

Callback copies invalidation material, marks dirty, emits event; no scan/mutation. Unregister outside callback.

Preflight uses ready cache; dirty -> one AF_UNSPEC refresh.

## 9.2 Exact Replace

~~~text
classify
foreign -> conflict
Desired absent -> Create Desired
refresh
require Desired
delete stale row only if exact record.Owned
refresh
require exactly one Desired/no stale/no foreign
return Verified
~~~

Create/verify Desired before deleting old Owned.

ERROR_OBJECT_ALREADY_EXISTS -> refresh/classify. Desired only succeeds; previous Owned+Desired cleans old; foreign conflicts. No broad deletion.

Remove selected identities only as authorized by State/Owned/RetireDesired: Pending never deletes Desired; Applying may delete Desired; Active deletes Owned; Retiring deletes Owned plus Desired iff RetireDesired=true.

Windows managed-park methods use the existing loopbackIfIndex/row builder but follow the same four-state park transaction:

- NormalizeManagedPark resolves the current loopback once and returns the exact desired loopback Park identity without mutation.
- PreflightManagedPark classifies cached/current same-prefix park rows against Park.Desired/Owned and state.
- ReconcileManagedPark, only for ParkApplying, creates/verifies new Desired before deleting stale exact previous Owned. This is mandatory when loopback ifindex changed. ERROR_OBJECT_ALREADY_EXISTS triggers cache refresh and exact classification.
- ObserveManagedPark compares against the persisted expected identity, never against a newly recomputed loopback row.
- RemoveManagedPark deletes only identities authorized by persisted Park State/Owned/RetireDesired and verifies absence; it does not call loopbackIfIndex to decide what to delete.
- SnapshotSelectedRoutes forces one live AF_UNSPEC refresh, projects selected rows, exact parks and foreign rows, then refreshes selectedCache.

## 9.3 Windows direct tests

Using routeTable/createRoute/deleteRoute/unicastAddr seams/counters:

- Normalize v4/v6/gateway;
- exact -> no mutation;
- empty -> one create;
- old Owned ifindex/gateway/metric replace;
- Desired+stale Owned cleanup;
- foreign wrong ifindex/gateway/protocol/metric zero mutation;
- duplicate Desired conflict;
- ParkActive + exact Park.Owned coexists with selected restore;
- ParkPending + pre-existing exact Park.Desired conflicts because mutation was not authorized;
- ParkApplying + exact Park.Desired is adoptable;
- ParkActive + different loopback ifindex/NextHop park conflicts;
- Park nil + park-looking row conflicts;
- EEXIST Desired vs foreign;
- exact/absent/foreign removal;
- v4/v6 parity;
- cache init once;
- 1000 duplicate reconcile -> zero scan/mutation after convergence;
- callback v4/v6;
- cancellation unregister;
- park Pending -> Applying -> Active with checkpoint-state seam;
- ParkApplying crash recovery adopts exact Desired;
- active park loopback change creates/verifies new Desired before deleting previous exact Owned;
- ParkRetiring removal uses persisted old loopback identity even when loopbackIfIndex() now returns another index;
- different park-looking same-prefix row is not deleted.

---

# 10. Kikimora selected reconciler and 10-second audit

Add toad/internal/control/selected_route_reconciler.go and selected_route_reconciler_test.go.

In runServe use this exact production order:

~~~text
routeManager, executor := platform.DefaultRouteManager()
manager.SetSelectedRouteWriter(routeManager)
manager.SetRecoveryDriver(NewRecoveryDriver(...))
manager.StartSelectedRouteReconciler(ctx)
load/restore desired lifecycle state
control.Serve(...)
~~~

The recovery driver must exist before route events/startup-resume can request recovery.

## 10.1 requestManagedSelectedRecovery

Add Manager helper:

~~~go
func (m *Manager) requestManagedSelectedRecovery(role, reason string)
~~~

Exact behavior:

1. Read the product RoleRuntime.
2. Lock Manager and require: role exists, enabled, process != nil, recoveryDriver != nil, recoveryInFlight == false.
3. Capture driver, current underlay Epoch and current product Operation; set role.recoveryInFlight=true; unlock.
4. Call product.SetRecovery(role, current.Recovery, core.RoleRecovering, reason). If it fails, clear recoveryInFlight and return.
5. Start exactly one goroutine calling m.recoverRole(driver, role, operation, epoch).
6. m.recoverRole keeps ownership of recoveryInFlight cleanup/retry behavior; do not build another recovery runner.

If prerequisites are not ready, leave restorePending/frozen unchanged. The 10-second audit retries the request.

Also fix m.recoverRole Engine construction as section 0.8 requires: ManagedSelectedRoutes: m.managedSelected.

## 10.2 Native event handling

Debounce native route events by exactly 100ms.

Prefix invalidation:

- clear matching selected verification token immediately;
- if restorePending && record.Park != nil: requestManagedSelectedRecovery and do not mutate selected/park state in the observer;
- if frozen && !restorePending: recovery engine owns the prefix; observe only, no repair/unpark;
- if not frozen: SelectedActive/SelectedApplying with managed+Desired+Healthy+family Ready -> targeted selected reconcile; SelectedPending -> preflight/continue; SelectedRetiring -> continue exact retirement;
- observer never independently clears a Park transaction.

Interface invalidation:

- clear verification tokens whose selected Desired/Owned uses the ifindex;
- if an exact Park identity uses the ifindex, treat it as affected too;
- an interface/Toad identity change requests the normal managed recovery sequence; never rebind records to an arbitrary current interface from the observer.

Full invalidation:

- clear all verification tokens;
- run one full audit.

## 10.3 Periodic safety audit

Fixed interval: 10 seconds. Use an injectable ticker in tests.

Each audit calls routes.SnapshotSelectedRoutes(ctx) exactly once. Do not call generic Snapshot per record and do not perform a per-record full FIB read. Index the returned exact rows by prefix.

For each marker-managed role/record:

- restorePending + Park != nil -> requestManagedSelectedRecovery; do not unpark in audit;
- frozen normal recovery -> verify observations/diagnostics only, no competing mutation;
- SelectedActive exact selected row and no foreign -> refresh verification token;
- SelectedActive missing/stale selected row, role not frozen, Healthy + family Ready -> exact targeted repair;
- SelectedApplying, role not frozen -> complete authorized selected transaction;
- SelectedPending, role not frozen -> preflight/continue;
- SelectedRetiring -> continue exact selected+park retirement; this cleanup is allowed even when frozen because it reduces explicitly retired ownership;
- ParkPending/ParkApplying outside a normal recovery but with restorePending -> request full managed recovery, not a park-only mutation;
- ParkActive with restorePending -> request full managed recovery;
- ParkRetiring during startup -> resume exact park removal only when selected state proves the prior ObserveRestoration/retirement context; otherwise request full recovery and remain frozen;
- any foreign same-prefix row -> failSelectedRouteIntegrity.

After a non-Park startup checkpoint has all non-Retiring selected records Active with current verification, no Retiring remains and Park==nil for every record, call CompleteStartupRestore(role).

## 10.4 Observer loss

If native subscription exits unexpectedly:

- ObserverErrors++;
- clear all verification tokens;
- immediate one-shot full audit;
- resubscribe after 1s,2s,4s,8s,... capped at 30s;
- periodic 10-second audit continues independently;
- no busy loop.

## 10.5 Integrity failure

Add:

~~~go
func (m *Manager) failSelectedRouteIntegrity(role string, err error)
~~~

It:

1. freezes selected Ensure for the role;
2. records error in role diagnostics/last-error surface;
3. enters the existing failByConfiguration/disabled safety path rather than inventing a new RoleState;
4. never deletes/replaces the foreign row;
5. keeps selected/Park checkpoint evidence.

## 10.6 Diagnostics

Extend SelectedRouteStats with:

~~~go
Unverified     int
Retiring       int
ParkPending    int
ParkApplying   int
ParkActive     int
ParkRetiring   int
Audits         uint64
DriftRepairs   uint64
ObserverErrors uint64
~~~

Definitions:

- DuplicateHits: only current verification-token fast path or platform Mutated=false exact proof;
- KernelMutations: selected/park platform result Mutated=true;
- DriftRepairs: observer/audit repairs a previously Active selected row;
- Audits: once per full 10-second audit, not per route.

---

# 11. Recovery ordering

The managed sequence is fixed and includes the new explicit selected-row withdrawal step:

~~~text
FreezeSelected
ObserveRoutes
Park
WithdrawSelected
Withdraw
Quiesce
ApplyEndpoint
Rebind / StartTransport
Validate
RestoreSelected
ObserveRestore
Publish
ResyncLeshy
UnfreezeSelected
~~~

Freeze waits in-flight and clears verification. Park first installs and verifies protection while the old selected row still exists. WithdrawSelected then removes only exact registered selected identities and verifies the park is now the winning same-prefix route. Only then may publication withdrawal, Quiesce or transport mutation run.

RestoreSelected recreates exact selected identities for Ready families while Park stays SelectedParkActive. ObserveRestore proves the new selected identity wins, checkpoints ParkRetiring before exact park deletion, then ClearPark/checkpoint. Unfreeze is last and bumps route generation. Selected/Park retirement completes before Publish. Foreign conflict stays non-Ready/frozen.

Update ManagedRecoverySequenceForAction and all sequence tests to assert Park < WithdrawSelected < Withdraw < Quiesce and RestoreSelected < ObserveRestore < Publish < UnfreezeSelected.

---

# 12. Transactional managed activation API

Modify selected_routes.go, config_api.go, api.go, main.go, Linux orchestration, Windows installer/tests.

Add regenerateLeshyConfigForMode(socket *string): nil local, nonnil managed.

API v3 methods:

~~~text
GetManagedLeshy
EnableManagedLeshy
DisableManagedLeshy
~~~

Request ManagedSocket. Response ManagedLeshyStatus {Enabled,Socket,Zones}.

Enable does not accept a supports flag.

Enable precondition: orchestration stopped Leshy.

Order:

~~~text
validate
check checkpoints
write managed config + fsync
write managed marker + fsync
set memory managed
return
~~~

Marker failure -> rewrite local config; memory local.

Disable preconditions: roles Desired=false, selected registry empty, parks empty.

Order:

~~~text
write local config + fsync
clear marker + fsync
clear memory managed
remove empty checkpoints
return
~~~

Marker-clear failure -> rewrite managed config and keep memory managed.

Ensure/Forget/Flush require managed marker role activation.

CLI thin API:

~~~text
kikimora-core managed-leshy status
kikimora-core managed-leshy enable --socket <path>
kikimora-core managed-leshy disable
~~~

---

# 13. Cutover: full Leshy restart only

DnsHandler.update_config does not rebuild RouteManager, so routing_backend may never be switched by hot reload.

Linux entry: linux/files/kikimora-cli/orchestration.sh + orchestration/service-cutover tests.

Windows: packaging/windows/install.ps1, test_package.ps1, new test_managed_leshy.ps1.

Capability preflight uses leshy capabilities --json and exact strings managed_selected_routes_v1, af_unix_control. Kikimora Handshake must advertise managed_selected_routes_v1. No version parsing.

Local->managed:

~~~text
verify both capabilities
verify roles suitable
graceful stop Leshy
verify gone
EnableManagedLeshy
start Leshy
wait listener
managed handshake
one routed query per enabled family
verify selected diagnostics
success
~~~

Failure after enable: stop Leshy; DisconnectAll/retire to empty; DisableManagedLeshy; start local; report rollback.

Managed->local:

~~~text
DisconnectAll
wait Stopped
registry+parks empty
stop Leshy
DisableManagedLeshy
start local
verify
~~~

Never concurrent writers.

---

# 14. Leshy generic registry — exact observed identity and ownership

Repository: smollgreymouse/leshy.

Entry points:

- src/routing/model.rs;
- src/routing/backend.rs;
- src/routing/registry.rs;
- src/routing/mod.rs;
- src/routing/linux.rs;
- src/routing/windows.rs;
- src/routing/macos.rs;
- fake backend/tests.

The current ObservedRoute stores target/protocol/metric but not a complete kernel identity. Make identity explicit so Linux/Windows exact removal cannot silently discard OIF/next-hop/scope detail.

## 14.1 Kernel identity model

In model.rs add:

~~~rust
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RouteKernelIdentity {
    pub ifindex: Option<u32>,
    pub next_hop: Option<IpAddr>,
    pub protocol: u8,
    pub metric: u32,
    pub scope: u8,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct ObservedRoute {
    pub key: RouteKey,
    pub target: RouteTarget,
    pub identity: RouteKernelIdentity,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RouteOwnership {
    pub owned: bool,
    pub observed: Option<ObservedRoute>,
}
~~~

Rules:

- Linux/Windows owned=true requires observed=Some(exact row).
- A pre-existing exact compatible Linux/Windows row returns owned=false + observed=Some(row). It may satisfy forwarding but must not be deleted as ours.
- macOS may use owned=true + observed=None only for a route command that this process actually created; its current lack of kernel read-back remains an explicit platform limitation and does not weaken Linux/Windows guarantees.
- managed Kikimora backend returns owned=false + observed=None because kernel ownership belongs to Kikimora.

Linux projection captures OIF separately even when target is Gateway. Windows projection captures InterfaceIndex and exact NextHop. scope is Linux route scope and 0 on Windows/macOS when unavailable.

## 14.2 Backend result and reconcile mode

Add:

~~~rust
pub(crate) struct EnsureResult {
    pub outcome: EnsureOutcome,
    pub ownership: RouteOwnership,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum ReconcileMode {
    KernelSnapshot,
    AuthorityOnDemand,
    RegistryOnly,
}
~~~

Change RouteBackend logically to:

~~~rust
async fn ensure_route(
    &self,
    intent: &RouteIntent,
    previous: Option<&RouteOwnership>,
) -> Result<EnsureResult>;

async fn remove_route(
    &self,
    intent: &RouteIntent,
    ownership: Option<&RouteOwnership>,
) -> Result<()>;

async fn snapshot(&self) -> Result<Vec<ObservedRoute>>;

fn observed_matches(
    &self,
    observed: &ObservedRoute,
    intent: &RouteIntent,
) -> bool;

fn reconcile_mode(&self) -> ReconcileMode;
~~~

Keep resolve_target and invalidation receiver.

Modes are fixed:

- Linux local: KernelSnapshot;
- Windows local: KernelSnapshot;
- Kikimora managed: AuthorityOnDemand;
- macOS current shell backend: RegistryOnly.

Change RouteManager::start_kernel_watch to branch on reconcile_mode before spawning the periodic task:

- KernelSnapshot: consume invalidations and run the existing 30-second Full safety audit;
- AuthorityOnDemand: consume controller invalidations but do not run a periodic snapshot/bulk reconcile; section 18 defines AuthorityLost behavior;
- RegistryOnly: no periodic kernel audit and no snapshot reconciliation.

RegistryOnly preserves current macOS shell behavior without pretending an empty snapshot is kernel truth.

## 14.3 RouteEntry

Replace separate observed/implicit ownership with:

~~~rust
ownership: Option<RouteOwnership>,
state: RouteState,
verified_full_epoch: u64,
~~~

A successful local Ensure stores the returned RouteOwnership.

Fast path requires Verified + matching desired + current full epoch. It does not invent ownership.

## 14.4 Exact reconcile

For KernelSnapshot backends:

- find same-key observed rows in the snapshot;
- backend.observed_matches decides exact desired compatibility;
- >1 exact desired row or any conflicting same-key row is a conflict unless backend exact previous ownership rules explicitly permit replacement;
- exact compatible pre-existing row may become Verified with owned=false;
- missing exact row calls ensure_route(intent, previous ownership);
- store returned exact ownership.

Do not reduce comparison to key+target. Protocol/metric/OIF/next-hop/scope remain available to the backend.

For AuthorityOnDemand, section 18 controls dirty revalidation through one controller RPC per demanded intent.

For RegistryOnly, no kernel snapshot reconciliation is attempted.

## 14.5 Cleanup/retirement

remove_zone, retire_intent and graceful shutdown pass the stored RouteOwnership.

Linux/Windows:

- owned=false -> no delete;
- owned=true + observed exact -> backend deletes only that exact identity;
- owned=true with missing observed is an invariant error.

macOS:

- owned=true/observed=None means this process successfully created the route through /sbin/route; keep existing best-effort delete semantics;
- File exists must return owned=false, not owned=true. Change the command helper so create success and tolerated pre-existing are distinguishable.

Managed Kikimora process shutdown never translates into Forget/Flush; controller authority persists independently.

## 14.6 Cross-platform compile tests

Adapt every fake and macOS backend in the same L10 commit. Required tests:

- exact ownership roundtrip;
- pre-existing compatible row -> owned=false;
- created row -> owned=true;
- cleanup skips owned=false;
- Linux/Windows owned=true requires observed;
- macOS File exists -> owned=false;
- macOS successful create -> owned=true/observed=None;
- RegistryOnly mode starts no periodic audit.

---

# 15. Leshy targeted epochs

Global kernel_epoch becomes full_epoch semantics.

Full increments. Prefix/Interface/Zone do not; only matching entries Dirty. Unaffected Verified stays fast. Targeted repaired entry uses current full_epoch.

Test 1000 Verified, invalidate one, hit other 999 -> zero unrelated ensure/snapshot calls.

---

# 16. Leshy Linux local backend — event-coherent cache, targeted read-back, IPv6 events

Current find_row -> link_names + dump_family enumerates a whole family on the first unique route and mutation read-back. Remove that from the DNS request path.

Entry points:

- src/routing/linux.rs;
- src/routing/linux_monitor.rs;
- tests/linux_route_registry_netns.rs;
- tests/netns_monitor_probe.rs;
- tests/linux-route-registry-netns.sh.

## 16.1 Fixed cache structure

Add to LinuxRouteBackend:

~~~rust
cache: tokio::sync::RwLock<HashMap<RouteKey, Vec<ObservedRoute>>>,
link_names: tokio::sync::RwLock<HashMap<u32, String>>,
cache_init: tokio::sync::OnceCell<()>,
monitor_generation: Arc<AtomicU64>,
cache_generation: AtomicU64,
~~~

Add cfg(test) counters: full_snapshots, targeted_reads, adds, deletes, replaces.

PlatformBackend::new stays synchronous. Do not block it on an async snapshot. The cache is lazy-initialized by async ensure_cache_initialized(), called by ensure/remove/snapshot before cache use.

## 16.2 Monitor-before-snapshot race closure

Change linux_monitor::spawn_monitor to receive Arc<AtomicU64> monitor_generation. For every relevant route/link event, increment monitor_generation before sending the debounced RouteInvalidation. The backend and monitor share the same Arc.

ensure_cache_initialized algorithm:

~~~text
loop:
    before = monitor_generation.load()
    full-read main v4 + v6 and link names
    after = monitor_generation.load()
    if before != after: repeat
    replace cache/link_names atomically enough under write locks
    cache_generation = after
    OnceCell complete
    return
~~~

This guarantees the initial cache is not declared current across an event that occurred during the snapshot.

Before trusting a cache hit in ensure_route, compare cache_generation with monitor_generation. If unequal, do one targeted read for the requested key before returning AlreadyMatching; do not full-dump. The normal invalidation task will later reconcile the broader dirty set.

## 16.3 Targeted read-back

Implement:

~~~rust
async fn read_prefix(&self, key: &RouteKey) -> Result<Vec<ObservedRoute>>
~~~

using the pinned rtnetlink 0.14 RouteGetRequest with destination/table filtering by mutating the request message. Do not upgrade rtnetlink solely for this work. Increment targeted_reads once per call.

After add/delete/replace, call read_prefix only for the affected key and update that cache entry. Never call dump_family from mutation read-back.

Full snapshot remains only for initialization, explicit snapshot/audit and diagnostics.

## 16.4 Exact Linux identity

ObservedRoute.identity captures:

- OIF if present, even for gateway routes;
- gateway as next_hop;
- protocol;
- metric/priority, default 0 only when kernel attribute absent;
- scope.

observed_matches requires key + logical target + the backend's expected protocol/metric/scope. A previous owned exact observation is the only row the backend may replace/delete. Any other same-key row is conflict.

## 16.5 IPv6 route subscriptions

Replace the ambiguous route group constant with:

~~~rust
const RTNLGRP_LINK: u32 = 1;
const RTNLGRP_IPV4_ROUTE: u32 = 7;
const RTNLGRP_IPV6_ROUTE: u32 = 11;
~~~

Subscribe all three. Keep 100ms debounce.

Fix the current test fixture assigning header.table=999; use a valid non-main u8 such as 253 so clippy/compile cannot fail on an overflowing literal.

## 16.6 Required tests

Unit/seam:

- lazy cache initializes once;
- event during initial snapshot forces retry;
- cache generation mismatch forces targeted read, not full snapshot;
- exact cache hit no kernel I/O;
- targeted v4/v6 read-back;
- protocol/metric/scope/OIF mismatch -> conflict;
- targeted invalidation touches only relevant key;
- serialized v4 and v6 monitor events classify correctly;
- 1000 duplicate hits after convergence -> counters flat.

Privileged netns:

- /32 and /128 add/read-back;
- 1000 duplicate classifications no route/syscall churn;
- external /32 delete restored by event in <5s;
- external /128 delete restored by event in <5s;
- test timeout must be <30s so periodic audit cannot be the mechanism;
- foreign same-prefix row remains byte-identical after Ensure conflict and cleanup;
- interface delete/recreate invalidates old ifindex;
- after convergence request-path full_snapshots counter does not increase.

---

# 17. Leshy Windows local backend — exact IP Helper writer, coherent cache and notifications

Entry points:

- src/routing/windows.rs;
- src/routing/backend.rs only for shared result types;
- new windows/tests/route-registry.ps1;
- .github/workflows/ci.yml.

Delete the normal-path broad helpers/behavior:

- find_route_rows full-table scan per prefix;
- delete_routes deleting every same-prefix row;
- create_route deleting arbitrary stale same-prefix rows on ERROR_OBJECT_ALREADY_EXISTS.

They must not remain reachable from RouteBackend.

## 17.1 Fixed backend state

Replace the unit struct with:

~~~rust
pub struct WindowsRouteBackend {
    api: Arc<dyn WindowsRouteApi>,
    cache: tokio::sync::RwLock<HashMap<RouteKey, Vec<ObservedRoute>>>,
    cache_generation: AtomicU64,
    notify_generation: Arc<AtomicU64>,
    invalidation_rx: std::sync::Mutex<Option<mpsc::UnboundedReceiver<RouteInvalidation>>>,
    notification: WindowsRouteNotification,
}
~~~

WindowsRouteNotification owns the NotifyRouteChange2 registration handle plus the raw Arc context pointer. Its Drop calls CancelMibChangeNotify2 outside the callback, then releases the raw Arc context. Never cancel from inside the callback.

Add a private WindowsRouteApi trait in windows.rs and a RealWindowsRouteApi implementation wrapping only these syscall families:

- GetIpForwardTable2 / FreeMibTable;
- CreateIpForwardEntry2;
- DeleteIpForwardEntry2;
- interface alias/index and gateway resolution helpers;
- NotifyRouteChange2 / CancelMibChangeNotify2.

Tests use FakeWindowsRouteApi. Do not scatter cfg(test) function-pointer globals through the module. One injected API object is the only syscall seam.

## 17.2 Notification callback lifetime and semantics

Register exactly one NotifyRouteChange2(AF_UNSPEC, ..., initial_notification=true) in new().

The callback context contains only:

~~~rust
struct WindowsNotifyContext {
    generation: Arc<AtomicU64>,
    tx: mpsc::UnboundedSender<RouteInvalidation>,
}
~~~

Callback behavior:

1. increment generation before publishing invalidation;
2. null row / initial notification -> RouteInvalidation::Full;
3. otherwise canonicalize DestinationPrefix into RouteKey(table=MAIN_TABLE) and emit Prefix;
4. if the row carries a nonzero InterfaceIndex, an Interface invalidation may also be emitted after Prefix;
5. copy all needed data before returning;
6. no FIB enumeration, create/delete, registry access, blocking wait or cancellation from callback.

The callback never mutates the cache directly.

## 17.3 Startup snapshot race closure

new() order is fixed:

~~~text
1 create notify generation=0 and invalidation channel
2 register NotifyRouteChange2(AF_UNSPEC, initial=true)
3 before = notify_generation
4 GetIpForwardTable2(AF_UNSPEC) once and project all v4/v6 rows
5 after = notify_generation
6 if before != after: repeat steps 3-5
7 publish cache
8 cache_generation = after
9 return backend
~~~

The initial callback itself may advance generation; therefore at least one stable snapshot after registration is required.

Every returned MIB table is freed exactly once, including conversion-error paths. Wrap the raw table pointer in a small RAII MibTable guard rather than relying on manual returns.

## 17.4 Cache coherence and targeted request path

Windows has no cheap exact-prefix getter in the current implementation. Use the event-coherent cache as the request-path read.

Before trusting a cache hit or concluding a conflict:

- if cache_generation == notify_generation, use cache;
- otherwise perform one AF_UNSPEC snapshot refresh, retrying if generation changes during the snapshot, then update cache_generation.

A stable duplicate DNS hit therefore performs zero GetIpForwardTable2 calls.

After this process performs Create/Delete, update the affected cache entry from a single coherent refresh. Because Windows IP Helper does not provide the same filtered route-get path used on Linux, this refresh is allowed after a local mutation, but repeated converged requests must not refresh. Test counters make this distinction explicit.

## 17.5 Exact Windows identity projection

project_row(row) must fill ObservedRoute.identity exactly:

- ifindex = Some(InterfaceIndex);
- next_hop = Some(concrete hop) when non-unspecified, otherwise None;
- protocol = row.Protocol;
- metric = row.Metric;
- scope = 0.

Observed logical target:

- concrete next hop -> RouteTarget::Gateway(next_hop);
- unspecified next hop -> RouteTarget::Device with alias and Some(ifindex).

observed_matches requires key, desired logical target and exact expected protocol MIB_IPPROTO_NETMGMT + metric 0. It must also require the resolved desired ifindex. A gateway target still carries/compares the resolved InterfaceIndex through identity; do not lose it because RouteTarget::Gateway lacks ifindex.

## 17.6 Ensure algorithm

Given intent and previous RouteOwnership:

~~~text
1 ensure cache current by generation rule
2 build exact desired MIB row
3 project desired identity
4 collect same-key cached rows
5 exact desired rows >1 -> RouteConflict
6 any row not exact desired and not exact previous owned -> RouteConflict
7 exactly one desired and no stale previous owned:
      return AlreadyMatching with observed exact row
      ownership.owned = previous ownership owned only if exact identity equals previous owned
      otherwise owned=false for pre-existing compatible row
8 no rows:
      CreateIpForwardEntry2(desired)
9 previous exact owned only:
      Create desired first when identity differs
      verify desired after coherent refresh
      Delete only exact previous owned
10 desired + stale previous owned:
      Delete only exact previous owned
11 refresh coherently
12 require exactly one desired and no stale/foreign
13 return Added with ownership {owned:true, observed:desired}
~~~

If desired identity equals previous exact owned, no replacement is needed.

ERROR_OBJECT_ALREADY_EXISTS:

- refresh cache coherently;
- exact desired only -> success, preserving owned=true only when it equals previous owned or this process can prove its just-issued create won;
- desired + exact previous owned -> delete previous exact only;
- any foreign -> conflict;
- never call broad same-prefix delete.

Create desired before deleting old exact Owned. This avoids a forwarding hole during ifindex/gateway changes.

## 17.7 Remove algorithm

remove_route(intent, ownership):

- None or owned=false -> success/no syscall;
- owned=true requires observed=Some(exact); otherwise invariant error;
- ensure cache current;
- exact owned row absent -> success;
- exact owned present -> DeleteIpForwardEntry2 using that exact row identity;
- same-prefix foreign row is untouched;
- refresh coherently and require exact owned absent.

Do not rebuild the row from current interface/gateway state for deletion. Delete the stored exact observed identity.

## 17.8 Windows unit tests

FakeWindowsRouteApi exposes counters and scripted errors/events. Required tests:

- startup registers notification before first snapshot;
- event during snapshot forces retry;
- all tables freed on success/error;
- initial/null callback -> Full;
- v4/v6 callback -> canonical Prefix;
- callback increments generation before send;
- callback performs zero table/create/delete calls;
- Drop cancels notification outside callback exactly once;
- stable cache duplicate -> zero extra table scans;
- generation change -> exactly one stable refresh;
- 1000 duplicate v4 + 1000 duplicate v6 ensures -> zero scans/mutations after convergence;
- exact pre-existing compatible row -> owned=false;
- exact row previously created by this backend -> owned=true preserved;
- wrong ifindex/gateway/protocol/metric -> conflict, zero delete;
- previous exact owned -> desired create/verify then exact old delete;
- desired + stale old owned -> stale exact delete only;
- desired + foreign -> conflict;
- duplicate desired -> conflict;
- EEXIST + exact desired after refresh -> success;
- EEXIST + foreign -> conflict;
- remove owned=false -> zero delete;
- remove exact owned -> one exact delete;
- foreign same-prefix survives cleanup;
- v4/v6 parity for all safety paths.

## 17.9 Elevated Windows acceptance

Add windows/tests/route-registry.ps1. Require Administrator and a disposable test interface/adapter.

The script must:

1. capture Get-NetRoute/Get-NetIPInterface/Get-NetAdapter baseline;
2. install one /32 and one /128 through real Leshy local backend;
3. verify DestinationPrefix, InterfaceIndex, NextHop, Protocol and RouteMetric;
4. issue 1000 repeated DNS classifications per family and prove route/API counters or diagnostic counters do not change after convergence;
5. externally delete /32 and /128 and prove event-driven restore in <5s;
6. add a foreign same-prefix row and prove Leshy returns conflict without deleting/modifying it;
7. change/recreate the test adapter so ifindex changes and prove exact old ownership is handled without touching foreign rows;
8. stop Leshy gracefully and prove only exact owned rows disappear;
9. compare unrelated route baseline/after state;
10. cleanup only test-created routes/adapters in finally.

The event-restore timeout must remain below the 30-second periodic audit interval so the test proves NotifyRouteChange2 rather than audit fallback.

---

# 18. Leshy managed backend — cross-platform AF_UNIX and demand-driven authority validation

Entry points:

- Cargo.toml;
- src/routing/kikimora.rs;
- src/routing/backend.rs;
- src/routing/mod.rs;
- src/routing/registry.rs;
- managed backend tests.

## 18.1 Fixed cross-platform transport

Pin exactly:

~~~toml
[target.'cfg(any(unix, windows))'.dependencies]
uds-compat = "=0.1.0"
~~~

Do not add TCP localhost or named pipes. uds-compat 0.1.0 exposes Tokio-compatible UnixStream on Unix and Windows; use the same import/path on both.

Replace both cfg-specific connect functions with:

~~~rust
async fn connect(path: &str) -> Result<uds_compat::UnixStream> {
    Ok(uds_compat::UnixStream::connect(path).await?)
}
~~~

Keep existing tokio AsyncRead/AsyncWrite framing:

~~~text
4-byte big-endian payload length
JSON payload
~~~

Keep CALL_TIMEOUT=5s, MAX_FRAME=1MiB and API v3 handshake.

Windows managed socket is the same Kikimora AF_UNIX endpoint:

~~~text
C:\ProgramData\Kikimora\core.sock
~~~

Do not normalize it to a TCP address.

## 18.2 Backend ownership result

KikimoraRouteBackend.ensure_route returns:

~~~text
EnsureResult {
  outcome: Added / AlreadyMatching,
  ownership: RouteOwnership { owned:false, observed:None }
}
~~~

Leshy does not own the kernel row in managed mode. The controller does.

remove_route semantics are different from local cleanup and must remain explicit:

- explicit registry retirement caused by reload/static removal/Forget path -> send ForgetLeshyRoute;
- zone removal may use FlushLeshyZone when the existing registry API intentionally retires the whole zone;
- ordinary Leshy process shutdown -> do not call managed remove at all; section 19 branches by reconcile_mode before cleanup.

The ownership flag therefore controls local kernel deletion only; it does not suppress explicit controller retirement requested by registry semantics.

## 18.3 ReconcileMode and snapshot misuse

KikimoraRouteBackend.reconcile_mode() returns AuthorityOnDemand.

KikimoraRouteBackend.snapshot() must return an error such as:

~~~text
"managed Kikimora backend has no local kernel snapshot; authority is demand-driven"
~~~

Do not return an empty vector, because empty state can be misinterpreted as every route missing. Tests must fail if AuthorityOnDemand code accidentally calls snapshot().

## 18.4 New invalidation kind

Add to RouteInvalidation:

~~~rust
AuthorityLost,
~~~

Semantics:

- Zone{name}: controller generation/readiness changed for one zone;
- AuthorityLost: control subscription/socket authority is unknown globally;
- Full remains local-kernel full snapshot invalidation and is never emitted by KikimoraRouteBackend.

The subscription pump emits AuthorityLost when it loses the controller connection after previously starting, on malformed subscription frames, or when reconnect attempts fail. It keeps the existing 1s -> 30s bounded reconnect backoff.

After reconnect/Handshake + snapshot, compare remembered zone generations and emit Zone only for zones whose authoritative generation/readiness changed. Do not emit Full merely because the socket reconnected.

## 18.5 Invalidation dispatcher must not bulk RPC

Change RouteManager::start_kernel_watch / helper dispatch so behavior depends on reconcile_mode:

### KernelSnapshot

Existing local behavior:

~~~text
invalidation
 -> mark matching dirty
 -> snapshot/reconcile immediately
~~~

Full advances full_epoch. Targeted Prefix/Interface/Zone does not, per section 15.

### AuthorityOnDemand

Never call reconcile_after_invalidation from the listener. Instead:

~~~text
Zone(z)       -> registry.mark_dirty(Zone(z)); no RPC
AuthorityLost -> registry.mark_dirty(AllAuthority); no RPC
other kinds   -> test-visible invariant error/log; no snapshot
~~~

Add a registry method with exact semantics:

~~~rust
pub(crate) async fn mark_authority_lost(&self)
~~~

It marks every managed entry Dirty/unverified without deleting desired intent/ownership metadata and without calling the backend. It does not increment the local full_epoch because that epoch represents kernel snapshot proof.

For Zone invalidation, only entries of that zone become Dirty.

The next classify/ensure for one Dirty intent calls Kikimora once, stores Verified on success and returns the DNS answer under existing fail-closed policy.

No background loop walks all registered managed intents after socket loss/reconnect. Kikimora's own selected-route observer repairs kernel drift independently.

### RegistryOnly

No kernel invalidation reconcile.

## 18.6 Managed fast path and authority loss

A managed Verified entry remains zero-RPC only while it has not been dirtied by Zone/AuthorityLost.

After AuthorityLost:

- a cached DNS response for a managed hostname must not bypass route authority validation;
- first post-loss hit for a prefix invokes exactly one EnsureLeshyRoute;
- controller unavailable/denied/conflict -> existing fail-closed DNS policy;
- success -> entry Verified again; subsequent hits zero RPC until next invalidation.

Concurrent first hits for the same key remain serialized by the existing per-entry registry lock and collapse to one controller call. Add an explicit concurrency regression with 32 simultaneous requests.

## 18.7 Windows AF_UNIX transport tests

Use uds_compat::UnixListener in the Windows test to create a real temporary AF_UNIX endpoint. Do not mock connect().

Required tests:

- Windows listener/client framed Handshake roundtrip;
- EnsureLeshyRoute framed request/result roundtrip;
- path with spaces under a temporary Windows directory;
- missing socket -> error/fail closed;
- no TCP/named-pipe fallback code is compiled;
- capability absent -> managed backend constructor/use refuses routing;
- frame >1MiB refused;
- 5s timeout path remains bounded.

## 18.8 Authority-on-demand tests

Script backend with 1000 Verified intents:

- AuthorityLost -> 1000 Dirty, zero backend Ensure calls, zero snapshot calls;
- one subsequent key hit -> exactly one Ensure;
- repeated same key -> no extra Ensure;
- 32 concurrent hits same Dirty key -> one Ensure;
- hit second key -> second Ensure only;
- Zone invalidation dirties only that zone;
- reconnect same generations -> no bulk Ensure and no unrelated dirtying;
- generation change one zone -> only that zone Dirty;
- family-not-ready denial keeps existing NODATA family suppression;
- managed backend snapshot invocation is a test failure;
- RouteManager::for_server in managed mode never constructs/uses PlatformBackend;
- socket loss never falls back local.

---

# 19. Leshy reload boundary, graceful shutdown and cutover capabilities

Entry points:

- src/server.rs;
- src/main.rs;
- src/service/windows.rs;
- src/dns/handler.rs;
- src/routing/mod.rs;
- src/routing/registry.rs;
- reload/service tests.

## 19.1 Routing-backend-affecting config is restart-only

Current DnsHandler.update_config does not rebuild RouteManager. Therefore hot reload must reject changes to fields that define RouteManager construction. Add a helper comparing old/new ServerConfig and fail apply_reload before any route retirement when any changes:

- routing_backend;
- kikimora_socket;
- route_aggregation_prefix;
- max_dynamic_routes.

Error text must contain:

~~~text
routing manager configuration changed; restart Leshy to apply routing backend/capacity changes
~~~

Cutover orchestration intentionally writes the new config while Leshy is stopped and performs a full process restart. Never teach reload to swap RouteManager in place in this work.

## 19.2 Reload invalidation is targeted, not Full

Replace DnsHandler.reconcile_routes_after_reload() with:

~~~rust
pub async fn reconcile_routes_after_reload(&self, zones: &[String]) -> usize
~~~

Build a sorted/deduplicated list of Changed + Added + Removed zone names before config swap. After the explicit old-intent retirement and new config swap, call handle_invalidation(RouteInvalidation::Zone{name}) once per affected zone.

Do not emit Full at the tail of ordinary config reload.

Effects:

- KernelSnapshot local backend: only affected zone entries become Dirty/reconciled; unrelated verified entries retain full_epoch;
- AuthorityOnDemand managed backend: only affected zone entries become Dirty and no bulk RPC happens;
- RegistryOnly macOS: no kernel snapshot audit is invented.

Tests: two-zone config, change one zone, prove second zone backend counters remain zero; managed mode proves reload sends zero controller calls until the changed route is next demanded.

## 19.3 Graceful server API

Add in server.rs:

~~~rust
pub async fn run_server_until<F>(
    config_arg: Option<PathBuf>,
    shutdown: F,
) -> anyhow::Result<()>
where
    F: Future<Output = ()> + Send;
~~~

run_server(config) becomes a thin wrapper:

~~~text
run_server_until(config, shutdown_signal())
~~~

shutdown_signal is fixed:

- Unix: tokio::select between ctrl_c and SIGTERM;
- Windows console: ctrl_c;
- Windows SCM does not use this wrapper; it passes its service stop future directly.

## 19.4 Cancel background tasks before route cleanup

Use tokio_util::sync::CancellationToken inside run_server_until. Every task created by server.rs in this scope must be tied to it:

- static-route retry loop;
- ConfigWatcher task;
- reload receiver task.

Refactor retry_static_routes(handler, cancel) to select between its 10-second timer and cancel.cancelled(). Wrap watcher.watch() in a select with cancel. Reload receiver also selects cancel.

Keep JoinHandles for these tasks. On shutdown:

~~~text
1 cancel token
2 await/finish background tasks
3 stop/drop DNS server future through tokio::select result
4 acquire handler and run route shutdown cleanup
5 return
~~~

No background task may call apply_static_routes after cleanup begins. Add a deterministic test with a retry tick racing shutdown; cleanup must be the last route mutation.

## 19.5 RouteManager shutdown cleanup

Add:

~~~rust
pub(crate) async fn shutdown_cleanup(&self) -> anyhow::Result<usize>
~~~

Behavior by reconcile_mode:

- KernelSnapshot: registry.remove_all_owned(backend) and exact local deletions;
- RegistryOnly: registry.remove_all_owned(backend), allowing macOS best-effort deletion of rows this process actually created;
- AuthorityOnDemand: return 0 and send no Forget/Flush. Controller ownership outlives the Leshy process.

Add DnsHandler.shutdown_routes() as a thin call through its RouteManager.

RouteRegistry.remove_all_owned snapshots keys in deterministic key order, locks each entry through the existing per-key serialization, and calls backend.remove_route only for entries whose local RouteOwnership says owned=true. A deletion error keeps the entry/evidence and is returned; do not erase it from memory first.

For local Linux/Windows, exact observed identity is mandatory. For macOS RegistryOnly, owned=true/observed=None uses the explicit backend exception from section 14.

## 19.6 run_server_until completion semantics

Use tokio::select! between server.run() and shutdown.

- shutdown wins -> cancel background tasks, await them, run shutdown_routes, then return Ok;
- server.run returns Ok unexpectedly -> same cleanup, then return Ok;
- server.run returns Err -> cancel tasks, attempt cleanup, return the original server error with cleanup error logged/attached as context rather than hiding the primary failure.

Cleanup timeout: 10 seconds. If local exact cleanup exceeds 10 seconds, return an error; service stop must not silently claim clean cutover.

## 19.7 Windows SCM graceful stop

Keep the current SCM status/reporting structure, but replace normal server_task.abort().

Create a tokio::sync::watch channel before spawning server task. SCM Stop handler:

- sends true on watch sender;
- also wakes the existing synchronous control loop;
- returns NoError immediately.

Spawn:

~~~text
run_server_until(config_path, async { wait until watch receiver == true })
~~~

On Stop:

1 report StopPending;
2 signal shutdown;
3 wait up to 15 seconds for server task;
4 if it finishes, report Stopped;
5 only after the 15-second timeout, abort as emergency fallback and report service failure/non-clean stop rather than treating abort as success.

The 15-second bound leaves margin under the SCM wait hint. Do not use abort on the normal path.

Add tests around an extracted service-stop coordinator so unit tests do not need a real SCM.

## 19.8 Linux/systemd graceful stop

SIGTERM must enter shutdown_signal and therefore run exact local cleanup. Add a Linux integration test that starts local Leshy with a test route, sends SIGTERM, waits for process exit and verifies the exact owned route is gone while a foreign same-prefix fixture remains.

This test is also the cutover prerequisite for local -> managed.

## 19.9 Capabilities command

Add top-level CLI variant:

~~~rust
Command::Capabilities { json: bool }
~~~

CLI:

~~~text
leshy capabilities --json
~~~

With --json output exactly:

~~~json
{
  "capabilities": [
    "af_unix_control",
    "managed_selected_routes_v1"
  ]
}
~~~

Sort capability strings lexicographically for stable scripts/tests. The command performs no config load, socket connect, DNS bind or kernel mutation and exits 0.

Without --json, print one capability per line in the same sorted order.

Required tests:

- clap parser;
- exact JSON snapshot;
- command succeeds with nonexistent config path;
- route backend fake sees zero calls;
- Windows build reports both capabilities.

---

# 20. Shared Ready-Toad test fixture and Linux managed successful E2E

The existing managed-leshy-smoke.sh intentionally exercises a not-Ready role and must remain. Add a deterministic Ready Toad fixture so success-path E2E does not depend on real VPN credentials/transports.

## 20.1 Fixed test fixture binary

Add:

~~~text
toad/internal/testtoad/cmd/ready-toad/main.go
~~~

It is test infrastructure only; do not install/package it. Build it explicitly in acceptance scripts.

CLI contract must match ExecLauncher exactly:

~~~text
ready-toad run -config <role.toml>
~~~

Implementation uses config.Load plus toadctl.Server at <cfg.StateDir>/control.sock and a small in-memory toadctl.Handler. It never creates a VPN transport or kernel route.

Behavior:

- resolve cfg.Interface with net.InterfaceByName; fail startup if missing;
- every 100ms resample that same interface name with net.InterfaceByName + Interface.Addrs; if ifindex or matching configured addresses change, increment Snapshot.Revision and wake subscribers;
- Snapshot.InterfaceName = cfg.Interface;
- Snapshot.IfIndex = currently resolved real ifindex;
- Snapshot.Addresses = actual addresses on that interface which match a family configured in cfg.Address;
- Snapshot.Families.IPv4/IPv6.Configured derives from cfg.Address; Ready is true only when the real interface currently has at least one valid address of that configured family;
- Snapshot.RouteReady = Families.Ready();
- Snapshot.State = "ready"; SessionConnected=true;
- Handshake returns Validate=true, Rebind=true, RestartTransportKeepingTUN=true, ReportsLiveEndpoints=false;
- Start stores request.Generation and increments revision;
- Validate returns Healthy=true and the exact family readiness;
- Rebind immediately resamples cfg.Interface and actual addresses, keeps Generation, increments revision;
- RestartTransportKeepingTUN behaves like Rebind for this fixture;
- Stop/cancel closes the server cleanly;
- Subscribe uses toadctl.Server's normal WaitForRevision flow.

Add fixture unit tests for Start/Validate/Rebind, independent v4/v6 readiness and ifindex refresh.

This fixture is the only synthetic Toad used by Linux/Windows managed-success acceptance. Do not invent a second protocol stub in shell/PowerShell.

## 20.2 Linux success script

Add:

~~~text
linux/tests/toad/managed-leshy-routing-netns.sh
~~~

Reuse the DNS stub/query code from managed-leshy-smoke.sh by moving those two small Python helpers into:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
~~~

Both smoke scripts call the same helpers afterward.

Fixture namespace:

~~~text
namespace: kk-leshy-routing-$$
underlay device: kkunderlay0
underlay address: 198.51.100.2/24
default route: via 198.51.100.1 dev kkunderlay0
managed device: kkmanaged0
managed v4: 10.203.0.1/24
managed v6: 2001:db8:203::1/64
DNS selected A: 203.0.113.10/32
DNS selected AAAA: 2001:db8:dead::10/128
Leshy listen: 127.0.0.1:15353
stub: 127.0.0.1:15354
core socket: /run/kk-managed-routing/core.sock
~~~

Before starting core, create both dummy devices and the namespace default route above. The managed device is never the default underlay. This gives core a real underlay path while keeping selected traffic target identity separate.

Build/use real binaries:

- kikimora-core;
- Leshy;
- ready-toad fixture passed as kikimora-core --toad-binary.

Role config contains:

~~~toml
name = "primary"
protocol = "openconnect"
interface = "kkmanaged0"
address = ["10.203.0.1/24", "2001:db8:203::1/64"]
state_dir = "/run/kk-managed-routing/state/primary"
leshy_zone = "primary"

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
~~~

The fixture ignores OpenConnect transport but config remains valid.

Leshy config uses routing_backend=kikimora, the core socket, ipv6=true and the shared DNS stub.

## 20.3 Exact Linux assertions

The script must assert in this order:

1. Start core, then run `kikimora-core connect --socket "$SOCKET" --role primary`. Core launches ready-toad; poll `status --json` until role Ready with both families and nonzero ToadGeneration.
2. Leshy Handshake succeeds with managed_selected_routes_v1.
3. Before the first query neither selected host route exists.
4. A query returns NOERROR with one answer and only after the /32 exists in main table through kkmanaged0.
5. Read exact /32 details: table main, dev kkmanaged0, proto static/4, expected metric/scope.
6. AAAA query analogously creates exact /128.
7. Capture selected stats. Issue 1000 A + 1000 AAAA queries. KernelMutations, full snapshot count and route table remain unchanged after convergence. DuplicateHits may increase.
8. Externally delete /32. Poll at <=100ms intervals and require exact restoration in <5s.
9. Externally delete /128 and require restoration in <5s. These timeouts prove event path, not 10s audit.
10. Inject a foreign same-prefix route on lo after first retiring/removing our exact selected fixture as required by the test phase. Query must SERVFAIL/conflict; capture the foreign row before/after and require byte-identical ip -details output.
11. Remove foreign fixture through the script, never through Kikimora. Restore managed route with a query.
12. Trigger managed recovery using the existing control/recovery trigger used by tests. During recovery poll snapshots and route table to prove ordering: selected route + park coexist after Park; after WithdrawSelected selected is absent and exact unreachable park remains; after RestoreSelected selected returns while park remains; after ObserveRestore park disappears; only then role Ready/unfrozen.
13. For recovery ifindex change, build core with kikimora_test_failpoints and set the recovery barrier to `withdraw-selected`. Wait for the `.waiting` marker, prove selected is absent and ParkActive is winning, delete/recreate dummy `kkmanaged0` with the same configured addresses, assert its ifindex differs, create the `.continue` marker, and let Withdraw/Quiesce/ApplyEndpoint/Rebind continue. ready-toad resamples the same configured interface name during Rebind; restored selected route uses the new ifindex.
14. Delete the configured IPv6 address from kkmanaged0. ready-toad detects it within its 100ms sampler and publishes IPv6 Ready=false while IPv4 stays Ready. A query still NOERROR; AAAA becomes family-suppressed/NODATA; v4 selected row remains unchanged. Restore the IPv6 address and require Ready=true again before subsequent recovery tests.
15. Kill kikimora-core. A/AAAA zone queries fail closed; Leshy remains alive; no local Leshy-owned fallback route appears.
16. Restart core with same state directory. schema2 checkpoint loads, role starts frozen, production managed recovery is selected, exact Park/selected state is reconciled, and ownership is reconstructed before normal fast-path ACK. No DNS request is allowed to be the mechanism that recreates checkpoint ownership.
17. Exercise Forget and FlushZone through API/reload. Exact owned selected and Park identities retire; unrelated foreign fixture remains.
18. DisconnectAll then managed rollback leaves selected registry empty, Park=nil for all entries and no managed park before local-mode start is permitted.

## 20.4 Failure diagnostics and cleanup

On ERR trap print:

~~~text
ip -details -4 route show table main
ip -details -6 route show table main
selected-routes.json
leshy-managed.json
kikimora-core status JSON
selected route diagnostics JSON
core log
Leshy log
ready-toad log
~~~

Cleanup kills children, removes only fixture routes/files and deletes the namespace. Do not suppress the original failure exit code.

---

# 21. Windows managed successful E2E

Add:

~~~text
packaging/windows/test_managed_leshy.ps1
~~~

Use the same ready-toad fixture compiled for windows/amd64. No fake control protocol in PowerShell.

## 21.1 VM prerequisites are explicit

The disposable Windows acceptance VM must expose two dedicated test adapters created by the VM harness, not by this script:

~~~text
KikimoraTestA
KikimoraTestB
~~~

They must be safe to assign documentation-range addresses and must not carry the VM's management/default route. The script parameters are:

~~~powershell
param(
  [Parameter(Mandatory=$true)][string]$KikimoraCore,
  [Parameter(Mandatory=$true)][string]$ReadyToad,
  [Parameter(Mandatory=$true)][string]$Leshy,
  [Parameter(Mandatory=$true)][string]$WorkDir,
  [string]$InterfaceA = "KikimoraTestA",
  [string]$InterfaceB = "KikimoraTestB"
)
~~~

Require Administrator. Abort before mutation if either adapter is absent or owns a default route.

Assign:

~~~text
A: 10.203.0.1/24 and 2001:db8:203::1/64
B: 10.204.0.1/24 and 2001:db8:204::1/64
selected A: 203.0.113.10/32
selected AAAA: 2001:db8:dead::10/128
core socket: <WorkDir>\core.sock
~~~

## 21.2 Windows assertions

1. Capture complete Get-NetRoute/Get-NetIPInterface/Get-NetAdapter baseline and exact test-adapter ifindices.
2. Start core with --toad-binary ReadyToad and role bound to InterfaceA; start Leshy managed over real AF_UNIX.
3. Verify Handshake/capability and both families Ready.
4. A query creates exact /32. AAAA creates exact /128. Verify DestinationPrefix, InterfaceIndex==InterfaceA index, NextHop representation, Protocol==NetMgmt/managed value expected by writer and RouteMetric.
5. Capture diagnostics. 1000 A + 1000 AAAA queries cause zero additional selected KernelMutations and no repeated GetIpForwardTable2 refreshes after convergence.
6. Remove exact /32 externally with Remove-NetRoute/DeleteIpForwardEntry fixture; notification/audit repairs it <5s. Repeat /128.
7. Insert a foreign same-prefix route with a deliberately different InterfaceIndex/NextHop/Metric. Managed query returns conflict/fail closed. Capture Get-NetRoute/CIM identity before/after and require it unchanged.
8. Remove the foreign fixture only from the script, then restore normal managed route.
9. Build test core with kikimora_test_failpoints and set recovery barrier to `withdraw-selected`. Wait for `.waiting`; prove ParkActive is checkpointed, old selected identity is absent and exact Windows loopback Park is winning. Rename InterfaceA temporarily to `KikimoraTestOld`, rename InterfaceB to the configured InterfaceA alias, assert the configured alias now resolves to InterfaceB's different ifindex, then create `.continue`. ready-toad resamples the same configured name during Rebind. Prove new Desired selected route uses the replacement ifindex; ParkRetiring is checkpointed before exact park delete; no stale old-ifindex selected row remains. Restore adapter names in finally.
10. Explicitly exercise loopback identity change seam in unit tests; E2E only verifies persisted exact park identity returned by platform and removed without broad same-prefix deletion.
11. Restart core during SelectedApplying and separately during ParkApplying using deterministic failpoints. Exact Desired is adopted only because Applying was durable. Pending never adopts a pre-existing row.
12. Restart during ParkRetiring after the park delete. Absent exact persisted park completes ClearPark; no unrelated loopback route is touched.
13. Kill core. Leshy queries fail closed, Leshy remains running, and no local fallback writer row appears.
14. Restart core with state directory and prove schema2 ownership/startup recovery before normal ACK.
15. Run DisconnectAll -> registry/Park empty -> DisableManagedLeshy -> full Leshy restart local. Never observe simultaneous local+managed writer authority.
16. Stop local Leshy gracefully and prove only its exact owned rows disappear.
17. Compare full unrelated route baseline/after; fail on any unrelated route identity change.

## 21.3 Cleanup

Use try/finally. Stop only processes started by the script, remove only documentation-range addresses/routes added by it, restore adapter address state, preserve logs on failure and never modify the VM management/default route.

---

# 22. Deterministic failure injection and crash-restart proof

Timing-based sleeps are not acceptable for crash semantics. Add one unit seam and one test-build process seam.

## 22.1 Unit failpoint seam

In selected_routes.go add an unexported hook to SelectedRouteManager:

~~~go
type selectedFailpoint func(point, role string, prefix netip.Prefix) error
~~~

Production constructor sets nil. A test constructor accepts the hook. Call it only at named durable boundaries listed below. A hook error simulates process interruption: return immediately without compensating rollback, preserving whatever checkpoint/kernel fake state exists at that point.

Stable names:

~~~text
route.after_pending_checkpoint
route.after_preflight
route.after_applying_checkpoint
route.after_kernel_reconcile
route.after_mark_active
route.after_active_checkpoint
route.after_retiring_checkpoint
route.after_kernel_delete

park.after_pending_checkpoint
park.after_preflight
park.after_applying_checkpoint
park.after_kernel_reconcile
park.after_mark_active
park.after_active_checkpoint
park.after_retiring_checkpoint
park.after_kernel_delete

managed.after_config_write
managed.after_marker_write
managed.after_local_config_write
managed.after_marker_remove
~~~

Do not rename these after tests land; E2E/build-tag hooks use the same names.

## 22.2 Process-crash failpoint seam for acceptance

Add two files in control package:

~~~text
selected_failpoints_default.go              //go:build !kikimora_test_failpoints
selected_failpoints_testbuild.go            //go:build kikimora_test_failpoints
~~~

Both expose one internal function:

~~~go
func processSelectedFailpoint(point, role string, prefix netip.Prefix)
~~~

Default is no-op. Test-build implementation reads:

~~~text
KK_SELECTED_FAILPOINT=<stable name>
KK_SELECTED_FAILPOINT_ROLE=<optional role>
KK_SELECTED_FAILPOINT_PREFIX=<optional canonical prefix>
~~~

On exact match it calls os.Exit(86) immediately. No deferred cleanup.

Acceptance scripts build kikimora-core with -tags kikimora_test_failpoints only for crash-restart cases. Release/package builds never use the tag.

SelectedRouteManager calls both the injectable unit hook and processSelectedFailpoint at the same boundary.

Add the same build-tag pattern in toad/internal/core for a recovery barrier:

~~~go
func processRecoveryBarrier(role string, step RecoveryStep) error
~~~

Managed RecoveryRunner calls it immediately after each successful driver step and before advancing to the next step. Default build is no-op. Test-build honors:

~~~text
KK_RECOVERY_BARRIER_STEP=<exact RecoveryStep string>
KK_RECOVERY_BARRIER_ROLE=<optional role>
KK_RECOVERY_BARRIER_DIR=<directory>
~~~

On match it atomically creates `<dir>/<role>.<step>.waiting`, then waits up to 30 seconds for `<dir>/<role>.<step>.continue`. Timeout returns an error and leaves recovery failed/frozen. The hook performs no route mutation.

Linux/Windows ifindex-replacement E2E sets the barrier to `withdraw-selected`: at that marker exact selected rows are absent and exact parks are winning, which is the only safe point for the harness to replace the managed adapter. Release builds contain no waiting behavior.

## 22.3 Selected-route crash matrix

| Injection | Durable state | Kernel may contain | Required restart result |
|---|---|---|---|
| route.after_pending_checkpoint | SelectedPending | previous Owned only / empty | rerun preflight; pre-existing unowned Desired conflicts |
| route.after_preflight | SelectedPending | unchanged | rerun preflight; then Applying |
| route.after_applying_checkpoint | SelectedApplying | previous Owned / empty | exact Desired may be created/adopted |
| route.after_kernel_reconcile | SelectedApplying | Desired and maybe previous Owned | adopt Desired, clean exact previous Owned only |
| route.after_mark_active | disk still Applying | Desired exact | adopt Desired and checkpoint Active |
| route.after_active_checkpoint | SelectedActive | Desired exact | restore Active unverified, audit re-proves |
| route.after_retiring_checkpoint from Active | SelectedRetiring RetireDesired=false | exact Owned may exist | delete exact Owned only |
| route.after_retiring_checkpoint from Applying | SelectedRetiring RetireDesired=true | Desired may exist + previous Owned | delete only exact Desired/Owned candidates |
| route.after_kernel_delete | SelectedRetiring | authorized identities absent | checkpoint absence/remove registry |

Every case restarts a new Manager from the same state directory and fake/live kernel state.

## 22.4 Park crash matrix

| Injection | Durable state | Kernel may contain | Required restart/result |
|---|---|---|---|
| park.after_pending_checkpoint | ParkPending | previous Park.Owned / no new park | mutation not authorized; exact new Desired already present conflicts |
| park.after_preflight | ParkPending | unchanged | preflight reruns |
| park.after_applying_checkpoint | ParkApplying | previous Owned / empty | exact Park.Desired may be created/adopted |
| park.after_kernel_reconcile | ParkApplying | Desired + maybe old Owned | adopt Desired; remove only exact old Owned after Desired proof |
| park.after_mark_active | disk ParkApplying | Desired exact | checkpoint ParkActive |
| park.after_active_checkpoint | ParkActive | exact Owned==Desired | recovery may proceed to WithdrawSelected |
| park.after_retiring_checkpoint from Active | ParkRetiring RetireDesired=false | exact Owned may exist | delete exact Owned only |
| park.after_retiring_checkpoint from Applying | ParkRetiring RetireDesired=true | Desired may exist + previous Owned | delete only exact authorized candidates |
| park.after_kernel_delete | ParkRetiring | authorized park identities absent | ClearPark + checkpoint |

## 22.5 Other mandatory injected failures

Test all:

- selected checkpoint temp-file write failure;
- fsync failure;
- atomic rename failure;
- platform Preflight error;
- platform Create/Add error;
- read-back mismatch after successful syscall;
- Windows ERROR_OBJECT_ALREADY_EXISTS exact vs foreign;
- native observer channel exits;
- native observer restart repeatedly fails: 1/2/4/8/16/30s schedule via fake clock;
- route audit SnapshotSelectedRoutes error;
- enable managed config write succeeds then marker write fails -> local config restored/memory local;
- disable local config write succeeds then marker remove fails -> managed config restored/memory managed;
- malformed marker;
- schema1 checkpoint under active marker;
- schema2 invalid Park/Owned/RetireDesired combinations;
- production recoverRole selector missing/false vs true;
- reload attempts routing_backend/kikimora_socket/capacity change -> reject before any route retirement;
- graceful shutdown background retry racing cancel -> cleanup is last mutation;
- managed AuthorityLost with 1000 routes -> zero bulk RPC;
- Linux cache snapshot raced by netlink event -> retry;
- Windows cache snapshot raced by NotifyRouteChange2 -> retry.

Every failure test asserts:

1. no unsafe success ACK/DNS answer;
2. exact durable checkpoint state;
3. exact allowed kernel identities;
4. restart outcome;
5. foreign rows byte-for-byte/logically unchanged;
6. no writer fallback.

---

# 23. Required test coverage by file

This is the minimum coverage, not suggestions.

## 23.1 Kikimora routing model

toad/internal/routing/selected_registry_test.go:

- every selected transition Pending/Applying/Active/Retiring;
- RetireDesired false/true semantics;
- every park transition nil/Pending/Applying/Active/Retiring/nil;
- Park.RetireDesired false/true semantics;
- invalid transitions rejected;
- Desired/Owned/Park identity validation;
- schema2 JSON roundtrip;
- corrupt/truncated JSON;
- unsupported schema1 under active marker;
- duplicate prefix;
- role/zone mismatch;
- canonical prefix;
- DNS only /32,/128;
- static /24,/64;
- generation change Active->Pending preserving Owned;
- RestoreRole all-or-nothing;
- checkpoint RouteGeneration restore+1.

toad/internal/routing/selected_plan_test.go:

- every section-3 selected/park/foreign classification;
- explicit SelectedKernelForeign unconditional conflict;
- wrong ifindex/gateway/protocol/metric/scope;
- duplicate Desired;
- Desired + stale exact Owned;
- authorized ParkApplying adoption;
- ParkPending cannot adopt;
- ParkActive exact coexistence;
- wrong Park identity conflicts;
- no protocol-class ownership tests remain.

## 23.2 Kikimora control

toad/internal/control/selected_routes_test.go:

- marker gating;
- real ToadGeneration persisted;
- selected and park transaction checkpoint order;
- every unit failpoint in section 22;
- applied vs already-applied wire state;
- verification token exact requirements;
- restored Active has no token;
- restorePending startup freeze;
- CompleteStartupRestore only exact conditions;
- capacity 16384 and duplicate not charged twice;
- family independence;
- frozen denial;
- Forget/Flush exact retirement with Park;
- failed selected/park delete retains evidence;
- strict marker read;
- enable/disable transactional rollback;
- ParkingState reports Active only for exact ParkActive coverage;
- diagnostics counters.

Fix TestRoleIPv6DefaultOnAndPerRoleToggle and every related config-generation test to use temp config/routing directories. No unit test writes /etc/kikimora.

toad/internal/control/control_test.go:

- recoverRole production Engine receives ManagedSelectedRoutes=m.managedSelected;
- managed role sees managed sequence including WithdrawSelected;
- unmanaged role sees legacy sequence;
- requestManagedSelectedRecovery dedupes recoveryInFlight;
- startup checkpoint ParkActive requests full managed recovery when process/driver available;
- unavailable process leaves restorePending/frozen for next audit.

toad/internal/control/selected_route_reconciler_test.go:

- Prefix/Interface/Full behavior;
- exactly one SnapshotSelectedRoutes per full audit;
- no per-record full snapshot;
- 100ms debounce;
- 10s ticker via fake clock;
- Active verification refresh;
- selected drift repair;
- Park causes recovery request, never observer unpark;
- Retiring cleanup;
- observer death/backoff;
- integrity conflict freeze/fail;
- CompleteStartupRestore gate.

toad/internal/control/recovery_driver_test.go + core engine tests:

- Freeze waits in-flight;
- exact sequence Park < WithdrawSelected < Withdraw < Quiesce;
- RestoreSelected < ObserveRestore < Publish < Unfreeze;
- no selected delete before every ParkActive exact observation;
- selected absent + park present before transport mutation;
- ParkActive remains through RestoreSelected;
- ParkRetiring checkpoint before unpark syscall;
- new ToadGeneration/ifindex;
- non-ready family remains parked.

## 23.3 Kikimora platform

Linux:

- toad/internal/platform/linux/netlink routes unit tests for normalize/preflight/reconcile/remove and managed park state;
- selected_netns_test.go updated to schema2 and independent Park transaction;
- linux/tests/toad/selected-routes-netns.sh privileged gate.

Windows:

- toad/internal/platform/windows/routes/routes_test.go contains the full section-9 matrix;
- tests compile/run natively on windows-latest;
- packaging/windows/test_managed_leshy.ps1 is release-VM runtime gate.

## 23.4 Ready-Toad fixture

toad/internal/testtoad/cmd/ready-toad tests:

- CLI run -config;
- config load;
- real interface resolution seam;
- Start generation/revision;
- Validate healthy;
- independent family readiness;
- Rebind ifindex refresh;
- Subscribe revision;
- clean context shutdown.

## 23.5 Leshy common

src/routing/registry.rs/backend/model tests:

- RouteKernelIdentity exact equality;
- pre-existing compatible -> owned=false;
- created -> owned=true;
- cleanup skips local owned=false;
- previous exact owned replacement;
- Full vs targeted epoch;
- 1000-entry targeted invalidation;
- ReconcileMode branches;
- AuthorityLost no bulk RPC;
- RegistryOnly no audit.

src/dns/handler.rs/server reload tests:

- cached response still demands Dirty managed route validation;
- conflict/authority/capacity safety;
- family suppression;
- mixed A+AAAA;
- one-zone reload does not touch another;
- route-manager-affecting hot reload rejected before mutation;
- shutdown cleanup last mutation.

Linux/Windows/macOS backend tests are exactly sections 14-18.

---

# 24. CI, cross-repository integration and release pin

## 24.1 Kikimora Toad workflow

File: .github/workflows/toad.yml.

Keep go-unit strategy.fail-fast=false. Required go-unit steps on ubuntu/macos/windows:

~~~text
go mod tidy + diff
gofmt check
go vet ./...
go build ./cmd/kikimora-core
go test ./...
go test -race ./...
~~~

Update linux-smoke-build artifact to also build:

~~~text
./internal/testtoad/cmd/ready-toad -> ready-toad
~~~

Add jobs:

### linux-selected-routes-netns

- needs linux-smoke-build;
- build selected netlink test binary;
- sudo linux/tests/toad/selected-routes-netns.sh.

### linux-managed-leshy-denial

- preserve current managed-leshy-smoke.sh with real core + real Leshy.

### linux-managed-leshy-success

- checkout smollgreymouse/leshy master into a sibling directory;
- install stable Rust;
- cargo build --release for Leshy;
- use shared kikimora-core + ready-toad artifact;
- sudo linux/tests/toad/managed-leshy-routing-netns.sh.

This cross-repo job is required on every change touching 08D/control/platform/generated Leshy config or the integration scripts.

Windows go-unit runs the direct Windows platform unit tests natively. windows-cross-build remains a compile portability job only.

windows-package-contract remains package shape only. Do not label either as runtime managed-route acceptance.

## 24.2 Leshy CI

File: .github/workflows/ci.yml.

Set every OS test matrix strategy.fail-fast=false so Linux failure cannot cancel Windows evidence.

Every OS:

~~~text
cargo fmt -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
~~~

Linux privileged job runs sudo tests/linux-route-registry-netns.sh.

Windows job must run native unit tests including NotifyRouteChange2/cache/AF_UNIX. It must not be canceled by another matrix leg.

Keep existing macOS integration and add RegistryOnly/shutdown semantics tests.

## 24.3 Windows runtime release gate

The disposable Windows VM with KikimoraTestA/B is an explicit release gate:

~~~text
windows/tests/route-registry.ps1
packaging/windows/test_managed_leshy.ps1
~~~

Record script logs and before/after route snapshots as release evidence. A hosted cross-build cannot replace this gate.

## 24.4 One Leshy release for both OSes

Current Kikimora packaging is inconsistent: Linux checkout uses Leshy master, Windows checkout uses windows-support, while linux/install.sh still expects Leshy 0.4.0. Remove this split.

After L10-L15 are green:

1. bump Leshy Cargo.toml version from 0.5.2 to 0.6.0;
2. update Cargo.lock;
3. tag the exact green Leshy commit v0.6.0;
4. push tag;
5. in Kikimora .github/workflows/build-packages.yml change both Linux and Windows Leshy checkout refs to v0.6.0; delete windows-support usage;
6. change linux/install.sh EXPECTED_VERSION to 0.6.0;
7. update packaging tests to require ^leshy 0.6.0$ rather than 0.5.x/0.4.0;
8. build both package families from that same tag;
9. record leshy --version in package test output.

Do not release Kikimora managed cutover with Linux and Windows embedding different Leshy revisions.

---

# 25. Exact verification commands

## 25.1 Kikimora local quality gates

~~~bash
cd toad
test -z "$(gofmt -l .)"
go mod tidy
git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
go test -race ./...
~~~

Build portability:

~~~bash
cd toad
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad ./internal/testtoad/cmd/ready-toad
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad ./internal/testtoad/cmd/ready-toad
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
~~~

## 25.2 Linux privileged Kikimora/managed gates

~~~bash
sudo bash linux/tests/toad/selected-routes-netns.sh

sudo env \
  KK_CORE_BIN=/path/kikimora-core \
  KK_LESHY_BIN=/path/leshy \
  bash linux/tests/toad/managed-leshy-smoke.sh

sudo env \
  KK_CORE_BIN=/path/kikimora-core \
  KK_READY_TOAD_BIN=/path/ready-toad \
  KK_LESHY_BIN=/path/leshy \
  bash linux/tests/toad/managed-leshy-routing-netns.sh
~~~

## 25.3 Leshy local gates

~~~bash
cargo fmt -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
sudo bash tests/linux-route-registry-netns.sh
~~~

Capabilities:

~~~bash
leshy capabilities --json | jq -e '.capabilities == ["af_unix_control","managed_selected_routes_v1"]'
~~~

## 25.4 Windows VM gates

Elevated PowerShell:

~~~powershell
powershell -ExecutionPolicy Bypass -File windows/tests/route-registry.ps1

powershell -ExecutionPolicy Bypass -File packaging/windows/test_managed_leshy.ps1 `
  -KikimoraCore C:\path\kikimora-core.exe `
  -ReadyToad C:\path\ready-toad.exe `
  -Leshy C:\path\leshy.exe `
  -WorkDir C:\Temp\kk-managed-test `
  -InterfaceA KikimoraTestA `
  -InterfaceB KikimoraTestB
~~~

## 25.5 Package/release gates

Before release:

~~~text
leshy --version == leshy 0.6.0 in Linux package
leshy.exe --version == leshy 0.6.0 in Windows package
both workflows checkout v0.6.0
no windows-support checkout remains
linux installer EXPECTED_VERSION == 0.6.0
~~~

---

# 26. What tests must inspect, not merely execute

A zero exit status is never sufficient for route correctness.

## 26.1 Linux selected identity

For every selected row assert:

- canonical destination prefix;
- table main/254;
- exact dev and ifindex;
- exact gateway or none;
- protocol 4 for Kikimora selected, Boot/3 for standalone Leshy local as defined by that backend;
- exact metric including IPv6 normalization;
- exact scope;
- row count for the prefix.

Use both platform API projection and ip -details -4/-6 route show table main.

For a managed park assert RTN_UNREACHABLE, exact prefix/table/protocol/ManagedParkMetric and the exact persisted SelectedParkIdentity.

## 26.2 Windows selected identity

For every row assert:

- DestinationPrefix;
- InterfaceIndex;
- NextHop including unspecified/on-link representation;
- Protocol;
- RouteMetric;
- same-prefix row count.

For managed Park assert the persisted loopback InterfaceIndex, loopback NextHop family, protocol and ManagedParkMetric. Never recompute current loopback identity when proving/removing an old Park.

## 26.3 Transaction proof

Crash tests inspect selected-routes.json after every injected boundary. The expected State/RetireDesired/Park.State/Park.RetireDesired must match section 22 exactly.

A kernel row without the preceding durable Applying/Retiring evidence is a test failure, even if final connectivity works.

## 26.4 Churn/performance proof

Compare counters before/after repeated queries:

- create/add calls;
- delete calls;
- replace calls;
- targeted reads;
- full snapshots/GetIpForwardTable2;
- controller RPC Ensure calls;
- Kikimora KernelMutations;
- DriftRepairs.

A flat visible FIB with repeated syscalls/RPCs is failure.

## 26.5 Foreign-row proof

Capture full exact identity before conflict attempt and compare after. Do not merely check that “a route still exists”. Wrong ifindex/gateway/protocol/metric/scope must remain unchanged.

---

# 27. Ordered landing sequence

Do not collapse these into a giant change. Every step must compile on all build tags/OSes it affects and pass its local gate before the next step.

## Kikimora K11 — exact route + park transaction model

Commit:

~~~text
feat(routing): persist exact selected and park transactions
~~~

Implement sections 1-3: shared constants, SelectedRouteIdentity, SelectedParkIdentity, two state machines, RetireDesired, schema2 validation, exact planner/foreign row model.

Gate: routing unit tests + go test ./... compilation adapters as needed.

## K12 — platform selected/park contract + Linux exact implementation

~~~text
feat(platform): reconcile managed selected routes by exact identity
~~~

Implement section 4 + Linux section 8 + fakes/Darwin unsupported methods + SnapshotSelectedRoutes.

Gate: Linux unit/netns selected tests.

## K13 — crash-safe SelectedRouteManager + strict startup restore

~~~text
feat(control): make selected ownership crash recoverable
~~~

Implement sections 2,5,6, failpoint unit seam, verification tokens, real ToadGeneration, restorePending, strict marker/checkpoint load.

Gate: selected_routes/control restart tests.

## K14 — managed parking inside SelectedRouteManager + reachable recovery

~~~text
fix(recovery): make managed parking transactional and reachable
~~~

Implement section 7, move managed registered parking out of parking.Manager, add WithdrawSelected, update core RecoveryDriver/ManagedRecoverySequenceForAction, and wire ManagedSelectedRoutes: m.managedSelected in production recoverRole.

Gate: core/control sequence tests + Linux netns.

## K15 — exact Windows selected/park writer

~~~text
feat(windows): reconcile managed selected routes without broad replacement
~~~

Implement section 9, winipcfg callback/cache, direct Windows tests.

Gate: windows-latest go test ./..., native build.

## K16 — selected reconciler + safety audit

~~~text
feat(control): reconcile managed route drift proactively
~~~

Implement section 10: StartSelectedRouteReconciler, SnapshotSelectedRoutes audit, requestManagedSelectedRecovery, diagnostics, observer restart.

Gate: fake-clock observer/audit tests + Linux drift netns.

## K17 — transactional managed activation API/CLI

~~~text
feat(control): make managed Leshy cutover transactional
~~~

Implement sections 12-13 mode API/CLI/gating, marker fsync ordering and cutover shell/PowerShell orchestration contracts. Do not enable production cutover by package default yet.

Gate: enable/disable failpoint tests + service/orchestration contract tests.

## Leshy L10 — exact RouteOwnership + reconcile modes + targeted epochs

~~~text
refactor(routing): retain exact observed route ownership
~~~

Implement sections 14-15 and adapt Linux/Windows/macOS/fake compilation. No platform cache rewrite yet.

Gate: common registry/macOS tests, all-OS compile.

## L11 — Linux coherent cache + IPv6 monitor

~~~text
fix(linux): remove DNS-path FIB dumps and watch IPv6 routes
~~~

Implement section 16.

Gate: clippy + unit + privileged netns.

## L12 — Windows exact local backend + notifications

~~~text
fix(windows): reconcile routes without broad same-prefix replacement
~~~

Implement section 17.

Gate: windows-latest unit + elevated local VM script.

## L13 — cross-platform managed AF_UNIX

~~~text
feat(routing): support Kikimora managed backend on Windows
~~~

Pin uds-compat=0.1.0, remove cfg TCP placeholder, add real Windows AF_UNIX framing tests.

## L14 — authority-on-demand + targeted reload

~~~text
fix(routing): avoid bulk managed IPC reconciliation
~~~

Implement section 18 AuthorityLost dispatch and section 19.1/19.2 hot-reload boundary/targeted zone invalidation.

Gate: 1000-entry zero-bulk-RPC and two-zone reload tests.

## L15 — graceful shutdown + capabilities + Leshy 0.6.0

~~~text
feat(service): support safe routing-backend cutover
~~~

Implement section 19 server/service cleanup and capabilities. Bump Cargo version to 0.6.0 only after all L10-L15 tests are green.

Gate: Linux SIGTERM cleanup, Windows service coordinator tests, all-OS CI.

## I1 — shared Ready-Toad fixture and Linux managed E2E

~~~text
test(toad): prove successful managed Leshy routing end to end
~~~

Implement section 20 and CI job.

## I2 — Windows managed E2E

~~~text
test(windows): prove managed Leshy ownership end to end
~~~

Implement section 21 and run on disposable VM.

## I3 — production one-writer cutover wiring

~~~text
feat(packaging): activate managed Leshy through one-writer cutover
~~~

Only after K17 + L13/L14/L15 + I1/I2 pass. Enable the orchestration path in installed Linux/Windows product.

## I4 — release pin and status

Exact order:

1. tag the green Leshy L15 commit v0.6.0;
2. switch both Kikimora package workflows to v0.6.0;
3. update Linux EXPECTED_VERSION/package tests;
4. run Linux + Windows runtime gates against the tag;
5. make Kikimora package artifacts;
6. update 08D and 08D1 Status to COMPLETE;
7. remove obsolete “managed Linux/macOS only”, windows-support and schema1 comments;
8. publish release.

---

# 28. Required regression matrix

| Scenario | Linux local | Windows local | Linux managed | Windows managed |
|---|---:|---:|---:|---:|
| first A exact read-back | MUST | MUST | MUST | MUST |
| first AAAA exact read-back | MUST | MUST | MUST | MUST |
| 1000 duplicate A zero extra mutation | MUST | MUST | MUST | MUST |
| 1000 duplicate AAAA zero extra mutation | MUST | MUST | MUST | MUST |
| no converged request-path full FIB scan | MUST | MUST | MUST | MUST |
| external v4 delete event repair | MUST | MUST | MUST | MUST |
| external v6 delete event repair | MUST | MUST | MUST | MUST |
| foreign wrong-ifindex untouched | MUST | MUST | MUST | MUST |
| foreign wrong-gateway untouched | MUST | MUST | MUST | MUST |
| foreign wrong-protocol untouched | MUST | MUST | MUST | MUST |
| foreign wrong-metric untouched | MUST | MUST | MUST | MUST |
| foreign wrong-scope untouched | MUST | N/A | MUST | N/A |
| Desired + stale exact Owned cleanup | MUST | MUST | MUST | MUST |
| interface/ifindex replacement | MUST | MUST | MUST | MUST |
| pre-existing compatible row not deleted on shutdown | MUST | MUST | N/A | N/A |
| created local row removed on graceful stop | MUST | MUST | N/A | N/A |
| v4 Ready / v6 not Ready | N/A | N/A | MUST | MUST |
| v6 Ready / v4 not Ready | N/A | N/A | MUST | MUST |
| selected Pending crash semantics | N/A | N/A | MUST | MUST |
| selected Applying crash adoption | N/A | N/A | MUST | MUST |
| selected Retiring RetireDesired semantics | N/A | N/A | MUST | MUST |
| park Pending crash semantics | N/A | N/A | MUST | MUST |
| park Applying crash adoption | N/A | N/A | MUST | MUST |
| park Active coexistence through restore | N/A | N/A | MUST | MUST |
| park Retiring exact removal | N/A | N/A | MUST | MUST |
| production recoverRole selects managed sequence | N/A | N/A | MUST | MUST |
| Park before WithdrawSelected before Quiesce | N/A | N/A | MUST | MUST |
| restart ownership/startup freeze | N/A | N/A | MUST | MUST |
| corrupt marker fails closed | N/A | N/A | MUST | MUST |
| corrupt/schema1 checkpoint fails managed startup | N/A | N/A | MUST | MUST |
| Forget exact removal | MUST | MUST | MUST | MUST |
| Flush error preserves retirement evidence | N/A | N/A | MUST | MUST |
| core unavailable no local fallback | N/A | N/A | MUST | MUST |
| socket loss 1000 routes zero bulk RPC | N/A | N/A | MUST | MUST |
| 32 concurrent Dirty hits one controller RPC | N/A | N/A | MUST | MUST |
| routing_backend hot reload rejected | MUST | MUST | MUST | MUST |
| one-zone reload leaves other zone fast | MUST | MUST | MUST | MUST |
| managed -> local rollback has zero overlap | N/A | N/A | MUST | MUST |
| Linux/Windows packaged Leshy same v0.6.0 | N/A | N/A | MUST | MUST |

---

# 29. Executor prohibitions

The executor must not:

- add another routing daemon or control socket;
- add TCP localhost or named pipes for Windows managed Leshy;
- use same-prefix/same-ifindex/protocol class as ownership proof;
- delete all same-prefix routes;
- call RouteReplace before exact previous ownership proof;
- drop unrecognized same-prefix rows during projection instead of marking foreign;
- restore checkpointed Active directly into fast-path verification;
- merge route and park transactions into one enum state;
- create/delete a park without Pending/Applying/Retiring durable evidence;
- maintain a second managed park ownership map in parking.Manager;
- infer IPv6 Ready from IPv4;
- let the observer independently unpark a recovery-owned prefix;
- use DNS TTL as selected-route retirement;
- silently ignore corrupt marker/checkpoint;
- automatically fall back to local Leshy writer;
- switch routing_backend through hot reload;
- full-dump FIB on every DNS route request;
- bulk-RPC every managed route after socket loss/reconnect;
- erase retirement evidence after failed delete;
- cancel Windows route notification from its callback;
- use current interface/loopback identity to delete a persisted old identity;
- call cross-compile “Windows runtime acceptance”;
- package Linux from Leshy master and Windows from windows-support;
- mark 08D/08D1 COMPLETE while any release gate below is red/unrun.

---

# 30. Definition of done

08D1 may be marked COMPLETE only when repository and runtime evidence proves all of the following.

## Ownership and crash safety

1. schema2 persists exact selected Desired/Owned/State/RetireDesired.
2. schema2 persists independent exact Park Desired/Owned/State/RetireDesired.
3. selected and park syscalls are bracketed by durable Pending/Applying/Retiring evidence.
4. real nonzero ToadGeneration is persisted.
5. restored Active is not fast-verified until live proof.
6. no protocol-class or same-ifindex ownership heuristic remains on managed path.
7. unrecognized same-prefix rows become explicit foreign conflicts.
8. Forget/Flush failures retain enough exact evidence for restart.

## Recovery and parking

9. production Manager.recoverRole passes ManagedSelectedRoutes and actually reaches managed sequence.
10. managed parking has one authority: SelectedRouteManager/schema2, not parking.Manager state.
11. exact ParkActive is durable before WithdrawSelected.
12. WithdrawSelected proves selected rows absent and parks present before Quiesce.
13. RestoreSelected coexists with ParkActive until exact selected proof.
14. ParkRetiring is durable before exact unpark.
15. Unfreeze is the final recovery action and advances route generation.

## Drift and performance

16. Kikimora Linux and Windows native observers exist and 10-second safety audit runs one full snapshot, not per-record snapshots.
17. standalone Leshy Linux no longer full-dumps FIB on converged DNS request path and subscribes IPv4+IPv6 route groups.
18. standalone Leshy Windows has coherent NotifyRouteChange2 cache and no broad same-prefix deletion/replacement.
19. targeted invalidation leaves unrelated verified entries fast.
20. managed AuthorityLost/Zone invalidations cause no bulk RPC; one Dirty demand causes one RPC.

## Cross-platform managed mode

21. Windows Leshy reaches Kikimora through real AF_UNIX using uds-compat 0.1.0.
22. no TCP/named-pipe/local-writer fallback exists.
23. v4/v6 readiness remains independent in Ensure/restore/DNS behavior.
24. Linux successful managed E2E passes.
25. Windows successful managed E2E passes on disposable VM.

## Cutover and shutdown

26. routing-manager-affecting hot reload is rejected; ordinary reload invalidates only affected zones.
27. local Leshy graceful SIGTERM/SCM stop removes only exact locally owned routes after retry/reload tasks are canceled.
28. managed Leshy process stop sends no automatic Forget/Flush.
29. Enable/DisableManagedLeshy transactions are fsync/marker ordered and rollback-safe.
30. local -> managed and managed -> local cutovers never overlap FIB writers.

## Release evidence

31. Kikimora and Leshy non-cancelled CI is green.
32. Linux privileged route-registry and managed-success jobs are green.
33. Windows local + managed VM scripts are green with before/after route snapshots.
34. Leshy is tagged v0.6.0 from the exact green commit.
35. Kikimora Linux and Windows packaging both consume v0.6.0; windows-support and installer 0.4.0 expectations are gone.
36. package contract tests report leshy 0.6.0 on both OSes.

Until all thirty-six are proven, keep status exactly:

> **08D implementation substantially landed; exact ownership, crash recovery, drift reconciliation, Windows parity and production cutover are still being completed.**
