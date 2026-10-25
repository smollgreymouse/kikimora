# Rust-native VPN client experiment — current status and continuation plan

This file freezes the state of the Rust-native managed VPN experiment in PR #25 so the branch can remain useful after the production implementation moves to the official protocol cores.

The architectural roadmap remains `docs/vpn-runtime-roadmap.md`; detailed Stage 0 and Stage 1 execution plans remain beside it. This document records what was actually implemented, what passed, what failed, and what should be done if the Rust-native path is resumed later.

## 1. Branch purpose after the production pivot

This branch is now research/experimental. It intentionally preserves:

- the Rust multi-instance runtime;
- Kikimora-owned Linux TUN lifecycle;
- state snapshot model and semantic backend transitions;
- import support for WG/AWG and VLESS share links;
- isolated network-namespace test harnesses;
- Rust-native AWG2 integration through `RunaRoo/AmneziaWG-RS`;
- Rust-native VLESS/REALITY integration through `aimalygin/xray-rust`;
- reference interop failures and diagnostics.

Do not silently weaken or delete the failing reference gates merely to make this branch green. They are the most valuable result of the experiment.

## 2. Architectural invariants already proven useful

The following decisions should be carried forward regardless of protocol-core language:

1. Kikimora owns the managed TUN lifecycle.
2. Transport/session reconnect must not recreate the TUN.
3. `route_ready` is independent of transport state.
4. A reconnecting managed TUN remains a fail-closed sink.
5. systemd supervises process failure; protocol recovery stays inside the client.
6. NetworkManager connectivity-state transitions are not reconnect triggers for managed clients.
7. State snapshots are authoritative; events are advisory.
8. Managed VPN instances are named and independent; they are not synonymous with `primary` / `secondary`.
9. Existing NetworkManager/OpenConnect `vpn0` remains an external target.
10. Stage 0 keeps the current Bash Kikimora as orchestrator.

## 3. Implemented generic runtime work

The branch contains a standalone `kikimora-vpn` runtime with:

- one process per managed VPN instance;
- real Linux `/dev/net/tun` creation;
- stable interface identity across backend reconnects;
- separate TUN read/write halves;
- bounded packet queues;
- queue limits by both packet count and total byte budget;
- state schema v1;
- atomic `state.json` replacement;
- semantic `starting`, `connecting`, `online`, `reconnecting`, `degraded`, `failed`, and stopping transitions;
- `route_ready` separated from session health;
- generation/reconnect counters;
- deterministic bounded exponential backoff with jitter;
- multi-instance systemd template packaging;
- test stub backend including deterministic reconnect loops.

A significant runtime bug was found and fixed during this work: backend transitions were originally published through `watch`, which can collapse a fast `reconnecting -> online` sequence into the final value. Semantic transitions now use bounded `mpsc`; snapshots still represent current state.

## 4. Import boundary

The experimental branch also contains import/normalization work for:

- `wg://`;
- `awg://`;
- `amneziawg://`;
- `wireguard://`;
- `vless://` with REALITY parameters.

WG/AWG import accepts base64 `[Interface]` / `[Peer]` configs and preserves the AWG2 fields required by the current protocol. Dangerous imported directives such as shell hooks and route-management commands are rejected. Secret configs are intended to be written atomically with mode `0600`.

This parser/normalization work is reusable conceptually, but the production official-core branch should copy only the externally useful contract rather than inheriting this branch's Rust protocol dependencies.

## 5. CI isolation model that should be retained

The test model is successful and should be copied to the production path:

### Unprivileged tests

- no root;
- no `/dev/net/tun`;
- deterministic state-machine and parser tests.

### Real TUN tests

- disposable Linux network namespace;
- no public/default route;
- TUN exists only inside the namespace;
- host namespace is checked for interface leakage.

### Protocol interoperability tests

Two namespaces are connected only by a private veth pair:

```text
client-ns <---- private veth ----> server-ns
```

There is no NAT and no public default route. Reference protocol peers are built at exact pinned revisions. Fault tests restart the peer and drop the private underlay while asserting that the client TUN ifindex does not change.

### Multi-instance soak

The branch includes a two-instance reconnect/crash soak that verifies:

- both managed TUNs coexist;
- repeated reconnect transitions do not change ifindex;
- killing one client does not break the other;
- host network namespace is not modified.

## 6. Green gates at the pivot point

The generic runtime side reached healthy CI behavior:

- Rust formatting: green;
- clippy with warnings denied: green;
- unit tests: green;
- ShellCheck: green;
- real Linux TUN namespace smoke: green;
- multi-instance reconnect/crash soak: green after fixing root-owned cleanup handling.

These green gates prove the runtime/lifecycle/test-boundary work, not protocol interoperability.

## 7. Rust-native AWG2 implementation state

The backend uses pinned `RunaRoo/AmneziaWG-RS` through its packet/TUN abstraction rather than its Linux TUN implementation.

The intended boundary is correct:

```text
Kikimora Linux TUN
       |
       | raw IP packets
       v
channel adapter
       |
       v
AmneziaWG-RS core
       |
       v
AWG2 UDP
```

The third-party library is not allowed to create/delete the kernel TUN or install routes/hooks.

Additional work already done:

- upstream workers run in an isolated smol executor thread;
- invalid key material is checked before reaching upstream panic-prone paths;
- health is derived from peer handshake/counter state;
- backend retries recreate only protocol/session state;
- endpoint resolution happens again at retry boundaries;
- retry/backoff does not recreate the TUN.

### AWG2 reference failure

The strict reference gate uses pinned official `amneziawg-go` / `amneziawg-tools` in a server namespace.

Observed behavior at the pivot:

- the Kikimora TUN receives traffic;
- the Rust backend emits encrypted/obfuscated UDP attempts;
- client-side AWG tx counters advance;
- no successful handshake is established;
- the official reference peer does not advance to an authenticated handshake/session.

Several false hypotheses were eliminated:

- the `amneziawg-go` message about first-class kernel support is only a warning; the reference process continues running;
- the Rust implementation does set H1 before computing MAC1;
- S1 is laid out as prefix padding before the fixed WireGuard handshake body, matching the Go implementation conceptually;
- the failure therefore remains in detailed AWG framing/interop, not in Kikimora TUN lifecycle.

A useful next diagnostic if this path is resumed is a matrix using the same engine and keys:

1. WG-shaped profile: `S=0`, standard H1-H4, no junk/I packets;
2. H ranges only;
3. S padding only;
4. junk packets only;
5. production AWG2 S/H/J/I profile.

The final gate must return to a real AWG2 production profile; the matrix is only for localization.

## 8. Rust-native VLESS/REALITY implementation state

The backend uses pinned `aimalygin/xray-rust` through its packet-oriented TUN runtime rather than giving it the Linux TUN descriptor.

Intended boundary:

```text
Kikimora Linux TUN
       |
       | raw IP packets
       v
xray-rust TUN packet API
       |
       v
VLESS + REALITY + Vision
```

The backend state machine was corrected so that starting the Xray core does not itself mean `online`; a successful transport/open event is required.

### Xray reference failure

The strict server reference is pinned official `XTLS/Xray-core` with a hermetic REALITY setup:

- Xray server on the private veth;
- local TLS REALITY decoy inside server namespace;
- tunneled HTTP target on a private address inside server namespace;
- no public/default route.

Observed behavior at the pivot:

- REALITY/VLESS authentication succeeds;
- official Xray accepts requests targeting the expected tunneled HTTP endpoint;
- the HTTP target does not receive the application payload;
- therefore failure is after VLESS/REALITY acceptance, in the Rust TUN-to-stream / Vision payload path.

A first REALITY detector warm-up was also found to be much slower than the original two-second curl loop allowed. The harness was corrected to allow a bounded warm-up; this did not eliminate the payload-path failure.

The next useful diagnostic if this path is resumed is the same TUN runtime with REALITY but no Vision flow. If that succeeds, isolate Vision framing. If it fails too, focus on the TUN TCP upload bridge after remote stream opening.

The final production gate must restore the desired VLESS + REALITY profile (and Vision when the server/share link requires it).

## 9. Why production is moving to official cores

The Rust-native architecture is attractive but both protocol implementations are comparatively young. Stage 0's purpose is to obtain trustworthy managed clients quickly, not to become maintainers of independent AWG2 or Xray protocol implementations.

The production successor therefore uses:

- official `amneziawg-go` as the AWG2 protocol dependency;
- official `XTLS/Xray-core` as the VLESS/REALITY protocol dependency;
- Kikimora-owned interface/lifecycle boundaries around those cores;
- the same real isolated reference tests.

The production switch is a protocol-engine decision, not a rejection of the runtime/orchestrator roadmap.

## 10. If the Rust-native path is resumed

Resume only after the official-core Stage 0 is operational. Recommended order:

1. Keep all existing failing reference gates intact.
2. Add the AWG compatibility matrix described above.
3. Localize and fix AWG framing against pinned `amneziawg-go`.
4. Add plain-REALITY TUN reference gate for xray-rust.
5. Localize Vision vs TUN upload bridge.
6. Require real bidirectional traffic for both protocols.
7. Require peer restart and underlay loss recovery with stable ifindex.
8. Require simultaneous AWG2 + VLESS managed instances.
9. Only then reconsider replacing the official cores in production.

## 11. Definition of success for this experimental branch

This branch should not be merged merely because generic tests are green. Rust-native Stage 0 is complete only when both strict official-reference protocol gates pass with real traffic and the TUN identity survives failure/recovery. Until then it remains a preserved experiment and roadmap branch.
