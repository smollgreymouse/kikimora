# Toad step 08D — Leshy managed selected routes and dual-stack single-writer integration

Status: **IMPLEMENTATION PLAN / BLOCKED UNTIL LESHY LOCAL REGISTRY IS GREEN**.

Reviewed Kikimora baseline: `3d2981e4d9f32693ce4f369a98812621008613b6` on `master`, 2026-11-03.

Upstream prerequisite: Leshy `docs/linux-unified-route-reconciliation-plan.md`, through its Leshy-only commits 1–8. The optional Leshy managed backend may be developed against a fake API, but must not be enabled in generated production configuration before this packet is complete.

This packet deliberately supersedes the earlier idea of keeping Leshy and Kikimora as concurrent writers coordinated only by generation/ACK. In managed mode **Kikimora is the sole writer for Leshy-selected routes and fail-closed parks**. Leshy remains the DNS classifier and route-intent producer.

---

## 0. Final ownership model

### Standalone Leshy

~~~text
DNS
  -> Leshy zone/family classifier
  -> Leshy route registry
  -> Leshy local platform backend
  -> kernel FIB
~~~

### Kikimora-managed Leshy

~~~text
DNS
  -> Leshy zone/family classifier
  -> Leshy route registry
  -> KikimoraRouteBackend
  -> existing Kikimora local control socket
  -> Kikimora SelectedRouteManager
  -> Linux routing.Executor / platform.RouteManager
  -> kernel FIB
~~~

Kikimora also owns:

- VPN transport lifecycle;
- endpoint underlay/table 51890;
- selected-route ownership checkpoint;
- fail-closed parking;
- selected-route restoration;
- family readiness authority.

Hard invariant:

> In managed mode, no Leshy code path calls the local Linux/macOS/Windows route writer for a managed selected/static route.

There is therefore no cross-process kernel-write race to solve with a two-writer lease protocol.

---

# 1. What changes from the current implementation

Current relevant code:

- `internal/leshy/bridge.go` publishes only a `<zone>.dev` interface-name file;
- `internal/control/recovery_driver.go` discovers Leshy ownership through baseline + kernel delta;
- `internal/routing/ownership.go` stores only role/interface/ifindex/prefix;
- `internal/parking/manager.go::PrepareOwnedWithdrawal` re-verifies discovered host routes;
- `internal/core/recovery_runner.go` runs `ObserveRoutes -> Park -> Withdraw -> ... -> Publish -> ResyncLeshy -> ObserveRestoration`;
- `internal/platform/linux/netlink/routes.go::applyOperation` uses `RouteReplace` for normal route operations;
- `toadctl.Snapshot.RouteReady` and `ValidationResult.Healthy` are global booleans, while the product contract requires address-family independence.

The current `ObserveRoutes -> Park -> Withdraw` order is safe only as a best-effort legacy bridge. There is a race: after the ownership snapshot/park set is built and before publication is withdrawn, Leshy may classify a new DNS address and add one more route that is not in the park inventory.

08D removes that race by making the route registration and route write one Kikimora transaction.

---

# 2. API contract: reuse the existing local control socket

Do **not** create a second daemon/socket.

Entry point: `toad/internal/control/api.go`.

The existing framed JSON control socket already has:

- local peer authorization;
- request/response framing;
- version negotiation;
- subscriptions;
- Manager dispatch.

Extend it to API version **3** while keeping versions 1 and 2 supported.

## 2.1 Request/response types

Add to `internal/control/api.go` or a new `internal/control/leshy_routes_api.go`:

~~~go
type LeshyRouteSource string

const (
    LeshyRouteDNS    LeshyRouteSource = "dns"
    LeshyRouteStatic LeshyRouteSource = "static"
)

type LeshyRouteIntent struct {
    Zone   string           `json:"zone"`
    Prefix string           `json:"prefix"`
    Source LeshyRouteSource `json:"source"`
}

type LeshyRouteResult struct {
    Role       string `json:"role"`
    Zone       string `json:"zone"`
    Prefix     string `json:"prefix"`
    Family     int    `json:"family"` // 4 or 6
    Generation uint64 `json:"generation"`
    State      string `json:"state"`  // applied|already-applied|denied|conflict
}
~~~

Extend:

~~~go
type Request struct {
    ...
    LeshyRoute *LeshyRouteIntent `json:"leshy_route,omitempty"`
}

type Response struct {
    ...
    LeshyRoute *LeshyRouteResult `json:"leshy_route,omitempty"`
}
~~~

New API methods:

~~~text
EnsureLeshyRoute
ForgetLeshyRoute
FlushLeshyZone
~~~

Handshake capability string:

~~~text
managed_selected_routes_v1
~~~

API v1/v2 must reject these methods as unsupported. API v3 may use all old methods unchanged.

## 2.2 Trust boundary

The Leshy request may specify only:

- configured zone;
- canonical prefix;
- source kind.

It must **not** specify role, interface, ifindex, gateway, route table, metric, protocol or parking state.

Kikimora derives those from authoritative role/config/runtime state.

Validation:

- zone must map to exactly one configured role;
- prefix must parse and be canonical;
- DNS source requires host prefix (/32 or /128);
- static source may be a non-host CIDR;
- mapped IPv6 is rejected;
- source family must be Ready for the role;
- role must be Desired and product-Ready/current-validation before a new route is accepted.

Unknown/ambiguous zone: hard error, no route mutation.

---

# 3. Per-family readiness becomes explicit product state

The current one-bit `RouteReady` is retained only for compatibility. It must stop being the authority for A-vs-AAAA publication.

## 3.1 Protocol types

Entry point: `toad/internal/toadctl/protocol.go`.

Add:

~~~go
type FamilyReadiness struct {
    Configured bool   `json:"configured"`
    Ready      bool   `json:"ready"`
    Reason     string `json:"reason,omitempty"`
}

type RouteFamilies struct {
    IPv4 FamilyReadiness `json:"ipv4"`
    IPv6 FamilyReadiness `json:"ipv6"`
}
~~~

Add `Families RouteFamilies` to:

- `toadctl.Snapshot`;
- `toadctl.ValidationResult`;
- the user-facing role snapshot in `internal/control/control.go`;
- `internal/core.RoleRuntime` through its embedded Toad/Validation state;
- `internal/leshy.PublicationState` (section 3.4).

Compatibility `RouteReady` semantics after migration:

~~~text
RouteReady = Families.IPv4.Ready || Families.IPv6.Ready
~~~

It means “at least one usable route-target family exists”, not “both families are usable”.

No caller may infer IPv6 readiness from `RouteReady=true`.

## 3.2 Structural family calculation

Entry point: `toad/internal/toadruntime/runtime.go`.

Split current `interfaceStructurallyReady` into:

~~~go
func interfaceIdentityReady(cfg *config.Config, iface Interface) bool
func interfaceFamilyReadiness(cfg *config.Config, iface Interface) toadctl.RouteFamilies
~~~

Rules:

### AWG2 / Xray

`cfg.Address` is authoritative configuration.

- any configured IPv4 prefix -> IPv4.Configured=true;
- any configured IPv6 prefix -> IPv6.Configured=true;
- a family is Ready only when all configured prefixes for that family are observed on the current ifindex;
- missing one family does not make the other family Ready by implication.

### OpenConnect

`LocalInterfaceExpectation` already parses negotiated `ipv4_address` and `ipv6_address`.

- IPv4.Configured becomes true when a negotiated IPv4 address exists;
- IPv6.Configured becomes true when a negotiated IPv6 address exists;
- `disable_ipv6=true` forces IPv6.Configured=false/Ready=false;
- absence of negotiated IPv6 on a server that did not negotiate it is not an error and must not create an infinite recovery loop;
- once a family has been validated/published in the current Toad generation, losing that negotiated family is material drift and invalidates validation.

Persist the “published in this generation” expectation in controller product state, not in the backend.

## 3.3 Validation family calculation

Entry points:

- `toad/internal/toadruntime/runtime.go::Validate`;
- `toad/internal/backend/backend.go::Validation`;
- backend validators in `internal/backend/awg2`, `xray`, `openconnect`.

Backend health is transport-wide. Family readiness is route-target-specific.

`Runtime.Validate`:

1. computes structural `RouteFamilies`;
2. runs backend validator;
3. if backend is unhealthy, every configured family becomes Ready=false with backend reason;
4. otherwise structurally ready families remain Ready=true;
5. returns `ValidationResult.Families`.

Overall `ValidationResult.Healthy`:

~~~text
true when backend validation is healthy and at least one configured family is Ready
~~~

This permits a legitimate IPv4-only role.

If a family that was previously published Ready in the same Toad generation disappears, controller invalidates the current validation and enters the existing recovery path. Initial lack of optional IPv6 on OpenConnect does not count as loss.

## 3.4 Publication state

Entry point: `toad/internal/leshy/publication.go`.

Extend:

~~~go
type PublicationState struct {
    Published  bool          `json:"published"`
    Zone       string        `json:"zone,omitempty"`
    Interface  string        `json:"interface,omitempty"`
    Generation uint64        `json:"generation,omitempty"`
    Families   RouteFamilies `json:"families"`
}
~~~

Use a small Leshy-owned family type or reuse `toadctl.RouteFamilies`; do not introduce two semantically different family models.

`Generation` is the authoritative selected-route generation exposed to Leshy. It changes when:

- Toad generation changes;
- selected interface ifindex changes;
- validated family readiness changes;
- role is frozen/withdrawn and later becomes Ready again.

---

# 4. New selected-route registry in Kikimora

Do not extend baseline/delta discovery into the new path.

Add `toad/internal/routing/selected_registry.go`.

## 4.1 Model

~~~go
type SelectedRouteSource string
const (
    SelectedDNS    SelectedRouteSource = "dns"
    SelectedStatic SelectedRouteSource = "static"
)

type SelectedRouteState string
const (
    SelectedPending SelectedRouteState = "pending"
    SelectedActive  SelectedRouteState = "active"
    SelectedParked  SelectedRouteState = "parked"
)

type SelectedRoute struct {
    Role           string
    Zone           string
    Prefix         netip.Prefix
    Source         SelectedRouteSource
    ToadGeneration uint64
    RouteGeneration uint64
    Interface      string
    IfIndex        int
    State          SelectedRouteState
}
~~~

Registry key:

~~~text
(role, canonical prefix)
~~~

The configured zone is stored and verified but is not the sole key.

Registry API:

~~~go
type SelectedRouteRegistry interface {
    Reserve(role, zone string, prefix netip.Prefix, source SelectedRouteSource,
            toadGeneration, routeGeneration uint64, iface string, ifindex int) error
    MarkActive(role string, prefix netip.Prefix) error
    MarkParked(role string, prefix netip.Prefix) error
    Snapshot(role string) []SelectedRoute
    SnapshotFamily(role string, family int) []SelectedRoute
    Remove(role string, prefix netip.Prefix)
    RemoveRole(role string)
}
~~~

Production implementation uses a mutex-protected map plus the existing role state-dir checkpoint.

## 4.2 Checkpoint format

Current `parking.json` contains `Baseline`, `Observed`, `Parked`.

Add a versioned checkpoint, preferably:

~~~text
<role StateDir>/selected-routes.json
~~~

Schema 1 contains:

- role;
- zone;
- route generation;
- Toad generation;
- selected entries including source/prefix/ifindex/state;
- parked prefixes.

Write atomically + fsync directory using existing fs utilities.

Crash invariant:

- `Reserve` is checkpointed **before** the kernel selected-route mutation;
- `MarkActive` is checkpointed after exact kernel verification;
- a crash after Reserve but before add leaves a Pending entry, which is safe and reconciled later;
- a crash after kernel add but before MarkActive still has a reserved ownership record, so recovery can park it.

This replaces the dangerous “route appeared before observer noticed it” window.

---

# 5. SelectedRouteManager: one transaction boundary

Add `toad/internal/control/selected_routes.go` (or a focused package injected into Manager).

It owns:

- registry;
- routing executor/platform route manager;
- per-role freeze state;
- per-role route generation;
- in-flight transaction counter/condition.

Required methods:

~~~go
func (m *SelectedRouteManager) Ensure(ctx context.Context, intent LeshyRouteIntent) (LeshyRouteResult, error)
func (m *SelectedRouteManager) Forget(ctx context.Context, intent LeshyRouteIntent) error
func (m *SelectedRouteManager) FlushZone(ctx context.Context, zone string) error
func (m *SelectedRouteManager) Freeze(role string) ([]routing.SelectedRoute, uint64, error)
func (m *SelectedRouteManager) Restore(ctx context.Context, role string, families toadctl.RouteFamilies) error
func (m *SelectedRouteManager) Unfreeze(role string) uint64
~~~

## 5.1 Ensure ordering

`EnsureLeshyRoute` exact order:

1. map zone -> role using configured `EffectiveEndpointPolicy().Zone`; require uniqueness;
2. canonicalize/validate prefix and source;
3. acquire role selected-route lock;
4. require not frozen;
5. snapshot authoritative product role;
6. require current role desired, Ready/current validation and requested family Ready;
7. derive interface name/ifindex from current Toad snapshot;
8. increment in-flight count;
9. `Reserve` + checkpoint ownership;
10. reconcile exact route in kernel;
11. exact read-back verification;
12. `MarkActive` + checkpoint;
13. decrement in-flight;
14. return ACK with current route generation.

DNS answer is allowed only after step 14.

If any step after Reserve fails, keep Pending ownership and return failure; do not erase evidence needed for fail-closed recovery.

## 5.2 Capacity

Kikimora independently enforces a selected-route capacity even if Leshy already does.

Initial limit: 16384 DNS host intents per role. Static entries are separate.

At capacity:

- reject new DNS prefix;
- keep existing prefixes;
- never evict an existing route automatically;
- response is safety denial, so Leshy returns SERVFAIL.

This prevents a compromised/buggy Leshy from bypassing its own bound.

---

# 6. Platform route reconciliation: never overwrite a foreign route

Entry points:

- `toad/internal/platform/contracts.go`;
- `toad/internal/platform/linux/netlink/routes.go`;
- `toad/internal/routing/transaction.go`.

Extend `platform.RouteManager`:

~~~go
type SelectedRouteDesired struct {
    Role     string
    Prefix   netip.Prefix
    IfIndex  int
    Gateway  netip.Addr
    Protocol int
    Metric   uint32
}

type RouteManager interface {
    ...
    ReconcileSelectedRoute(context.Context, SelectedRouteDesired, *SelectedRoute) error
    RemoveSelectedRoute(context.Context, SelectedRoute) error
}
~~~

Linux implementation runs under existing `kernelMu`.

## 6.1 ReconcileSelectedRoute algorithm

1. snapshot same-family main-table rows;
2. find exact prefix rows;
3. if exact desired row already exists -> success/no mutation;
4. if an old row matches the registry's previous owned identity -> `RouteReplace` to the new expected target is allowed;
5. if a same-prefix row exists but is not provably the previous owned row -> return conflict, no mutation;
6. otherwise add desired row;
7. snapshot/read back exact prefix and verify:
   - family;
   - canonical prefix;
   - table 254;
   - expected ifindex/gateway;
   - protocol;
   - metric;
8. success only after exact verification.

Do not reuse generic `applyOperation` blindly for this path, because its normal `RouteReplace` can overwrite a foreign row.

Keep endpoint table 51890 entirely separate.

## 6.2 Protocol

Use the existing selected-route protocol value during migration unless a dedicated protocol marker is introduced in a separate compatibility step. Ownership comes from the explicit registry/checkpoint, not from “all proto static routes belong to us”.

---

# 7. Parking changes: explicit ownership allows non-host static prefixes

Entry point: `toad/internal/parking/manager.go`.

Current host-prefix restriction exists because kernel discovery cannot safely claim arbitrary CIDRs.

For the new explicit registry path, add:

~~~go
func (m *Manager) PrepareRegisteredWithdrawal(
    ctx context.Context,
    role string,
    owned []routing.SelectedRoute,
) error
~~~

Rules:

- every prefix came from explicit Reserve/ACK ownership;
- DNS entries are /32 or /128;
- static entries may be wider CIDRs;
- verify current exact owned row before parking when it still exists;
- install same-prefix unreachable route with metric 42760;
- keep real lower-metric selected route until withdrawal/restart removes it;
- checkpoint Parked state.

Keep legacy `PrepareOwnedWithdrawal` host-only until baseline/delta migration is removed.

## 7.1 Exact restoration proof

Fix `ObserveRestorationFromKernel`.

Current test “any lower-metric real row for the prefix” is insufficient.

A park may be removed only if registry says the expected selected route is Active and kernel read-back matches:

- exact prefix/family;
- table 254;
- expected current ifindex or gateway;
- expected protocol;
- metric wins over 42760.

A route on the physical interface or another VPN must never release a park.

---

# 8. Recovery ordering after single-writer cutover

The existing legacy order is not the final managed order.

Entry points:

- `toad/internal/core/recovery.go`;
- `toad/internal/core/recovery_runner.go`;
- `toad/internal/core/engine.go`;
- `toad/internal/control/recovery_driver.go`.

Add steps:

~~~go
RecoveryFreezeSelected
RecoveryRestoreSelected
RecoveryUnfreezeSelected
~~~

Managed sequence:

~~~text
FreezeSelected
  -> waits for in-flight Ensure transactions
  -> new Ensure calls are denied
  -> returns stable explicit ownership set

Park
  -> install/verify unreachable protection for registered selected routes

Withdraw
  -> remove legacy .dev publication if still emitted during migration

Quiesce
ApplyEndpoint
Rebind OR StartTransport
Validate
  -> authoritative per-family result

RestoreSelected
  -> restore registry routes for Ready families to current ifindex
  -> parks still exist

ObserveRestoration
  -> exact read-back
  -> remove only parks whose real expected route wins

Publish
  -> update publication/family/generation state

UnfreezeSelected
  -> accept new Ensure requests under a new route generation
~~~

Key invariant:

> There is never a point where a registered destination has neither a valid selected route nor a verified park while an unintended default path exists.

## 8.1 ActionValidate

A plain validation that does not mutate transport does not need to park stable routes.

However, when validation was invalidated because selected interface identity or a previously-published family disappeared, it is not “plain”. The caller must choose the normal recovery path that includes Freeze/Park.

Encode this decision in the controller; do not let `ActionValidate` guess from string reasons.

## 8.2 Full Toad restart

Before stopping/replacing the Toad:

- Freeze completed;
- all registered prefixes are parked;
- checkpoint is fsynced.

A replacement Toad may reuse the interface name but has a different ifindex. Restore uses the new ifindex and increments route generation.

---

# 9. Remove baseline/delta ownership after managed cutover

Entry point: `toad/internal/control/recovery_driver.go`.

Legacy functions:

- `captureOwnershipBaseline`;
- `captureOwnedDelta`;
- `selectedRouteCandidates`;
- `baselines`;
- `ownershipLoaded`;
- old `routing.MemoryOwnership`.

Migration steps:

1. keep them while `leshy_route_backend=legacy-file`;
2. add new explicit registry path for `managed-core`;
3. run parity tests;
4. switch generated Linux config to managed-core;
5. after one release with compatibility gate, delete baseline/delta from the managed code path;
6. retain legacy reader only if rollback requires it.

Do not run explicit registry and baseline discovery as two sources of truth for the same managed zone.

---

# 10. Leshy bridge after cutover

Entry point: `toad/internal/leshy/bridge.go`.

The `.dev` bridge becomes compatibility publication, not route-write authority.

Modes:

~~~text
legacy-file:
  current <zone>.dev behavior
  Leshy writes selected routes itself

managed-core:
  Leshy uses KikimoraRouteBackend
  core API is selected-route writer authority
  .dev file may be emitted temporarily for diagnostics/rollback but Leshy managed backend must not consume it as permission to write FIB
~~~

Add explicit mode to generated Leshy configuration rather than auto-detecting from file presence.

Never silently fall back from `managed-core` to `legacy-file` if the core socket is unavailable. That would recreate the dual-writer/leak race.

---

# 11. Kikimora configuration generation

Find the current Leshy config generation path (routing lists / generated zones) and add server fields:

~~~toml
routing_backend = "kikimora"
kikimora_socket = "/run/kikimora/core.sock"
~~~

Activation gate:

- installed Leshy version advertises managed backend support;
- Kikimora API Handshake reports `managed_selected_routes_v1`;
- generated config validates;
- isolated tests pass.

If any gate fails, stay entirely in legacy-file mode. Never configure half-managed zones.

For a managed role, the zone remains one logical zone. IPv4/IPv6 policy comes from per-family authority; there are not separate `primary-v4` and `primary-v6` zones.

---

# 12. One DNS name / A + AAAA end-to-end contract

For a single zone `primary`:

~~~text
example.com A
  -> Leshy primary
  -> 203.0.113.10/32
  -> EnsureLeshyRoute(primary, v4)
  -> Kikimora checks primary IPv4 Ready
  -> route via current primary Toad
  -> ACK
  -> DNS A returned

example.com AAAA
  -> Leshy primary
  -> 2001:db8::10/128
  -> EnsureLeshyRoute(primary, v6)
  -> Kikimora checks primary IPv6 Ready
  -> route via current primary Toad
  -> ACK
  -> DNS AAAA returned
~~~

If IPv4 Ready and IPv6 not Ready:

- A can succeed;
- AAAA is suppressed/denied;
- no IPv6 selected route is installed;
- previously registered IPv6 destinations remain parked while unsafe;
- IPv4 readiness does not authorize IPv6.

No separate “choose IP version” switch exists.

---

# 13. Forget / zone removal semantics

`ForgetLeshyRoute` is explicit configuration/ownership retirement, **not DNS TTL expiry**.

DNS dynamic entries are not forgotten merely because a cache record expired.

Use Forget/Flush only for:

- explicit zone deletion;
- route target/role reassignment;
- static route removal;
- explicit operator cleanup supported by product semantics.

For a role still managed but a family deliberately disabled:

1. freeze affected family/role;
2. park current registered family routes;
3. suppress/deny future family answers;
4. only remove functional routes while park protection exists;
5. keep/delete parks according to the explicit family-disable product policy. Do not convert “disable IPv6 to stop leaks” into “remove /128 and let default IPv6 leak”.

The initial 08D implementation may keep disabled-family parks until role disconnect/profile change explicitly releases ownership. This is safer than automatic expiry.

---

# 14. Exact code-change sequence

The executor must implement in this order.

## K1 — protocol/data types only

Files:
- `internal/toadctl/protocol.go`;
- `internal/leshy/publication.go`;
- role snapshot types/tests.

Add RouteFamilies and API v3 structs. No routing behavior change.

Tests:
- JSON backward compatibility;
- v1/v2 still decode;
- v3 round-trip;
- old snapshots without Families yield zero/not-ready states, never inferred IPv6.

## K2 — family readiness in Toad runtime

Files:
- `internal/toadruntime/runtime.go`;
- `internal/backend/backend.go`;
- AWG2/Xray/OpenConnect backend tests.

Tests:
- v4-only;
- v6-only where configuration allows;
- dual-stack;
- one configured family missing;
- OpenConnect disable_ipv6;
- OpenConnect server negotiates only v4 without infinite unhealthy state;
- loss of previously published v6 invalidates family readiness.

## K3 — explicit selected registry + checkpoint

Files:
- add `internal/routing/selected_registry.go`;
- add checkpoint helper/tests;
- no API mutation yet.

Tests:
- Reserve-before-active crash state;
- checkpoint restore;
- duplicate reserve idempotent;
- ifindex/generation change;
- v4/v6 keys;
- static /24 and /64 ownership.

## K4 — Linux selected-route reconciler

Files:
- `internal/platform/contracts.go`;
- `internal/platform/linux/netlink/routes.go`;
- fake platform for unit tests.

Tests:
- exact already-present = zero mutation;
- missing route = one add;
- old owned ifindex -> safe replace;
- foreign same-prefix -> conflict/no replace;
- v4/v6;
- exact protocol/metric verification;
- table 51890 untouched.

Privileged netns test for real kernel behavior.

## K5 — SelectedRouteManager + API Ensure

Files:
- add `internal/control/selected_routes.go`;
- `internal/control/api.go`;
- Manager construction/wiring.

Tests:
- zone -> unique role;
- request cannot select arbitrary interface;
- Ready v4 accept /32;
- non-Ready v6 deny /128;
- reserve checkpoint precedes kernel apply (failure injection);
- successful ACK only after read-back;
- 16384 capacity boundary;
- concurrent duplicate Ensure -> one mutation;
- freeze blocks/waits for in-flight operations.

## K6 — registered parking and exact unpark proof

Files:
- `internal/parking/manager.go`;
- tests/netns parking tests.

Tests:
- explicit /32, /128, /24, /64 park;
- foreign/unregistered prefix not parkable;
- wrong-interface lower-metric route does not release;
- correct route releases;
- partial Apply failure remains fail closed;
- restart checkpoint rebuild.

## K7 — recovery sequence integration

Files:
- `internal/core/recovery.go`;
- `internal/core/recovery_runner.go`;
- `internal/core/engine.go`;
- `internal/control/recovery_driver.go`.

Tests:
- exact step order;
- Ensure racing with Freeze;
- route registered immediately before recovery is included in parks;
- new Ensure after Freeze denied;
- new ifindex restoration;
- old generation cannot become Active;
- parks remain until exact selected route restored;
- Unfreeze happens last.

## K8 — Leshy managed backend activation

Files:
- Kikimora Leshy config generator;
- installer/service permissions if required;
- Leshy managed backend from its plan commit 9.

Tests:
- API handshake/capability;
- core socket permissions for Leshy service;
- no local Leshy route write in managed mode;
- core unavailable -> SERVFAIL/deny, never legacy local fallback;
- legacy-file mode unchanged.

## K9 — remove managed baseline/delta discovery

Files:
- `internal/control/recovery_driver.go`;
- `internal/routing/ownership.go`;
- checkpoint migration code.

Only after parity/e2e tests are green.

## K10 — diagnostics and soak

Expose per role/family:

- registered selected routes;
- active selected routes;
- parked selected routes;
- route generation;
- Ensure requests / duplicate hits;
- kernel adds/replaces/deletes;
- conflicts/denials/capacity;
- restore count;
- family readiness.

Do not log every successful duplicate Ensure at info level.

---

# 15. Required deterministic tests

At minimum:

1. One qname with A + AAAA produces two independent registered prefixes under one zone.
2. IPv4 Ready does not authorize IPv6.
3. IPv6 Ready does not authorize IPv4.
4. Duplicate Ensure x1000 causes one kernel mutation.
5. 100 concurrent duplicate Ensure calls coalesce.
6. Freeze waits for an in-flight Reserve/apply transaction.
7. No Ensure can commit Active after Freeze begins.
8. A prefix accepted immediately before Freeze is in the parking set.
9. Toad ifindex reuse/name reuse cannot validate an old route.
10. Foreign same-prefix route is not replaced.
11. Wrong VPN/physical route cannot release a park.
12. Crash after Reserve before kernel add restores Pending ownership safely.
13. Crash after kernel add before MarkActive still parks from reserved ownership.
14. Static non-host prefixes use explicit ownership and can park.
15. Route capacity fails closed; no LRU eviction.
16. Endpoint table 51890 is unchanged by selected-route operations.
17. Legacy API versions and legacy-file mode remain green before cutover.

---

# 16. Required isolated Linux acceptance

Run only in disposable netns/VM, not the workstation.

Scenario:

~~~text
physical default v4 + v6
primary fake TUN v4 + v6
secondary fake TUN v4 + v6
Leshy managed-core mode
~~~

Verify:

1. A route -> primary /32, AAAA -> primary /128.
2. Repeat DNS 10k times -> zero new route mutations after convergence.
3. Withdraw primary:
   - Freeze completed;
   - /32 and /128 parks exist;
   - physical/secondary defaults remain;
   - route-get for selected destinations cannot escape.
4. Create a DNS classification concurrently with withdrawal:
   - either Ensure commits before Freeze and is parked;
   - or it is denied after Freeze;
   - never an unparked orphan.
5. Restart primary with same ifname/new ifindex:
   - restore routes to new ifindex;
   - verify exact rows;
   - parks removed only afterward.
6. Make IPv6 unavailable while IPv4 remains:
   - new A continues according to role policy;
   - new AAAA denied;
   - known IPv6 destinations fail closed, not physical;
   - IPv4 cannot be used as proof of IPv6 readiness.
7. Core process crash/restart:
   - selected-routes checkpoint recovered;
   - parks/ownership reconstructed;
   - no stale old-generation route accepted.
8. Leshy process crash/restart:
   - core registry remains authoritative;
   - replayed DNS intents are idempotent.
9. Core socket outage:
   - managed Leshy never switches to local FIB writer.

---

# 17. CI / acceptance commands

Non-privileged:

~~~bash
cd toad
go test ./internal/toadctl ./internal/toadruntime ./internal/routing ./internal/parking ./internal/control ./internal/core
go test -race ./internal/routing ./internal/parking ./internal/control ./internal/core
go test ./...
~~~

Privileged disposable environment:

~~~bash
sudo bash linux/tests/toad/selected-routes-netns.sh
sudo bash linux/tests/toad/route-parking-netns.sh
./linux/tests/toad/run-isolated.sh multi-toad
~~~

Then run Leshy tests from its implementation packet.

---

# 18. Cutover order on a real installation

Do not perform this automatically during development.

After isolated acceptance:

1. install new Kikimora core with API v3 but keep `legacy-file`;
2. install new Leshy with managed backend support but keep local backend;
3. verify both capability handshakes;
4. generate managed-core Leshy config;
5. restart Leshy only;
6. verify it reports managed backend and performs no local selected-route writes;
7. connect roles and confirm selected-route registry is populated in Kikimora;
8. verify A/AAAA family routing;
9. run controlled recovery;
10. only after observation retire baseline/delta ownership path for this installation.

Rollback:

- switch configuration back to legacy-file/local as one explicit transaction;
- never run both writers concurrently;
- clear/reconcile selected-route registry before enabling legacy writer.

---

# 19. Definition of done

08D is complete only when:

- one logical zone supports A + AAAA with independent family authority;
- Kikimora is the only writer for managed selected/static routes;
- Leshy cannot fall back to local writes in managed mode;
- ownership is registered before kernel apply and before DNS ACK;
- recovery Freeze closes the old ObserveRoutes/Park/Withdraw race;
- explicit registry, not kernel discovery, drives parking;
- exact target identity drives unpark;
- IPv4 readiness never authorizes IPv6;
- v4/v6 selected routes and parks pass netns tests;
- steady replay converges to zero mutations;
- capacity is bounded/fail-closed;
- core/Leshy crash recovery is covered;
- legacy mode remains an explicit rollback mode, never an automatic fallback.

---

# 20. Non-goals

- Moving VPN parking into Leshy.
- Per-family duplicate zone names.
- DNS-TTL route garbage collection.
- IPv6 CIDR aggregation.
- Automatic dual-writer fallback.
- Treating interface name alone as route identity.
- Claiming family readiness solely because the interface is UP.
