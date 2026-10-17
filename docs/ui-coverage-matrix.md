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
