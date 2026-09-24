# Toad step 07F.2J — invalidate validation across Toad generations

Status: **IMPLEMENTED; PRIVILEGED VERIFICATION REQUIRED**.

Privileged baseline exposing this packet:
`287335d00912be2db7ff98e41a6948ddb58b3ad1`.

Implementation commit:
`42adcd1` — `fix(toad): invalidate validation on Toad replacement`.

Purpose: ensure a restarted/replaced Toad process cannot inherit Ready authority that was validated for the process generation it replaced.

## Fresh privileged evidence at 287335d

The authoritative run at approximately 20:48-20:50 +03:00 produced:

- build-only PASS;
- route-parking PASS;
- multi-toad PASS;
- xray-interop PASS;
- both routed endpoint preflights PASS;
- Phase A PASS;
- complete Phase B PASS;
- Phase C PASS;
- Phase D PASS.

This privileged run therefore verifies 07F.2I: OpenConnect negotiated-address loss now selects real route-target recovery and returns Ready/current epoch.

The first remaining failure is Phase E after the test deliberately SIGKILLs the Xray Toad process.

Final replacement evidence:

```text
xray state=Starting
route_ready=true
ifindex=6
validated_underlay_epoch=3
validation.healthy=true
validation.state=ready
reason=Xray managed TUN is up; tunneled session not yet proven
```

The replacement process and Xray-owned TUN are alive. The failure is authoritative-state handoff, not failure to restart the process.

## Root cause

`BeginToadGeneration` correctly cleared the old Toad snapshot/generation and moved the role to Starting, but it left:

- `ValidatedEpoch` from the old process generation;
- the old `Validation` result.

Validation tokens are explicitly bound to operation + Toad generation + underlay epoch. Therefore Ready authority from generation N must never survive replacement by generation N+1, even if operation and underlay epoch did not change.

Because the old `ValidatedEpoch` still equalled current underlay epoch 3, the replacement's first `RouteReady=true` snapshot did not schedule fresh validation. Product state remained Starting forever while carrying stale current-epoch validation metadata.

## Xray session semantics

This is not fixed by weakening Xray's traffic-counter health semantics.

The existing contract intentionally distinguishes:

- `RouteReady`: structurally valid managed TUN;
- `SessionConnected`: real tunneled traffic proof;
- product `RoleReady`: current-generation structural validation for the current underlay epoch.

Xray's validator may validate a running official Xray instance + structurally ready managed TUN without requiring nonzero traffic counters. SessionConnected may remain false until on-demand traffic occurs.

Therefore Phase E needs fresh generation-bound validation, not an artificial payload requirement and not a false-online relaxation.

## Fix

`Controller.BeginToadGeneration` now clears:

```text
ToadGeneration = 0
Toad = zero snapshot
ValidatedEpoch = 0
Validation = zero result
State = Starting   (when desired)
```

The first accepted replacement snapshot with `RouteReady=true` is therefore eligible for a new `BeginValidation`, producing a token bound to the replacement generation and current underlay epoch.

## Privileged-derived regression

`TestBeginToadGenerationInvalidatesCurrentValidation` proves:

1. generation 10 becomes Ready only after successful current-generation validation;
2. `BeginToadGeneration` removes generation-10 validation authority;
3. the role is Starting with `ValidatedEpoch=0` and no current Validation result;
4. a generation-11 `RouteReady=true` snapshot is accepted;
5. fresh validation can begin and its token is bound to generation 11 and the current epoch.

The test is included in `linux/tests/toad/privileged-regressions-model.sh`.

## Non-privileged verification at 42adcd1

PASS:

- focused generation-boundary regression;
- privileged-regressions-model.sh;
- `go test ./...`;
- `go test -race ./...` on repeat;
- `go vet ./...`;
- service-cutover;
- orchestration-cutover;
- packaging;
- `run-rootless.sh model`;
- bash syntax;
- shellcheck;
- `git diff --check`.

One first full race run hit two existing timing-sensitive control tests; each passed 20 consecutive isolated race runs and the immediately repeated full race suite passed. No production change was made for those transient test timings.

## Required privileged verification

Run `bash run-privileged-gates.sh` only after the privileged-derived regression model is green.

Required next evidence:

- retain Phases B-D PASS;
- Phase E Xray Toad SIGKILL must restart a new generation;
- the replacement must undergo fresh generation-bound validation and reach product Ready/current epoch;
- unrelated AWG/OpenConnect roles remain Ready and keep identity;
- Phase F completes persisted desired-state/core restart acceptance;
- route-parking, multi-toad and xray-interop remain green;
- final cleanup and host-state checks PASS.

Do not start 08A until the complete 07F.2 privileged suite is green.
