# Toad step 08D1 — managed selected-route audit remediation and completion plan

Status: **REMEDIATION / COMPLETION PLAN**.

Audit date: 2026-11-05.

Reviewed heads:

- Kikimora `master`: `1cd80cf`;
- Leshy `master`: `84e6e9ab`.

This packet follows 08D after its K1-K10 implementation landed. It records the concrete gaps found by the post-implementation audit and defines the remaining work required before managed selected routes can be called complete on Linux and Windows.

The old feature branches are not an implementation source: the current `master` branches contain the newest 08D and route-registry work. Completion must happen on top of the current masters.

---

## 0. Completion criteria

08D is complete only when all of the following are true:

1. Kikimora is the only FIB writer for managed Leshy selected/static routes.
2. A restart of `kikimora-core` restores selected-route ownership from the persisted checkpoint before managed mode is re-armed.
3. A selected route removed or changed behind Kikimora's back is detected and reconciled; an `Active` registry row can never be trusted forever without kernel proof.
4. A foreign same-prefix route is never replaced or deleted on Linux or Windows.
5. Windows managed mode works end-to-end: Leshy can reach the Windows Kikimora control socket and Kikimora can reconcile selected routes without duplicate/stale rows.
6. Standalone Leshy remains correct without Kikimora on Linux and Windows, including event-driven route drift detection.
7. IPv4 and IPv6 readiness remain independent throughout DNS admission, restore, parking and reconciliation.
8. Activation/cutover is an explicit one-writer transaction. There is no state in which Leshy local routing and Kikimora managed routing are both authoritative.
9. Current CI and privileged acceptance gates are green.

---

# 1. P0 — restore the selected-route checkpoint at core startup

## Finding

Kikimora writes `<role StateDir>/selected-routes.json` before and after route mutations, but production startup does not read it.

Relevant code:

- `toad/internal/routing/selected_registry.go::ReadSelectedRoutesCheckpoint`;
- `toad/internal/control/selected_routes.go::newSelectedRouteManager`;
- `toad/internal/control/selected_routes.go::writeCheckpoint`;
- `toad/internal/control/selected_routes.go::loadManagedMarker`.

Today `newSelectedRouteManager` always creates an empty `MemorySelectedRegistry`. The managed marker is restored separately, so after a core crash managed mode can come back with an empty ownership registry while the checkpoint still contains the authoritative selected routes.

## Required fix

1. Add a production restore path to `newSelectedRouteManager`.
2. For every configured role with a state directory:
   - read `selected-routes.json` if it exists;
   - validate schema, role, zone, canonical prefix, source, interface identity and generations;
   - load valid entries into the registry before managed roles are armed.
3. Treat checkpoint corruption as fail-closed:
   - do not silently replace a corrupt checkpoint with an empty registry;
   - do not arm managed mode for the affected role;
   - publish a diagnostic error with the checkpoint path and reason.
4. Restored `Active` entries are ownership claims, not fresh kernel proof. Mark them as requiring reconciliation before they can use the zero-I/O fast path.
5. Restored `Parked` entries must preserve fail-closed protection until exact restoration succeeds.
6. Restored `Pending` entries remain owned and must be reconciled/parked; they must not disappear merely because the previous process crashed between Reserve and MarkActive.

## Tests

Add restart tests covering:

- Active + Parked + Pending entries;
- empty/missing checkpoint;
- corrupt JSON;
- wrong role/zone;
- non-canonical prefix;
- restart while managed marker exists;
- restart after Reserve but before kernel mutation;
- restart after kernel mutation but before MarkActive.

Acceptance: after restart the registry contains the same ownership set, and no selected destination can fall back to discovery from the FIB.

---

# 2. P0 — remove the blind `SelectedActive` fast path until kernel proof exists

## Finding

`SelectedRouteManager.applySelectedRoute` currently returns immediately when the registry contains an `Active` entry with the same identity:

```text
registry says Active
    -> zero I/O success
```

The comment says a monitor/audit repairs a dropped row, but no selected-route kernel monitor/audit exists in production.

Therefore:

```text
kernel selected route deleted externally
registry remains Active
next DNS Ensure -> fast path -> success
route remains missing
```

This can persist indefinitely.

Relevant code:

- `toad/internal/control/selected_routes.go::applySelectedRoute`;
- `toad/internal/platform/contracts.go::RouteManager`;
- platform `ReconcileSelectedRoute` implementations.

## Required fix — safety patch first

Before adding an optimized observer, remove the blind fast path.

For an existing `Active` entry, call the platform reconciler and require exact read-back before returning `already-applied`.

Change the platform contract so reconciliation reports whether it actually mutated the kernel, for example:

```go
type SelectedReconcileResult struct {
    Mutated bool
}
```

Then diagnostics can distinguish:

- exact row already present -> duplicate/no mutation;
- missing/stale owned row repaired -> mutation;
- foreign row -> conflict.

This is intentionally allowed to cost more kernel I/O until the observer below is complete.

## Required fix — optimized steady state

Add a selected-route kernel observation layer:

1. maintain a per-role/per-prefix verification epoch;
2. invalidate only affected prefixes on route/interface events;
3. run a bounded periodic audit as a fallback;
4. permit zero-I/O `Active` fast path only when the entry is verified in the current observation epoch.

Linux may use netlink notifications. Windows must use IP Helper route notifications.

## Tests

- delete an Active v4 row externally -> next audit/event restores it;
- delete an Active v6 row externally -> restores it independently;
- replace an Active row with a foreign same-prefix row -> conflict, no deletion;
- repeated DNS hits after verified convergence -> zero mutations;
- no event source available -> periodic audit still repairs drift.

---

# 3. P0 — Windows Kikimora selected-route Replace must be a real replace

## Finding

The shared planner can return `SelectedReconcileReplace`, but the Windows implementation currently executes the same desired-route create path for Add and Replace.

Relevant code:

- `toad/internal/routing/selected_plan.go::PlanSelectedRoute`;
- `toad/internal/platform/windows/routes/routes.go::ReconcileSelectedRoute`;
- `toad/internal/platform/windows/routes/routes.go::applyOperation`.

A stale owned row on the old ifindex can therefore remain next to the new desired row. The final read-back sees the desired row and reports success even though the stale owned row still exists.

## Required fix

Do not implement selected-route Replace through the generic Windows `applyOperation` replacement behavior.

Implement a dedicated exact selected-route transaction:

1. snapshot same-prefix rows;
2. planner proves every conflicting row is owned;
3. create the desired row if it is not already exact;
4. exact-read-back the desired row;
5. delete only the old rows that match the recorded owned identity and are not the desired row;
6. read back again and require:
   - exactly the desired owned row is present;
   - no stale owned row remains;
   - no foreign row was touched.

Creating/verifying the new row before deleting the old owned row avoids a forwarding hole.

The generic `ERROR_OBJECT_ALREADY_EXISTS -> delete desired row -> recreate` path must not be used for selected-route reconciliation.

## Windows unit tests

Add platform-seam tests for:

- None: exact desired row -> zero mutation;
- Add: no row -> one create;
- Replace old ifindex -> create desired, delete old exact owned row;
- Replace old gateway/metric -> same;
- foreign same-prefix row -> conflict, zero mutation;
- desired + stale owned row -> stale row removed;
- RemoveSelectedRoute removes only the recorded owned identity;
- v4 and v6 parity.

---

# 4. P0 — make Windows Leshy managed transport real

## Finding

Kikimora already exposes its Windows control plane using AF_UNIX:

`C:\ProgramData\Kikimora\core.sock`

in `toad/internal/control/transport_windows.go`.

Leshy `src/routing/kikimora.rs` deliberately rejects every non-Unix target:

```text
the Kikimora managed backend currently requires a Unix control socket
```

Therefore Windows Leshy cannot use managed routing at all.

## Required fix in Leshy

1. Split control transport into platform modules.
2. Keep the existing framing and API v3 protocol unchanged.
3. On Windows, connect to the same AF_UNIX path used by Kikimora.
4. Keep the exact same timeout, capability handshake, subscription and no-local-fallback semantics.
5. Do not add TCP localhost as a second production transport.
6. Add a Windows control-socket integration test against a real `kikimora-core` process.

## Acceptance

On a Windows VM:

```text
Leshy DNS query
 -> KikimoraRouteBackend
 -> AF_UNIX core.sock
 -> EnsureLeshyRoute
 -> Windows SelectedRouteManager writer
 -> exact kernel row
 -> DNS answer
```

Killing the core must yield fail-closed DNS behavior and must never make Leshy switch to its local writer.

---

# 5. P0 — finish standalone Leshy Windows route ownership/reconciliation

## Finding

The large Leshy refactor correctly moved both families through the common `RouteRegistry`, but the Windows platform half of the planned reconciliation work did not land.

Current Windows behavior still includes:

- `GetIpForwardTable2` scans on request-path observation;
- broad same-prefix deletion;
- `ERROR_OBJECT_ALREADY_EXISTS` replacement behavior capable of deleting a foreign row;
- no `NotifyRouteChange2` observer;
- no Windows invalidation receiver.

Relevant file:

- Leshy `src/routing/windows.rs`.

## Required fix in Leshy

### Exact ownership

1. Define the Windows owned identity as:
   - destination prefix;
   - interface index;
   - next hop;
   - route protocol;
   - metric.
2. `ensure_route`:
   - exact desired row -> `AlreadyMatching`;
   - no row -> create;
   - only provably owned stale row -> replace safely;
   - foreign same-prefix row -> typed `RouteConflict`;
   - never delete all same-prefix rows.
3. `remove_route`:
   - delete only the exact row Leshy owns;
   - absent exact row is success;
   - foreign same-prefix row remains untouched.

### Event-driven invalidation

Add a Windows route observer using `NotifyRouteChange2(AF_UNSPEC, ...)`.

Rules:

- callback only captures the event and sends an invalidation;
- callback must not perform route mutation;
- cancellation happens outside the callback;
- emit targeted prefix/interface invalidations when possible;
- fall back to Full when event data is insufficient;
- keep the periodic audit as fallback.

Use exact refresh/read-back after the event rather than trusting incomplete callback row data.

## Windows acceptance

A disposable elevated VM test must cover:

- v4 add/read-back;
- v6 add/read-back;
- 1000 duplicate DNS classifications -> one mutation;
- external delete v4 -> event-driven restore;
- external delete v6 -> event-driven restore;
- foreign same-prefix row -> conflict/no touch;
- cleanup removes owned rows only;
- adapter recreation/ifindex change -> safe rebind;
- no full-FIB scan in the converged DNS fast path.

---

# 6. P1 — store the real Toad generation in selected ownership

## Finding

Selected-route Reserve calls currently pass `0` as `ToadGeneration` in both initial apply and Restore, even though `core.RoleRuntime` already carries the authoritative `ToadGeneration`.

Relevant code:

- `toad/internal/control/selected_routes.go::applySelectedRoute`;
- `toad/internal/control/selected_routes.go::Restore`;
- `toad/internal/core/model.go::RoleRuntime`;
- `toad/internal/routing/selected_registry.go::SelectedRoute`.

## Required fix

1. Obtain the current role runtime before Reserve.
2. Persist its real `ToadGeneration`.
3. On restore/reconcile, treat an older generation as previous ownership that must be re-proven against the current interface before becoming Active.
4. Never use a stale generation as readiness authority.

Tests must show that replacing the Toad process invalidates old selected-route proof even if interface names are reused.

---

# 7. P1 — wire managed activation into a real install/upgrade transaction

## Finding

`EnableManagedSelectedRoutes` and `DisableManagedSelectedRoutes` exist and are tested, but production code does not invoke the enable path.

The current transport smoke writes `routing_backend = "kikimora"` by hand, so it does not prove the real cutover.

Relevant code:

- `toad/internal/control/selected_routes.go::EnableManagedSelectedRoutes`;
- `toad/internal/control/config_api.go`;
- Linux/Windows packaging and installer paths.

## Required fix

Add one explicit cutover operation owned by Kikimora packaging/control:

1. verify the installed Leshy supports the managed backend/API version;
2. verify Kikimora advertises `managed_selected_routes_v1`;
3. stop or quiesce Leshy local route writes;
4. persist the managed activation marker;
5. regenerate Leshy config with `routing_backend = "kikimora"`;
6. restart/reload Leshy;
7. verify the handshake and managed health;
8. only then declare the cutover complete.

Rollback is equally explicit:

1. freeze managed selected writes;
2. park/withdraw managed ownership safely;
3. remove the managed marker;
4. regenerate local-backend config;
5. restart Leshy;
6. verify local mode.

There must be no automatic fallback from managed to local mode on socket loss.

---

# 8. P1 — fix Linux Leshy IPv6 route-event subscription

## Finding

The Linux Leshy monitor subscribes to raw netlink route group 7, which is the IPv4 route group. The production socket does not subscribe to the IPv6 route group.

The existing privileged acceptance tests prove IPv4 external-delete restoration, but only prove IPv6 add/replay — not IPv6 delete/event/restore.

Relevant code:

- Leshy `src/routing/linux_monitor.rs`;
- Leshy `tests/linux_route_registry_netns.rs`;
- Leshy `tests/netns_monitor_probe.rs`.

## Required fix

1. subscribe to both IPv4 and IPv6 route multicast groups using named constants rather than raw magic numbers;
2. keep link notifications;
3. add a privileged `/128 delete -> event -> restore` test;
4. assert that restoration occurs before the periodic audit deadline, proving the event path rather than the fallback.

---

# 9. P1 — targeted Leshy invalidation must not destroy every fast path

## Finding

The common Leshy registry uses one global kernel epoch.

A targeted invalidation currently increments that epoch, marks only the affected entries Dirty, but leaves unrelated entries Verified with the old epoch. Their next DNS hit therefore misses the fast path and performs backend I/O even though they were not invalidated.

Relevant code:

- Leshy `src/routing/mod.rs::reconcile_after_invalidation`;
- Leshy `src/routing/registry.rs::ensure`;
- Leshy `src/routing/registry.rs::mark_dirty`.

## Required fix

Use targeted dirty state as the authority instead of invalidating every entry through one global epoch.

One acceptable implementation:

- `Full` invalidation advances a global/full epoch and dirties all entries;
- `Prefix`, `Interface` and `Zone` invalidations dirty only the affected entries;
- an unaffected Verified entry remains valid for the current full epoch;
- a targeted reconciled entry receives fresh verification state.

Add a regression test:

```text
1000 verified entries
targeted invalidate 1 prefix
reconcile it
DNS-hit remaining 999
=> zero backend calls for those 999
```

---

# 10. P1 — extend managed end-to-end acceptance beyond the denial path

## Finding

`linux/tests/toad/managed-leshy-smoke.sh` proves:

- API framing;
- capability gating;
- denial -> SERVFAIL;
- non-zone bypass;
- no local fallback when core dies.

It intentionally keeps the role not ready. It therefore does not prove a successful selected-route write through the complete Rust -> Go -> kernel chain.

## Required Linux E2E

Add a ready test role/helper and prove:

1. A query -> selected v4 route appears in the namespace;
2. AAAA -> selected v6 route appears independently;
3. repeat queries do not create kernel churn;
4. controller-side external route deletion is detected and restored;
5. family 6 not-ready suppresses only AAAA;
6. recovery freezes, parks, withdraws, restores and unfreezes in the defined order;
7. core loss never invokes the Leshy local writer;
8. explicit Forget/Flush removes only owned routes.

## Required Windows E2E

On the disposable Windows VM prove the same contract through:

- real AF_UNIX core transport;
- real IP Helper route writer;
- v4 + v6;
- adapter ifindex change;
- route drift;
- foreign same-prefix safety.

---

# 11. P1 — add direct Windows Kikimora selected-route tests

## Finding

`toad/internal/platform/windows/routes/routes_test.go` covers endpoint policy, parking and snapshots, but currently has no direct tests of `ReconcileSelectedRoute` or `RemoveSelectedRoute`.

## Required tests

Use the existing `routeTable/createRoute/deleteRoute/unicastAddr` seams to cover every planner action and exact ownership rule listed in section 3.

Also add an elevated Windows acceptance script/test that reads the real forwarding table before and after the operation and cleans up every route it created.

Cross-compilation alone is not an acceptance gate.

---

# 12. P2 — make the current CI gate honest and green

Before calling 08D complete, current master must pass its own gate.

## Kikimora

Run on Linux/macOS/Windows where configured:

- `gofmt -l .` is empty;
- `go vet ./...`;
- `go test ./...`;
- `go test -race ./...`;
- ShellCheck;
- Linux privileged selected-route netns acceptance;
- Windows selected-route runtime acceptance.

The current control test `TestRoleIPv6DefaultOnAndPerRoleToggle` must not write into `/etc/kikimora`. Make it hermetic by constructing the manager with the test config directory before calling `SetRoutingList`.

## Leshy

Require:

- `cargo fmt -- --check`;
- `cargo clippy --all-targets --all-features -- -D warnings`;
- full unit/integration suite;
- Linux privileged route-registry/netlink acceptance;
- Windows privileged route-registry acceptance;
- managed Windows transport E2E.

Do not use fail-fast cancellation as evidence that Windows is green; the Windows job must actually run to completion.

---

# 13. Ordered implementation sequence

The order below is intentional: first restore safety invariants, then optimize and activate production cutover.

## Phase A — Kikimora safety closure

### K11 — checkpoint restore

Files:

- `toad/internal/control/selected_routes.go`;
- `toad/internal/routing/selected_registry.go`;
- selected-route tests.

Deliverable: restart restores ownership and corruption fails closed.

### K12 — stale Active closure

Files:

- `toad/internal/control/selected_routes.go`;
- `toad/internal/platform/contracts.go`;
- Linux and Windows selected-route implementations.

Deliverable: no registry-only success without current kernel proof.

### K13 — Windows exact Replace

Files:

- `toad/internal/platform/windows/routes/routes.go`;
- `routes_test.go`.

Deliverable: old owned rows cannot accumulate; foreign rows cannot be touched.

### K14 — real Toad generation

Files:

- `toad/internal/control/selected_routes.go`;
- registry/recovery tests.

Deliverable: selected ownership is tied to the actual Toad process generation.

### K15 — selected-route observer/audit

Files:

- platform observer code;
- control scheduler/diagnostics.

Deliverable: event-driven drift repair plus bounded periodic fallback.

## Phase B — Leshy platform parity

### L10 — Windows Kikimora AF_UNIX transport

Deliverable: managed backend connects to Windows core.sock and subscriptions work.

### L11 — Windows local exact ownership

Deliverable: no broad same-prefix replacement/deletion.

### L12 — Windows route notifications

Deliverable: `NotifyRouteChange2` targeted reconciliation for v4/v6.

### L13 — Linux IPv6 monitor

Deliverable: real IPv6 route events are received and restore /128 drift.

### L14 — targeted epoch semantics

Deliverable: one route event does not force backend I/O for unrelated verified routes.

## Phase C — production cutover and acceptance

### K16 — explicit managed activation transaction

Deliverable: installer/upgrade can safely switch one writer -> the other and roll back.

### I1 — Linux successful managed E2E

Deliverable: real DNS -> Rust -> Go -> kernel route path with v4/v6 and recovery.

### I2 — Windows successful managed E2E

Deliverable: same contract on the Windows VM.

### I3 — release gate

Only after both repo CI gates and both privileged E2E gates are green:

- update 08D status to COMPLETE;
- remove temporary compatibility wording that says managed mode is Linux/macOS-only;
- release/package the paired compatible Kikimora + Leshy revisions.

---

# 14. Required regression matrix

| Scenario | Linux local Leshy | Windows local Leshy | Linux managed | Windows managed |
|---|---:|---:|---:|---:|
| 1000 duplicate A answers -> one mutation | MUST | MUST | MUST | MUST |
| 1000 duplicate AAAA answers -> one mutation | MUST | MUST | MUST | MUST |
| external v4 route delete -> restore | MUST | MUST | MUST | MUST |
| external v6 route delete -> restore | MUST | MUST | MUST | MUST |
| foreign same-prefix row untouched | MUST | MUST | MUST | MUST |
| interface/ifindex replacement | MUST | MUST | MUST | MUST |
| process restart preserves ownership | N/A | N/A | MUST | MUST |
| crash after Reserve before mutation | N/A | N/A | MUST | MUST |
| crash after mutation before MarkActive | N/A | N/A | MUST | MUST |
| core unavailable -> no local fallback | N/A | N/A | MUST | MUST |
| v4 ready / v6 not ready | MUST | MUST | MUST | MUST |
| v6 ready / v4 not ready | MUST | MUST | MUST | MUST |
| explicit Forget/Flush exact removal | MUST | MUST | MUST | MUST |

---

# 15. Definition of done

Do not mark this packet complete based on compilation, unit tests alone, or the presence of platform code.

Completion requires all of the following evidence in the repositories:

- checkpoint restart proof;
- route-drift proof for both families;
- foreign-route safety proof;
- Windows managed transport proof;
- Windows exact Replace proof;
- real Toad-generation persistence;
- explicit production cutover proof;
- Linux and Windows managed successful E2E;
- green non-cancelled CI for both repositories.

Until those gates pass, the correct project status is:

> 08D implementation substantially landed; Linux and Windows completion/remediation in progress.
