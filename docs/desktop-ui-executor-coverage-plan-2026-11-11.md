# Desktop UI / UI↔core executor coverage and verification plan — 2026-11-11

Status: **MANDATORY UI EXECUTION PACKET — subordinate to docs/roadmap.md**

Master order/status authority:

~~~text
docs/roadmap.md
~~~

This file remains the concrete UI/UI↔core implementation packet. Execute its
U-stages only at the points assigned by the master roadmap.

Read with:

~~~text
docs/desktop-ui-core-audit-2026-11-10.md
docs/desktop-ui-test-execution-and-coverage-plan.md
docs/toad-steps/08d1-post-739e1ab-audit-remediation.md
~~~

Where this file left a choice about safety-policy ownership, core runtime-path
injection, Layer-C managed-state scope or stage order, `docs/roadmap.md`
provides the mandatory answer.

Current planning baseline:

~~~text
origin/master:
    b751134879a72f65fded8ad68fbb391e374d0a17

last core/test implementation:
    739e1ab3cb9cab359e32ba036057616ce7f10910

last desktop implementation:
    a2bc44102e93ca53e3d1733e991a2bc1b11d37c5
~~~

The desktop source has not changed since a2bc441. Core/API/test infrastructure
has changed materially afterward, so the executor must use current HEAD and
the master roadmap rather than checking out the historical UI audit baseline.

No VM result is claimed by this document. VM verification is explicitly an
executor task.

---

# 1. Clean baseline that the executor must reproduce

## 1.1 Local build

From repository root:

~~~bash
rm -rf build/desktop-ui
cmake -S desktop -B build/desktop-ui -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop-ui --parallel
~~~

Then:

~~~bash
QT_QPA_PLATFORM=offscreen \
QT_QUICK_BACKEND=software \
ctest --test-dir build/desktop-ui --output-on-failure -V
~~~

At baseline `c1e481f`, the top-level CTest result is misleading:

~~~text
100% tests passed, 0 tests failed out of 9
~~~

The underlying QtTest totals contain **9 skipped cases**:

~~~text
real-core-e2e:
    1 skipped

real-core-ui:
    1 skipped

real-core-matrix:
    5 skipped

tray-live:
    1 skipped

tray-menu:
    1 skipped
~~~

The same baseline emits **20 QWARN lines**:

~~~text
19 x:
    QQuickStyle::setStyle() must be called before loading QML that imports Qt Quick Controls 2.

1 x:
    RoleListModelTest::staysConsistentThroughStructuralChanges()
    Trying to construct an instance of an invalid type, type id: 4097
~~~

This is the baseline to improve.

A later stage is not accepted merely because CTest still says 9/9 passed.

## 1.2 Current GitHub Desktop UI evidence

Current workflow:

~~~text
.github/workflows/desktop-ui.yml
~~~

At this baseline:

~~~text
current-head Desktop UI runs:
    0
~~~

The most recent queried workflow runs are historical failures from
2026-10-20/21.

The workflow currently:

~~~text
triggers only for pull_request changes in desktop/**
does not run on push to master
does not trigger for toad/core API changes
builds live core fixtures only on Linux
does not provide KIKIMORA_UI_SOCKET to live QML suites
hard-codes ftelnov/leshy ref v0.4.0
does not fail on mandatory QSKIP
does not fail on unexpected QML warnings
~~~

The executor must not cite the old October workflow as current release evidence.

---

# 2. Non-negotiable product/test rules

1. Preserve the **two-page** product UI:
   - Главная
   - Настройки

2. Do not restore old five-tab navigation to satisfy stale tests.

3. Core-reported safety state is authoritative.

4. UI must not infer managed route authority from tunnel aggregate state.

5. IPv4 and IPv6 readiness are independent.

6. FakeCore must mirror the real snapshot schema wherever QML consumes it.

7. A mandatory test may not pass via `QSKIP`.

8. Do not delete/comment/disable a red test to make the gate green.

9. Do not add broad QML warning suppression.

10. Hermetic tests must not read/write:
    - `/etc/kikimora`
    - `/var/lib/kikimora`
    - `/run/kikimora`
    - `C:\ProgramData\Kikimora`

11. Installed-VM tests may use installed paths, but must preserve and restore
    the initial lab role/profile state.

12. Layer-C hermetic real-core tests and Layer-D installed-VM tests are both
    required; neither substitutes for the other.

13. Any change under `toad/**` that can affect Snapshot/API semantics must run
    Desktop UI CI.

14. Final acceptance requires zero mandatory skips and zero unexpected QML
    warnings.

---

# 3. Required test layers

The executor must keep four explicit layers.

## Layer A — protocol/model unit tests

Current binaries:

~~~text
real-core-client-test
role-list-model-test
system-theme-test
~~~

Purpose:

~~~text
wire framing
Handshake/API compatibility
snapshot field mapping
revision/resync/reconnect logic
model role names
QVariantMap get()
theme state
command error lifetime
~~~

No QML interaction is necessary.

## Layer B — QML semantics against FakeCore

Current binaries:

~~~text
ui-behavior-test
tray-menu-test
~~~

Purpose:

~~~text
layout semantics
Home + Settings navigation
drawers/dialogs
button enabled/disabled policy
safety banners
fake state matrix
accessibility names
tray menu semantics
~~~

FakeCore schema must be contract-identical for all fields used by QML.

## Layer C — hermetic real core + real IPC

Current binaries:

~~~text
real-core-e2e-test
real-core-ui-test
real-core-matrix-test
tray-live-test
~~~

Required final architecture:

~~~text
test owns temp root
test owns role configs
test owns profile registry
test owns routing lists
test owns generated Leshy config
test launches exact kikimora-core test binary
test launches fake Toad helper
test talks through real control transport
test loads real QML where relevant
test exits with zero mandatory skips
test removes temp root
~~~

Run Layer C on:

~~~text
Linux
macOS
Windows
~~~

## Layer D — installed lab VM

Required lab hosts:

~~~text
ubuntu2604
win10
~~~

Purpose:

~~~text
installed package/service composition
real service socket path and permissions
desktop binary in installed layout
tray
autostart
service restart/reconnect
platform-specific UI integration
package-version evidence
~~~

VM tests are run by the executor, not by this audit/planning pass.

---

# 4. Stage U1 — align the C++ backend contract to current core truth

Implementation points:

~~~text
desktop/src/core/CoreBackend.h
desktop/src/core/RealCoreClient.h
desktop/src/core/RealCoreClient.cpp
desktop/src/models/RoleListModel.h
desktop/src/models/RoleListModel.cpp
desktop/src/core/FakeCoreBackend.h
desktop/src/core/FakeCoreBackend.cpp
~~~

## 4.1 Add top-level authority properties

CoreBackend must expose:

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

safeToMutate: bool
safeToDisconnect: bool
diagnosticsAvailable: bool
diagnosticsMutable: bool
~~~

The last four are C++ backend policy properties, not QML-derived convenience
bindings. Their exact truth tables and command classification are fixed in
`docs/roadmap.md` §8.

RealCoreClient maps from Go Snapshot:

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

Handshake maps:

~~~text
min_version
max_version
capabilities
~~~

## 4.2 API version

Current core API:

~~~text
APIVersion = 3
~~~

The UI currently sends API v2.

Change client requests to v3.

Handshake acceptance:

~~~text
min_version <= 3 <= max_version
~~~

If false:

~~~text
coreState = Incompatible
apiCompatible = false
commandChannelReady = false
explicit error text
no endless reconnect loop
no ordinary mutation commands
~~~

## 4.3 Authoritative channel-ready state

`commandChannelReady` becomes true only after:

~~~text
socket connected
Handshake compatible
authoritative Snapshot received
startup_ready true
mode_transition == none
~~~

On disconnect/reconnect:

~~~text
commandChannelReady -> false immediately
startupReady -> false until new authoritative snapshot
do not preserve stale routeAuthorityReady as actionable state
~~~

## 4.4 Family readiness

Extend `RoleSnapshot` and model roles:

~~~text
ipv4Configured
ipv4Ready
ipv4Reason
ipv6Configured
ipv6Ready
ipv6Reason
~~~

Map from:

~~~text
roles[].families.ipv4
roles[].families.ipv6
~~~

Do not derive one family from `route_ready`.

## 4.5 Selected-route statistics

Expose `roles[].selected_routes`.

Keeping a `QVariantMap selectedRoutes` is acceptable.

Preserve these keys:

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

Include the map in:

~~~text
RoleListModel::data
RoleListModel::roleNames
RoleListModel::get
snapshotSignature
~~~

## 4.6 Fix existing `leshyIpv6` loss

`RoleListModel::get()` currently omits `leshyIpv6`.

Add it.

This is a required regression because the role drawer uses `Core.roles.get()`.

## 4.7 Command error lifecycle

Define command-scoped error semantics:

~~~text
new user command:
    clear previous command-scoped error

same command fails:
    set current error

same command succeeds:
    leave command error clear

transport disconnect:
    set transport error / unavailable state

successful reconnect + authoritative snapshot:
    clear transport error
~~~

A streamed unrelated snapshot must not clear an outstanding transport error.

## 4.8 Exact U1 unit tests

Add to `desktop/tests/RealCoreClientTest.cpp`:

~~~text
mapsManagedAuthorityFields()
mapsIndependentFamilyReadiness()
mapsSelectedRouteStats()
acceptsApiV3HandshakeAndCapabilities()
rejectsHandshakeWhenApi3IsOutsideServerRange()
commandChannelWaitsForAuthoritativeSnapshot()
disconnectClearsCommandChannelReadiness()
reconnectRequiresNewHandshakeAndSnapshot()
commandErrorClearsOnNextSuccessfulCommand()
authorityFieldsDoNotSurviveDisconnectAsActionableTruth()
~~~

Add to `desktop/tests/RoleListModelTest.cpp`:

~~~text
getCarriesLeshyIpv6()
getCarriesFamilyReadiness()
getCarriesSelectedRouteStats()
roleNamesExposeNewRoutingFields()
~~~

Required assertions include:

~~~text
leshy_ipv6=false -> get()["leshyIpv6"] == false

ipv4 configured=true ready=true
ipv6 configured=true ready=false reason="..."
-> exact values survive RealCoreClient -> RoleSnapshot -> RoleListModel::get

selected_routes.registered=3
selected_routes.conflicts=1
selected_routes.denials=4
-> exact map values survive
~~~

U1 gate:

~~~text
real-core-client-test:
    0 failed
    0 skipped

role-list-model-test:
    0 failed
    0 skipped
    no invalid-type warning
~~~

Suggested commit:

~~~text
fix(desktop): map current core routing authority and family truth
~~~

---

# 5. Stage U2 — make FakeCore schema-identical

Files:

~~~text
desktop/src/core/FakeCoreBackend.h
desktop/src/core/FakeCoreBackend.cpp
desktop/tests/UiBehaviorTest.cpp
~~~

## 5.1 Top-level state fixtures

FakeCore must model at least:

~~~text
local stopped
local ready
managed healthy
managed authority lost
managed mode transition prepared
managed mode transition config_written
managed mode transition marker_written
API incompatible
reconnecting / command channel not ready
~~~

## 5.2 Family fixtures

At least:

~~~text
IPv4 ready + IPv6 ready
IPv4 ready + IPv6 unavailable
IPv4 unavailable + IPv6 ready
IPv6 disabled by role policy
neither family configured
~~~

## 5.3 Selected-route fixtures

At least:

~~~text
healthy:
    registered > 0
    active == registered
    conflicts = 0
    denials = 0

drift:
    drift_repairs > 0

denied/conflicted:
    conflicts > 0
    denials > 0

parking:
    park_active > 0
    parked > 0
~~~

## 5.4 managed_interfaces exact shape

Real core:

~~~text
managed_interfaces:
  <iface>:
    present: bool
    managed: bool
~~~

FakeCore must use the same nested map.

Do not use the old string shorthand such as:

~~~text
kk-primary0: "kernel"
~~~

## 5.5 Windows Leshy support

FakeCore must not report Windows as unsupported when real core supports it.

The fake support matrix must match the real support function.

U2 gate:

~~~text
ui-behavior-test:
    all existing fake tests green
    new state fixtures reachable
~~~

Suggested commit:

~~~text
test(desktop): align FakeCore with the real snapshot schema
~~~

---

# 6. Stage U3 — fail-closed safety presentation

Files:

~~~text
desktop/qml/Main.qml
desktop/qml/Pages/DiagnosticsPage.qml
desktop/qml/Drawers/ProfileDrawer.qml
desktop/qml/Controls/KConnectControl.qml
desktop/qml/Controls/ConnectCircle.qml
desktop/qml/Controls/KRoleDetailDrawer.qml
desktop/src/DesktopIntegration.h
desktop/src/DesktopIntegration.cpp
desktop/tests/UiBehaviorTest.cpp
desktop/tests/TrayMenuTest.cpp
desktop/tests/TrayLiveTest.cpp
~~~

This is the complete mutation/tray safety surface. Do not gate only Main.qml
while leaving profile import, role editing or tray Connect independently
clickable.

Keep the two-page navigation.

## 6.1 Global authority banner

Add a stable test hook:

~~~text
objectName: routeAuthorityBanner
~~~

Display when:

~~~text
!apiCompatible
or modeTransition != "none"
or (managedMode && !routeAuthorityReady)
~~~

The banner must display:

~~~text
API incompatibility reason
or safe human-readable mode transition phase
or routeAuthorityError
~~~

Do not display full activation/transition IDs.

Fingerprints are acceptable only in technical diagnostics.

## 6.2 "Protected" rule

This must be impossible:

~~~text
managedMode == true
routeAuthorityReady == false
UI text == "Protected"
~~~

Top status and connect control must use the combined safety truth.

Required fake test:

~~~text
authorityLossNeverClaimsProtected()
~~~

## 6.3 Mutation gating

Define one backend-owned policy; do not scatter unrelated conditions.

Mandatory semantics are fixed by `docs/roadmap.md` §8:

~~~text
commandChannelReady =
    apiCompatible
    && authoritative snapshot received on this connection
    && startupReady
    && modeTransition == none

safeToMutate =
    commandChannelReady
    && (!managedMode || routeAuthorityReady)

safeToDisconnect =
    commandChannelReady

diagnosticsAvailable =
    apiCompatible

diagnosticsMutable =
    apiCompatible
    && startupReady
    && modeTransition == none
~~~

RealCoreClient must enforce the same policy before sendCommand; disabled QML
controls are not the only guard.

Block additive/mutating actions when `safeToMutate == false`:

~~~text
CONNECT all
connect role
retry role
validate role
rediscover endpoints
profile create/switch/membership edit
tunnel create/import/remove
routing list save
bypass/default route change
IPv6 policy change
~~~

Explicit disconnect operations may remain enabled when `safeToDisconnect`.

## 6.4 Role family display

Role technical section must show independently:

~~~text
IPv4:
    not configured
    ready
    unavailable · <reason>

IPv6:
    not configured
    ready
    unavailable · <reason>
~~~

## 6.5 Selected-route diagnostics

Role/full diagnostics must display at least:

~~~text
registered
active
parked
retiring
drift_repairs
denials
conflicts
kernel_mutations
~~~

Home should not become a counter dashboard.

Only concise warning state belongs on Home.

## 6.6 managed_interfaces formatting

Do not stringify nested JS objects.

Render explicit fields:

~~~text
<iface>: present=yes/no · NetworkManager=managed/unmanaged
~~~

## 6.7 Exact U3 fake-QML tests

Add:

~~~text
authorityLossNeverClaimsProtected()
authorityLossShowsCoreReason()
modeTransitionShowsBanner()
modeTransitionBlocksUnsafeActions()
disconnectRemainsAvailableDuringAuthorityFailure()
commandsDisabledWhileClientReconnects()
apiIncompatibleBlocksMutationAndExplainsWhy()
ipv4AndIpv6ReadinessRenderIndependently()
ipv6PolicyFalseLeavesRoleSwitchUnchecked()
selectedRouteConflictAppearsInDiagnostics()
selectedRouteDenialAppearsInDiagnostics()
managedInterfacesNestedObjectRendersFields()
~~~

For each gating test assert both:

~~~text
button.enabled
and no FakeCore mutation/revision caused by attempted click
~~~

Suggested commit:

~~~text
fix(desktop): surface route authority and gate unsafe UI mutations
~~~

---

# 7. Stage U4 — production-equivalent QML bootstrap and warning gate

Current problem:

`ui-behavior-test` produces 19 style-order QWARN lines.

The live UI fixture also previously emitted:

~~~text
ThemeManager is not defined
~~~

The executor must create one shared fixture.

Recommended files:

~~~text
desktop/tests/support/UiTestFixture.h
desktop/tests/support/UiTestFixture.cpp
desktop/tests/support/QmlWarningGuard.h
desktop/tests/support/QmlWarningGuard.cpp
~~~

## 7.1 Bootstrap order

Before first QML import in a test process:

~~~text
QQuickStyle::setStyle("Basic")
~~~

Then install the same production context as `desktop/src/main.cpp`:

~~~text
Core
Desktop
ThemeManager
SystemIconProvider
~~~

Only then:

~~~text
engine.load(qrc:/qml/Main.qml)
~~~

Apply to:

~~~text
UiBehaviorTest
RealCoreUiTest
RealCoreMatrixTest
future screenshot tests
~~~

## 7.2 Warning guard

Capture Qt messages.

Fail a test on unexpected:

~~~text
ReferenceError
TypeError
Binding loop
QQmlApplicationEngine failed
QQuickStyle ordering error
qrc:/qml/... runtime error
required property not initialized
Cannot assign
Unable to assign
~~~

Do not fail on a platform message unless it represents a real test/product
problem; any whitelist entry must include an explanation in code.

## 7.3 Remove baseline warnings

U4 is not accepted until:

~~~text
UiBehaviorTest:
    0 QWARN

RoleListModelTest:
    no invalid-type warning
~~~

Fix the cause of the RoleListModel warning rather than filtering it.

Suggested commit:

~~~text
test(desktop): share the production QML bootstrap and fail on runtime warnings
~~~

---

# 8. Stage U5 — hermetic real-core fixture

This is the most important test-infrastructure stage.

Current live suites depend on external environment variables and silently skip.

Final Layer-C tests must own their core.

## 8.1 Required core filesystem injection

The temporary core must not generate/read product Leshy state.

The implementation is no longer optional/recommended. Follow
`docs/roadmap.md` §12 exactly:

~~~text
toad/internal/control/runtime_paths.go

type RuntimePaths struct {
    LeshyConfig      string
    RoutingDir       string
    BypassConfig     string
    LeshyPublication string
}

func (m *Manager) SetRuntimePaths(paths RuntimePaths) error
~~~

SetRuntimePaths is called from runServe before StartupManagedAuthority and is
closed once startup authority begins.

Extend serve with exactly:

~~~text
--leshy-config
--routing-dir
--bypass-config
--diagnostics-host-sources=true|false
~~~

in addition to the already-existing config/state/publication/socket/provider
flags.

The existing `--leshy-publication-dir` must feed both
`RuntimePaths.LeshyPublication` and the existing `leshy.FileBridge`.
`buildLeshyZones()` must use the Manager publication path instead of directly
calling `config.DefaultLeshyVPNDir()`.

Production defaults remain unchanged when options are omitted. Packaged
service construction must resolve an unset diagnostics-host-sources option to
true.

Core implementation points:

~~~text
toad/cmd/kikimora-core/main.go
toad/internal/control/runtime_paths.go
toad/internal/control/control.go
toad/internal/control/config_api.go
toad/internal/control/api.go
~~~

Important consistency fix:

~~~text
GetRoutingLists
~~~

must read `m.leshyConfigFile()`, not independently call
`config.DefaultLeshyConfigPath()`.

Do not invent a second runtime-path source.

For hermetic diagnostics, add:

~~~go
func (m *Manager) SetDiagnosticsHostSources(enabled bool) error
~~~

and make `diagnostics.Collector.hostSources=false` suppress journal/event-log,
network commands **and platform logGlobs() reads**. Layer C passes
`--diagnostics-host-sources=false`.

## 8.2 LiveCoreFixture

Recommended:

~~~text
desktop/tests/support/LiveCoreFixture.h
desktop/tests/support/LiveCoreFixture.cpp
~~~

Responsibilities:

~~~text
create QTemporaryDir
write deterministic Toad configs
write deterministic profiles
write deterministic routing-list files
create empty temp endpoint-provider directory
start exact kikimora-core binary
point it at fake-toad helper
pass every mutable runtime path under the temp root
set KIKIMORA_RESTART_MARKER under the temp root
disable diagnostics ambient host sources
wait for socket
wait for compatible Handshake
wait for authoritative Snapshot
provide socket path to RealCoreClient
stop core cleanly
check process exit
delete temp root
~~~

Exact core arguments/environment are specified in `docs/roadmap.md` §12.6.

It must assert that:

~~~text
no core-created file escaped the temp root
generated Leshy route_target paths use the temp publication dir
endpoint providers never fall back to /usr/local/libexec/kikimora
diagnostics capture reads no ambient product logs/journal/network state
restart marker stays under the temp root
~~~

## 8.3 Deterministic fixture data

At least two roles and two non-default profiles so no required case can skip:

~~~text
roles:
    alpha
    beta

profiles:
    default = alpha,beta
    work = beta
    home = alpha

routing lists:
    alpha
    beta

bypass:
    direct
~~~

Use stable fake Toad transport/config data.

No real VPN credentials.

## 8.4 Convert current live tests

### real-core-e2e

Current env skip:

~~~text
KIKIMORA_CORE_BINARY and KIKIMORA_FAKE_TOAD_BINARY are required
~~~

CI may still provide the binary paths, but absence in a mandatory CI lane is a
harness failure, not QSKIP.

Cases must cover:

~~~text
Handshake v3
Snapshot
Subscribe
revision advance
profile switch
role action
core process restart/reconnect
~~~

### real-core-ui

Remove dependence on a pre-existing system socket.

Update stale expectations:

~~~text
bottom tabs = 2
status assertion follows current aggregate/safety presentation
ThemeManager exists
no QML warning
~~~

Add real-core QML cases:

~~~text
realSnapshotReachesQml()
realCoreRestartDisablesThenRestoresCommands()
realAuthorityFieldsReachQml()
realFamilyReadinessReachesRoleDrawer()
realSelectedRouteStatsReachDiagnostics()
~~~

### real-core-matrix

Mandatory cases must no longer QSKIP.

Fix stale assumptions:

~~~text
settings tab index = 1
two bottom tabs
fixture always provides >=2 profiles
routing-list test uses fixture-owned alpha/beta, not host "primary"
role-row click has deterministic geometry under the shared fixture
~~~

Required cases:

~~~text
roleActionDrivesTheCore()
settingsShowLiveState()
profileSwitchDrivesTheCore()
createTunnelWritesConfiguration()
routingListsRoundTrip()
setRoleIpv6RoundTrip()
setBypassModeRoundTrip()
diagnosticsCaptureRoundTrip()
~~~

All file mutations happen under temp root.

### tray-live

Run against the same owned core fixture.

No socket env QSKIP.

### Layer-C managed-state boundary

Do not forge privileged managed-mode state inside the hermetic Layer-C core.

Layer C proves real core process + real IPC + real temp filesystem/config
round-trips. Managed-authority loss/recovery variants are proven by:

~~~text
Layer A:
    scripted RealCoreClient protocol replies

Layer B:
    FakeCore QML safety scenarios

Layer D:
    installed Ubuntu/Windows managed runtime
~~~

The real-core QML case `realAuthorityFieldsReachQml()` therefore verifies the
actual local-mode values published by a real core. The managed=true /
route_authority_ready=false presentation is mandatory in Layer A+B and the
installed VM lanes, not created by hand-editing marker/checkpoint files in
Layer C.

This ownership is fixed by `docs/roadmap.md` §12.7.

## 8.5 Zero-skip invariant

After U5, these four Layer-C binaries must finish:

~~~text
0 failed
0 skipped
~~~

on every supported CI OS.

Suggested commits:

~~~text
test(control): make desktop live-core paths filesystem-hermetic
test(desktop): run live UI suites against a self-owned core
~~~

---

# 9. Stage U6 — explicit no-skip/no-warning test runner

CTest alone cannot enforce the required policy.

Add wrappers:

~~~text
desktop/tests/run-required-suites.sh
desktop/tests/run-required-suites.ps1
~~~

They must:

1. run each required binary separately;
2. capture stdout/stderr;
3. preserve per-suite logs;
4. fail if process exit != 0;
5. fail if output contains mandatory `SKIP`;
6. fail if output contains unexpected warning patterns;
7. print one final summary.

Required Linux/macOS shell structure:

~~~bash
run_required() {
    name="$1"
    shift

    log="$ARTIFACT_DIR/$name.log"
    "$@" >"$log" 2>&1
    rc=$?

    cat "$log"

    if [ "$rc" -ne 0 ]; then
        return "$rc"
    fi

    if grep -E '^.*SKIP[[:space:]]+:' "$log"; then
        echo "$name: mandatory QtTest skip"
        return 1
    fi

    if grep -E 'ReferenceError|TypeError|Binding loop|QQmlApplicationEngine failed' "$log"; then
        echo "$name: unexpected QML warning"
        return 1
    fi
}
~~~

Do not use exactly this regex blindly if Qt output format differs on Windows;
implement equivalent semantics.

Required suite groups:

~~~text
Layer A:
    real-core-client-test
    role-list-model-test
    system-theme-test

Layer B:
    ui-behavior-test
    tray-menu-test

Layer C:
    real-core-e2e-test
    real-core-ui-test
    real-core-matrix-test
    tray-live-test
~~~

Translation test is mandatory in release CI; install LinguistTools rather than
accepting the current skip.

Suggested commit:

~~~text
test(desktop): fail required UI suites on skips and runtime warnings
~~~

---

# 10. Stage U7 — GitHub Desktop UI workflow

File:

~~~text
.github/workflows/desktop-ui.yml
~~~

## 10.1 Triggers

Required:

~~~yaml
on:
  pull_request:
    paths:
      - "desktop/**"
      - "toad/**"
      - "VERSION"
      - ".github/managed-routing-companion.env"
      - ".github/workflows/desktop-ui.yml"
  push:
    branches:
      - master
    paths:
      - "desktop/**"
      - "toad/**"
      - "VERSION"
      - ".github/managed-routing-companion.env"
      - ".github/workflows/desktop-ui.yml"
  workflow_dispatch:
~~~

Using broad `toad/**` is intentional.

## 10.2 Companion Leshy

Remove:

~~~text
repository: ftelnov/leshy
ref: v0.4.0
~~~

Read:

~~~text
.github/managed-routing-companion.env
~~~

and use the exact current companion ref/SHA, same as package workflows.

The workflow must fail if tag/ref HEAD does not equal the pinned SHA.

## 10.3 Go fixture binaries on all OSes

Install Go/build test core on:

~~~text
ubuntu-latest
macos-15
windows-latest
~~~

Build exact source:

~~~text
go build ./cmd/kikimora-core
go build ./internal/control/testhelper
~~~

Use platform-appropriate executable suffix.

## 10.4 Qt

Install:

~~~text
Qt 6.x pinned consistently
Quick/QML/Controls
LinguistTools
required image/shader modules
~~~

Do not let translation remain optional in release lane.

## 10.5 Test commands

Use the required-suite wrappers from U6.

Do not use one opaque:

~~~text
ctest ... -> 9/9 passed
~~~

as final proof.

CTest may still be run for discovery/JUnit, but the required-suite wrapper is
the release gate.

## 10.6 QML smoke

Keep offscreen application smoke, but add explicit failure patterns:

~~~text
QQmlApplicationEngine failed
ReferenceError
TypeError
Binding loop
required property
qrc:/qml/... error
~~~

## 10.7 Artifacts

Upload per OS:

~~~text
all QtTest logs
CTest/JUnit if generated
QML warning log
screenshot artifacts from U10
coverage report on Linux
~~~

Do not upload release-named Kikimora packages from this workflow. The sole
release artifact owner is `.github/workflows/build-packages.yml` as fixed by
`docs/roadmap.md` P1 and §19.

U7 gate:

~~~text
one exact SHA
Linux green
macOS green
Windows green
zero mandatory skips on all three
zero unexpected QML warnings on all three
~~~

Suggested commit:

~~~text
ci(desktop): gate UI-core contract on every supported OS
~~~

---

# 11. Stage U8 — Linux installed-VM runner

Master-roadmap split:

~~~text
U8i before candidate:
    implement and test the runner itself

U8e after Phase J:
    install the exact immutable candidate .deb and execute this runner
~~~

Do not report installed-UI acceptance from a source-tree/local rebuild before
Phase J.

Add:

~~~text
desktop/tests/guest-run-suites.sh
~~~

The runner operates on installed artifacts, not the hermetic temp core.

Inputs:

~~~text
--build-dir <test binaries>
--socket /run/kikimora/core.sock
--expected-version <candidate version>
--expected-sha <source sha>
~~~

Before tests, record:

~~~text
uname -a
installed Kikimora package version
kikimora-core version
desktop binary/version
service status
socket owner/mode
active profile
role desired states
aggregate state
~~~

Preserve this as INITIAL_STATE.

## 11.1 Required installed Linux checks

### A. Read-only startup

~~~text
desktop connects to installed core
Handshake compatible
authoritative snapshot arrives
Home + Settings load
no QML warning
~~~

### B. Core restart/reconnect

~~~text
restart kikimora-core
UI goes non-actionable during disconnect
UI does not keep stale Protected claim
UI reconnects
new snapshot revision accepted
commands become available only after authoritative snapshot
~~~

### C. Role safe lifecycle

Use only designated lab role(s).

~~~text
connect designated role
observe state reflected in UI/client
disconnect designated role
restore initial desired state
~~~

### D. Diagnostics

Verify installed UI sees:

~~~text
managed_mode
mode_generation
mode_transition
route_authority_ready
family readiness
selected-route stats when managed
managed_interfaces structured values
~~~

### E. Tray

~~~text
tray action exists
aggregate action follows state
show window
hide window
close-to-tray
~~~

### F. Autostart

~~~text
toggle on
verify installed autostart entry
toggle back to initial value
~~~

## 11.2 Linux runner fail-closed behavior

Any of these is fatal:

~~~text
readiness timeout
suite nonzero exit
mandatory QSKIP
unexpected QML warning
cannot restore initial role/profile/autostart state
installed version mismatch
socket permission mismatch
~~~

Runner exits nonzero.

Do not treat VM inability to reach Ready as "skip".

## 11.3 Linux evidence

Append exact:

~~~text
Kikimora SHA
package SHA256
package version
runner command
runner exit code
per-suite totals
mandatory skips = 0
warnings = 0
initial state restored = YES
~~~

---

# 12. Stage U9 — Windows installed-VM runner

Master-roadmap split:

~~~text
U9i before candidate:
    harden the PowerShell runner and make failure/skip propagation exact

U9e after Phase J:
    install the exact immutable candidate MSI and execute the runner
~~~

Do not substitute a hand-deployed UI or portable source-tree binary for the
U9e MSI proof.


Executor runs this on `win10`.

Current runner:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

It must be repaired before being used as a release gate.

## 12.1 Fail aggregation

Required pattern:

~~~powershell
$failures = 0
$mandatorySkips = 0

# every suite:
# - capture process exit
# - capture log
# - parse SKIP/Totals
# - increment failures/skips

if ($failures -gt 0 -or $mandatorySkips -gt 0) {
    exit 1
}
exit 0
~~~

Current `continue` on readiness failure must become a hard failure.

## 12.2 Required installed Windows checks

Same semantic set as Linux:

~~~text
installed UI -> installed core over AF_UNIX
compatible Handshake
authoritative snapshot
Home + Settings
core service restart/reconnect
safe lab role connect/disconnect
managed authority diagnostics
family diagnostics
selected-route diagnostics
tray
show/hide
autostart
restore initial state
~~~

Also verify:

~~~text
C:\ProgramData\Kikimora\core.sock
actual socket ACL/access from desktop user
SCM service state
installed MSI/portable version evidence
~~~

## 12.3 Windows evidence

Record:

~~~text
Kikimora SHA
MSI/zip SHA256
installed version
PowerShell runner exit
per-suite totals
mandatory skips = 0
warnings = 0
state restored = YES
~~~

---

# 13. Stage U10 — accessibility and deterministic visual coverage

This is after semantic correctness, not before.

## 13.1 Accessibility tests

Add Qt/QML assertions for:

~~~text
main connect control:
    Accessible.Button
    nonempty Accessible.name

icon-only buttons:
    nonempty Accessible.name

profile selector:
    accessible label

role row:
    accessible role/name/state

role drawer:
    accessible title

safety banner:
    Accessible.AlertMessage
    meaningful text

unsafe disabled action:
    accessibility also reports disabled

keyboard:
    Tab reaches primary controls
    Enter/Space activates focused button
    Escape closes non-busy popup/drawer
~~~

## 13.2 Geometry/high-DPI

Automated test sizes:

~~~text
340x620
360x720
480x800
~~~

At each size:

~~~text
two bottom tabs visible
connect control not clipped
profile selector reachable
role list scrollable
Settings scrollable
safety banner not overlapping navigation
drawer buttons reachable
~~~

Run one DPR > 1 scenario if Qt offscreen supports deterministic DPR.

## 13.3 Screenshots

Pinned Linux CI lane captures deterministic FakeCore screenshots:

~~~text
home-stopped-light
home-ready-light
home-authority-lost-light
home-mode-transition-light
home-ipv6-unavailable-light
settings-diagnostics-light

home-stopped-dark
home-authority-lost-dark
settings-diagnostics-dark
~~~

Use:

~~~text
fixed locale
fixed font setup
software rendering
fixed window size
deterministic FakeCore fixture
~~~

Pixel-golden comparison may be used only on the pinned Linux lane with a small
documented tolerance.

Windows/macOS/VM screenshots are evidence artifacts, not cross-platform
pixel-equality gates.

Suggested commit:

~~~text
test(desktop): cover accessibility and safety-state presentation
~~~

---

# 14. Stage U11 — C++ source coverage

Do this after the semantic test suite is stable.

Recommended Linux tooling:

~~~text
gcovr
~~~

Configure a dedicated coverage build.

Scope:

~~~text
desktop/src/**
~~~

Exclude:

~~~text
generated Qt code
tests/**
build/**
~~~

Initial required thresholds:

~~~text
desktop/src overall line >= 75%
desktop/src overall branch >= 60%

RealCoreClient.cpp line >= 80%
RoleListModel.cpp line >= 80%
DesktopIntegration.cpp line >= 80%
~~~

Do not lower thresholds to absorb a red build.

Add tests.

QML itself is judged by semantic/accessibility/visual cases, not a fake line
coverage percentage.

Suggested commit:

~~~text
ci(desktop): enforce native UI source coverage
~~~

---

# 15. Complete coverage matrix the executor must satisfy

| Contract | Layer A | Layer B | Layer C | Ubuntu VM | Win10 VM |
|---|---|---|---|---|---|
| API v3 compatible Handshake | yes | — | yes | yes | yes |
| API incompatibility | yes | yes | yes | — | — |
| Snapshot revision monotonicity | yes | — | yes | yes | yes |
| reconnect after core restart | yes | state UI | yes | yes | yes |
| command disabled before authoritative snapshot | yes | yes | yes | yes | yes |
| exactly Home + Settings | — | yes | yes | smoke | smoke |
| aggregate connect/disconnect | client mapping | yes | yes | lab | lab |
| PartiallyReady/Recovering | yes | yes | yes | observe | observe |
| authority lost never Protected | yes | yes | yes | yes | yes |
| mode transition gating | yes | yes | yes | yes | yes |
| routeAuthorityError text | yes | yes | yes | yes | yes |
| activation fingerprint redaction | yes | diagnostics only | yes | view | view |
| IPv4/IPv6 independent readiness | yes | yes | yes | view | view |
| leshy_ipv6=false switch | model | yes | yes | view | view |
| selected route counters | yes | yes | yes | managed | managed |
| selected route conflict warning | yes | yes | yes | managed | managed |
| managed_interfaces nested object | — | yes | real diagnostics | yes | yes |
| role available_actions | yes | yes | yes | yes | yes |
| role connect/disconnect | — | yes | yes | lab | lab |
| profile switch | — | yes | yes | lab-safe | lab-safe |
| profile creation/membership | — | yes | yes temp | optional | optional |
| tunnel create/import/remove | parser | yes | yes temp | optional | optional |
| routing list edit/readback | — | yes | yes temp | lab-safe | lab-safe |
| bypass/default route | — | yes | yes temp | lab-safe | lab-safe |
| diagnostics capture | client | yes | yes | yes | yes |
| command error clear | yes | yes | yes | — | — |
| tray aggregate state/action | — | yes | yes | yes | yes |
| show/hide/close-to-tray | DesktopIntegration | yes | — | yes | yes |
| autostart | DesktopIntegration | QML wiring | — | yes | yes |
| Russian translation | — | yes | — | smoke | smoke |
| light/dark | theme | yes | — | screenshot | screenshot |
| reduced motion | — | yes | — | smoke | smoke |
| keyboard accessibility | — | yes | — | smoke | smoke |
| min geometry | — | yes | — | screenshot | screenshot |
| QML runtime warnings | — | zero | zero | zero | zero |
| mandatory skips | zero | zero | zero | zero | zero |

A blank/"optional" VM cell does not make the corresponding Layer-C row optional.

---

# 16. Current stale tests the executor must deliberately repair

## 16.1 RealCoreUiTest

Current stale assertions:

~~~text
expects old status prefix "Ready · r..."
expects 5 bottom tabs
does not install ThemeManager production context
skips without external KIKIMORA_UI_SOCKET
~~~

Required new contract:

~~~text
status follows current aggregate + authority truth
exactly 2 bottom tabs
shared production-equivalent QML bootstrap
self-owned core fixture
0 required skips
~~~

## 16.2 RealCoreMatrixTest

Current stale/fragile behavior:

~~~text
expects 5 tabs
Settings at old index
profile switch can skip with one profile
routing list hard-codes "primary"
role-row click can skip on offscreen geometry
external live socket required
~~~

Required:

~~~text
2 tabs
Settings index 1
fixture always has >=2 profiles
fixture owns exact alpha/beta lists
deterministic role-row geometry
no mandatory QSKIP
self-owned core
~~~

## 16.3 UiBehaviorTest

Current:

~~~text
functionally green
19 style-order QWARN
~~~

Required:

~~~text
style set once before first Controls import
shared bootstrap
0 QWARN
new managed-authority state tests
~~~

## 16.4 RoleListModelTest

Current:

~~~text
one invalid-type QWARN
does not prove leshyIpv6 in get()
does not prove family fields
does not prove selected route map
~~~

Required:

~~~text
0 warnings
new routing/family model assertions
~~~

## 16.5 TrayLiveTest

Current:

~~~text
entire suite skips without external socket
~~~

Required:

~~~text
owned core fixture
0 skips
~~~

## 16.6 TrayMenuTest

Current:

~~~text
translation case skips when LinguistTools unavailable
~~~

Required release lane:

~~~text
LinguistTools installed
translation test executes
0 skips
~~~

---

# 17. Required executor commit sequence

Do not collapse all work into one commit.

Recommended:

~~~text
U1
fix(desktop): map current core routing authority and family truth

U2
test(desktop): align FakeCore with the real snapshot schema

U3
fix(desktop): surface route authority and gate unsafe UI mutations

U4
test(desktop): share the production QML bootstrap and fail on warnings

U5a
test(control): make desktop live-core filesystem paths injectable

U5b
test(desktop): run live UI suites against a self-owned core

U6
test(desktop): fail required suites on skips and runtime warnings

U7
ci(desktop): gate the UI-core contract on every supported OS

U8
test(desktop): add the Linux installed UI runner

U9
test(desktop): make the Windows installed UI runner fail closed

U10
test(desktop): cover accessibility and deterministic safety-state visuals

U11
ci(desktop): enforce native UI source coverage

U12
docs(desktop): record exact UI release evidence
~~~

Executor may split a stage further for clarity.

Do not merge U1-U7 into one opaque patch.

---

# 18. Gate after every stage

After each code stage, run at least:

~~~bash
cmake -S desktop -B build/desktop-ui -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop-ui --parallel

QT_QPA_PLATFORM=offscreen \
QT_QUICK_BACKEND=software \
ctest --test-dir build/desktop-ui --output-on-failure -V
~~~

After U6, use the required-suite wrapper as the real gate.

Also run relevant Go tests whenever core fixture/API paths change:

~~~bash
cd toad
go test ./internal/control ./internal/config
go test ./cmd/kikimora-core
~~~

If the changed package has broader dependencies:

~~~bash
go test ./...
~~~

Before commit:

~~~bash
git diff --check
~~~

A stage is not green if:

~~~text
required test skipped
unexpected QWARN remains
QML runtime error appears
test only passes against developer-installed config
test changes /etc, /var/lib, /run, ProgramData in Layer C
~~~

---

# 19. Exact GitHub acceptance record

After U7 and every material UI/core change, record:

~~~text
Kikimora SHA:
Desktop UI workflow run ID:
Linux job:
macOS job:
Windows job:

Layer A:
    pass/fail/skip
Layer B:
    pass/fail/skip
Layer C:
    pass/fail/skip

mandatory skips:
unexpected warnings:
coverage:
artifact links/names:
~~~

Acceptance requires:

~~~text
all OS jobs success
mandatory skips = 0
unexpected warnings = 0
coverage thresholds green
~~~

---

# 20. Exact VM acceptance record

Only the executor with VM access performs these.

## Ubuntu

Record:

~~~text
VM:
    ubuntu2604

Kikimora SHA:
package version:
package SHA256:
test runner SHA:
initial role/profile state:
runner exit:
mandatory skips:
unexpected warnings:
state restored:
screenshots:
~~~

## Windows

Record:

~~~text
VM:
    win10

Kikimora SHA:
MSI/zip version:
artifact SHA256:
test runner SHA:
initial role/profile state:
runner exit:
mandatory skips:
unexpected warnings:
state restored:
screenshots:
~~~

If a required VM gate cannot run, UI acceptance is **BLOCKED**, not skipped.

---

# 21. Final UI acceptance criteria

UI/UI↔core is accepted only when all of these are true on one exact source SHA:

1. Two-page Home + Settings UX retained.
2. RealCoreClient consumes all safety-relevant current Snapshot fields.
3. API v3 Handshake compatibility enforced.
4. Managed authority loss cannot display Protected.
5. Mode transition blocks unsafe mutation.
6. Disconnect remains available where safe.
7. IPv4/IPv6 readiness shown independently.
8. Selected-route health visible in diagnostics.
9. `leshyIpv6=false` reaches the role drawer correctly.
10. FakeCore schema matches real shapes used by QML.
11. `managed_interfaces` renders real nested values correctly.
12. Layer A passes with zero skips/warnings.
13. Layer B passes with zero skips/warnings.
14. Layer C passes on Linux/macOS/Windows with zero skips/warnings.
15. Desktop UI workflow runs for both desktop and core changes.
16. Workflow uses current pinned Leshy, not v0.4.0.
17. Translation test executes.
18. Ubuntu installed runner passes and restores state.
19. Win10 installed runner passes and restores state.
20. Accessibility/geometry safety cases pass.
21. Linux native source coverage gates pass.
22. Exact evidence is recorded in repo docs.

Only then may UI be marked release-ready.

---

# 22. Executor start prompt

Use this task statement verbatim if desired:

~~~text
Update from origin/master and read:

- docs/roadmap.md
- docs/desktop-ui-core-audit-2026-11-10.md
- docs/desktop-ui-test-execution-and-coverage-plan.md
- docs/desktop-ui-executor-coverage-plan-2026-11-11.md
- docs/toad-steps/08d1-post-739e1ab-audit-remediation.md

The current UI structure is intentionally two pages: Home + Settings. Do not
restore the old five-tab UI.

Follow the stage order in docs/roadmap.md. The U1-U12 sections in this file are
implementation detail, not permission to skip the core remediation or to move
U5 after the core G/H VM acceptance.

Mandatory rules:
- do not delete/comment/skip tests to make them green;
- required QtTest QSKIP is a gate failure;
- FakeCore must mirror real snapshot shapes;
- hermetic real-core tests must own a temp filesystem and may not touch product
  /etc, /var/lib, /run or ProgramData paths;
- a managed core with route_authority_ready=false must never be shown as
  Protected;
- IPv4 and IPv6 family readiness are independent;
- keep disconnect available where it safely reduces exposure;
- run Desktop UI CI on core/API changes;
- use the current companion Leshy pin, not v0.4.0;
- VM acceptance must be performed on ubuntu2604 and win10 and must restore the
  initial lab state;
- record exact SHA, workflow run IDs, VM runner results, skip counts, warning
  counts and artifact hashes in the repo.

Do not claim UI release acceptance until Layer A/B/C, GitHub Linux/macOS/Windows,
ubuntu2604 and win10 are all green with zero mandatory skips.
~~~
