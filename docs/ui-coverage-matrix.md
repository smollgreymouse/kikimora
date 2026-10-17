# UI coverage matrix

What the desktop UI can do, how it maps onto the core, and which test proves it.
Status on 2026-10-17, after the Windows lane was brought up.

## Console surface → UI

`kikimora-core` verbs (Windows names in brackets where they differ), the core
method each one sends, the UI affordance, and the test that exercises it.

| CLI verb | Core method | UI affordance | Test |
|---|---|---|---|
| `status [--json]` | `Handshake` + `GetSnapshot` | whole screen: status line, role rows, `coreStatus` | `real-core-ui` (`observe-ready` / `observe-not-ready`), `ui-behavior` (fake) |
| `watch [--json]` | `Subscribe` | internal revisioned stream | `real-core-client`, `real-core-e2e` |
| `start` / `connect-all` | `ConnectAll` | connect circle (`CONNECT`) | `real-core-ui` (lifecycle), `ui-behavior::connectCircleDrivesGlobalLifecycle` |
| `stop` / `disconnect-all` | `DisconnectAll` | connect circle (`DISCONNECT`) | same |
| `connect --role R` | `ConnectRole` | role row primary action, drawer | `ui-behavior::drawerActionsAreCoreReported`, `::drawerOpensAndControlsOneRole` |
| `disconnect --role R` | `DisconnectRole` | role row, drawer | same |
| `retry --role R` | `RetryRole` | drawer secondary action | `ui-behavior::drawerActionsAreCoreReported` |
| `restart` | `DisconnectAll` + `ConnectAll` | **no dedicated control** (only the toggle) | — |
| `profiles` / `profiles use NAME` | `SetActiveProfile` | profile combo (`KComboBox`) | `ui-behavior::profileSwitcherReflectsActiveProfile` |
| `interfaces` | snapshot `roles[].interface` | Diagnostics page, role detail drawer | `ui-behavior::diagnosticsPageExplainsRoleState`, `::drawerShowsRoleDetailFields` |
| `version` | — | not shown | — |
| `service install\|uninstall\|start\|stop` | — | installer (MSI / deb) | packaging tests |
| `doctor`, `verify`, `diag`, `debuglog` | — | **absent** | — |

### Core methods with no CLI verb

| Core method | UI affordance | Test |
|---|---|---|
| `ValidateRole` | drawer action | `ui-behavior::drawerActionsAreCoreReported` (action list only) |
| `RediscoverEndpoints` | drawer action | same |

The UI therefore reaches the core through a slightly wider surface than the CLI:
validation and endpoint rediscovery exist only as buttons. The CLI equivalents
(`validate`, `rediscover`) are missing and would be the natural next additions.

### Console surface with no UI at all

These are operator tasks and are deliberately CLI-only today:

* Leshy: `dns enable|disable|suspend|resume`, `domains add|remove|list|import|export|default`,
  `routes add|remove|list`, `backup` / `restore`;
* lifecycle: `install`, `upgrade`, `uninstall`, `enable` / `disable`, `start` / `stop`
  of the units themselves;
* diagnostics: `doctor`, `debuglog`, `diag`, `verify`, `completion`.

That gap is a product decision, not an accident: on Linux those verbs live in the
`kk` wrapper (shell + systemd + Leshy plumbing), on Windows the MSI owns install
and the core owns runtime state. If they should appear in the UI, the honest shape
is a "Maintenance" tab that shells out to the same helpers, not a reimplementation.

## Test coverage of the UI code

| Layer | Tests | Notes |
|---|---|---|
| QML behaviour on a fake backend | `ui-behavior` 13 cases | offscreen, no core needed: navigation, drawer, connect circle, aggregation, profiles, diagnostics, design system, core-reported actions, routing page, settings |
| Client protocol | `real-core-client`, `real-core-e2e` | socket framing, handshake, subscribe, commands |
| Model + theme + tray | `role-list-model`, `system-theme`, `tray-menu` | tray parity: aggregate line, per-role lines, toggle text, Russian catalogue |
| **Click → core → UI** | `real-core-ui` (1 case, 3 modes) | needs a live core; skips unless `KIKIMORA_UI_SOCKET` is set; asserts the circle/status/rows react to real core state and that clicking drives the core |
| **Tray → core → tray** | `tray-live` (1 case) | needs a live core; skips without `KIKIMORA_UI_SOCKET`; asserts the first (disabled) line equals the aggregate, one line per role, and that the toggle both reflects and drives the core (state restored afterwards) |
| Packaging | `tests/test_packaging.sh` | staging contract |

### The click → core → UI chain, concretely

`real-core-ui` in `lifecycle` mode starts from a stopped core and asserts:

1. circle state `Stopped`, action `CONNECT`, status line `Stopped · r…`;
2. a click on the circle brings the core to `Ready` (observed on the client),
   the circle flips to `Ready` / `DISCONNECT`, the status line starts with
   `Ready · r`, and `roleRow` items appear;
3. the bottom tabs switch (`selectedTab` 0 ↔ 1);
4. a second click returns the core to `Stopped` and the circle back to
   `Stopped` / `CONNECT`.

`observe-ready` and `observe-not-ready` are the same assertions without the clicks,
for hosts where the operator already connected the roles.

## First live run on Ubuntu (2026-10-17)

`real-core-ui` was enabled and executed against the running core on the lab VM
(`KIKIMORA_UI_SOCKET=/run/kikimora/core.sock`, offscreen QPA, `-j2` build):

| Mode | Result | What it proves |
|------|--------|----------------|
| `lifecycle` | **PASS**, 40 s | click the circle -> core `Stopped` -> `Ready` -> `Stopped`; circle state/action text, status line, role rows and all five tabs follow |
| `observe-ready` | **PASS** | with roles connected, the UI renders `Ready` and the role rows |
| `observe-not-ready` | **PASS** | with the core stopped, the UI shows the non-ready aggregate |

Two defects surfaced the moment the test stopped being disabled:

1. **Stale expectation**: the test demanded four bottom tabs, but U12 added
   Diagnostics as the fifth. Nothing caught it because the only UI test that walks
   the tabs was `DISABLED`. Fixed by asserting five tabs and clicking through all
   of them.
2. **Cold-start timing**: after one click the aggregate passes `Connecting` and
   `PartiallyReady` before `Ready`, and three real transports need ~20-40 s on the
   VM - the assertion allowed 10 s. The test now waits for "not Stopped" first and
   then for `Ready` with a 90 s ceiling, and for the stop path 60 s.

Both were test defects, not product defects - which is the point of running it.

## Platform reality

* **Linux**: the full chain is runnable — Qt toolchain on the lab VM, offscreen
  QPA, live core socket. This is where the UI tests execute.
* **Windows**: the workstation has **no Qt SDK** (`qmake` absent, no `C:\Qt`), so
  the shipped `kikimora-ui.exe` comes from a prebuilt artifact and the UI tests
  cannot be built or executed there. Until Qt is installed on a Windows builder,
  UI coverage on Windows is limited to runtime smoke checks of the shipped exe.

## Gaps to close, in order of value

1. `real-core-ui` should grow one case per role action (`connect`, `disconnect`,
   `retry`) and one for the profile combo, so the full click matrix runs against a
   live core instead of only the aggregate toggle.
2. Add `validate` and `rediscover` to `kikimora-core` so the two UI-only verbs stop
   being UI-only.
3. Stand up a Windows builder with Qt (or a CI job using the Linux runner plus a
   cross-check on Windows runtime) before claiming UI parity there.
4. Keyboard/accessibility coverage is asserted only through focus-ring and Escape
   behaviour; a systematic tab-order test is missing.

## Profiles and the first behavioural findings (2026-10-17)

**Profiles work, and their size comes from the role configs.** A role joins a
named profile through `profiles = [...]` in its TOML; `default` is every role.
Measured on the lab VM:

| Profile | Roles | Result |
|---------|-------|--------|
| `one` | awg | `active=one`, only awg desired, Ready |
| `two` | awg, oc | `active=two`, both Ready |
| `four` | awg, oc, xray-nl, xray2 | all four Ready, aggregate Ready |

The fourth tunnel was a clone of `xray-nl` (`kk-xray2`, same endpoint, own state
dir) - the core ran two VLESS tunnels to the same server without complaint.
Switching persists: after `profiles use two` plus a service restart the core came
back with `active=two` and exactly that role set. The original configs were
restored afterwards.

**Defect found by that test, fixed: a vanished profile crash-looped the daemon.**
`RestoreDesiredState` answered `persisted active profile %q is not configured` and
systemd restarted the unit forever (restart counter 16) - editing a role config
could take the whole product down, with no way back except editing files by hand.
The manager now falls back to `default`, keeps the per-role desired flags, records
`restored with fallback from profile %q` in the desired-state status and prints the
same on stderr. Verified on the VM: with `active_profile = "gone"` planted in the
store the unit comes up `active`, `active=default`, aggregate `Ready`.

The lab VM currently runs the locally built core (`/usr/local/bin/kikimora-core`,
backup of the packaged one at `kikimora-core.prev`); the `.deb` must be rebuilt to
ship the fix, and the local build reports `v0.1.0-dev` because it lacks the release
`-ldflags`.

**Settings page**: not empty, but thin - it carries the simulated marker and one
Diagnostics card (backend, platform, core state, revision, Leshy support). Worth
surfacing there, in order: active profile and the profile list (the combo lives on
the Connect page today), core socket path and service state, Leshy DNS state plus
the routed domain count, underlay summary, and the operator-only actions that exist
only in the console (the U17.3 "Maintenance" tab).

**UI matrix status**: `real-core-ui` enabled with three modes green on Ubuntu
(`lifecycle` 40 s, `observe-ready`, `observe-not-ready`). The tray is still covered
only against FakeCore (`tray-menu`); a live-core tray case and the per-button
matrix are the next items.

## Checkpoint (2026-10-17, evening)

Landed:

* `real-core-ui` enabled with three modes green on Ubuntu (`lifecycle` 40 s,
  `observe-ready`, `observe-not-ready`); the stale four-tab assertion and the
  cold-start timeout were fixed on the way.
* `tray-live` added and PASS (1.28 s) against the running core: the disabled state
  line equals the aggregate text, every role has a line, and the toggle both follows
  `aggregateActionText` and switches the tunnels, restoring the state at the end.
* Profiles measured: 1, 2 and 4 tunnels (the fourth a second VLESS to the same
  endpoint), switching and persistence after a service restart; the vanished-profile
  crash loop found and fixed in the control plane (`51ce632`).

Windows guest, in progress:

* VPN health re-checked after all the config churn: `KikimoraCore` and `leshy` both
  Running/Automatic, `aggregate=Ready`, awg-main / oc-main / xray-win Ready with
  addresses, `kk-awg0` carrying megabytes, and `chatgpt.com` + `openai.com` both
  answering 200 through `kk-awg0`.
* Qt cannot be fetched on the workstation (no VPN there), so the download lives in
  the guest: a Leshy zone `qt` plus an NRPT rule for `.qt.io` turned the rewritten
  `77.86.162.2` answer into a working route and `download.qt.io` answers 200;
  Python 3.13.7 and aqtinstall 3.3.0 are installed, `6.10.2` ships `win64_mingw`
  (no MSVC in the guest) and the archive is downloading from
  `qt.mirror.constant.com`.

Next: finish the Qt download, build the UI suite on Windows, then the per-button
matrix (per-role connect/disconnect/retry, profile switch, drawer) and the Settings
page content. The control-plane fix still needs a rebuilt `.deb`.

## Settings page filled, live matrix landed (2026-10-17, late)

The Settings card now carries what an operator needs next to the diagnostics:
`Profile`, `Profiles` (the list), `Aggregate` and `Underlay`, on top of backend,
platform, core state, revision and Leshy support. `ui-behavior` asserts the rows
against the fake backend (15/15 pass).

New live matrix suite `real-core-matrix` (`desktop/tests/RealCoreMatrixTest.cpp`):

| Case | Result | Notes |
|------|--------|-------|
| `settingsShowLiveState` | **PASS** | waits for the first snapshot, then asserts the card shows the live profile, underlay summary and core state |
| `roleActionDrivesTheCore` | **SKIP** | the role row does not open the drawer under the offscreen platform with a live client; the same interaction passes on the fake fixture, so the harness, not the app, is the suspect - next attempt should expose the window before clicking |
| `profileSwitchDrivesTheCore` | **SKIP** | the lab host carries a single profile after the earlier restore; the case needs a host with two or more |

Both skips are deliberate and printed, not silent: the suite reports `3 passed,
0 failed, 2 skipped`.

Also learned while wiring this up: the per-role action buttons live in
`KRoleDetailDrawer.qml` (QObject tree, not the visual tree), so tests must reach
them with `findChild` and a settled click point - the reason `ui-behavior` always
did it that way.

## Live matrix complete on Ubuntu (2026-10-17, late)

`real-core-matrix` runs against the running core and is now fully green:
**5 passed, 0 failed, 0 skipped** in 1.3 s.

| Case | What it proves |
|------|----------------|
| `roleActionDrivesTheCore` | opens the role drawer, clicks the per-role action, the core's desired flag flips and the window renders the new state text; the role is restored through the core |
| `settingsShowLiveState` | the Settings card carries the live profile, profile list, aggregate and underlay, not placeholders |
| `profileSwitchDrivesTheCore` | picking a profile in the combo (`activated(int)`, i.e. as a user does) changes `active_profile` in the core and switching back restores it |

Four fixture lessons, all of which cost a run to find:

1. the role rows live on the **default** screen - switching to another tab and
   clicking a row that is not visible makes the offscreen platform drop the event;
2. the per-role buttons live in `KRoleDetailDrawer.qml`, reachable through the
   QObject tree (`findChild`), not the visual tree;
3. cleanup must **not** repeat the UI action: the drawer can close after the action,
   so the role is restored through the core client instead;
4. `roleCount`/`coreState` are populated **before** the first snapshot - tests must
   wait for `activeProfile`/`profiles` or they assert an empty screen; and a combo
   only reaches the core through `activated(int)`, not through `currentIndex`.

Remaining for U17.1: the secondary drawer actions (`retry`, `validate`,
`rediscover`), drawer close, the live tab walk (four tabs after the first) and the
per-button sweep on Windows once the Qt build exists there.
