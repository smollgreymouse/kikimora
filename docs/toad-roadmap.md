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
- [x] executable capability contract fixed;
- [x] authoritative epoch/generation validation fixed;
- [x] Xray false-online semantics fixed;
- [x] IPv4 + IPv6 fail-closed parking proven;
- [x] complete endpoint route/rule reconciliation proven;
- [x] NetworkManager `kk-*` ownership exclusion implemented/proven;
- [x] address-loss drift repair proven;
- [x] desired state persisted/restored across core restart;
- [x] privileged Go ownership cutover gate rebuilt from shared multi-protocol fixture;
- [x] observer dependency injection seam for deterministic tests;
- [x] observer health split into per-dimension errors;
- [x] coalescer self-kick after failure for immediate convergence retry;
- [x] sleep reconnect test exercises production coalescer and epoch advancement;
- [x] resume-while-recovery test exercises real observer/recovery path;
- [x] NetworkManager reconnect test has scripted watcher and assertions;
- [x] stale retry timers bound to process/epoch identity.

At audited code baseline `a9c58f88abcebaf7ffb3f630b5b69f0294d7e4b1`, PR CI was not yet verified but all local gates pass:
- `go test ./...` green;
- `go test -race ./...` green;
- `go vet ./...` green;
- `shellcheck` clean on `go-orchestration-acceptance.sh`.

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

The old single “step 07” is an umbrella architecture document only. Executors must use the audited packets below and must not infer completion from the existence of code or tests.

### Current audit and executor entry point

The current audited code HEAD is:

`cbfb70a31cda689fac1aa2719f9a00a3b60c5db4`

The current executor packet is:

`docs/toad-steps/07e-current-head-proof-closure.md`

The previous `07c-07d-executor-handoff-def08ccb.md` is historical input. Its implementation work landed, but the 2026-09-22 follow-up audit found that several new tests/gates do not yet prove their claims.

Current CI on audited HEAD:

- **green:** Route parking `35764438643`;
- **green:** CLI JSON API `35764438687`;
- **green:** Desktop UI `35764438640`;
- **green:** VPN profiles `35764438751`;
- **green:** VPN endpoint underlay `35764438836`;
- **red:** ShellCheck `35764438645`;
- **red:** Toad core `35764438661`.

The Toad-core red is currently a race-mode timing failure in `TestAsyncFullRestartHandoff`: the test observes the intentional intermediate Recovering state before `Engine.Recover` commits Starting. Do not weaken the production state machine.

The ShellCheck red is in the newly added privileged orchestration acceptance script.

The same audit also found proof gaps that CI redness alone does not expose:

- the sleep-source test injects its fake after observer goroutines already started;
- the NetworkManager reconnect test has no meaningful assertion and its fake does not execute the claimed failure path;
- the resume-while-recovery test cancels the observer context before queuing the events it claims to test;
- underlay restart tests duplicate a mini-supervisor instead of exercising `Manager.superviseUnderlayCoalescer`;
- raw netlink watch and semantic underlay convergence share one health bit/error, so one healthy goroutine can mask another failed one;
- the new `go-orchestration-acceptance.sh` does not follow the real core CLI/config/Go-ownership contract and cannot count as 07D acceptance yet.

### Ordered packets

Completed protocol packets:

1. `01-platform-linux-tun.md`;
2. `02-awg2-official-core.md`;
3. `03-awg2-isolated-interop.md`;
4. `04-xray-official-core.md`;
5. `05-xray-isolated-interop.md`;
6. **COMPLETE:** `06-multi-toad-isolated.md` — simultaneous real AWG2 + Xray + OpenConnect isolation gate.

6A. **IMPLEMENTATION PRESENT; STATUS MUST BE RE-AUDITED/CLOSED IN 07E:** `06a-current-head-baseline.md`.
The key Xray false-online fix described by 06A is now in `backend/xray`: TUN-UP alone no longer means connected; Xray inbound traffic counters establish the online session. Current `linux-xray-lifecycle` and `linux-xray-interop` jobs are green. Do not reimplement the old packet blindly; 07E must update its stale status from current evidence after the whole HEAD is green.

7A. **IMPLEMENTED/AUDITED; REVALIDATE AFTER 07E GREEN:** `07a-authoritative-state-and-capabilities.md`.
No new 07A design work is planned.

7B. **IMPLEMENTED/AUDITED; REVALIDATE AFTER 07E GREEN:** `07b-routing-parking-failclosed.md`.
No new 07B design work is planned.

7C. **REOPENED FOR PROOF CLOSURE:** `07c-underlay-resume-networkmanager.md`.
Most production implementation is present, but observer diagnostics and several deterministic tests are not yet trustworthy enough to call automated acceptance complete.

7D. **REOPENED FOR PRIVILEGED GATE REBUILD:** `07d-privileged-cutover-acceptance.md`.
Desired-state persistence, startup restore and API-aware shell cutover are present. The newly created privileged gate must be rebuilt from the proven multi-Toad fixture and actually run successfully.

7E. **IMPLEMENTATION LANDED; LOCAL CLOSURE PENDING:** `07e-current-head-proof-closure.md`.
The implementation work is present. The executor must finish local formatting/ShellCheck and privileged hermetic gates. GitHub Actions are reviewer-side evidence and must not block executor progress.

7F. **NEXT AFTER 07E LOCAL GATES:** `07f-cross-platform-release-packaging.md`.
Build one canonical release payload before any real installed-host staging. Linux and macOS are supported packaging targets. Linux must locally produce and verify the complete installable artifact (UI + core + Toad + system integration) and is the only 07F prerequisite for Linux 08A. macOS package implementation is required; native macOS install smoke is recorded separately if the executor is not on macOS. Windows gets packaging/build scaffolding only and remains disabled/experimental.

8A. **FUTURE / OPERATOR-GATED AFTER 07F-LINUX:** `08a-linux-installed-host-staging.md`.
Execute only after 07E local acceptance is closed and the 07F Linux artifact has been built/tested locally from the same HEAD. This packet covers installation of the exact 07F Linux artifact, read-only installed-host preflight, reversible Go cutover, real core restart, desired-state persistence and optional real suspend/resume. It never retires legacy automatically.

8B. **FUTURE / OPERATOR-GATED:** `08b-observation-rollback-and-retirement.md`.
Execute only after 08A evidence is reviewed. It defines an operator-selected observation window, rollback-confidence review and a separate explicit decision about `retire-legacy --confirm`.

The umbrella `07-go-reconcile-resume.md` is **superseded and must not be executed directly**.

### Executor rules for the current sequence

- start from the actual branch HEAD; never reset to a historical reviewed SHA;
- finish the remaining 07E local gates from `07e-current-head-proof-closure.md` before packaging;
- do not query/wait for GitHub Actions as an executor gate; use local commands and record their results;
- after 07E local closure, execute `07f-cross-platform-release-packaging.md` before any 08A installed-host work;
- 08A must consume a verified 07F artifact from the same HEAD, never an ad-hoc source checkout install;
- do not reimplement 06A/07A/07B from old prose when the code is already present;
- fix proof quality, not only red CI;
- do not weaken a real interop, fail-closed or state-machine assertion;
- do not call a created-but-unrun script an acceptance gate;
- privileged namespace tests are allowed when explicitly assigned by 07E;
- do not automatically suspend the developer workstation;
- do not perform installed-host cutover without explicit operator authorization;
- never run `retire-legacy --confirm` without a separate explicit operator decision;
- update roadmap/status from code and recorded **local command evidence**; CI may be appended later by a reviewer but is not required for executor progress.

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
