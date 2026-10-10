# Toad 07C/07D executor handoff from HEAD def08ccb

Status: **CURRENT EXECUTOR PLAN**.

Repository: `smollgreymouse/kikimora`  
Branch: `feat/native-core-vpn-clients`  
PR: #27  
Reviewed HEAD: `def08ccb4ae03c84db6cc6125d8b63f9fd160390`  
Reviewed CI: `Toad core` run `35751034945`.

This file is a mechanical implementation handoff. Do not redesign the architecture. Do not reimplement code that is already present. Make the listed changes in order, run the listed gates after each phase, and stop on the explicit STOP conditions.

## What is already implemented and must be preserved

The current branch already contains substantial 07A-07D work. In particular, do **not** rewrite these areas from scratch:

- authoritative Toad generation snapshots and validation tokens;
- process-identity checks around streaming and compatibility `state.json` observations;
- capability-derived recovery selection;
- endpoint desired/current reconciliation;
- IPv4/IPv6 parking and route ownership tracking;
- endpoint-before-transport recovery ordering;
- `ErrRoutesStillParked` retry semantics;
- normalized transport endpoints with hostname+port;
- PreferredSrc derivation and source-address-aware underlay identity;
- `netstate.Compare` canonical epoch logic;
- non-lossy coalescer callback;
- read-only `Manager.Snapshot()`;
- Linux managed-interface repair with ifindex guard;
- OpenConnect static-address repair prohibition;
- native logind D-Bus sleep observation;
- NetworkManager unmanaged enforcement and watcher;
- persisted desired state store and Manager persistence/restore;
- core `--state-dir` support and Go-owned startup restore;
- systemd `StateDirectory=kikimora/core`;
- API-aware orchestration cutover fixture.

Only fix the concrete gaps below.

---

# Phase 0 — restore a green current-HEAD baseline

## 0.1 Fix the current CI formatting failure first

Current `Toad core` run `35751034945` fails before unit tests on all three OSes only because:

`toad/internal/platform/linux/networkmanager/watcher.go`

is not gofmt-clean.

CI shows this exact formatting-only diff:

- align the constant assignments in the `const (...)` block;
- remove the extra blank line before `func (Manager) Watch(...)`.

Do not hand-edit spacing. Run:

```bash
cd toad
gofmt -w internal/platform/linux/networkmanager/watcher.go
test -z "$(gofmt -l .)"
```

Commit this separately, e.g.:

`style(toad): gofmt NetworkManager watcher`

## 0.2 Run the full non-privileged baseline immediately after gofmt

From `toad/`:

```bash
go test ./...
go test -race ./internal/netstate ./internal/toadruntime ./internal/control ./internal/core
go vet ./...
```

Then from repo root:

```bash
bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash linux/tests/completions.sh
python3 linux/tests/json_api.py
```

If any compile/test failure appears after gofmt, fix only the failure caused by the current 07C changes before proceeding.

Do not start new 07C/07D work while this baseline is red.

### Evidence to record

Record exact command, exit code and failing test name if any. Do not write “tests pass” without command output.

---

# Phase 1 — make canonical underlay convergence restartable

Current code points:

- `toad/internal/netstate/coalescer.go`
  - `type Coalescer`
  - `(*Coalescer).Run`
  - `(*Coalescer).publish`
- `toad/internal/control/control.go`
  - `(*Manager).watchUnderlay`
  - `(*Manager).superviseUnderlayWatch`
  - `(*Manager).applyUnderlayChange`
  - `(*Manager).queueUnderlayInvalidation`

## 1.1 Fix silent permanent death of the semantic coalescer

Current defect:

`watchUnderlay` starts:

```go
go func() {
    _ = coalescer.Run(ctx, m.underlayInvalidations, initial)
}()
```

and discards the error.

If `Build()` fails once, `Coalescer.Run` exits forever. The 30-second audit continues writing invalidations, but there is no consumer. Underlay convergence is permanently dead.

### Required change

Replace the fire-and-forget coalescer goroutine with a supervised loop.

Preferred shape in `control.go`:

```go
func (m *Manager) superviseUnderlayCoalescer(ctx context.Context) {
    backoff := &supervisor.Backoff{}
    for {
        if ctx.Err() != nil {
            return
        }

        m.mu.Lock()
        initial := m.underlay
        m.mu.Unlock()

        c := &netstate.Coalescer{
            Settle:  250 * time.Millisecond,
            Maximum: 2 * time.Second,
            Build:   m.buildUnderlaySnapshot,
            Changed: func(change netstate.Change) error {
                m.applyUnderlayChange(change)
                return nil
            },
        }

        err := c.Run(ctx, m.underlayInvalidations, initial)
        if ctx.Err() != nil {
            return
        }

        // Record observer degradation. Do not silently exit.
        m.setObserverHealth("netlink", false, fmt.Errorf("underlay coalescer: %w", err))

        delay := backoff.Next()
        timer := time.NewTimer(delay)
        select {
        case <-ctx.Done():
            timer.Stop()
            return
        case <-timer.C:
        }
    }
}
```

Then `watchUnderlay` must:

- start `superviseUnderlayCoalescer(ctx)`;
- start `superviseUnderlayWatch(ctx)`;
- keep the 30-second audit ticker alive independently;
- no longer capture one stale `initial` forever.

Do not combine raw netlink watcher supervision and semantic coalescer supervision into one goroutine.

## 1.2 Match the 07C debounce contract

Current Manager values are 150ms / 1s.

Change to:

- Settle: 250ms;
- Maximum: 2s.

Keep the defaults in `netstate.Coalescer` sensible, but Manager must explicitly use 250ms/2s.

## 1.3 Handle a closed invalidation channel

In `Coalescer.Run`, replace:

```go
case invalidation := <-invalidations:
```

with:

```go
case invalidation, ok := <-invalidations:
    if !ok {
        return nil
    }
```

Do not spin on zero-value invalidations after channel close.

## 1.4 Tests for Phase 1

Add/extend `toad/internal/netstate/coalescer_test.go`:

1. burst of raw invalidations -> one semantic callback;
2. resume with unchanged identity -> same epoch + `ChangeResumeValidation`;
3. closed invalidation channel -> `Run` exits;
4. callback error -> `Run` returns the error.

Add control-level test(s), preferably in `toad/internal/control/control_test.go`, for:

5. first underlay build fails, supervised coalescer restarts, next invalidation is processed;
6. after restart, canonical epoch starts from current `m.underlay`, not from the stale snapshot captured before the failure;
7. periodic audit remains alive while netlink watcher/coalescer is degraded.

If hard-coded platform builders make this impossible, add a narrow test seam:

```go
underlayBuilder func(context.Context) (netstate.Snapshot, error)
```

Initialize it to the production builder in `newManager`; use it only as dependency injection, not as a new architecture layer.

---

# Phase 2 — finish route-target drift recovery and async full-restart handoff

Current code points:

- `toad/internal/control/control.go`
  - `type role`: `validationInFlight`, `recoveryInFlight`, `everRouteReady`;
  - `observeToadSnapshotForProcess`;
  - `observeCompatibilityStateForProcess`;
  - `scheduleRouteTargetRecovery`;
  - `scheduleValidation`;
  - `recoverRole`;
  - `restartRole`;
- `toad/internal/control/recovery_driver.go`
  - `(*recoveryDriver).StartTransport`;
- `toad/internal/core/recovery.go`
  - `ErrToadRestartPending`;
- `toad/internal/core/engine.go`
  - `Engine.Recover`;
- `toad/internal/toadruntime/runtime.go`
  - `Validate`;
  - `RunHealthLoop`.

## 2.1 Preserve the current intended transition

The intended path is now:

```text
previously RouteReady
    -> RouteReady=false
    -> scheduleRouteTargetRecovery exactly once
    -> park/withdraw/quiesce/apply endpoint
    -> stable transport restart, OR full Toad replacement
```

For a full replacement:

```text
StartTransport
    -> restartRole()
    -> ErrToadRestartPending
    -> Engine leaves product RoleStarting
    -> parking remains active
    -> replacement process publishes RouteReady
    -> scheduleValidation()
    -> ValidateRole()
    -> activateReadyRole()
    -> endpoint/publish/resync/restoration
    -> parking inactive
```

Do not change this ordering.

## 2.2 Add a bounded escape from `recoveryInFlight=true`

Current defect:

When `recoverRole` gets `core.ErrToadRestartPending`, it intentionally leaves `recoveryInFlight=true` until a replacement RouteReady snapshot arrives.

If the replacement process stays alive but never becomes RouteReady, the role can remain blocked forever because repeated bad snapshots are ignored by `scheduleRouteTargetRecovery`.

### Required change

Add a dedicated bounded retry for pending full restarts.

Do **not** reuse the parked-route retry predicate blindly because that predicate requires `RoleRecovering`, while async restart intentionally exposes `RoleStarting`.

Add Manager state, e.g.:

```go
restartRetryPending map[string]bool
```

Initialize it in `newManager`.

Add helper:

```go
func (m *Manager) schedulePendingRestartRetry(
    driver core.RecoveryDriver,
    name string,
    process Process,
    operation uint64,
    epoch uint64,
)
```

Required timer callback checks, under `m.mu`:

- role still exists;
- role is still desired/enabled;
- `current.process == process` (never retry an old process);
- current underlay epoch still equals captured `epoch`;
- `current.recoveryInFlight == true`;
- if `current.stateValid && current.observed.RouteReady`, clear pending and return;
- otherwise keep fail-closed state and start another bounded recovery attempt for the **current** product operation.

Use existing `supervisor.Backoff`; do not busy-loop.

Clear the pending-restart retry state when:

- replacement RouteReady enters `scheduleValidation`;
- role is disconnected;
- role process changes again;
- recovery succeeds;
- fatal recovery error is recorded.

## 2.3 Do not regress initial startup

`everRouteReady` is intentional.

A Toad that has never once been RouteReady must **not** be treated as “route-target drift” and immediately restarted just because its first snapshots are not ready.

Keep this predicate in `scheduleRouteTargetRecovery`:

```go
!r.everRouteReady
```

as a suppression condition.

## 2.4 OpenConnect address loss must trigger transport recovery, never address injection

Preserve current runtime rule in `RunHealthLoop`:

- OpenConnect negotiated address loss => `RouteReady=false`;
- reason => `OpenConnect negotiated interface drift requires transport recovery`;
- do not call Linux interface repairer;
- do not netlink-add old negotiated address.

The control plane must then use `scheduleRouteTargetRecovery`.

Do not add an OpenConnect-specific restart command outside the capability/recovery path.

## 2.5 Deterministic tests for Phase 2

Add tests that prove all of these exact transitions.

### In `control_test.go`

1. **initial-not-ready does not recover**
   - enabled live process;
   - `everRouteReady=false`;
   - observe `RouteReady=false`;
   - zero recovery calls.

2. **ready-to-not-ready recovers once**
   - first accepted snapshot RouteReady=true;
   - next accepted snapshot RouteReady=false;
   - five repeated false snapshots;
   - exactly one recovery transaction while `recoveryInFlight=true`.

3. **stale process cannot trigger/finish drift recovery**
   - replace process;
   - old process publishes bad snapshot;
   - no recovery state mutation.

4. **async full restart handoff**
   - driver forces `ErrToadRestartPending`;
   - product state is Starting, not Failed/Ready;
   - parking remains active;
   - no Publish/ObserveRestoration after StartTransport;
   - replacement RouteReady snapshot enters normal validation path.

5. **replacement never ready**
   - pending-restart timer fires;
   - bounded second attempt occurs;
   - no permanent stuck `recoveryInFlight=true`;
   - no tight loop.

6. **replacement ready cancels pending retry**
   - pending timer exists;
   - replacement RouteReady arrives before timer;
   - validation starts;
   - timer does not restart the new healthy process.

### In `toadruntime/runtime_test.go`

Keep and run:

- same-ifindex AWG repair;
- repair-rate limiting;
- replacement-ifindex rejection;
- OpenConnect negotiated address is not handed to repairer.

Add explicit assertion that OpenConnect drift snapshot is `RouteReady=false`.

---

# Phase 3 — finish 07C sleep/resume and NetworkManager deterministic coverage

Current code already has:

- native logind D-Bus source;
- watcher retry loop in Manager;
- NetworkManager `SetManaged` + writable-property fallback;
- NameOwnerChanged/DeviceAdded watcher;
- Manager `watchManagedInterfaceOwnership`;
- packaged `90-kikimora-unmanaged.conf`.

Do not replace these.

## 3.1 Make sleep source injectable for deterministic reconnect tests

Current `watchSleep` calls `platform.DefaultSleepSource()` internally, which prevents a hermetic reconnect test.

Add Manager field:

```go
sleepSource platform.SleepSource
```

Initialize it once in `newManager`:

```go
sleepSource: platform.DefaultSleepSource(),
```

Then `watchSleep` reads `m.sleepSource`.

Do not add public API solely for this; tests in package `control` can replace the field directly.

## 3.2 Add sleep watcher reconnect test

Fake sleep source behavior:

- first `Watch` call returns a retryable error;
- second call remains live and emits `Preparing=true`, then `Preparing=false`.

Assert:

- Manager marks sleep observer degraded, then healthy again;
- suspend does not call DisconnectRole/DisconnectAll;
- desired bits remain unchanged;
- resume queues canonical underlay convergence;
- validation invalidation occurs only after the fresh resumed underlay snapshot is accepted.

Use short injected backoff only if you add a Manager-local retry-delay seam. Do not mutate global `supervisor.RetryDelays`; that previously caused a race under `go test -race`.

## 3.3 Add resume-while-recovery-in-flight test

Scenario:

- role already Recovering;
- receive suspend/resume;
- multiple netlink/NM/resume invalidations occur;
- canonical underlay settles to one current epoch;
- no duplicate concurrent recovery transaction;
- final validation, if successful, is bound to the current epoch only.

## 3.4 NetworkManager ownership tests

Using a fake implementing both:

- `platform.ManagedInterfaceVerifier`;
- `platform.ManagedInterfaceWatcher`.

Cover:

1. managed `kk-*` => role blocked/Recovering;
2. watcher invalidation + verifier now unmanaged => current process gets validation request;
3. watcher exits => Manager reconnects with bounded backoff;
4. no configured external `vpn0` is touched;
5. repeated events do not create validation/recovery storms.

## 3.5 Package rule verification

Assert in shell/package tests that installed file is exactly:

`/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf`

with:

```ini
[keyfile]
unmanaged-devices=interface-name:kk-*
```

If this is already covered, leave implementation unchanged and record the existing test name.

---

# Phase 4 — close 07C with tests, not assumptions

Run:

```bash
cd toad
go test ./internal/netstate ./internal/platform/... ./internal/underlay ./internal/toadruntime ./internal/control ./internal/core
go test -race ./internal/netstate ./internal/toadruntime ./internal/control ./internal/core
go test ./...
go vet ./...
cd ..
sudo bash linux/tests/toad/run-isolated.sh route-parking
sudo bash linux/tests/toad/run-isolated.sh multi-toad
```

Also rerun full PR CI.

### Status rule

07C may be marked code/CI complete only if all automated gates above are green.

Do **not** automatically suspend the developer workstation.

The real suspend/resume gate remains operator/manual. Report:

`07C automated gates green; real suspend/resume manual gate pending`

unless the user explicitly asks to execute a real suspend cycle.

---

# Phase 5 — audit 07D existing implementation before adding code

07D is already substantially implemented. Verify first; do not rewrite.

Existing areas to preserve:

- `toad/internal/control/desired_state.go`;
- `desired_state_test.go`;
- Manager persistence rollback on Save failure;
- `ShutdownProduct` semantics;
- `RestoreDesiredState`;
- `kikimora-core serve --state-dir`;
- Go-owned startup restore in `cmd/kikimora-core/main.go`;
- `linux/files/kikimora-core.service` with:
  - `StateDirectory=kikimora/core`;
  - `--state-dir /var/lib/kikimora/core`;
- API-aware `linux/tests/toad/orchestration-cutover.sh`;
- read-only preflight;
- first migration `core start --socket ...`;
- already-Go idempotence;
- rollback ordering tests.

## 5.1 Verify shutdown path in the real daemon

In `toad/cmd/kikimora-core/main.go`, confirm daemon exit ultimately performs `ShutdownProduct`, not an intent-clearing `DisconnectAll`.

If `defer manager.Close()` is the path, inspect `Manager.Close()` and make it call product shutdown semantics only.

Add a command-level or Manager-level regression if not already present.

## 5.2 Verify startup missing/corrupt desired-state behavior

Required:

- Go-owned + missing desired file => all roles disabled + explicit diagnostic reason;
- Go-owned + corrupt desired file => startup error;
- non-Go ownership => persisted desired roles are not started.

Add tests if any branch is absent.

## 5.3 Complete orchestration fixture cases only if missing

`linux/tests/toad/orchestration-cutover.sh` already covers much of 07D.

Verify these cases are actually present and passing:

- happy path waits for API and Ready;
- systemd start failure => rollback;
- service active but API absent => rollback;
- one role Failed => rollback;
- never Ready => timeout/rollback;
- NetworkManager preflight blocker => **no ownership/service mutation**;
- already-Go cutover does not ConnectAll again;
- rollback stops Go roles before restoring legacy ownership.

Add only missing cases.

---

# Phase 6 — implement the missing privileged 07D orchestration gate

This is currently absent.

Create:

`linux/tests/toad/go-orchestration-acceptance.sh`

and add mode:

`orchestration-acceptance`

to:

`linux/tests/toad/run-isolated.sh`.

Do not use the public Internet.

Reuse the same private namespace/reference fixtures as the existing AWG2/Xray/OpenConnect/multi-Toad tests.

## Required phases in `go-orchestration-acceptance.sh`

### A. Initial connect

Assert:

- core controls all desired roles;
- every desired role is Ready;
- `validated_underlay_epoch == underlay.epoch`;
- route target ready;
- endpoint applied at current epoch;
- Leshy publication present;
- parking inactive;
- selected IPv4 and IPv6 cannot leak to physical default.

### B. Underlay change/recovery

Mutate the private underlay.

Assert:

- one canonical epoch transition after coalescing;
- affected role enters fail-closed recovery;
- endpoint policy updates before transport action;
- role is never Ready while parking is active;
- independent roles keep their PID/ifindex unless their own transport requires change.

### C. AWG/Xray address drift

Delete one configured local address.

Assert:

- RouteReady drops;
- repair occurs;
- ifindex remains identical;
- selected traffic does not leak.

### D. OpenConnect negotiated-address drift

Remove negotiated address.

Assert:

- no static address injection;
- RouteReady=false;
- control recovery/full replacement starts;
- restored session eventually reaches Ready/current epoch;
- parking remains fail-closed until restoration.

### E. Toad crash

Kill one Toad.

Assert:

- only that role restarts;
- other roles keep PID/ifindex;
- selected traffic for failed role remains fail-closed.

### F. Core crash/restart

Assert:

- persisted desired state relaunches intended roles;
- explicitly disabled role stays disabled;
- stale previous generation is rejected;
- parking/checkpoint restoration is idempotent.

Do not weaken existing interop assertions to make this pass.

---

# Phase 7 — final status/roadmap update rules

Only after Phase 0-6:

1. update `docs/toad-steps/07a-authoritative-state-and-capabilities.md` status from pending/current to complete **only if current HEAD tests still prove its acceptance**;
2. same for 07B;
3. 07C:
   - mark automated implementation complete when automated gates are green;
   - explicitly leave manual suspend/resume pending if not performed;
4. 07D:
   - do not mark production cutover complete without the privileged orchestration gate;
   - real installed-host cutover remains operator/manual;
5. update `docs/toad-roadmap.md` with exact CI run IDs and remaining manual gates.

Do not mark legacy retirement complete.

Do not run `retire-legacy --confirm`.

---

# Expected commit sequence

Use small commits in this order:

1. `style(toad): gofmt NetworkManager watcher`
2. `fix(control): supervise underlay coalescer failures`
3. `test(netstate): cover coalescer restart and closed input`
4. `fix(control): bound asynchronous Toad restart recovery`
5. `test(control): cover route-target drift restart handoff`
6. `test(control): cover sleep and NetworkManager reconnection`
7. `test(orchestration): add privileged Go recovery acceptance`
8. `docs(toad): record 07C/07D acceptance state`

Combine 2+3 or 4+5 only if the implementation and its test are inseparable.

---

# Final executor report format

Return exactly:

```text
HEAD before:
HEAD after:

Commits:
- <sha> <subject>

Phase 0 baseline:
- gofmt:
- go test ./...:
- go test -race ...:
- go vet:
- shell fixtures:

07C:
- coalescer supervision:
- source/epoch tests:
- route-target recovery:
- async restart timeout:
- OpenConnect drift:
- AWG/Xray repair:
- NetworkManager:
- sleep/resume:
- route-parking gate:
- multi-toad gate:
- current CI run:

07D:
- desired state:
- shutdown/restart:
- orchestration fixture:
- orchestration-acceptance gate:
- current CI run:

Manual gates NOT run:
- real suspend/resume:
- installed-host cutover:
- legacy retirement:

Unresolved:
- <none or exact file:function + failure>
```

If a STOP condition is reached, do not improvise. Commit only safe completed work, write a focused markdown sub-plan under `docs/toad-steps/`, and return its path.
