# Toad step 07F.2H — publish structural drift before same-ifindex repair

Status: **IMPLEMENTED; PRIVILEGED VERIFICATION REQUIRED**.

Privileged baseline exposing this packet:
`e49e039415341702ca7b0947b0af740549bf3f5e`.

Implementation commit:
`4a6e8fe` — `fix(toad): publish structural drift before repair`.

Purpose: make a real managed-interface structural failure observable and fail-closed before the runtime performs automatic same-ifindex repair.

## Fresh privileged evidence at e49e039

The authoritative privileged run at approximately 18:12-18:14 +03:00 produced:

- build-only PASS;
- route-parking PASS;
- multi-toad PASS;
- xray-interop PASS;
- Xray endpoint routed-underlay preflight primary PASS;
- OpenConnect endpoint routed-underlay preflight primary+backup PASS;
- Phase 3 PASS;
- Phase A PASS;
- Phase 6 PASS;
- Phase B PASS;
- AWG remained fail-closed on its stable TUN during underlay loss;
- AWG recovered at epoch 3;
- Xray kept its original TUN identity;
- OpenConnect kept its original TUN identity.

This privileged run therefore verifies 07F.2G and closes the complete Phase B underlay-mutation contract.

The first remaining failure occurs in Phase C after the test removes `10.77.0.2/24` from `kk-awg0`:

```text
Phase C: AWG did not detect address loss
```

The final snapshot is already repaired and Ready with the original AWG ifindex.

## Root cause

`interfaceStructurallyReady` correctly reports false when a configured AWG address is missing. The bug was ordering inside `Runtime.RunHealthLoop`.

On the first health tick after address removal the old code:

1. read the drifted interface;
2. detected `structuralReady=false`;
3. immediately called the interface repairer in the same tick;
4. reread the repaired interface;
5. replaced the pending degraded snapshot with the repaired Ready snapshot;
6. published only the final repaired state.

Kernel repair therefore worked, but the product/control plane never observed the fail-closed structural transition.

This violates the intended 07C/07F acceptance contract: structural drift must first withdraw route readiness, then repair the same owned interface.

## Fix

The health loop now uses an explicit two-tick handoff.

First drift tick:

```text
read interface
  -> structuralReady=false
  -> publish State=degraded
  -> publish RouteReady=false
  -> schedule repair no earlier than the next health-loop tick
```

Next eligible tick:

```text
same interface still drifted
  -> obtain LocalInterfaceExpectation
  -> repair same ifindex
  -> reread interface
  -> publish repaired structural state
```

The existing one-second retry interval still rate-limits failed/incomplete mutation attempts. A successful repair clears the repair deadline.

If a nominal repair succeeds but structural readiness is still false, the runtime now keeps an explicit degraded state with reason `managed interface drift persists after repair` instead of publishing a misleading healthy transport state.

OpenConnect negotiated-address drift remains separate and is never handed to the generic interface repairer.

## Deterministic verification at 4a6e8fe

The AWG repair test now requires observable ordering:

- `RouteReady=false` must be visible first;
- repair call count must still be zero at that point;
- repair then runs;
- the same ifindex is retained;
- route readiness returns after repair.

PASS:

- gofmt;
- targeted toadruntime/control/core tests;
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

## Required privileged verification

Run `bash run-privileged-gates.sh`.

Required evidence:

- retain the fully green Phase B contract;
- Phase C observes AWG `route_ready=false` after address removal;
- Phase C repairs the same AWG ifindex and returns Ready/current epoch;
- Phase D completes OpenConnect negotiated-address recovery;
- phases E-F complete;
- route-parking, multi-toad and xray-interop remain green;
- final cleanup and host-state before/after PASS.

Do not start 08A until the complete 07F.2 privileged suite is green.
