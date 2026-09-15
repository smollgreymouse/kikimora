# Kikimora desktop UI implementation plan

This plan implements the desktop UI described in [`desktop-ui-architecture.md`](desktop-ui-architecture.md) and is coordinated with the Go multi-VPN roadmap.

The interaction target is intentionally close to Amnezia: **one large aggregate connection circle**, compact role/toad status rows below it, and a drawer/expansion for a selected role.

## 1. Non-negotiable platform policy

```text
Linux    = real Kikimora core + real Leshy
macOS    = real Kikimora core + real Leshy
Windows  = shared frontend + FakeCore only
```

Leshy currently supports Linux and macOS, not Windows. Therefore real Kikimora networking is delivered on Linux and macOS together. Windows remains a frontend portability/test target until the missing routing/Leshy substrate exists.

A milestone claiming "desktop real-core support" is incomplete unless both Linux and macOS pass it.

Windows must still build and run the exact same QML/pages/models. It uses `FakeCoreClient` and visibly reports that its state is simulated.

## 2. UI/Go roadmap relationship

The UI is built in parallel with the Go core rather than after it.

```text
G0 contracts/tests
  -> U0 UX + state-contract freeze

G1 Go daemon / N-role model
  -> U1 Qt skeleton + three-platform build
  -> U2 CoreClient/FakeCore abstraction

G2 underlay monitor
G3 central reconciler
  -> U3 control API v1 + snapshot/event contract
  -> U4 aggregate state model

G4 endpoint-underlay ownership
G5 parking/publication
  -> U5 role detail state
  -> U6 Routing/Leshy page

G6 first real driver
G7 controlled recovery
  -> U7 Linux real-core E2E
  -> U8 macOS real-core E2E
  -> U9 recovery/degraded UX

G8 all managed roles Go-owned
G9 legacy watcher retired
  -> U10 tray/menu-bar + packaging

G10 diagnostics/operator surface
  -> U11 diagnostics/release acceptance
```

## 3. U0 - freeze UI state semantics and reference behavior

### Goal

Prevent the UI from inventing networking semantics later.

### Deliverables

Document and test the canonical mapping from core state to presentation state.

Freeze these concepts:

```text
AggregateConnectionState
RoleState
CoreConnectionState
UnderlayState
LeshyState
RoutingSafetyState
BackendKind = Real | Fake
```

Freeze primary aggregate button semantics:

```text
if every enabled role is stopped:
    button action = ConnectAll
else:
    button action = DisconnectAll
```

This includes partial, connecting, recovering and degraded states. Per-role correction is a secondary action in role detail, not hidden behavior of the global circle.

### Required state table

At minimum record expected circle presentation for:

```text
all stopped
all connecting
all ready
one ready + one connecting
one ready + one failed
one recovering + others ready
all failed after ConnectAll
underlay missing
real core unavailable
Windows FakeCore
```

### Exit gate

The UI can be implemented from the state table without asking QML to infer low-level networking state.

## 4. U1 - Qt 6 application skeleton and build matrix

### Goal

Create one cross-platform frontend before adding product pages.

### Structure

```text
desktop/
  CMakeLists.txt
  src/
  qml/
  tests/
```

Use:

```text
Qt6::Core
Qt6::Gui
Qt6::Quick
Qt6::QuickControls2
Qt6::Svg
Qt6::Network where needed by RealCoreClient
```

Prefer `qt_add_qml_module` for owned QML modules/resources.

### Build targets

CI must build:

```text
ubuntu-latest
macos-latest
windows-latest
```

Windows success does not imply networking support; it proves frontend portability.

### Initial smoke

The app opens one compact window with placeholder Home/Profiles/Routing/Settings pages and no networking logic in QML.

### Exit gate

All three OS builds start the same application shell.

## 5. U2 - CoreClient abstraction and FakeCore

### Goal

Make frontend development independent of real VPN-driver completion.

Create an interface approximately shaped as:

```text
CoreClient
  connectTransport()
  backendKind()
  capabilities()
  requestSnapshot()
  subscribe()
  sendCommand(Command)
```

Implement:

```text
RealCoreClient   Linux/macOS
FakeCoreClient   Windows + UI tests + optional developer mode
```

### FakeCore requirements

FakeCore must be deterministic, scriptable and stateful. It must not be a collection of ad-hoc booleans inside QML.

Scenario fixtures:

```text
all-stopped
connecting-all
all-ready
primary-ready-secondary-failed
secondary-recovering
underlay-down
parking-active
endpoint-pending
leshy-failed
core-error
```

FakeCore accepts the same semantic commands:

```text
ConnectAll
DisconnectAll
ConnectRole
DisconnectRole
RetryRole
SetActiveProfile
RediscoverEndpoints
```

It returns simulated snapshots/events with monotonically increasing revisions.

### Windows policy

Windows runtime uses FakeCore by default. Add a visible `SIMULATED`/`FakeCore` indication in Diagnostics and optionally a subtle developer banner on normal pages. Never display language implying the Windows host is actually protected.

### Exit gate

The complete intended Home behavior can be exercised on Windows without a real daemon.

## 6. U3 - design system and Amnezia-like control layer

### Goal

Build visual primitives before product composition.

Create `KikimoraTheme` singleton with semantic tokens:

```text
backgroundBase
surfaceBase
surfaceHover
surfacePressed
textPrimary
textSecondary
textMuted
borderSoft
accentPrimary
accentSuccess
accentWarning
accentError
spacing*
radius*
animationFast
animationNormal
focusRingWidth
```

### Required controls

```text
KConnectControl
KRoleSummaryList
KRoleSummaryRow
KRoleDetailDrawer
KStatusChip
KButton
KIconButton
KHeader
KBottomNav
KDrawer
KDialog
KBanner
KSwitch
KTextField
KComboBox
KProgressIndicator
KDivider
```

### KConnectControl

This is the visual center of the product.

Requirements:

```text
large circular ring
center label/icon
hover/press/focus states
disconnected/connected/warning/error colors
rotating/progress arc for Connecting/Disconnecting/Recovering
keyboard activation
reduced-motion mode
```

It receives presentation properties such as:

```text
state
primaryText
secondaryText
busy
enabled
```

and emits only an activation signal. It never decides which network command to send.

### Exit gate

Story/test harness can render every aggregate state and every collapsed role state without the full Home page.

## 7. U4 - Home page: one global circle

### Goal

Match the requested Amnezia-like hierarchy.

### Layout

```text
Header / active profile
Underlay short status

          large global circle
          aggregate state text

Role/toad status list
  Primary      Ready       >
  Secondary    Recovering  >
  ...

Bottom navigation
```

Do not render separate large role cards.

### Global action controller

Create a C++ presentation controller such as `AggregateConnectionController`.

It maps activation to:

```text
Disconnected -> ConnectAll
otherwise running/partial/busy -> DisconnectAll
```

The controller submits one core command. It does **not** iterate `RolesModel` and send N QML-side commands.

The core owns ordering, rollback and per-role orchestration.

### Aggregate rendering examples

```text
all stopped:
  circle = neutral
  text = Connect

all ready:
  circle = warm success/accent
  text = Connected

connecting:
  animated ring
  text = Connecting

secondary recovering, primary ready:
  warning/progress state
  text = Recovering

primary ready, secondary failed:
  warning/error aggregate
  text = Partially connected / Problem
  click still = DisconnectAll
```

Exact strings belong in localization resources.

### Exit gate

The Home page has exactly one dominant connection control regardless of role count.

## 8. U5 - collapsed role/toad list and Amnezia-style expansion

### Goal

Expose role-specific truth without competing with the global control.

### Collapsed row

Each `KRoleSummaryRow` contains:

```text
role label
optional short protocol/server label
semantic state text/icon
chevron
```

Rows stay compact even with N roles.

### Selection behavior

Clicking a row opens `KRoleDetailDrawer` for that role.

Only one role detail is open at a time in the compact window.

The drawer shows:

```text
role name
state
protocol/driver
server/endpoint
runtime interface
underlay summary
last validated epoch
endpoint-underlay ready/pending
Leshy published/not published
parked destinations
last recovery action/reason
last error
```

Secondary actions appear here when allowed by core capabilities:

```text
Connect role
Disconnect role
Retry role
Rediscover endpoint
Open diagnostics
```

These actions must be hidden/disabled based on core-reported `available_actions`, not guessed from QML state.

### Exit gate

A user can understand why one role differs from the aggregate state without leaving Home, but Home remains visually as simple as a single-VPN client.

## 9. U6 - navigation, Profiles, Routing/Leshy, Settings

### Navigation

Use one StackView-style host and bottom navigation:

```text
Home
Profiles
Routing
Settings
```

### Profiles

First production scope:

```text
list profiles
active profile marker
profile detail
contained roles/toads
activate profile
validation errors
```

Profile editing can follow once the Go schema is stable.

### Routing/Leshy

Real Linux/macOS page:

```text
Leshy service state
DNS integration
role-to-zone mapping
default zone
endpoint-underlay state
parking summary
routing safety warnings
```

Windows renders FakeCore scenarios and a simulation marker.

### Settings

Split settings ownership:

```text
UI-only -> local QSettings/native preferences
network/core -> core API
```

### Exit gate

No page shells out or reads runtime networking files directly.

## 10. U7 - local control API v1 for Linux/macOS

### Goal

Replace test FakeCore with real streaming state on both production platforms.

### Transport

```text
Unix-domain socket
length-prefixed JSON v1
```

### Handshake

Client sends supported protocol range and UI version.

Core returns:

```text
protocol version
core version
platform
backend kind = real
capabilities
```

### Snapshot/event rules

```text
GetSnapshot -> complete revision N
Subscribe -> revision N+1...
```

Frontend rejects stale revisions and refreshes on gaps.

### Commands required for initial UI

```text
ConnectAll
DisconnectAll
ConnectRole
DisconnectRole
RetryRole
SetActiveProfile
RediscoverEndpoints
GetDiagnosticsSnapshot
```

### Security

The real core authorizes the local peer and exposes high-level operations only.

### Exit gate

The exact same AppModel can switch between FakeCore and RealCoreClient without QML changes.

## 11. U8 - Linux real-core integration

### Scope

Wire real Linux state into:

```text
aggregate circle
role list
role drawer
profiles
routing/Leshy
settings
tray
```

### Required E2E scenarios

```text
start UI with daemon already running
start UI with VPNs already ready
ConnectAll from all stopped
DisconnectAll from ready
one role fails during ConnectAll
one role recovers on underlay change
parking becomes active
endpoint-underlay pending
core restarts while UI stays open
UI restarts while tunnels stay up
```

### Exit gate

No production Linux Home state depends on FakeCore or text CLI parsing.

## 12. U9 - macOS real-core integration

### Scope

Implement the same semantic core contract on macOS with real Leshy and real routing/VPN ownership.

Required platform work may include:

```text
launchd core/helper lifecycle
privilege setup
macOS underlay observation
route/endpoint adapter
Leshy launchd integration
native status item/menu
login item/autostart
notification integration
```

Do not fork QML pages for macOS networking semantics.

### E2E parity

Run the same semantic scenarios as Linux where platform behavior allows:

```text
ConnectAll
DisconnectAll
partial failure
recovery after network change
parking/safe withdrawal equivalent
Leshy publication
core restart
UI restart
```

### Exit gate

The "real desktop client" milestone is not complete until this stage passes. Linux-only completion is insufficient.

## 13. U10 - recovery/degraded UX

### Goal

Make multi-role recovery understandable without making Home noisy.

### Home rules

Home shows only aggregate summary plus role row state.

Examples:

```text
Connected
Recovering
Partially connected
Waiting for network
Connection failed
```

### Detail rules

Recovery reason belongs in the expanded role drawer:

```text
physical underlay changed
transport validation failed
endpoint path pending
parking active during controlled restart
waiting for underlay
```

Avoid transient toast spam for expected automatic recovery.

Use notifications only for meaningful user-visible failures or persistent degraded state.

### Exit gate

Normal handoff/recovery is understandable from the circle + one role row; deeper reason is one click away.

## 14. U11 - tray/menu-bar integration

### Common contract

Create `DesktopShell` abstraction:

```text
showMainWindow
hideMainWindow
notify
setAggregateState
setMenuModel
setLaunchAtLogin
```

### Linux

Use Qt tray where available and degrade gracefully when absent.

### macOS

Use a native `NSStatusItem` adapter if needed, following the same architectural lesson as Amnezia.

### Windows

Tray is allowed for frontend parity but displays FakeCore/simulated status.

### Menu hierarchy

Keep the aggregate action primary:

```text
Show Kikimora
Connected / Recovering / ...
Connect all OR Disconnect all
Primary: Ready
Secondary: Recovering
Quit
```

Per-role mutating actions are optional and secondary.

### Exit gate

Closing the UI window does not stop the Linux/macOS core/tunnels.

## 15. U12 - diagnostics

Diagnostics must expose the state needed to explain the aggregate circle.

Required data:

```text
UI version
core version
protocol version
backend kind real/fake
capabilities
underlay identity/epoch
aggregate state derivation inputs
per-role states
last recovery action/reason
endpoint desired/applied/pending
parking state
Leshy state
recent structured logs
```

Actions:

```text
copy status
export debug bundle
refresh snapshot
open log location where meaningful
```

No diagnostics page may become a hidden second networking controller.

## 16. U13 - accessibility, keyboard and localization

Required before release:

```text
full keyboard navigation
visible focus ring
Enter/Space activation
screen-reader labels for circle and role rows
no color-only state meaning
reduced-motion support
translatable strings
long-string layout tests
HiDPI tests
```

The central circle must announce both state and action, for example conceptually:

```text
"Kikimora disconnected. Connect all VPNs."
"Kikimora connected. Disconnect all VPNs."
```

## 17. U14 - packaging

### Linux

Package Qt runtime/plugins/QML modules/resources and integrate desktop entry/icon/autostart behavior as appropriate.

### macOS

Build/sign/notarize `Kikimora.app`, package Qt frameworks/plugins, install or bootstrap the privileged core/helper and Leshy integration with a documented lifecycle.

### Windows

Package only the shared frontend + FakeCore mode for now. Do not ship a misleading networking service.

### Exit gate

A clean-machine install launches successfully on all three platforms, and only Linux/macOS expose real networking capabilities.

## 18. U15 - CI/test matrix

### Common tests

```text
C++ model/controller unit tests
QML component smoke tests
FakeCore deterministic scenario tests
aggregate-state derivation tests
role drawer tests
snapshot/revision tests
keyboard/focus tests
```

### Linux real-core tests

```text
IPC integration
ConnectAll/DisconnectAll
partial role failure
underlay recovery
parking/endpoint state propagation
UI/core restart independence
```

### macOS real-core tests

Same semantic contract, with platform-specific route/service adapters.

### Windows tests

```text
build
launch
FakeCore scenarios
all pages render
no real networking capability advertised
```

### Exit gate

Windows failures catch frontend portability issues; Linux/macOS failures gate production networking behavior.

## 19. U16 - release acceptance

The desktop UI line is complete when all of the following are true:

1. Home has one large aggregate Connect/Disconnect circle.
2. The circle invokes `ConnectAll`/`DisconnectAll`, never N QML-side role commands.
3. Per-role/toad states appear as compact collapsed rows below the circle.
4. Clicking a row expands a detailed role drawer.
5. Per-role truth remains visible during partial/degraded aggregate state.
6. Linux uses the real Kikimora core and real Leshy.
7. macOS uses the real Kikimora core and real Leshy.
8. Real-core release gates require both Linux and macOS.
9. Windows uses FakeCore only and visibly reports simulation.
10. The shared QML tree builds on Linux/macOS/Windows.
11. UI shutdown does not tear down real tunnels.
12. Core/UI restarts resynchronize from a fresh snapshot.
13. Recovery state is understandable without exposing low-level route noise on Home.
14. Diagnostics can explain why aggregate and per-role states differ.
15. Accessibility, localization, HiDPI and packaging gates pass.

## 20. Explicit non-goals

Do not implement in this UI line:

```text
Windows real networking without Leshy/routing support
separate giant connection card per VPN role
QML shell commands
QML parsing of `kk` human output
route-table editing UI
one top-level window per role
VPN-over-VPN dependency visualization
```

The intended product remains a simple Amnezia-like front surface over a substantially more capable multi-VPN core.
