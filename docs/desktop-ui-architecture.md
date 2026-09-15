# Kikimora desktop UI target architecture

This document defines the target desktop UI architecture for the Go multi-VPN
Kikimora transition.

It is intentionally a **desktop** design. The first production platforms are Linux
and macOS. Windows is built from the same UI sources from the beginning, but its
network/VPN/Leshy backend is initially a capability-reporting stub rather than a
fake working VPN implementation.

The detailed implementation sequence is in
[`desktop-ui-implementation-plan.md`](desktop-ui-implementation-plan.md).

The Go control-plane target remains described in
[`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md).

## 1. Product scope

The desktop application has three responsibilities:

1. present Kikimora's multi-VPN state clearly;
2. send explicit operator actions to the Kikimora core;
3. provide desktop integration such as tray/menu-bar status, notifications,
   startup behavior, settings and diagnostics.

The UI does **not** own networking state and does not inspect Linux/macOS kernel
networking as an independent source of truth.

The authoritative chain is:

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
Kikimora desktop frontend
    |
    +-- Qt/C++ frontend state/models
    +-- QML presentation
    +-- platform desktop integration
```

The UI must never reconstruct core truth from TUN existence, route tables,
NetworkManager state, launchd/systemd state or Leshy runtime files when the core is
available.

## 2. Reference: useful ideas from the Amnezia desktop client

Amnezia is a useful reference for both the visual language and the implementation
split, but Kikimora should not copy Amnezia branding, icons, artwork or resource files.

The relevant implementation patterns in the Amnezia client are:

- Qt 6 as the common desktop toolkit;
- Qt Quick/QML for the application surface;
- a reusable QML control library rather than styling every page independently;
- a singleton style/theme object;
- a page/navigation controller with a StackView-based navigation surface;
- C++ models/controllers exported to QML;
- platform-specific code hidden behind a common desktop application;
- desktop tray/status integration outside QML where native behavior requires it;
- a client/service boundary for privileged operations.

Concrete reference paths in `amnezia-vpn/amnezia-client`:

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
ipc/ipc_interface.rep
```

Visual traits worth adopting as a **Kikimora design language** rather than as a
literal clone:

- compact utility-app window;
- dark, near-black background;
- layered dark surfaces;
- restrained warm accent color;
- rounded cards and controls;
- strong central connection/status affordance;
- short, smooth state animations;
- bottom navigation for the small-window layout;
- drawers/sheets for secondary detail rather than opening many independent windows;
- status communicated by both text and color;
- keyboard/focus behavior treated as a first-class desktop concern.

Kikimora differs from Amnezia in one fundamental product assumption: there is not one
single global VPN connection. The home surface must represent multiple independent VPN
roles and Leshy routing truth without pretending they are one tunnel.

## 3. Technology decision

### 3.1 UI toolkit

Use **Qt 6 + Qt Quick/QML**.

Reasons:

- one UI implementation for Linux, macOS and Windows;
- mature HiDPI and desktop input handling;
- QML is a good fit for the compact animated style being targeted;
- C++ adapters can expose strongly typed state to QML;
- platform-native integration can be added in C++/Objective-C++ without forking the
  QML page tree;
- CMake and Qt deployment tooling support all three target desktop platforms.

The UI project should use CMake and a QML module rather than a manually maintained
resource list once the skeleton is established.

Conceptual build shape:

```text
qt_add_executable(kikimora-ui ...)
qt_add_qml_module(kikimora-ui
    URI Kikimora.UI
    ...)
```

### 3.2 Language boundary

Do **not** embed the Go core into the Qt process through cgo/C ABI.

Use two processes:

```text
unprivileged desktop UI
        |
        | local authenticated IPC
        v
privileged/separately supervised Go core
```

This gives:

- privilege separation;
- independent core/UI crashes and upgrades;
- the same UI against a fake backend in tests;
- the same Windows UI against a stub core;
- no C ABI lifetime/threading bridge between Qt and Go;
- a clean path for CLI/TUI and future frontends to share core semantics.

### 3.3 Frontend layering

Use three frontend layers:

```text
QML pages/components
        |
        v
Qt/C++ presentation models/controllers
        |
        v
CoreClient transport/protocol adapter
```

QML should contain view composition and small presentation-only expressions.
It should not contain protocol framing, reconnection logic, revision ordering,
permission handling or platform networking knowledge.

## 4. Process architecture by platform

### 4.1 Linux production

```text
kikimora-ui                    user session
    |
    | local socket
    v
kikimora-core                  system service
    |
    +-- Go VPN drivers
    +-- rtnetlink/NM/logind underlay inputs
    +-- endpoint routing
    +-- parking/publication
    +-- Leshy orchestration
```

`kikimora-core` is the only networking authority. systemd supervises it but does not
implement network-watch policy.

### 4.2 macOS production

```text
Kikimora.app                   user session
    |
    | local socket
    v
kikimora-core/helper           launchd-supervised privileged component
    |
    +-- Go VPN drivers/platform adapter
    +-- physical-underlay observation
    +-- route/endpoint ownership
    +-- parking/publication equivalent
    +-- Leshy orchestration
```

macOS is not a later UI port. The common UI and real core contract must be validated on
macOS throughout implementation. Missing macOS networking/Leshy support is a blocker for
the corresponding production milestone, not a reason to substitute the Windows-style
stub.

### 4.3 Windows initial product

```text
kikimora-ui.exe
    |
    | same control protocol
    v
kikimora-core.exe --stub
```

The Windows stub must implement protocol negotiation and explicit capabilities but must
not pretend that VPN/Leshy control works.

Typical capability response:

```json
{
  "platform": "windows",
  "backend": "stub",
  "capabilities": {
    "vpn_control": false,
    "leshy": false,
    "routing_policy": false,
    "endpoint_underlay": false,
    "diagnostics": true,
    "settings": true
  }
}
```

Production UI behavior on Windows:

- application starts normally;
- navigation, theme, settings and diagnostics shell work;
- VPN/Leshy surfaces clearly show `Unsupported`/`Not implemented on Windows yet`;
- destructive or meaningless actions are unavailable;
- no fake `Connected` state is ever shown;
- a developer-only simulated backend may exist for UI tests, but it is not the
  production Windows stub.

This prevents platform-specific QML forks while keeping Windows build health from day
one.

## 5. Core control API

The desktop UI needs a long-lived API; repeatedly spawning `kk ... --json` is not the
production architecture.

The existing CLI JSON API remains useful as a semantic compatibility seed, but the GUI
requires:

- initial snapshot;
- streaming state changes;
- command request/result correlation;
- revision/generation ordering;
- capability negotiation;
- clean reconnect after core restart.

### 5.1 Transport

Recommended first implementation:

```text
Linux/macOS: Unix-domain socket
Windows:     named pipe
framing:     uint32 big-endian payload length + UTF-8 JSON
```

The protocol layer must be transport-neutral so protobuf or another encoding can replace
JSON later without changing QML models.

Reasons to prefer a local socket/pipe over localhost HTTP:

- no TCP port allocation/collision;
- simpler local-only exposure;
- peer identity/ACL support;
- natural lifecycle with a system daemon;
- no accidental LAN listener.

### 5.2 Security

The privileged core must authorize the local peer, not merely trust that a process can
name the socket.

Platform requirements:

```text
Linux:   socket permissions + peer credentials
macOS:   socket permissions + peer credentials/code-sign identity where needed
Windows: named-pipe ACL when the real backend arrives
```

The API should expose high-level operations, not arbitrary shell/root execution.

Forbidden API examples:

```text
runShell(command)
runIp(args)
writeFileAsRoot(path, bytes)
```

Allowed shape:

```text
ConnectRole(role)
DisconnectRole(role)
SetActiveProfile(name)
RediscoverEndpoints(role)
ExportDiagnostics(options)
```

### 5.3 Handshake

Every connection starts with a version/capability handshake.

Conceptual request:

```json
{
  "type": "hello",
  "ui_version": "...",
  "protocol_min": 1,
  "protocol_max": 1
}
```

Conceptual response:

```json
{
  "type": "hello_result",
  "protocol": 1,
  "core_version": "...",
  "platform": "linux",
  "backend": "real",
  "capabilities": {
    "vpn_control": true,
    "leshy": true,
    "routing_policy": true,
    "endpoint_underlay": true,
    "logs": true,
    "debug_bundle": true
  }
}
```

No QML expression should test `Qt.platform.os` to decide whether core functionality
exists. It should read capabilities.

`Qt.platform.os` remains valid for presentation details such as shortcut labels or native
window behavior.

### 5.4 Snapshot + event stream

After handshake:

```text
GetSnapshot
    -> complete StateSnapshot(revision=N)

Subscribe
    -> StateChanged(revision=N+1, ...)
    -> StateChanged(revision=N+2, ...)
```

The client rules are:

1. never apply an event older than the current revision;
2. if an event gap is detected, request a fresh snapshot;
3. after IPC reconnect, discard assumptions and request a fresh snapshot;
4. optimistic UI may show an operation as submitted, but core state remains authoritative.

### 5.5 Command shape

Commands should carry a request id and, for state-sensitive mutations, an optional
expected revision.

Example:

```json
{
  "type": "command",
  "request_id": "...",
  "expected_revision": 183,
  "command": "connect_role",
  "role": "primary"
}
```

The UI shows the core state transition (`Starting`, `Recovering`, etc.) rather than
inventing a local connection state because the button was pressed.

## 6. Frontend state model

The Qt/C++ frontend should expose a small set of typed models to QML rather than a large
number of unrelated global context properties.

Recommended top-level object graph:

```text
AppModel
    |
    +-- CoreConnectionModel
    +-- CapabilityModel
    +-- UnderlayModel
    +-- RolesModel
    +-- ProfilesModel
    +-- LeshyModel
    +-- RoutingSafetyModel
    +-- SettingsModel
    +-- DiagnosticsModel
```

### 6.1 CoreConnectionModel

State:

```text
Disconnected
Connecting
Ready
ProtocolMismatch
PermissionDenied
CoreUnavailable
```

This is separate from VPN role state.

### 6.2 UnderlayModel

Expose operator-useful interpreted state only:

```text
available
interface name/display name
address family/path summary
underlay epoch
last material change
```

The UI does not need raw rtnetlink events.

### 6.3 RolesModel

A list model supports the existing two roles and future N-role core without redesigning
the view model.

Per-role fields should include at least:

```text
id
label
configured/enabled
state
driver/protocol
interface
server/endpoint display data
last error
last recovery reason
last validated underlay epoch
published to Leshy
parked destination count
available actions
```

Recommended role state vocabulary:

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

The exact core enum must be shared by API schema rather than translated independently in
QML.

### 6.4 LeshyModel

Expose:

```text
capability present
service/runtime state
routing ready/not ready
default zone
per-zone/role mapping
DNS integration state
last error
```

Windows stub reports the capability as absent rather than constructing fake Leshy state.

### 6.5 RoutingSafetyModel

This makes Kikimora-specific safety visible without exposing implementation clutter:

```text
endpoint policy ready/pending
parking active/count
leak-safety state
recovery transaction phase when relevant
```

This is particularly useful in diagnostics and in the role detail drawer.

## 7. Visual design system

### 7.1 Design intent

The first Kikimora desktop theme should deliberately resemble the restrained Amnezia
utility-app feel:

```text
near-black base
+ dark layered cards
+ pale primary text
+ muted secondary text
+ one warm accent
+ green/warning/red semantic accents
+ rounded geometry
+ short unobtrusive animation
```

Do not import Amnezia's theme file, icon files, logos or illustration assets. Implement
Kikimora-owned tokens and assets.

### 7.2 Theme tokens

Create a singleton QML theme object, for example `KikimoraTheme`.

Token groups:

```text
colors
spacing
radii
typography
control heights
opacity
animation durations
focus ring metrics
```

Suggested token hierarchy:

```text
background/base
surface/base
surface/hovered
surface/pressed
surface/elevated
text/primary
text/secondary
text/muted
border/soft
accent/primary
accent/success
accent/warning
accent/error
```

Freeze semantic names in code. Exact values can evolve without page rewrites.

### 7.3 Typography

Use one distributable font family with verified redistribution terms or use the system
font stack initially.

Required roles:

```text
Display
H1
H2
Body
BodyEmphasized
Caption
Button
MonospaceDiagnostics
```

Do not make pages hard-code font family/size repeatedly.

### 7.4 Geometry

Recommended first desktop target:

```text
default width:   about 440-480 px
default height:  about 720-780 px
minimum width:   about 380 px
```

This preserves the compact Amnezia-like utility shape while leaving enough vertical room
for two VPN role cards.

The layout must still support resizing and HiDPI rather than relying on one fixed pixel
canvas.

### 7.5 Motion

Default transition durations should be short, normally around 150-250 ms.

Use motion for:

- hover/focus state;
- navigation push/pop;
- drawer expansion;
- connecting/recovering progress;
- aggregate status changes.

Do not continuously animate healthy idle state.

Support reduced-motion behavior by disabling nonessential transitions when the platform
or user preference requests it.

## 8. Reusable QML control layer

Create a dedicated component module before product pages multiply.

Recommended components:

```text
KButton
KIconButton
KConnectControl
KCard
KRoleCard
KStatusChip
KBanner
KHeader
KBottomNav
KDrawer
KDialog
KSwitch
KTextField
KComboBox
KListRow
KSection
KProgressIndicator
KEmptyState
KErrorState
KTooltip
KDivider
```

Text primitives:

```text
KDisplayText
KHeadingText
KBodyText
KCaptionText
KButtonText
```

Controls own hover, pressed, disabled, focus and keyboard semantics. Product pages should
not recreate these mechanics.

## 9. Navigation architecture

Use one application window and one primary `StackView`-style navigation host.

Recommended bottom navigation:

```text
Home
Profiles
Routing
Settings
```

Diagnostics can live under Settings and be directly reachable from error/status banners.

Secondary screens use stack push/pop. Short contextual information uses a bottom drawer
or dialog.

Do not create multiple top-level windows for normal workflow.

## 10. Home/dashboard design

The home page is where Kikimora must intentionally diverge from single-VPN clients.

### 10.1 Header

Show:

```text
active profile
physical underlay summary
aggregate protection/routing status
```

The aggregate status is derived presentation, not an independent core state machine.

### 10.2 Role cards

Render one card per configured role from `RolesModel`.

For the current primary/secondary configuration each card shows:

```text
role label
protocol/driver
server/endpoint label
state text
compact circular state/connect control
last recovery/error hint when relevant
```

The circular control can reuse the visual idea of Amnezia's animated connection ring, but
there are separate controls/statuses for separate roles.

A role card must make these states visibly distinct:

```text
Stopped
Connecting
Ready
Waiting for network
Recovering
Failed
Unsupported
```

### 10.3 Global actions

A convenience `Connect all` / `Disconnect all` action may be provided, but it is an
orchestrator command that results in independent per-role state transitions.

The UI must never collapse two role failures into a false single `VPN connected` boolean.

### 10.4 Role detail drawer

Selecting a role opens a drawer with:

```text
runtime interface
transport endpoint
physical underlay path summary
last validated epoch
endpoint-underlay state
Leshy publication state
parked route count
last recovery action/reason
connect/disconnect/retry action when allowed
```

Raw route dumps remain diagnostics, not the normal home surface.

## 11. Profiles page

Profiles become a normal GUI feature rather than a shell-only concept.

Required operations:

```text
list profiles
show active profile
activate profile
create/edit/delete when API support exists
show role/driver/provider summary
validate before apply
```

Profile switching is asynchronous. The UI shows core-reported role transitions and does
not assume that selecting a profile means both roles are immediately Ready.

For the first UI milestone it is acceptable to provide read-only profile details plus
`activate`; editing can follow once the Go profile schema is stable.

## 12. Routing/Leshy page

Linux and macOS production builds expose a real routing page.

Show operator-level state:

```text
Leshy running/ready
DNS integration
role-to-zone mapping
default zone
endpoint-underlay readiness
parked destination summary
routing safety warnings
```

Avoid turning the main GUI into an `ip route` frontend.

Advanced/raw details belong in Diagnostics.

On Windows this page remains present so the information architecture is identical, but it
renders the capability-unavailable state from the stub core.

## 13. Settings page

Shared settings:

```text
launch UI at login
theme
language
notifications
close-to-tray behavior
confirm disconnect-all
log level where supported
```

Core-owned settings must be written through the core API. UI-only preferences may use
`QSettings`/platform-native user preferences.

Keep the distinction explicit:

```text
UI preference != networking configuration
```

## 14. Diagnostics

Diagnostics are essential for a multi-state orchestrator.

Required views:

```text
core/UI versions and protocol version
core connection state
capabilities
underlay snapshot/epoch
role state table
last recovery reason/action
endpoint desired/applied/pending summary
parking summary
Leshy state
recent structured logs
```

Actions:

```text
copy status
export debug bundle
open log folder where meaningful
refresh snapshot
```

Diagnostics must consume the same core API as the main UI. It must not bypass the API and
silently become a second networking inspector.

## 15. Tray/menu-bar integration

Desktop integration belongs behind a C++ interface such as:

```text
DesktopShell
    showMainWindow()
    hideMainWindow()
    notify(...)
    setAggregateState(...)
    setMenuModel(...)
    setLaunchAtLogin(...)
```

### Linux

Use Qt system tray support where the desktop environment supports it. The application
must still be fully usable when a tray implementation is absent.

### macOS

Use a native `NSStatusItem` adapter if Qt tray behavior proves unreliable or incomplete.
This follows the useful pattern seen in Amnezia without copying its implementation.

The menu should expose at least:

```text
Show Kikimora
aggregate status
Primary: state/action
Secondary: state/action
Connect all / Disconnect all
Quit
```

### Windows

Use the same abstract shell and QSystemTrayIcon/native adapter as appropriate. With the
stub backend, VPN actions are disabled and the status indicates that the networking core
is unavailable.

## 16. Window lifecycle

Closing the main window should normally hide it when tray/menu-bar mode is enabled.

`Quit` is an explicit action.

Do not automatically stop managed VPN roles merely because the GUI exits. Core lifetime
is independent from UI lifetime.

On next UI start:

```text
connect to core
handshake
fresh snapshot
render actual running state
```

This is required for a daemon-owned VPN architecture.

## 17. Platform abstraction boundary

Platform-specific code is allowed for desktop integration, but not as scattered page
conditions.

Recommended interfaces:

```text
DesktopShell
AutostartAdapter
NotificationAdapter
FileDialogAdapter
CredentialStore
PlatformInfo
```

Common QML talks to these through C++ abstractions.

Platform implementations:

```text
platform/linux/
platform/macos/
platform/windows/
```

The QML tree stays shared.

## 18. Repository layout target

A possible target layout:

```text
desktop/
├── CMakeLists.txt
├── src/
│   ├── main.cpp
│   ├── app/
│   ├── coreclient/
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
├── assets/
│   ├── icons/
│   └── branding/
└── tests/
    ├── unit/
    ├── qml/
    └── fakecore/
```

The Go daemon remains outside `desktop/`.

## 19. Backend adapters used by development and tests

The frontend uses the same abstract `CoreClient` with three concrete environments:

```text
RealCoreClient       production Linux/macOS
StubCoreClient       production Windows initially
FakeCoreServer       deterministic tests/developer scenarios
```

`FakeCoreServer` must be able to script states such as:

```text
both roles stopped
both roles ready
primary ready + secondary failed
underlay absent
underlay epoch changes
one role recovering while the other remains ready
parking active
endpoint policy pending
core restart/reconnect
protocol mismatch
```

This allows UI work to proceed before every Go driver exists and makes rare recovery
states reproducible.

## 20. Error semantics

Errors must be typed before they reach QML.

At minimum distinguish:

```text
CoreUnavailable
PermissionDenied
ProtocolMismatch
UnsupportedCapability
InvalidConfiguration
UnderlayUnavailable
DriverFailure
LeshyFailure
RoutingSafetyFailure
OperationRejected
```

QML chooses presentation text from typed error data. It should not parse daemon log
strings to decide application behavior.

## 21. Accessibility and desktop input

From the first reusable controls milestone:

- all interactive controls are keyboard reachable;
- focus is visibly indicated;
- Enter/Space activates expected controls;
- Escape closes drawer/dialog or navigates back where appropriate;
- tab order is deterministic;
- status is never color-only;
- scalable text does not clip critical controls;
- tooltip/accessible names exist for icon-only buttons;
- reduced motion is respected where available.

Do not bolt keyboard/focus support onto finished pages later.

## 22. Localization

All operator-visible strings go through Qt translation facilities from the beginning.

Do not embed English-only status names directly in core payloads. The core sends stable
enums/codes; the UI localizes them.

Initial requirement should include at least English and Russian resource flow even if one
translation is incomplete during early development.

## 23. Theme support

Implement the theme tokens so light mode is structurally possible even if the first
visual target is dark-first.

Recommended user setting:

```text
System
Dark
Light
```

Do not make page code branch on dark/light colors directly.

## 24. Packaging consequences

Because production Linux/macOS include a privileged/separately supervised core, packaging
must install more than a QML executable.

### Linux

Canonical package installs:

```text
kikimora-ui
kikimora-core
systemd service/socket metadata
desktop entry/icons
Leshy integration/configuration
```

An AppImage can be useful as a UI/fake-core developer artifact but should not be treated
as the complete production installer while a system daemon must be installed.

### macOS

Canonical distribution must include:

```text
Kikimora.app
privileged/launchd core component
required Leshy/runtime components
code signing
notarization
```

The installation design must support upgrading UI and core coherently.

### Windows

Initial package contains:

```text
kikimora-ui.exe
stub core/runtime
Qt runtime
```

No driver or fake VPN service is installed before the Windows networking backend is a
real roadmap item.

## 25. CI requirements

The UI repository line is cross-platform from the first skeleton commit.

Required build matrix:

```text
Linux x86_64
macOS arm64
macOS x86_64 or a documented universal-binary build
Windows x86_64
```

Required generic checks:

```text
C++ unit tests
Qt model/controller tests
QML tests
qml lint/static checks
fake-core contract tests
protocol schema compatibility tests
```

Linux and macOS production-core E2E tests are additional gates; Windows uses the stub
contract gate until a real core exists.

## 26. UI state acceptance matrix

Every release candidate should render and test at least:

```text
core unavailable
core reconnecting
protocol mismatch
underlay absent
underlay ready
all roles stopped
one role starting
one role ready / one stopped
one role recovering / other ready
one role failed / other ready
both roles ready
parking active during controlled recovery
endpoint underlay pending
Leshy failed
Windows capability-unavailable state
```

This matrix is more important than screenshot coverage of only the happy path.

## 27. Architectural invariants

The following are hard rules for the desktop UI line.

### UI is not a networking authority

No page calls NetworkManager, `ip`, `route`, `netstat`, `scutil`, `networksetup` or other
system networking tools to decide tunnel truth while a real core exists.

### Events are not reconnect commands

The frontend never responds to a platform network-change event by issuing VPN reconnect.
Underlay/recovery decisions belong to the Go reconciler.

### Multiple VPN roles remain visible as multiple states

Do not reduce the backend to one `isConnected` boolean.

### No VPN chaining in the UI model

Do not expose or suggest a role topology where one managed VPN uses another managed VPN as
its transport underlay.

### Leshy availability is capability-driven

Linux/macOS real builds expose real Leshy state. Windows stub reports Leshy unsupported.
The QML page tree remains shared.

### Parking remains a core invariant

The UI may visualize parking but never creates/removes parked routes itself.

### Core survives GUI exit

Closing or crashing the UI does not implicitly tear down healthy VPN roles.

### One common QML product

Do not fork `qml/linux`, `qml/macos` and `qml/windows` page trees. Platform divergence
belongs behind adapters and capabilities.

## 28. Completion criteria

The target desktop architecture is complete when:

1. one Qt/QML UI source tree builds on Linux, macOS and Windows;
2. Linux and macOS use the real Go core and real Leshy integration;
3. Windows uses the same control protocol against an explicit stub core;
4. the UI can reconnect to a restarted core and reconstruct state from a fresh snapshot;
5. primary and secondary VPN roles are independently represented and controllable;
6. underlay/recovery/parking state is observable without the UI becoming a networking
   authority;
7. profile activation and Leshy/routing status are available in the GUI;
8. tray/menu-bar behavior works on Linux and macOS;
9. reusable controls and theme tokens produce a consistent Amnezia-inspired but
   Kikimora-owned visual language;
10. keyboard, focus, HiDPI and localization paths are covered from the common component
    layer;
11. CI builds all three platforms on every UI change;
12. Linux and macOS pass real-core end-to-end UI smoke tests;
13. Windows passes the explicit unsupported/stub state contract;
14. no Amnezia branding or UI resource files are copied into Kikimora.
