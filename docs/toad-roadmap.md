# Kikimora Toad roadmap

This is the canonical living roadmap for the production managed-VPN client path in PR #27.

A **Toad** is one Kikimora-managed VPN client/runtime instance. Kikimora orchestrates Toads; Leshy owns routing/DNS policy; protocol correctness comes from official protocol cores.

Repository documents, not chat history, are the source of truth. Update this file when architecture, status, dependency pins, acceptance gates, or the immediate implementation horizon change.

## Current direction

- runtime/control-plane language: Go;
- cross-platform runtime root: `toad/`;
- per-protocol executable: `kikimora-toad`;
- central daemon: `kikimora-core`;
- desktop frontend: Qt 6/QML in `desktop/`;
- one Toad process = one independently supervised managed VPN instance;
- production Linux ownership remains legacy/external until the explicit Go cutover gate passes;
- Go endpoint/routing/parking/recovery code already exists on the PR branch, but it is **not yet accepted as production owner**;
- routing/DNS classification remains Leshy; the Go core is taking lifecycle, endpoint-underlay, parking/publication coordination in ordered stages;
- external non-Toad VPNs such as a corporate `vpn0` remain externally owned;
- Windows remains UI/FakeCore scope; Linux is the first production acceptance platform and macOS needs its own privileged parity gate.

The 2026-09-21 post-push audit is `docs/toad-post-push-audit.md`. Canonical naming details remain in `docs/toad-naming.md`.

## Official protocol engines

Kikimora does not reimplement protocol machinery.

Pinned managed engines:

- AmneziaWG: `amnezia-vpn/amneziawg-go`, tag `v3.1.20260828`, commit `b5928efb6ca19f0153958460c3d141f04abc5c2e`;
- Xray: `XTLS/Xray-core`, release `v26.7.28`, commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`;
- OpenConnect: official `openconnect` executable supervised by Toad; hermetic reference server is `ocserv`.

Rules:

- no Kikimora AWG2 framing/crypto implementation;
- no independent VLESS/REALITY/Vision implementation;
- no OpenConnect protocol reimplementation;
- protocol health must not be inferred solely from process existence or TUN-UP;
- a backend is not called accepted until its real isolated data-plane gate is green;
- combined product isolation is not accepted until all managed protocols run simultaneously in the same client namespace.

The Rust-native experiment remains isolated in PR #25 and is not the production implementation path.

## Current implementation status

The branch now contains much more than the original Stage 0 protocol work: a Go core/controller, Toad IPC, supervision, endpoint/routing/parking/Leshy adapters, Linux/macOS platform work, service/cutover scripts and a Qt/QML desktop. Those are real code, but the 2026-09-21 audit found correctness gaps that prevent declaring the migration complete.

Protocol foundation:

- [x] Go Toad skeleton/config/state/platform abstraction;
- [x] Linux Toad-owned AWG TUN lifecycle;
- [x] official AmneziaWG backend and real isolated interop;
- [x] official Xray backend and real REALITY/VLESS/Vision interop;
- [x] OpenConnect backend supervising official openconnect + hermetic ocserv interop;
- [x] simultaneous AWG2 + Xray + OpenConnect multi-Toad gate;
- [x] Stage 0 protocol isolation complete.

Control-plane code present but **not production-accepted**:

- [x] Go desired/observed controller and revisioned API;
- [x] per-Toad control IPC and generation snapshots;
- [x] process supervisor/backoff;
- [x] endpoint/routing/parking/Leshy package boundaries;
- [x] Linux netlink underlay watcher and route executor;
- [x] systemd ownership/cutover scaffolding;
- [x] Qt/QML real-core UI path;
- [x] partial macOS adapters;
- [ ] executable capability contract fixed;
- [ ] authoritative epoch/generation validation fixed;
- [ ] Xray false-online semantics fixed;
- [ ] IPv4 + IPv6 fail-closed parking proven;
- [ ] complete endpoint route/rule reconciliation proven;
- [ ] NetworkManager `kk-*` ownership exclusion implemented/proven;
- [ ] address-loss drift repair proven;
- [ ] desired state persisted/restored across core restart;
- [ ] privileged Go ownership cutover accepted.

At audited code baseline `2c0fa833177c49c60cd0c58291490e1a28a16f79`, PR CI was not green. The audit records concrete failures and code defects in `docs/toad-post-push-audit.md`.

Do not infer completion from the amount of code already present.

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

The old single “step 07” became stale after the large control-plane push. It is retained as an umbrella architecture document only. Executors must use the smaller ordered packets below.

### Current executor handoff

The current mechanical implementation plan is:

`docs/toad-steps/07c-07d-executor-handoff-def08ccb.md`

It was produced from code audit of HEAD `def08ccb4ae03c84db6cc6125d8b63f9fd160390` and CI run `35751034945`.

At that reviewed HEAD:

- all Linux privileged/interoperability jobs in `Toad core` were green, including AWG2, Xray, OpenConnect, multi-Toad, TUN owner and IPv4/IPv6 route parking;
- the cross-platform Go unit jobs were blocked before tests by one formatting-only failure in `toad/internal/platform/linux/networkmanager/watcher.go`;
- 07A and 07B implementation/acceptance code is already present and must not be rewritten from the original packets;
- 07C implementation is largely present but still needs coalescer-failure supervision, bounded async full-restart handoff and deterministic sleep/NetworkManager/recovery coverage;
- 07D persistence, startup restore and API-aware cutover fixture are already present; the major missing automated acceptance is the privileged `go-orchestration-acceptance.sh` gate.

The handoff file is the executor entry point for finishing 07C/07D. The original 07A-07D packet files remain the acceptance contracts; where they describe code that is already implemented, audit/verify it instead of implementing it again.

Completed protocol packets:

1. `01-platform-linux-tun.md`;
2. `02-awg2-official-core.md`;
3. `03-awg2-isolated-interop.md`;
4. `04-xray-official-core.md`;
5. `05-xray-isolated-interop.md`.

Mandatory execution order from the current branch:

6. **COMPLETE:** `06-multi-toad-isolated.md` — simultaneous real AWG2 + Xray + OpenConnect isolation gate in one client namespace. Green: run `35639289523`, job `106464572838`.

6A. **DEFERRED, still required before final merge:** `06a-current-head-baseline.md`. By explicit user decision this does not block the 07A-07D implementation work.

7A. **IMPLEMENTED/AUDITED; REVALIDATE ON CURRENT HEAD:** `07a-authoritative-state-and-capabilities.md` — capability contract, generation/epoch validation authority and authoritative Toad state ingress are present. Do not redesign. Re-run its acceptance after Phase 0 of the current handoff.

7B. **IMPLEMENTED/AUDITED; REVALIDATE ON CURRENT HEAD:** `07b-routing-parking-failclosed.md` — endpoint reconciliation, ownership, IPv4/IPv6 parking and fail-closed recovery order are present. Do not redesign. Re-run its acceptance after Phase 0 of the current handoff.

7C. **CURRENT:** `07c-underlay-resume-networkmanager.md`, executed through `07c-07d-executor-handoff-def08ccb.md`. Finish only the concrete remaining gaps and automated acceptance. Real workstation suspend/resume remains an explicit manual/operator gate.

7D. **NEXT AFTER 07C AUTOMATED GREEN:** `07d-privileged-cutover-acceptance.md`, also executed through the current handoff. Preserve existing desired-state and cutover work; add/finish only missing acceptance, especially the privileged orchestration gate. Real installed-host cutover and legacy retirement remain manual/operator decisions.

The umbrella `07-go-reconcile-resume.md` is **superseded and must not be executed directly**.

Rules for this sequence:

- start from the actual branch HEAD; do not reset to the reviewed `def08ccb` SHA because the handoff/roadmap commits follow it;
- read `07c-07d-executor-handoff-def08ccb.md` before editing code;
- restore a green current-HEAD baseline before any further implementation;
- the current explicit ordering exception remains: 06A is deferred and does not block 07C/07D implementation, but remains required before final merge;
- if a packet or handoff marks a question **STOP/DESIGN**, executor stops and produces the requested focused sub-plan;
- do not “fix” acceptance by weakening a real interop or fail-closed test;
- do not automatically suspend the developer workstation, perform real production cutover, or retire legacy writers;
- update this roadmap only from code/CI evidence, not from intent.

## Stage 0 protocol release gates

A protocol is not working merely because its process starts or its TUN exists.

### AWG2

Independent hermetic gate exists and covers official AmneziaWG data traffic, server/underlay recovery and stable TUN identity.

### VLESS/REALITY

Independent hermetic gate exists and covers official Xray REALITY/VLESS/Vision data traffic and recovery. The current audited HEAD also exposed an important regression: Xray `Health()` equates TUN-UP with an online remote session, causing the unreachable-server lifecycle job to report `online`. Step 06A fixes the backend semantic, not the test.

### OpenConnect

The independent hermetic gate uses official `openconnect` against `ocserv`, a route-free vpnc script and a stable named `kk-oc0` route target. CI credentials are synthetic; real token/password material never belongs in the repository.

### Combined gate

Stage 0 remains incomplete until one test runs all three managed clients concurrently and proves:

- three live Toad processes;
- three distinct TUNs;
- real traffic through all three official protocol paths;
- one server/underlay/Toad failure does not disturb the other two;
- failed selected traffic cannot fall through another Toad or physical underlay;
- no ambient default/split-default route is installed by a protocol core.

That exact gate is `06-multi-toad-isolated.md`.

## Control-plane direction after standalone clients

Stage 0 keeps existing Bash Kikimora as orchestrator.

The first post-Stage-0 control-plane packet is now defined by the 2026-09-21 legacy suspend/resume failure. See `docs/toad-resume-recovery-architecture.md` and planned step 07.

Later replace heuristic wrappers/watchdogs with a direct explicit Go control contract between Kikimora and Toad processes. Expected concepts include:

- persistent desired state: start/stop/reconnect/reload;
- observed state snapshot;
- generation/config identity;
- underlay generation;
- interface identity/name/ifindex;
- interface configuration readiness kept distinct from process liveness;
- protocol session health;
- route readiness kept distinct from protocol health;
- explicit fail-closed route state when a selected Toad is unavailable;
- reason codes instead of parsing logs;
- idempotent/level-triggered reconciliation rather than one-shot reconnect events;
- explicit lifecycle events;
- multiple simultaneous Toads.

Host/network events may wake the reconciler, but they must not directly command protocol lifecycle. In particular:

- do not couple reconnect decisions to NetworkManager `CONNECTED_GLOBAL` state;
- do not discard a recovery request merely because the Toad is already reconnecting;
- suspend/resume must not recreate a stable route-target TUN;
- Linux `kk-*` TUNs must be explicitly excluded from NetworkManager configuration ownership;
- if a selected route target is not ready, routing must install an explicit deny/unreachable/blackhole outcome rather than allowing kernel fallback to another Toad or the physical default route;
- IPv4 and IPv6 must follow the same fail-closed rule.

## Desktop/UI status

The earlier Fyne direction is obsolete. The branch now contains a Qt 6/QML desktop client using the real local core IPC.

Current UI facts:

- one aggregate connect/disconnect control;
- compact per-role rows and detail actions;
- revisioned `RealCoreClient` subscription;
- production Linux uses the real core rather than silently selecting FakeCore;
- Windows stays FakeCore/frontend-only under the current networking roadmap.

UI code does not make networking acceptance true. Desktop tests must follow the authoritative core state contract from 07A-D, including monotonic revisions, persisted desired state and degraded/recovery states.

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

PR #27 stays draft until:

- 06A and the simultaneous three-Toad gate are green;
- 07A-07C safety contracts are green;
- 07D privileged Linux cutover/restart acceptance is recorded;
- current PR CI is green;
- no unresolved STOP/DESIGN packet blocks production ownership.

Do not merge based on process/TUN existence, unit-only success, or systemd-active status.
