# Desktop UI + UI↔core audit — 2026-11-10

Status: **AUTHORITATIVE UI FINDINGS — execution order is unified in docs/roadmap.md**

> **2026-11-16 continuation:** this audit remains historical finding detail,
> but the current implementation/status audit is
> `docs/toad-steps/08d1-prod-readiness-audit-2026-11-16.md`. Do not infer a
> current handoff from the 2026-11-10 source baseline.

Master core+UI roadmap:

~~~text
docs/roadmap.md
~~~

This audit remains the finding/evidence source for the desktop and UI↔core
defects. The master roadmap decides execution order and supplies the missing
cross-core/UI architecture choices.

This audit was intentionally produced separately from the 08D1 managed-routing
audit; the unified roadmap now composes both.

Audited source:

~~~text
Kikimora:
    6e874bb1c5ea265a258a4c632484da9dab86ab52
~~~

Primary implementation:

~~~text
desktop/
toad/internal/control/
toad/internal/toadctl/
toad/internal/platform/
.github/workflows/desktop-ui.yml
~~~

Companion test/coverage plans:

~~~text
docs/desktop-ui-test-execution-and-coverage-plan.md
docs/desktop-ui-executor-coverage-plan-2026-11-11.md
~~~

The 2026-11-11 file is the latest concrete executor packet and carries the
current-master baseline, exact test case names and acceptance gates.

No production code is changed by this audit.

---

# 1. Executive verdict

The visual/product structure has moved in the right direction.

The current main UI is already the requested two-page shape:

~~~text
Home
Settings
~~~

Profiles, role editing and routing-list editing are presented through the
profile/role drawers instead of permanent top-level pages.

That structure should be retained.

The release problem is the frontend/core truth boundary and the test system.

The desktop client still consumes an older subset of the core snapshot while
the core has gained safety-critical managed-routing state.

The automatic test suite is also materially weaker than the headline
"100% CTest passed" suggests because the real GUI↔core suites silently
QSKIP when no live core socket is supplied.

Current UI release status:

~~~text
two-page UI structure:
    RETAIN

fake-backend QML behaviour:
    useful but insufficient

RealCoreClient protocol mapping:
    PARTIAL

managed-routing operator truth:
    MISSING

live UI↔core automatic coverage:
    NOT RELIABLE

Ubuntu installed-UI acceptance:
    needs a deterministic hard-gate runner

Windows installed-UI acceptance:
    existing runner is not a hard gate

Desktop UI GitHub workflow:
    NOT CURRENT / NOT A RELEASE GATE
~~~

---

# 2. Current UI architecture

## 2.1 Backend seam

UI-facing contract:

~~~text
desktop/src/core/CoreBackend.h
~~~

Implementations:

~~~text
desktop/src/core/FakeCoreBackend.*
desktop/src/core/RealCoreClient.*
~~~

QML receives one backend object named `Core` and does not speak to the Go
control socket directly. This is the correct seam and should remain.

## 2.2 Current pages

Current `desktop/qml/Main.qml` has exactly two bottom-navigation items:

~~~text
Главная
Настройки
~~~

Home currently contains:

~~~text
aggregate connect/disconnect control
profile selector and profile creation
role list
role drawer
routing-list editing
tunnel create/import/remove
default-route selection
~~~

Settings currently contains:

~~~text
appearance
reduced motion
autostart
compact diagnostics
full diagnostics drawer
~~~

This matches the intended product direction better than the old five-tab UI.

Do not restore the five-tab layout merely to satisfy stale tests.

## 2.3 Core-originated role actions

The role drawer uses `roles[].available_actions`, and RealCoreClient maps the
core-reported action names to API methods.

This is a good ownership boundary: the core owns action eligibility; QML
renders it. Keep this design.

---

# 3. P0 UI↔core findings

## P0-UI1 — the UI ignores managed route-authority safety state

The current Go Snapshot publishes:

~~~text
startup_ready
managed_mode
mode_generation
activation_id_fingerprint
mode_transition
mode_transition_id_fingerprint
route_authority_ready
route_authority_error
~~~

Production source:

~~~text
toad/internal/control/control.go
    Snapshot
~~~

But `desktop/src/core/RealCoreClient.cpp::handleSnapshot()` does not parse any
of these fields, and CoreBackend has no properties for them.

Consequence:

A managed core can remain connected, with tunnel roles still reporting Ready,
while route_authority_ready is false after observer/authority loss. The current
UI can still render `Protected` because top-level presentation is driven by
aggregate_state only.

That is unsafe operator messaging.

Required frontend properties:

~~~text
startupReady
managedMode
modeGeneration
activationIdFingerprint
modeTransition
modeTransitionIdFingerprint
routeAuthorityReady
routeAuthorityError
~~~

Required presentation policy:

~~~text
if managedMode && !routeAuthorityReady:
    never display Protected
    show a high-visibility routing-authority warning
    display routeAuthorityError

if modeTransition != none:
    show transition state
    block ordinary connect/config/routing mutations

disconnect remains available where it can safely reduce exposure
diagnostics remain available in every state
~~~

## P0-UI2 — independent IPv4/IPv6 readiness is ignored

Go role snapshot publishes:

~~~text
roles[].families.ipv4.configured
roles[].families.ipv4.ready
roles[].families.ipv4.reason
roles[].families.ipv6.configured
roles[].families.ipv6.ready
roles[].families.ipv6.reason
~~~

Source:

~~~text
toad/internal/toadctl/protocol.go
    FamilyReadiness
    RouteFamilies
~~~

RealCoreClient ignores `families` and only consumes legacy `route_ready`.

`route_ready` means at least one configured family is usable; it does not mean
both IPv4 and IPv6 are ready.

Required RoleSnapshot fields:

~~~text
ipv4Configured
ipv4Ready
ipv4Reason
ipv6Configured
ipv6Ready
ipv6Reason
~~~

Role drawer and Diagnostics must show the families independently.

Never infer IPv6 readiness from routeReady.

## P0-UI3 — managed selected-route diagnostics are dropped

Go role snapshot contains `roles[].selected_routes` with:

~~~text
registered
active
parked
retiring
drift_repairs
park_pending
park_applying
park_active
park_retiring
ensures
duplicate_hits
kernel_mutations
denials
conflicts
capacity_denials
forgets
restores
~~~

Source:

~~~text
toad/internal/control/selected_routes.go
    SelectedRouteStats
~~~

RealCoreClient ignores this object entirely.

At minimum role/full diagnostics must expose:

~~~text
registered
active
park states
drift repairs
denials
conflicts
kernel mutations
~~~

Do not overload Home with every counter.

## P0-UI4 — ordinary CTest silently omits the main live GUI↔core coverage

Current CTest registers nine targets:

~~~text
real-core-client
real-core-e2e
real-core-ui
real-core-matrix
ui-behavior
system-theme
role-list-model
tray-live
tray-menu
~~~

A fresh local audit run reported:

~~~text
100% tests passed, 0 tests failed out of 9
~~~

but internal QtTest results included at least these skips:

~~~text
real-core-e2e:
    1 QSKIP without KIKIMORA_CORE_BINARY/KIKIMORA_FAKE_TOAD_BINARY

real-core-ui:
    1 QSKIP without KIKIMORA_UI_SOCKET

real-core-matrix:
    5 QSKIP without KIKIMORA_UI_SOCKET

tray-live:
    1 QSKIP without KIKIMORA_UI_SOCKET

tray-menu:
    translation case QSKIP when LinguistTools is absent
~~~

Therefore CTest target success is not evidence that live UI↔core paths ran.

Mandatory release suites must have zero skip paths.

## P0-UI5 — the live suites are stale and fail when actually executed

A live isolated audit run used:

~~~text
real kikimora-core
real IPC
toad/internal/control/testhelper as fake Toad runtime
temporary role configs
offscreen QML
~~~

Results:

### real-core-ui

~~~text
2 passed
1 failed
0 skipped
~~~

The hard failure is a stale assertion for the old status rendering
`Ready · r...`.

The suite also emitted:

~~~text
ThemeManager is not defined
~~~

because the live test fixture does not reproduce the production QML context.

### real-core-matrix

~~~text
4 passed
2 failed
1 skipped
~~~

Observed problems:

~~~text
settingsShowLiveState still expects 5 bottom tabs
current product intentionally has 2

profileSwitch may skip when the host has only one profile

routingListsRoundTrip assumes a list named primary
the test is therefore not hermetic to its fixture
~~~

### tray-live

~~~text
4 passed
0 failed
0 skipped
~~~

This is useful positive evidence that the real tray/core path works against an
isolated live core.

The stale tests must be corrected to the current two-page product; do not
restore obsolete UI behaviour to make them green.

## P0-UI6 — desktop CI does not run when the core API changes

Current workflow:

~~~text
.github/workflows/desktop-ui.yml
~~~

only triggers on pull-request changes to:

~~~text
desktop/**
.github/workflows/desktop-ui.yml
~~~

Changes to the core snapshot/API can therefore break the UI contract without
running Desktop UI CI.

This is exactly the class of gap that allowed the new D9 fields to remain
invisible to the frontend.

## P0-UI7 — there is no current Desktop UI GitHub evidence

The queried Desktop UI workflow history ends on 2026-10-20/21 for the most
recent entries returned.

The latest twelve queried runs are historical PR failures.

There is no current master run proving the audited desktop source.

The workflow also has no `push: master` trigger.

Old October runs must not be used as release evidence for current master.

## P0-UI8 — Windows guest runner does not fail the overall run

Current:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

executes suites and prints each suite's exit code, but it does not accumulate
failures and return a nonzero process exit at the end.

If the live composition never becomes Ready, it prints the condition and
`continue`s.

A red suite/readiness failure can therefore leave the runner itself successful.

The Windows VM runner is not currently a hard release gate.

---

# 4. P1 UI↔core correctness findings

## P1-UI1 — leshyIpv6 is lost by RoleListModel::get()

RealCoreClient correctly parses:

~~~text
roles[].leshy_ipv6 -> RoleSnapshot.leshyIpv6
~~~

RoleListModel also declares a `leshyIpv6` model role.

But `RoleListModel::get()` omits the field.

`KRoleDetailDrawer.qml` receives `Core.roles.get(index)` and defaults
`leshyIpv6` to true when it is undefined.

Consequence:

The role drawer can show IPv6 enabled when the core explicitly reports it
disabled.

Required regression:

~~~text
snapshot leshy_ipv6=false
-> RoleListModel::get()[leshyIpv6] == false
-> role IPv6 switch unchecked
~~~

## P1-UI2 — FakeCore diagnostics schema differs from the real core

Real Go diagnostics publish:

~~~text
managed_interfaces:
    map<string, {present: bool, managed: bool}>
~~~

FakeCoreBackend currently publishes a string value such as:

~~~text
kk-primary0: kernel
~~~

QML concatenates `owners[key]` as text.

Against a real core, that object renders like `[object Object]` instead of
useful ownership state.

FakeCoreBackend must mirror the exact real QVariant shape, and QML must format
`present` and `managed` explicitly.

## P1-UI3 — FakeCore Windows Leshy support is stale

FakeCoreBackend returns `leshySupported=false` on Windows.

Current Go `leshyPlatformSupported()` returns true for:

~~~text
linux
darwin
windows
~~~

The fake backend must not encode an obsolete platform capability matrix.

## P1-UI4 — RealCoreClient speaks API v2 and does not validate Handshake

Current client:

~~~text
desktop/src/core/RealCoreClient.cpp
    constexpr int apiVersion = 2
~~~

Current core:

~~~text
toad/internal/control/control.go
    APIVersion = 3
~~~

Old non-privileged calls still work on API v2, so this is not an immediate wire
break.

But RealCoreClient does not validate server `min_version`, `max_version` or
`capabilities` before declaring itself Connected.

Required direction:

~~~text
use API v3
validate v3 inside server min/max
retain server capabilities
expose apiCompatible/capabilities for diagnostics
incompatible core -> explicit Incompatible state
~~~

Do not turn protocol incompatibility into an endless generic reconnect loop.

## P1-UI5 — command error lifetime is stale

`m_lastCommandError` is set on transport/API error but successful ordinary
commands do not generally clear it.

An old error can remain visible after later successful operations.

Define one policy:

~~~text
clear a command-scoped error when a new command begins
and/or on that command's successful response
~~~

Do not clear a transport error merely because an unrelated stream snapshot
arrived.

## P1-UI6 — command gating during reconnect is incomplete

ConnectCircle disables only when:

~~~text
Core.coreState == Unavailable
~~~

Client states also include:

~~~text
Disconnected
Connecting
~~~

while `sendMessage()` silently drops requests unless the socket is connected.

This creates a clickable no-op window during reconnect.

Expose an authoritative `commandChannelReady` (or equivalent) and disable
mutating UI until a compatible handshake plus authoritative snapshot completed.

## P1-UI7 — live test fixtures do not reproduce production QML context

Production `main.cpp` installs:

~~~text
Core
Desktop
ThemeManager
SystemIconProvider
Basic Qt Quick style
~~~

Current live fixtures install only a subset.

Observed result:

~~~text
ThemeManager is not defined
~~~

All QML tests should use one shared bootstrap matching production main.

## P1-UI8 — unexpected QML warnings do not fail tests

Fresh `ui-behavior` passed all cases but repeatedly printed:

~~~text
QQuickStyle::setStyle() must be called before loading QML
~~~

The test remained green.

Unexpected runtime warnings must fail tests, including:

~~~text
ReferenceError
TypeError
Binding loop
QQmlApplicationEngine failed
QQuickStyle ordering errors
~~~

Narrow platform-specific exceptions may be explicitly whitelisted.

## P1-UI9 — routing-list live test is not hermetic

`RealCoreMatrixTest::routingListsRoundTrip` hard-codes `primary`.

That is host/config dependent.

The hermetic fixture must own deterministic route-list names or select an
actual returned list rather than assuming a developer's profile naming.

## P1-UI10 — current live core fixture leaks into product filesystem defaults

The manual live audit core emitted a generated-Leshy config error under:

~~~text
/etc/kikimora/leshy/
~~~

because development `serve` still renders the product default Leshy path.

`GetRoutingLists` also directly reads `config.DefaultLeshyConfigPath()` rather
than the Manager's configured path.

Hermetic UI/core tests must never touch:

~~~text
/etc/kikimora
/var/lib/kikimora
/run/kikimora
C:\ProgramData\Kikimora
~~~

unless they are explicitly installed-VM tests.

The testable core needs one injected generated-Leshy config path, and all
routing-list reads/writes must use the same Manager path.

---

# 5. Automatic UI test coverage audit

## 5.1 real-core-client

Type:

~~~text
protocol/client unit test with a fake socket server
~~~

Good coverage:

~~~text
length-prefixed framing
old snapshot field mapping
profiles
structured errors
revision-gap resync
partial aggregate labels
stale resync reply
bypass read-back
~~~

Missing:

~~~text
D9 managed-state fields
families
selected_routes
handshake compatibility/capabilities
command-error clear
disconnect/reconnect authority state
~~~

## 5.2 real-core-e2e

Type:

~~~text
real Go core + fake Toads + RealCoreClient
no QML
~~~

The Linux workflow supplies binaries to this test.

A manual audit run with real built fixtures passed:

~~~text
3 passed
0 failed
0 skipped
~~~

This is the best current proof that the C++ protocol client can talk to a real
core, but it does not load QML and is not yet fully filesystem-hermetic.

## 5.3 ui-behavior

Type:

~~~text
QML + FakeCoreBackend
offscreen
~~~

Fresh audit result:

~~~text
22 QtTest passes including init/cleanup
0 failed
0 skipped
~~~

Useful areas:

~~~text
profile/tunnel creation
profile switching
two-page navigation
role drawer
membership edits
routing-list edits
dirty-draft confirmation
aggregate lifecycle
partial state
role actions
settings/diagnostics
default route
design-system loading
~~~

Limitation:

It proves the fake contract. The fake currently differs from real core shapes
and the suite stays green despite runtime warnings.

## 5.4 real-core-ui

Type:

~~~text
QML + RealCoreClient + external live core
~~~

Default:

~~~text
QSKIP without KIKIMORA_UI_SOCKET
~~~

Current isolated live result:

~~~text
2 pass
1 fail
0 skip
~~~

It is not currently a release gate.

## 5.5 real-core-matrix

Type:

~~~text
QML/client action matrix + external live core
~~~

Default:

~~~text
5 test cases QSKIP without KIKIMORA_UI_SOCKET
~~~

Current isolated live audit:

~~~text
4 pass
2 fail
1 skip
~~~

Failures are current test/harness drift, not reasons to revert the product UI.

## 5.6 tray-live

Type:

~~~text
DesktopIntegration tray + RealCoreClient + live core
~~~

Default:

~~~text
QSKIP without socket
~~~

Current isolated live audit:

~~~text
4 pass
0 fail
0 skip
~~~

Keep this suite but make its live fixture mandatory in CI.

## 5.7 tray-menu

Type:

~~~text
tray + fake backend
~~~

Fresh local result:

~~~text
4 pass
1 translation skip
~~~

The release CI lane must install LinguistTools and make translation coverage
non-optional.

## 5.8 system-theme

Covers palette and persistence.

It does not prove Settings switches drive ThemeManager correctly through QML.

## 5.9 role-list-model

Covers model mechanics and most existing fields.

It does not catch the missing `leshyIpv6` key in `get()`.

A fresh run also emitted a Qt warning about constructing an invalid type.
Investigate and remove that warning rather than accepting it as baseline.

## 5.10 currently unproved functional areas

Current automatic tests do not directly prove:

~~~text
managed route-authority warning and action gating
mode-transition banner/gating
mode generation/fingerprint rendering
independent IPv4/IPv6 family readiness
selected-route counters
real managed_interfaces diagnostics shape
IPv6 switch reflects leshy_ipv6=false
autostart toggle through DesktopIntegration
window close-to-tray lifecycle
show/hide Settings actions
clipboard import path
diagnostics capture toggle through a real core
core API incompatibility UX
command error recovery/clear
real reconnect UI after core restart
keyboard/focus accessibility
minimum-size and high-DPI layout
visual safety-state regression in light/dark modes
~~~

---

# 6. Desktop UI CI audit

Current workflow:

~~~text
.github/workflows/desktop-ui.yml
~~~

Problems:

1. No push-to-master lane.
2. Core contract changes do not trigger it.
3. Linux alone builds Go core/fake-Toad fixtures.
4. live QML suites receive no mandatory live socket and skip.
5. Linux package dependency checks out Leshy v0.4.0 instead of the current companion pin.
6. no zero-skip assertion.
7. no unexpected-QML-warning gate.
8. no current coverage report.
9. no current workflow evidence for audited master.

The workflow must be rebuilt according to the companion execution plan.

---

# 7. Windows lab runner audit

Current runner:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

Good properties:

~~~text
adds Qt runtime to PATH
uses offscreen/software rendering
waits for a live core
runs fake and live suites
sets a large QTest timeout
~~~

Release blockers:

~~~text
does not aggregate suite failures into final process exit
readiness failure uses continue
does not require zero mandatory skips
does not run every required suite/category
does not pin/report source SHA and binary versions
~~~

It must become a hard gate.

---

# 8. Documentation drift

`docs/ui-coverage-matrix.md` still starts from the 2026-10 UI and contains
historical five-tab assumptions and old test counts.

`desktop/README.md` also contains prototype-era statements that no longer
fully describe the real-core product.

Do not delete the historical material.

Mark the old matrix as superseded and use:

~~~text
docs/desktop-ui-core-audit-2026-11-10.md
docs/desktop-ui-test-execution-and-coverage-plan.md
~~~

as the current authority.

---

# 9. Release decision

The UI is **not release-accepted** at this audited head.

The two-page UX should be retained.

Blocking work:

~~~text
U1 update CoreBackend/RealCoreClient to current core truth
U2 fix model/fake-schema correctness
U3 render and gate managed authority safely
U4 repair live tests to current UI and production QML context
U5 make hermetic real-core UI tests mandatory on all CI OSes
U6 make Desktop UI workflow current and core-triggered
U7 harden Ubuntu/Win10 installed runners
U8 add semantic/accessibility/visual coverage
U9 run exact-head CI + both lab VMs with zero mandatory skips
~~~

Execution details and exact commands are in:

~~~text
docs/desktop-ui-test-execution-and-coverage-plan.md
~~~

---

## C8R 1.4.0 + final U8e/U9e + U12 — GREEN — 2026-11-24

### C8R on the 1.4.0 candidate

The 66-row matrix re-ran on the immutable 1.4.0 MSI (the J5 artifact),
with the 1.4.0-bundled Leshy and the final-head failpoint core:

~~~text
source sha      baf40e3c1041b990defd608d7e27148055c41e2b
harness         6a2ae8d1447a2d704fe1c9b9809279f35d317561eff13f776d4d6745765fdab0
candidate MSI   96a137e604a28f73ba49f8e49adc52e269271364ec704b171f8e40247867f7c2
failpoint core  a3444bbccee3d0fcb42d4fb42f5d1c785a74c1b01e228598778f22c5955d2590
                (the Go source is identical 461092d..baf40e3)
leshy           e0e440fbd317639a05db4fcecce32822523aa4495c41355162760c350211a648
preflight       ProductName=Kikimora Version=1.4.0
RESULT          PASS=66 FAIL=0
~~~

This closes the MajorUpgrade/ProductVersion risk left by the 1.3.1 C8R
re-anchor: legacy-local, legacy-managed, partial-destination, all four
in-flight phases and the restart subrows migrated on the 1.4.0 package.

### Final U8e — installed Linux

~~~text
candidate deb   7fd1335b19f3a5c43f6d6bb0b01eb4d00f74ea109639aea8ac47a94ee6de276b
expected        1.4.0, source baf40e3
suites          ui-behavior 38, system-theme 5, role-list-model 12,
                tray-menu 12, real-core-client 32, real-core-ui 6,
                real-core-matrix 14, tray-live 4
RESULT          123 passed, 0 failed, 0 skipped
runner          all green, zero mandatory skips, RC=0, no unexpected QML warning
~~~

### Final U9e — installed Windows

~~~text
candidate MSI   96a137e604a28f73ba49f8e49adc52e269271364ec704b171f8e40247867f7c2
installed core  kikimora-core 1.4.0, backend real
installed leshy leshy 0.6.0
suites          ui-behavior 38, system-theme 5, role-list-model 12,
                tray-menu 12, real-core-client 32, real-core-ui 6,
                real-core-matrix 14, tray-live 4
delegated       ui-accessibility, ui-visual-state (exact-head CI artifact)
RESULT          123 passed, 0 failed, 0 skipped
runner          all green, zero mandatory skips, U9E_RC=0
~~~

The installed package is the exact J MSI. The suite binaries are the same
desktop test build used by the prior U9e round; the desktop source is
unchanged by J3 (VERSION/install/workflow only).

### U12 — closure record

~~~text
Kikimora source   baf40e3c1041b990defd608d7e27148055c41e2b (VERSION 1.4.0)
Leshy             v0.6.0 / b87fea8d0145f9c5261b9854f8af2ac659fc0695
Desktop UI        45465423234
  macos-15          SUCCESS (acquired on attempt 2 after runner cancellation)
  ubuntu-latest     SUCCESS
  windows-latest    SUCCESS
  U10 goldens       SUCCESS
  U10 compare       SUCCESS
CI QtTest totals  ubuntu 147 passed, windows 137 passed, macos 147 passed;
                  0 failed, 0 skipped on every OS
Installed totals  Linux 123 passed, Windows 123 passed;
                  0 failed, 0 skipped
Screenshots       u10-goldens-qt683 (9 PNGs)
Logs              desktop-ui-logs-ubuntu-latest,
                  desktop-ui-logs-windows-latest,
                  desktop-ui-logs-macos-15
C8R 1.4.0         PASS=66 FAIL=0
~~~

Coverage authority remains `docs/ui-coverage-matrix.md` for the surface map;
the historical October tables are not rewritten. The numbers above are the
final installed-UI evidence for the J candidate.

> **Superseded artifact note (2026-11-24).** The published 1.4.0 assets were
> rebuilt at 386e71 to fix the kk --help version banner. Final hashes:
> deb 65904655fd8099bbb0dbc4abe31f9d40b17db5eed7f9cfdeaf00895d5363e079,
> MSI 8ae1022ed8d262ceaabc2f77e56f761e3be625fde61f9b56ae3cce361f56e653.
> U8e/U9e re-ran 123/0/0 on those files; see the execution log.
