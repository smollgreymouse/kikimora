# Kikimora desktop UI implementation plan

This is the detailed implementation plan for the desktop UI described in
[`desktop-ui-architecture.md`](desktop-ui-architecture.md).

The plan is intentionally coordinated with the Go multi-VPN migration in
[`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md) and the
project [`roadmap.md`](roadmap.md).

## 1. Non-negotiable platform policy

From the first buildable UI skeleton:

```text
Linux     = production target
macOS     = production target
Windows   = shared UI + production stub backend
```

Linux and macOS are developed in parallel. A milestone that claims real-core desktop
integration is incomplete until both Linux and macOS satisfy that milestone's acceptance
gate.

Windows is not allowed to disappear from CI while its networking backend is missing. It
must compile and run the same frontend, negotiate the same control protocol and present
explicit unsupported capabilities from a stub core.

Do not implement:

```text
Linux UI now
-> macOS rewrite later
-> Windows rewrite much later
```

The intended progression is:

```text
shared Qt/QML frontend from day one
        |
        +-- Linux real backend grows with Go core
        +-- macOS real backend grows with Go core
        +-- Windows stub continuously validates portability
```

## 2. Relationship to the Go roadmap

The UI line is parallel to the Go-control-plane line rather than a task after G10.

Dependency summary:

```text
G0 safety contracts
 |
 +---------------------------> U0 UI contract/design freeze

G1 Go daemon skeleton
 |                              U1 Qt skeleton/build matrix
 +----------------------------> U2 local control API v1
                                U3 fake/stub core

G2/G3 underlay + reconciler
 |                              U4 app shell/design system
 +----------------------------> U5 dashboard state model

G4 endpoint ownership
G5 parking/publication
 |                              U6 role details/routing safety
 +----------------------------> U7 profiles + Leshy/routing pages

G6 first real driver
G7 controlled recovery
 |                              U8 Linux+macOS real-core E2E
 +----------------------------> U9 tray/notifications/recovery UX

G8 all roles Go-owned
G9 legacy watcher retired
 |                              U10 production packaging
 +----------------------------> U11 release acceptance

G10 diagnostics API ----------> U8/U9 diagnostics surface finalization
```

The UI must be usable against FakeCore before the first production VPN driver exists.
This keeps visual/product work off the critical path of networking implementation.

## 3. Source-tree decision

Add a top-level desktop tree rather than mixing Qt frontend code into the Go core.

Target layout:

```text
desktop/
├── CMakeLists.txt
├── cmake/
│   ├── Linux.cmake
│   ├── MacOS.cmake
│   └── Windows.cmake
├── src/
│   ├── main.cpp
│   ├── app/
│   │   ├── Application.*
│   │   └── UiSettings.*
│   ├── coreclient/
│   │   ├── CoreClient.*
│   │   ├── CoreProtocol.*
│   │   ├── FramedJsonTransport.*
│   │   ├── RealCoreClient.*
│   │   └── StubCoreClient.*
│   ├── models/
│   │   ├── AppModel.*
│   │   ├── CapabilityModel.*
│   │   ├── CoreConnectionModel.*
│   │   ├── UnderlayModel.*
│   │   ├── RolesModel.*
│   │   ├── ProfilesModel.*
│   │   ├── LeshyModel.*
│   │   ├── RoutingSafetyModel.*
│   │   └── DiagnosticsModel.*
│   ├── controllers/
│   │   ├── NavigationController.*
│   │   ├── RoleActionsController.*
│   │   ├── ProfileActionsController.*
│   │   └── DiagnosticsController.*
│   └── platform/
│       ├── DesktopShell.h
│       ├── linux/
│       ├── macos/
│       └── windows/
├── qml/
│   ├── Main.qml
│   ├── Theme/
│   ├── Controls/
│   ├── Pages/
│   ├── Drawers/
│   └── Dialogs/
├── assets/
│   ├── icons/
│   └── branding/
└── tests/
    ├── unit/
    ├── qml/
    ├── protocol/
    └── fakecore/
```

Do not make the QML directory platform-specific.

## 4. U0 - UI architecture and contract freeze

### Goal

Turn the desired Amnezia-like desktop experience and Kikimora-specific multi-VPN
semantics into explicit contracts before page implementation starts.

### Deliverables

1. `desktop-ui-architecture.md` accepted as target architecture.
2. Screen/state inventory frozen for MVP.
3. UI/core ownership boundary frozen.
4. Platform matrix frozen.
5. First control API message schema drafted.
6. Design-token names frozen, exact color values still adjustable.
7. Required test-state matrix frozen.

### Required MVP screens

```text
Home
Profiles
Routing
Settings
Diagnostics
```

### Required contextual surfaces

```text
role detail drawer
core unavailable banner/screen
protocol mismatch screen
confirmation dialog
generic error dialog
about/version dialog
```

### State inventory

Core connection:

```text
Disconnected
Connecting
Ready
CoreUnavailable
PermissionDenied
ProtocolMismatch
```

Underlay:

```text
Unavailable
Ready
Changing/settling only if core exposes it
```

VPN role:

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

Leshy/routing:

```text
Unsupported
Stopped
Starting
Ready
Degraded
Failed
```

### Exit gate

A UI developer can implement every MVP screen against fake data without asking what
component owns networking truth or what Windows should pretend to support.

## 5. U1 - Qt 6 desktop skeleton and three-OS CI

### Goal

Produce the smallest real application that builds and launches on Linux, macOS and
Windows before product UI is written.

### Build work

Create a Qt 6 CMake project with at least:

```text
Qt6::Core
Qt6::Gui
Qt6::Quick
Qt6::QuickControls2
Qt6::Svg
Qt6::Network
Qt6::Test
```

Add Widgets only if tray/native menu integration actually requires it.

Use:

```text
qt_standard_project_setup()
qt_add_executable(...)
qt_add_qml_module(...)
```

### Application bootstrap

`main.cpp` should do only application/bootstrap work:

```text
create QGuiApplication/QApplication as required
set organization/application names
register frontend types
load fonts/resources
construct AppModel/CoreClient/DesktopShell
expose one small root frontend object to QML
load Main.qml
```

Avoid an Amnezia-style proliferation of unrelated global QML context properties. Prefer
one root object with typed submodels.

### Initial QML

`Main.qml` initially renders:

```text
Kikimora
UI version
platform
backend: fake/stub/disconnected
```

No networking control yet.

### CI jobs

Create jobs for:

```text
Linux x86_64 build + tests
macOS arm64 build + tests
macOS x86_64/universal strategy validation
Windows x86_64 build + tests
```

Minimum checks:

```text
cmake configure
cmake build
ctest
QML import validation/qmllint
headless application launch smoke test where platform permits
```

### Artifact policy

At this stage CI may publish raw/dev artifacts only. Do not spend time on signed
production packaging before the architecture is proven.

### Exit gate

The same `Main.qml` and common C++ frontend build on all three desktop OSes with no
platform-QML forks.

## 6. U2 - local control protocol v1

### Goal

Create the stable boundary between Qt UI and Go core before the GUI depends on internal
Go structs.

### Protocol package in Go

Create a protocol package that contains transport-neutral request/response/event DTOs.
It must not import Linux-specific routing packages.

Conceptual message envelope:

```json
{
  "protocol": 1,
  "type": "...",
  "request_id": "...",
  "revision": 123,
  "payload": {}
}
```

### Required request types

MVP read operations:

```text
hello
get_snapshot
subscribe
get_profiles
get_diagnostics
get_recent_logs
```

MVP commands:

```text
connect_role
disconnect_role
connect_all
disconnect_all
activate_profile
rediscover_endpoints
export_debug_bundle
```

Only add profile editing commands once the Go profile schema is ready.

### Required hello response

```text
protocol selected
core version
platform
backend kind real/stub/fake
capabilities
```

### Required snapshot sections

```text
core
underlay
roles
profiles
leshy
routing_safety
settings/capabilities relevant to UI
```

### Revision rules

- snapshot carries revision `N`;
- every state event carries a monotonically increasing revision;
- UI ignores stale revisions;
- revision gaps force resnapshot;
- reconnect forces resnapshot;
- command result does not replace state event as source of truth.

### Framing

Implement and test:

```text
uint32 BE length
JSON bytes
```

Reject:

```text
zero/invalid frames
frames over configured maximum
invalid UTF-8/JSON
unknown required protocol version
```

Unknown additive fields are ignored.

### Endpoint selection

Linux target:

```text
/run/kikimora/control.sock
```

or another documented runtime socket owned by the service package.

macOS target:

```text
launchd-owned local Unix socket or equivalent private local endpoint
```

Windows stub:

```text
named pipe or in-process StubCoreClient during first skeleton,
with named-pipe transport required before Windows real backend work
```

If Windows initially uses an in-process production stub, its DTO/capability behavior must
be identical to the wire protocol and covered by the same contract tests.

### Security tests

Linux/macOS real core tests must include:

```text
unauthorized peer rejected
malformed frame rejected
unknown command rejected
UI cannot request arbitrary privileged shell execution
```

### CLI relationship

Do not delete `kk ... --json`.

Refactor semantically so CLI JSON and control API use the same canonical core status
types where possible. The GUI must not spawn `kk` as its normal transport.

### Exit gate

A tiny test client can handshake, fetch a snapshot and receive ordered events from the Go
skeleton on Linux and macOS; Windows returns a valid stub handshake.

## 7. U3 - FakeCore and production Windows StubCore

### Goal

Make UI development deterministic and keep Windows honest without networking support.

### FakeCore

Create a developer/test server that implements the complete protocol and can load named
scenarios.

Required scenarios:

```text
all-stopped
all-ready
primary-ready-secondary-stopped
primary-ready-secondary-failed
underlay-down
underlay-change
primary-recovering-secondary-ready
parking-active
endpoint-pending
leshy-failed
core-restart
protocol-mismatch
```

Scenario scripts should support delayed transitions, for example:

```text
Stopped -> Starting -> Connecting -> Ready
Ready -> Recovering -> Ready
Ready -> Recovering -> Failed
```

### StubCore

Windows production stub is intentionally boring:

```text
hello works
snapshot works
capabilities say VPN/Leshy/routing unsupported
settings/UI diagnostics work where meaningful
network mutation commands return UnsupportedCapability
```

Do not reuse FakeCore as production StubCore. A demo that claims `Ready` would hide the
absence of the real Windows networking implementation.

### Shared tests

Every protocol field consumed by Qt should be tested against:

```text
Go real/skeleton encoder
FakeCore encoder
Windows StubCore encoder
Qt decoder/model mapping
```

### Exit gate

UI developers can reproduce every important role/recovery state without touching a real
VPN, and the Windows production build cannot falsely display a working tunnel.

## 8. U4 - Kikimora design system

### Goal

Build the visual vocabulary before implementing pages.

### Theme module

Create `KikimoraTheme` with semantic tokens.

Initial dark theme target:

```text
background       near-black
surface          slightly lighter near-black
surface-hover    subtle light overlay
primary text     almost white
secondary text   cool gray
accent           warm amber/apricot family
success          green
warning          yellow/amber
error            red
```

The desired feel should be recognizably close to Amnezia's dark restrained UI without
copying its resource definitions.

### Token groups

```text
Color
Spacing
Radius
Typography
Animation
Focus
ControlSize
```

### First controls

Implement and test in isolation:

```text
KButton
KIconButton
KCard
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
KProgressIndicator
KDivider
KTooltip
```

Then:

```text
KConnectControl
KRoleCard
```

### KConnectControl

Visual behavior inspired by Amnezia's circular control:

```text
Stopped       neutral ring
Starting      animated progress arc
Connecting    animated progress arc
Ready         warm accent ring
Recovering    warning/progress treatment
Failed        error treatment
Unsupported   muted/disabled treatment
```

It must always include text/accessible state; color is not the only signal.

### Component gallery

Create a developer-only page or test harness that renders every control in:

```text
normal
hover
pressed
focus
disabled
error
light/dark if both available
```

### Assets

Create Kikimora-owned SVG icons or use a compatible licensed icon set.

Do not copy Amnezia icon files.

### Typography

Choose and document one redistributable font or use platform/system UI font until a
bundled font license is explicitly approved.

### Exit gate

Product pages can be assembled without inventing new button/card/text styling locally.

## 9. U5 - application shell and navigation

### Goal

Implement the full window lifecycle before networking screens become complex.

### Main window

Target compact desktop geometry:

```text
initial width ~460
initial height ~760
minimum width ~380
```

Persist window geometry carefully, clamped to the currently available screen so moving
between monitors cannot strand the window off-screen.

### Navigation

Use one primary stack and bottom navigation:

```text
Home
Profiles
Routing
Settings
```

Diagnostics is a settings subpage and may also be deep-linked from banners/errors.

### Navigation controller

Responsibilities:

```text
push page
pop page
go to root tab
open role drawer
open dialog
handle Escape/back semantics
```

QML page files should not manually know paths to unrelated pages.

### Core connection overlay/banner

If the core is unavailable:

- the app shell still opens;
- UI preferences/settings remain available;
- network controls are disabled;
- a clear reconnect/error surface appears;
- reconnection uses bounded backoff;
- after reconnect the app requests a fresh snapshot.

### Protocol mismatch

Show a blocking compatibility screen with UI/core versions and upgrade guidance.
Do not try to continue with partially understood state.

### Exit gate

The complete navigation shell works against FakeCore on all three OSes.

## 10. U6 - Home/dashboard and multi-VPN semantics

### Goal

Make the normal multi-VPN state understandable at a glance.

### Header

Show:

```text
Kikimora/app identity
active profile
physical underlay state
aggregate routing/protection summary
```

### Role list

Bind directly to `RolesModel`, not hard-coded QML ids for exactly two tunnels.

The current product will normally render:

```text
Primary
Secondary
```

but the delegate model should tolerate future N-role core state.

### Role card layout

Suggested structure:

```text
+------------------------------------+
| Primary                  READY     |
| AmneziaWG / server label            |
|                                    |
|           (connect ring)           |
|                                    |
| via Wi-Fi / wlan0                  |
| details >                          |
+------------------------------------+
```

Use a smaller circular affordance than a single-VPN app so two cards fit naturally.

### Action semantics

Per-role connect/disconnect action:

```text
button click
 -> send command
 -> mark command pending only
 -> core emits actual state
 -> card updates from actual state
```

Do not locally set `Ready` after command success.

### Global action

Provide optional:

```text
Connect all
Disconnect all
```

The resulting UI remains per-role. For example:

```text
Primary   Ready
Secondary Failed
```

must never become a single green `Connected` state.

### Underlay unavailable

When the physical underlay is absent:

```text
header warning
roles show WaitingForUnderlay when core reports it
connect actions reflect core availability
no UI-triggered restart loop
```

### Recovery

When one role is recovering:

```text
that role animates recovery
other healthy role remains visually Ready and usable
```

This is an explicit acceptance test for per-role isolation.

### Exit gate

Every role-state combination in the FakeCore matrix renders correctly, including one
healthy role beside one failed/recovering role.

## 11. U7 - role detail and routing-safety surfaces

### Goal

Expose enough Kikimora-specific state to explain recovery without overwhelming Home.

### Role detail drawer

Fields:

```text
role
state
driver/protocol
runtime interface
server/endpoint label
underlay interface/path
last validated underlay epoch
last state change
last recovery reason
last recovery action
Leshy publication state
endpoint policy state
parked destination count
last error
```

### Safety display

Use concise status chips:

```text
Endpoint path   Protected / Pending / Unavailable
Leshy route     Published / Withdrawn
Parking         Inactive / N destinations parked
```

### Controlled recovery visualization

If core exposes transaction phase, map it to operator language rather than implementation
calls:

```text
Protecting routes
Rebinding transport
Starting tunnel
Validating
Restoring routes
```

Never imply the UI is performing those steps.

### Developer details

Raw generation numbers, exact endpoint IPs and internal phase identifiers may live behind
an `Advanced` expander or Diagnostics.

### Exit gate

A user can answer `why is this role not Ready?` from the drawer without reading journal
logs for normal failures.

## 12. U8 - Profiles page

### Goal

Bring the existing Kikimora profile concept into the GUI without coupling QML to shell
configuration files.

### First production scope

Required:

```text
list profiles
active marker
per-profile primary/secondary summary
endpoint provider summary
activate profile
```

Optional/deferred until Go profile schema is stable:

```text
create
edit
duplicate
delete
import/export
```

### Activation flow

```text
select profile
 -> confirmation if disruptive
 -> activate_profile command
 -> profile command pending
 -> core changes desired state
 -> role cards independently transition
 -> active profile updates from core snapshot/event
```

No QML file edits `vpn.conf` or profile files directly.

### Validation

Display typed core validation failures next to the relevant role/provider field.

### Windows

The page can list UI/sample configuration only if the stub has real persisted profile
metadata. It must not offer `Activate` as if networking were available when
`vpn_control=false`.

### Exit gate

Profile activation works against FakeCore and Linux/macOS real core schema without UI
knowledge of profile storage format.

## 13. U9 - Routing/Leshy page

### Goal

Make Leshy orchestration visible as a product feature on Linux and macOS.

### Main surface

Show:

```text
Leshy state
DNS integration state
default zone
role/zone mapping
endpoint-underlay aggregate status
parking aggregate status
```

### Warnings

Examples:

```text
Leshy unavailable
DNS integration degraded
one role unpublished
endpoint policy pending
parked destinations retained after failed recovery
```

### Actions

Only expose actions that have a clear high-level core API:

```text
retry/repair DNS integration
rediscover endpoints
open diagnostics
```

Do not expose raw `ip rule add`, `resolvectl`, route-table edits or arbitrary service
restart buttons as the normal UI API.

### Windows

Render one stable capability-unavailable state:

```text
Routing orchestration is not available in the Windows preview backend.
```

Do not hide the tab conditionally; keeping the shared information architecture exercises
cross-platform layout and makes backend capability differences explicit.

### Exit gate

Linux and macOS can both report real Leshy/routing state through the same frontend model;
Windows renders the stub state from capabilities.

## 14. U10 - Settings and desktop integration

### Goal

Complete ordinary desktop-app behavior without polluting networking models.

### UI preferences

Store in UI/user preferences:

```text
theme
language
close to tray
notifications
confirm disconnect-all
window geometry
last selected top-level page if desired
```

### Core settings

Store via core API only:

```text
core logging level
VPN/recovery settings if later exposed
networking/profile settings
```

### Linux desktop shell

Implement:

```text
show/hide window
tray if available
notifications
autostart UI at login
open config/log locations where allowed
```

The daemon remains systemd-managed independently from UI login startup.

### macOS desktop shell

Implement:

```text
native menu-bar/status item
show/hide app
notifications
login-item behavior
Dock/window activation semantics
```

Use a native Objective-C++ adapter for `NSStatusItem` if needed rather than embedding
macOS-specific logic in QML.

### Windows shell

Implement the same frontend interface using normal Windows tray/window integration.
Networking actions remain capability-disabled.

### Tray menu model

Do not hard-code the tray to a single VPN connection.

Desired content:

```text
Show Kikimora
----------------
Primary      Ready
Secondary    Recovering
----------------
Connect all / Disconnect all
Settings
Quit UI
```

If per-role direct actions fit platform conventions, include them below each role.

### Quit semantics

`Quit UI` exits the UI process only.

If a future `Stop Kikimora` action is added, make it a distinct explicit core command
with confirmation. Never conflate window close with VPN teardown.

### Exit gate

Linux and macOS both provide polished tray/menu behavior and UI autostart without changing
core tunnel lifetime; Windows shell works with stub state.

## 15. U11 - Diagnostics and supportability

### Goal

Make the architecture debuggable without requiring normal users to run shell commands.

### Diagnostics model

Required data:

```text
UI version/build
core version/build
control protocol
backend kind
capabilities
core connection state
underlay epoch/path
per-role state/driver/interface
last recovery reason/action
endpoint policy desired/applied/pending summary
Leshy publication
parking counts
Leshy/DNS state
recent structured logs
```

### Debug bundle

UI calls one high-level API:

```text
export_debug_bundle
```

Core owns gathering privileged/runtime diagnostics.

The bundle contract should support redaction policy. Secrets/private keys/passwords must
not enter the bundle by default.

### Logs

Display a bounded log snapshot with:

```text
time
component
role
severity
message
```

Do not parse arbitrary human journal formatting if structured core logs are available.

### Recovery audit trail

For each role expose the most recent causal sequence conceptually:

```text
underlay epoch 41 -> 42
reason: gateway/source changed
transport validation failed
selected action: transport restart
parking: 27 destinations
result: Ready
```

This makes the new reconciler understandable and directly supports the architecture's
`events are not commands` rule.

### Exit gate

A support/debug report can explain current state and last recovery without manually
inspecting runtime files.

## 16. U12 - Linux and macOS real-core integration gate

### Goal

Replace FakeCore as the normal backend on **both** production platforms.

This stage runs in parallel on Linux and macOS. It is not two sequential roadmap stages.

### Linux track

Verify:

```text
UI connects to system kikimora-core
peer authorization works
snapshot/event stream works across daemon restart
real role connect/disconnect works
real profile activation works
underlay handoff reflected without frontend polling
parking/recovery state visible
Leshy/routing state visible
tray survives core restart
```

### macOS track

Verify the same product-level behavior:

```text
UI connects to launchd/privileged core
peer authorization works
real role connect/disconnect works
real profile activation works
underlay transitions reflected
parking/routing safety equivalent visible
real Leshy integration visible
menu-bar integration survives core restart
```

If a Linux-specific core operation has no macOS implementation yet, this stage remains
open. Do not declare desktop production ready with a macOS fake/stub backend.

### Cross-platform parity test

Given equivalent FakeCore/real-core logical states, the frontend model must expose the
same enum/field semantics independent of OS.

### Exit gate

All normal Home/Profiles/Routing/Diagnostics flows use real core data on both Linux and
macOS.

## 17. U13 - recovery UX validation

### Goal

Validate the UI specifically against the physical-underlay/recovery behavior that
motivated the new Go architecture.

### Required live scenarios

Linux and macOS must both test:

```text
Wi-Fi -> Ethernet
Ethernet -> Wi-Fi
source-address change
physical gateway change
physical network loss and return
suspend/resume
core restart while tunnels remain/are reconstructed as designed
one role recovery while another remains Ready
failed recovery with parking retained
```

### Noise scenarios

Where platform-applicable:

```text
connectivity classification changes without material path change
unrelated virtual interface churn
route events produced by managed TUN lifecycle
```

Expected frontend result:

- no spurious global reconnect animation when core performs no recovery;
- only affected role changes state;
- UI follows core revision stream;
- no local network listener issues reconnect commands.

### Resume behavior

On app resume/window activation the UI may ensure IPC is alive and resnapshot after a
lost connection. It must not infer that OS resume means VPN restart.

### Exit gate

UI remains a passive/command frontend while accurately rendering all G7 recovery paths.

## 18. U14 - production packaging

### Goal

Ship coherent UI + core packages on Linux/macOS and a truthful UI + stub package on
Windows.

### Linux packaging

Package should install:

```text
kikimora-ui
kikimora-core
systemd unit/socket
Leshy/runtime integration
desktop entry
icons/translations
policies/groups/socket permissions
```

Post-install validation:

```text
core can start
UI user can authenticate to control API
UI can fetch snapshot
no root launch of GUI required
```

### macOS packaging

Package/app distribution must include and validate:

```text
Kikimora.app
privileged/launchd core component
Leshy/runtime support
code signing identities
entitlements where required
notarization
upgrade path
uninstall path
```

Signing/notarization should enter CI before release candidate, not after UI completion.

### Windows packaging

Initial package:

```text
Kikimora UI
Qt runtime
StubCore
translations/assets
```

Installer text/release notes must not imply that Windows VPN control is implemented.

### Version compatibility

Installer/update policy must define supported UI/core protocol ranges so partial upgrades
do not yield undefined behavior.

### Exit gate

Clean machines on all three OSes can install and launch the intended platform behavior;
Linux/macOS reach real core, Windows reaches explicit stub.

## 19. U15 - test strategy

### C++ unit tests

Test:

```text
JSON framing
protocol decoding
revision ordering
snapshot replacement
reconnect behavior
capability mapping
role-state mapping
command pending/result handling
error-code mapping
```

### QML/component tests

Test controls for:

```text
focus
keyboard activation
disabled state
hover/pressed
long translated text
high DPI
narrow minimum width
```

### Model/view tests

Assert key combinations:

```text
Ready + Ready
Ready + Failed
Ready + Recovering
WaitingForUnderlay + WaitingForUnderlay
Unsupported Windows backend
Leshy failed while roles remain connected
parking active during recovery
```

### FakeCore E2E

Run deterministic scenarios on Linux/macOS/Windows CI.

### Real-core E2E

Linux/macOS only initially:

```text
launch core
launch UI test harness
handshake
connect role
observe state revisions
trigger controlled state transition
verify QML-facing model
```

Tests do not need to click pixels for every state; most behavior should be testable at
frontend model/controller boundaries.

### Screenshot regression

Use screenshots selectively for stable core surfaces:

```text
Home
role drawer
Routing
Windows unsupported state
error/protocol mismatch
```

Keep screenshot tests on a canonical rendering environment to avoid font/rasterization
noise across all OSes.

### Exit gate

UI behavior is primarily protected by model/protocol tests; screenshot tests protect the
visual contract rather than replacing behavior tests.

## 20. U16 - accessibility, localization and polish gate

### Keyboard

Required end-to-end paths without mouse:

```text
switch tabs
select role
connect/disconnect role
open/close drawer
select/activate profile
open diagnostics
copy/export status
close dialogs
```

### Focus

Every custom control has a visible focus ring consistent with the theme.

### Localization

At least English and Russian catalogs are wired into CI extraction/update flow.

Test layouts with deliberately long strings; do not assume English label widths.

### Accessibility

Icon-only buttons have accessible names.
Status is text + color/icon.
Reduced motion is honored where practical.

### HiDPI

Verify at representative scale factors on both Linux and macOS.

### Exit gate

The desktop application is usable as a real desktop tool rather than merely a visual
prototype.

## 21. Core API data contract details

The exact schema will evolve, but the UI plan requires these concepts from G1-G10.

### Role DTO

Conceptual fields:

```json
{
  "id": "primary",
  "label": "Primary",
  "enabled": true,
  "state": "ready",
  "driver": "amneziawg",
  "interface": "amn0",
  "endpoint": { "label": "..." },
  "underlay_epoch": 42,
  "published": true,
  "parked_routes": 0,
  "recovery": {
    "reason": null,
    "action": null
  },
  "actions": {
    "connect": false,
    "disconnect": true,
    "retry": false
  }
}
```

### Underlay DTO

```json
{
  "available": true,
  "epoch": 42,
  "ipv4": {
    "interface": "wlan0",
    "gateway": "192.0.2.1",
    "source": "192.0.2.20"
  },
  "last_change_reason": "source_changed"
}
```

The UI does not compare these fields to decide whether an underlay change is material;
that decision already happened in core.

### Routing safety DTO

```json
{
  "endpoint_underlay": "ready",
  "parking": {
    "active": false,
    "count": 0
  },
  "fail_closed": true
}
```

### Capabilities DTO

Capabilities are feature-oriented, not OS-name-oriented:

```json
{
  "vpn_control": true,
  "leshy": true,
  "routing_policy": true,
  "endpoint_underlay": true,
  "profiles_write": false,
  "logs": true,
  "debug_bundle": true
}
```

This allows future Windows implementation to turn capabilities on without rewriting QML
platform branches.

## 22. UI action policy

Every button action must fall into one of three categories.

### Local UI action

Examples:

```text
change theme
navigate
copy text
resize window
```

Handled entirely by frontend.

### Core command

Examples:

```text
connect role
disconnect role
activate profile
rediscover endpoints
repair DNS
```

Sent through control API.

### Unsupported capability

The control is disabled or replaced by explanatory presentation based on capability data.

Do not implement a fourth category where QML invokes shell commands.

## 23. Command concurrency and UX

The frontend should prevent accidental duplicate clicks but not create a second hidden
state machine.

For each command:

```text
request id
command pending indicator
command result/error
actual core state remains authoritative
```

If a command result is lost because IPC reconnects:

```text
reconnect
fresh snapshot
render truth
```

Do not retry mutating commands blindly unless the API explicitly defines them as
idempotent and supplies an idempotency key.

## 24. Core restart behavior

Required sequence:

```text
UI Ready
core disappears
 -> CoreConnectionModel = CoreUnavailable/Reconnecting
 -> freeze/disable core mutations
 -> keep last state visually marked stale, or clear according to approved UX
core returns
 -> handshake
 -> capability validation
 -> GetSnapshot
 -> atomically replace frontend models
 -> resume event subscription
```

Do not replay assumed role state from QSettings.

## 25. Window/tray aggregate-state algorithm

Tray icon/color may summarize the system, but the algorithm must be deterministic and
presentation-only.

Suggested precedence:

```text
core unavailable/protocol error
    > routing safety failure
    > any role Failed
    > any role Recovering/Starting/Connecting
    > expected enabled roles all Ready and Leshy healthy
    > partial/stopped
```

Clicking the tray opens the app where per-role truth remains visible.

The aggregate state must not be sent back into core as networking policy.

## 26. Visual acceptance targets

The desired visual family should preserve these traits:

```text
compact tall window
very dark base
minimal chrome
rounded 14-18 px cards
subtle 1 px borders/focus rings
warm accent used sparingly
large whitespace/spacing between groups
status chips instead of dense tables on Home
animated circular progress only for active transitions
bottom navigation with simple line icons
bottom drawer for role detail
```

Avoid:

```text
classic admin-dashboard sidebar at 1000+ px width
raw routing tables on Home
bright gradients everywhere
one giant global VPN button hiding role state
platform-native widgets mixed arbitrarily with custom dark controls
```

## 27. Proposed screen wireframes

### Home

```text
+--------------------------------------+
| Kikimora              profile: home |
| Wi-Fi wlan0                protected |
|                                      |
| +----------------------------------+ |
| | PRIMARY                    READY | |
| | AmneziaWG / NL-1                 | |
| |          ( O )                    | |
| | via wlan0              Details > | |
| +----------------------------------+ |
|                                      |
| +----------------------------------+ |
| | SECONDARY              RECOVERING| |
| | Xray / DE-2                     | |
| |          ( ↻ )                    | |
| | routes parked: 14      Details > | |
| +----------------------------------+ |
|                                      |
| [ Disconnect all ]                  |
|                                      |
| Home   Profiles   Routing  Settings |
+--------------------------------------+
```

### Role drawer

```text
+--------------------------------------+
| Secondary                            |
| RECOVERING                           |
|                                      |
| Transport       Xray                 |
| Interface       tun0                 |
| Underlay        wlan0                |
| Epoch           42                   |
| Endpoint path   Protected            |
| Leshy           Withdrawn            |
| Parking         14 destinations      |
| Recovery        restarting transport |
|                                      |
| [ Cancel/Disconnect if supported ]   |
+--------------------------------------+
```

### Windows Routing page

```text
+--------------------------------------+
| Routing                              |
|                                      |
|      Routing backend unavailable     |
|                                      |
| Leshy and VPN routing are not yet    |
| implemented in the Windows backend.  |
|                                      |
| UI build: ...                        |
| Core: Windows stub                   |
+--------------------------------------+
```

## 28. Milestone grouping for actual work

To keep implementation reviewable, group work into release-sized milestones.

### M1 - Portable shell

Contains:

```text
U0
U1
U3 basic fake/stub
U4 basic theme/controls
U5 navigation shell
```

Result: same polished empty shell runs on all three OSes.

### M2 - Live state frontend

Contains:

```text
U2 protocol
U3 complete FakeCore
U6 Home
U7 role details
```

Result: complete multi-VPN UX works against deterministic backend on all OSes.

### M3 - Operator surfaces

Contains:

```text
U8 Profiles
U9 Routing/Leshy
U11 Diagnostics
```

Result: all required product surfaces exist before real driver cutover.

### M4 - Real Linux + macOS

Contains:

```text
U10 desktop integration
U12 real-core integration
U13 recovery validation
```

Result: Linux and macOS are both genuine Kikimora clients, not frontend demos.

### M5 - Distribution

Contains:

```text
U14 packaging
U15 full tests
U16 accessibility/localization/polish
```

Result: shippable desktop line with Windows explicitly in stub-preview state.

## 29. Review gates per pull request

Every UI PR should answer:

```text
Does it compile on Linux/macOS/Windows?
Does it add platform-specific QML?
Does it introduce a new core truth outside CoreClient?
Does it parse human CLI/log output for behavior?
Does it preserve role isolation?
Does Windows truthfully report unsupported capabilities?
Are keyboard/focus states covered for new controls?
Are new strings translatable?
Does a new core command have protocol/contract tests?
```

A PR that introduces `#ifdef`/`Qt.platform.os` into product business-state logic should be
rejected unless it is genuinely presentation/platform-shell behavior.

## 30. Final acceptance criteria

The desktop UI line is complete for the first Linux/macOS release when:

1. Linux and macOS run the same Qt/QML product against real Kikimora Go core instances.
2. Windows builds the same frontend and clearly uses a stub backend.
3. the UI contains no networking authority independent from core;
4. core restart/reconnect is handled by handshake + resnapshot;
5. primary and secondary roles remain independently visible and actionable;
6. physical-underlay loss/recovery is rendered from core state without frontend restart
   policy;
7. parking, endpoint safety and Leshy publication can be inspected from role details or
   Diagnostics;
8. profiles can be selected from the GUI;
9. Linux and macOS expose real Leshy/routing state;
10. Windows exposes Leshy/routing as unsupported rather than fake;
11. tray/menu-bar integration is multi-role aware;
12. UI exit does not stop daemon-owned VPNs;
13. all QML pages share one design system and one page tree;
14. three-OS build CI is mandatory;
15. Linux/macOS real-core E2E tests cover connect, disconnect, recovery and core restart;
16. accessibility, keyboard, HiDPI and localization gates pass;
17. signed/notarized macOS and production Linux packaging are validated;
18. no Amnezia brand assets or source UI resources have been copied.
