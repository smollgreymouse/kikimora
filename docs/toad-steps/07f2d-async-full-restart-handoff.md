# Toad step 07F.2D — async full-process restart hand-off

Status: **IMPLEMENTED LOCALLY; PRIVILEGED VERIFICATION REQUIRED**.

Privileged baseline exposing this packet:
`873ae7f1f2bf2ee841c5d421d0a09d4bcd79a230`.

Implementation commit:
`1ff2c18` — `fix(toad): hand off async full restart startup`.

Purpose: close the new Phase B blocker exposed only after the 07F.2C
stable-AWG recovery hand-off became correct.

## Fresh privileged evidence at 873ae7f

The operator ran:

```bash
bash run-privileged-gates.sh
```

Fresh artifacts were written at approximately 16:02-16:04 +03:00 on
2026-09-24.

Per-gate evidence:

- route-parking: PASS;
- multi-toad: PASS;
- xray-interop: PASS;
- orchestration Phase 3: PASS;
- orchestration Phase A: PASS;
- Phase 6 AWG endpoint non-recursion: PASS;
- Phase B primary -> backup epoch 1 -> 2: PASS;
- Phase B AWG outage semantics: PASS:
  - AWG remained non-terminal Recovering;
  - `route_ready=true`;
  - `validated_underlay_epoch=0`;
  - publication withdrawn;
  - `10.77.0.1` remained routed through `kk-awg0`;
  - payload was unreachable while the physical AWG endpoint path was down;
  - no fallback to the Xray physical underlay;
- Phase B primary restoration advanced the canonical epoch to 3;
- AWG itself recovered to Ready, `validated_underlay_epoch=3`, publication
  restored and stable TUN identity preserved;
- orchestration still FAILED before Phase C because OpenConnect did not complete
  its full-process replacement hand-off.

This means 07F.2C fixed the defect it was designed to fix. The remaining Phase B
failure is a separate full-process restart coordination bug.

## New blocker

Final failing OpenConnect state from the fresh orchestration log:

```text
state=Starting
route_ready=false
operation=2
validated_underlay_epoch=0
recovery.step=quiesce
recovery.operation=1
recovery.epoch=2
recovery.attempt=2
recovery.last_error=dial unix .../oc-state/control.sock: connect: no such file or directory
```

Before that terminal/transient sequence, OpenConnect spends time in the expected
`Starting` state while its replacement process is establishing the official
OpenConnect session and negotiated TUN.

## Root cause

For protocols without `Rebind` or `RestartTransportKeepingTUN`, recovery
selects a full Toad process replacement.

The first recovery transaction correctly:

1. observes and parks owned routes;
2. withdraws publication;
3. quiesces the old process;
4. applies endpoint policy;
5. starts a full process replacement;
6. returns `ErrToadRestartPending`.

The old manager logic then kept `recoveryInFlight=true` and armed
`schedulePendingRestartRetry` using the ordinary one-second recovery backoff.

That retry was architecturally wrong for an asynchronous replacement:

- `kikimora-toad` does not publish its control socket until
  `Runtime.Start` completes;
- OpenConnect startup legitimately needs time to create and configure the
  negotiated TUN;
- `Runtime.Start` already has bounded interface readiness
  (30 seconds for OpenConnect, 3 seconds for other protocols);
- if startup fails and the process exits, `Manager.wait/retryAfter` already
  owns process-level bounded retry.

The one-second pending-restart timer therefore launched a *second*
`Engine.Recover` against a replacement whose control socket was intentionally
not ready yet. The second transaction reached `Quiesce`, failed to dial
`control.sock`, and could move the role through Failed/Starting churn.

This duplicated two retry owners:

```text
recovery engine
    -> launch replacement
    -> pending-restart timer
       -> recovery engine again
          -> Quiesce replacement before control.sock exists   [wrong]

replacement runtime
    -> bounded startup / interface readiness
    -> process exits on startup failure
    -> Manager.wait/retryAfter                                [already correct]
```

## Fix

The full-process replacement boundary is now an explicit hand-off.

Manager state:

```text
restartHandoffPending[role] = true
recoveryInFlight = false
```

after `ErrToadRestartPending`.

Rules:

- the old recovery worker no longer owns the role after replacement launch;
- no recovery timer replays `Engine.Recover` against the starting replacement;
- route-target recovery is suppressed while the replacement hand-off is
  pending;
- a replacement RouteReady snapshot clears the hand-off and enters the ordinary
  generation/epoch-bound validation + activation pipeline;
- a replacement process exit clears the hand-off and is retried by the existing
  process supervisor;
- deliberate stop/start clears stale hand-off state;
- underlay may advance while the replacement is starting; when the replacement
  becomes RouteReady it validates against the *current* canonical underlay
  rather than replaying an old epoch-2 recovery transaction.

No protocol-specific OpenConnect exception was added to core recovery ordering.
The fix is protocol-neutral for all full-process replacement backends.

## Safety properties preserved

This change does not:

- weaken AWG recent-handshake validation;
- change AWG freshness windows;
- change structural `route_ready` semantics;
- replace any real protocol fixture with a mock;
- weaken fail-closed routing assertions;
- make rootless kernel modes authoritative;
- start 08A.

Parking/publication withdrawal performed before full restart remains in place
until the replacement passes the normal validation/activation path.

## Deterministic coverage

Updated/additional tests prove:

- route-target recovery runs one disruptive transaction before full restart;
- `ErrToadRestartPending` releases `recoveryInFlight` and arms the explicit
  replacement hand-off;
- RouteReady=false observations during replacement startup do not replay the
  recovery transaction;
- a replacement RouteReady generation clears the hand-off and enters normal
  validation;
- process exit clears hand-off state so ordinary supervisor retry owns the next
  start;
- the previous stable-transport `ErrValidationPending` hand-off remains
  unchanged.

## Non-privileged verification at 1ff2c18

PASS:

```text
gofmt
go test ./...
go test -race ./...
go vet ./...
bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash desktop/tests/test_packaging.sh
bash linux/tests/toad/run-rootless.sh model
bash -n ...
shellcheck -S warning ...
git diff --check
```

A first full Go test run also hit the existing sleep-watcher timing test once;
the test passed immediately in isolation and the subsequent full suite and race
suite were green. It is not evidence for the full-restart defect.

## Required privileged verification

Run only:

```bash
bash run-privileged-gates.sh
```

Required closure evidence:

- route-parking PASS;
- multi-toad PASS;
- orchestration Phase A-F PASS;
- Xray interop PASS;
- Phase B still proves the 07F.2C AWG fail-closed behavior;
- OpenConnect replacement reaches Ready/current epoch without recovery replay;
- final fixture cleanup PASS;
- host-state before/after PASS.

Do not start 08A until this entire suite is green.
