# Kikimora Toad roadmap

This is the canonical living roadmap for the production managed-VPN client path in PR #27.

A **Toad** is one Kikimora-managed VPN client/runtime instance. Kikimora orchestrates Toads; Leshy owns routing/DNS policy; protocol correctness comes from official protocol cores.

Repository documents, not chat history, are the source of truth. Update this file when architecture, status, dependency pins, acceptance gates, or the immediate implementation horizon change.

## Current direction

- runtime/control-plane language: Go;
- cross-platform runtime root: `toad/`;
- per-protocol executable: `kikimora-toad`;
- central daemon: `kikimora-core`;
- chapter 08 product surface: installed console/runtime only (`kk`, `kikimora-core`, `kikimora-toad`); an existing Qt 6/QML prototype remains in `desktop/`, but UI acceptance is deferred to chapter 09;
- one Toad process = one independently supervised managed VPN instance;
- production Linux ownership remains legacy/external until the installed-host 08A cutover/rollback gate is explicitly authorized and passes;
- the Go endpoint/routing/parking/recovery path is **privileged-hermetic accepted on Linux**, but it is not yet the production owner on the developer workstation;
- routing/DNS classification remains Leshy; the Go core is taking lifecycle, endpoint-underlay, parking/publication coordination in ordered stages;
- external non-Toad VPNs such as a corporate `vpn0` remain externally owned;
- Windows remains UI/FakeCore scope **today**; real Windows networking may be activated only after the native Windows routing/TUN/DNS substrate exists and the mandatory Windows VM parity packet `08a4-windows-vm-console-lifecycle-acceptance.md` passes; Linux remains the first production acceptance platform and macOS needs its own privileged parity gate.

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

Stage 0 and the 07A-07F.2 Linux hermetic/control-plane acceptance sequence are complete. Chapter 08 now closes the **console/runtime product** on a disposable VM before any workstation cutover: real Toads, installed CLI orchestration, OS lifecycle recovery, stable machine-readable state exposure, and a final Linux `.deb`. UI work is explicitly deferred to chapter 09.

The current branch has advanced through the installed console/API surface, package lifecycle work, hypervisor lifecycle gates and the suspend/underlay recovery fixes through `a4055e6`. The last full authoritative privileged hermetic kernel/network suite remains recorded at `846335d18a71e9da81211b319f6831acf3ffba24`; the disposable VM has also completed the four privileged gates on the current runtime line.

Therefore distinguish two acceptance statements:

- **privileged hermetic Linux control plane:** accepted at `846335d`;
- **real installed-host production ownership:** still pending 08A operator-gated staging/cutover.

Protocol foundation:

- [x] Go Toad skeleton/config/state/platform abstraction;
- [x] Linux Toad-owned AWG TUN lifecycle;
- [x] official AmneziaWG backend and real isolated interop;
- [x] official Xray backend and real REALITY/VLESS/Vision interop;
- [x] OpenConnect backend supervising official openconnect + hermetic ocserv interop;
- [x] simultaneous AWG2 + Xray + OpenConnect multi-Toad gate;
- [x] Stage 0 protocol isolation complete.

Control-plane implementation and hermetic Linux acceptance:

- [x] Go desired/observed controller and revisioned API;
- [x] per-Toad control IPC and generation snapshots;
- [x] process supervisor/backoff;
- [x] endpoint/routing/parking/Leshy package boundaries;
- [x] Linux netlink underlay watcher and route executor;
- [x] systemd ownership/cutover scaffolding;
- [x] revisioned local core API suitable for CLI and future UI (`Handshake`, snapshot, subscribe, structured commands);
- [~] Qt/QML real-core prototype exists, but it is not a chapter-08 acceptance or packaging requirement; UI closure moves to chapter 09;
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

Current local evidence is stronger than the original 2026-09-21 audit:

- full 07E non-privileged acceptance is green;
- all four authoritative privileged gates are green on `846335d`, including orchestration phases A-F and final host-state equality;
- side-by-side `kikimora-next` packaging/collision tests are green;
- on the Ubuntu 26.04 VM testbed, `run-rootless.sh model` is green on the accepted runtime line and the host-network-dependent IPC test passes under `-race -count=20`;
- the VM privileged gate set is green 4/4 with clean host-state restoration;
- real system-wide AWG passes Google, Telegram, ChatGPT trace and unauthenticated OpenAI API probes;
- real system-wide OpenConnect reaches the internal GitLab endpoint and cleans up correctly;
- real Xray proves TUN/routing plus Google and Telegram traffic, then hits the operator's already-known ISP DPI limitation at the ChatGPT DNS/application step; external Xray remains non-blocking because the hermetic official-Xray gate is authoritative.

Do not infer workstation production completion from these results. The next required evidence is 08A.2: installed console orchestration plus real VM OS-lifecycle acceptance, followed by package lifecycle proof and only then workstation staging.

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

Current privileged-accepted staging baseline:

`846335d18a71e9da81211b319f6831acf3ffba24`

Fresh privileged evidence on 2026-09-25 passes all four authoritative gates on that HEAD: route-parking, multi-toad, orchestration-acceptance phases A-F, and hermetic Xray interop. Final cleanup reports host state unchanged. The complete 07E non-privileged Phase 7 suite is also green after the narrow ShellCheck proof cleanup below.

Current execution packet:

`docs/toad-steps/08a2-vm-console-lifecycle-acceptance.md`

Umbrella installed-host packet remains `docs/toad-steps/08a-linux-installed-host-staging.md`.

Current accepted chapter-08 Linux artifact source HEAD:

`66a9a93` (runtime suspend/underlay fix is `a4055e6`; later commits add acceptance docs/harness only)

### Current Ubuntu VM acceptance lane

A disposable GUI Ubuntu VM at `192.168.1.236` is the immediate pre-production acceptance target before touching the developer workstation. The VM is now a **full console-product acceptance environment**, not merely a place to rerun protocol scripts.

Recorded baseline:

- Ubuntu 26.04.1 LTS, kernel `7.0.0-38-generic`;
- VM underlay `enp0s3`, `192.168.1.236/24`, default gateway `192.168.1.1`;
- branch `feat/native-core-vpn-clients`; real-VPN application acceptance recorded at `6c01dcb`;
- Go `1.26.0`, Git/build tools, OpenConnect/ocserv, slirp4netns, tcpdump, curl, OpenSSL and required network/system tools installed;
- local secret profiles for AWG, VLESS/REALITY and OpenConnect are present only as ignored mode-`0600` VM files;
- the VM has already completed the privileged gate set and real protocol/data-plane stage with clean rollback/cleanup;
- VirtualBox host control is available for pause/resume, reset, ACPI power, save-state and virtual-NIC link state, while Guest Additions/SSH provide guest observation.

Recorded real protocol outcome:

- AWG: PASS, including Telegram and ChatGPT/OpenAI application probes;
- OpenConnect: PASS, including real HTTPS to `gitlab.sca.ad-tech.ru`;
- Xray: integration/data-plane works for Google + Telegram, then the known ISP DPI limitation blocks the ChatGPT path; classify this as external-network evidence, not a Kikimora orchestration regression.

Current VM packet:

`docs/toad-steps/08a2-vm-console-lifecycle-acceptance.md`

Required sequence now is:

1. install an exact package-built candidate in the VM;
2. expose the full console orchestration surface over the same versioned core API used by future UI;
3. run real AWG + OpenConnect under **Kikimora core orchestration**, not standalone protocol scripts;
4. drive core/Toad crashes, NetworkManager restart, NIC loss/restoration, OS suspend/resume, VM pause/save-state/reset, reboot, shutdown/start and cold boot;
5. verify persistent desired state, fail-closed routing, independent Toad ownership, clean routes/rules and revisioned state/API reconnection after every event;
6. run package install/upgrade/remove/reinstall acceptance;
7. produce the final chapter-08 Linux console/runtime `.deb` and SHA-256;
8. only then return to workstation side-by-side/cutover work.

The earlier rootless flake analysis remains valid background: `55d4782` removed accidental live-host observation from `TestCoreIPCUsesFakeToadBinaryWithoutNetwork`; do not widen timeouts to hide fixture defects.

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
- fresh privileged evidence at `f7ac441` proves Phase C now passes and exposes the next failure in Phase D: OpenConnect negotiated-address loss stayed structurally unready while generic recovery selected Rebind/Validate semantics instead of recreating the missing route target;
- 07F.2I makes structural `RouteReady=false` override Rebind: stable-TUN restart wins when available, otherwise full Toad/process restart recreates the route target;
- polling assertion stderr is suppressed so expected wait iterations no longer flood privileged logs with duplicate Python tracebacks;
- fresh privileged evidence at `287335d` emits Phase D PASS and reaches Phase E;
- Phase E exposes a generation-boundary authority bug: Xray replacement starts with a new TUN/process generation but inherits the old generation's `ValidatedEpoch`, so no fresh validation is scheduled and product state remains Starting;
- 07F.2J clears generation-bound validation authority in `BeginToadGeneration` and proves a replacement `RouteReady=true` snapshot can start fresh validation bound to the new generation;
- fresh privileged evidence at `df838db` emits Phase E PASS and reaches Phase F;
- Phase F restores desired intent correctly but exposes stale parking checkpoint identity after core restart because replacement AWG/Xray Toads have new ifindexes;
- 07F.2K discards stale old-ifindex ownership/baseline evidence while preserving only parks still verified in the kernel, then rewrites the checkpoint against the current route-target identity;
- an already-restored active park is no longer cleared by an empty replacement-interface ownership set;
- fresh privileged evidence at `6b43e91` reaches a correct Phase F product snapshot and host-state PASS, proving the checkpoint restart behavior;
- the orchestration gate stayed red only because its embedded Python predicate used `false` instead of `False`;
- 07F.2L fixes that harness typo and adds `check-orchestration-predicates.py`, which compiles and token-checks all 12 `mpf_wait_snapshot` Python blocks before privileged execution;
- the checker is part of `privileged-regressions-model.sh` and `run-rootless.sh model`;
- fresh privileged evidence at `846335d` emits `Phase F PASS`, `=== ALL PHASES PASSED ===`, all four gate PASS results, final cleanup, and `host state unchanged: PASS`;
- **07F.2 is complete. 08A may proceed through Gate 0 and read-only preflight; installed-host mutation still requires explicit operator authorization.**

GitHub Actions are not an executor gate. Local command evidence is authoritative for executor progress.


### Ordered packets

Completed protocol packets:

1. `01-platform-linux-tun.md`;
2. `02-awg2-official-core.md`;
3. `03-awg2-isolated-interop.md`;
4. `04-xray-official-core.md`;
5. `05-xray-isolated-interop.md`;
6. **COMPLETE:** `06-multi-toad-isolated.md` — simultaneous real AWG2 + Xray + OpenConnect isolation gate.

6A. **COMPLETE / PRIVILEGED-RUNTIME BASELINE GREEN:** `06a-current-head-baseline.md`.
Xray keeps structural TUN readiness separate from tunneled-session proof; the hermetic official-Xray interop gate is green on privileged baseline `846335d`. The disposable VM has since rerun the privileged acceptance lane successfully, and later current-branch changes are packaging/docs/real-test harness changes rather than a reopened protocol implementation phase.

7A. **COMPLETE / REVALIDATED:** `07a-authoritative-state-and-capabilities.md`.
Authoritative state, generation-bound validation and executable capability selection are covered by the current deterministic and privileged suites.

7B. **COMPLETE / REVALIDATED:** `07b-routing-parking-failclosed.md`.
Endpoint reconciliation, selected-route ownership and fail-closed parking are covered by the current deterministic and privileged route-parking/orchestration suites.

7C. **AUTOMATED PROOF COMPLETE; REAL SUSPEND/RESUME OPERATOR-GATED:** `07c-underlay-resume-networkmanager.md`.
Current local tests and privileged orchestration prove underlay convergence, structural drift detection/repair and current-epoch recovery. Real workstation suspend/resume remains an 08A operator action.

7D. **PRIVILEGED HERMETIC ACCEPTANCE COMPLETE; INSTALLED-HOST CUTOVER PENDING:** `07d-privileged-cutover-acceptance.md`.
Desired-state persistence, startup restore, crash recovery and phases A-F are green in the authoritative privileged suite.

7E. **COMPLETE — LOCAL AUTOMATED CLOSURE GREEN:** `07e-current-head-proof-closure.md`.
Full Go test/race/vet, cutover fixtures, completions, JSON API and ShellCheck are green locally; privileged kernel/network evidence is also green.

7F. **LINUX RELEASE PACKAGING COMPLETE FOR 08A:** `07f-cross-platform-release-packaging.md`.
Canonical Linux packaging is complete; macOS/Windows retain their separately documented platform scope.

7F.1. **COMPLETE FOR LINUX 08A PACKAGING CONTRACT:** `07f1-release-artifact-hardening.md`.
Linux install-complete package/version contract is green; exact staging artifacts are produced from the selected 08A staging HEAD.

7F.2A. **CLOSED — MODEL/PROBE ROOTLESS, KERNEL GATES PRIVILEGED:** `07f2a-rootless-hermetic-test-runner.md`.
The no-sudo executor layer is complete. Mapped userns is unavailable in the current executor, so rootless kernel gate modes are intentionally retired rather than retried. `run-rootless.sh model` is the deterministic executor gate; `run-rootless.sh probe` is capability diagnostics only.

7F.2. **COMPLETE — ALL FOUR PRIVILEGED GATES GREEN ON 846335d:** `07f2-orchestration-underlay-fixture.md`.
Synthetic underlay, endpoint non-recursion, real protocol fixtures, phases A-F, crash/restart recovery, route parking and hermetic Xray all pass in one authoritative operator run; cleanup leaves host state unchanged.

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

7F.2H. **IMPLEMENTED; PHASE C PRIVILEGED-VERIFIED:** `07f2h-structural-drift-publication.md`.
Fresh evidence at `f7ac441` proves AWG address drift is observed fail-closed and repaired with the same ifindex.

7F.2I. **IMPLEMENTED; PHASE D PRIVILEGED-VERIFIED:** `07f2i-route-loss-vs-rebind.md`.
Fresh evidence at `287335d` proves OpenConnect negotiated-address loss selects real recovery and Phase D completes.

7F.2J. **IMPLEMENTED; PHASE E PRIVILEGED-VERIFIED:** `07f2j-generation-validation-invalidation.md`.
Fresh evidence at `df838db` proves an Xray Toad replacement completes fresh generation-bound validation and Phase E passes.

7F.2K. **IMPLEMENTED; PHASE F PRODUCT STATE PRIVILEGED-VERIFIED:** `07f2k-parking-checkpoint-rebind.md`.
Fresh evidence at `6b43e91` shows AWG/Xray Ready on replacement interfaces after core restart, OpenConnect desired=false/Stopped, aggregate Ready, no stale checkpoint errors, and host state unchanged.

7F.2L. **COMPLETE / PRIVILEGED CLOSURE VERIFIED:** `07f2l-orchestration-predicate-validation.md`.
All embedded `mpf_wait_snapshot` Python predicates are locally checked; the fresh privileged run emits Phase F PASS and ALL PHASES PASSED.

8A. **CURRENT — CLOSE THE INSTALLED CONSOLE/RUNTIME PRODUCT BEFORE WORKSTATION CUTOVER:** `08a-linux-installed-host-staging.md`.

8A.1. **NEXT / OPERATOR-GATED — SIDE-BY-SIDE WORKSTATION STAGING:** `08a1-side-by-side-installed-staging.md`.
The disposable-VM console/runtime acceptance and final canonical package are now complete. Return to the workstation only through the documented side-by-side/preflight sequence. Read-only preflight may run immediately; installation, ownership cutover and legacy retirement still require explicit operator authorization at their mutation boundaries.

8A.2. **COMPLETE — VM CONSOLE ORCHESTRATOR + OS LIFECYCLE:** `08a2-vm-console-lifecycle-acceptance.md`.
Installed-console baseline, core/Toad recovery, NetworkManager restart, repeated physical-link recovery, reboot/cold boot, VirtualBox pause/resume, save-state/start and crash/reset recovery, bounded soak and real application probes are green. Suspend/resume exposed and fixed the `WaitingForUnderlay` recovery gap in `a4055e6`; two post-fix real OS suspend/resume cycles returned AWG + OpenConnect automatically to Ready/current epoch with no manual reconnect.

8A.3. **COMPLETE — FINAL LINUX CONSOLE/RUNTIME PACKAGE:** `08a3-linux-console-package.md`.
The final canonical package is `dist/08a-final-66a9a93/kikimora_1.0.0_amd64.deb`, SHA-256 `758156aff047c55392575298ce2c08fa55f9fb02d3661ecc0e3a24c45712d75c`. Static package contract, fresh install semantics, real upgrade, remove/reinstall, purge/reinstall and final installed-package AWG/OpenConnect smoke all pass on the disposable VM.

8A.4. **PLANNED / WINDOWS REAL-NETWORKING ACTIVATION GATE — BLOCKED BY NATIVE WINDOWS SUBSTRATE:** `08a4-windows-vm-console-lifecycle-acceptance.md`.
Windows remains FakeCore-only until native Windows TUN/routing/DNS ownership, authenticated local IPC and a real installer exist. Once that implementation is present, a disposable Windows VM must repeat the same class of evidence as Linux: installed CLI/core/Toads, real AWG + OpenConnect application probes, core/Toad crash recovery, DHCP/link loss, two suspend/resume cycles, hibernate when supported, reboot/cold boot before desktop login, VirtualBox pause/save/reset/crash recovery, bounded soak, no-accumulation checks and installer install/upgrade/uninstall/purge lifecycle. Passing UI/FakeCore tests never satisfies 08A.4.

8B. **FUTURE / OPERATOR-GATED:** `08b-observation-rollback-and-retirement.md`.
Execute only after 08A evidence is reviewed. It defines an operator-selected observation window, rollback-confidence review and a separate explicit decision about `retire-legacy --confirm`.

8C. **PARTIAL REAL-NETWORK EVIDENCE RECORDED / KNOWN ISP DPI BLOCK / NON-BLOCKING FOR 08A:** `08c-external-xray-validation.md`.
At `6c01dcb`, the VM Xray system-wide run created the real route target and carried real Google and Telegram HTTPS traffic, then failed resolving/reaching the ChatGPT path in the operator's provider network. This matches the already-established ISP behavior that cuts Xray protocols. Treat it as an external-network limitation, not a replacement for or failure of the mandatory hermetic official-Xray gate. 08C remains non-blocking for the AWG + OpenConnect chapter-08 product.

9. **FUTURE — DESKTOP/UI OVER THE ACCEPTED CONTROL PLANE:** `09-desktop-ui.md`.
UI/UX, tray behavior and desktop packaging begin only after the chapter-08 console/runtime package is accepted. The UI consumes the existing versioned Snapshot/Subscribe control channel and does not own orchestration semantics.

The umbrella `07-go-reconcile-resume.md` is **superseded and must not be executed directly**.

### Executor rules for the current sequence

- start from the actual branch HEAD; never reset to a historical reviewed SHA;
- current execution packet is `08a2-vm-console-lifecycle-acceptance.md`; 07F.2 and the VM privileged/real-protocol prechecks are complete;
- do not query/wait for GitHub Actions as an executor gate; use local commands and record their results;
- do not ask the executor for sudo; run deterministic/model gates unprivileged, record the single rootless probe capability result, and leave real kernel/network acceptance to the operator `run-privileged-gates.sh`;
- never require a public/remote Xray server for automated acceptance; use the pinned official local Xray fixture;
- do not weaken AWG handshake health or widen its freshness window to make the fixture pass;
- do not start 08A until 07F.2 is complete;
- for default 08A staging, require real AmneziaWG + OpenConnect; Xray external reachability is non-blocking in the known DPI-constrained provider network while hermetic Xray acceptance stays mandatory;
- 08A lifecycle acceptance must run from an installed package-built candidate, never from ad-hoc source-tree binaries;
- chapter 08 is console/runtime only: do not make Qt/UI build, UI behavior or Qt runtime dependencies a release gate for the final console `.deb`;
- preserve and test the versioned local state/control API because chapter 09 UI will consume it directly;
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

Independent hermetic gate exists and covers official Xray REALITY/VLESS/Vision data traffic and recovery. The earlier false-online defect, where Xray health could be inferred from TUN-UP instead of real session proof, was fixed in 06A and is covered by current automated acceptance. Real external VLESS/REALITY validation is an additional 08C/operator layer, not a replacement for the hermetic gate.

### OpenConnect

The independent hermetic gate uses official `openconnect` against `ocserv`, a route-free vpnc script and a stable named `kk-oc0` route target. CI credentials are synthetic; real token/password material never belongs in the repository.

### Combined gate

Stage 0 is complete. `06-multi-toad-isolated.md` runs all three managed clients concurrently and proves:

- three live Toad processes;
- three distinct TUNs;
- real traffic through all three official protocol paths;
- one server/underlay/Toad failure does not disturb the other two;
- failed selected traffic cannot fall through another Toad or physical underlay;
- no ambient default/split-default route is installed by a protocol core.

Later 07F.2 privileged orchestration acceptance builds on that completed protocol gate rather than reopening Stage 0.

## Control-plane direction after standalone clients

Stage 0 originally kept the existing Bash Kikimora as orchestrator while the standalone protocol clients were proven. The post-Stage-0 07A-07F.2 sequence has since implemented and privileged-accepted the explicit Go control contract on the hermetic Linux path.

The 2026-09-21 legacy suspend/resume failure remains useful architectural background in `docs/toad-resume-recovery-architecture.md`, but step 07 is no longer future work. The immediate boundary is now 08A.2 real VM lifecycle acceptance of the installed console/runtime product, followed by workstation ownership/cutover and 08B observation/retirement. UI begins only in chapter 09.

The implemented Go control contract includes:

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

## Console/state-channel boundary and chapter 09 UI

Chapter 08 deliberately ends at a usable installed **console/runtime product**. The existing Qt 6/QML code is a prototype/reference consumer, not a release gate for this chapter.

Before UI work starts, chapter 08 must lock down:

- one installed `kk` console control surface;
- `kikimora-core` + independently supervised Toads;
- API Handshake/capabilities;
- canonical machine-readable snapshot;
- monotonic revision;
- revisioned Subscribe stream;
- structured reason/error codes;
- reconnect/resubscribe behavior across core restart and OS lifecycle events;
- secret-free diagnostics/state;
- final Linux console/runtime `.deb`.

The UI chapter is `docs/toad-steps/09-desktop-ui.md`. It must consume the same local API directly and must not scrape CLI output or independently infer VPN truth from process/TUN/NetworkManager state.

Preferred Linux packaging boundary after chapter 08:

```text
kikimora        -> core + Toads + kk + Linux integration
kikimora-ui     -> optional Qt UI depending on a compatible kikimora runtime
```

The packages may be merged later for distribution convenience only if the console/runtime package remains independently testable and installable.

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
