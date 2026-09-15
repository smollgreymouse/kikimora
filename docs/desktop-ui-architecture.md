# Kikimora desktop UI target architecture

This document defines the target desktop UI architecture for the Go multi-VPN Kikimora transition.

The intended visual/interaction model is deliberately close to the useful desktop/mobile language of Amnezia VPN: a compact dark application, one dominant circular connection control, compact status below it, StackView-style navigation, drawers for details, and a reusable Qt Quick control layer. Kikimora does not copy Amnezia branding or assets.

The detailed implementation sequence is in [`desktop-ui-implementation-plan.md`](desktop-ui-implementation-plan.md).

The Go control-plane target remains described in [`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md).

## 1. Platform policy

The platform split is fixed from the beginning:

```text
Linux    = real Kikimora core + real VPN drivers + real Leshy
macOS    = real Kikimora core + real VPN drivers + real Leshy
Windows  = shared Qt/QML application + FakeCore only
```

Leshy itself currently declares Linux + macOS support, with rtnetlink on Linux and `/sbin/route` on macOS, and installs as a systemd service on Linux or launchd service on macOS. Windows is not a Leshy target today.

Therefore Kikimora production networking exists only on platforms where the complete Kikimora stack exists. We do not create a half-real Windows backend without Leshy merely to claim platform parity.

Windows remains valuable from day one as a frontend portability target. The same QML, presentation models, navigation, theme and protocol DTOs must build and run there, but they are backed by `FakeCore` and never mutate networking.

A future Windows production backend is a new roadmap item only after the required routing/Leshy substrate exists there.

## 2. Product boundary

The desktop application has three responsibilities:

1. present the aggregate Kikimora state clearly;
2. expose the individual VPN-role / "toad" state without cluttering the home screen;
3. send explicit operator actions to the Kikimora core and provide desktop integration.

The UI never owns networking truth.

```text
OS networking
    |
    v
Kikimora Go core
    |
    +-- physical-underlay monitor
    +-- endpoint-underlay controller
    +-- VPN role state machines
    +-- route parking/publication
    +-- Leshy orchestration
    |
    v
versioned local control API
    |
    v
Qt/C++ presentation layer
    |
    v
shared QML UI
```

The UI must not infer truth from TUN existence, `ip route`, NetworkManager, launchd/systemd, or Leshy runtime files when a real core is available.

## 3. Reference architecture from Amnezia

Useful patterns to adopt from Amnezia:

- Qt 6 + Qt Quick/QML;
- one compact application window;
- reusable QML controls instead of page-local styling;
- singleton style/theme object;
- C++ controllers/models exported to QML;
- StackView-based navigation;
- a large circular connection control as the visual center;
- secondary state in drawers/sheets rather than many independent windows;
- platform-specific desktop integration behind common interfaces;
- privileged/network logic outside the QML process.

Reference areas in `amnezia-vpn/amnezia-client` include:

```text
client/CMakeLists.txt
client/amneziaApplication.cpp
client/core/controllers/coreController.cpp
client/ui/controllers/
client/ui/models/
client/ui/qml/main2.qml
client/ui/qml/Pages2/
client/ui/qml/Controls2/
client/ui/qml/Modules/Style/AmneziaStyle.qml
client/ui/utils/systemTrayNotificationHandler.cpp
```

The important product correction for Kikimora is not to turn every managed VPN into a separate big connect card. Kikimora may have several independent VPN roles internally, but the normal user action is still one aggregate "connect all / disconnect all" operation.

## 4. Technology decision

Use **Qt 6 + Qt Quick/QML + CMake** for the frontend.

Frontend layering:

```text
QML pages/components
        |
        v
Qt/C++ presentation models/controllers
        |
        v
CoreClient interface
        |
        +-- RealCoreClient on Linux/macOS
        +-- FakeCoreClient on Windows/tests
```

Do not embed the Go networking core through cgo/C ABI. Linux and macOS use a separate long-running core process with a local authenticated IPC channel.

QML contains view composition and presentation-only expressions. Protocol framing, revision ordering, permissions, retries and platform networking remain below QML.

## 5. Process architecture by platform

### 5.1 Linux production

```text
kikimora-ui                    user session
    |
    | Unix-domain socket
    v
kikimora-core                  system service
    |
    +-- Go VPN drivers
    +-- rtnetlink / NetworkManager / logind inputs
    +-- endpoint routing
    +-- parking/publication
    +-- Leshy orchestration
```

systemd supervises processes; it is not the VPN recovery state machine.

### 5.2 macOS production

```text
Kikimora.app                   user session
    |
    | local authenticated IPC
    v
kikimora-core/helper           launchd-supervised privileged component
    |
    +-- Go VPN drivers/platform adapter
    +-- physical-underlay observation
    +-- endpoint routing
    +-- parking/publication equivalent
    +-- Leshy orchestration
```

macOS is a first-class production target, not a later port. A real-core milestone is incomplete if it works on Linux but not macOS.

### 5.3 Windows

```text
kikimora-ui.exe
    |
    v
FakeCoreClient
```

There is no production Windows networking core in the current roadmap.

`FakeCore` implements the same frontend-facing contract and can simulate:

```text
all roles stopped
all roles connecting
all roles ready
one role failed
one role recovering
underlay lost/restored
parking active
endpoint-underlay pending
Leshy unavailable/failure
core/API error states
```

Windows UI must visually indicate that it is running with a simulated backend. It must never claim that the host is actually protected.

The Windows target exists to catch Qt/QML portability regressions and allow UI development/testing without inventing unsupported networking semantics.

## 6. Core control API

Linux/macOS require a long-lived local API rather than repeatedly spawning `kk --json`.

Required properties:

- version/capability handshake;
- complete initial snapshot;
- revisioned event stream;
- request/result correlation;
- reconnect after core restart;
- high-level commands only.

Initial transport:

```text
Linux/macOS: Unix-domain socket
framing:     length-prefixed UTF-8 JSON initially
Windows:     no transport requirement while FakeCore is in-process
```

The API must expose semantic operations such as:

```text
ConnectAll()
DisconnectAll()
ConnectRole(role)        # secondary/detail action, not the home-page primary action
DisconnectRole(role)
RetryRole(role)
SetActiveProfile(name)
RediscoverEndpoints(role)
ExportDiagnostics(...)
```

Do not expose arbitrary shell/root execution.

Every real-core connection starts with version/capability negotiation. The QML layer consumes capabilities; it does not use `Qt.platform.os` as a substitute for backend capability detection.

After handshake:

```text
GetSnapshot -> StateSnapshot(revision=N)
Subscribe   -> StateChanged(revision=N+1...)
```

If an event gap is detected or IPC reconnects, the UI requests a fresh snapshot.

## 7. Frontend state model

Recommended top-level object graph:

```text
AppModel
    +-- CoreConnectionModel
    +-- CapabilityModel
    +-- AggregateConnectionModel
    +-- UnderlayModel
    +-- RolesModel
    +-- ProfilesModel
    +-- LeshyModel
    +-- RoutingSafetyModel
    +-- SettingsModel
    +-- DiagnosticsModel
```

### 7.1 AggregateConnectionModel

This is a presentation model derived from canonical role/core state. It drives the single large connection circle.

Recommended aggregate visual states:

```text
Disconnected      all enabled roles stopped
Connecting        aggregate connect operation in progress
Connected         all required/enabled roles Ready
Recovering        one or more roles recovering and none terminally failed
Degraded          at least one usable role exists but the desired aggregate state is not satisfied
Failed            requested aggregate connection could not be established
Disconnecting     aggregate stop in progress
Unavailable       no real backend / underlay/core prevents the requested operation
```

This model must not erase per-role truth. It is a summary only.

Primary button semantics are intentionally simple:

```text
all enabled roles stopped
    -> click = ConnectAll

anything running/starting/recovering/partially ready
    -> click = DisconnectAll
```

This avoids a confusing state where the same global button tries to "finish connecting the missing role" while other roles are already active. Per-role retry/connect actions live in the expanded role detail.

### 7.2 RolesModel

The model is N-role even though current profiles normally expose primary and secondary roles.

Per-role fields:

```text
id
label
configured/enabled
state
driver/protocol
interface
server/endpoint display label
last error
last recovery reason
last validated underlay epoch
published to Leshy
parked destination count
endpoint-underlay state
available actions
```

Role states:

```text
Stopped
Starting
WaitingForUnderlay
Connecting
Ready
Degraded
Recovering
Disconnecting
Failed
Unsupported
```

### 7.3 LeshyModel

Linux/macOS real backend exposes real Leshy state:

```text
service/runtime state
routing ready/not ready
default zone
per-role/zone mapping
DNS integration state
last error
```

Windows FakeCore exposes simulated values only and marks the whole backend as simulated.

### 7.4 RoutingSafetyModel

Expose operator-useful safety state:

```text
endpoint policy ready/pending
parking active/count
leak-safety state
recovery transaction phase
```

## 8. Visual design system

The visual target is intentionally closer to Amnezia than the earlier card-heavy draft:

```text
near-black background
pale primary text
muted secondary text
one warm accent
large central connection ring
compact status rows underneath
rounded drawers/sheets
short restrained animations
bottom navigation
```

Create a singleton `KikimoraTheme` containing colors, spacing, radii, typography, control metrics, opacity, animation duration and focus-ring metrics.

Suggested first window geometry:

```text
default width:  400-440 px
default height: 680-760 px
minimum width:  360-380 px
```

The home page must not require enough vertical space for multiple large role cards.

## 9. Reusable QML controls

Build the control library before product pages multiply.

Core components:

```text
KConnectControl          # one large Amnezia-like aggregate circle
KRoleSummaryList
KRoleSummaryRow          # collapsed per-role status row
KRoleDetailDrawer        # expanded role details
KStatusChip
KButton
KIconButton
KCard
KBanner
KHeader
KBottomNav
KDrawer
KDialog
KSwitch
KTextField
KComboBox
KListRow
KProgressIndicator
KTooltip
KDivider
```

`KConnectControl` owns ring geometry, progress animation, focus/hover/pressed states and aggregate text/icon presentation. It does not decide networking state itself.

`KRoleSummaryRow` is deliberately small: it is status/navigation first, not another connection button.

## 10. Navigation

Use one application window and one StackView-style navigation host.

Bottom navigation:

```text
Home
Profiles
Routing
Settings
```

Diagnostics live under Settings and may be linked directly from error banners/details.

Secondary information opens by stack navigation or drawer. Do not create multiple top-level windows for normal workflow.

## 11. Home screen

### 11.1 Desired composition

The default home layout is:

```text
+----------------------------------+
| Kikimora              home       |
| Wi-Fi / Ethernet underlay        |
|                                  |
|                                  |
|              (  O  )             |
|            CONNECT ALL           |
|                                  |
|         aggregate state text     |
|                                  |
| -------------------------------- |
| Primary                 Ready  > |
| Secondary            Stopped  > |
| ...                              |
| -------------------------------- |
|                                  |
| Home  Profiles  Routing Settings |
+----------------------------------+
```

When connected, the same central circle becomes the global disconnect control, following the Amnezia interaction pattern:

```text
CONNECTED
click -> DisconnectAll
```

During aggregate transition the ring animates. In `Degraded`/`Failed`, the circle uses warning/error semantics but remains a global disconnect control while any role is active.

### 11.2 Compact role/toad status list

Below the circle render one collapsed row per configured VPN role / "toad".

Collapsed row contains only the information needed to scan the system:

```text
role display name
protocol/server short label when useful
state text + semantic indicator
chevron/disclosure affordance
```

Examples:

```text
Primary      AmneziaWG / NL      Ready       >
Secondary    Xray / DE           Recovering  >
```

No second large circle and no large standalone role card belongs on Home.

### 11.3 Expanding a role

Clicking a collapsed role row opens or expands `KRoleDetailDrawer`, analogous to Amnezia exposing detail for the selected server/protocol.

Detail content:

```text
role name
state
protocol/driver
server/endpoint label
runtime interface
physical underlay path summary
last validated underlay epoch
endpoint-underlay state
Leshy publication state
parked route count
last recovery action/reason
last error
```

Context actions may appear here:

```text
Connect this role
Disconnect this role
Retry this role
Rediscover endpoint
Open diagnostics
```

These are secondary/advanced actions. Normal use remains the single global circle.

Only one role detail drawer should be expanded at a time on the compact layout.

## 12. Profiles

Profiles remain a separate page.

Required operations:

```text
list profiles
show active profile
activate profile
show contained roles/toads and drivers
validate before apply
create/edit/delete when the core schema is stable
```

Changing a profile does not make QML invent a success state. The home circle and role list update from core events.

## 13. Routing / Leshy

Linux and macOS expose real routing/Leshy state:

```text
Leshy running/ready
DNS integration
role-to-zone mapping
default zone
endpoint-underlay readiness
parking summary
routing safety warnings
```

Windows FakeCore renders a clearly simulated/non-production version of this page so the QML remains portable.

Raw route dumps remain Diagnostics, not normal Home content.

## 14. Settings and diagnostics

Shared settings include:

```text
launch UI at login
theme/language
notifications
close-to-tray behavior
confirm disconnect-all
logging preference where supported
```

Diagnostics must expose:

```text
UI/core/API version
real vs FakeCore backend
capabilities
underlay snapshot/epoch
aggregate state
role state table
recovery reasons/actions
endpoint desired/applied/pending
parking state
Leshy state
recent structured logs
```

## 15. Tray/menu-bar

Desktop integration sits behind C++ abstractions, for example:

```text
DesktopShell
AutostartAdapter
NotificationAdapter
CredentialStore
PlatformInfo
```

### Linux

Use Qt system tray support where available; the app must remain usable without a tray implementation.

### macOS

Prefer a native `NSStatusItem` adapter when Qt tray behavior is insufficient. The production macOS build still uses the same QML application and the same real Kikimora/Leshy core contract.

### Windows

The tray may be implemented for UI parity, but it reflects FakeCore state and clearly marks simulation. No network commands leave the FakeCore boundary.

Tray menu follows the same product hierarchy as Home:

```text
Show Kikimora
aggregate state
Connect all / Disconnect all
role status summaries
Quit
```

Per-role mutation is optional in the tray; the normal aggregate action is primary.

## 16. Window/core lifecycle

Closing the window normally hides it when tray/menu-bar mode is enabled. `Quit` is explicit.

Stopping the UI must not stop Linux/macOS managed tunnels. Core lifetime is independent.

On UI restart:

```text
connect to real core
handshake
fresh snapshot
render actual state
```

On Windows:

```text
instantiate FakeCore
load selected deterministic scenario
render SIMULATED state
```

## 17. Repository layout

Suggested target:

```text
desktop/
├── CMakeLists.txt
├── src/
│   ├── main.cpp
│   ├── app/
│   ├── coreclient/
│   │   ├── CoreClient.h
│   │   ├── RealCoreClient.*
│   │   └── FakeCoreClient.*
│   ├── models/
│   ├── controllers/
│   └── platform/
│       ├── linux/
│       ├── macos/
│       └── windows/
├── qml/
│   ├── Main.qml
│   ├── Theme/
│   ├── Controls/
│   ├── Pages/
│   └── Drawers/
└── tests/
```

## 18. Testing contract

Frontend unit/component tests must cover at least:

```text
Disconnected -> ConnectAll
Connected -> DisconnectAll
partial/degraded aggregate -> DisconnectAll
connecting animation
aggregate failure
collapsed role rows
opening one role drawer
switching between role drawers
role recovery/error detail
core disconnect/reconnect snapshot replacement
revision gap handling
FakeCore simulation badge
keyboard/focus navigation
HiDPI/layout smoke
```

Production acceptance requires both Linux and macOS real-core E2E coverage. Windows is a frontend/FakeCore build-and-test gate only.

## 19. Hard rules

1. Home has one dominant global circular connection control.
2. Home does not show separate large per-role connection cards.
3. Per-role/toad state is a compact collapsed list below the circle.
4. Clicking a role reveals detail in a drawer/expansion, Amnezia-style.
5. Aggregate state never replaces canonical per-role state in the core.
6. `ConnectAll`/`DisconnectAll` are core orchestration commands, not loops implemented in QML.
7. Linux and macOS are real production targets together.
8. Windows remains on FakeCore until the required Leshy/routing substrate exists.
9. QML never shells out or mutates networking directly.
10. Platform-specific UI integration may differ; product/state semantics may not.
