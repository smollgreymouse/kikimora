# Desktop UI automated test execution and coverage plan

Status: **UI TEST-LAYER REFERENCE — execution order comes from docs/roadmap.md**

Master order/status authority:

~~~text
docs/roadmap.md
~~~

Latest concrete UI executor packet:

~~~text
docs/desktop-ui-executor-coverage-plan-2026-11-11.md
~~~

Use the master roadmap for cross-core/UI ordering and the 2026-11-11 packet for
UI implementation/test detail. This file remains the architectural test-layer
reference.

Audit source:

~~~text
docs/desktop-ui-core-audit-2026-11-10.md
~~~

Audited baseline:

~~~text
Kikimora:
    6e874bb1c5ea265a258a4c632484da9dab86ab52
~~~

Goal:

Make the desktop UI and the UI↔core contract release-testable on Linux,
Windows and macOS, with no mandatory suite silently passing through QSKIP.

The executor may fix implementation/test/harness bugs autonomously but must not
restore obsolete UI structure, weaken assertions, hide warnings, or replace a
required real-core/VM test with a FakeCore unit test.

---

# 1. What the current test suites are and how to run them

## 1.1 Configure and build all desktop tests

From repository root:

~~~bash
cmake -S desktop -B build/desktop -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop --parallel
~~~

List CTest targets:

~~~bash
ctest --test-dir build/desktop -N
~~~

Current expected target names:

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

## 1.2 Run the current non-live/default CTest suite

~~~bash
QT_QPA_PLATFORM=offscreen \
QT_QUICK_BACKEND=software \
ctest --test-dir build/desktop --output-on-failure -V
~~~

Important:

At the audited baseline, a green CTest run is **not** enough. The live targets
can QSKIP because no KIKIMORA_UI_SOCKET is present.

Always read the QtTest Totals and SKIP lines, not only CTest's final percentage.

## 1.3 Run one suite directly

Examples:

~~~bash
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software \
  build/desktop/ui-behavior-test

build/desktop/real-core-client-test

QT_QPA_PLATFORM=offscreen \
  build/desktop/system-theme-test
~~~

Direct execution is useful during development because QtTest prints individual
case names and warning lines.

## 1.4 Current real-core-e2e usage

Build the Go fixtures:

~~~bash
mkdir -p /tmp/kikimora-ui-e2e
(cd toad && go build -o /tmp/kikimora-ui-e2e/kikimora-core ./cmd/kikimora-core)
(cd toad && go build -o /tmp/kikimora-ui-e2e/fake-toad ./internal/control/testhelper)
~~~

Then:

~~~bash
KIKIMORA_CORE_BINARY=/tmp/kikimora-ui-e2e/kikimora-core \
KIKIMORA_FAKE_TOAD_BINARY=/tmp/kikimora-ui-e2e/fake-toad \
ctest --test-dir build/desktop -R '^real-core-e2e$' -V
~~~

At the audited baseline this path passes when the fixtures are supplied.

## 1.5 Current external live-core usage

Against an already running core:

~~~bash
KIKIMORA_UI_SOCKET=/run/kikimora/core.sock \
QT_QPA_PLATFORM=offscreen \
QT_QUICK_BACKEND=software \
build/desktop/real-core-ui-test
~~~

`real-core-ui-test` currently understands:

~~~text
KIKIMORA_UI_TEST_MODE=lifecycle
KIKIMORA_UI_TEST_MODE=observe-ready
KIKIMORA_UI_TEST_MODE=observe-not-ready
~~~

Other live suites:

~~~bash
KIKIMORA_UI_SOCKET=/run/kikimora/core.sock \
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software \
build/desktop/real-core-matrix-test

KIKIMORA_UI_SOCKET=/run/kikimora/core.sock \
QT_QPA_PLATFORM=offscreen \
build/desktop/tray-live-test
~~~

These commands are useful for manual diagnosis **today**, but after U4 they
must not be the primary CI architecture; the CI fixture must create its own
core and configuration.

## 1.6 Current Windows lab runner

Current command on win10:

~~~powershell
powershell -ExecutionPolicy Bypass -File C:\uitests\guest-run-suites.ps1
~~~

Current runner is diagnostic only until U7: it does not propagate every suite
failure/readiness failure to its process exit.

## 1.7 How to interpret QtTest output

Release evidence must record, for every mandatory suite:

~~~text
Totals: X passed, 0 failed, 0 skipped
process exit = 0
~~~

A mandatory QSKIP is a failure of the harness/gate, even if QtTest/CTest exits
successfully.

Optional platform-specific cases must be explicitly listed in this document;
do not create new optional skips ad hoc.

---

# 2. Test ownership model

Every behaviour belongs to one of four layers.

## Layer A — pure model/protocol units

Examples:

~~~text
real-core-client
role-list-model
system-theme
~~~

Purpose:

~~~text
JSON/wire mapping
state derivation
model roles
error/reconnect logic
theme persistence
~~~

No QML visual interaction is required.

## Layer B — QML semantics against FakeCore

Primary suite:

~~~text
ui-behavior
tray-menu
~~~

Purpose:

~~~text
layout/interaction semantics
dialogs/drawers
button wiring
fake state matrix
accessibility object names
presentation policy
~~~

FakeCore must mirror the exact real core schema. It is not allowed to invent
a simplified incompatible shape merely to make QML tests easy.

## Layer C — hermetic real core + real IPC

Suites:

~~~text
real-core-e2e
real-core-ui
real-core-matrix
tray-live
~~~

Purpose:

~~~text
actual Go core contract
actual frame protocol/socket
actual snapshot revisions
QML click -> C++ client -> Go core -> snapshot -> QML
tray action -> core -> tray
~~~

This layer must create its own temp core, fake Toads, configs and paths.

It must run on Linux/macOS/Windows CI with zero mandatory skips.

## Layer D — installed lab VM

Platforms:

~~~text
ubuntu2604
win10
~~~

Purpose:

~~~text
installed package
real service/socket ACL
desktop process
tray/platform integration
autostart
real package versions
service restart/reconnect
platform-specific rendering
~~~

Layer D is not a substitute for hermetic Layer C.

---

# 3. U1 — align CoreBackend and RealCoreClient with current core truth

Files:

~~~text
desktop/src/core/CoreBackend.h
desktop/src/core/RealCoreClient.h
desktop/src/core/RealCoreClient.cpp
desktop/src/core/FakeCoreBackend.h
desktop/src/core/FakeCoreBackend.cpp
desktop/src/models/RoleListModel.h
desktop/src/models/RoleListModel.cpp
desktop/tests/RealCoreClientTest.cpp
desktop/tests/RoleListModelTest.cpp
~~~

## 3.1 Add top-level managed authority properties

Add CoreBackend properties/readers for:

~~~text
startupReady: bool
managedMode: bool
modeGeneration: qulonglong
activationIdFingerprint: QString
modeTransition: QString
modeTransitionIdFingerprint: QString
routeAuthorityReady: bool
routeAuthorityError: QString
commandChannelReady: bool
apiCompatible: bool
coreCapabilities: QStringList
~~~

RealCoreClient parses them from every authoritative snapshot/handshake.

FakeCoreBackend must expose them with realistic defaults.

Recommended FakeCore safe baseline:

~~~text
startupReady = true
managedMode = false
modeGeneration = 0
modeTransition = none
routeAuthorityReady = true
commandChannelReady = true
apiCompatible = true
~~~

Add explicit fake scenarios for:

~~~text
managed healthy
managed authority lost
mode transition prepared/config_written/marker_written
API incompatible
~~~

## 3.2 Upgrade handshake handling

Change RealCoreClient API request version to 3.

On handshake response:

~~~text
validate min_version <= 3 <= max_version
store capabilities
only then declare command channel ready
request snapshot
subscribe
~~~

If incompatible:

~~~text
apiCompatible = false
commandChannelReady = false
coreState = Incompatible
retain explanatory lastCommandError
do not reconnect-loop endlessly
~~~

## 3.3 Parse family readiness

Extend RoleSnapshot and RoleListModel with:

~~~text
ipv4Configured
ipv4Ready
ipv4Reason
ipv6Configured
ipv6Ready
ipv6Reason
~~~

Map exactly from `roles[].families`.

## 3.4 Parse selected-route stats

Prefer a QVariantMap property in RoleSnapshot for diagnostic counters unless
strongly typed fields materially improve the UI.

Required keys preserved:

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

Expose it through RoleListModel role names and `get()`.

## 3.5 Fix `leshyIpv6` map loss

Add `leshyIpv6` to `RoleListModel::get()`.

## 3.6 Fix command error state

Give each outgoing command a deterministic error lifecycle.

At minimum:

~~~text
new user command clears prior command-scoped error
failure sets the new error
success keeps it clear
transport disconnect sets a transport error/state
stream snapshots do not clear transport errors by themselves
successful reconnect + authoritative snapshot clears transport error
~~~

## 3.7 U1 tests

Add RealCoreClientTest cases for:

~~~text
mapsManagedAuthorityFields
mapsIndependentFamilyReadiness
mapsSelectedRouteStats
acceptsApiV3HandshakeAndCapabilities
rejectsIncompatibleHandshake
commandChannelNotReadyBeforeHandshakeSnapshot
commandErrorClearsAfterSuccessfulCommand
reconnectResetsAuthorityAndRevision
~~~

Add RoleListModelTest:

~~~text
getCarriesLeshyIpv6AndFamilyState
getCarriesSelectedRouteStats
~~~

Exit gate:

~~~text
real-core-client: 0 failed, 0 skipped
role-list-model: 0 failed, 0 skipped
no Qt warnings
~~~

Suggested commit:

~~~text
fix(desktop): map current core authority and routing truth
~~~

---

# 4. U2 — make FakeCore schema-identical where QML depends on it

Files:

~~~text
desktop/src/core/FakeCoreBackend.*
desktop/tests/UiBehaviorTest.cpp
~~~

## 4.1 Managed interfaces

Fake diagnostics must mirror real:

~~~text
managed_interfaces:
  kk-primary0:
    present: true
    managed: true
~~~

No string shorthand.

## 4.2 Platform Leshy support

FakeCore Leshy support must follow the current real product support matrix.

For current supported build platforms:

~~~text
linux = true
windows = true
macos = true
~~~

If future support changes, update fake and real contract tests together.

## 4.3 Add D9/family/selected route fixtures

At least these deterministic fake scenarios:

~~~text
local stopped
local ready
managed healthy
managed authority lost
managed transition in progress
IPv4 ready / IPv6 unavailable
IPv6 ready / IPv4 unavailable
selected-route conflict/denial counters nonzero
~~~

Suggested commit:

~~~text
test(desktop): make FakeCore match the current snapshot schema
~~~

---

# 5. U3 — render and gate managed authority safely

Files:

~~~text
desktop/qml/Main.qml
desktop/qml/Pages/DiagnosticsPage.qml
desktop/qml/Controls/KRoleDetailDrawer.qml
desktop/qml/Controls/KConnectControl.qml
desktop/qml/Controls/ConnectCircle.qml
desktop/tests/UiBehaviorTest.cpp
~~~

Preserve exactly two main pages.

## 5.1 Global authority banner

Add a stable objectName, for example:

~~~text
routeAuthorityBanner
~~~

Rules:

~~~text
managed && routeAuthorityReady:
    no warning

managed && !routeAuthorityReady:
    warning/error banner visible
    include routeAuthorityError when present
    top-level status must not say Protected

modeTransition != none:
    transition banner visible
    show safe human-readable phase
~~~

Do not expose full ActivationID or transition ID.

## 5.2 Mutating action policy

Create one QML/backend-derived boolean rather than repeating ad hoc conditions:

~~~text
safeToMutate = commandChannelReady
               && apiCompatible
               && startupReady
               && modeTransition == none
               && (!managedMode || routeAuthorityReady)
~~~

Use it for additive/mutating controls:

~~~text
connect all
connect/retry/validate/rediscover role
profile mutation
routing-list save/default-route change
IPv6 toggle
tunnel create/remove
~~~

Explicit disconnect actions remain available when the channel is ready because
they reduce active exposure.

## 5.3 Family readiness UI

Role drawer/diagnostics display:

~~~text
IPv4: not configured | ready | unavailable + reason
IPv6: not configured | ready | unavailable + reason
~~~

`routeReady` may remain as a compact legacy summary but is not the source of
family truth.

## 5.4 Selected-route diagnostics

Put detailed counters under role/full diagnostics.

Home may show only a concise warning when:

~~~text
conflicts > 0
denials > 0
routeAuthorityReady == false
~~~

## 5.5 managed_interfaces rendering

Render each object as explicit text, e.g.:

~~~text
kk-primary0: present=yes · unmanaged-from-NM=yes
~~~

Do not rely on JavaScript object string conversion.

## 5.6 U3 tests

UiBehaviorTest must add:

~~~text
authorityLossNeverClaimsProtected
modeTransitionBlocksUnsafeActions
disconnectRemainsAvailableDuringAuthorityFailure
familyReadinessIsIndependent
selectedRouteCountersAppearInDiagnostics
managedInterfacesObjectRendersMeaningfully
ipv6DisabledSnapshotUnchecksTheRoleSwitch
incompatibleApiShowsExplicitState
reconnectDisablesCommandsUntilAuthoritativeSnapshot
~~~

All use exact FakeCore shapes from U2.

Suggested commit:

~~~text
fix(desktop): surface managed authority and gate unsafe UI actions
~~~

---

# 6. U4 — one production-equivalent QML test bootstrap

Add:

~~~text
desktop/tests/support/UiTestFixture.h
desktop/tests/support/UiTestFixture.cpp
desktop/tests/support/QmlWarningGuard.h
desktop/tests/support/QmlWarningGuard.cpp
~~~

Refactor:

~~~text
UiBehaviorTest.cpp
RealCoreUiTest.cpp
RealCoreMatrixTest.cpp
~~~

## 6.1 Bootstrap must match production

Before loading any QML:

~~~text
QQuickStyle::setStyle(Basic)
create SystemTheme
create DesktopIntegration
install Core context property
install Desktop context property
install ThemeManager context property
install SystemIconProvider
load qrc:/qml/Main.qml
~~~

Do not call QQuickStyle::setStyle repeatedly after the first QML import.

## 6.2 Warning guard

Capture Qt messages during each test.

Unexpected messages matching these classes fail the case:

~~~text
ReferenceError
TypeError
Binding loop
QQmlApplicationEngine failed
QQuickStyle::setStyle() must be called before
qrc:/qml/... error
~~~

Allow only narrow, documented platform messages.

## 6.3 Repair stale live assertions

RealCoreUiTest:

~~~text
expect exactly two bottom tabs
assert status semantically against current Core state
do not require the old Ready · r string
assert no QML warnings
~~~

RealCoreMatrixTest:

~~~text
expect two tabs
fixture provides at least two profiles; profile switch cannot skip
fixture owns known routing lists; no hard-coded host assumption
no QSKIP for required cases
~~~

## 6.4 Skip policy

After U5 hermetic fixture integration:

Mandatory test cases must not contain environment-based QSKIP.

Keep separate optional/manual test binaries if a case truly needs an external
operator composition.

Suggested commit:

~~~text
test(desktop): make QML fixtures production-equivalent and warning-clean
~~~

---

# 7. U5 — hermetic real-core fixture on every CI OS

The release contract must not require a pre-existing system core.

## 7.1 Core path isolation

Current development core still reaches product default Leshy config paths.

Add one explicit serve option and Manager plumbing, recommended name:

~~~text
kikimora-core serve --leshy-config <path>
~~~

Production default remains unchanged.

Files likely involved:

~~~text
toad/cmd/kikimora-core/main.go
toad/internal/control/control.go
toad/internal/control/config_api.go
toad/internal/control/api.go
~~~

Requirements:

~~~text
serveOptions carries leshyConfigPath
Manager uses it for render/readback
GetRoutingLists reads m.leshyConfigFile(), not DefaultLeshyConfigPath()
test core uses temp config dir/state dir/publication dir/leshy config/socket
no hermetic test accesses product filesystem paths
~~~

Also expose/inject a temp writer-lock path if any managed-mode UI fixture needs
mode transitions.

## 7.2 Add a reusable live composition helper

Recommended:

~~~text
desktop/tests/support/LiveCoreFixture.h
desktop/tests/support/LiveCoreFixture.cpp
~~~

Responsibilities:

~~~text
create temp root
build/write deterministic role configs
create at least two profiles
create deterministic role/zone/list names
launch exact kikimora-core binary
launch fake Toads through core
wait for socket + authoritative snapshot
publish socket path to test client
cleanly stop core
assert no path escaped temp root
~~~

Do not shell out to installed services in this layer.

## 7.3 Required deterministic fixture

At minimum:

~~~text
profile default:
    alpha
    beta

profile work:
    beta

roles:
    alpha
    beta

routing lists:
    alpha
    beta

known bypass mode
~~~

Fixtures may use testhelper transports; no external VPN credentials.

## 7.4 Run all real-core suites against it

Mandatory zero-skip suites:

~~~text
real-core-e2e
real-core-ui
real-core-matrix
tray-live
~~~

Run on:

~~~text
ubuntu-latest
macos-15
windows-latest
~~~

Windows uses the repository AF_UNIX transport, not a mocked socket.

Suggested commits:

~~~text
test(control): make desktop live-core paths hermetic
test(desktop): run real GUI contract against a self-owned core
~~~

---

# 8. U6 — rebuild Desktop UI GitHub CI as a contract gate

File:

~~~text
.github/workflows/desktop-ui.yml
~~~

## 8.1 Triggers

Use both:

~~~yaml
pull_request:
push:
  branches: [master]
workflow_dispatch:
~~~

Path filter must include UI and core-contract changes.

Recommended conservative set:

~~~text
desktop/**
toad/**
VERSION
.github/managed-routing-companion.env
.github/workflows/desktop-ui.yml
~~~

Using broad `toad/**` is intentional: missing a contract path is more expensive
than occasionally running an extra desktop build.

## 8.2 Current Leshy pin

Do not hard-code `ftelnov/leshy v0.4.0`.

Read:

~~~text
.github/managed-routing-companion.env
~~~

and checkout the exact companion ref/SHA used by package workflows.

## 8.3 Fixtures on all OSes

Install Go on all matrix OSes and build:

~~~text
kikimora-core
toad internal control testhelper
~~~

Then run the hermetic Layer-C fixture.

## 8.4 Test stages

Each OS runs:

~~~text
configure desktop
build
pure/client tests
fake QML tests
hermetic real-core tests
tray tests
QML smoke
~~~

Linux additionally runs native package contract/release package build.

## 8.5 No-skip gate

Add a wrapper that captures QtTest output and rejects any `SKIP` line for
mandatory suites.

Do not parse only CTest exit status.

## 8.6 Translation gate

Install Qt LinguistTools in the release UI lane.

`tray-menu::russianTranslationAppliesToTheTrayMenu` becomes mandatory.

## 8.7 Warning gate

Unexpected QML warnings fail via U4; preserve full logs as artifacts.

## 8.8 Artifacts

Upload per-OS:

~~~text
CTest/QtTest logs
JUnit when available
QML warning log
coverage report
deterministic UI screenshots from U8
package artifacts on Linux
~~~

Suggested commit:

~~~text
ci(desktop): run the UI-core contract on every supported OS
~~~

---

# 9. U7 — hard installed-VM runners

Lab machines:

~~~text
ubuntu2604
    192.168.3.71

win10
    192.168.3.72
~~~

Resolve/check addresses before use; do not assume stale connectivity.

## 9.1 Linux installed runner

Add:

~~~text
desktop/tests/guest-run-suites.sh
~~~

Inputs:

~~~text
--build-dir <desktop test binaries>
--socket /run/kikimora/core.sock
--expected-kikimora-version <version>
--expected-source-sha <sha>
~~~

Required suites/categories:

~~~text
ui-behavior
system-theme
role-list-model
tray-menu
real-core-client
real-core-ui installed smoke
real-core-matrix installed-safe subset
tray-live
~~~

`real-core-e2e` remains primarily hermetic; installed VM need not spawn a second
core unless the runner explicitly creates a temp socket/state root.

Runner rules:

~~~text
readiness failure -> exit nonzero
suite failure -> exit nonzero
mandatory skip -> exit nonzero
unexpected warning -> exit nonzero
print installed package/core/UI versions
print git/source evidence provided by caller
restore role state after live tests
~~~

## 9.2 Fix Windows runner

File:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

Required changes:

~~~text
$failures = 0
$skips = 0

readiness failure:
    increment failure, not continue-success

after each suite:
    capture exit code
    parse Totals/SKIP
    any failed/mandatory skipped row increments failure

end:
    if failures > 0 exit 1
    exit 0
~~~

Also run missing pure suites or clearly delegate them to the exact CI artifact
and record that split.

## 9.3 Installed test safety

Installed UI tests may:

~~~text
read state
switch lab profiles
connect/disconnect lab roles
restart the lab core
test tray/autostart
~~~

They must not:

~~~text
change host default networking outside Kikimora's lab roles
delete unrelated configs
use personal production credentials
silently leave roles in a different desired state
~~~

Each mutable test records initial state and restores it.

Suggested commits:

~~~text
test(desktop): add the Linux installed UI gate
test(desktop): make the Windows guest runner fail closed
~~~

---

# 10. U8 — complete semantic, accessibility and visual coverage

## 10.1 Functional coverage matrix

The final automated matrix must contain at least these rows.

| Behaviour | Unit/Fake | Hermetic real core | Installed VM |
|---|---|---|---|
| QML loads with zero warnings | ui-behavior | real-core-ui | both |
| exactly two main pages | ui-behavior | real-core-ui | smoke |
| aggregate connect/disconnect | fake | real-core-ui | lab roles |
| partial/recovering/failed | fake matrix | client snapshot | optional observe |
| authority lost never says Protected | fake | real snapshot fixture | both smoke |
| mode transition banner/gating | fake | real snapshot/transition fixture | smoke |
| core reconnect/revision reset | client unit | core restart | both |
| API incompatibility | client unit | version fixture | — |
| core-reported role actions | fake | real-core-matrix | smoke |
| profile create/switch/membership | fake | real-core-matrix | safe lab |
| tunnel import/create/remove | fake parse/UI | real-core-matrix temp config | safe lab optional |
| routing list edit/readback | fake | real-core-matrix temp lists | safe lab |
| bypass/default route selection | fake | real-core-matrix | safe lab |
| IPv6 false switch state | model/fake | real snapshot | both view |
| independent v4/v6 readiness | fake | real snapshot | both view |
| selected-route counters | fake | real snapshot | managed lab |
| diagnostics capture toggle | fake | real core | installed core |
| managed_interfaces object | fake schema | real diagnostics | both |
| command error clear | client unit | rejected then successful command | — |
| autostart toggle | DesktopIntegration unit | — | both |
| close-to-tray/show/hide | UI unit | — | both |
| tray aggregate action | tray-menu | tray-live | both |
| Russian translation | tray-menu/QML | — | smoke |
| theme/reduced motion | unit/QML | — | screenshots |
| accessibility names/focus | QML test | — | smoke |
| min-size/high-DPI layout | QML geometry | — | screenshot |

Rows marked installed VM may be grouped into one deterministic runner.

## 10.2 Accessibility

Add automated checks for:

~~~text
Connect control has Accessible.Button + nonempty name
icon-only buttons have Accessible.name
dialogs/drawers have names
keyboard Tab can reach primary controls
Enter/Space activates focused primary button
Escape closes non-busy dialog/drawer
disabled unsafe controls are actually disabled to accessibility
~~~

Use Qt accessibility/object properties; do not add a second UI automation
framework unless Qt cannot express the assertion.

## 10.3 Deterministic screenshots

On one pinned Linux Qt lane, capture named snapshots for:

~~~text
home-stopped-light
home-ready-light
home-recovering-light
home-authority-lost-light
home-transition-light
settings-light
home-stopped-dark
home-authority-lost-dark
settings-dark
~~~

Sizes:

~~~text
360x720
340x620 minimum
one larger desktop size, e.g. 480x800
~~~

Use software rendering, fixed locale and fixed fake data.

Do not use cross-OS pixel-perfect golden equality as the main gate.

Linux pinned-lane golden comparison may use a small documented tolerance.

Ubuntu/Win10 installed screenshots are artifacts for review, not pixel-equal
to Linux CI.

## 10.4 Source coverage

Add Linux coverage instrumentation for C++ tests.

Recommended tool:

~~~text
gcovr
~~~

Scope:

~~~text
desktop/src/**
exclude generated Qt files
exclude tests
~~~

Final minimum gates:

~~~text
desktop/src overall line coverage >= 75%
desktop/src overall branch coverage >= 60%

RealCoreClient.cpp line coverage >= 80%
RoleListModel.cpp line coverage >= 80%
DesktopIntegration.cpp line coverage >= 80%
~~~

Do not lower thresholds to make a red build green. Add tests.

QML is gated by behavioural/visual coverage rather than a misleading line
coverage percentage.

Suggested commits:

~~~text
test(desktop): cover accessibility and safety-state presentation
ci(desktop): enforce native UI source coverage
~~~

---

# 11. U9 — exact-head release acceptance

UI/UI↔core becomes ACCEPTED only when all are true on one exact Kikimora SHA.

## 11.1 GitHub

~~~text
Desktop UI workflow triggered by the exact SHA
ubuntu job green
macOS job green
Windows job green
all mandatory suites 0 failed / 0 skipped
no unexpected QML warnings
coverage thresholds green
Linux package UI contract green
~~~

## 11.2 Ubuntu lab

Using the exact candidate package/artifact:

~~~text
guest-run-suites.sh exit 0
0 mandatory skips
core restart/reconnect UI test green
authority/diagnostic presentation green
autostart/tray smoke green
initial role state restored
screenshot artifacts saved
~~~

## 11.3 Windows lab

Using the exact candidate MSI/portable artifact:

~~~text
guest-run-suites.ps1 exit 0
0 mandatory skips
AF_UNIX UI socket connection green
core restart/reconnect green
tray/autostart smoke green
initial role state restored
screenshot artifacts saved
~~~

## 11.4 Documentation

Update:

~~~text
docs/ui-coverage-matrix.md
desktop/README.md
docs/toad-steps/09-desktop-ui.md
~~~

with exact current test counts/run IDs and no obsolete five-tab claims.

Only after U9 may the UI be cited as release-ready alongside the networking
release gates.

---

# 12. Required executor commit slices

Recommended order:

~~~text
U1  fix(desktop): map current core authority and routing truth
U2  test(desktop): make FakeCore match the current snapshot schema
U3  fix(desktop): surface managed authority and gate unsafe UI actions
U4  test(desktop): make QML fixtures production-equivalent and warning-clean
U5a test(control): make desktop live-core paths hermetic
U5b test(desktop): run real GUI contract against a self-owned core
U6  ci(desktop): run the UI-core contract on every supported OS
U7a test(desktop): add the Linux installed UI gate
U7b test(desktop): make the Windows guest runner fail closed
U8a test(desktop): cover accessibility and safety-state presentation
U8b ci(desktop): enforce native UI source coverage
U9  docs(desktop): record exact UI release evidence
~~~

Do not compress U1-U6 into one opaque commit.

---

# 13. Executor rules

1. Preserve the two-page Home + Settings product structure.
2. Core safety truth outranks cosmetic convenience.
3. Do not infer managed authority from aggregate tunnel state.
4. Do not infer one address family's readiness from the other.
5. FakeCore fixtures must match real wire/snapshot shapes.
6. Mandatory tests may not QSKIP.
7. Do not add broad warning suppression.
8. Do not restore five-tab assertions.
9. Do not run hermetic tests against /etc, /var/lib, /run or ProgramData.
10. Installed VM tests restore the state they found.
11. A core API change must run the Desktop UI workflow.
12. A green CTest percentage without QtTest zero-skip evidence is insufficient.

Stop only for:

~~~text
missing Qt/toolchain infrastructure that cannot be repaired in repo
unavailable lab VM for a mandatory installed gate
unsafe inability to isolate a test from non-lab network state
irrecoverable repository corruption
~~~

Ordinary implementation/test failures are fixed and rerun autonomously.

---

# 14. Evidence to append after every stage

| Field | Required |
|---|---|
| Stage | U1/U2/... |
| Kikimora SHA | exact 40-char |
| Files/functions | concrete |
| Local Qt tests | suite totals with skips |
| QML warnings | zero or exact explained optional warning |
| Coverage | line/branch once enabled |
| Desktop UI CI | run ID + all OS conclusions |
| ubuntu2604 | runner result when required |
| win10 | runner result when required |
| Screenshots | artifact names when required |
| State restored | YES for mutable installed tests |
| Next stage allowed | YES/NO |

Record this evidence in the current desktop UI progress/coverage documentation
without deleting historical results.
