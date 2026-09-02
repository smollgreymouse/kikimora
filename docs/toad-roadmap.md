# Kikimora Toad roadmap

This is the canonical living roadmap for the production managed-VPN client path in PR #27.

A **Toad** is one Kikimora-managed VPN client/runtime instance. Kikimora orchestrates Toads; Leshy owns routing/DNS policy; protocol correctness comes from official protocol cores.

Repository documents, not chat history, are the source of truth. Update this file when architecture, status, dependency pins, acceptance gates, or the immediate implementation horizon change.

## Current direction

- runtime language: Go;
- cross-platform runtime root: `toad/`;
- executable: `kikimora-toad`;
- future CLI namespace: `kk toad ...`;
- per-instance service name: `kikimora-toad@<name>.service`;
- one Toad process = one independently supervised managed VPN instance;
- Stage 0 orchestrator: existing Bash Kikimora;
- routing/DNS: existing Leshy;
- external NetworkManager/OpenConnect VPNs such as `vpn0` remain externally owned;
- future desktop frontend: native GUI/tray, with Fyne currently the leading implementation choice, speaking to the Kikimora control plane rather than owning protocol lifecycles.

Canonical naming details: `docs/toad-naming.md`.

## Official protocol engines

Stage 0 intentionally does not implement protocol machinery itself.

Pinned starting revisions:

- AmneziaWG: `amnezia-vpn/amneziawg-go`, tag `v3.1.20260828`, commit `b5928efb6ca19f0153958460c3d141f04abc5c2e`;
- Xray: `XTLS/Xray-core`, release `v26.7.28`, commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`.

Rules:

- no Kikimora AWG2 framing/crypto implementation;
- no independent VLESS/REALITY/Vision implementation;
- use official cores as Go dependencies;
- a backend is not called working until its real isolated client/server data-plane gate is green.

The Rust-native experiment remains isolated in PR #25 and is not the production implementation path.

## Current implementation status

- [x] production branch split from Rust-native experiment;
- [x] `Toad` naming fixed for runtime/entity/CLI/service concepts;
- [x] cross-platform Go skeleton under `toad/`;
- [x] normalized config validation;
- [x] state schema v1 and atomic snapshot writer;
- [x] protocol backend interface;
- [x] `kikimora-toad validate` / skeleton `run` command;
- [x] Linux/macOS/Windows unit-CI matrix exists;
- [x] next three detailed execution packets exist in `docs/toad-steps/`;
- [ ] platform TUN abstraction implemented;
- [ ] Linux Toad-owned TUN gate green;
- [ ] official AmneziaWG2 backend attached to Toad-owned TUN;
- [ ] real isolated AWG2 client/server gate green;
- [ ] Xray official-core implementation packets written after AWG result review;
- [ ] official Xray backend working;
- [ ] real isolated VLESS/REALITY/Vision gate green;
- [ ] simultaneous multi-Toad protocol gate green;
- [ ] Stage 0 complete.

## Product topology

The target is multiple independently active managed VPNs, not one selected desktop VPN.

```text
Kikimora
   |
   +-- Toad awg-main   -> kk-awg0
   +-- Toad xray-main  -> kk-xray0
   +-- Toad awg-backup -> kk-awg1
   +-- external target -> vpn0
   |
   v
 Leshy
```

Leshy eventually routes zones to named targets:

```text
blocked -> awg-main
ai      -> xray-main
work    -> corporate
```

`primary` / `secondary` remain compatibility concepts during early migration only.

## Hard architectural invariants

1. **Official cores own protocol correctness.**
2. **Each Toad is independently restartable.** A fault in one must not recreate or stop another.
3. **Ordinary transport recovery must not recreate the route-target TUN.** Handshake timeout, rekey, peer restart, packet loss, suspend/resume, REALITY reset, and underlay loss are session events.
4. **Managed TUNs fail closed.** Traffic routed to an unavailable Toad must not silently escape via the physical default route.
5. **NetworkManager connectivity state is not a reconnect command.** `CONNECTED_GLOBAL` must never cause a full Toad teardown/recreation.
6. **systemd supervises process death, not normal network health.**
7. **Toads report facts; Kikimora decides policy; Leshy decides routing/DNS policy.**
8. **Secrets never appear in state snapshots or normal logs.**
9. **External VPNs remain externally owned.**
10. **The runtime stays cross-platform.** Common config/state/backend/lifecycle code lives under `toad/`; platform-specific code stays behind build-tagged adapters.
11. **Real protocol gates are isolated.** Protocol CI uses disposable namespaces/private links with no public/default route or NAT.
12. **Plans advance only a few concrete steps ahead.** Do not pre-plan the rest of Stage 0 in executor-level detail before current results are reviewed.

## Cross-platform layout

Canonical shape:

```text
toad/
  cmd/kikimora-toad/
  internal/
    backend/
      awg2/
      xray/
    config/
    state/
    platform/
      tun.go
      tun_linux.go
      tun_darwin.go
      tun_windows.go
```

Linux is the first real TUN/integration implementation because Linux network namespaces provide a safe deterministic protocol test environment. That must not move the common runtime back under `linux/`.

## Toad state contract

Authoritative per-instance snapshot target:

```text
/run/kikimora/vpn/clients/<name>/state.json
```

The schema separates transport/session state from route-target availability.

States currently planned:

```text
starting
armed
connecting
online
reconnecting
degraded
failed
stopping
```

and separately:

```text
route_ready=true|false
```

A Toad may remain `route_ready=true` while `state=reconnecting`: the stable TUN remains a fail-closed routing target while the official core recovers.

Snapshots are authoritative. Future events/control sockets are advisory/control-plane mechanisms layered on top.

## AWG2 boundary

Preferred Linux boundary:

```text
Toad creates and owns kk-awg0
        |
        +-- owner fd retained for Toad lifetime
        |
        +-- duplicated fd
                |
                v
amneziawg-go/tun.CreateTUNFromFile
                |
                v
amneziawg-go/device.NewDevice
```

The official core receives only the duplicate. Closing/stopping the protocol attachment must not close the Toad owner fd or change the TUN ifindex.

Health is sampled from official UAPI facts such as latest handshake, RX/TX bytes, and endpoint. `device.Up()` alone is not proof of connectivity.

## Xray boundary

Use original Xray-core as a Go dependency. Generate the minimum TUN + VLESS + REALITY/Vision config and create/start a `core.Instance` through official APIs.

Xray automatic system routing must remain disabled. Kikimora/Leshy own route policy.

Normal VLESS/REALITY transport failures must recover inside the live Xray instance rather than causing Toad process/TUN recreation.

Do not write detailed Xray execution packets until the current AWG packets have been executed and reviewed.

## Share-link requirement

Stage 0 must eventually import:

- `wg://`;
- `awg://`;
- `amneziawg://`;
- `wireguard://`;
- `vless://`.

Import produces normalized Toad TOML. Arbitrary routing/shell directives such as `PreUp`, `PostUp`, `PreDown`, `PostDown`, and `Table` are rejected. Stdin import must be supported so secret links need not appear in argv/history.

## Testing gates

### Cross-platform unit gate

Linux, macOS and Windows:

- formatting;
- vet;
- unit tests;
- common packages compile without Linux-only imports.

### Linux TUN gate

Disposable network namespace, no default/public route:

- real TUN creation;
- addresses/MTU;
- owner fd and duplicate fd semantics;
- closing duplicate leaves same TUN/ifindex alive;
- owner close removes TUN;
- no root-namespace interface leakage.

### AWG2 reference gate

Two isolated namespaces/private veth only:

- official pinned reference implementation;
- real AWG2 handshake with non-default J/S/H/I profile;
- real encrypted IP traffic;
- RX/TX counters advance;
- server restart recovery without Toad restart;
- underlay down/up recovery without Toad restart;
- same client TUN ifindex throughout;
- clean owner-driven shutdown.

### Xray reference gate

Later, using original pinned Xray-core server plus local REALITY decoy/target:

- real VLESS+REALITY authentication;
- real tunneled payload response;
- Vision when configured;
- reference restart recovery;
- underlay recovery;
- stable TUN identity;
- fail-closed behavior;
- no public/default route/NAT.

### Multi-Toad gate

AWG2 and Xray active simultaneously. Failure of either protocol/reference must not affect the other's process, interface, or traffic.

## Development stages

### Stage 0 — standalone Toads on official cores

Existing Bash Kikimora orchestrates independently working Toads; Leshy remains routing authority.

High-level contract: `docs/toad-stage0.md`.

Only the next few executor packets are detailed at any time in `docs/toad-steps/`.

### Stage 1 — explicit state in shadow mode

Existing status/diag tooling consumes Toad snapshots for observability while legacy readiness remains routing-authoritative.

### Stage 2 — explicit readiness for managed Toads

Replace managed-client interface/recreation heuristics with authoritative Toad `route_ready`/identity/state. External black-box VPNs retain observation heuristics.

### Stage 3 — semantic control and events

Add control/event IPC. `reconnect` becomes a protocol/session command, not `systemctl restart`.

### Stage 4 — endpoint-underlay leases

Replace polling/pending markers for managed Toads with explicit endpoint prepare/grant semantics.

### Stage 5 — Kikimora orchestrator daemon

Move steady-state control logic from Bash watchdog/reconcile scripts into a dedicated control plane. Shell remains installation/recovery tooling.

### Stage 6 — N named targets / N zones

Remove the hard primary/secondary model and allow several connected Toads plus external targets.

### Stage 7 — desktop GUI/tray

Fyne is currently the leading GUI choice. The frontend should expose Toad profiles, share-link import, counters, status, start/stop/reconnect, and later routing-zone assignment. GUI restart must not affect Toad lifecycles.

### Stage 8 — compatibility-watchdog cleanup

Remove managed-Toad heuristics/polling once explicit state and control are authoritative; retain compatibility observation only for external VPNs.

## Immediate implementation horizon

Do **not** expand this list until current results are returned and reviewed.

1. `docs/toad-steps/01-platform-linux-tun.md` — platform abstraction + Linux-owned TUN.
2. `docs/toad-steps/02-awg2-official-core.md` — official `amneziawg-go` attachment over duplicated Toad TUN fd.
3. `docs/toad-steps/03-awg2-isolated-interop.md` — real isolated AWG2 data plane + server/underlay recovery + same-ifindex gate.

After step 03, update this roadmap with actual results before writing Xray execution packets.

## Handoff discipline

Before ending a substantial implementation session:

1. update **Current implementation status** here;
2. update the active step file only if its assumptions changed;
3. record exact upstream tag/commit changes;
4. record blockers/failures that affect future decisions;
5. keep PR #27 body aligned;
6. never leave an architectural decision only in chat.

Minimum reading order for a new agent:

1. `docs/toad-roadmap.md`;
2. `docs/toad-stage0.md`;
3. `docs/toad-naming.md`;
4. the active file in `docs/toad-steps/`;
5. PR #27 and current CI;
6. implementation/tests.
