# Kikimora Toad roadmap

This is the canonical living roadmap for the production managed-VPN client path in PR #27.

A **Toad** is one Kikimora-managed VPN client/runtime instance. Kikimora orchestrates Toads; Leshy owns routing/DNS policy; protocol correctness comes from official protocol cores.

Repository documents, not chat history, are the source of truth. Update this file when architecture, status, dependency pins, acceptance gates, or the immediate implementation horizon change.

## CURRENT HANDOFF / START HERE — 2026-10-14

> **2026-10-14 code-vs-docs audit.** A full cross-check of this roadmap, the
> plans and the step packets against the actual source is recorded in
> [`toad-code-docs-audit-2026-10-14.md`](toad-code-docs-audit-2026-10-14.md).
> Read it before trusting any "COMPLETE" banner in this file: it separates
> genuinely-closed work from overstated claims and lists remaining
> contradictions. The committed merge-conflict markers that previously sat here
> have been resolved, and the stale 08C "AWAITING SECRET" wording was corrected.
>
> **Overstatement corrections applied (audit §7).** Each was fixed in its source
> document and marked with a `2026-10-14 audit` note:
> - `K*` design system, functional Routing/Leshy + Settings pages, Windows
>   `SIMULATED` marker, ten FakeCore fixtures and `available_actions` drawer
>   behavior do **not** exist → flagged in `docs/roadmap.md` (U3/U6) and
>   `docs/desktop-ui-implementation-plan.md` §5/§6/§8/§9/§14.
> - CTest summary corrected to the five registered targets (this file, above).
> - Chapter 09 "FUTURE" reconciled to "ACTIVE prototype"
>   (`docs/toad-steps/09-desktop-ui.md`).
> - 08A.4a packet annotated with per-item VM results and its OpenConnect
>   BLOCKED caveat (`docs/toad-steps/08a4a-windows-native-networking-substrate.md`).
> - 08A.3R scope caveats (no DNS write counters; IPv4-only soak)
>   (`docs/toad-steps/08a3r-linux-dual-stack-dns-route-regression.md`).
> - This file's privileged-gate list corrected to five (incl. `dualstack`).
> - `test-report.txt` marked SUPERSEDED / historical.
>
> Still genuinely OPEN and unimplemented: the UI items above, real DNS mutation
> counters, IPv6/endpoint-table soak assertions, and the Linux workstation
> deploy (08A.1/08B).

## HISTORICAL — 2026-10-11 (retained as evidence; not the current status)

### 2026-10-11 Linux gate hardening implementation (after source audit)

Updated `linux/tests/toad/leshy-dns-integration.sh` and `linux/tests/toad/vm-dualstack-regression.sh`:

- Leshy integration now rejects missing A/DNS resolution, missing snapshot, unmanaged interface mismatch, and split-default routes across all route tables. A bounded netlink route/rule monitor detects route churn even if counts remain equal. True DNS write-counter instrumentation and explicit active unchanged-classification replay remain **OPEN**; this is not full zero-mutation proof.
- IPv6 paths without a controlled dead-v6 fixture are recorded as observations, not false PASS. Negative-case verification belongs to the privileged netns gate.
- Installed VM script now requires explicit `AWG_PROBE_CMD` and `OC_PROBE_CMD` real role-specific application probes, run with 20s timeout before and after core restart; route-count drift, missing core status or malformed JSON now fail the gate. Public HTTP probes remain diagnostic only.
- Both scripts require fresh disposable-VM execution before the hardened test itself can be reported PASS. No developer workstation mutation authorized.
- Remaining: instrument actual Leshy DNS writes and classification replays; derive endpoints/sources from installed config; enforce per-role Ready, network-link/suspend recovery and meaningful soak; add production-path routing verification instead of relying on manually inserted netns routes.

### POST-PULL CODE / ACCEPTANCE AUDIT — 2026-10-11 (HEAD `a8d9ab4`)

**Audit basis:** fast-forward from `4567181` to `a8d9ab4`, review of the Linux 08A.3R evidence packet, the new Linux shell gates, Go netlink snapshot code, and Windows status claims. The statements below are **static source findings**; no new privileged VM tests were run as part of this audit.

**Actual progress:** the Linux 08A.3R model, privileged netns, Leshy/DNS and installed-VM scripts now exist and report PASS on a disposable Ubuntu VM. The Linux netlink snapshot changed to `RouteListFiltered(... RT_FILTER_TABLE)` to include endpoint table 51890, fixing a real blind spot that prevented endpoint-policy garbage collection/removal. Windows native service/underlay/Wintun/routes/AWG2/OpenConnect/packaging and cross-platform model tests are implemented, but Windows VM real-network acceptance is still pending.

**Acceptance weaknesses to resolve before calling the legacy-host regression definitively closed:**

1. **[HIGH] Linux Leshy zero-mutation is not demonstrated.** `linux/tests/toad/leshy-dns-integration.sh` compares route **counts** two seconds apart and prints `PASS: zero mutation`. Equal counts do not exclude delete/re-add of identical `/128` routes, route replace operations, rule churn, or DNS rewrites. Add event/write instrumentation (netlink monitor and Leshy/DNS mutation counters where available), repeat over a meaningful bounded interval and fail on unauthorized steady-state writes; capture actual classifications/reconcile cycles rather than only waiting.
2. **[HIGH] DNS and IPv6 assertions can silently pass without coverage.** In that script, empty A responses are warnings, missing AAAA is accepted without verifying the chosen fallback policy, an IPv6 route is considered safe merely because it does not contain `dev kk-`, and family-readiness parsing ends with `sys.exit(0)` or logs warnings. Demand a controlled dual-stack fixture and explicit pass/fail conditions for effective family route/source, fast failure, usable DNS, managed-state readiness, and an actual broken-v6 case; separate SKIP with reason when a platform lacks IPv6.
3. **[HIGH] Installed VM gate records observations instead of asserting live traffic/recovery.** `linux/tests/toad/vm-dualstack-regression.sh` logs HTTP probes without enforcing their exit/HTTP success, uses process presence rather than AWG/OC data-plane application tests, logs route-count drift as WARNING, and logs core state after restart without asserting Ready and per-role reconnection. Convert these to hard assertions with timeouts; if external endpoints are unavailable use explicit controlled fixtures and structured SKIP rather than false PASS.
4. **[MEDIUM] VM gate is environment-specific and insufficiently comprehensive.** It hardcodes two transport endpoint IPv4 addresses and Leshy port, collects only three 2-second route samples, does not demonstrate real suspend/resume or link/DHCP recovery, and does not assert endpoint source selection after restart. Derive endpoints and family capabilities from installed config/status; preserve before/after evidence and test recovery against expected role state.
5. **[MEDIUM] Separate netns helper coverage from real stack ownership.** `dualstack-dns-routing-netns.sh` is valuable for fail-closed and endpoint-policy model/kernel behavior, but some `ip route/rule` setup is inserted manually after the Go helper cleans up. This must not be labeled proof that the running Go+Leshy production path installed the same routes correctly. Keep helper PASS, add production-path assertions in the installed gate.
6. **[MEDIUM] Roadmap handoff text lagged behind commits.** The older Linux banner still said `08A.3R OPEN` although `a8d9ab4` recorded VM PASS; the Windows start-of-packet still reads as if Phase 0 code is the next work although native support was implemented. Use this audit and the authoritative latest status records when handing work to the next model, not outdated historical instructions. Windows real VM acceptance still remains outstanding.

**Execution priority:** P0 improve Linux 08A.3R gate truthfulness (items 1–3), P1 portability/full lifecycle evidence (items 4–5), P2 refresh stale Windows 08A.4a handoff/status copy; then rerun corrected Linux gates on the disposable VM and attach logs/build/package SHA. Previously recorded PASS results remain historical evidence, but **the stronger no-churn/no-blackhole claims are not proven by the existing scripts**. Do not cut over the developer workstation. Windows VM 08A.4a/08A.4b remains a separate mandatory gate.

### HISTORICAL HANDOFF — 2026-10-08

**Historical 2026-10-08 handoff, retained for context. For the current starting
point read the 2026-10-14 handoff and audit at the top of this file.**

### Current branch and execution policy

- work on branch `feat/native-core-vpn-clients`;
- start from the **current branch tip**; do not reset to an older reviewed SHA;
- accepted Linux runtime/package source is `66a9a93`; later commits are
  acceptance, roadmap and handoff updates;
- do not touch untracked `.gigacode/`, `build/`, `dist/` or local VM-analysis
  files unless the current task explicitly requires them;
- do not install/cut over Kikimora on the developer workstation in the Windows
  workstream.

### Linux state — ALMOST RELEASE READY, DEPLOYMENT BRANCH DEFERRED

The Linux console/runtime product is accepted on the disposable Ubuntu VM:

- privileged hermetic Linux control plane: PASS;
- installed canonical `kikimora 1.0.0`: PASS;
- real AWG + OpenConnect application traffic: PASS;
- core/Toad crash recovery: PASS;
- NetworkManager and physical-link recovery: PASS;
- two real OS suspend/resume cycles after the recovery fix: PASS;
- reboot/cold boot before GUI login: PASS;
- VirtualBox pause/save-state/crash-style restart: PASS;
- bounded lifecycle soak/no-accumulation: PASS;
- package install/upgrade/remove/purge/reinstall lifecycle: PASS;
- final canonical package:
  `dist/08a-final-66a9a93/kikimora_1.0.0_amd64.deb`;
- canonical package SHA-256:
  `758156aff047c55392575298ce2c08fa55f9fb02d3661ecc0e3a24c45712d75c`;
- isolated `kikimora-next 1.0.0` was also installed beside canonical Kikimora
  on the disposable VM and coexistence passed with next-core inactive/disabled.

**Linux 08A.3R PASSED on disposable Ubuntu VM (2026-10-13):** `docs/toad-steps/08a3r-linux-dual-stack-dns-route-regression.md`. All four slices were rerun on the current branch tip after the xhttp transport work. Slice 1 (Go model tests), Slice 2 (privileged netns), Slice 3 (Leshy/DNS integration with netlink event monitor), and Slice 4 (installed VM regression with real AWG/OC application probes and core-restart recovery) all passed. Existing 08A.2/08A.3 lifecycle/package acceptance remains valid. No developer-workstation deployment is authorized.

What is **not** done on Linux is deliberate production deployment to the
developer workstation. That is now a separate deferred branch:

- `08a1-side-by-side-installed-staging.md`;
- `08b-observation-rollback-and-retirement.md`.

Do not resume that branch unless the operator explicitly asks to deploy/cut
over the developer workstation. Linux may be described as **almost release
ready**: VM/package/runtime acceptance is complete; workstation deployment and
observation/legacy retirement are intentionally deferred.

### 08C EXTERNAL XRAY VALIDATION (COMPLETE 2026-10-13)

08C is now **complete**. Real Xray/VLESS/REALITY/xhttp traffic was validated on
the disposable Ubuntu Linux VM and the disposable Windows 10 VM
(`win10`, 192.168.1.246):

- Linux VM: Google 200, Telegram 302, ChatGPT 308 (IPv4 only; IPv6 is not
  tunneled by this provider profile); core-managed role xray-nl reached Ready.
- Windows VM: Google 200 (87248 bytes), Telegram 302 (145 bytes),
  ChatGPT 308 (1107 bytes); core-managed role xray-win reached Ready.

Phases 2–4 of 08C were run on the Windows VM:
- failure/recovery: killing the kikimora-toad.exe child was detected by the core,
  which auto-restarted the role to Ready;
- core-managed role: xray-win started/stopped independently of awg-main and
  oc-main;
- suspend/resume: NOT RUN because VBoxManage is on the hypervisor host, not the
  VM guest; the known wintun stale-adapter limitation remains documented in
  08A.4b.

The hermetic official-Xray gate remains the authoritative automated protocol gate.

`docs/toad-steps/08c-external-xray-validation.md`

### Profiles — core switching implemented; routing between roles remains chapter 09 UI work

`SetActiveProfile` is now implemented in core (no UI yet). Roles can declare
`profiles = ["work", "home"]` in their TOML. Switching the active profile
enables all roles that belong to the target profile and disables the rest,
with rollback if persistence fails. The implicit `default` profile still
includes all configured roles for backwards compatibility.

A profile switch currently:

- stops roles not in the new profile;
- starts roles in the new profile;
- persists the new desired state.

Leshy already routes traffic between active managed Toads by endpoint/DNS
classification, so the active profile indirectly determines routing by selecting
which Toads participate. The remaining UI work is exposing profile selection in
the chapter 09 interface.

The workaround of manually `connect`/`disconnect` individual roles is no longer
required; profile switching can be driven via the core API/CLI.

Profile switching is covered by isolated Go unit tests
(`go test ./internal/control -run Profile`) and by privileged, OS-specific
integration tests gated behind the `privileged` build tag
(`TestPrivilegedProfileSwitchingLinux/Windows/Darwin`). See
`docs/toad-profiles.md` for the exact commands.

**Profile switching acceptance on disposable Ubuntu VM (192.168.1.51):**

- ✅ isolated config tests: `TestValidateProfiles*` PASS
- ✅ isolated control tests: `TestSetActiveProfile*`, `TestProfilesListsKnownProfiles`, `TestSetActiveProfileNoOpForSameProfile`, `TestSetActiveProfileRollbackOnPersistFailure` PASS
- ✅ privileged Linux integration test: `TestPrivilegedProfileSwitchingLinux` PASS under `root`

**Profile switching acceptance on Windows VM (192.168.1.246):**

- ✅ isolated config tests: `TestValidateProfiles*` PASS
- ✅ isolated control tests: `TestSetActiveProfile*`, `TestProfilesListsKnownProfiles`, `TestSetActiveProfileNoOpForSameProfile`, `TestSetActiveProfileRollbackOnPersistFailure` PASS
- ✅ privileged Windows integration test: `TestPrivilegedProfileSwitchingWindows` PASS under elevated `greymouse`

**UI integration:**

- `CoreBackend` exposes `profiles` and `setActiveProfile()` to QML.
- `RealCoreClient` parses `profiles` from the core snapshot and sends `SetActiveProfile`.
- `FakeCoreBackend` supports profile switching for the prototype.
- The Profiles tab in `Main.qml` now shows a `ComboBox` selector bound to `Core.profiles` and `Core.activeProfile`.
- Desktop tests pass on Linux VM:
  - `real-core-client` — profile list parsing + `SetActiveProfile` command.
  - `ui-behavior` — profile `ComboBox` reflects `Core.activeProfile`.
  - all five registered CTest targets PASS: `real-core-client`, `real-core-e2e`,
    `ui-behavior`, `system-theme`, `role-list-model` (`desktop/CMakeLists.txt`).
  - **correction (2026-10-14 audit):** the earlier summary named only four and
    omitted `role-list-model`; a sixth binary `real-core-ui-test` is built but
    never registered with `add_test`, so it never runs under CTest.
- Windows VM lacks Qt/CMake build environment, so UI build is covered by Linux CI and source-level cross-platform code.
- **correction (2026-10-14 audit):** the UI is a prototype only. The `K*`
  design-system controls, functional Routing/Leshy and Settings pages, the
  Windows `SIMULATED` marker, the ten FakeCore fixtures and `available_actions`
  drawer behavior do not exist. See `toad-code-docs-audit-2026-10-14.md`
  §3.3–§3.9.

`docs/toad-steps/08c-external-xray-validation.md` records this under the
Linux VM report.

**08A.4b acceptance status (COMPLETE 2026-10-13):**

- ✅ real AWG + OpenConnect traffic: both roles `Ready` (AWG 10.8.1.3/32,
  OC 10.0.0.x/24 connected to ve.ad-tech.ru with TOTP auth)
- ✅ core/Toad crash recovery (kill toad → core restarts → Ready)
- ✅ two real OS suspend/resume cycles: AWG recovers automatically; OC needs
  disconnect/connect after resume (wintun adapter stale after savestate)
- ✅ reboot/cold boot: service auto-start, both roles restored via desired.json
- ✅ VirtualBox pause/resume: both roles survive
- ✅ hard reset (VBoxManage reset): service auto-start, AWG Online
- ✅ bounded lifecycle soak (5 min): no accumulation, stable Ready
- ✅ no-accumulation: 1 core, 1–2 toad across all lifecycle tests
- ✅ package lifecycle: service stop/uninstall/install → running with roles
- ✅ dual-stack underlay: IPv4 192.168.1.246 + IPv6 2a00:1370:818c:9de::/64
- ✅ DNS resolution: ve.ad-tech.ru → 46.243.227.103 via router DNS
- ✅ endpoint policy routes stable: 72.56.119.116/32, 46.243.227.103/32

**Bugs found and fixed during 08A.4b:**

1. **vpnc-script format**: OpenConnect on Windows invokes `--script` through
   `cscript.exe` (Windows Script Host), not `cmd.exe`. The previous `.bat`
   script failed with "no script engine for file extension .bat". Rewritten
   as JScript (`.js`) which dispatches through cscript automatically (`fd328b7`).
2. **TOTP secret format**: OpenConnect `--token-secret=@<path>` requires the
   `base32:` prefix in the secret file for base32-encoded TOTP keys. Without
   it, the raw string is used as the secret, producing wrong TOTP codes.
3. **shared state_dir**: Both roles pointed at the same `state_dir`, causing
   `parking.json` role mismatch. Each role now gets its own `state_dir`.
4. **missing leshy_zone/rule_priority**: Custom role names (awg-main, oc-main)
   need explicit `leshy_zone` and `rule_priority` in TOML; the auto-fill only
   covers "primary"/"secondary".
5. **secret file CRLF**: Windows-created secret files had `\r\n` line endings
   that corrupted TOTP base32 secrets.

**Known limitation (not a code bug):** after VM savestate/resume, the OC
wintun adapter becomes stale and openconnect.exe cannot recreate it. Manual
`disconnect` + `connect` is needed. This is a wintun driver behaviour on
Windows when the VM is saved/restored; Linux TUN does not have this issue.

The active engineering step was **08A.4a** — Windows native networking substrate.
Phases 0–9 are implemented; Phase 10 (deterministic/cross-build gates) is
green except for one remaining no-VM item.

`docs/toad-steps/08a4a-windows-native-networking-substrate.md`

**Completed phases (all without a VM):**

1. ✅ Phase 0 — baseline freeze and unsupported-seam inventory
2. ✅ Phase 1 — production service shell + AF_UNIX IPC + process lifecycle
3. ✅ Phase 3 — native underlay observer (winipcfg IP Helper)
4. ✅ Phase 4 — managed TUN ownership (Wintun + deterministic GUID)
5. ✅ Phase 5 — native route manager (IP Helper + fail-closed parking)
6. ✅ Phase 6 — DNS ownership design (Leshy-only, documented)
7. ✅ Phase 7 slice 1 — AWG2 protocol attachment (GUID-based Wintun reopen)
8. ✅ Phase 7 slice 2 — OpenConnect Windows integration (Close+Start restart,
   route-free .bat vpnc-script, interface counters)
9. ✅ Phase 7 slice 3 — platform hardening (sleep notifications, interface
   repair, managed-interface verifier, child-process isolation)
10. ✅ Phase 8 — recovery ordering regressions (all six product-contract tests
    pass on windows/amd64)
11. ✅ Phase 9 — Windows MSI installer (WiX v3, package contract, CI gate)
12. ✅ Phase 10 — deterministic unit tests (9 new) + CI cross-build +
    package-contract jobs

**Remaining without a VM (implementable now):**

1. ~~Integrate the cross-platform `08-dual-stack-dns-route-stability.md`
   family/DNS/route-churn regression into native route/DNS tests~~ — DONE
   (`ad1e007`, `4641171`): deterministic model tests now cover all three
   platforms (Windows routes/parking, Linux netlink endpointPolicyPlan +
   Snapshot, Darwin netstat parsing + routeManager, endpoint Resolve,
   netstate family readiness). 30+ new tests encode split-default refusal,
   family independence, zero-mutation replay, mapped-IPv6 rejection and
   managed-interface exclusion.
2. ~~`peer_windows.go` defense-in-depth~~ — DONE (`3382348`): `peer_windows.go`
   rejects non-AF_UNIX connections; `peer_unsupported.go` is narrowed to
   `!linux && !darwin && !windows`.

### Windows service hardening fixes — 2026-10-10

A static review of the Windows service path found four defects; all are fixed:

1. the SCM-hosted service never loaded per-Toad TOMLs (`serviceOptions` left
   `serveOptions.configs` empty, and `runServe` only reads `configs`), so a
   service that reported `Running` served no roles and no control socket.
   `configsFromDir` now enumerates `C:\ProgramData\Kikimora\toads\*.toml` for
   both the console `serve --config-dir` path and the service;
2. `newManagerWithDeps` rejected zero managed Toads. A fresh install has no
   TOMLs and is now a valid fail-closed empty core (zero roles, `Stopped`
   aggregate) that still serves snapshots and accepts desired-state edits;
3. the service disabled automatic recovery because no ownership file was wired
   (`goOwnsLifecycle=false` ran unconditionally). The service now points at
   `C:\ProgramData\Kikimora\ownership.conf`, a missing file is tolerated as
   legacy ownership, and `kikimora-core service install` writes the legacy
   default cutover gate. Staging `routing_owner="go"` + `tunnel_owner="go"`
   enables bounded recovery and desired-state restore on the next start;
4. `Snapshot.LeshySupported` was hardcoded to `runtime.GOOS == "linux"` even
   though `leshy.FileBridge` is wired on every manager platform; it now follows
   the Linux/Darwin/Windows support matrix.

Also fixed: `endpoint/dualstack_test.go` compared the raw error string instead
of using `errors.Is(err, ErrResolutionUnavailable)`.

Not a defect: `linux/install.sh` `EXPECTED_VERSION="0.4.0"` is the pinned Leshy
binary version (`leshy ${EXPECTED_VERSION}`), not the Kikimora version.

Known follow-up (no code change now): the MSI is the primary Windows contract
and does not yet lay down `ownership.conf`; MSI installs start correctly in
legacy mode and recovery can be enabled by staging the file. Ship the ownership
template in the MSI when 08A.4b begins.

**Requires a disposable Windows VM:**

1. VM handoff gate (section 11): install MSI, service before desktop login,
   real AWG + OpenConnect traffic, baseline application probes
2. Privileged Wintun lifecycle test
3. Privileged route-manager live verification
4. SCM start-before-desktop-login
5. Dual-stack DNS/route-stability field regression including real IPv6/no-IPv6 behavior
6. 08A.4b lifecycle parity acceptance (crash recovery, suspend/resume,
   hibernate, reboot, VirtualBox lifecycle, MSI lifecycle)

Concrete unsupported seams remaining:

~~~text
(none)
~~~

Windows peer authorization is now handled by `peer_windows.go`, which rejects
non-AF_UNIX connections as defense-in-depth; `peer_unsupported.go` is narrowed
to `!linux && !darwin && !windows`. Windows AF_UNIX does not expose an
`SO_PEERCRED` equivalent and named pipes (the original alternative) are blocked
by a go-winio bug recorded in the 08a4a Phase 1 record, so the directory ACL on
`C:\ProgramData\Kikimora` remains the primary authorization gate: `connect()`
opens the socket file, and the installer restricts the parent directory to
SYSTEM/Administrators. All other previously-listed seams have native Windows
implementations: underlay observer, TUN ownership, route manager, AWG2
attachment, OpenConnect script/counters/transport-restart, interface repair,
managed-interface verifier, sleep notifications and child-process isolation.

Windows local IPC is now guarded by both the directory ACL and the transport
check; a full `SO_PEERCRED`-equivalent peer-PID authorization should replace
the transport check when named pipes become viable.

### 08A.4b status: in progress (see acceptance status above)

`docs/toad-steps/08a4-windows-vm-console-lifecycle-acceptance.md` is the
**acceptance packet after implementation**. It intentionally repeats the class
of Linux VM evidence, but it is blocked until 08A.4a can install a real Windows
package and pass baseline real AWG + OpenConnect traffic.

## Current direction

- runtime/control-plane language: Go;
- cross-platform runtime root: `toad/`;
- per-protocol executable: `kikimora-toad`;
- central daemon: `kikimora-core`;
- chapter 08 product surface: installed console/runtime only (`kk`, `kikimora-core`, `kikimora-toad`); an existing Qt 6/QML prototype remains in `desktop/` and is being actively extended (profile switcher + tests), but chapter-08 acceptance and packaging still do not depend on Qt; see `toad-code-docs-audit-2026-10-14.md` on the chapter-09 "FUTURE" contradiction;
- one Toad process = one independently supervised managed VPN instance;
- Linux VM/package/runtime acceptance is complete; developer-workstation deployment/cutover is a separate deferred operator-gated branch and is not the current execution path;
- the Go endpoint/routing/parking/recovery path is **privileged-hermetic accepted on Linux**, but it is not yet the production owner on the developer workstation;
- routing/DNS classification remains Leshy; the Go core is taking lifecycle, endpoint-underlay, parking/publication coordination in ordered stages;
- external non-Toad VPNs such as a corporate `vpn0` remain externally owned;
- Windows native networking substrate (08A.4a) and VM lifecycle acceptance (08A.4b) are complete; 08C external Xray validation is also COMPLETE on both Linux and Windows VMs (2026-10-13); the open front is the deferred Linux workstation deployment (08A.1/08B) and the chapter-09 desktop UI.

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

Stage 0, the 07A-07F.2 Linux hermetic/control-plane acceptance sequence, Linux 08A.2 VM lifecycle acceptance and Linux 08A.3 final package acceptance are complete. The Linux console/runtime is therefore almost release ready; deployment to the developer workstation and subsequent observation/legacy retirement are intentionally deferred. Windows native networking (08A.4a) and VM lifecycle (08A.4b) are complete; the active roadmap is now 08C external Xray validation on both Linux and Windows VMs. UI work remains deferred to chapter 09.

The current branch has advanced through the installed console/API surface, package lifecycle work, hypervisor lifecycle gates and the suspend/underlay recovery fixes through `a4055e6`. The last full authoritative privileged hermetic kernel/network suite remains recorded at `846335d18a71e9da81211b319f6831acf3ffba24`; the disposable VM has also completed the four privileged gates on the current runtime line.

Therefore distinguish three statements:

- **privileged hermetic Linux control plane:** accepted at `846335d`;
- **Linux disposable-VM installed product/package:** accepted, including real lifecycle and package evidence;
- **developer-workstation production ownership:** intentionally deferred to the separate 08A.1/08B deployment branch.

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

Windows native networking substrate (08A.4a):

- [x] Windows cross-build baseline and unsupported-seam inventory;
- [x] Windows service shell (SCM, recovery, graceful stop, ProgramData);
- [x] Windows AF_UNIX local IPC (directory ACL authorization gate);
- [x] native Windows underlay observer (winipcfg IP Helper + change callbacks);
- [x] Windows managed TUN ownership (Wintun + deterministic UUIDv5 GUID);
- [x] native Windows route manager (IP Helper, host-prefix endpoints, fail-closed parking);
- [x] DNS ownership design (Leshy-only, documented decision);
- [x] AWG2 Windows protocol attachment (GUID-based Wintun reopen);
- [x] OpenConnect Windows integration (Close+Start restart, route-free .bat, counters);
- [x] Windows platform hardening (sleep, interface repair, managed-iface, process isolation);
- [x] recovery ordering regressions pass on windows/amd64;
- [x] Windows MSI installer (WiX v3, package contract, CI gate);
- [x] deterministic unit tests + CI cross-build + package-contract jobs;
- [x] peer_windows.go defense-in-depth (last unsupported seam closed);
- [x] dual-stack/DNS/route-stability deterministic model tests (30+ new across
      Windows/Linux/Darwin + endpoint + netstate);
- [x] service config/ownership/recovery hardening (serviceOptions loads TOMLs,
      fail-closed zero-role startup, ownership cutover gate, platform Leshy flag)
      — see "Windows service hardening fixes" above;
- [x] VM handoff gate (install MSI, service before login, Wintun lifecycle, route-manager, dual-stack field regression) — PASSED on disposable Windows 10 VM (2026-10-11);
- [x] 08A.4b lifecycle parity acceptance (COMPLETE 2026-10-13; known wintun stale-adapter limitation documented).

Current local evidence is stronger than the original 2026-09-21 audit:

- full 07E non-privileged acceptance is green;
- all four authoritative privileged gates are green on `846335d`, including orchestration phases A-F and final host-state equality;
- side-by-side `kikimora-next` packaging/collision tests are green;
- on the Ubuntu 26.04 VM testbed, `run-rootless.sh model` is green on the accepted runtime line and the host-network-dependent IPC test passes under `-race -count=20`;
- the VM privileged gate set is green 4/4 with clean host-state restoration;
- real system-wide AWG passes Google, Telegram, ChatGPT trace and unauthenticated OpenAI API probes;
- real system-wide OpenConnect reaches the internal GitLab endpoint and cleans up correctly;
- real Xray proves TUN/routing plus Google and Telegram traffic, then hits the operator's already-known ISP DPI limitation at the ChatGPT DNS/application step; external Xray remains non-blocking because the hermetic official-Xray gate is authoritative.

Do not infer developer-workstation production completion from these results. Linux workstation staging is intentionally deferred. The current required work is Windows 08A.4a native networking implementation; only after its VM handoff gate passes should Windows 08A.4b lifecycle parity acceptance begin.

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

**correction (2026-10-14 audit):** the current repo-root `run-privileged-gates.sh` runs **five** gates — route-parking, **dualstack**, multi-toad, orchestration-acceptance, xray-interop (`run-privileged-gates.sh:158-162`). The four-gate wording above is historical; today the dual-stack gate is also mandatory.

Completed execution packet:

`docs/toad-steps/08c-external-xray-validation.md` (08C — dual-platform external Xray validation, COMPLETE 2026-10-13 on both the Linux and Windows VMs). See the 2026-10-14 code-vs-docs audit for the conflicting stale "AWAITING SECRET" wording elsewhere in this file.

The Linux installed-host packets `08a-linux-installed-host-staging.md`, `08a1-side-by-side-installed-staging.md` and `08b-observation-rollback-and-retirement.md` are retained as a **deferred deployment branch**, not the current execution path.

Current accepted chapter-08 Linux artifact source HEAD:

`66a9a93` (runtime suspend/underlay fix is `a4055e6`; later commits add acceptance docs/harness only)

### Completed Ubuntu VM acceptance lane — historical Linux evidence

The disposable GUI Ubuntu VM was the chapter-08 Linux product acceptance environment. That lane is now complete and retained here as evidence; it is no longer the immediate execution target.

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

Completed VM packet:

`docs/toad-steps/08a2-vm-console-lifecycle-acceptance.md`

The completed acceptance sequence was:

1. install an exact package-built candidate in the VM;
2. expose the full console orchestration surface over the same versioned core API used by future UI;
3. run real AWG + OpenConnect under **Kikimora core orchestration**, not standalone protocol scripts;
4. drive core/Toad crashes, NetworkManager restart, NIC loss/restoration, OS suspend/resume, VM pause/save-state/reset, reboot, shutdown/start and cold boot;
5. verify persistent desired state, fail-closed routing, independent Toad ownership, clean routes/rules and revisioned state/API reconnection after every event;
6. run package install/upgrade/remove/reinstall acceptance;
7. produce the final chapter-08 Linux console/runtime `.deb` and SHA-256;
8. leave workstation side-by-side/cutover as a separately operator-gated deployment branch.

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
- **07F.2 and Linux VM/package acceptance are complete. Developer-workstation mutation remains deferred; the active implementation path is Windows 08A.4a.**

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

7C. **AUTOMATED PROOF COMPLETE; VM SUSPEND/RESUME ACCEPTED:** `07c-underlay-resume-networkmanager.md`.
Current local tests and privileged orchestration prove underlay convergence, structural drift detection/repair and current-epoch recovery; Linux VM suspend/resume is also accepted. Developer-workstation suspend testing is deferred with the Linux deployment branch.

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

8A. **LINUX PRODUCT ACCEPTED ON VM / WORKSTATION DEPLOYMENT DEFERRED; WINDOWS NATIVE RUNTIME NOW CURRENT.**

8A.1. **DEFERRED LINUX DEPLOYMENT BRANCH / OPERATOR-GATED:** `08a1-side-by-side-installed-staging.md`.
The disposable-VM console/runtime acceptance and final canonical package are complete, and refreshed `kikimora-next` coexistence has also passed on the VM. Developer-workstation installation/cutover is intentionally postponed. Resume this branch only when the operator explicitly asks to deploy Linux on the workstation; then continue from the already-green read-only preflight and the first side-by-side install mutation boundary. Before any ownership cutover, run `08-dual-stack-dns-route-stability.md` against the real host so the legacy dead-IPv6/AAAA/route-churn failure becomes a mandatory regression gate rather than being inherited by the new runtime.

8A.2. **COMPLETE — VM CONSOLE ORCHESTRATOR + OS LIFECYCLE:** `08a2-vm-console-lifecycle-acceptance.md`.
Installed-console baseline, core/Toad recovery, NetworkManager restart, repeated physical-link recovery, reboot/cold boot, VirtualBox pause/resume, save-state/start and crash/reset recovery, bounded soak and real application probes are green. Suspend/resume exposed and fixed the `WaitingForUnderlay` recovery gap in `a4055e6`; two post-fix real OS suspend/resume cycles returned AWG + OpenConnect automatically to Ready/current epoch with no manual reconnect.

8A.3. **COMPLETE — FINAL LINUX CONSOLE/RUNTIME PACKAGE:** `08a3-linux-console-package.md`.
The final canonical package is `dist/08a-final-66a9a93/kikimora_1.0.0_amd64.deb`, SHA-256 `758156aff047c55392575298ce2c08fa55f9fb02d3661ecc0e3a24c45712d75c`. Static package contract, fresh install semantics, real upgrade, remove/reinstall, purge/reinstall and final installed-package AWG/OpenConnect smoke all pass on the disposable VM.

8A.3R. **EVIDENCE CAPTURED / PASSED ON DISPOSABLE UBUNTU VM — Linux dual-stack/DNS/route-stability regression:** `08a3r-linux-dual-stack-dns-route-regression.md`.
All four slices pass on the disposable Ubuntu VM at commit `e0d131d`: Slice 1 rootless model tests (30+), Slice 2 privileged hermetic netns (dualstack-dns-routing-netns.sh + Go helper, 18 tests), Slice 3 Leshy/DNS combined integration (8 tests), Slice 4 installed VM regression (before/after capture, zero-mutation soak, core restart recovery, no split-defaults). Bug found and fixed: `snapshotKernelUnlocked()` did not see table 51890 routes, breaking endpoint policy garbage collection and removal. Existing 08A.2/08A.3 acceptance is not revoked; the regression supplement is satisfied.

8A.4a. **VM HANDOFF GATE PASSED / NATIVE WINDOWS NETWORKING SUBSTRATE:** `08a4a-windows-native-networking-substrate.md`.
Phases 0–10 implemented and green (service shell, AF_UNIX IPC, underlay observer, TUN ownership, route manager, DNS design, AWG2+OpenConnect backends, platform hardening, installer, unit tests, CI cross-build/package gates). All unsupported seams closed (`peer_windows.go`, `3382348`). Dual-stack/DNS/route-stability deterministic model tests complete across all three platforms (`ad1e007`, `4641171`): Windows routes/parking, Linux netlink endpointPolicyPlan + Snapshot, Darwin netstat parsing + routeManager, endpoint Resolve family separation, netstate family readiness — 30+ new tests.

**VM handoff gate PASSED on disposable Windows 10 VM (2026-10-11):**
MSI installed, KikimoraCore service AUTO_START+LocalSystem, `kk status --json` Ready, real dual-stack underlay (IPv4 192.168.1.246 + IPv6 2a00:1370:818c:9de::/64), privileged Wintun lifecycle test PASS, route-manager 12/12 PASS, full Go test suite 21/21 packages PASS, no split-default routes, zero-mutation route stability, service restart recovery, dual-stack HTTP probes IPv4+IPv6 OK. AWG+OpenConnect real-traffic probes deferred to 08A.4b where role configuration is the primary scope.

The additional mandatory field regression is `08-dual-stack-dns-route-stability.md`:
family-specific IPv4/IPv6 readiness, no accidental dead split-default capture,
DNS A/AAAA safety, deterministic DNS ownership and zero-mutation steady-state
reconciliation. Integrate the deterministic model checks with remaining Windows
route/DNS work, then collect real Windows VM evidence. Run the same regression
before any later Linux workstation ownership cutover.

8A.4b. **COMPLETE — WINDOWS VM CONSOLE LIFECYCLE ACCEPTANCE:** `08a4-windows-vm-console-lifecycle-acceptance.md`.
Real AWG + OpenConnect traffic, core/Toad crash recovery, two OS suspend/resume cycles (AWG auto-recovers; OC needs manual reconnect — wintun stale-adapter limitation), reboot/cold boot, VirtualBox pause/reset, bounded soak, no-accumulation, package lifecycle and dual-stack field regression all PASS on the disposable Windows 10 VM (2026-10-13). Five bugs found and fixed (vpnc-script .js, TOTP base32 prefix, per-role state_dir, leshy_zone/rule_priority, secret file CRLF).

8B. **DEFERRED WITH LINUX WORKSTATION DEPLOYMENT / OPERATOR-GATED:** `08b-observation-rollback-and-retirement.md`.
This is not on the current Windows execution path. Execute only after the operator later resumes Linux workstation deployment, completes 08A.1 installed-host cutover and chooses an observation window. Legacy retirement remains a separate explicit decision.

8C. **COMPLETE — DUAL-PLATFORM EXTERNAL XRAY VALIDATION:** `08c-external-xray-validation.md`.
Real VLESS/REALITY/xhttp traffic was validated on both the Linux VM and the Windows VM (Google 200, Telegram 302, ChatGPT 308). The step packet header marks 08C **COMPLETE (2026-10-13)**; this roadmap line previously still said "AWAITING SECRET / current execution path", which was stale (see `toad-code-docs-audit-2026-10-14.md` §3.2). Windows VM suspend/resume for Xray was NOT RUN (host-side VBoxManage limitation).

9. **FUTURE — DESKTOP/UI OVER THE ACCEPTED CONTROL PLANE:** `09-desktop-ui.md`.
UI/UX, tray behavior and desktop packaging begin only after the chapter-08 console/runtime package is accepted. The UI consumes the existing versioned Snapshot/Subscribe control channel and does not own orchestration semantics.

The umbrella `07-go-reconcile-resume.md` is **superseded and must not be executed directly**.

### Executor rules for the current sequence

- start from the actual branch HEAD; never reset to a historical reviewed SHA;
- **08C (`08c-external-xray-validation.md`) is COMPLETE on both VMs (2026-10-13)**; Linux 08A.1/08B workstation deployment is intentionally deferred, while Linux 08A.2 VM lifecycle and 08A.3 final package acceptance are complete;
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

The 2026-09-21 legacy suspend/resume failure remains useful architectural background in `docs/toad-resume-recovery-architecture.md`, but step 07 and Linux 08A.2/08A.3 are no longer future work. Linux 08A.1/08B deployment is intentionally parked as a separate operator-gated branch. The **current mainline boundary is Windows 08A.4a implementation**, followed by 08A.4b acceptance. UI begins in chapter 09 over the already accepted control plane.

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
- if a selected route target is not ready, routing must install an explicit fail-closed outcome rather than allowing kernel fallback to another Toad or the physical default route; for an unavailable address family, prefer prompt unreachable/reject semantics over a silent blackhole so DNS AAAA/A answers cannot recreate multi-second dead-family stalls;
- IPv4 and IPv6 must follow the same fail-closed rule, but readiness is family-specific: IPv4 Ready never implies IPv6 Ready.

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