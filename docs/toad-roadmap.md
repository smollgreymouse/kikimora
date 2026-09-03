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
- [x] platform TUN abstraction implemented;
- [x] Linux Toad-owned TUN gate green;
- [x] official AmneziaWG2 backend attached to Toad-owned TUN;
- [x] real isolated AWG2 client/server gate green;
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
   |
   +-- external corporate VPN -> vpn0
   |
   v
 Leshy routing/DNS policy
```

A failure or restart of one Toad must not recreate or disrupt another Toad's interface.

## Cross-platform boundary

Common runtime code lives under `toad/`.

Platform-specific code must stay behind platform adapters/build tags:

```text
toad/internal/platform/tun.go
toad/internal/platform/tun_linux.go
toad/internal/platform/tun_darwin.go      # later
toad/internal/platform/tun_windows.go     # later
```

Linux is implemented first because it is the current deployment/test platform and supports isolated network-namespace CI. That must not leak Linux-only assumptions into common config/state/backend APIs.

## TUN lifecycle invariant

Ordinary protocol/session/underlay recovery must not recreate the route-target TUN.

For AmneziaWG on Linux the intended ownership is:

```text
Toad owner fd ------------------------------+
                                             |
                                             +--> keeps kk-awg0 alive
                                             |
DuplicateFile() -> official amneziawg-go ----+
```

Closing the duplicate used by the protocol core must not remove the TUN. Closing the final Toad owner fd during deliberate process shutdown may remove it.

For Xray, use the official Xray-core TUN implementation initially; keep the Xray core instance alive across normal VLESS/REALITY transport failures so its TUN remains stable.

## Current implementation horizon

Do not spec the whole project in detailed packets. Only the next few understood steps belong in `docs/toad-steps/`.

Current packets:

1. `01-platform-linux-tun.md` — platform-neutral TUN contract + real Linux Toad-owned TUN and fd duplication;
2. `02-awg2-official-core.md` — attach official `amneziawg-go` to the Linux Toad-owned TUN;
3. `03-awg2-isolated-interop.md` — real AWG2 client/server gate with recovery and stable ifindex.

Do not implement a later packet while executing an earlier one.

After each executor result, review actual code and CI and revise the next packet if needed. Write Xray execution packets only after the AWG path has produced enough concrete integration information.

## Stage 0 protocol release gates

A protocol is not "working" because its process starts or its TUN exists.

### AWG2

Must pass a hermetic real client/server test using official AmneziaWG implementation(s), including:

- production AWG2 J/S/H/I parameters;
- real handshake;
- real encrypted traffic;
- server restart recovery;
- private-underlay down/up recovery;
- unchanged client TUN ifindex through ordinary failures;
- no default route/NAT/public data path in the test namespaces.

### VLESS/REALITY

Must pass a hermetic real client/server test using official Xray-core, including:

- REALITY authentication;
- VLESS payload delivery and response;
- Vision when configured;
- server restart recovery;
- underlay down/up recovery;
- stable managed TUN across ordinary transport failure;
- no default route/NAT/public data path in the test namespaces.

After both independent gates are green, add a simultaneous AWG2 + Xray multi-Toad gate.

## Control-plane direction after standalone clients

Stage 0 keeps existing Bash Kikimora as orchestrator.

Later replace heuristic wrappers/watchdogs with a direct explicit control contract between Kikimora and Toad processes. Expected concepts include:

- desired state: start/stop/reconnect/reload;
- observed state snapshot;
- generation/config identity;
- interface identity/name/ifindex;
- protocol session health;
- route readiness kept distinct from protocol health;
- reason codes instead of parsing logs;
- explicit lifecycle events;
- multiple simultaneous Toads.

Do not couple reconnect decisions to NetworkManager `CONNECTED_GLOBAL` state.

## Future GUI direction

A native desktop/tray GUI is a later stage, not part of the current execution horizon.

Useful design decisions already retained internally:

- Fyne is the current leading GUI toolkit candidate;
- tray-first desktop UX;
- thin frontend over a stable client/control API;
- profiles/server management;
- share-link import;
- status and traffic counters;
- multiple active Toads;
- GUI does not own VPN protocol lifecycle.

Do not include external reference-repository names/links in project planning docs merely because their implementation techniques informed these decisions.

## Share-link requirement

Stage 0 eventually needs server/profile import for common link formats, including AWG/WireGuard-style inputs and `vless://` REALITY profiles.

Import must normalize into Toad configuration and must not execute arbitrary hooks/routing commands embedded in source configuration.

Detailed import work is deliberately not in the current three-packet horizon.

## Handoff rules for executors

Detailed step files are written for weaker implementation models and must be treated literally.

Executor rules:

1. Read `docs/toad-roadmap.md`, `docs/toad-stage0.md`, `docs/toad-naming.md`, then the assigned step.
2. Inspect existing code named by the step before editing it.
3. Do not invent an upstream API. If the named pinned dependency differs from the plan, stop and report exact package/symbol evidence.
4. Do not broaden scope to later steps.
5. Do not weaken a real test because it is difficult to pass.
6. Never use the runner/public network as VPN test data plane.
7. Keep protocol secrets out of logs/state.
8. Run all acceptance commands from the step.
9. Commit implementation changes.
10. Return commit SHA, exact test results, changed-file summary, deviations, and unresolved questions.

## Merge rule

PR #27 stays draft until the production protocol gates are real and green.

Do not merge based only on skeleton/platform success.
