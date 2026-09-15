# Go multi-VPN implementation plan

Status: implementation plan for the target architecture described in
[`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md).

This plan is intentionally documentation-only. The implementation must be done in staged
changes that keep the current shell/systemd architecture usable until the Go control plane
has reached behavioral parity.

## 1. Scope

The target result is a Go daemon that:

- owns several VPN clients/tunnels concurrently;
- treats every managed VPN as a peer using the physical underlay directly;
- monitors physical-network changes inside the daemon;
- owns VPN endpoint-underlay policy;
- uses fail-closed route parking during tunnel loss and controlled recovery;
- publishes only usable VPN roles to Leshy;
- recovers affected roles independently;
- does not use a systemd network watchdog to decide reconnects.

The implementation must not add VPN chaining or a dependency graph between VPN roles.

## 2. Migration principles

### 2.1 Preserve current safety before improving behavior

The existing route parking and endpoint-underlay behavior already encode important safety
properties. The Go rewrite must first reproduce those properties before deleting the shell
owner.

In particular preserve:

- endpoint traffic forced through physical underlay;
- role-specific endpoint policy;
- unreachable fallback when physical underlay is unavailable;
- safe-boundary handling for destructive endpoint policy changes;
- fail-closed parking for Leshy-owned routes;
- parking removal only after a real replacement route is observed;
- role publication only after readiness stabilization;
- independent primary/secondary transitions.

### 2.2 Never have two writers

For each mutable kernel/runtime namespace define one active owner:

```text
endpoint table/rules
parking routes
role publication files
VPN process lifecycle
```

During migration a feature gate may switch ownership from shell to Go, but both must not
write the same resource concurrently.

### 2.3 Introduce observation before control

The first Go versions should be able to run in observer/shadow mode. They should compute
underlay snapshots, role state and intended actions without changing routes or restarting
VPNs. This gives a direct comparison against the current runtime before cutover.

### 2.4 Keep Leshy compatibility during the VPN rewrite

Do not combine the Go VPN-client migration with a mandatory Leshy protocol redesign.
Initially retain the existing `primary.dev` and `secondary.dev` publication contract and
current Leshy route behavior. Generalize the internal role model first; extend the
Leshy-facing schema separately when additional zones are actually supported.

## 3. Proposed Go package layout

The exact filenames may change, but responsibilities should be separated approximately as
follows:

```text
cmd/kikimora/
    main.go

internal/config/
    load.go
    validate.go
    legacy_profiles.go

internal/core/
    controller.go
    reconcile.go
    events.go
    state.go

internal/netstate/
    snapshot.go
    compare.go
    coalesce.go

internal/platform/linux/netlink/
    watcher.go
    snapshot.go
    routes.go

internal/platform/linux/networkmanager/
    watcher.go
    metadata.go

internal/platform/linux/logind/
    sleep.go

internal/underlay/
    resolver.go
    identity.go

internal/tunnel/
    role.go
    state.go
    supervisor.go
    driver.go

internal/tunnel/drivers/
    <driver implementations>

internal/endpoint/
    manager.go
    providers.go
    policy.go
    state.go

internal/parking/
    manager.go
    ownership.go
    state.go

internal/leshy/
    publication.go
    routes.go
    recovery.go

internal/routing/
    executor.go
    transaction.go

internal/status/
    model.go
    json.go

tests/netns/
    ...
```

Platform-specific Linux code must stay behind narrow interfaces so the core state machine
can be unit-tested without real netlink, NetworkManager or systemd.

## 4. Stage G0 - freeze contracts and build the test harness

Goal: make current behavior executable as a specification before the Go control plane owns
anything.

### Work

1. Record the current kernel resources and ownership boundaries:
   - endpoint rules and table `51890`;
   - parking metric and route shape;
   - runtime publication files;
   - readiness counters and current lifecycle files.
2. Convert current endpoint-underlay and parking integration scenarios into a reusable
   netns-oriented acceptance matrix.
3. Add explicit regression scenarios for physical-network changes even though the current
   shell implementation may not pass all of them yet.
4. Define a machine-readable status model for the future daemon before the CLI format is
   implemented.
5. Define feature gates for ownership cutover so tests can prove that only one writer is
   active.

### Required acceptance scenarios

At minimum the harness must represent:

```text
cold boot with no VPNs
one role available, another absent
both roles available
hard disappearance of one TUN
reappearance of one TUN
endpoint provider failure
endpoint policy pending while a role is live
route parking after hard disappearance
parking retained across recovery until real route observation
physical underlay absent
physical underlay restored
```

### Exit criteria

- current safety behavior has executable tests;
- future underlay/recovery tests exist even if marked pending for later stages;
- resource ownership is documented and feature-gateable.

## 5. Stage G1 - Go daemon skeleton and legacy configuration reader

Goal: establish a long-running process and internal role model without owning routes or
VPNs.

### Work

1. Create `cmd/kikimora` and lifecycle wiring:
   - context cancellation;
   - signal handling;
   - structured logging;
   - clean shutdown;
   - status snapshot publication.
2. Implement the internal N-role model.
3. Read the current primary/secondary profile configuration through a compatibility
   adapter.
4. Convert existing profile data into internal role objects:

```text
role name
current interface expectation
endpoint provider
provider args
Leshy publication identity
```

5. Add a versioned internal configuration model for future client-owned VPN driver
   settings, but do not require users to migrate yet.
6. Add validation that all managed roles have `physical` as their transport underlay.
   There is no configuration switch for VPN-over-VPN.

### Tests

- legacy profile parsing;
- duplicate/invalid role validation;
- deterministic role ordering;
- configuration reload with unchanged semantic state;
- rejecting a future configuration that attempts to select another managed VPN as
  transport underlay.

### Exit criteria

The daemon can start in observer mode, load the same role selection as the existing
installation and expose it in status without mutating the host.

## 6. Stage G2 - canonical physical-underlay monitor in shadow mode

Goal: replace polling/network-manager-state reasoning with a single canonical physical
snapshot while still making no routing or reconnect changes.

### 6.1 Mandatory rtnetlink watcher

Subscribe to:

- link changes;
- IPv4/IPv6 address changes;
- route changes relevant to physical default selection.

Watch callbacks only emit invalidation events. They do not calculate recovery decisions.

### 6.2 Snapshot builder

After invalidation, rebuild a full canonical snapshot from the kernel.

For each address family select the effective physical default using semantics compatible
with the current endpoint-underlay implementation. Exclude:

- every managed VPN interface;
- loopback;
- `leshy-dns0` and other Kikimora-owned virtual plumbing;
- any explicitly configured non-underlay virtual interface category required by tests.

Capture:

```text
ifindex
name
gateway
preferred source
MTU
route table
metric/priority metadata
```

The selected preferred source must be part of material path identity. A DHCP/source
address change can invalidate an existing VPN transport even if interface and gateway stay
the same.

### 6.3 NetworkManager enrichment

Add an optional DBus watcher for:

- active connection identity;
- Wi-Fi BSSID/AP change;
- NetworkManager restart/reconnect.

Do not use global NetworkManager connectivity state as physical-path truth.

A `CONNECTED_SITE -> CONNECTED_GLOBAL` transition with the same kernel snapshot must be
logged as a no-op and must not advance the underlay epoch.

### 6.4 Suspend/resume

Subscribe to logind sleep notifications.

On resume:

- force a complete snapshot rebuild;
- request role validation even if the physical snapshot compares equal;
- do not immediately restart every role.

### 6.5 Event coalescing

Implement a short settle/debounce window with a bounded maximum delay. The purpose is to
collapse DHCP and link bursts, not to hide state changes for seconds.

The implementation should be configurable for tests and should have two properties:

1. a burst results in one canonical resnapshot/reconcile where possible;
2. sustained churn cannot postpone reconciliation forever.

### 6.6 Periodic consistency audit

Add a low-frequency resnapshot timer inside the daemon. It is not a reconnect watchdog;
it only detects missed events or drift between cached and kernel state.

### Tests

Pure unit tests:

- snapshot equality and material difference rules;
- event coalescing;
- epoch advancement;
- source-address change;
- gateway change;
- route metric change that does not change the selected path;
- NetworkManager connectivity classification change with identical path;
- BSSID handoff classification;
- resume validation with identical snapshot.

Netns/integration tests:

- Wi-Fi-like interface replaced by Ethernet-like interface;
- default route removed and restored;
- unrelated veth churn;
- managed TUN creation/removal does not become physical underlay;
- physical source address change.

### Exit criteria

The Go daemon can run for real installations in shadow mode and explain every underlay
change as old/new canonical state without changing a route or VPN.

## 7. Stage G3 - central reconciler and generation-safe execution

Goal: make all future mutation decisions pass through one state authority.

### Work

1. Implement a reconciler that owns:
   - current underlay snapshot and epoch;
   - desired role configuration;
   - runtime role state;
   - desired endpoint/parking/publication state.
2. All event sources emit invalidations into the reconciler queue.
3. Add a monotonically increasing reconcile generation.
4. Create per-role executors for blocking VPN operations.
5. Create a serialized routing executor for route/rule mutations.
6. Every async completion carries the generation that scheduled it.
7. If completion is stale, record the result but run another reconcile before applying a
   state transition that could overwrite newer desired state.
8. Ensure shutdown cancels or drains in-flight operations predictably.

### Why this stage comes before route ownership

Without generation-safe execution, a sequence such as:

```text
resume
route change
DHCP source change
old driver start completion
```

can commit actions calculated against stale underlay. This must be solved before Go starts
owning endpoint routes or tunnel lifecycle.

### Tests

- stale completion cannot restore an old role state;
- repeated invalidations collapse into one desired result;
- per-role operations do not cause global reconnects;
- route mutations are serialized;
- cancellation during start/recovery leaves the next reconcile able to reconstruct state.

## 8. Stage G4 - port endpoint-underlay ownership to Go

Goal: move table `51890` and endpoint provider policy into the Go control plane while VPNs
may still be externally owned.

This is the first stage with kernel mutation and therefore requires an explicit single-
writer cutover flag.

### Work

1. Port endpoint provider invocation semantics:
   - `static`;
   - `happ`;
   - `command`;
   - exact vs `dynamic-additive` capability.
2. Port endpoint address resolution and IPv4-mapped-IPv6 filtering.
3. Port role-specific policy priorities.
4. Port physical-underlay selection from the canonical snapshot rather than recomputing it
   independently.
5. Preserve unreachable fallback when no physical path exists.
6. Preserve current state comparison/idempotence.
7. Preserve `underlay-pending` while external VPN clients still own tunnel lifecycle.
8. Expose pending reason and desired/applied underlay identity in status.

### Cutover rule

When Go owns endpoint policy, shell `route-watch` must not mutate table `51890` or its
rules. The migration switch must be atomic at service/config level; there is no supported
mixed ownership.

### Tests

Re-run all current endpoint-underlay tests against the Go owner, including:

- policy present before VPN startup;
- exact endpoint change while VPN is live becomes pending;
- unchanged pending does not churn logs/files/routes;
- dynamic-additive adds only proven live endpoints;
- physical underlay change while role is live becomes pending;
- provider failure preserves previous safe policy;
- no physical underlay installs/keeps safe unreachable behavior;
- two roles transition independently.

### Exit criteria

Go endpoint policy passes parity tests and can be enabled on a real installation without
shell touching table `51890`.

## 9. Stage G5 - port route parking and Leshy role publication to Go

Goal: make Go own the safety barrier required before it begins restarting VPNs itself.

### 9.1 Parking ownership

Port the existing route ownership model:

- establish baseline routes that predate Kikimora ownership;
- observe Leshy-created IPv4 `proto static` `/32` routes on published managed roles;
- maintain last-observed owned destinations;
- on role withdrawal/hard disappearance, install high-metric unreachable parking;
- never park endpoint-underlay destinations;
- keep parking across recovery;
- remove a parked destination only after a real winning route has been observed again.

The first Go port should preserve the existing metric and route shape so behavior can be
compared directly.

### 9.2 Publication ownership

Move writes/removals of:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

into the Go role state machine.

The shell watcher must stop writing these files when the Go publication owner is enabled.

### 9.3 Controlled-withdraw primitive

Implement one atomic logical operation used by later recovery stages:

```text
prepare role withdrawal
    -> refresh last route observation
    -> ensure parking safety barrier
    -> unpublish role from Leshy
    -> confirm desired fail-closed state
```

The exact kernel calls cannot be literally atomic, but the ordering and rollback/reconcile
semantics must make every intermediate state fail closed.

### Tests

- current route-parking suite against Go implementation;
- hard interface disappearance uses cached ownership;
- controlled withdrawal parks before unpublish/teardown;
- unchanged role keeps publication;
- one role withdrawal does not unpublish another;
- daemon restart reconstructs ownership safely from kernel/runtime state;
- stale marker files are not trusted as proof of readiness.

### Exit criteria

Go owns parking and Leshy publication safely. This is a hard prerequisite for automatic
Go-controlled tunnel recovery.

## 10. Stage G6 - tunnel driver API and one real driver

Goal: make Kikimora own one VPN role lifecycle end-to-end before scaling to all roles.

### 10.1 Driver API

Define a protocol-neutral interface around operations conceptually equivalent to:

```text
Start(role config, selected physical underlay)
Stop/Quiesce
Inspect
Validate
Interface
Optional RebindTransport
Optional RestartTransportKeepingTun
```

A driver reports capabilities; the core decides recovery policy.

### 10.2 Driver ownership boundaries

A driver may own:

- protocol process/library instance;
- protocol-specific sockets;
- tunnel-interface creation if required by the protocol;
- protocol-specific health/readiness inspection.

A driver must not own:

- Leshy user routes;
- route parking;
- table `51890` policy;
- global underlay selection;
- role publication;
- another role's lifecycle.

### 10.3 Readiness

Replace the old generic `interface UP + IPv4 for N cycles` as the sole authority with a
layered decision:

```text
endpoint underlay safe
AND structural tunnel ready
AND driver-specific required readiness
AND role still matches current reconcile generation
```

The structural stabilization window can remain part of the decision, but it is not enough
by itself after resume or an underlay change.

### 10.4 First-driver strategy

Implement only one real VPN driver first. Choose the protocol/client that gives the best
access to transport lifecycle and health information. Do not implement several incomplete
drivers in parallel.

The first driver must support test injection/fakes so state-machine tests do not require a
real remote VPN server.

### Tests

- normal start/stop;
- start failure;
- cancellation during start;
- interface appears before transport readiness;
- driver reports unhealthy while TUN remains UP;
- driver crash/disappearance;
- restart returns a new TUN instance;
- if supported, transport-only restart preserves TUN identity.

### Exit criteria

One role can be fully owned by Go behind an explicit canary/feature gate while other roles
remain external and safe.

## 11. Stage G7 - controlled underlay recovery using parking

Goal: implement the behavior that motivated the architecture: safe reaction to physical
network changes without an Amnezia-style unconditional full reconnect.

### 11.1 Recovery decision

When the underlay epoch changes or resume forces validation, each enabled role is examined
independently.

Decision order:

```text
1. role stopped/disabled -> no action
2. no physical underlay -> WaitingForUnderlay + fail closed
3. validate existing transport
4. if healthy on current path -> keep Ready, update validated epoch
5. if driver can rebind safely -> rebind then validate
6. if driver can restart transport while keeping TUN -> controlled transport recovery
7. otherwise -> controlled full role restart
```

No step implies restarting another role.

### 11.2 Disruptive recovery transaction

For actions that can interrupt the role:

```text
Ready
  -> Recovering
  -> refresh owned-route observation
  -> install/confirm parking
  -> unpublish role
  -> quiesce/stop transport
  -> apply pending/new endpoint underlay at safe boundary
  -> start/rebind transport
  -> structural + driver validation
  -> publish role
  -> trigger required Leshy re-evaluation/cache action
  -> observe real Leshy routes
  -> remove matching parking
  -> Ready
```

If any step fails, keep the role unpublished and user routes parked. Retry policy must be
bounded/backed off; failure must not create a hot restart loop.

### 11.3 Hard physical loss

When the physical path disappears:

- mark enabled roles as no longer validated for new traffic;
- use route parking to fail closed;
- preserve endpoint unreachable protection;
- move roles to `WaitingForUnderlay` as appropriate;
- do not repeatedly stop/start while no underlay exists.

When a physical path returns, reconcile once against the new epoch.

### 11.4 Resume

Resume forces validation of every Ready role even if underlay identity is unchanged.

A role with a healthy surviving transport stays Ready. A stale role enters the same
controlled recovery transaction. The existence of its TUN alone is not enough to skip
recovery.

### Regression tests

This stage must contain explicit tests for known classes of bugs:

1. **NetworkManager connectivity-only transition**
   - emit `CONNECTED_GLOBAL -> CONNECTED_SITE -> CONNECTED_GLOBAL`;
   - keep interface/gateway/source unchanged;
   - expect zero underlay epoch changes and zero tunnel restarts.
2. **Docker/veth churn**
   - create/delete unrelated veth pairs;
   - expect zero tunnel restarts.
3. **Wi-Fi -> Ethernet**
   - material physical path change;
   - validate/recover each role independently.
4. **DHCP/source address change**
   - same interface/gateway, new preferred source;
   - new epoch and role validation.
5. **Gateway change**
   - new epoch and role validation.
6. **Suspend/resume**
   - equal snapshot after resume;
   - forced validation, restart only stale roles.
7. **NetworkManager restart**
   - rebuild enrichment metadata;
   - do not restart unless canonical/validated state requires it.
8. **One stale role, one healthy role**
   - only stale role recovers;
   - healthy role and its Leshy routes remain published.
9. **Recovery failure**
   - failed role remains parked/unpublished;
   - other roles continue operating.
10. **Event burst during recovery**
    - old recovery completion cannot overwrite a newer underlay generation.

### Exit criteria

Physical-network changes and resume are handled entirely inside Go with per-role recovery,
fail-closed user routing and no unconditional global reconnect.

## 12. Stage G8 - own all configured VPN roles

Goal: move from one canary Go-owned VPN to the normal multi-VPN configuration.

### Work

1. Enable driver-backed ownership for every configured role whose driver is supported.
2. Keep a common role model and lifecycle; do not special-case `primary` recovery against
   `secondary` recovery.
3. Allow independent concurrent operation of multiple roles.
4. Use bounded recovery concurrency to prevent CPU/process/route storms after a global
   physical handoff.
5. Keep routing mutations serialized even if protocol operations run concurrently.
6. Expose each role independently through status and logs.
7. Add configuration and CLI migration for client-owned VPN driver settings.

### Tests

- start all roles with physical underlay available;
- one role start failure does not block another;
- one role recovery does not withdraw another;
- simultaneous stale transports recover without route ownership conflicts;
- all roles wait when physical underlay disappears;
- physical underlay return allows all roles to recover with bounded concurrency;
- endpoint policy for every role still points only to physical underlay.

### Exit criteria

The normal Kikimora deployment no longer depends on external VPN clients for managed
roles.

## 13. Stage G9 - retire `leshy-route-watch.service`

Goal: remove the legacy watcher only after every responsibility has an explicit Go owner.

### Responsibility checklist

Before deletion, prove the Go daemon owns or intentionally replaces every old behavior:

```text
[ ] managed role detection/readiness
[ ] role publication files
[ ] endpoint provider reconciliation
[ ] table 51890 and endpoint rules
[ ] endpoint pending/safe-boundary behavior
[ ] Leshy route observation
[ ] route parking
[ ] hard-disappearance cached ownership
[ ] recovery publication/cache action
[ ] physical-underlay observation
[ ] idempotent restart/reconcile behavior
```

### Systemd target

Systemd should supervise the Go daemon as a process, not supervise individual tunnel state.

The final cutover removes the route-watch service/unit and any timer/watchdog whose job is
to infer physical-network changes and reconnect VPNs.

A separate Leshy/DNS health service may remain temporarily if its responsibility has not
yet been migrated. Its existence must not duplicate underlay/tunnel lifecycle logic.

### Exit criteria

Stopping/removing the legacy route watcher changes no expected routing, parking, endpoint,
publication or recovery behavior.

## 14. Stage G10 - status, diagnostics and operator tooling

This work starts earlier but is completed after full ownership.

### Required status

Human and JSON status should expose:

```text
Underlay:
  epoch
  IPv4 selected path
  IPv6 selected path
  last change reason/time

Per role:
  desired enabled/disabled
  runtime state
  driver
  interface
  endpoint provider
  endpoint policy ready/pending
  last validated underlay epoch
  last health result
  current recovery action/reason
  published to Leshy yes/no
  parked route count
```

### Debug logging

Every recovery decision should log old/new identities and why an action was selected.
Examples of useful reasons:

```text
source-address-changed
default-interface-changed
gateway-changed
resume-validation
transport-health-failed
endpoint-policy-pending
transport-rebind
transport-restart
full-role-restart
```

A log line saying only `network changed` is insufficient.

## 15. Detailed ownership cutover matrix

Use this matrix as a release gate.

| Resource | Current owner | Intermediate owner | Final owner |
| --- | --- | --- | --- |
| physical underlay observation | shell polling/reconcile | Go shadow | Go |
| endpoint provider execution | route-watch | Go after G4 cutover | Go |
| table `51890` and endpoint rules | route-watch | Go after G4 cutover | Go |
| Leshy route observation | route-watch/lifecycle | Go after G5 cutover | Go |
| parking routes | route lifecycle | Go after G5 cutover | Go |
| role publication files | route-watch | Go after G5 cutover | Go |
| VPN transport/process | external clients | one Go canary role | Go drivers |
| role readiness truth | inferred interface state | Go role state | Go role state |
| Leshy classification/routes | Leshy | Leshy | Leshy |
| process supervision | systemd | systemd | systemd |

No row may have two mutation owners at the same time.

## 16. Failure handling rules

These rules must be encoded in tests, not left as operator convention.

### Underlay unavailable

```text
no physical path
=> do not spin reconnects
=> endpoint policy remains fail closed/safe
=> user destinations remain parked
=> enabled roles wait
```

### Endpoint provider fails

```text
provider error
=> keep last known safe endpoint policy
=> do not publish a replacement role without endpoint safety
=> expose degraded/pending status
```

### Driver start/recovery fails

```text
failure
=> role remains unpublished
=> parking remains
=> backoff retry according to role policy
=> other roles are untouched
```

### Go daemon restarts

On startup, rebuild truth from:

- kernel links/addresses/routes/rules;
- configured role definitions;
- reconstructible runtime files;
- driver/process inspection where supported.

Do not trust an old `Ready` marker. Revalidation is required before publication.

### Leshy unavailable

Tunnel lifecycle and endpoint safety remain owned by Go. Publication/recovery must not
remove parking simply because Leshy is unavailable. Parking is removed only after the
real replacement route is observed.

## 17. Test strategy

Use four layers.

### 17.1 Pure unit tests

Cover:

- state-machine transitions;
- snapshot comparison;
- epoch semantics;
- recovery decision policy;
- generation/stale completion handling;
- configuration migration;
- endpoint desired/applied state;
- parking desired/applied state.

### 17.2 Fake-driver integration tests

A deterministic fake driver must simulate:

- slow start;
- start failure;
- stale transport after resume;
- health success/failure/unknown;
- rebind support;
- transport-only restart support;
- full TUN replacement;
- cancellation.

This is the primary way to exhaustively test recovery ordering.

### 17.3 Linux netns tests

Verify real kernel behavior for:

- route/rule ownership;
- physical default changes;
- source address changes;
- endpoint table behavior;
- route parking;
- TUN disappearance;
- unrelated veth churn;
- multi-role isolation.

### 17.4 Real protocol smoke tests

For each supported driver, keep a small smoke suite for:

- connect;
- disconnect;
- suspend/resume where CI/environment permits;
- physical interface handoff where CI/environment permits;
- recovery after remote transport interruption.

The correctness of generic recovery policy must not depend solely on flaky external VPN
servers; that belongs in deterministic fake-driver and netns tests.

## 18. CI gates

Each implementation stage should add a CI gate before ownership moves.

Minimum final gates:

```text
go test ./...
go test -race ./...
netns underlay-change suite
endpoint-underlay parity suite
route-parking parity suite
multi-role isolation suite
legacy configuration migration suite
```

No legacy writer is removed until its parity suite passes against the Go owner.

## 19. Recommended implementation order summary

```text
G0 contracts/tests
  -> G1 daemon + role model
  -> G2 underlay shadow monitor
  -> G3 reconciler/generation model
  -> G4 endpoint-underlay Go ownership
  -> G5 parking + Leshy publication Go ownership
  -> G6 first real VPN driver
  -> G7 controlled underlay recovery
  -> G8 all managed roles owned by Go
  -> G9 remove route-watch/watchdog architecture
  -> G10 complete diagnostics/operator surface
```

The critical ordering is G4/G5 before G7: Kikimora must own endpoint safety and parking
before it is allowed to perform automatic disruptive VPN recovery.

## 20. Definition of done for the architecture migration

The migration is complete when all of the following are true:

1. Kikimora owns the configured VPN clients/tunnels in Go.
2. Every managed VPN endpoint is proven to use the physical underlay, never another
   managed VPN.
3. Physical-network changes are detected inside the Go daemon from canonical kernel state.
4. NetworkManager connectivity-only transitions do not restart tunnels.
5. Suspend/resume validates roles even when TUN interfaces stay UP.
6. Recovery decisions are per role.
7. A disruptive recovery parks user destinations before withdrawing/stopping a role.
8. Parking remains until Leshy has recreated real winning routes.
9. Endpoint-underlay changes are applied at a safe transport boundary.
10. No shell/systemd network watchdog owns VPN recovery.
11. `leshy-route-watch.service` has been removed after parity.
12. Systemd only supervises the daemon/process lifecycle for this functionality.
13. The full parity, regression, netns and race-test suites pass.
