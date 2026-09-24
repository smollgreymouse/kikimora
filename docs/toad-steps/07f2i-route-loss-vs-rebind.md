# Toad step 07F.2I — distinguish structural route loss from underlay rebind

Status: **IMPLEMENTED; PRIVILEGED VERIFICATION REQUIRED**.

Privileged baseline exposing this packet:
`f7ac441e3bc683691eb408bb64b907b81bec952b`.

Implementation commit:
`0147ecd` — `fix(toad): distinguish route loss from underlay rebind`.

Purpose: make recovery action selection depend on the failure class, not only on the backend's least-disruptive capability.

## Fresh privileged evidence at f7ac441

The privileged orchestration run reaches and passes Phase C after 07F.2H:

- complete Phase B remains PASS;
- AWG structural address loss is observed;
- AWG recovers with the same ifindex;
- Phase C emits PASS.

The first remaining failure is Phase D OpenConnect negotiated-address drift.

After `kk-oc0` loses its negotiated address, the role remains `route_ready=false` and eventually reaches:

```text
state=Failed
reason=Toad "oc" is not eligible for current-epoch validation
route_ready=false
recovery.step=validate
validated_underlay_epoch=0
```

The OpenConnect child is still connected, but the managed route target is structurally invalid because it has no usable negotiated address.

## Root cause

07F.2G correctly made OpenConnect `Rebindable` for ordinary canonical-underlay changes. That exposed an ambiguity in the generic recovery selector.

Two different events both invalidate `ValidatedEpoch`, but require different actions:

1. **Underlay changed, route target still structurally ready**
   - endpoint host route moves to the new physical underlay;
   - OpenConnect/Xray may use non-destructive `Rebind`.

2. **Managed route target itself is structurally lost**
   - e.g. OpenConnect negotiated address disappears;
   - `Rebind` cannot recreate the missing address;
   - recovery must restart transport while keeping the TUN if supported, otherwise replace the Toad/process.

The old selector saw stale epoch + `Rebind=true` and chose Rebind even for case 2. Recovery then reached Validate with `RouteReady=false`, so validation was ineligible and the role became Failed.

## Fix

`DecideRole` now gives structural route-target loss priority over stale-underlay Rebind whenever the role is Recovering and the live Toad snapshot has `RouteReady=false`.

Selection is now:

```text
structural RouteReady loss
    + RestartTransportKeepingTUN -> ActionRestartTransport
    otherwise                    -> ActionRestartToad

healthy route target + stale underlay
    + Rebind                     -> ActionRebind
```

For OpenConnect negotiated-address loss this selects full Toad/process replacement, allowing the official client to negotiate a fresh address again.

For AWG-like backends that can restart transport while retaining TUN identity, stable-TUN restart remains preferred.

## Deterministic regression

`TestRecoveryDecisionStructuralRouteLossOverridesRebind` proves:

- Rebind-only capability does not win when `RouteReady=false`;
- stable-TUN transport restart wins when available;
- once `RouteReady=true`, stale-underlay recovery may again choose Rebind.

The test is included in `linux/tests/toad/privileged-regressions-model.sh`, so future privileged runs are blocked on this regression being green first.

## Harness noise fix

`mpf_wait_snapshot` polling predicates intentionally fail many times while waiting. Their Python assertion stderr is now suppressed per polling iteration.

A final timeout still prints one explicit Phase failure plus the full snapshot, so diagnostic evidence is preserved without thousands of duplicate traceback lines.

## Non-privileged verification

PASS:

- focused recovery selector regression;
- `privileged-regressions-model.sh`;
- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- `run-rootless.sh model`;
- bash syntax;
- shellcheck;
- `git diff --check`.

## Required privileged verification

Run `bash run-privileged-gates.sh` only after the privileged-derived regression model is green.

Required next evidence:

- retain complete Phase B PASS;
- retain Phase C PASS;
- Phase D must observe OpenConnect route-target loss and recover a negotiated address;
- phases E-F must complete;
- route-parking, multi-toad and xray-interop remain green;
- final cleanup and host-state before/after PASS.

Do not start 08A until the complete 07F.2 privileged suite is green.
