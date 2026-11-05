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

---

# 31. Gap-closure addendum — contracts missing from the first execution packet

This section was added after rereading the full packet against the current Kikimora and Leshy code. It does not remove or relax any earlier requirement. Where it tightens an earlier contract, the stricter rule here is the release requirement.

The gaps below are cross-cutting. They must be closed before the first phase that depends on them; they are not deferred cleanup.

Severity:

- P0: can violate one-writer, exact ownership, fail-closed behavior, durable crash recovery or local privilege boundary.
- P1: can make acceptance nondeterministic, create unbounded work, break upgrade/boot operability or hide safety evidence.

| Gap | Severity | First owning stage |
|---|---:|---:|
| config/marker mode switch is not crash-transactional | P0 | 1 / 6 |
| no machine-verifiable proof that Leshy is stopped during mode change | P0 | 1 / 6 |
| packaged boot may expose Leshy before mode recovery completes | P0 | 1 / 6 |
| stale managed client/checkpoint is not fenced across reactivation | P0 | 1 / 5 / 6 |
| v1 wire cannot express final auth/fencing semantics | P0 | 1 / 5 |
| Windows interactive control socket has no privileged route-method auth | P0 | 1 |
| durable JSON replacement semantics are duplicated/underspecified on Windows | P1 | 1 |
| Ensure/Forget/observer/recovery checkpoint lock domain is incomplete | P0 | 2 |
| debounced observer leaves a stale fast-path window | P0 | 3 / 4 / 5 |
| audit can overlap/storm when snapshots are slow | P1 | 3 / 4 |
| route API has no explicit startup-ready gate | P0 | 3 / 6 |
| schema1 -> schema2 has no safe package upgrade choreography | P0 | 6 |
| authority/security state is insufficiently observable | P1 | 3 / 7 |
| E2E recovery trigger is vague/timing-dependent | P1 | 7 |
| second core on another socket can share the same state authority | P0 | 1 |
| legacy orchestration ownership is easy to conflate with Leshy backend mode | P1 | 1 |
| runServe regenerates Leshy config before persistent mode restore | P0 | 1 / 6 |
| Windows packaged core has no explicit persistent StateDir in service arguments | P0 | 1 |
| moving Leshy master makes cross-repo acceptance non-reproducible | P1 | 0 / 8 |
| Windows exact identity omits InterfaceLuid | P0 | 3 / 5 |
| selected route calls/Freeze have no bounded liveness contract | P0 | 2 / 4 / 5 |
| foreign integrity conflict reuses a generic failure path | P0 | 3 |
| service-manager dependency does not imply application startup readiness | P0 | 1 / 6 |
| transition journal identifiers/generations/digests are not deterministic enough for crash recovery proof | P0 | 1 |
| “exact ownership” does not state the byte-identical privileged-replacement trust boundary | P0 | 2 / 3 / 5 |
| Windows create row is not required to use InitializeIpForwardEntry and notifications are incomplete rows | P0 | 3 / 5 |
| Windows park-vs-selected proof compares route metric offset but not effective interface+route metric | P0 | 3 / 9 |
| common ManagedSelectedRouteProtocol=4 is wrong cross-platform; Windows 4 means ICMP, not NETMGMT | P0 | 2 / 3 / 5 |

## 31.1 Managed-mode change is itself a crash-safe transaction

Sections 12-13 specify rollback when a function returns an error, but two durable files participate in the mode switch:

- generated Leshy config;
- leshy-managed.json.

A process crash, power loss or reboot can happen between those writes. In particular, a Managed -> Local transition can otherwise leave a local Leshy config next to a still-managed marker. After reboot that can violate the one-writer invariant.

Add a durable core-owned transition journal:

~~~go
type ManagedLeshyMode string

const (
    ManagedLeshyLocal   ManagedLeshyMode = "local"
    ManagedLeshyManaged ManagedLeshyMode = "managed"
)

type ManagedLeshyTransitionPhase string

const (
    ManagedTransitionPrepared      ManagedLeshyTransitionPhase = "prepared"
    ManagedTransitionConfigWritten ManagedLeshyTransitionPhase = "config_written"
    ManagedTransitionMarkerWritten ManagedLeshyTransitionPhase = "marker_written"
)

type ManagedLeshyTransition struct {
    Schema           int
    ID               string
    From             ManagedLeshyMode
    To               ManagedLeshyMode
    Phase            ManagedLeshyTransitionPhase
    Socket           string
    FromActivationID string
    ToActivationID   string
    FromGeneration   uint64
    ToGeneration     uint64
    Zones            []string
    ConfigSHA256     string
}
~~~

Path:

~~~text
<core StateDir>/leshy-mode-transition.json
~~~

The journal lives in the hardened core state directory, not beside the generated Leshy config.

Enable transaction is exactly:

~~~text
1 acquire Leshy writer fence
2 verify no transition journal
3 read committed FromGeneration, require it is not MaxUint64, set ToGeneration=FromGeneration+1, generate fresh ToActivationID; FromActivationID is empty
4 durably write transition: local -> managed, phase=prepared
5 durably write managed Leshy config
6 read back config and record ConfigSHA256
7 durably write journal phase=config_written
8 durably write schema-2 managed marker with ToActivationID/ToGeneration
9 durably write journal phase=marker_written
10 apply managed mode in memory
11 durably commit leshy-mode-generation.json to ToGeneration
12 durably remove transition journal
13 release writer fence
~~~

Disable transaction is exactly:

~~~text
1 acquire Leshy writer fence
2 require roles stopped + selected registry empty + Park nil
3 verify no transition journal; read active marker into FromActivationID and FromGeneration, require committed generation equals it, require it is not MaxUint64, set ToGeneration=FromGeneration+1 and ToActivationID empty
4 durably write transition: managed -> local, phase=prepared
5 durably write local Leshy config
6 read back config and record ConfigSHA256
7 durably write journal phase=config_written
8 durably remove managed marker
9 durably write journal phase=marker_written
10 apply local mode in memory
11 remove now-empty selected checkpoints
12 durably commit leshy-mode-generation.json to ToGeneration
13 durably remove transition journal
14 release writer fence
~~~

For Disable, marker_written means the durable marker operation, which is deletion, has completed.

Startup recovery occurs before ordinary managed-marker/checkpoint restore, before route authority is published, and before packaged Leshy is allowed to start. The boot order is therefore: acquire core.lock -> load transition journal -> if journal exists acquire the Leshy writer fence -> recover/finish transition -> release writer fence -> strict marker load -> checkpoint restore -> platform/reconciler readiness -> Serve/role Ready. If a journal exists but the writer fence is busy, core fails startup/route authority rather than rewriting mode files underneath a live Leshy process.

Config/marker consistency is checked by parsing the generated Leshy server config, not by filename/timestamp:

~~~text
marker absent:
    routing_backend must be local/platform
    managed ActivationID/ModeGeneration fields must be absent

marker present:
    routing_backend must be kikimora
    kikimora_socket/ActivationID/ModeGeneration must equal marker

no journal:
    require the consistency rules above
    mismatch -> startup failure

journal target managed:
    validate From/To generation relation and activation fields
    validate target socket/zones
    rewrite managed config when digest/content differs
    write/repair schema-2 marker with ToActivationID/ToGeneration
    apply managed mode in memory
    durably commit last_committed_generation=ToGeneration
    durably remove journal

journal target local:
    validate From/To generation relation and activation fields
    inspect selected checkpoint files directly before normal RestoreRole
    require every relevant checkpoint to be absent or schema2-empty with Park nil
    rewrite local config when digest/content differs
    durably remove managed marker
    apply local mode in memory
    durably commit last_committed_generation=ToGeneration
    durably remove journal
~~~

Recovery always rolls the recorded transaction forward. It never guesses from whichever of config/marker happens to exist.

Crash tests inject exit after every numbered durable boundary and restart from the same filesystem. Every restart must converge to the journal target mode without any interval in which both local and managed writers are permitted.

## 31.2 A real writer fence is required; “orchestration stopped Leshy” is not enough

The core API cannot prove from a caller assertion that Leshy is absent. A manual or buggy EnableManagedLeshy call while local Leshy is alive would violate invariant 0.1.

Add an OS-backed exclusive lifetime lock for Kikimora-generated Leshy installations.

Generated Leshy config contains:

~~~toml
writer_lock = "<platform path>"
~~~

Paths:

~~~text
Linux:   /run/kikimora/leshy-writer.lock
Windows: C:\ProgramData\Kikimora\state\leshy-writer.lock
~~~

Rules:

- Leshy acquires an exclusive non-blocking OS lock before constructing RouteManager, binding DNS, or touching routes.
- Leshy holds it for the process lifetime.
- A second Leshy using the same generated installation fails before route/DNS mutation.
- EnableManagedLeshy and DisableManagedLeshy acquire the same lock and fail when it is busy.
- Core holds the lock for the whole transition-journal transaction.
- Ordinary route operations do not hold this lock; it is a process/mode fence, not the kernel mutation mutex.

Use real OS locks, not PID-file authority:

- Linux: flock with LOCK_EX and LOCK_NB;
- Windows: LockFileEx exclusive and non-blocking.

The Rust side wraps this as WriterLease; the Go side wraps the same contract behind one leshyWriterFence interface.

Required tests:

- one Leshy holds lock -> second Leshy fails;
- local Leshy holds lock -> Enable denied and config/marker unchanged;
- managed Leshy holds lock -> Disable denied and config/marker unchanged;
- abnormal process exit releases the OS lock;
- mode transaction holds the lock across every failpoint;
- lock file contents are never treated as authority.

This replaces the weak prose-only “Leshy is stopped” precondition with a machine-verifiable fence.

## 31.3 Boot ordering must let core recover mode state before packaged Leshy starts

Linux already has kikimora-core.service Before=leshy.service. Add a hard packaged dependency so a failed core/mode-recovery start prevents Kikimora’s Leshy from starting:

~~~text
Requires=kikimora-core.service
After=kikimora-core.service
~~~

Keep the existing core Wants=leshy.service; the ordering edge remains core Before Leshy. Add a service-contract test for the combined unit graph and a failure fixture where core startup fails on a corrupt transition journal and Leshy never reaches ExecStart.

This belongs to Kikimora packaging only, not generic standalone Leshy.

Windows currently relies on auto/delayed start. Add a real SCM dependency:

~~~text
leshy depends on the installed Kikimora Core service
~~~

Use the actual package service name rather than a new hardcoded display name.

Boot acceptance:

1. leave the transition journal at each phase;
2. restart/reboot the service stack;
3. core starts first and completes journal recovery;
4. only then Leshy acquires writer_lock;
5. Leshy reads config consistent with marker/mode generation;
6. no local/managed writer overlap is observed.

Windows package tests inspect the dependency, not merely delayed-auto-start configuration.

## 31.4 Activation identity fences stale clients and stale checkpoints

A schema-2 checkpoint from an earlier managed activation must not be accepted only because role/zone names still match. Likewise, a stale Leshy process must not continue sending route intents after Disable and re-Enable.

Upgrade the managed marker to schema 2:

~~~go
type managedModeMarker struct {
    Schema         int
    Socket         string
    CoreVersion    string
    ManagedZones   []string
    ActivationID   string
    ModeGeneration uint64
}
~~~

ActivationID:

- 128 bits from crypto/rand;
- lowercase hex;
- generated for every Local -> Managed activation;
- never reused;
- empty invalid in marker schema 2.

ModeGeneration:

- increments on every completed Local <-> Managed transition;
- the transition journal stores FromGeneration and ToGeneration; ToGeneration must equal FromGeneration+1 exactly;
- overflow is a hard error;
- the monotonic committed counter is persisted independently at:

~~~text
<core StateDir>/leshy-mode-generation.json
~~~

with schema + last_committed_generation only. It is a counter, not a second mode authority.

Startup/recovery compares the counter with the journal:

~~~text
last_committed_generation == FromGeneration:
    transaction is in flight
    roll forward to ToGeneration

last_committed_generation == ToGeneration:
    generation commit already happened
    verify target config/marker/mode and remove leftover journal

anything else:
    stale/corrupt journal
    startup failure
~~~

On successful transition recovery/commit, durably write last_committed_generation=ToGeneration before deleting the transition journal. A managed marker generation lower than the committed counter without a matching journal is corruption/startup failure.

SelectedRoutesCheckpoint schema 2 also stores:

~~~go
ActivationID   string
ModeGeneration uint64
~~~

Restore requires exact equality with the active marker.

Generated managed Leshy config stores:

~~~toml
kikimora_activation_id = "<id>"
kikimora_mode_generation = <n>
~~~

Every managed route request carries those fields explicitly:

~~~go
type LeshyRouteIntent struct {
    Zone           string
    Prefix         string
    Source         LeshyRouteSource
    ActivationID   string
    ModeGeneration uint64
}
~~~

Controller order:

~~~text
privileged auth
-> marker exists
-> ActivationID/ModeGeneration exact match
-> managed-zone activation check
-> selected-route transaction
~~~

Mismatch is a non-retryable denial with zero registry/kernel mutation.

Tests:

- stale checkpoint ActivationID -> startup failure under active marker;
- stale managed request after Disable -> denied;
- old request after Disable + re-Enable with same zone/socket -> denied;
- current activation succeeds;
- crash never creates two valid active ActivationIDs;
- diagnostics expose ModeGeneration and only a fingerprint of ActivationID.

## 31.5 The final wire is managed_selected_routes_v2

The current managed_selected_routes_v1 capability predates ActivationID and privileged request authentication. Those are ownership/security semantics, not ignorable optional fields.

Keep API framing version 3. Add final capabilities:

~~~text
af_unix_control
managed_selected_routes_v2
privileged_control_auth_v1
~~~

Final schema-2 activation requires all three.

Compatibility:

- retain v1 only during the explicitly defined pre-schema2 compatibility window through Stages 1-9; Stage 10 removes v1 activation support;
- schema-2 managed mode never activates a v1-only Leshy;
- old v1 clients fail closed against schema-2 managed mode;
- final cutover/preflight uses v2 only;
- earlier v1 references in sections 13, 19 and 24 remain baseline history and are superseded for release activation by this v2 requirement.

Cross-repository rollout order is fixed so one repository can merge without breaking the currently packaged v1 pair:

~~~text
1 Leshy parser gains backward-compatible auth-file + ActivationID/ModeGeneration fields: they are absent only in legacy v1 config and required in v2 managed config; Leshy can speak v2 while local mode and old v1-managed config still work.
2 Kikimora learns v2 parsing/auth/schema2 types but does not auto-convert a schema1 active installation.
3 schema2 EnableManagedLeshy requires the v2 capabilities and privileged auth.
4 schema1/v1 route handling remains a temporary compatibility surface only for pre-upgrade development/old-running pairs.
5 package upgrade drains schema1 to local before replacing the old pair.
6 after Stage 9 acceptance, Stage 10 removes v1 activation support.
~~~

Do not merge a change that makes auth mandatory for an already-running schema1/v1 installation before the matching Leshy capability can be deployed. Conversely, a v2 Leshy pointed at an old core must fail managed startup cleanly and never select its local writer as fallback.

Temporary v1 acceptance is gated by the absence of a schema2 marker/transition target. Once a schema2 marker exists, v1 requests are denied before route mutation regardless of socket peer permissions.

Final Leshy capabilities output includes all three strings sorted lexicographically.

Compatibility tests:

| Core | Leshy | Result |
|---|---|---|
| v2 core | v2 client/current activation | allowed |
| v2 core/schema2 marker | v1-only client | activation refused |
| v2 core/local mode | v1-only Leshy local | unaffected |
| old core | v2 managed Leshy | fail closed/no local fallback |
| v2 core | stale ActivationID | denied/no mutation |

## 31.6 Privileged route-control methods need application authentication

Windows AF_UNIX intentionally grants BUILTIN\Users read/write so the desktop UI can connect, and Windows AF_UNIX does not expose SO_PEERCRED. Therefore socket ACL/type is not enough authorization for route mutation.

Keep desktop access. Add a separate service-to-service bearer credential.

Core creates once before Serve:

~~~text
<core StateDir>/control-route-auth.key
~~~

Creation rules:

- on a fresh/local installation with no managed marker, missing key is generated before the control socket opens;
- create the temporary key inside the already-hardened StateDir, apply final 0600/DACL before writing/renaming, then durable-sync it;
- on an already-managed installation, a missing or malformed key is a fail-closed startup/route-authority error; do not silently rotate it while an existing Leshy process may still hold the old credential;
- explicit credential rotation is a future/operator transaction and must stop Leshy through writer_lock before replacing the key.

Properties:

- 32 random bytes;
- encode exactly as 64 lowercase hexadecimal characters plus one trailing newline on disk; the in-memory comparison uses the decoded 32 bytes;
- Unix mode 0600;
- Windows DACL SYSTEM + Administrators only;
- durable atomic creation;
- valid existing key reused on ordinary restart;
- never stored in leshy-managed.json;
- never returned by Handshake/Snapshot/diagnostics;
- never logged.

Generated Leshy config contains only:

~~~toml
kikimora_auth_file = "<path>"
~~~

Default packaged paths are:

~~~text
Linux:   /var/lib/kikimora/core/control-route-auth.key
Windows: C:\ProgramData\Kikimora\state\core\control-route-auth.key
~~~

Leshy reads the file at startup and sends the credential with privileged requests. kikimora-core CLI commands that invoke EnableManagedLeshy/DisableManagedLeshy read the same default file; test/custom-StateDir commands accept an explicit --auth-file path. The token is never accepted as a command-line literal because process listings would expose it.

Add Request field conceptually as PrivilegedAuth with JSON name privileged_auth.

Privileged methods are exactly:

~~~text
EnsureLeshyRoute
ForgetLeshyRoute
FlushLeshyZone
EnableManagedLeshy
DisableManagedLeshy
all test-failpoint-only control methods
~~~

Auth check runs before route-intent parsing or mutation. Use constant-time comparison.

There is no Linux root/group bypass; service clients use the same protocol on both OSes.

Stable failure code:

~~~text
privileged_auth_required
~~~

Rollout compatibility is explicit:

- under a schema2 marker or a v2 request, every privileged method above requires the bearer credential with no peer-based bypass;
- while an already-running schema1/v1 installation is being carried through the development/upgrade window from 31.5, legacy v1 Ensure/Forget/Flush may continue through the pre-existing peer policy only when the active marker/checkpoint is schema1 and no schema2 transition is in progress;
- EnableManagedLeshy/DisableManagedLeshy v2 and all schema2/test-failpoint methods always require auth;
- the legacy unauthenticated v1 branch is isolated in one compatibility function, emits a deprecation diagnostic counter, and cannot accept ActivationID/ModeGeneration fields;
- Stage 10 removes that compatibility function before release completion; section 35 cannot pass while it exists.

This is the only temporary exception to the final no-bypass rule and exists solely to avoid breaking the old pair before it can be drained.

Tests:

- Windows desktop user may Handshake/Snapshot without token but cannot schema2/v2 route or cutover mutate;
- Linux authorized peer without token also cannot schema2/v2 privileged-mutate;
- correct token succeeds;
- wrong/empty token performs zero mutation;
- token absent from logs/diagnostics;
- key ACL/mode package tests;
- Leshy missing/unreadable auth file fails managed startup and never falls back local.

## 31.7 One durable JSON primitive

Checkpoint, marker and transition journal must not use near-duplicate file writers.

Add to toad/internal/fsutil:

~~~go
func WriteAtomicFile(path string, data []byte, mode fs.FileMode) error
func WriteAtomicJSON(path string, value any, mode fs.FileMode) error
func RemoveDurable(path string) error
~~~

WriteAtomicJSON is a thin marshal + newline wrapper over WriteAtomicFile. The privileged auth key uses WriteAtomicFile, not an ad-hoc secret writer.

POSIX semantics:

~~~text
mkdir parent
temp in same directory
chmod
write JSON + newline
file.Sync
file.Close
atomic rename
SyncDir(parent)
~~~

Windows semantics:

- replacement of an existing destination must be replace-existing and write-through;
- use MoveFileExW with MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH for the final same-volume replacement; do not substitute plain os.Rename for this Windows path;
- do not assume POSIX rename behavior without a Windows test;
- directory sync stays best-effort as currently documented.

RemoveDurable removes the file and syncs its parent; missing is success.

Use this primitive for selected-routes.json, leshy-managed.json, leshy-mode-transition.json and mode-generation state.

Tests include replacement, write/sync/replace failure, durable delete, existing destination on Windows, permissions/ACL.

## 31.8 All selected mutation/checkpoint paths share one lock domain

The packet names applyMu but does not state every path that must use it. Without this, Ensure, Forget, observer repair, audit and recovery can interleave registry mutations and checkpoint snapshots.

Per role, selectedRoleState.applyMu guards:

- Ensure after admission;
- Forget;
- FlushZone;
- RestoreSelected;
- ParkRegistered;
- WithdrawSelected;
- ObserveRestoration;
- selected/park retirement continuation;
- observer targeted repair;
- audit targeted repair;
- startup reconciliation.

No registry mutation followed by checkpoint is legal outside applyMu.

Lock order:

~~~text
Manager/selected-manager mutex:
    short admission/metadata only
    released before blocking work

role.applyMu:
    route/park transaction + checkpoint boundary

registry internal mutex:
    inside registry call only

platform kernel/cache mutex:
    inside platform method

filesystem durable writer internal lock:
    last
~~~

Forbidden:

- acquire applyMu while Manager.mu is held;
- acquire Manager.mu while platform kernel mutex is held;
- platform callbacks acquiring applyMu;
- platform callbacks writing checkpoints.

Capture generation/operation before applyMu, then revalidate authoritative runtime after applyMu before mutation. If it changed, restart/abort the transaction.

Full audit snapshot is taken without role applyMu; before using it, acquire applyMu and recheck registry/generation. A stale snapshot never overwrites a newer transaction.

FlushZone holds one role applyMu for the whole deterministic prefix-ordered batch.

Race tests place barriers between registry transition and checkpoint and prove Ensure/Forget/observer cannot create a checkpoint omitting an already-authorized transaction.

## 31.9 Fast-path proof needs an observation sequence

Clearing verification only when a 100ms-debounced event reaches control leaves a stale-ACK window after a native route event has already arrived.

Extend observer contract with a monotonically increasing Sequence:

~~~go
type SelectedRouteInvalidation struct {
    Kind     SelectedRouteInvalidationKind
    Prefix   netip.Prefix
    IfIndex  int
    Sequence uint64
}

type SelectedRouteObservation interface {
    CurrentSelectedRouteSequence() uint64
}
~~~

Rules:

- native callback increments Sequence before enqueue/coalescing;
- overflow is fatal, never wrap;
- callback performs no FIB scan;
- debouncer keeps highest Sequence and union of affected scopes;
- detected event gap -> Full;
- SelectedRouteManager tracks processedObservationSequence.

Fast path additionally requires:

~~~text
processedObservationSequence == CurrentSelectedRouteSequence()
~~~

Thus an event that has reached the process disables zero-I/O fast paths before debounce delivery.

When targeted events are processed:

1. clear affected tokens;
2. leave unaffected tokens intact;
3. advance processedObservationSequence.

Unrelated entries become fast again without kernel I/O.

Full clears all tokens then advances processed sequence.

Exact read-back/snapshot returns a stable proof using double sampling:

~~~text
loop:
    before = CurrentSequence
    exact read-back/snapshot
    after = CurrentSequence
    if before != after: retry
    return proof at after
~~~

If a later event arrives, CurrentSequence changes and the token cannot be used until that event is processed.

Standalone Leshy local backends get the analogous observation sequence. AuthorityOnDemand uses a separate authority sequence for controller subscription changes/loss.

Guarantee boundary:

> User space prevents stale fast ACK after the OS/controller notification reaches the process. It cannot observe an external mutation before the OS/controller delivers any notification; periodic audit remains the bounded-loss safety net.

Tests block between callback sequence increment and debounced handling and prove no stale fast ACK occurs.

## 31.10 Audit and observer restart are single-flight and bounded

A 10-second ticker must not start overlapping full snapshots.

Kikimora selected reconciler:

- one audit singleflight for the process;
- tick while busy increments AuditSkippedBusy and starts nothing;
- full audit deadline 5 seconds;
- observer-loss immediate audit uses the same singleflight;
- one SnapshotSelectedRoutes call maximum per audit;
- snapshot error invalidates zero-I/O proof until targeted proof or later successful audit;
- audit failure never triggers broad delete/replace;
- resubscribe backoff stays independent.

Observer event queue is bounded/coalesced. No goroutine per kernel event. Full dominates targeted events.

Leshy 30-second local audit uses the same single-flight rule.

Diagnostics add AuditSkippedBusy, AuditFailures, ObserverSequence and ProcessedSequence.

Stress test: fake 12-second snapshot + several ticks + 10,000 route events -> one snapshot, bounded event memory, no foreign mutation, later convergence.

## 31.11 Route authority has an explicit startup-ready gate

Control socket availability is not route-authority readiness.

Add manager state routeAuthorityReady and routeAuthorityError.

It becomes ready only after:

1. privileged auth key is ready;
2. transition-journal recovery completed;
3. marker validated/applied;
4. marker/checkpoint ActivationID and ModeGeneration validation completed;
5. selected registry restore completed;
6. platform writer wired;
7. native observer subscription established or explicitly supported observerless policy applies;
8. initial stable selected snapshot/audit completed where required;
9. selected reconciler is running.

During the final packaged startup sequence, the control listener is not bound until this initial authority gate is ready (section 31.22). The route-method guard still exists independently: tests/internal callers before Serve, and any future runtime transition that sets routeAuthorityReady=false, receive retryable route_authority_initializing with zero route mutation.

routeAuthorityReady means the routing authority infrastructure and initial kernel/checkpoint observation are coherent; it does not mean every restored role has completed its per-role managed recovery. A role with restorePending/frozen remains non-Ready and its Ensure stays denied until that role's normal recovery/CompleteStartupRestore finishes.

Marker-managed roles cannot publish product Ready while routeAuthorityReady is false or while their selected role state is restorePending/frozen.

Tests call the handler directly before the gate and also race a real Leshy process against service startup; neither path may receive a route ACK before restore/audit readiness.

## 31.12 Schema-1 to schema-2 requires an upgrade choreography

The packet correctly refuses to guess exact ownership from schema 1. A release still needs a safe upgrade path for machines already carrying schema-1 managed state.

No automatic schema-1 adoption.

Before replacing old binaries:

~~~text
1 detect old managed marker/checkpoints
2 stop Leshy / prove old process stopped
3 call old core DisconnectAll
4 wait all roles Stopped
5 read the old schema1 checkpoint prefix set before draining, then require:
    - old checkpoint/diagnostics report no registered selected ownership/parks after DisconnectAll;
    - for every schema1 prefix, no old managed park signature remains;
    - if any same-prefix forwarding row remains and old schema1 cannot prove it foreign vs stale-owned, abort upgrade rather than adopting/deleting it;
    - unrelated prefixes are ignored
6 stop old core
7 back up old marker/checkpoints/config
8 remove old managed marker only after emptiness proof
9 write/generate local config
10 install new binaries
11 start new core in local mode
12 health check
13 later enable managed v2 through normal cutover
~~~

If emptiness cannot be proven, abort upgrade and restore/start the old installation.

Implement on Linux packaging and Windows installer/MSI.

Fixtures:

- schema1 managed nonempty -> drain succeeds;
- old core unavailable -> upgrade aborts untouched;
- drain foreign conflict -> abort;
- crash after backup/removal -> installer resumes to defined local state;
- old local install -> normal upgrade.

Keep backups until first successful schema2 managed activation or explicit cleanup.

## 31.13 Diagnostics expose authority state without secrets

Extend diagnostics/status with:

~~~text
managed_mode
mode_generation
activation_id_fingerprint
mode_transition
route_authority_ready
route_authority_error
selected_checkpoint_schema
observer_sequence
processed_observer_sequence
audit_failures
audit_skipped_busy
privileged_auth: configured|missing|invalid
~~~

Activation fingerprint is the first 12 hex characters of SHA-256(ActivationID).

Never expose bearer auth token or auth-file contents. Failure bundles redact auth material.

## 31.14 E2E needs one deterministic recovery trigger

Sections 20-21 require exact recovery-order observation but previously leave the trigger vague.

Under the kikimora_test_failpoints build tag add TestTriggerManagedRecovery.

It:

- carries role;
- requires privileged auth;
- is only dispatched in test builds;
- calls the same requestManagedSelectedRecovery(role, "test trigger") used by production paths;
- does not invoke driver steps directly;
- returns after recovery is accepted/in-flight.

Release builds do not advertise it and return unsupported_method.

Linux/Windows E2E combines this method with the withdraw-selected recovery barrier. A separate integration test still triggers a real underlay change and proves production observation reaches the same requestManagedSelectedRecovery path.

## 31.15 Singleton core/state ownership

One packaged StateDir must have one core.

Core acquires an exclusive OS lifetime lock at:

~~~text
<core StateDir>/core.lock
~~~

before reading transition/marker/checkpoints.

Rules:

- real OS lock, not PID contents;
- held for core lifetime;
- second core on same StateDir fails before socket, Toad launch or route mutation;
- packaged mode forbids different StateDirs pointing at the same managed marker/config authority;
- test second core -> zero mutation.

This closes the “second core on another socket” hole.

## 31.16 Do not conflate orchestration ownership and Leshy backend mode

orchestration-ownership.conf controls old-script vs Go ownership:

~~~text
routing_owner
tunnel_owner
endpoint_owner
~~~

It does not prove whether live Leshy is local-backend or managed-backend.

Authorities stay distinct:

~~~text
orchestration-ownership.conf:
    legacy/external/go product orchestration ownership

leshy-managed.json + transition journal:
    configured Leshy routing backend mode

writer_lock:
    runtime proof Leshy is absent during mode mutation

ActivationID/ModeGeneration:
    managed IPC/checkpoint fencing generation
~~~

Tests cover these separately. Do not simplify them into one flag.

## 31.17 Persistent core StateDir and startup ordering are mandatory

Current runServe regenerates the Leshy config immediately after NewManagerWithConfigDir, before desired-state persistence, selected writer wiring, strict managed marker/checkpoint restore, or any future transition-journal recovery. That order must change before mode transactions are enabled.

Managed selected routing requires a non-empty persistent core StateDir.

Packaged paths are fixed:

~~~text
Linux core StateDir:
    /var/lib/kikimora/core

Windows core StateDir:
    C:\ProgramData\Kikimora\state\core
~~~

Accordingly the packaged auth file is:

~~~text
Linux:
    /var/lib/kikimora/core/control-route-auth.key

Windows:
    C:\ProgramData\Kikimora\state\core\control-route-auth.key
~~~

Windows package/service installation must explicitly pass the persistent directories, including at least:

~~~text
serve
--state-dir C:\ProgramData\Kikimora\state\core
--config-dir C:\ProgramData\Kikimora\toads
--leshy-publication-dir C:\ProgramData\Kikimora\leshy\vpn
~~~

and any existing ownership/provider arguments required by that package. Do not depend on an empty command-line default for persistent state.

A non-packaged/development core may start without StateDir only when managed selected routing is unavailable: it does not advertise managed_selected_routes_v2, cannot EnableManagedLeshy, and privileged selected-route methods fail with persistent_state_required.

Final runServe startup order is:

~~~text
1 parse options and establish persistent StateDir policy
2 acquire <StateDir>/core.lock
3 create/load privileged auth key
4 load/validate role configs and construct Manager
5 DO NOT regenerate Leshy config yet
6 read/recover leshy-mode-transition.json
7 read/validate committed ModeGeneration state
8 strict read/apply schema-2 managed marker
9 load/validate marker-bound schema-2 selected checkpoints
10 regenerate/verify Leshy config for the recovered committed mode
11 create/wire platform RouteManager + RecoveryDriver
12 start native selected observer/reconciler and complete the initial stable selected snapshot/audit
13 establish routeAuthorityReady for infrastructure; restored per-role restorePending/frozen state remains intact
14 start product/underlay/sleep/interface observers
15 load/apply persisted desired lifecycle state and start role work; checkpointed managed roles enter normal managed recovery before Role Ready
16 start control Serve only after the infrastructure gate is ready
~~~

If transition recovery itself needs config generation in step 6, it uses the mode-explicit regeneration helper from section 12 and then step 10 verifies the final result. No generic RegenerateLeshyConfig call is allowed before committed mode is known.

Startup failures in steps 2-14 occur before managed route authority is served. With the packaged service dependency from 31.3, Leshy does not start against half-restored mode state.

Required tests:

- spy RegenerateLeshyConfig and prove it is not called before transition/marker restore;
- transition target managed rewrites managed config before Serve;
- transition target local rewrites local config before Serve;
- empty StateDir refuses v2 activation;
- Windows package contract contains explicit persistent StateDir/config paths;
- restart preserves desired state, auth key, mode generation, marker and checkpoints under the same StateDir.

## 31.18 Cross-repository integration must use a pinned companion revision

Section 24 currently describes the Kikimora cross-repo job as checking out Leshy master. That makes a Kikimora commit non-reproducible: the same CI job can pass or fail later against a different Leshy commit.

Add a tracked companion-revision file in Kikimora:

~~~text
.github/managed-routing-companion.env
~~~

Content during development:

~~~text
LESHY_REF=<full 40-hex commit SHA>
~~~

Rules:

- never use master, a moving branch, or “latest” in authoritative managed-routing integration jobs;
- the workflow reads LESHY_REF from this file and checks out exactly that SHA;
- print the resolved Leshy SHA at job start and upload it with integration evidence;
- a Kikimora PR that depends on new Leshy behavior first lands/identifies the green Leshy commit, then updates LESHY_REF in the same Kikimora PR;
- if the referenced Leshy SHA is unavailable, CI fails rather than falling back to master;
- Stage 10 changes LESHY_REF from the development SHA to the final v0.6.0 tag only after the tag is created, then verifies the tag resolves to the expected SHA;
- package workflows still use the explicit v0.6.0 tag as section 24.4 requires.

During parallel development, the Stage handoff record carries both the Kikimora SHA and pinned LESHY_REF so a later executor can reproduce the exact pair.

Add a workflow contract test that fails if the managed-routing integration checkout contains ref: master, ref: windows-support, or omits an explicit ref.

This is a P1 reproducibility gate: it does not change route semantics, but no vertical acceptance result counts as release evidence when the companion revision is moving.

## 31.19 Windows exact identity includes InterfaceLuid, not only InterfaceIndex

The earlier platform-neutral identity lists IfIndex but Windows IP Helper also exposes InterfaceLuid and route APIs prefer the LUID when it is supplied. Persisting only a numeric index is not a sufficient crash/restart identity boundary because interface indices can change or be reused.

Extend both exact identities additively:

~~~go
type SelectedRouteIdentity struct {
    // fields from section 1.1...
    InterfaceLuid uint64
}

type SelectedParkIdentity struct {
    // fields from section 1.1...
    InterfaceLuid uint64
}
~~~

JSON name:

~~~text
interface_luid
~~~

Rules:

- Linux writes zero and ignores the field.
- Windows NormalizeSelectedRoute and kernel projection store both InterfaceLuid and InterfaceIndex from the exact MIB row.
- Windows NormalizeManagedPark stores the loopback LUID and index.
- A Windows Desired/Owned/Park equality comparison includes both.
- Exact Windows deletion constructs the row with persisted LUID and persisted index; it does not resolve the current adapter and substitute a new identity.
- When persisted LUID is nonzero, any observed same-prefix row with a different LUID is foreign even if index/next-hop/metric happen to match.
- schema2 Windows checkpoints require nonzero InterfaceLuid for Active Owned selected/Park identities.
- a recreated adapter intentionally produces a new Desired LUID/index; old Owned remains the exact previous identity until replacement/retirement is proven.
- do not persist Age or Origin as owner identity: those are kernel-maintained observation fields, not writer-controlled ownership tags.

Extend Leshy RouteKernelIdentity additively:

~~~rust
pub(crate) struct RouteKernelIdentity {
    pub interface_luid: Option<u64>,
    // existing fields...
}
~~~

Windows local backend stores Some(LUID); Linux/macOS store None.

Required Windows tests:

- same index + different LUID -> conflict;
- same LUID + different index -> conflict/readback mismatch;
- persisted old LUID exact delete does not resolve the new adapter;
- adapter recreate creates new Desired and removes only old exact Owned after new proof;
- park exactness includes loopback LUID.

This requirement tightens sections 1, 9, 14 and 17; do not leave the LUID only in platform-private temporary structs.

## 31.20 Route transactions and Freeze are deadline-aware

Current SelectedRouteManager code can call the platform reconciler with context.Background, while Freeze waits indefinitely for inFlight via sync.Cond. One blocked platform call can therefore wedge recovery, shutdown and cutover forever.

No managed route mutation uses context.Background.

Controller API route mutation deadline:

~~~text
EnsureLeshyRoute / ForgetLeshyRoute / FlushLeshyZone:
    4 seconds maximum inside the core
~~~

The 4-second controller deadline intentionally expires before Leshy's existing 5-second control call timeout so the client receives a typed failure instead of abandoning a still-running controller operation.

Recovery/audit platform calls inherit their parent context but each individual kernel call is additionally bounded to 5 seconds. Use the earlier parent deadline if it is sooner.

Replace indefinite Freeze waiting with a context-aware zero-in-flight signal. Conceptual per-role state:

~~~go
type selectedRoleState struct {
    frozen       bool
    inFlight     int
    inFlightZero chan struct{}
    // existing fields...
}
~~~

Rules:

- when inFlight changes 0 -> 1, create a new open inFlightZero channel;
- every admitted operation decrements inFlight in defer;
- when it reaches zero, close the channel exactly once;
- Freeze(ctx, role) sets frozen=true first, snapshots the current zero channel, unlocks metadata mutex, then waits on channel or ctx.Done;
- Freeze timeout/error leaves frozen=true; it never reopens admission merely because recovery could not drain;
- a later recovery retry may call Freeze again and observe eventual zero;
- Unfreeze is the only path that clears frozen after successful recovery/startup restore;
- Forget/Flush/observer repairs use caller contexts and the same applyMu; no detached mutation goroutine survives request cancellation.

Platform implementations must check context before and after blocking/native work where the API permits it. A timeout after the kernel syscall but before checkpoint/read-back is handled by the existing Applying/Retiring durable state; the controller returns failure and restart/reconcile completes from evidence.

Checkpoint fsync is not falsely advertised as cancellable. If durable storage itself hangs, service watchdog/OS failure handling applies, but no goroutine is spawned to continue a checkpoint after the caller has been told success.

Tests:

- fake platform call blocks until context -> API returns before 5s and inFlight drains;
- Freeze times out while call is blocked and remains frozen;
- once blocked call exits, recovery retry proceeds;
- no mutation uses context.Background in selected_routes.go;
- Leshy sees controller timeout/error rather than its own 5s outer timeout in the normal injected-stall case;
- timeout after fake kernel Create with Applying checkpoint recovers by exact adoption;
- finalizeShutdown/ReleaseProduct uses a bounded shutdown context rather than context.Background, and timeout returns/logs incomplete cleanup instead of hanging service stop forever.

This is a P0 liveness/fail-closed contract because a permanently stuck Freeze makes one-writer cutover unverifiable.

## 31.21 Foreign-route integrity failure has a dedicated quarantine state

Section 10.5 previously said to use the existing failByConfiguration/disabled path. That path was designed for configuration errors, not for preserving selected/Park transaction evidence under a kernel ownership conflict.

Do not route selected integrity conflicts through generic automatic teardown.

Add per-role state:

~~~go
integrityQuarantined bool
integrityReason      string
~~~

failSelectedRouteIntegrity performs exactly:

~~~text
1 set selected role frozen=true
2 set integrityQuarantined=true and store reason
3 clear all verification tokens for the role
4 cancel/disable automatic selected recovery retries for the role
5 publish RoleFailed/Degraded diagnostic state with the integrity reason
6 keep configured Desired intent and all selected/Park checkpoint evidence
7 do NOT DisconnectRole
8 do NOT release selected routes/parks
9 do NOT stop/delete the foreign row
~~~

The Toad process is not automatically killed solely because of a foreign selected row: stopping it can remove the managed interface while an externally installed physical route still exists, which is not a stronger fail-closed guarantee. The system instead stops issuing new selected-route ACKs and reports the integrity incident explicitly. Section 0.7 remains the honest boundary: Kikimora cannot undo a foreign route it does not own.

While quarantined:

- Ensure returns non-retryable conflict/denial with zero mutation;
- observer/audit only observes and refreshes diagnostics;
- ordinary automatic recovery is not scheduled;
- explicit DisconnectRole remains an operator action and may retire exact owned evidence, but must still leave foreign rows untouched.

RetryRole is the recovery entry:

~~~text
RetryRole on integrityQuarantined role
-> one full exact selected snapshot under audit singleflight
-> if foreign conflict remains: keep quarantine, return conflict
-> if conflict is gone: clear integrityQuarantined only after snapshot proof
-> keep role frozen
-> requestManagedSelectedRecovery("integrity conflict cleared")
-> normal recovery owns eventual Unfreeze
~~~

If the role was explicitly disabled/disconnected by the operator, RetryRole follows the ordinary disabled-role semantics instead of silently re-enabling it.

Diagnostics expose integrity_quarantined and integrity_reason.

Tests:

- conflict does not call DisconnectRole/release resources;
- conflict leaves checkpoint byte-for-byte ownership evidence intact except diagnostics/state fields intended to change;
- new Ensure performs zero platform mutation;
- automatic recovery does not loop;
- Retry while foreign remains does nothing destructive;
- remove foreign fixture externally, Retry -> normal managed recovery -> Unfreeze;
- explicit operator Disconnect still never deletes foreign row.

This supersedes only the generic-failure-path choice in section 10.5; all earlier foreign-row non-mutation rules remain.

## 31.22 Service dependency needs an application-readiness barrier

A service-manager dependency alone is not enough:

- Linux kikimora-core.service is Type=simple, so systemd considers it started before runServe completes initialization.
- Windows kikimoraService currently reports svc.Running immediately after spawning runServe in a goroutine.

Therefore section 31.3 service dependencies are necessary but not sufficient.

Add a control-server readiness callback without creating another socket or health daemon.

Go control API shape:

~~~go
func ServeWithReady(
    ctx context.Context,
    socket string,
    manager *Manager,
    ready func(),
) error
~~~

Existing Serve remains a thin compatibility wrapper calling ServeWithReady with nil.

ServeWithReady order:

~~~text
prepare/remove stale socket
listenControl succeeds
protect socket/ACL succeeds
invoke ready exactly once
enter Accept loop
~~~

runServe calls ServeWithReady only after the final startup order in 31.17 has completed through routeAuthorityReady/mode readiness. Therefore “ready callback fired” means:

- committed mode recovered;
- Leshy config verified for that mode;
- checkpoint restore completed;
- platform/recovery/reconciler initialization completed;
- routeAuthorityReady is settled;
- control socket is actually bound and protected.

Linux packaged Leshy service adds:

~~~text
Requires=kikimora-core.service
After=kikimora-core.service
ExecStartPre=/usr/local/bin/kikimora-core wait-startup --socket /run/kikimora/core.sock --timeout 30s
~~~

Add the non-mutating CLI command:

~~~text
kikimora-core wait-startup --socket PATH --timeout DURATION
~~~

It repeatedly performs Handshake/Snapshot until:

- the socket is reachable;
- core reports startup_ready=true;
- mode_transition=none;
- in managed mode route_authority_ready=true.

For local mode, startup_ready=true is sufficient after committed local config verification.

It exits nonzero on timeout or permanent startup error. It never changes desired state, routes or mode.

The packaged core snapshot adds startup_ready bool. startup_ready becomes true immediately before ServeWithReady binds/listens; it is false/default in partial startup state and tests.

Windows service:

~~~text
status StartPending
spawn runServe with opts.ready callback closing a ready channel
select:
    ready channel -> report svc.Running
    runServe error first -> return service failure, never report Running
    startup timeout 30s -> cancel runServe and return service failure
then enter normal SCM request loop
~~~

The Windows Leshy SCM dependency from 31.3 now has real meaning because SCM does not start the dependent service until Kikimora Core reports Running after application readiness.

Do not use an arbitrary sleep or delayed-auto-start as the correctness mechanism.

Tests:

- Linux corrupt transition/checkpoint: core never serves ready; Leshy ExecStartPre fails and Leshy ExecStart is not reached;
- Linux slow fake startup: wait-startup blocks then succeeds exactly when ServeWithReady binds;
- Windows runServe early failure: service emits StartPending then failure, never Running;
- Windows slow startup: Running appears only after ready callback;
- Windows startup timeout cancels runServe and dependent Leshy never starts;
- ordinary successful boot has exactly one ready callback;
- core restart with managed Leshy preserves fail-closed behavior; no local fallback while socket is unavailable.

## 31.23 Mode-journal fields are deterministic

The transition record itself must be reproducible enough for recovery tests.

Rules:

- ID is a fresh 128-bit crypto/rand lowercase-hex transaction identifier, distinct from ActivationID;
- Zones is canonical sorted unique zone names;
- ConfigSHA256 is lowercase hex SHA-256 over the exact generated config bytes including the final newline;
- config generation for an identical semantic input is byte-deterministic;
- recovery verifies the digest by reading the final path, not by trusting the write buffer;
- unknown transition schema/phase/from/to is a startup error;
- a transition with From==To is invalid;
- FromActivationID is empty exactly when From==local and otherwise equals the active schema2 marker ActivationID;
- ToActivationID is nonempty exactly when To==managed and is freshly generated for that transition; it is empty when To==local;
- FromGeneration equals the committed generation at transaction start;
- ToGeneration equals FromGeneration+1 exactly and is the generation written to the target marker/config/counter;
- recovery never synthesizes or guesses any of these four from filenames or timestamps.

Tests render identical configs twice and require identical bytes/hash on Linux and Windows line-ending policy used by the generator.

## 31.24 Exact ownership has an explicit privileged-host trust boundary

No userspace route manager can distinguish two kernel rows that are byte-for-byte identical in every kernel-exposed identity field if a privileged external actor deletes one and recreates the other while the manager is not observing it.

Therefore define the exact safety claim precisely rather than promising impossible provenance detection.

For this packet:

~~~text
foreign row:
    a same-prefix row whose complete kernel-exposed identity does not match
    a checkpoint-authorized Desired/Owned/Park identity for the current
    transaction state

identity-continuous row:
    a row whose complete kernel-exposed identity exactly matches that
    authorized persisted identity
~~~

An identity-continuous row may be adopted only in the Applying/Retiring crash states already specified. Active restart still requires live read-back before fast verification.

Threat/trust boundary:

- root/Administrator or another process already holding route-mutation privilege is trusted not to maliciously forge a byte-identical Kikimora/Leshy row;
- protection against a malicious local administrator is out of scope because that actor can directly alter the FIB, kill services and read protected state;
- ordinary/unprivileged users remain in scope and must not obtain route-mutation authority through the control socket, which is why 31.6 is mandatory.

To minimize accidental identity collision, every writer-controlled field available on the platform remains in the identity:

Linux:
- prefix/table;
- OIF;
- next hop;
- protocol;
- metric;
- scope;
- route type/projection category.

Windows:
- prefix;
- InterfaceLuid;
- InterfaceIndex;
- NextHop;
- Protocol;
- Metric;
- selected-vs-Park projection category.

Do not claim Age or Origin as ownership tags on Windows because the stack controls them. Do not introduce a fake userspace nonce that is not represented in the kernel row and then treat it as kernel proof.

Documentation/tests must use the terms above. A test that creates an exact byte-identical row with Administrator/root privileges is testing the trusted-host boundary, not a distinguishable foreign-row case. Foreign-row tests deliberately differ in at least one kernel-exposed identity field and prove that field is preserved.

If a future platform gains a real owner/tag field that survives read-back and participates in row identity, add it to the persisted identity in a new schema rather than silently changing this trust boundary.

## 31.25 Windows MIB row initialization, incomplete notifications and effective metric proof

Microsoft's CreateIpForwardEntry2 contract requires a new MIB_IPFORWARD_ROW2 to be initialized with InitializeIpForwardEntry before the caller fills route-specific fields. The current Kikimora Windows row builder starts from a zero-value struct, which is not the required API contract.

Every Windows create-row builder, in both Kikimora and Leshy, uses exactly:

~~~text
row = zero MIB_IPFORWARD_ROW2
row.Init() / InitializeIpForwardEntry
set InterfaceLuid and InterfaceIndex from normalized desired interface
set DestinationPrefix
set NextHop
set Metric to the required route metric offset
set Protocol to the required writer protocol
CreateIpForwardEntry2
exact Get/table read-back
~~~

Do not construct a CreateIpForwardEntry2 row only with a struct literal.

The initialized profile is part of creation safety:

- ValidLifetime and PreferredLifetime must be infinite (0xffffffff);
- the writer does not intentionally create expiring selected/Park routes;
- read-back with finite lifetime is not accepted as an exact desired managed row;
- Age and Origin remain excluded because Windows owns them;
- platform-native tests record the actual stable Loopback/AutoconfigureAddress/Publish/Immortal values returned for selected and Park rows and require the projectors not to silently discard an unexpected safety-relevant difference. They are not used as a synthetic ownership nonce.

Delete semantics continue to use the exact persisted lookup identity: DestinationPrefix + NextHop + InterfaceLuid/InterfaceIndex. Metric/protocol/profile mismatches are still foreign/conflict checks before deletion; the code does not rely on DeleteIpForwardEntry2 to distinguish them.

NotifyRouteChange2 has a separate rule: the callback MIB_IPFORWARD_ROW2 is incomplete and is never projected as a complete selected/park row.

Callback copies only enough data for invalidation:

~~~text
DestinationPrefix
NextHop
InterfaceLuid
InterfaceIndex
notification type
~~~

It increments observation Sequence first and emits Prefix/Interface/Full invalidation. Complete identity, metric, protocol and route profile are obtained later by the normal stable read-back/cache refresh. No callback test is allowed to assert ownership from callback Metric/Protocol/flags.

### Windows effective metric

MIB_IPFORWARD_ROW2.Metric is a route metric offset. Windows route preference uses:

~~~text
effective_metric =
    MIB_IPFORWARD_ROW2.Metric
  + MIB_IPINTERFACE_ROW.Metric for that InterfaceLuid
~~~

Therefore the fixed ManagedParkMetric=42760 alone does not prove that a selected route with route-offset 0 wins over a loopback Park.

Extend the private Windows platform API seam with exact interface metric lookup by InterfaceLuid and include it in stable restoration proof.

Use uint64 arithmetic to avoid overflow while comparing uint32 components:

~~~text
selected_effective =
    uint64(selected_route.Metric) + uint64(selected_interface.Metric)

park_effective =
    uint64(park_route.Metric) + uint64(loopback_interface.Metric)
~~~

Rules:

- Park creation may proceed even when it would immediately outrank selected; early fail-closed blackholing during recovery is safe, but it must be diagnosed.
- WithdrawSelected still requires exact ParkActive, so after selected withdrawal Park is the protecting route.
- ObserveRestoration must not retire Park unless:
  1. exact restored selected Desired is present;
  2. exact persisted ParkActive is present;
  3. selected_effective < park_effective;
  4. no foreign same-prefix row has an effective preference capable of winning under the Windows route selection rules represented by the platform projection.
- if selected_effective >= park_effective, keep ParkActive, keep role frozen and return a typed restoration-not-winning error; do not delete the park to recover connectivity;
- interface metric change notification invalidates the restoration proof. The Windows observer must subscribe/translate the relevant interface-change signal already used by platform interface observation, or the 10-second full audit must read interface metrics as part of the proof. A changed interface metric never reuses an old winning token.
- diagnostics report selected route offset, selected interface metric, park route offset, loopback interface metric and the two effective metrics.

Do not change ManagedParkMetric dynamically per prefix in this packet. It remains a deterministic persisted route identity. Safety comes from proving effective preference before unpark, not from guessing an always-large-enough constant.

Windows tests:

- row builder calls Init before field overrides;
- created selected/Park rows read back infinite lifetimes;
- callback row with garbage/zero Metric/Protocol only causes invalidation and never ownership classification;
- selected offset 0 + interface metric 50000 vs Park 42760 + loopback metric 1 -> restoration stays frozen/ParkActive;
- selected interface metric 25 vs Park 42760 + loopback metric 1 -> selected winning proof succeeds;
- interface metric changes after proof -> token invalidated/re-audited before unpark;
- arithmetic uses uint64 and does not wrap at uint32 max;
- a same-prefix foreign row is still preserved regardless of effective metric.

This tightens sections 9, 11, 17, 21 and 26. Windows E2E must capture Get-NetIPInterface metrics alongside Get-NetRoute and explicitly prove the effective-metric inequality before accepting Park removal.

## 31.26 Managed route protocol is platform-native, not one shared numeric constant

The earlier section 1.1 defines ManagedSelectedRouteProtocol = 4 as if the protocol number had the same meaning on Linux and Windows. It does not.

Linux:

~~~text
RTPROT_STATIC = 4
~~~

Windows NL_ROUTE_PROTOCOL / RouteProtocol:

~~~text
RouteProtocolNetMgmt / MIB_IPPROTO_NETMGMT = 3
RouteProtocolIcmp    / MIB_IPPROTO_ICMP    = 4
~~~

Therefore the current Windows ownedRouteProtocol = 4 is not a static/network-management ownership tag; it identifies the ICMP protocol class. The plan must not preserve that bug.

Keep one semantic role in the routing model, but use platform-native protocol values.

Replace the shared numeric constant with platform helpers/contracts:

~~~go
const (
    ManagedSelectedRouteTable = 254
    ManagedParkMetric uint32 = 42760
)
~~~

Do not define a cross-platform ManagedSelectedRouteProtocol = 4.

The required platform values are exact:

~~~text
Linux selected/Park protocol:
    unix.RTPROT_STATIC == 4

Windows selected/Park protocol:
    int(winipcfg.RouteProtocolNetMgmt) == 3
~~~

Identity semantics:

- SelectedRouteIdentity.Protocol and SelectedParkIdentity.Protocol remain persisted exact observed numeric values.
- platform-neutral registry validation checks Protocol > 0; platform restore/writer validation checks the exact platform value;
- Linux schema2 Active/Owned/Desired/Park require protocol 4;
- Windows schema2 Active/Owned/Desired/Park require protocol 3;
- a Windows same-prefix row with protocol 4 is foreign even if every other field matches;
- a Linux same-prefix row with protocol 3 is foreign to the managed selected writer;
- checkpoints are not portable between OSes.

Add Platform string to SelectedRoutesCheckpoint schema2 with exact values linux or windows. Strict restore requires the running platform to match. This also prevents a copied Windows checkpoint with protocol 3 from being interpreted under Linux rules.

Windows writer changes required by this contract:

~~~text
ownedRouteProtocol = int(winipcfg.RouteProtocolNetMgmt) // 3
~~~

or eliminate the private numeric constant and use RouteProtocolNetMgmt directly at row construction/projection boundaries.

Leshy Windows local backend follows the same rule:

- do not use Linux RTPROT values when projecting Windows RouteProtocol;
- expected Windows static/managed rows use a Windows winipcfg protocol enum named semantically, never a Linux numeric constant;
- tests use the enum/semantic constant, never literal 4.

Migration/compatibility:

- existing pre-schema2 Windows routes created with protocol 4 are not silently adopted as schema2 Owned;
- Stage 6 schema1 drain removes them using the old running binary's known legacy behavior before v2/schema2 activation;
- if such a protocol-4 same-prefix row remains after drain and provenance is ambiguous, upgrade aborts rather than reclassifying it;
- fresh schema2 Windows activation creates only NETMGMT protocol 3 rows.

Required tests:

- compile-time/unit assertion int(winipcfg.RouteProtocolNetMgmt) == 3;
- Windows desired selected route protocol is NetMgmt, not Icmp;
- protocol-4 same-prefix row conflicts and is untouched by schema2 writer;
- Windows Park protocol is NetMgmt;
- Linux desired/Park remain RTPROT_STATIC 4;
- copied schema2 checkpoint with Platform mismatch fails startup before mutation;
- schema1 Windows protocol-4 fixture is drained by old-version upgrade choreography and never adopted into schema2;
- diagnostics render semantic name plus numeric value, for example netmgmt(3) on Windows and static(4) on Linux.

This supersedes the common numeric constant in section 1.1 while preserving its intent: protocol remains part of exact identity and cannot drift inside one platform implementation.

## 31.27 Supersession map — executor must follow the stricter addendum rule

The original sections 0-30 are intentionally retained for history/detail. Where an earlier statement conflicts with an addendum rule, do not choose between them. Use this map.

| Earlier section / wording | Final controlling rule |
|---|---|
| section 1.1 shared ManagedSelectedRouteProtocol=4 | section 31.26: Linux static(4), Windows netmgmt(3) |
| section 1 identity contains IfIndex only | section 31.19: Windows exact identity also persists InterfaceLuid |
| sections 2/12 marker/checkpoint keyed by role/zone/generation only | section 31.4: ActivationID + ModeGeneration fence marker, checkpoint and requests |
| section 2/12 schema2 file writes use local writer helpers | section 31.7: all durable state uses shared WriteAtomicFile/WriteAtomicJSON/RemoveDurable |
| section 4/10 event invalidation clears tokens after debounce | section 31.9: observation Sequence suppresses fast path as soon as event reaches process |
| section 5 Freeze uses inFlight/cond semantics | section 31.20: context-aware inFlightZero and bounded operation deadlines |
| section 7 managed registered parking in parking.Manager | section 7 addendum/K14: managed Park transaction lives only in SelectedRouteManager |
| section 10.5 generic failByConfiguration/disabled path | section 31.21: dedicated integrity quarantine, no automatic destructive teardown |
| section 10 audit ticker | section 31.10: single-flight, bounded deadline and bounded/coalesced event queue |
| section 11 Park removal based on selected route metric vs ManagedParkMetric | section 31.25: Windows requires effective route+interface metric proof |
| sections 12-13 Enable/Disable rollback inside one process | sections 31.1/31.23: durable transition journal, deterministic digest and crash roll-forward |
| section 12 prose precondition “Leshy stopped” | section 31.2: OS writer lease is the machine-verifiable fence |
| sections 12-13 managed marker schema/capability v1 | sections 31.4/31.5: marker schema2 and managed_selected_routes_v2 |
| existing control socket peer authorization | section 31.6: privileged bearer auth is mandatory for route/cutover mutations on every OS |
| service dependency/order only | sections 31.3/31.22: dependency plus application readiness barrier |
| current runServe early RegenerateLeshyConfig | section 31.17: persistent StateDir and transition/marker/checkpoint restore precede config regeneration/Serve |
| section 9/17 Windows zero-value MIB row construction | section 31.25: InitializeIpForwardEntry/row.Init before field overrides |
| section 9/17 NotifyRouteChange2 row projection | section 31.25: callback row is invalidation-only, never complete ownership proof |
| section 14 exact identity claim without provenance caveat | section 31.24: byte-identical privileged replacement is inside the trusted-host boundary |
| section 20/21 vague “trigger recovery” | section 31.14: test-tag TestTriggerManagedRecovery + real recovery barrier |
| section 24 cross-repo checkout of moving master | section 31.18: pinned companion SHA/tag |
| section 30 original 36-item Definition of Done | section 35: original 36 plus all supplemental completion gates |

No executor may implement an earlier weaker statement because it appears sooner in the file. The addendum is normative for every conflicting point.

---

# 32. Staged execution plan

Section 27 remains the fine-grained commit sequence. This section groups those commits into dependency-safe stages and incorporates the gap closures above.

A stage is complete only when its exit gate is green.

## Stage 0 — freeze contracts, baselines and red tests

Purpose: establish a clean measurable starting point.

Entry:

- Kikimora baseline 4a49100 or a documented direct descendant;
- Leshy baseline 84e6e9ab or a documented descendant;
- production managed cutover not enabled.

Work:

1. Record both exact SHAs in implementation PR/log.
2. Set relevant CI matrices fail-fast=false.
3. Add .github/managed-routing-companion.env with the exact current Leshy 40-hex SHA and change authoritative cross-repo integration scaffolding to consume that pinned ref; no moving master counts as stage evidence.
4. Add named test skeletons for schema2, mode-journal crashes, writer fence, privileged auth, observation-sequence race, production recovery selector, Windows exact replacement and Linux IPv6 event restore. Skeletons may be explicitly skipped with a message naming their owning stage; do not commit intentionally failing CI. At the start of the owning stage, remove the skip before implementation and make the test drive that stage to green.
5. Record current syscall/RPC duplicate-query counters.
6. Keep v1 runtime behavior unchanged.

Exit: baseline failures are separated from new work; test locations are fixed; no behavior change is hidden in setup.

## Stage 1 — durability, fencing and security foundation

Purpose: close authority holes before schema2 depends on them.

Includes 31.1-31.7, 31.15-31.18 and 31.22-31.23 as plumbing. Production v2 activation remains off.

### Stage 1A — durable filesystem

- fsutil.WriteAtomicJSON;
- fsutil.RemoveDurable;
- native Linux/Windows durability tests;
- migrate current checkpoint/marker writers to common primitive without schema change.

### Stage 1B — process fencing

- core StateDir lock;
- Leshy WriterLease;
- generated writer_lock;
- native lock tests;
- Linux/Windows service dependency contract;
- ServeWithReady/wait-startup readiness barrier contract, with production activation still off.

### Stage 1C — privileged control auth

- key creation/ACL;
- Request auth field;
- privileged middleware;
- Leshy auth-file config plumbing;
- v2 capability constants only, activation still disabled.

### Stage 1D — transition plumbing

- transition types/read/write/recovery tests;
- mode-generation storage;
- no production Enable/Disable switchover yet.

Exit:

- all-OS unit tests;
- desktop nonprivileged calls still work;
- privileged call without auth has zero mutation;
- duplicate core/duplicate Leshy rejected;
- Windows replace-existing durability test;
- package dependency tests.

No-go: no schema2 production restore or v2 activation before this stage.

## Stage 2 — Kikimora exact transaction core on Linux

Fine-grained work:

~~~text
K11
K12 Linux/Darwin/fakes
K13
K14
~~~

Mandatory additions:

- 31.8 lock domain;
- 31.20 deadline-aware API mutation and Freeze;
- 31.24 exact-ownership terminology/trust boundary is reflected in planner comments, diagnostics and foreign-row tests;
- schema2 ActivationID/ModeGeneration fields;
- real ToadGeneration;
- production recoverRole ManagedSelectedRoutes wiring;
- independent selected + Park transactions;
- managed parking only in SelectedRouteManager;
- WithdrawSelected in recovery;
- route authority not-ready until platform/restore prerequisites.

Internal order:

~~~text
2.1 K11 model/planner/schema2
2.2 K12 platform contract + Linux writer
2.3 K13 manager transaction/restore/verification
2.4 K14 Park + WithdrawSelected + reachable recovery
2.5 Linux privileged selected netns to schema2
~~~

Exit:

- all state transition/invalid transition tests;
- unit crash failpoints;
- Linux exact selected netns;
- production managed sequence test;
- no managed legacy parking.Manager calls;
- no schema1 Active adoption;
- race tests clean.

Do not start production cutover.

## Stage 3 — Kikimora Windows writer, observation safety and audit

Fine-grained work:

~~~text
K15
K16
~~~

Stage 3A:

- Windows exact selected/Park writer;
- section 31.19 persisted InterfaceLuid in selected/Park identity;
- section 31.25 InitializeIpForwardEntry creation contract and Windows effective-metric restoration proof;
- section 31.26 Windows managed protocol is RouteProtocolNetMgmt(3), while Linux remains RTPROT_STATIC(4);
- exact Replace;
- persisted loopback Park identity;
- notification registration/cancel;
- SnapshotSelectedRoutes;
- no broad replace.

Stage 3B:

- 31.9 observation Sequence on Linux and Windows;
- stable double-sampled proof;
- pending-event fast-path suppression;
- targeted token clear;
- gap -> Full.

Stage 3C:

- selected reconciler;
- section 31.21 integrity quarantine instead of generic failByConfiguration teardown;
- 31.10 single-flight audit;
- 31.11 startup-ready gate;
- observer restart/backoff;
- integrity diagnostics.

Exit:

- windows-latest direct tests;
- Linux/Windows debounce-race tests;
- one audit = one snapshot;
- 10k-event slow-audit stress;
- Linux v4/v6 event repair under 5s;
- routeAuthorityReady race test.

Cross-compile is not acceptance.

## Stage 4 — Leshy common exact ownership and Linux local safety

Fine-grained work:

~~~text
L10
L11
~~~

Stage 4A:

- RouteKernelIdentity/RouteOwnership;
- section 31.20 bounded local transaction/reconcile liveness where applicable;
- section 31.24 exact-ownership trust-boundary terminology in Leshy local planner/tests;
- ReconcileMode;
- exact cleanup ownership;
- macOS RegistryOnly;
- Leshy observation-sequence safety.

Stage 4B:

- Linux event-coherent cache;
- startup snapshot race closure;
- targeted prefix read;
- IPv4 + IPv6 route groups;
- exact OIF/gateway/protocol/metric/scope;
- no converged request-path full FIB dump.

Stage 4C:

- exact remove_all_owned/shutdown registry primitive for Stage 6.

Exit:

- fmt/clippy/tests;
- privileged netns;
- /128 event repair under 5s;
- 1000 duplicate A/AAAA flat syscall counters;
- pre-existing compatible route survives cleanup;
- pending native event prevents stale registry fast path.

May run in parallel with Stages 2-3 after Stage 1 freezes contracts.

## Stage 5 — Leshy Windows local safety and managed v2 IPC

Fine-grained work:

~~~text
L12
L13
L14
~~~

Stage 5A:

- exact Windows local IP Helper seam;
- InterfaceLuid in Leshy RouteKernelIdentity and exact Windows cleanup;
- every CreateIpForwardEntry2 row uses Init/InitializeIpForwardEntry and notification callbacks remain invalidation-only;
- Windows RouteProtocol uses the Windows enum semantics from 31.26; no Linux protocol literals are shared into this backend;
- NotifyRouteChange2;
- coherent cache;
- no broad delete;
- exact cleanup ownership.

Stage 5B:

- Windows AF_UNIX;
- auth-file loading;
- ActivationID/ModeGeneration requests;
- capabilities af_unix_control, managed_selected_routes_v2, privileged_control_auth_v1.

Stage 5C:

- AuthorityLost;
- zero bulk RPC;
- per-zone invalidation;
- authority sequence;
- targeted reload;
- snapshot misuse error.

Exit:

- native windows-latest tests;
- elevated local Windows route-registry gate;
- real AF_UNIX framing;
- missing/wrong auth fail closed;
- stale ActivationID denied;
- 1000-route AuthorityLost zero bulk RPC;
- 32 concurrent hits one RPC;
- no local fallback.

## Stage 6 — transactional cutover, upgrade and service lifecycle

Fine-grained work:

~~~text
K17
L15 except final tag
31.1/31.2 production activation
31.3 boot ordering
31.12 upgrade path
~~~

Stage 6.1:

- run_server_until;
- cancel retry/reload tasks;
- local exact cleanup;
- Windows SCM graceful stop;
- Linux SIGTERM cleanup;
- final v2 capabilities output;
- Linux wait-startup ExecStartPre and Windows service Running-after-ready behavior.

Stage 6.2:

- EnableManagedLeshy/DisableManagedLeshy;
- writer fence;
- transition journal;
- marker schema2;
- ModeGeneration/ActivationID;
- config digest;
- startup recovery.

Stage 6.3 cutover:

~~~text
capability/auth preflight
-> graceful stop Leshy
-> writer lock acquirable
-> Enable/Disable transaction
-> restart Leshy
-> verify mode generation/activation
~~~

Stage 6.4:

- schema1 drain;
- abort-safe Linux/Windows upgrade;
- backup/resume tests.

Exit:

- crash at every journal boundary recovers;
- reboot at each phase on Linux/Windows;
- zero local/managed writer overlap;
- schema1 upgrade drains or aborts safely;
- routing-backend hot reload rejected;
- zone reload targeted;
- packaged Leshy starts after core mode recovery.

No release tag yet.

## Stage 7 — deterministic integration infrastructure

Includes Ready-Toad, process failpoints, TestTriggerManagedRecovery, recovery barrier, shared DNS fixtures and diagnostic redaction.

Production build contains none of the test trigger/barrier behavior.

Exit:

- fixture tests;
- test trigger reaches real requestManagedSelectedRecovery;
- barrier only after successful step;
- timeout leaves role safe/frozen;
- release binary does not advertise test method;
- bundle redacts auth token.

## Stage 8 — Linux vertical acceptance

Run section 20 and every Linux MUST row of section 28.

Required vertical scenarios:

1. local exact ownership/no-churn;
2. graceful local cleanup;
3. v2 activation through fence/journal;
4. managed A + AAAA;
5. external v4/v6 event repair;
6. pending-event fast-path safety;
7. ParkActive -> WithdrawSelected -> barrier;
8. ifindex change -> exact restore -> ParkRetiring;
9. family readiness independence;
10. AuthorityLost no bulk/no fallback;
11. schema2 core restart/freeze;
12. transition crash/reboot;
13. managed->local rollback;
14. schema1 upgrade drain;
15. foreign rows unchanged;
16. full diagnostics/counters.

Exit: all Linux MUST rows green, no skipped privileged gate, no hidden churn, unrelated network state unchanged.

## Stage 9 — Windows vertical acceptance

Run route-registry.ps1, test_managed_leshy.ps1, transition reboot matrix and schema1 upgrade fixture.

Additionally require:

- SCM dependency core -> Leshy;
- desktop nonprivileged calls still work;
- privileged route auth;
- auth-key ACL;
- LockFileEx writer fence;
- Windows atomic replacement;
- core singleton;
- transition reboot recovery;
- stale ActivationID denial;
- pending-notification race;
- exact old loopback Park removal;
- selected-vs-Park effective metric inequality using Get-NetIPInterface + Get-NetRoute before unpark;
- high selected-interface-metric fixture keeps Park Active/frozen rather than falsely declaring restoration;
- unrelated route snapshot equality.

Exit: every Windows MUST row green and before/after snapshots archived.

## Stage 10 — compatibility pin, packaging and release

Preconditions: Stages 0-9 green, no open P0/P1, non-cancelled green CI.

Exact order:

1. remove temporary v1 activation compatibility in the implementation pair; final activation is v2 only.
2. bump Leshy Cargo.toml to 0.6.0 and update Cargo.lock in the same candidate commit.
3. run the full Leshy all-OS CI on that exact version-bump SHA; do not tag while any leg is red/cancelled.
4. tag exactly that proven SHA as v0.6.0 and verify the tag resolves back to it.
5. update Kikimora companion ref/package refs to v0.6.0 and update installer EXPECTED_VERSION/tests.
6. run Kikimora unit/cross-repo Linux integration against the tag and require all non-cancelled green.
7. run the Windows VM local+managed gates against Leshy built from the same tag.
8. run previous-release -> candidate schema1 upgrade on Linux and Windows.
9. build Linux/Windows Kikimora artifacts.
10. run package contract tests and verify both embedded Leshy binaries report 0.6.0.
11. fresh-install both artifacts and repeat managed activation smoke.
12. record package hashes and exact source SHAs in the release evidence.
13. mark 08D/08D1 COMPLETE.
14. publish.

Release evidence records SHAs, CI runs, privileged runs, VM run, upgrade run, package hashes, Leshy versions and activation fingerprint.

---

# 33. Dependency graph and executor scheduling

~~~text
Stage 0
   |
Stage 1 durability/fence/auth/contracts
   |
   +-------------------------+
   |                         |
Stage 2 Kikimora Linux       Stage 4 Leshy common/Linux
   |                         |
Stage 3 Kikimora Win/audit   Stage 5 Leshy Win/managed-v2
   |                         |
   +------------+------------+
                |
Stage 6 cutover/upgrade/lifecycle
                |
Stage 7 deterministic integration
                |
Stage 8 Linux vertical
                |
Stage 9 Windows vertical
                |
Stage 10 pin/package/release
~~~

Allowed parallelism:

- Stages 2 and 4 after Stage 1.
- Stages 3 and 5 after their respective predecessors.
- Stage 7 scaffolding may begin early, final gate after Stage 6.
- Windows preparation may overlap Linux acceptance, but shares the same frozen architecture.

Forbidden parallelism:

- independent workers changing the same repo ownership model;
- cutover worker changing mode semantics while Stage 1 contracts move;
- release pinning pre-v2 Leshy;
- E2E shell workarounds for missing product behavior except explicit test/foreign fixtures.

Sequential executor follows Stage 0 -> 10.

Parallel executor uses at most:

~~~text
Worker K: Kikimora Stages 2-3
Worker L: Leshy Stages 4-5
Worker T: fixture scaffolding after contract freeze
~~~

Root/integrator owns Stages 1 and 6 because they are cross-repository authority boundaries.

---

# 34. Stage handoff record

At the end of every stage record:

~~~text
Stage:
Kikimora SHA:
Leshy SHA:
Implemented packet sections:
Wire capability/version:
Persistent schema:
ModeGeneration/ActivationID behavior:
Native OS gates run:
Privileged gates run:
Crash failpoints run:
Known skipped gate + reason:
Foreign-row proof:
No-churn counters:
Next-stage entry criteria satisfied: yes/no
~~~

A stage is not done because its commit merged. It is done when its exit gate has evidence.

If a stage finds a new ownership, crash, fencing or authorization ambiguity, stop dependent stages and add the resolved contract to this packet before continuing. Do not leave architecture only in code.

---

# 35. Completion supplement for section 30

The original thirty-six items in section 30 remain mandatory but are no longer sufficient by themselves. 08D1 is COMPLETE only when section 30 and every addendum gate below are both proven.

## 35.1 Additional mandatory completion gates

37. Managed mode changes are journaled and crash-recoverable across every config/marker write boundary.
38. Kikimora-generated Leshy holds the OS writer lease for its lifetime; Enable/Disable cannot run while that lease is held.
39. Packaged Linux and Windows boot ordering prevents Leshy from starting before core mode recovery.
40. Schema2 marker/checkpoints and managed requests are fenced by ActivationID + ModeGeneration.
41. Final production capability is managed_selected_routes_v2 with privileged_control_auth_v1; schema2 activation cannot use v1-only clients.
42. All privileged route/cutover methods require the service credential on both OSes while ordinary desktop read/control surface remains usable as designed.
43. Selected checkpoint, marker, mode generation and transition journal use the shared durable file primitive; Windows replacement is native write-through replace-existing.
44. Every selected/Park registry mutation + checkpoint is serialized through the documented per-role applyMu lock order.
45. Native observation sequence prevents a stale zero-I/O ACK after an event has reached the process but before debounce handling.
46. Periodic audits and observer-loss audits are single-flight, bounded and event queues remain bounded under storm.
47. routeAuthorityReady prevents managed route ACK and managed Role Ready publication before restore/writer/observer/initial proof is complete.
48. Previous schema1 managed installations have a tested drain-to-local upgrade path; ambiguous old same-prefix rows abort upgrade.
49. Test-only recovery trigger/barrier uses the real production recovery entry and is absent from release builds.
50. One persistent StateDir is protected by a core lifetime lock; a second core cannot share the same authority.
51. runServe performs transition/marker/checkpoint recovery before any generic Leshy config regeneration and Windows package supplies an explicit persistent StateDir.
52. Authoritative cross-repository integration uses a pinned Leshy companion SHA/tag, never a moving master/windows-support branch.
53. Windows exact selected/Park identity includes InterfaceLuid as well as InterfaceIndex.
54. Route mutations and Freeze are deadline-aware; one stuck platform call cannot leave recovery/cutover waiting forever without a bounded error.
55. Foreign-route integrity conflicts enter the dedicated quarantine behavior and never trigger generic destructive cleanup.
56. Leshy v0.6.0 tag points at the exact post-version-bump SHA whose full CI passed; Kikimora Linux and Windows packages consume that same tag.
57. Windows CreateIpForwardEntry2 rows are initialized through InitializeIpForwardEntry; NotifyRouteChange2 callback rows are never treated as complete ownership proof.
58. Windows Park removal is allowed only after exact selected/Park presence plus effective route+interface metric proof that selected wins.
59. Packaged service readiness is application-level: Linux wait-startup and Windows SERVICE_RUNNING occur only after committed mode/startup gates are ready.
60. Managed route protocol is platform-native: Linux selected/Park uses RTPROT_STATIC(4), Windows uses RouteProtocolNetMgmt(3); schema2 checkpoint Platform prevents cross-OS reinterpretation.
61. Mode transition journal persists exact From/To ActivationID and From/To Generation; committed generation can only match From or To during recovery, otherwise startup fails closed.
62. Exact-ownership claims use the documented privileged-host trust boundary: byte-identical privileged replacement is not falsely claimed as distinguishable foreign provenance.
63. Transition config bytes/zones/ID/digest are deterministic per section 31.23 and recovery validates the final-path digest before commit.

## 35.2 Supplemental regression matrix

| Scenario | Linux | Windows | Required evidence |
|---|---:|---:|---|
| crash after each Local->Managed journal boundary | MUST | MUST | restart converges managed, no writer overlap |
| crash after each Managed->Local journal boundary | MUST | MUST | restart converges local, no stale marker writer |
| live Leshy blocks Enable/Disable writer fence | MUST | MUST | files unchanged, explicit error |
| second Leshy instance same writer lock | MUST | MUST | second process exits before DNS/routes |
| second core same StateDir | MUST | MUST | second process exits before socket/route mutation |
| corrupt transition journal blocks Leshy service start | MUST | MUST | application-readiness barrier prevents Leshy ExecStart/SCM start |
| stale ActivationID after re-enable | MUST | MUST | denied, zero mutation |
| wrong/missing privileged auth | MUST | MUST | denied before intent parse/mutation |
| nonprivileged UI Handshake/Snapshot remains usable | N/A by token | MUST | Windows interactive user acceptance |
| atomic durable replacement of existing JSON | MUST | MUST | old-or-new complete file only |
| event arrives during 100ms debounce | MUST | MUST | no stale fast ACK |
| event arrives during exact read-back | MUST | MUST | double-sample retry/no stale token |
| slow audit spans multiple ticks | MUST | MUST | one snapshot, skipped-busy counter |
| 10,000 event storm | MUST | MUST | bounded queue, eventual convergence |
| Ensure immediately after core socket creation | MUST | MUST | route_authority_initializing/no mutation |
| schema1 nonempty managed upgrade | MUST | MUST | drain-to-local or abort untouched |
| schema1 ambiguous same-prefix row | MUST | MUST | upgrade aborts; no adoption/delete |
| Windows same ifindex/different LUID | N/A | MUST | conflict/no delete |
| Windows old LUID after adapter recreate | N/A | MUST | exact old cleanup only after new proof |
| Windows create row without InitializeIpForwardEntry | N/A | MUST | test fails before Create |
| Windows callback row is incomplete | N/A | MUST | invalidation only; no ownership projection |
| Windows selected effective metric >= Park effective metric | N/A | MUST | Park remains Active, role frozen |
| Windows interface metric changes after restoration proof | N/A | MUST | proof invalidated before unpark |
| Windows protocol 4 ICMP-lookalike same-prefix row | N/A | MUST | treated foreign and preserved; desired uses NetMgmt(3) |
| copied schema2 checkpoint from other OS | MUST | MUST | Platform mismatch fails before mutation |
| blocked platform call + Freeze timeout | MUST | MUST | role remains frozen; retry later succeeds |
| foreign conflict quarantine + Retry | MUST | MUST | no auto teardown; recovery only after external conflict removal |
| pinned companion revision | MUST | MUST | CI log + evidence contains exact SHA |
| final package Leshy source identity | MUST | MUST | v0.6.0 resolves to same tested SHA |

## 35.3 Final executor stop rule

If any section-35 gate is missing, skipped without an explicit release waiver, or proven only by a cross-build where runtime proof is required:

> **do not mark 08D/08D1 COMPLETE, do not enable package-default managed cutover, and do not publish the release.**

A release waiver is not an executor decision. It requires an explicit project-owner decision recorded outside the implementation commit with the exact skipped gate and risk.
