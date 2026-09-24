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

Current implementation/test baseline before this roadmap evidence update:

`21eeac5`

`21eeac5` adds the explicit privileged-derived regression gate on top of the Phase C fix `4a6e8fe`.

Fresh privileged evidence at `e49e039` verifies 07F.2G and the complete Phase B contract: AWG fail-closed recovery passes and both Xray/OpenConnect preserve TUN identity. The next blocker is Phase C visibility: same-tick AWG address repair hides the required structural `route_ready=false` transition. `4a6e8fe` publishes degradation before repair.

Current packet:

`docs/toad-steps/07f2h-structural-drift-publication.md`

Current audited state:

- 07F.1 packaging hardening remains implemented and locally green;
- 07F.2A is closed as a capability split rather than a rootless-kernel promise;
- `run-isolated.sh` never self-elevates;
- `run-rootless.sh` now exposes only `model` and diagnostic `probe`;
- the diagnostic probe repeatedly and reproducibly fails only at mapped userns creation in this executor:
  `unshare: write failed /proc/self/uid_map: Operation not permitted`;
- therefore real netns/TUN/protocol acceptance is no longer retried through rootless mode;
- repository-root `run-privileged-gates.sh` is the authoritative operator kernel gate and now owns:
  user-owned prebuild, stale-fixture cleanup, route-parking, multi-toad, orchestration-acceptance, hermetic Xray interop, per-gate logs, and host-state before/after verification;
- privileged evidence at `46c7aaa` exposed the original AWG Phase B validation defect;
- 07F.2B fixed AWG health-aware validation;
- 07F.2C restored structural `route_ready`, added non-terminal validation-pending hand-off, and strengthened Phase B fail-closed routing assertions;
- fresh privileged evidence at `873ae7f` proved that AWG now stays fail-closed/recoverable during outage and returns Ready/current-epoch after restoration;
- that run exposed the OpenConnect async full-restart replay, fixed in 07F.2D;
- fresh privileged evidence at `db20949` proves the replay is gone: replacement startup remains at a single `start-transport` handoff with no second Quiesce/Restart transaction;
- the same run exposed a fixture topology defect: production endpoint policy correctly routes the OpenConnect endpoint through the canonical synthetic underlay, but the AWG/Xray gateway namespaces had no path to the OC server namespace;
- 07F.2E adds a narrow no-NAT routed transit for the real OpenConnect endpoint through both synthetic underlays;
- fresh privileged evidence at `fa799a4` proves that routed OC fixture and OpenConnect replacement recovery now work, then exposes only an Xray identity regression: `kk-xray0` ifindex changed `3 -> 7`;
- 07F.2F makes Xray `Rebindable`, preserving the embedded official-Xray instance/TUN while core-owned endpoint routing moves to the new underlay;
- fresh privileged evidence at `91c642a` proves the Xray identity check now passes and exposes the same unnecessary full-restart behavior for OpenConnect (`kk-oc0` ifindex `4 -> 6`);
- 07F.2G makes OpenConnect `Rebindable`, leaving its official reconnect loop/child-owned TUN alive while core endpoint routing moves to the new underlay;
- fresh privileged evidence at `e49e039` emits `Phase B PASS`, proving the full canonical-underlay mutation contract including stable Xray/OpenConnect identities;
- the same run reaches Phase C and exposes an ordering bug: AWG address drift is repaired in the same health tick, so the fail-closed `route_ready=false` state is never published;
- 07F.2H splits detection/publication from same-ifindex repair by at least one health-loop tick;
- privileged failures are now required to become focused deterministic regressions before another sudo-run is requested; `linux/tests/toad/privileged-regressions-model.sh` collects these and `run-rootless.sh model` runs it automatically;
- all non-privileged gates, including the privileged-derived regression model, are green for the current implementation;
- **remaining blocker is one fresh operator run of `bash run-privileged-gates.sh` against the current HEAD**. Until that is green, 07F.2 / 07F-Linux remain incomplete.

GitHub Actions are not an executor gate. Local command evidence is authoritative for executor progress.


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

7E. **NON-PRIVILEGED LOCAL GATES GREEN; KERNEL EVIDENCE IS OPERATOR-PRIVILEGED:** `07e-current-head-proof-closure.md`.
The production/test implementation is present. The executor remains sudo-free; real kernel/network evidence is collected only by the explicit operator privileged runner.

7F. **IMPLEMENTATION LANDED:** `07f-cross-platform-release-packaging.md`.
Canonical Linux/macOS builders and Windows scaffold exist.

7F.1. **IMPLEMENTED:** `07f1-release-artifact-hardening.md`.
Linux install-complete package/version contract, macOS static staging and Windows scaffold hardening are present.

7F.2A. **CLOSED — MODEL/PROBE ROOTLESS, KERNEL GATES PRIVILEGED:** `07f2a-rootless-hermetic-test-runner.md`.
The no-sudo executor layer is complete. Mapped userns is unavailable in the current executor, so rootless kernel gate modes are intentionally retired rather than retried. `run-rootless.sh model` is the deterministic executor gate; `run-rootless.sh probe` is capability diagnostics only.

7F.2. **ACTIVE THROUGH 07F.2H; WAITING FOR FRESH PRIVILEGED ACCEPTANCE:** `07f2-orchestration-underlay-fixture.md`.
Synthetic underlay, endpoint non-recursion and the real protocol fixtures are implemented. Phase B is now privileged-green. The current proof gap is Phase C structural-drift publication before automatic same-ifindex repair. The remaining proof is a fresh `run-privileged-gates.sh` run covering route-parking, multi-toad, orchestration A-F and hermetic Xray.

7F.2C. **IMPLEMENTED; AWG PHASE B BEHAVIOR PRIVILEGED-VERIFIED:** `07f2c-stable-tun-recovery-handoff.md`.
Fresh evidence at `873ae7f` proves the intended AWG behavior: structural route readiness remains stable, selected traffic stays fail-closed without physical fallback, and restoration reaches Ready/current epoch. Full 07F.2 suite closure moved to the independent blocker below.

7F.2D. **IMPLEMENTED; REPLAY BEHAVIOR PRIVILEGED-VERIFIED:** `07f2d-async-full-restart-handoff.md`.
Fresh evidence at `db20949` proves a full Toad replacement is handed off once to the replacement runtime/process supervisor: no second recovery transaction reaches Quiesce/Restart before control.sock exists.

7F.2E. **IMPLEMENTED; ROUTED OPENCONNECT RECOVERY PRIVILEGED-VERIFIED:** `07f2e-routed-openconnect-underlay-fixture.md`.
Fresh evidence at `fa799a4` proves the real OpenConnect endpoint is reachable through both synthetic underlays and the replacement role returns Ready/current epoch. The suite then fails only on the independent Xray identity assertion below.

7F.2F. **IMPLEMENTED; XRAY TUN IDENTITY PRIVILEGED-VERIFIED:** `07f2f-xray-underlay-rebind.md`.
Fresh evidence at `91c642a` proves Xray keeps the same TUN identity across the canonical underlay cycle.

7F.2G. **IMPLEMENTED; OPENCONNECT TUN IDENTITY PRIVILEGED-VERIFIED:** `07f2g-openconnect-underlay-rebind.md`.
Fresh evidence at `e49e039` proves OpenConnect keeps the same managed TUN through the canonical underlay cycle and Phase B completes.

7F.2H. **IMPLEMENTED; PRIVILEGED VERIFICATION REQUIRED:** `07f2h-structural-drift-publication.md`.
Managed structural drift is now published fail-closed before automatic same-ifindex repair, so core can observe and act on the degraded route target instead of seeing only the already-repaired snapshot.

8A. **FUTURE / OPERATOR-GATED AFTER PRIVILEGED-GREEN 07F.2:** `08a-linux-installed-host-staging.md`.
Execute only after 07F.2 makes orchestration-acceptance green and the exact Linux `.deb`/SHA256/package-preservation contract is green locally from the same HEAD. The default real-host production scope is AmneziaWG + OpenConnect. A real external Xray endpoint is not required for 08A; an unvalidated Xray role stays disabled.

8B. **FUTURE / OPERATOR-GATED:** `08b-observation-rollback-and-retirement.md`.
Execute only after 08A evidence is reviewed. It defines an operator-selected observation window, rollback-confidence review and a separate explicit decision about `retire-legacy --confirm`.

8C. **DEFERRED / OPERATOR-GATED / NON-BLOCKING FOR 08A:** `08c-external-xray-validation.md`.
Run only when the operator can provide a real Xray/VLESS/REALITY profile and reachable remote endpoint. Until then the required automated Xray proof is the existing hermetic official-Xray netns fixture. 08C does not block AmneziaWG + OpenConnect production staging.

The umbrella `07-go-reconcile-resume.md` is **superseded and must not be executed directly**.

### Executor rules for the current sequence

- start from the actual branch HEAD; never reset to a historical reviewed SHA;
- current packet is `07f2h-structural-drift-publication.md`; 07F.2A is already closed as a model/probe-vs-privileged split;
- do not query/wait for GitHub Actions as an executor gate; use local commands and record their results;
- do not ask the executor for sudo; run deterministic/model gates unprivileged, record the single rootless probe capability result, and leave real kernel/network acceptance to the operator `run-privileged-gates.sh`;
- never require a public/remote Xray server for automated acceptance; use the pinned official local Xray fixture;
- do not weaken AWG handshake health or widen its freshness window to make the fixture pass;
- do not start 08A until 07F.2 is complete;
- for default 08A staging, require real AmneziaWG + OpenConnect only; keep Xray desired=false until 08C or explicit operator-provided endpoint validation;
- 08A must consume a verified 07F artifact from the same HEAD, never an ad-hoc source checkout install;
- do not reimplement 06A/07A/07B from old prose when the code is already present;
- fix proof quality, not only red CI;
- do not weaken a real interop, fail-closed or state-machine assertion;
- do not call a created-but-unrun script an acceptance gate;
- privileged namespace tests are operator-only and must go through the tracked repository-root `run-privileged-gates.sh`; do not duplicate them through rootless mode;
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
