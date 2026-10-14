# Kikimora — code vs. roadmap/plan audit (2026-10-14)

Status: **static audit record. No privileged/VM tests were run as part of this
audit.** This document compares what the roadmaps and plans claim against what
the code in this tree actually contains, and marks each item as closed, open,
overstated ("lies"), or structurally broken.

## 0. Method and baseline

- Repository: `C:\Users\greymouse\Projects\kikimora`.
- Branch: `feat/native-core-vpn-clients`.
- HEAD at audit time: `bfb3b49401e7de4864bb9f3dcc076ca0492b7bae`
  ("docs(roadmap): desktop UI profile integration tests pass on Linux VM",
  2026-10-13). The branch is **645 commits ahead of `origin/master`**.
- Update from origin: `git fetch --all --prune` and
  `git pull origin feat/native-core-vpn-clients` both reported **"Already up to
  date."** Local `master` and `origin/master` are identical. No new remote work
  arrived.
- Method: read the roadmap/plan/step documents, then explored the matching source
  trees (`toad/`, `desktop/`, `linux/tests/`, `packaging/`) and scripts to verify
  each claim with `file:line` evidence. No source was modified by the audit.
- Caveat: everything below is source-level. Real-VM acceptance evidence lives in
  the tracking documents and was not re-executed here.

## 1. Verdict summary

| Area | Document claim | Code reality | Class |
| --- | --- | --- | --- |
| Profiles (core + CLI + tests) | Fully implemented and accepted | Implemented as described | CLOSED |
| Xray `xhttp` transport | Implemented | Implemented + test | CLOSED |
| Core/UI command vocabulary | All 8 commands | Present in `control` API | CLOSED |
| Windows native substrate (code) | Phases 0-10 implemented | Files present | CLOSED (source) |
| netlink table-51890 fix | `RT_FILTER_TABLE` fix present | Present | CLOSED |
| `toad-roadmap.md` integrity | Canonical living roadmap | **Committed merge-conflict markers** | BROKEN |
| 08C external Xray status | COMPLETE **and** AWAITING SECRET | Contradicts itself in one file | LIE |
| Desktop design system (`K*`) | Required/described as present | Absent (3 ad-hoc QML files) | OVERSTATED |
| Routing/Leshy + Settings pages | Real state pages | `PlaceholderPage` only | OVERSTATED |
| Windows "SIMULATED" marker | Required visible indication | Only "FakeCore" text | OVERSTATED |
| FakeCore scenarios | 10 deterministic fixtures | 4 demo scenarios, timer-driven | OVERSTATED |
| Drawer actions from `available_actions` | Required | Guessed from QML state | OVERSTATED |
| "all 5 CTest targets PASS" | 5 named | 5 exist, only 4 named; 1 dead binary | INACCURATE |
| Chapter 09 UI = FUTURE | Start only after ch.08 | UI actively built + tests claimed | CONTRADICTION |
| 08a4a handoff gate | PASSED, proceed | Same doc lists 6 pending + OC BLOCKED | INCONSISTENT |
| Leshy/DNS DNS-write counters | Instrumented | Absent, self-admitted pending | OVERSTATED |
| VM dual-stack drift check | Fails on route drift | IPv4 main table only | OVERSTATED |
| `run-privileged-gates` gate list | 4 gates named | Actually runs 5 (dualstack omitted) | INACCURATE |
| Linux workstation deploy | Deferred | Deferred | OPEN |
| macOS real-core E2E | Required for milestone | Unverified on real macOS | OPEN |

> The table above is the state **at the audited HEAD (`bfb3b49`)**. Several
> OVERSTATED/INACCURATE rows were subsequently remediated in code — see §8 for
> the commit-level status (`5cac3f2`, `6e209bd`).

## 2. What is genuinely closed (verified in code)

These claims match the source and can be trusted:

1. **Profiles feature** — `(*control.Manager).SetActiveProfile` at
   `toad/internal/control/control.go:993`; `ValidateProfiles` +
   `Config.Profiles` at `toad/internal/config/config.go:41,388`; snapshot fields
   `Profiles`/`ActiveProfile` at `toad/internal/control/control.go:86,89`;
   `Manager.Profiles()` at `control.go:2086`; CLI `profiles use` at
   `toad/cmd/kikimora-core/main.go:445`; API case + capability list at
   `toad/internal/control/api.go:251,270`.
   - Unit tests present: `TestSetActiveProfile*`, `TestProfilesListsKnownProfiles`,
     `TestSetActiveProfileNoOpForSameProfile`,
     `TestSetActiveProfileRollbackOnPersistFailure` in `control_test.go`;
     `TestValidateProfiles*` in `config_test.go`.
   - Privileged tests present behind the `privileged` build tag:
     `TestPrivilegedProfileSwitchingLinux/Windows/Darwin` in
     `profile_{linux,windows,darwin}_privileged_test.go`.
2. **Xray `xhttp` transport** — `xrayXHTTPSettings`, `xrayXHTTPEExtra`,
   `xrayXHTTPXmux` and `buildCoreJSON` wiring in
   `toad/internal/backend/xray/config.go:85-156`; `VLESSXHTTPConfig` +
   `transport = "xhttp"` allowance in `toad/internal/config/config.go:267,271,500`;
   test `TestBuildCoreJSONXHTTPTransport` in `config_test.go:101`.
3. **Command vocabulary** — `ConnectAll`, `DisconnectAll`, `ConnectRole`,
   `DisconnectRole`, `RetryRole`, `SetActiveProfile`, `RediscoverEndpoints`,
   `GetDiagnosticsSnapshot`, plus `Handshake`, `GetSnapshot`, `Subscribe`,
   `ValidateRole`, all present in `toad/internal/control/api.go:250-290` with
   matching `Manager` methods.
4. **Windows native substrate (source presence)** — service shell
   `toad/cmd/kikimora-core/service_windows.go`; native underlay
   `toad/internal/underlay/default_windows.go`; route manager
   `toad/internal/platform/windows/routes/routes.go`; TUN/Wintun ownership
   `toad/internal/platform/tun_windows.go` + `managed_interface_windows.go` +
   `interface_repair_windows.go`; MSI `packaging/windows/kikimora.wxs`;
   package contract `packaging/windows/test_package.ps1`.
5. **netlink blind-spot fix** — `RouteListFiltered(..., RT_FILTER_TABLE)` at
   `toad/internal/platform/linux/netlink/routes.go:56`.
6. **Runner modes** — `run-isolated.sh` exposes the documented modes and never
   self-elevates (`:302-315`); `run-rootless.sh` supports only `model`/`probe`
   (`:49-58`).
7. **08A.3R scripts exist** with the asserted hard checks: the netlink event
   monitor in `linux/tests/toad/leshy-dns-integration.sh:187-201` fails on *any*
   route/rule event; `vm-dualstack-regression.sh:86,103,146` require real
   AWG/OC probe commands; the dual-stack netns gate exists as
   `linux/tests/toad/dualstack-dns-routing-netns.sh` + Go helper
   `toad/internal/platform/linux/dualstack/testhelper/main.go`.

## 3. Lies / overstatements (docs describe things the code does not do)

### 3.1 `docs/toad-roadmap.md` is committed with unresolved merge-conflict markers

The canonical roadmap contains literal conflict markers at lines 9 (`<<<<<<< HEAD`),
11 (`=======`) and 40 (`>>>>>>> 696f9db…`). `git show HEAD:docs/toad-roadmap.md`
confirms they are committed, not a local edit. The file therefore renders as
broken markdown and carries two contradictory handoff banners ("2026-10-13" HEAD
side vs the 2026-10-11 audit + "2026-10-08" side). **This is the single most
serious documentation defect.** (Resolved as part of this audit — see §6.)

### 3.2 08C external Xray status contradicts itself in one file

`docs/toad-roadmap.md:90` states **"08C EXTERNAL XRAY VALIDATION (COMPLETE
2026-10-13)"**, while `docs/toad-roadmap.md:737` still lists **"8C. CURRENT
EXECUTION PATH — DUAL-PLATFORM EXTERNAL XRAY VALIDATION (AWAITING SECRET)"**.
Lines 332 and 359 also still say the next step is 08C. The step packet itself
(`docs/toad-steps/08c-external-xray-validation.md:3`) says **COMPLETE**. The
"AWAITING SECRET / current execution path" text is stale and should be removed.

### 3.3 Desktop design system (`K*` controls) does not exist

Roadmap §U3 (`docs/roadmap.md:518-531`) and plan §6
(`docs/desktop-ui-implementation-plan.md:258-310`) describe
`KConnectControl`, `KRoleSummaryRow`, `KRoleDetailDrawer`, `KBottomNav`,
`KDrawer`, `KStatusChip`, `KButton`, `KIconButton`, `KHeader`, `KDialog`,
`KBanner`, `KSwitch`, `KTextField`, `KComboBox`, `KProgressIndicator`,
`KDivider`. A repo-wide search finds these names **only in `docs/`**. The actual
QML is three files: `desktop/qml/Controls/ConnectCircle.qml`,
`desktop/qml/Controls/RoleRow.qml`, `desktop/qml/Drawers/RoleDrawer.qml`.
The `KikimoraTheme` singleton exists but uses a different, smaller token set than
the plan (`KikimoraTheme.qml:7-24`); tokens such as `backgroundBase`,
`surfaceBase`, `surfacePressed`, `textMuted`, `borderSoft`, `accentPrimary`,
`accentSuccess/Warning/Error`, `animationNormal`, `focusRingWidth` are absent.
There is also no `AggregateConnectionController`, no `DesktopShell`, no
`CoreClient`/`FakeCoreClient` (the real names are `CoreBackend` /
`FakeCoreBackend`), and no `LocalIpcBackend`.

### 3.4 Routing/Leshy and Settings pages are placeholders

Roadmap (`docs/roadmap.md:567-573`) and plan §9
(`desktop-ui-implementation-plan.md:469-492`) describe real pages exposing Leshy
state, DNS, zone mapping, endpoint-underlay state and parking. In code both are
`PlaceholderPage` instances (`desktop/qml/Main.qml:247-256`), the Routing page
showing only `leshySupported` text.

### 3.5 Windows "visibly simulated" marker is absent

Plan §5 (`desktop-ui-implementation-plan.md:224`) requires a visible
`SIMULATED`/`FakeCore` indication. Windows does force the fake backend
(`desktop/src/main.cpp:38-40`) and shows a "FakeCore · revision …" banner
(`desktop/qml/Main.qml:125-137`), but a case-insensitive search for `simulated`
across `desktop/` returns **zero** matches. The exact "simulated" marker is not
implemented.

### 3.6 FakeCore is not the scriptable fixture set the plan requires

Plan §5 (`desktop-ui-implementation-plan.md:189-220`) lists ten deterministic
scenarios (`all-stopped`, `connecting-all`, `all-ready`,
`primary-ready-secondary-failed`, `secondary-recovering`, `underlay-down`,
`parking-active`, `endpoint-pending`, `leshy-failed`, `core-error`). Code
implements four demo scenarios and drives them with timers
(`desktop/src/core/FakeCoreBackend.cpp:192-256`, transitions at
`FakeCoreBackend.cpp:121,150`). It is stateful, but not the fixed scriptable
fixture set described.

### 3.7 Role drawer actions violate the explicit plan rule

Plan §8 (`desktop-ui-implementation-plan.md:435`) requires per-role actions to be
driven by core-reported `available_actions`, "not guessed from QML state". The
drawer instead guesses from `state`:
`desktop/qml/Drawers/RoleDrawer.qml:89`
(`state === "Stopped" ? "Connect this role" : "Disconnect this role"`).
`availableActions` is parsed by `RealCoreClient` but never surfaced, and is never
populated by `FakeCoreBackend`.

### 3.8 "All 5 CTest targets PASS" is mis-named

The five registered CTest targets are `real-core-client`, `real-core-e2e`,
`ui-behavior`, `system-theme`, `role-list-model`
(`desktop/CMakeLists.txt:167,183,254,270,283`). The docs name only four and omit
`role-list-model` (`docs/toad-roadmap.md:161-163`,
`go-vpn-orchestration-v2-progress.md:113`). A sixth test binary,
`real-core-ui-test` (`CMakeLists.txt:185-218`), is built but **never registered
with `add_test`**, so it never runs under CTest. `real-core-e2e` also silently
skips unless `KIKIMORA_CORE_BINARY`/`KIKIMORA_FAKE_TOAD_BINARY` are set
(`desktop/tests/RealCoreEndToEndTest.cpp:75-80`).

### 3.9 Chapter 09 is declared FUTURE while the UI is actively being built

`docs/toad-steps/09-desktop-ui.md:3` says **"FUTURE; START ONLY AFTER CHAPTER 08
CONSOLE/RUNTIME PACKAGE ACCEPTANCE"**, and `docs/toad-roadmap.md:326,388` defer
UI to chapter 09. Yet the current branch tip is a desktop-UI commit
("desktop UI profile integration tests pass"), the roadmap records UI test
passes (`docs/toad-roadmap.md:154-164`), and `.github/workflows/desktop-ui.yml`
exists. The documents disagree about whether UI is in scope now.

### 3.10 The Windows 08A.4a packet contradicts its own status banner

`docs/toad-steps/08a4a-windows-native-networking-substrate.md:3` declares
**"VM HANDOFF GATE PASSED … PROCEED TO 08A.4b"**, but the same document still
lists six "Requires a disposable Windows VM" items as open (`:544-560`) and marks
OpenConnect real traffic **BLOCKED** (`:834-840`). The status line and the
pending list / evidence table (`:792-817`) disagree. (Some of the same items are
simultaneously marked PASS in the evidence table.)

### 3.11 Leshy/DNS "DNS write-counter instrumentation" does not exist

The audit section of the roadmap claims true DNS write-counter instrumentation
was the goal; `08a3r` says the warnings "have been addressed by hardening". In
code, `leshy-dns-integration.sh` monitors only `ip monitor route rule`
(`:187-201`) and the script itself states the counters/text `"...(not proof of
attempted DNS writes)"` (`:229`) and that "DNS write counters and active replay
still pending" (`:325`). There is no DNS mutation counter. This part of the
claim is not met.

### 3.12 VM dual-stack "route-count drift fails the gate" is IPv4-main-table only

`vm-dualstack-regression.sh` compares only the IPv4 main-table route count
first vs last (`:122-125`); IPv6 main and endpoint table 51890 are logged but
never compared (`:116-119`). The script admits it is "a bounded drift check, not
netlink/DNS zero-write acceptance" (`:201-202`). Broader "zero-mutation"
wording overstates the assertion.

### 3.13 `run-privileged-gates.sh` gate list is stale

`run-privileged-gates.sh` runs five gates: route-parking, **dualstack**,
multi-toad, orchestration-acceptance, xray-interop (`:158-162`). The roadmap
summary lists only four and omits `dualstack` (`docs/toad-roadmap.md:596-597`).

### 3.14 Minor count/staleness drift

- `08a4a` claims `test_package.ps1` "verifies 26 assertions" (`:448`); the file
  contains ~30 `Assert-True` sites (~28 distinct).
- `08a3r` says "18 tests" (`:26,42`); the netns gate is 8 shell + 11 Go = 19
  logical checks.
- `test-report.txt` (tracked, repo root) references HEAD `55d4782`, while HEAD
  is `bfb3b49`; the report is stale.

## 4. What is genuinely open (docs and code agree)

- **Linux workstation deployment / cutover** — `08A.1` / `08B` deferred,
  operator-gated. Not started by design.
- **macOS real-core E2E (U9)** — code has partial Darwin adapters, but no macOS
  hardware verification. The "real desktop milestone" is explicitly incomplete
  until macOS passes.
- **Windows as a real networking product** — Windows still runs FakeCore in the
  product; real networking is not yet the default and is gated behind
  acceptance. (The native substrate code exists, but the product surface is
  simulated.)
- **Desktop UI completeness** — design system, Routing/Leshy page, Settings
  core-backed behavior, diagnostics page, accessibility/localization, HiDPI,
  packaging, tray parity (`DesktopShell` contract), profile role listing are all
  unimplemented.
- **DNS-ownership migration to Go** — `leshy-health-watch.service` remains a
  separate owner by design.
- **Full zero-mutation DNS proof** — not demonstrated (§3.11).

## 5. Structural documentation problems

1. **Merge-conflict markers in the canonical roadmap** (§3.1) — fixed here.
2. **Two competing "roadmaps"** — `docs/toad-roadmap.md` (Toad packets + G/U
   architecture in `docs/roadmap.md`) and `docs/roadmap.md` (G0-G10 / U0-U16)
   use different numbering and occasionally different UI component names and
   platform status. They are not reconciled.
3. **Encoding damage (re-checked 2026-10-14: no longer present)** — the audit
   noted mojibake (`�?"`) from a bad encoding round-trip in
   `docs/go-core-ui-toads-integration-plan.md`. Re-inspection of the current
   file finds **zero** `U+FFFD` sequences and no BOM; its em-dashes and `→`
   arrows are valid UTF-8. The file appears to have been fixed since the
   original finding. `docs/toad-roadmap.md` had the same class of damage
   previously and is also clean now.
4. **Contradictory statuses** for 08C (§3.2) and chapter 09 (§3.9).
5. **Stale generated report** `test-report.txt` (§3.14).
6. **Planning docs that still look executable** — `07f2a-rootless-hermetic-test-runner.md`
   still documents `run-rootless.sh route-parking|xray-interop|…` modes that the
   script no longer accepts (it only supports `model`/`probe`).

## 6. Actions taken by this audit

1. Added this file (`docs/toad-code-docs-audit-2026-10-14.md`).
2. Embedded an audit section at the top of `docs/toad-roadmap.md`.
3. Resolved the committed merge-conflict markers in `docs/toad-roadmap.md`
   (kept the 2026-10-13 handoff as current; demoted the 2026-10-11 sections and
   the 2026-10-08 header to clearly-labelled historical sections).
4. Corrected the stale 08C "AWAITING SECRET / current execution path" wording to
   reflect the documented COMPLETE status.
5. Ran a documentation truthfulness pass over every overstated claim listed in
   §3 and marked each corrected document with a `2026-10-14 audit` note — see
   §7 for the per-claim table.

## 7. Corrections applied 2026-10-14 (documentation truthfulness pass)

Each overstated claim was corrected **in its source document** and marked with a
`2026-10-14 audit` note or a roadmap correction. Commits by theme:

| # | Claim corrected | Source document(s) | Fix |
|---|-----------------|--------------------|-----|
| 3.3 | `K*` design system exists | `docs/roadmap.md` (U3), `docs/desktop-ui-implementation-plan.md` §6 | "NOT IMPLEMENTED" banner |
| 3.4 | Routing/Leshy & Settings functional | `docs/roadmap.md` (U6), `docs/desktop-ui-implementation-plan.md` §9 | "PARTIAL / placeholder" banner |
| 3.5 | Windows shows `SIMULATED` | `docs/desktop-ui-implementation-plan.md` §5 | "partial; no `SIMULATED` string" |
| 3.6 | 10 FakeCore fixtures | `docs/desktop-ui-implementation-plan.md` §5 | "only 4 demo scenarios" |
| 3.7 | Drawer uses `available_actions` | `docs/desktop-ui-implementation-plan.md` §8 | "violated; guessed from state" |
| 3.8 | "all 5 CTest" / "four Qt tests" | `docs/toad-roadmap.md`, `docs/go-vpn-orchestration-v2-progress.md` | list all five, note dead `real-core-ui-test` |
| 3.9 | Chapter 09 is `FUTURE` | `docs/toad-steps/09-desktop-ui.md` | "ACTIVE prototype" |
| 3.10 | 08A.4a pending-vs-PASS contradiction | `docs/toad-steps/08a4a-...md` | status reconciliation + per-item results |
| 3.11 | Leshy hardening "addressed" warnings | `docs/toad-steps/08a3r-...md` | scope caveat: no DNS write counters |
| 3.12 | VM soak "stable" | `docs/toad-steps/08a3r-...md` | "IPv4 main-table only" |
| 3.13 | Four privileged gates | `docs/toad-roadmap.md` | correction: five, incl. `dualstack` |
| 3.14 | `test-report.txt` current | `test-report.txt`, `08a4a` assertion count | SUPERSEDED banner; count fixed |
| — | 08A.4a OpenConnect blocked | `08a4a`, `08a4a` completion state | explicit BLOCKED caveat retained |

Also updated: the top-of-roadmap embedded handoff and the `08a4a` completion-state
handoff string now read as past tense with the OpenConnect caveat.

## 8. Follow-ups and remediation status

The user's directive after this audit was to **fix the overstated items in code,
not merely annotate the docs**. Status as of 2026-10-14 (after commits
`5cac3f2`, `6e209bd`):

1. **`K*` design system / Routing page / `SIMULATED` marker /
   `available_actions` drawer — PARTIALLY DONE.** The `SIMULATED` marker, ten
   deterministic FakeCore fixtures, `available_actions`-driven drawer, and a
   **subset** of `K*` (`KButton`, `KCard`, `KStatusDot`, `KTag`,
   `KSectionHeader`, `KInfoRow`, `KPageTitle`) are implemented, and the
   Routing/Settings placeholders were replaced with functional (prototype)
   pages (`5cac3f2`). The **full** planned `K*` set and the full drawer field
   surface remain open. C++/QML verified by inspection only — **the Qt build
   and CTest must be run on the Linux VM.**
2. **`real-core-ui-test` registered — DONE.** Now CTest target `real-core-ui`,
   `DISABLED TRUE` (needs a live `KIKIMORA_UI_SOCKET`) (`5cac3f2`).
3. **DNS write instrumentation + IPv6/endpoint-table soak — DONE (`6e209bd`),
   re-run pending.** `leshy-dns-integration.sh` now fingerprints DNS state and
   watches `/run/kikimora/leshy/dns` via `inotifywait` when available;
   `vm-dualstack-regression.sh` asserts v4/v6/endpoint-table stability;
   `dualstack-dns-routing-netns.sh` compares full route/rule state across two
   reconciles. The hardened gates have **not** been re-executed on a disposable
   VM since.
4. **`test-report.txt`** — left in place with a SUPERSEDED banner (acceptable
   interim; regenerate from current HEAD or delete in favour of the roadmap).
5. **Fix the mojibake/encoding damage in
   `docs/go-core-ui-toads-integration-plan.md`** — not done.
6. **Single-source-of-truth for status** — the roadmap handoff section serves
   this role; still not formally enforced.

### Code remediation commits

| Commit | Scope |
|--------|-------|
| `5cac3f2` | `feat(desktop)`: `CoreBackend.simulated` + `roleActionByName`, 10 FakeCore fixtures, `available_actions` drawer, `K*` subset, functional Routing/Settings, `SIMULATED` markers, `real-core-ui` CTest registration |
| `6e209bd` | `test(linux)`: DNS write monitoring + full v6/endpoint-table 51890 soak assertions in the three 08A.3R gates |
