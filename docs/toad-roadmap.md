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
- [x] `kikimora-toad validate` and production AWG2 `run` command;
- [x] Linux/macOS/Windows unit-CI matrix exists;
- [x] platform TUN abstraction implemented;
- [x] Linux Toad-owned TUN gate green;
- [x] official AmneziaWG2 backend attached to Toad-owned TUN;
- [x] real isolated AWG2 client/server gate green;
- [x] WG/AWG profile and VLESS REALITY share-link import normalizes into Toad config;
- [x] reusable local/CI isolated-network harness and local run plan exist;
- [x] next Xray/multi-Toad execution packets written after AWG result review;
- [x] official Xray backend working with official Xray-owned TUN lifecycle gate green;
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

For AmneziaWG on Linux the ownership is:

```text
Toad owner fd ------------------------------+
                                             |
                                             +--> keeps kk-awg0 alive
                                             |
DuplicateFile() -> official amneziawg-go ----+
```

Closing the duplicate used by the protocol core must not remove the TUN. Closing the final Toad owner fd during deliberate process shutdown may remove it.

For Xray, use the official Xray-core TUN implementation initially; keep the Xray core instance alive across normal VLESS/REALITY transport failures so its TUN remains stable.

## Profile import

`kikimora-toad import` normalizes external profile material into the same validated Toad configuration used by the runtime.

Current supported inputs:

- ordinary WireGuard/AmneziaWG `[Interface]` + `[Peer]` configuration text;
- `wg://`, `wireguard://` and `amneziawg://` encoded/query profile forms supported by the importer;
- direct `vless://` links using REALITY over TCP/raw, including UUID, endpoint, SNI, public key, short id, fingerprint, spiderX and optional Vision flow.

WireGuard has no single universal official share-URI standard, so raw provider `.conf` input is the compatibility baseline. Import never executes routing hooks or arbitrary commands from source material.

Treat share links as bearer credentials. Real links must not enter CI logs, fixtures, issues or commits.

## Isolated test environment

Hermetic Linux tests share helpers in:

```text
linux/tests/toad/lib/netns.sh
```

The local entry point is:

```text
linux/tests/toad/run-isolated.sh
```

and the workstation run plan is `docs/toad-local-isolated-test-plan.md`.

Required CI protocol gates remain private-network tests with disposable namespaces, no default route, no NAT and no public data path. Optional real-VPS tests are a separate manual layer and must consume local secret profiles without weakening hermetic gates.

## Current implementation horizon

Do not spec the whole project in detailed packets. Only the next few understood steps belong in `docs/toad-steps/`.

Completed packets:

1. `01-platform-linux-tun.md` — platform-neutral TUN contract + real Linux Toad-owned TUN and fd duplication;
2. `02-awg2-official-core.md` — attach official `amneziawg-go` to the Linux Toad-owned TUN;
3. `03-awg2-isolated-interop.md` — real AWG2 client/server gate with recovery and stable ifindex;
4. `04-xray-official-core.md` — embedded pinned official Xray-core, official Xray-owned TUN lifecycle and VLESS/REALITY config wiring.

Current packets, in order:

5. `05-xray-isolated-interop.md` — real isolated VLESS + REALITY + Vision payload/recovery gate;
6. `06-multi-toad-isolated.md` — simultaneous AWG2 + Xray failure-isolation gate.

Do not implement a later packet while executing an earlier one.

After each executor result, review actual code and CI and revise the next packet if concrete upstream/runtime behavior differs from the written assumptions.

## Stage 0 protocol release gates

A protocol is not "working" because its process starts or its TUN exists.

### AWG2

The hermetic official-reference gate is green and covers:

- production AWG2 J/S/H/I parameters;
- real handshake;
- real encrypted traffic;
- server restart recovery;
- private-underlay down/up recovery;
- unchanged client TUN ifindex through ordinary failures;
- no default route/NAT/public data path in the test namespaces.

### VLESS/REALITY

The official embedded Xray lifecycle/TUN gate is green, but protocol success is intentionally still unclaimed until step 05.

Step 05 must pass a hermetic real client/server test using official Xray-core, including:

- REALITY authentication;
- VLESS payload delivery and response;
- Vision when configured;
- server restart recovery;
- underlay down/up recovery;
- stable managed TUN across ordinary transport failure;
- no default route/NAT/public data path in the test namespaces.

After both independent gates are green, add the simultaneous AWG2 + Xray multi-Toad gate.

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
