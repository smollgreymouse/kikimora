# Toad step 07F.2G — preserve OpenConnect TUN across underlay rebind

Status: **IMPLEMENTED; OPENCONNECT TUN IDENTITY PRIVILEGED-VERIFIED AT e49e039**.

Privileged baseline exposing this packet:
`91c642ab329f3ae3ebc5a6a06b1aba903180849d`.

Implementation commit:
`9dd4be1` — `fix(toad): preserve OpenConnect TUN across underlay rebind`.

Purpose: prevent ordinary canonical-underlay changes from forcing a full OpenConnect Toad restart and recreating the child-owned managed TUN.

## Fresh privileged evidence at 91c642a

The authoritative privileged run at approximately 17:39-17:40 +03:00 produced:

- build-only PASS;
- route-parking PASS;
- multi-toad PASS;
- xray-interop PASS;
- `Xray endpoint routed-underlay fixture: primary PASS`;
- `OpenConnect endpoint routed-underlay fixture: primary+backup PASS`;
- orchestration Phase 3 PASS;
- Phase A PASS;
- Phase 6 AWG endpoint non-recursion PASS;
- Phase B canonical epoch transition PASS;
- Phase B AWG fail-closed proof PASS;
- AWG recovered at epoch 3;
- the Xray identity assertion now passes.

The first remaining failure is:

```text
Phase B: unrelated OpenConnect TUN identity changed: 4 -> 6
```

Because the Xray identity assertion runs before the OpenConnect identity assertion, this fresh run verifies the 07F.2F Xray Rebind fix.

## Existing OpenConnect contract

OpenConnect's separately proven transport-loss semantics already require the official client to remain in its reconnect path and keep the same `kk-oc0` ifindex across ordinary physical-underlay loss. Only a terminal server-requested disconnect or authentication-state reset permits child/TUN teardown.

Therefore the Phase B identity assertion is correct; the failure is a capability-selection gap, not a reason to weaken the test.

## Root cause

Before this packet the OpenConnect backend implemented `backend.Backend`, `backend.Validator`, and `backend.EndpointReporter`, but did not implement `backend.Rebindable` or `backend.TransportRestarter`.

On a changed underlay epoch the protocol-neutral selector therefore chose `ActionRestartToad`. A full Toad replacement terminates the official OpenConnect child, whose owned TUN disappears and is recreated with a new ifindex.

That is too disruptive for an ordinary underlay handoff.

## Correct ownership

Core already owns transport endpoint placement:

```text
underlay changes
    -> core ApplyEndpoint
    -> endpoint host route moves to selected physical underlay
    -> official OpenConnect child remains alive
    -> OpenConnect reconnect loop follows the new kernel route
```

The routed 07F.2E fixture already proves the real OpenConnect endpoint is reachable through both synthetic underlays, so no new routing exception is required.

## Fix

`backend/openconnect.Backend` now implements `backend.Rebindable`.

`Rebind`:

- honors context cancellation;
- does not stop/relaunch the OpenConnect child;
- does not recreate `kk-oc0`;
- does not duplicate endpoint-routing ownership;
- acknowledges the already-applied core endpoint-policy handoff.

Runtime capability discovery therefore exposes `Capabilities.Rebind=true`, and ordinary canonical-underlay changes select `ActionRebind` instead of `ActionRestartToad`.

## Deterministic verification at 9dd4be1

PASS:

- gofmt;
- targeted OpenConnect/runtime/core/control tests;
- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- service-cutover;
- orchestration-cutover;
- packaging;
- rootless model;
- bash syntax;
- shellcheck;
- `git diff --check`.

## Privileged result and next packet

Fresh evidence at `e49e039` completes Phase B: both routed-endpoint preflights pass, AWG remains fail-closed/recoverable, Xray retains its TUN identity, OpenConnect retains its TUN identity, and `Phase B PASS` is emitted.

The first subsequent failure is Phase C structural address drift visibility: after removing the AWG address, runtime repairs the interface in the same health tick and never publishes an observable `route_ready=false` snapshot. That independent ordering defect is tracked in `07f2h-structural-drift-publication.md`.

07F.2 as a whole and 08A remain blocked until the 07F.2H post-fix privileged suite is fully green.
