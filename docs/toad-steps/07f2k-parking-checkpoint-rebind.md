# Toad step 07F.2K — rebind stale parking checkpoints across core restart

Status: **IMPLEMENTED; PHASE F PRODUCT STATE PRIVILEGED-VERIFIED AT 6b43e91**.

Privileged baseline exposing this packet:
`df838db338f181fe2a60bad08127ea67c6a15e6f`.

Implementation commit:
`ccbfe4a` — `fix(toad): rebind stale parking checkpoints safely`.

Purpose: make parking/checkpoint crash recovery safe when a restarted core launches replacement Toads with new interface ifindexes.

## Fresh privileged evidence at df838db

The authoritative run at approximately 21:13-21:15 +03:00 produced:

- build-only PASS;
- route-parking PASS;
- multi-toad PASS;
- xray-interop PASS;
- both routed-endpoint preflights PASS;
- Phase A PASS;
- complete Phase B PASS;
- Phase C PASS;
- Phase D PASS;
- Phase E PASS.

This privileged run therefore verifies 07F.2J: after deliberate Xray Toad SIGKILL, the replacement generation performs fresh validation and returns product Ready/current epoch.

The first remaining failure is Phase F after deliberate core restart with persisted desired state.

Desired intent itself is restored correctly:

- AWG desired=true;
- Xray desired=true;
- OpenConnect desired=false and Stopped.

But the new core launches replacement AWG/Xray Toads with new ifindexes. Recovery then fails in `ObserveRoutes` with:

```text
stale parking checkpoint identity for role "awg"
stale parking checkpoint identity for role "xray"
```

AWG retries this path repeatedly while Xray becomes Failed.

## Root cause

The parking checkpoint persists two fundamentally different kinds of evidence:

1. **ownership evidence** (`Baseline` / `Observed`), which is tied to one concrete route-target interface + ifindex and must never be transferred to a replacement interface;
2. **fail-closed park evidence** (`Parked`), which represents verified kernel unreachable/park routes by destination prefix and must survive a core crash until a real route returns.

The old restore path treated any interface/ifindex mismatch as a terminal stale-checkpoint error. That is too strict after a normal core restart because replacement Toad interfaces legitimately get new ifindexes.

Simply deleting the checkpoint would also be wrong: if the core crashed while routes were actually parked, dropping `Parked` state could lose fail-closed ownership and strand or leak kernel state.

## Fix

`restoreOwnershipCheckpoint` now separates these two evidence classes.

For the same role with a changed interface/ifindex:

- restore `Parked` only from routes that are still actually present in the kernel;
- discard stale `Baseline` and `Observed` ownership evidence;
- clear the in-memory ownership registry for the role;
- mark ownership state loaded so old ifindex evidence is not rediscovered;
- rewrite the checkpoint against the current route-target identity, preserving only verified parked prefixes.

A checkpoint whose `Role` itself does not match remains a hard error.

`Park` also recognizes an already-restored active park. It does not overwrite that fail-closed state with an empty ownership set merely because the replacement Toad has not yet rebuilt current-interface ownership.

Later, when a real route for a parked prefix appears again, normal kernel read-back releases the park.

## Privileged-derived regressions

`TestRecoveryCheckpointRebindsStaleIdentityWithoutTransferringOwnership` proves:

- old `Baseline`/`Observed` entries from ifindex N do not cross to ifindex N+1;
- the checkpoint is rebound to the current interface identity;
- an inactive stale checkpoint becomes a clean current-identity checkpoint.

`TestRecoveryCheckpointPreservesVerifiedParkAcrossIdentityChange` proves:

- a real kernel park survives stale checkpoint identity;
- entering `Park` again does not clear the restored active park;
- the rewritten checkpoint preserves the parked prefix on the new identity while dropping stale ownership evidence;
- the park is released only after a real current-interface route for the prefix returns.

Both tests are included in `linux/tests/toad/privileged-regressions-model.sh`.

## Non-privileged verification at ccbfe4a

PASS:

- both focused checkpoint restart regressions;
- privileged-regressions-model.sh;
- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- packaging;
- `run-rootless.sh model`;
- bash syntax;
- shellcheck;
- `git diff --check`.

Current post-run inspection also shows no named network namespaces left behind.

## Privileged result and next packet

Fresh evidence at `6b43e91` reaches the Phase F post-restart snapshot with AWG/Xray Ready on replacement interfaces, OpenConnect desired=false/Stopped, aggregate Ready, and no stale parking checkpoint errors. The privileged runner also reports host state unchanged.

The product behavior required by this packet is therefore verified. The gate remains red only because the Phase F embedded Python predicate used lowercase `false` identifiers and raised `NameError` before evaluating the already-correct snapshot. That independent harness defect is tracked in `07f2l-orchestration-predicate-validation.md`.

07F.2 as a whole remains open until one final privileged run emits an actually green orchestration A-F result.
