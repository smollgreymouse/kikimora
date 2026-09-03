# Toad step 06 — simultaneous AWG2 + Xray multi-Toad isolation

## Goal

Prove the Stage 0 product topology with multiple independently supervised Toad processes active at the same time: at least one real AWG2 client and one real VLESS + REALITY + Vision client, each using its official protocol core and its own managed interface, with failures isolated from the other Toad.

This step is after both independent protocol gates are green.

## Prerequisites

- AWG2 isolated client/server gate green;
- Xray isolated client/server gate green;
- reusable namespace harness/local runner available;
- no unresolved ownership/recovery issue from earlier packets.

## Topology

Use disposable namespaces and private links only. One acceptable layout:

```text
awg-client-ns ---- private veth ---- awg-server-ns
   kk-awg0                              awg-ref0

xray-client-ns --- private veth ---- xray-server-ns
   kk-xray0                             Xray REALITY reference + target
```

A stronger variant may run both client Toad processes in one client namespace if doing so makes routing/ownership interference easier to detect, provided protocol underlays remain explicit and there is still no default route/NAT/public data path.

## Required proof

Start both Toads concurrently and record:

- AWG Toad PID and `kk-awg0` ifindex `A`;
- Xray Toad PID and `kk-xray0` ifindex `X`.

Prove simultaneously:

- real AWG encrypted data traffic;
- real Xray REALITY/VLESS/Vision application traffic;
- both processes/state snapshots remain independent;
- interfaces have distinct configured names/identities.

## Failure isolation phases

### A — AWG server failure

Stop only AWG reference server.

Assert:

- AWG Toad remains alive and `kk-awg0` ifindex remains `A`;
- Xray Toad remains alive;
- `kk-xray0` ifindex remains `X`;
- Xray application traffic continues succeeding throughout the AWG outage.

Restart AWG reference and verify AWG traffic recovery without affecting Xray.

### B — Xray server failure

Stop only Xray reference server.

Assert:

- Xray Toad remains alive and `kk-xray0` ifindex remains `X`;
- AWG Toad remains alive;
- `kk-awg0` ifindex remains `A`;
- AWG encrypted traffic continues succeeding throughout the Xray outage.

Restart Xray reference and verify Xray application traffic recovery without affecting AWG.

### C — one Toad shutdown

Terminate the AWG Toad cleanly while Xray remains active.

Assert:

- `kk-awg0` disappears;
- Xray process/interface remain unchanged;
- Xray application traffic still works.

Then restart AWG Toad if needed for the symmetric check, or terminate Xray and assert AWG remains unaffected.

## Routing policy boundary

This packet tests protocol/process isolation, not full Leshy production policy.

Do not add an ambient default route merely to simplify the test. Explicit test routes may target only the private protocol/application destinations required by each gate.

Leshy integration may consume the stable interface identities after this gate; it is not allowed to become a hidden dependency for protocol correctness.

## State/control assertions

Each Toad must write only its own state directory and report its own:

- protocol;
- interface name/ifindex;
- protocol/session health available at that stage;
- counters where trustworthy.

Stopping/restarting one Toad must never mutate the other's state file.

## Shared test infrastructure

Extend `linux/tests/toad/run-isolated.sh` with a `multi-toad` mode and include it in `all` after both independent protocol interop gates.

Reuse `linux/tests/toad/lib/netns.sh` instead of duplicating generic namespace polling/cleanup logic.

## CI acceptance

Dedicated `linux-multi-toad` gate must prove in one run:

- both official protocol clients active simultaneously;
- both real official reference servers active;
- real traffic through both protocols;
- AWG outage does not interrupt Xray traffic/interface/process;
- Xray outage does not interrupt AWG traffic/interface/process;
- restart recovery for each remains independent;
- deliberate shutdown of one Toad removes only its interface;
- no root namespace leaks;
- no public/default-route/NAT test path.

Existing single-protocol gates remain required and green.

## Forbidden shortcuts

Do not:

- serialize the tests so the clients are never active together;
- share one process between the two Toads;
- reuse one interface for both protocols;
- restart both Toads when only one protocol fails;
- use a mocked server for either protocol;
- weaken independent protocol gates after adding the combined gate.

## Executor report

Return:

1. commit SHA(s);
2. CI run id/result;
3. simultaneous PIDs and initial interface ifindices;
4. evidence of concurrent real traffic;
5. AWG-failure effect on both Toads;
6. Xray-failure effect on both Toads;
7. one-Toad shutdown isolation evidence;
8. final cleanup result.
