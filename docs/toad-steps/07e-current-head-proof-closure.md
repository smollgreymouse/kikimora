# Toad step 07E — current-HEAD proof closure

Status: **IMPLEMENTATION LANDED; LOCAL CLOSURE PENDING** (2026-09-23).

Reviewed branch: `feat/native-core-vpn-clients`
Reviewed HEAD: `cbfb70a31cda689fac1aa2719f9a00a3b60c5db4`
PR: #27

Reviewed current CI:

- `Route parking` run `35764438643`: green;
- `CLI JSON API` run `35764438687`: green;
- `Desktop UI` run `35764438640`: green;
- `VPN profiles` run `35764438751`: green;
- `VPN endpoint underlay` run `35764438836`: green;
- `ShellCheck` run `35764438645`: **red**;
- `Toad core` run `35764438661`: **red**.

This packet reopens the automated closure of 07C/07D. It does not redesign 07A/07B and it does not perform a real host cutover.

## Executor evidence policy

For executors, **local commands are authoritative**. Do not query, wait for, or use GitHub Actions as a gate for continuing work.

GitHub CI is a reviewer-side post-push signal only. The executor must:

- run the required Go/shell/privileged tests locally;
- fix local failures;
- record exact local commands and results;
- commit/push completed work;
- continue to the next packet when the packet's local gates are green.

A red/stale/missing GitHub workflow by itself is **not** a STOP condition for an executor.

For ShellCheck, use a local repository command. If workflow-specific exclusions are needed, factor the existing workflow logic into a reusable local script (preferred path: `linux/tests/shellcheck-all.sh`) and make the workflow call the same script later; do not rely on reading CI logs.

---
The executor must fix proof quality, not merely make CI green.

---

## 0. Current audit conclusions

The implementation added after `def08ccb...` is real and should be preserved where correct:

- supervised underlay coalescer;
- 250 ms settle / 2 s maximum coalescing;
- bounded async restart retry state;
- injectable sleep source field;
- NetworkManager ownership watcher;
- OpenConnect transport-recovery path;
- persisted desired state;
- API-aware shell cutover fixture.

However, current HEAD cannot be called 07C/07D automated-complete because:

1. final local closure commands have not all been recorded on the implementation HEAD;
2. one race-mode test is timing-flaky;
3. several new unit tests do not exercise the production goroutines they claim to test;
4. observer health can report healthy while semantic underlay convergence is dead;
5. the new privileged orchestration script is not ShellCheck-clean and does not follow the real core/config/ownership contracts;
6. the 07D diagnostics acceptance is still legacy-Leshy-centric.

Do not mark 07C or 07D complete until the packet's **local** acceptance commands are green.

---

# Phase 1 — restore a truthful green baseline

## 1.1 Fix `TestAsyncFullRestartHandoff` synchronization

File:

`toad/internal/control/control_test.go`

Current failure under:

```bash
go test -race ./...
```

CI job: `106870267145`.

Observed failure:

```text
TestAsyncFullRestartHandoff:
product role should be Starting after ErrToadRestartPending
got Recovering
```

Root cause in the test:

- `routeTargetRecoveryDriver.StartTransport()` closes `started`;
- the test wakes immediately;
- `Engine.Recover` has not necessarily returned from `StartTransport` and committed `RoleStarting` yet;
- non-race scheduling usually hides the intermediate state.

### Required diff

Do not change production state semantics to satisfy this test.

Replace the immediate state assertion after `<-driver.StartedChan()` with a bounded predicate wait for:

```go
role.State == core.RoleStarting &&
role.Recovery.Step == core.RecoveryStartTransport
```

Prefer a reusable polling helper with a short deadline.

Even better, if a test-only signal can be emitted after `recoverRole` returns without changing production behavior, use that completion boundary.

Do not replace the assertion with `Starting || Recovering`.

Run the test repeatedly:

```bash
cd toad
go test -count=50 ./internal/control -run TestAsyncFullRestartHandoff
go test -race -count=20 ./internal/control -run TestAsyncFullRestartHandoff
```

## 1.2 Make `go-orchestration-acceptance.sh` ShellCheck-clean

File:

`linux/tests/toad/go-orchestration-acceptance.sh`

Current ShellCheck run `35764438645` reports at least:

- SC1090;
- SC2153;
- SC2317;
- SC2034.

Fix the script itself. Do not add a broad file-level exclusion for all warnings.

Allowed narrow handling:

- use a ShellCheck `source=` directive for the known `lib/netns.sh` path;
- initialize required environment variables explicitly using shell parameter checks;
- remove unused variables;
- remove unreachable helper code or wire it into actual execution.

After Phase 1:

```bash
shellcheck linux/tests/toad/go-orchestration-acceptance.sh
cd toad
go test ./...
go test -race ./...
go vet ./...
```

Do not proceed while these are red.

---

# Phase 2 — replace false-positive observer/recovery tests

Current tests were added rapidly and several do not prove the production behavior.

## 2.1 Inject observer dependencies before goroutines start

Current code point:

`toad/internal/control/control.go:newManager`

Problem:

`newManager` starts:

- `watchUnderlay`;
- `product.Run`;
- `watchSleep`;
- `watchManagedInterfaceOwnership`

before tests can safely replace:

- `sleepSource`;
- `interfaceOwnership`;
- `underlayBuilder`.

A test that assigns these fields after `NewManager` races with a watcher that may already have captured the old dependency.

### Required implementation

Add one internal dependency constructor, for example:

```go
type managerDependencies struct {
    underlayBuilder    func(context.Context) (netstate.Snapshot, error)
    underlayWatch      func(context.Context, chan<- netstate.Invalidation) error
    sleepSource        platform.SleepSource
    interfaceOwnership platform.ManagedInterfaceVerifier
}

func newManagerWithDependencies(
    paths []string,
    launcher Launcher,
    socketPath, legacyPath, providerDir string,
    deps managerDependencies,
) (*Manager, error)
```

Production `newManager` supplies the real defaults.

Tests construct the Manager with fakes **before** observer goroutines start.

Do not expose this as public product API.

Also add a field for raw underlay watch:

```go
underlayWatch func(context.Context, chan<- netstate.Invalidation) error
```

and replace direct `underlay.DefaultWatch(...)` in `superviseUnderlayWatch`.

## 2.2 Rewrite the sleep reconnect test

Current test:

`TestSleepWatcherReconnectsAndHandlesSuspendResume`

is scheduling-dependent because it swaps `manager.sleepSource` after construction.

Replace it with a pre-injected fake that:

1. first `Watch` call returns a controlled error;
2. test waits for observer degraded state;
3. second `Watch` call is observed through a call-count/event channel;
4. test waits for observer healthy state;
5. fake sends `Preparing=true`;
6. assert desired role bits unchanged and `suspended=true`;
7. fake sends `Preparing=false`;
8. assert a real underlay invalidation reaches the production coalescer;
9. fake underlay builder returns a newer canonical snapshot;
10. assert current product validation epoch is invalidated/revalidated against the new current epoch.

No fixed 100 ms sleep as the proof mechanism. Use channels or bounded predicates.

## 2.3 Rewrite `TestResumeWhileRecoveryInFlight`

Current test cancels `monitorCtx`, manually toggles `suspended`, queues invalidations with no consumer, and only checks `recoveryInFlight`.

That is not an acceptance test.

Replacement must use the actual:

- `watchSleep`;
- `watchUnderlay`;
- coalescer;
- recovery scheduler.

Scenario:

1. role is Ready at epoch N;
2. recovery is deliberately held in-flight by a blocking fake recovery driver;
3. send suspend;
4. mutate fake raw underlay to epoch-relevant new identity;
5. send resume plus several raw invalidations;
6. release recovery;
7. require exactly one canonical new epoch N+1;
8. no two recovery transactions overlap;
9. stale validation at N cannot restore Ready;
10. final successful validation is for N+1.

Record recovery-driver max concurrency and assert it never exceeds 1.

## 2.4 Rewrite NetworkManager reconnect tests

Current `TestNetworkManagerWatcherReconnects` has no assertion and its fake ignores `watchErr`.

Replace fake watcher with scripted attempts:

```text
attempt 1 -> return injected error
attempt 2 -> stay alive and accept event
```

Assert:

- first failure makes NM observer unhealthy;
- second attempt occurs after bounded backoff;
- observer becomes healthy;
- event causes `EnsureUnmanaged` to be called again;
- managed -> unmanaged transition requests validation exactly once;
- `vpn0` or another explicitly external interface is never passed through a Kikimora-owned mutation path.

Do not mutate global `supervisor.RetryDelays`; use a Manager-local test seam if shorter backoff is needed.

## 2.5 Exercise the real coalescer supervisor

Current tests `TestCoalescerBuildFailureRestartsAndProcessesNextInvalidation` and `TestCoalescerRestartUsesCurrentUnderlay` implement their own mini-supervisor loop instead of testing `Manager.superviseUnderlayCoalescer`.

Rewrite them around a Manager built with injected `underlayBuilder`.

Assertions:

- first build failure marks convergence unhealthy;
- the production supervisor starts a new Coalescer;
- restart uses current `m.underlay`, not stale initial state;
- one subsequent invalidation converges to the expected epoch.

## 2.6 Make the audit test actually prove audit convergence

Current `TestAuditRemainsAliveWhileCoalescerDegraded` only proves `manager.shuttingDown == false`.

Add an injectable audit interval, e.g.:

```go
underlayAuditInterval time.Duration
```

production default: 30 s.

Test:

- raw watcher sends no useful event;
- builder initially reflects old state;
- external fake state changes;
- no raw invalidation is sent;
- short test audit interval fires;
- canonical underlay advances because of the audit.

That is the actual acceptance condition.

---

# Phase 3 — make observer diagnostics truthful

Current:

`ObserverState` has one `NetlinkHealthy` bit and one shared `LastError`.

But two independent goroutines call the same netlink health key:

- raw netlink watcher;
- semantic underlay coalescer.

A healthy raw subscription can overwrite a failed coalescer and make diagnostics report healthy even though semantic convergence is dead.

Any healthy observer can also clear the shared `LastError` from another observer.

## 3.1 Split health dimensions

Change `ObserverState` to expose separate health for:

- raw netlink watch;
- underlay convergence;
- sleep source;
- NetworkManager.

Give each its own error field.

If compatibility requires keeping `netlink_healthy`, derive it as:

```text
raw-watch-healthy && underlay-converger-healthy
```

Do not allow a healthy sleep/NM watcher to clear an underlay error.

## 3.2 Self-kick convergence after coalescer restart

When a Coalescer exits because `Build` or callback failed:

- back off;
- construct the new Coalescer using current underlay;
- immediately queue one synthetic invalidation, e.g. `coalescer-restart`.

Do not wait up to 30 seconds for the periodic audit before retrying useful convergence.

Test:

```text
first Build fails
no additional external event
supervisor restarts
self-kick causes second Build
new snapshot becomes canonical
```

---

# Phase 4 — audit bounded async restart timers for stale work

Files:

- `toad/internal/control/control.go`
  - `schedulePendingRestartRetry`;
  - `scheduleRecoveryRetry`;
  - `scheduleValidation`;
  - `stopRoleProcess`;
  - process replacement paths.

Current `schedulePendingRestartRetry` captures process and epoch but then fetches the current product operation at timer fire.

That must be deliberate and proven.

## Required tests

1. old retry timer + new process -> old timer does nothing;
2. old retry timer + new underlay epoch -> old timer does nothing;
3. old retry timer + explicit disconnect -> old timer does nothing;
4. old retry timer + replacement RouteReady -> old timer does nothing;
5. old retry timer + newer product operation -> old timer must not accidentally launch duplicate work for the newer operation unless that newer operation independently requested recovery;
6. repeated pending restart retries never overlap; max recovery concurrency = 1.

Prefer capturing the exact operation/generation identity and rejecting stale timer work rather than silently adopting a newer operation.

Move goroutine launch outside `m.mu` where practical.

Keep retry backoff fail-closed; do not add a retry path that clears parking merely because a timer expires.

---

# Phase 5 — rebuild the privileged 07D gate from proven fixtures

Files:

- `linux/tests/toad/go-orchestration-acceptance.sh`;
- `linux/tests/toad/multi-toad-interop.sh`;
- `linux/tests/toad/lib/netns.sh`;
- optionally create a shared fixture library under `linux/tests/toad/lib/`;
- `linux/tests/toad/run-isolated.sh`.

The current new script is not acceptable as evidence even after ShellCheck is fixed.

## 5.1 Do not hand-roll a second protocol fixture

Extract the known-good setup primitives from `multi-toad-interop.sh` into a reusable shell library, for example:

`linux/tests/toad/lib/multi-protocol-fixture.sh`

It should own:

- namespace/veth topology;
- AWG reference configuration;
- Xray REALITY key/server/cover configuration;
- ocserv certificate/password/server configuration;
- valid per-Toad TOML generation;
- server start/stop/wait helpers;
- cleanup;
- common isolation assertions.

Both:

- `multi-toad-interop.sh`;
- `go-orchestration-acceptance.sh`

must consume the same fixture code.

Do not weaken the existing multi-Toad test while extracting it.

## 5.2 Fix core CLI syntax

Correct command shape is:

```text
kikimora-core <command> [options]
```

Use:

```bash
"$CORE_BIN" start --socket "$CORE_SOCKET"
"$CORE_BIN" status --socket "$CORE_SOCKET" --json
"$CORE_BIN" stop --socket "$CORE_SOCKET"
```

Do not use:

```text
kikimora-core --socket ... connect-all
kikimora-core --socket ... get-snapshot
```

There is no `get-snapshot` command.

## 5.3 Fix timeout units

`lib/netns.sh:wait_until` takes milliseconds.

Use:

- 5000 for 5 s;
- 10000 for 10 s;

not `5` or `10`.

Replace fixed `sleep 3/4/5` acceptance waits with predicate polling.

## 5.4 Generate valid Toad TOML

Do not use the current generic writer that produces invalid tables.

AWG must contain its endpoint inside `[awg2]`.

Xray must contain its endpoint inside `[vless_reality]`.

OpenConnect must contain gateway/auth fields inside `[openconnect]`.

For OpenConnect, omit `address` entirely when there is no configured static address. Never emit:

```toml
address = [""]
```

Use the already-valid multi-Toad fixture as the source of truth.

## 5.5 Run the production Go-owned lifecycle

Create a temporary ownership TOML for the fixture:

```toml
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
```

Pass it to core with:

```bash
--ownership-config "$OWNERSHIP_CONFIG"
```

This is required because:

- automatic recovery is enabled from Go ownership;
- persisted desired state is restored on restart only in Go-owned mode.

Without this, Phase B-D/F do not exercise the production contract.

## 5.6 Give every test role a valid publication policy

Roles named `awg`, `xray`, `oc` do not receive the `primary/secondary` default Leshy zone/priority.

Set explicit unique zones and priorities in the generated config.

If the exact policy shape differs from current validation, use `config.Config`, `EffectiveEndpointPolicy()` and existing tests as authority. Do not invent unsupported config.

Use a fixture-local Leshy publication directory or another existing injected bridge if available; do not write host production runtime files from the isolated test.

## 5.7 Strengthen phases A-F

### A — initial connect

Require for **all three** desired roles:

- state Ready;
- route_ready true;
- validated epoch == current underlay epoch;
- endpoint state ready/applied for current epoch;
- publication present;
- parking inactive;
- expected endpoint route/rule ownership present;
- selected IPv4/IPv6 traffic cannot fall through the physical underlay.

### B — underlay change

Capture before:

- epoch;
- all Toad PIDs/generations;
- all TUN ifindices.

Cause one controlled underlay identity change.

Require:

- one canonical new epoch after coalescing;
- affected role is never Ready while parking active;
- fail-closed selected traffic during recovery;
- final Ready only at current epoch;
- unaffected roles keep PID/ifindex if their own recovery is not required.

Do not accept `Degraded` as equivalent to proving fail-closed recovery.

### C — AWG and Xray local-address drift

For each:

- capture ifindex;
- delete expected local address;
- observe route_ready false or repair transition;
- assert no physical selected-traffic leak;
- wait for address restoration;
- assert same ifindex;
- assert Ready/current epoch.

### D — OpenConnect negotiated-address drift

- capture generation/PID;
- remove negotiated address;
- prove old address is not immediately statically injected by Kikimora;
- route_ready must become false;
- fail-closed traffic while recovering;
- transport/full Toad recovery must advance the appropriate identity;
- final session has a negotiated usable address;
- final Ready/current epoch;
- parking inactive only after restoration.

### E — one Toad crash

Before crash capture all three:

- PID/generation;
- ifindex.

Crash exactly one Toad.

Require:

- only that role gets new process/generation;
- other roles keep PID/generation/ifindex;
- failed role selected traffic stays fail-closed;
- all recover to Ready/current epoch.

### F — core restart and persisted desired intent

Before core restart:

1. explicitly disconnect one role through core API;
2. verify desired=false persisted for that role.

Then terminate core and restart using the **same Go ownership config and state dir**.

Require:

- two desired roles are relaunched and Ready;
- deliberately disabled role stays stopped;
- old-generation snapshot cannot overwrite new generation;
- endpoint/routing reconciliation is idempotent;
- parking/checkpoint restoration is safe;
- require exact expected roles, not a loose `ready_count >= 2`.

If testing an actual crash distinct from graceful SIGTERM is required, define it explicitly and ensure orphan Toad cleanup semantics are deterministic. Do not call SIGTERM a crash in the report.

## 5.8 Remove fixed sleeps

Use `wait_until` or a JSON predicate helper for state transitions.

Every wait must have a finite deadline and dump diagnostics on failure.

---

# Phase 6 — complete 07D diagnostics acceptance

Current `kk doctor/debuglog` remains largely legacy-Leshy oriented.

Before real-host staging, diagnostics must include bounded/redacted Go state.

Files:

- `linux/files/kikimora-cli/maintenance.sh`;
- corresponding shell tests;
- package/install tests if new files are added.

Add to `kk debuglog` or `kk diag`:

- orchestration ownership triple;
- `kikimora-core status --json` when API is reachable;
- desired-state file metadata and non-secret intent fields;
- Toad role IDs, generations, states, interface names/ifindices;
- canonical underlay epoch/interface/gateway/source;
- endpoint configured/live/applied epoch;
- parking state;
- Leshy publication;
- IPv4/IPv6 rules/routes and table 51890;
- NetworkManager managed state for `kk-*`;
- core service and Toad process state;
- bounded recent core/Toad logs.

Never dump:

- OpenConnect password/cookie/token secret;
- AWG private/preshared keys;
- other protocol private credential material.

Add a fixture test with sentinel secret strings and assert they are absent from the bundle.

---

# Phase 7 — automated acceptance

## Non-privileged

```bash
cd toad
gofmt -w .
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
cd ..

bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash linux/tests/completions.sh
python3 linux/tests/json_api.py
shellcheck linux/tests/toad/go-orchestration-acceptance.sh
```

## Privileged hermetic

```bash
sudo bash linux/tests/toad/run-isolated.sh route-parking
sudo bash linux/tests/toad/run-isolated.sh multi-toad
sudo bash linux/tests/toad/run-isolated.sh orchestration-acceptance
```

Push after local acceptance. Do not wait for GitHub Actions before reporting completion or moving to the next assigned packet. A reviewer may inspect CI separately.

07C automated proof can be re-closed only after the observer/recovery tests above are real and green locally.

07D automated proof can be re-closed only after `orchestration-acceptance` is actually executed successfully **locally**.

---

# Phase 8 — status update after proof

Only after Phase 7 is green:

- re-audit 06A against the current Xray implementation and recorded `linux-xray-lifecycle` / `linux-xray-interop` results; if its traffic-counter health contract is proven, mark 06A complete instead of reimplementing it;
- revalidate 07A and 07B acceptance on that same green HEAD;
- change 07C status to **automated proof complete; real suspend/resume pending**;
- change 07D status to **privileged hermetic acceptance complete; installed-host cutover pending**;
- record exact CI run IDs and privileged command output;
- point roadmap next to 08A.

Do not run:

- real workstation suspend;
- installed-host cutover;
- `retire-legacy --confirm`.

Those require explicit operator authorization in later packets.

---

# STOP/DESIGN conditions

Stop and write a focused sub-plan if:

1. the existing multi-Toad fixture cannot be safely factored without weakening it;
2. core production recovery requires host-global Leshy files that cannot be isolated in a namespace fixture;
3. an exact Go-owned fixture exposes a missing dependency-injection seam in production code;
4. core crash semantics require systemd/cgroup ownership rather than a namespace process fixture;
5. the real diagnostics contract cannot redact secrets without changing the public API.

Do not paper over any of these with a weaker assertion.

---

# Executor report

Return exactly:

```text
HEAD before:
HEAD after:

Commits:
- ...

Current CI failures fixed:
- Toad core / TestAsyncFullRestartHandoff:
- ShellCheck:

Proof-quality replacements:
- underlay supervisor:
- periodic audit:
- sleep reconnect:
- resume while recovery:
- NetworkManager reconnect:
- observer health:
- pending restart stale timers:

07D privileged gate:
- shared fixture:
- valid TOML:
- Go ownership:
- phases A-F:
- shellcheck:
- exact command/output:

Diagnostics:
- new Go state captured:
- redaction test:

Automated gates:
- go test ./...:
- go test -race ./...:
- go vet ./...:
- service-cutover:
- orchestration-cutover:
- route-parking:
- multi-toad:
- orchestration-acceptance:
- PR CI run IDs:

Manual/operator gates NOT run:
- real suspend/resume
- installed-host cutover
- legacy retirement

Unresolved:
- none OR exact file:function + evidence
```
