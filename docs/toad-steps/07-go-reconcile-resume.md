# Toad step 07 — Go Kikimora reconciliation, suspend/resume recovery and fail-closed routing

Status: **planned, not started**.

This packet is intentionally **after** Toad step 06 and Stage 0 protocol completion. Do not execute it while step 06 is unfinished.

Architecture reference: `docs/toad-resume-recovery-architecture.md`.

## Goal

Introduce the first Go Kikimora control-plane/reconciliation layer around managed Toads so host network changes, suspend/resume and partially broken interfaces converge back to the desired state without event-driven restart races or direct-route leaks.

This step is not a rewrite of Leshy DNS classification. It establishes the authoritative lifecycle/readiness/reconciliation contract that Leshy can consume during migration.

## Prerequisites

- Toad step 06 green;
- Stage 0 marked complete only after its existing gates pass;
- AWG2 and Xray Toads expose stable state snapshots;
- stable route-target interface semantics already proven for both protocols;
- no unresolved ownership issue that requires changing the Stage 0 Toad backend contract.

## Required architecture before coding

The executor must preserve these boundaries:

### Go Kikimora

Owns desired state and reconciliation:

- desired Toad up/down;
- config generation;
- underlay generation;
- endpoint-underlay policy;
- route-target readiness;
- fail-closed host routing state;
- suspend/resume observations;
- retry/backoff and convergence.

### Toad

Owns the managed interface and protocol instance:

- interface creation/identity;
- interface addresses/MTU/link state;
- protocol core;
- protocol health;
- idempotent transport reset;
- state publication.

### Leshy

Still owns DNS classification and route intent during the migration phase.

It must consume explicit Toad readiness and must not infer readiness from process existence, interface name existence or stale runtime files.

## State contract

Extend the control/state model so the reconciler can distinguish:

- process alive;
- interface present;
- expected ifindex/identity;
- interface configuration valid;
- underlay ready;
- protocol/session healthy;
- route target ready;
- desired config generation;
- observed generation/reason.

Do not collapse these into a single `connected` boolean.

At minimum the controller must be able to tell the difference between:

```text
A) TUN stable + underlay down + protocol unhealthy
   -> route_ready=true, degraded/reconnecting

B) process alive + TUN present + address missing
   -> route_ready=false, needs interface reconciliation

C) Toad absent
   -> route_ready=false, explicit fail-closed routing required
```

## Reconcile loop

Implement a level-triggered/idempotent reconciler.

Events may enqueue a reconcile pass, but correctness must not depend on receiving any one event.

Required behavior:

1. read desired state;
2. observe process/interface/config/underlay/protocol state;
3. repair interface drift first;
4. reconcile endpoint-underlay pinning;
5. publish route readiness;
6. keep selected routes fail-closed while transport is unhealthy;
7. allow ordinary official-core recovery;
8. escalate to an idempotent transport reset after a bounded grace period;
9. repeat until desired and observed states converge.

A reconcile request received while another reconcile/reconnect is running must be coalesced or queued. It must never be discarded merely because the instance is already in `reconnecting`.

## Suspend/resume implementation

Use an authoritative host sleep source on Linux, preferably systemd-logind `PrepareForSleep`, for suspend/resume phase information.

### Suspend entry

- keep Toad desired state `up`;
- do not recreate/stop the managed TUN merely because suspend begins;
- pause or back off transport retries if useful;
- preserve route-target identity;
- record diagnostic state.

### Resume

- bump underlay generation;
- wait for a real physical endpoint route, not NetworkManager `CONNECTED_GLOBAL`;
- reconcile endpoint pinning;
- reconcile Toad-owned interface address/MTU/link state;
- keep selected destinations fail-closed;
- allow protocol self-recovery;
- if stale after grace period, request idempotent transport reset;
- keep retrying/reconciling until healthy or explicitly stopped.

NetworkManager global connectivity events may wake the reconcile loop but may not directly call `stop()+start()`.

## Linux NetworkManager ownership gate

For `kk-*` Toad interfaces, add a Linux platform integration that prevents NetworkManager from owning their IP configuration.

The implementation may choose the supported mechanism appropriate to the installed NetworkManager version, but the acceptance contract is fixed:

- NetworkManager does not activate/manage address configuration for `kk-*`;
- Toad remains the authoritative owner;
- an external deletion of a Toad address is detected and repaired;
- suspend/resume does not change Toad interface identity;
- NetworkManager restart does not permanently steal the interface.

Do not require NetworkManager to exist for Toad correctness.

## Fail-closed route transaction

Add a routing reconciliation API that represents a selected destination as one of:

```text
SelectedReady(toad, destination)
SelectedBlocked(toad, destination, reason)
NotSelected(destination)
```

For `SelectedReady`, install/retain the Toad route.

For `SelectedBlocked`, install/retain an explicit unreachable/blackhole policy with priority sufficient to prevent fallback to another Toad or the physical default route.

Never implement VPN unavailability by merely deleting the selected route.

Cover both IPv4 and IPv6.

The DNS layer must not treat "DNS answer returned" as proof that route installation succeeded.

## External VPN behavior

Keep external clients such as `vpn0` outside Toad ownership.

The controller may expose an external-route-target adapter that reports:

- interface identity;
- address presence;
- carrier/operstate where meaningful;
- route-target readiness.

If that target becomes unusable:

- switch selected destinations to explicit blocked state;
- do not silently use the physical default;
- do not restart the external client unless a separate explicit ownership adapter is added later.

## Required regression tests

Add hermetic tests where possible and a manual real-host suspend gate.

### Reconcile/idempotence

- duplicate resume/network events;
- out-of-order underlay events;
- reconcile requested while transport reset is already running;
- controller eventually converges without duplicate Toad destruction/recreation.

### TUN ownership/config drift

- remove an expected Toad address externally;
- change MTU externally;
- verify automatic repair;
- verify unchanged ifindex.

### Underlay generation

- remove physical endpoint route;
- restore via a different gateway/source;
- verify endpoint pin update;
- verify no recursive endpoint routing through a Toad.

### Fail-closed routing

For both primary-selected and default-selected destinations:

- resolve destination;
- make selected route target unavailable;
- prove the destination cannot use physical IPv4 default;
- prove the destination cannot use physical IPv6 default;
- prove it cannot escape through a different healthy Toad unless explicit failover policy says so.

### NetworkManager

When NetworkManager is available:

- prove `kk-*` is not configuration-owned by it;
- restart NetworkManager;
- simulate sleep/resume device state transitions;
- verify Toad address/MTU/ifindex remain correct or are reconciled.

### Real suspend/resume gate

On a development host:

1. start at least one managed AWG Toad;
2. record PID, TUN ifindex, address, MTU, endpoint physical route and health;
3. suspend for several minutes;
4. resume and wait for controller convergence;
5. assert same TUN identity;
6. assert address/MTU restored;
7. assert endpoint underlay points to the physical network;
8. assert protocol traffic recovers;
9. assert selected destinations never used direct IPv4/IPv6 fallback while recovery was incomplete.

## Acceptance

Step 07 is green only when:

- recovery is level-triggered/idempotent rather than one-shot;
- no NetworkManager `CONNECTED_GLOBAL -> restart VPN` dependency exists;
- managed Toad interface drift is repaired;
- endpoint underlay is generation-aware;
- repeated recovery observations cannot get stuck in a state equivalent to legacy `Reconnecting`;
- selected destinations fail closed when their route target is unavailable;
- IPv4 and IPv6 are both covered;
- ordinary recovery preserves Toad interface identity;
- existing Stage 0 protocol gates remain green.

## Forbidden shortcuts

Do not:

- fix the problem with an arbitrary sleep delay followed by unconditional restart;
- recreate the TUN on every resume;
- use process existence as readiness;
- use `CONNECTED_GLOBAL` as the authoritative resume/reconnect trigger;
- ignore a reconcile request because another recovery is in progress;
- delete selected routes and rely on the main table;
- protect only IPv4;
- take ownership of external `vpn0` implicitly;
- weaken step 06 or earlier protocol tests to make this pass.

## Executor report

Return:

1. commit SHA(s);
2. exact packages/files added or changed;
3. desired/observed state model;
4. reconcile-loop behavior;
5. Linux NetworkManager ownership evidence;
6. interface-drift repair evidence;
7. underlay-generation/endpoint-pin evidence;
8. fail-closed IPv4/IPv6 evidence;
9. duplicate/reordered-event evidence;
10. real suspend/resume evidence if the host gate is run;
11. CI run ids/results;
12. unresolved platform-specific questions.
