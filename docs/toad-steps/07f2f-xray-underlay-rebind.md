# Toad step 07F.2F — preserve embedded Xray TUN across underlay rebind

Status: **IMPLEMENTED; XRAY TUN IDENTITY PRIVILEGED-VERIFIED AT 91c642a**.

Privileged baseline exposing this packet:
`fa799a4fa95cccb71c7c5dcf8e867606fba32512`.

Implementation commit:
`72b0308` — `fix(toad): preserve Xray TUN across underlay rebind`.

Purpose: stop recreating the embedded official-Xray instance and its owned TUN merely because the canonical physical underlay changed.

## Fresh privileged evidence at fa799a4

The operator reran `bash run-privileged-gates.sh`. Fresh artifacts were written at approximately 16:48-16:49 +03:00 on 2026-09-24.

Results before the new blocker:

- build-only: PASS;
- route-parking: PASS;
- multi-toad: PASS;
- xray-interop: PASS;
- OpenConnect routed-underlay preflight: `primary+backup PASS`;
- orchestration Phase 3: PASS;
- Phase A: PASS;
- Phase 6 AWG endpoint non-recursion: PASS;
- Phase B canonical underlay switched primary -> backup and epoch advanced to 2;
- Phase B retained the 07F.2C AWG fail-closed stable-TUN behavior;
- primary restoration advanced the canonical epoch to 3;
- the Phase B wait predicate reached the point where AWG, Xray and OpenConnect were all Ready at the current epoch.

The first remaining assertion failure was:

```text
Phase B: unrelated Xray TUN identity changed: 3 -> 7
```

Because the Ready/current-epoch predicate completed before this identity check, the routed OpenConnect endpoint fixture from 07F.2E is verified by this run. The next blocker is specifically unnecessary Xray TUN recreation.

## Existing architecture contract

This failure is not a test-invariant mistake. Existing Xray packets require the embedded official Xray instance to own `kk-xray0` for its lifetime and ordinary upstream/underlay recovery to preserve the same managed TUN identity.

## Root cause

Runtime capability selection is protocol-neutral. Before this packet the Xray backend implemented `backend.Backend`, `backend.Validator`, and `backend.EndpointReporter`, but neither `backend.Rebindable` nor `backend.TransportRestarter`.

Therefore a changed validated-underlay epoch selected `ActionRestartToad`. Replacing the whole Toad closes the embedded Xray instance; because official Xray owns its own TUN inbound, `kk-xray0` is destroyed and recreated with a new ifindex.

## Correct recovery ownership

For Xray, transport endpoint placement is already owned by core endpoint policy:

```text
underlay changes
    -> core ApplyEndpoint
    -> endpoint host route moves to selected physical underlay
    -> embedded Xray keeps running
    -> Xray transport reconnects through ordinary kernel routing
```

The backend should acknowledge that routing handoff without closing the embedded instance.

## Fix

`backend/xray.Backend` now implements `backend.Rebindable`. Its `Rebind` operation honors context cancellation, does not close the Xray instance, does not recreate the TUN, and does not duplicate endpoint-routing ownership.

Runtime capability discovery therefore exposes `Capabilities.Rebind=true`, so the protocol-neutral selector chooses `ActionRebind` instead of `ActionRestartToad` for ordinary Xray underlay epoch changes.

## Fixture strengthening

The fixture now also proves the real Xray endpoint is reachable through the primary AWG synthetic underlay. A preflight temporarily forces `198.51.100.2/32` through the primary gateway and requires a successful ping.

Expected marker:

```text
Xray endpoint routed-underlay fixture: primary PASS
```

Routing remains asymmetric: Xray may be reached through primary AWG, OpenConnect through either underlay, but the AWG endpoint is not exported through backup Xray. The AWG fail-closed proof therefore remains intact.

## Non-privileged verification at 72b0308

PASS: gofmt, targeted Xray/runtime/core/control tests, `go test ./...`, `go test -race ./...`, `go vet ./...`, bash syntax, shellcheck, service-cutover, orchestration-cutover, packaging, rootless model, and `git diff --check`.

## Privileged result and next packet

Fresh evidence at `91c642a` contains both routed-endpoint preflight PASS markers and proceeds through the Phase B Xray identity assertion without failure. The next and first remaining assertion is `Phase B: unrelated OpenConnect TUN identity changed: 4 -> 6`.

This verifies the Xray non-destructive Rebind behavior. The independent OpenConnect capability gap is tracked in `07f2g-openconnect-underlay-rebind.md`.

07F.2 as a whole and 08A remain blocked until the 07F.2G post-fix privileged suite is fully green.
