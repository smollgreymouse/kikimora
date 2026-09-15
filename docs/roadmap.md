# Kikimora roadmap

This file is the project roadmap for the next architecture transition. Current runtime
behavior remains documented in the existing focused documents; future design is described
in separate target/plan documents so current and planned behavior are not mixed.

Go multi-VPN target architecture:
[`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md)

Go multi-VPN implementation plan:
[`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md)

Desktop UI target architecture:
[`desktop-ui-architecture.md`](desktop-ui-architecture.md)

Desktop UI implementation plan:
[`desktop-ui-implementation-plan.md`](desktop-ui-implementation-plan.md)

## Current baseline

The current shell/systemd implementation already provides several safety primitives that
must survive the migration rather than be redesigned from scratch.

### Implemented baseline

- Leshy domain-aware routing with independent primary and secondary VPN roles.
- Role publication through runtime `.dev` files after structural readiness stabilization.
- VPN endpoint policy in table `51890` that forces managed VPN transport endpoints through
  the physical underlay rather than either managed VPN.
- Role-specific endpoint priorities and independent transitions.
- Safe `underlay-pending` behavior when an externally owned live VPN cannot safely have its
  endpoint path rewritten.
- `dynamic-additive` endpoint-provider semantics for proven live rotating transports.
- Fail-closed parking of Leshy-owned IPv4 host routes when a managed VPN role disappears.
- Parking retained until a real lower-metric replacement route is observed.
- Cached route ownership for hard TUN disappearance.
- Separate Leshy/DNS health and resolver repair.
- A versioned CLI JSON surface that already establishes the rule that machine consumers
  do not scrape human CLI output.

Reference documents:

- [`vpn-endpoints.md`](vpn-endpoints.md)
- [`endpoint-providers.md`](endpoint-providers.md)
- [`vpn-readiness.md`](vpn-readiness.md)
- [`route-parking.md`](route-parking.md)
- [`orchestration.md`](orchestration.md)
- [`profiles.md`](profiles.md)
- [`cli-json-api.md`](cli-json-api.md)

## Target direction

Kikimora becomes a Go multi-VPN client and control plane instead of supervising interfaces
created by unrelated VPN clients.

The fundamental topology is fixed:

```text
                     physical underlay
                    /        |         \
                   /         |          \
               VPN A      VPN B       VPN C
                   \         |          /
                    \        |         /
                         Leshy
                           |
                   user traffic policy
```

Every managed VPN transport goes directly through the physical underlay. VPN-over-VPN
chains and tunnel dependency graphs are outside the architecture.

Systemd/launchd remain process supervisors. Physical-network detection, VPN recovery,
endpoint routing and per-role state machines move into the Go core.

A new Qt 6 / Qt Quick desktop application becomes the operator-facing client:

```text
Linux                         macOS                         Windows initially
------                        -----                         -----------------
Qt/QML UI                     Qt/QML UI                     same Qt/QML UI
   |                             |                             |
real Go core                  real Go core                  stub Go/core API
   |                             |                             |
real VPN + Leshy              real VPN + Leshy              capabilities=unsupported
```

Linux and macOS are simultaneous production targets. Windows is a cross-platform build and
UI target from the beginning, but it must explicitly report unsupported VPN/Leshy
capabilities until a real Windows core becomes a future roadmap line.

The desktop UI is never a second networking authority. It observes and commands the Go
core through a versioned local control API.

## Roadmap overview

The core/networking lane remains G0-G10:

```text
Current shell/systemd baseline
        |
        v
G0  Freeze safety contracts and acceptance tests
        |
        v
G1  Go daemon skeleton + N-role internal model + control API foundation
        |
        v
G2  Canonical physical-underlay monitor in shadow mode
        |
        v
G3  Generation-safe central reconciler
        |
        v
G4  Endpoint-underlay ownership moves to Go
        |
        v
G5  Route parking + Leshy publication move to Go
        |
        v
G6  First real Go-owned VPN driver/role
        |
        v
G7  Controlled underlay recovery using parking
        |
        v
G8  All configured managed VPN roles owned by Go
        |
        v
G9  Retire leshy-route-watch / network-watchdog architecture
        |
        v
G10 Complete status, diagnostics and operator API
```

The desktop lane starts in parallel instead of waiting for G10:

```text
U0   UI architecture/state/contract freeze
 |
U1   Qt 6 skeleton + Linux/macOS/Windows build matrix
 |
U2   versioned local control protocol
 |
U3   deterministic FakeCore + truthful Windows StubCore
 |
U4   Kikimora theme and reusable QML controls
 |
U5   application shell/navigation/core reconnect behavior
 |
U6   multi-VPN Home/dashboard
 |
U7   role detail + routing-safety surfaces
 |
U8   Profiles
 |
U9   Routing/Leshy
 |
U10  tray/menu-bar/settings desktop integration
 |
U11  diagnostics/supportability
 |
U12  real-core Linux + macOS integration gate
 |
U13  physical-underlay/recovery UX validation
 |
U14  production packaging
 |
U15  complete automated test gate
 |
U16  accessibility/localization/HiDPI polish
```

The detailed UI work and acceptance tests are in
[`desktop-ui-implementation-plan.md`](desktop-ui-implementation-plan.md).

Important dependency shape:

```text
G1 -----------------------> U2 control API
G2/G3 --------------------> U6 underlay/state presentation
G4/G5 --------------------> U7/U9 endpoint + parking + Leshy presentation
G6/G7 --------------------> U12/U13 real tunnel/recovery integration
G10 ----------------------> U11 final diagnostics richness

U1/U3/U4/U5 can proceed before real VPN drivers by using FakeCore.
```

## G0 - safety contract freeze and test harness

**Purpose:** turn current routing behavior into an executable specification before the
rewrite begins.

Deliverables:

- endpoint-underlay parity matrix;
- route-parking parity matrix;
- readiness/publication parity matrix;
- netns harness reusable by shell and Go implementations;
- explicit single-writer ownership switches for route/rule resources;
- future physical-underlay regression cases recorded before implementation.

The migration must not proceed by visually comparing routes after the fact. Existing
safety behavior is the test oracle.

UI coordination:

- U0 freezes the UI state vocabulary and ownership boundary at the same time;
- frontend state names must not invent networking semantics that contradict the G0/G1
  core model.

**Exit gate:** current safety invariants are represented by tests and every mutable
resource has one named owner.

## G1 - Go daemon, role model and control API foundation

**Purpose:** create the future control-plane process without changing networking.

Deliverables:

- long-running Go daemon;
- structured logging and clean shutdown;
- internal model that supports N managed VPN roles;
- compatibility reader for current primary/secondary profiles;
- future driver configuration model;
- read-only status exposing loaded role state;
- transport-neutral control API DTOs and version/capability handshake needed by U2;
- initial local control endpoint able to serve a state snapshot without networking
  mutations.

Hard rule introduced here:

```text
every role transport underlay = physical
```

There is no supported role-to-role underlay configuration.

The CLI JSON API remains available. The desktop GUI must not repeatedly spawn `kk --json`
as its normal runtime transport; both frontends should converge on canonical Go status
structures where practical.

**Exit gate:** Go can run beside the current implementation in observer mode with zero
kernel mutations, and the U2 client can negotiate protocol version/capabilities and fetch
a read-only snapshot.

## G2 - canonical physical-underlay monitor

**Purpose:** establish one source of interpreted physical-network state inside Go.

Deliverables:

- rtnetlink route/link/address subscriptions;
- canonical IPv4/IPv6 physical path snapshot;
- preferred source address in path identity;
- underlay epoch/material comparison;
- optional NetworkManager metadata watcher;
- logind suspend/resume integration;
- macOS-equivalent physical-network input in the production platform track;
- event coalescing with bounded settle time;
- low-frequency consistency resnapshot;
- interpreted underlay state exposed through the control API.

NetworkManager events are hints. Global connectivity-state transitions are not restart
commands.

Required Linux regression:

```text
CONNECTED_GLOBAL -> CONNECTED_SITE -> CONNECTED_GLOBAL
same interface/gateway/source
=> zero tunnel recovery actions
```

UI coordination:

- U6 consumes the interpreted snapshot/epoch;
- the UI never receives raw network events in order to decide recovery;
- window activation/resume may repair IPC and resnapshot, but never calls reconnect merely
  because the OS resumed.

**Exit gate:** shadow logs correctly distinguish material underlay changes from irrelevant
network churn and the same interpreted state is available to the frontend model.

## G3 - central reconciler

**Purpose:** ensure all later mutation passes through one race-safe authority.

Deliverables:

- reconciler-owned desired/current state;
- reconcile generation numbers;
- watcher events as invalidations only;
- serialized routing executor;
- asynchronous per-role protocol executors;
- stale-completion rejection/reconcile;
- monotonic control-API state revision suitable for snapshot + event-stream clients.

UI coordination:

- frontend revision is an observation ordering primitive, not the core reconcile generation
  unless deliberately mapped/documented;
- event gaps or IPC reconnect require a fresh snapshot;
- command results do not become an alternate source of role truth.

**Exit gate:** event bursts, resume and slow driver completions cannot commit state
calculated from an obsolete underlay generation; UI clients cannot regress to stale state.

## G4 - move endpoint-underlay ownership into Go

**Purpose:** make Go own the transport-safety plane before it owns VPN reconnects.

Deliverables:

- table `51890` and rule management in Go on Linux;
- corresponding physical-endpoint safety implementation on macOS;
- current static/happ/command provider contracts;
- `dynamic-additive` semantics;
- role-specific endpoint policy;
- physical-underlay unreachable/fail-safe behavior;
- safe pending behavior while VPN lifecycle is still externally owned;
- endpoint desired/applied/pending state in diagnostics and control API.

The shell route watcher stops writing endpoint policy when this stage is enabled.

UI coordination:

- U7 role detail can show endpoint `Ready/Pending/Unavailable`;
- U9 Routing may show aggregate endpoint safety;
- UI receives interpreted state and high-level actions such as `rediscover_endpoints`, not
  arbitrary route-table mutation.

**Exit gate:** the existing endpoint-underlay suite passes against the Go owner with no
dual writer, and Linux/macOS frontend models can consume the platform-neutral result.

## G5 - move parking and Leshy publication into Go

**Purpose:** establish the safety barrier required before automatic VPN recovery is
allowed.

Deliverables:

- Go observation of Leshy-owned destination routes;
- baseline exclusion of pre-existing routes;
- cached last-owned set for hard disappearance;
- fail-closed unreachable parking;
- parking retained until a real replacement route wins;
- Go ownership of role publication consumed by Leshy;
- Linux and macOS platform implementations needed by the real client;
- controlled-withdraw primitive:

```text
refresh route ownership
-> park
-> unpublish role
-> permit disruptive tunnel operation
```

This stage intentionally reuses the current parking concept instead of introducing a new
recovery-specific leak-prevention mechanism.

UI coordination:

- U7/U9 receive publication and parking summaries through the core API;
- UI may display `14 destinations parked`, but never creates or removes parking itself;
- Windows StubCore advertises these capabilities as unsupported.

**Exit gate:** Go parking/publication passes current parity tests and can safely withdraw a
role before a future controlled restart on both production desktop platforms.

## G6 - first Go-owned VPN driver

**Purpose:** prove end-to-end ownership with one protocol/client before multiplying driver
work.

Deliverables:

- protocol-neutral tunnel driver interface;
- fake driver for deterministic state-machine tests;
- one production driver;
- start/stop/quiesce/inspect/validate operations;
- driver capability reporting for rebind or transport-only restart where available;
- layered readiness replacing `TUN UP + IPv4` as the only truth;
- high-level connect/disconnect commands through the control API.

Only one role should be enabled as a canary during first real cutover. Other roles remain
independent and continue using the legacy path until their driver is ready.

UI coordination:

- U3 FakeCore should already have made U6/U7 usable before this stage;
- real-driver integration now replaces fake state for that canary on Linux and macOS;
- UI continues to use the exact same role DTO/state enum.

**Exit gate:** one role is fully owned by Go without weakening endpoint safety, parking or
Leshy routing and can be controlled through the same API used by the desktop frontend.

## G7 - physical-underlay change recovery

**Purpose:** implement the actual network-change behavior inside Kikimora.

A material underlay change or resume triggers **per-role reconciliation**, not a global
VPN restart.

Decision order:

```text
role disabled/stopped
    -> nothing

no physical underlay
    -> fail closed + WaitingForUnderlay

existing transport validates
    -> keep it

driver can safely rebind
    -> rebind + validate

driver can restart transport while keeping TUN
    -> controlled transport recovery

otherwise
    -> controlled full role restart
```

Disruptive recovery uses the G5 parking barrier:

```text
Ready
 -> Recovering
 -> park Leshy-owned destinations
 -> unpublish role
 -> stop/quiesce transport
 -> commit new endpoint underlay at safe boundary
 -> start/rebind
 -> validate
 -> publish
 -> wait for real Leshy routes
 -> unpark
 -> Ready
```

Required regressions include:

- NetworkManager connectivity-only state changes;
- unrelated Docker/veth churn;
- Wi-Fi to Ethernet;
- source-address/DHCP change;
- gateway change;
- NetworkManager restart;
- suspend/resume with TUN still present;
- one stale role while another stays healthy;
- recovery failure retaining parking;
- a newer underlay epoch arriving during an older recovery;
- macOS physical handoff and sleep/wake equivalents.

UI coordination:

- U13 validates exactly these paths through the desktop frontend;
- only the affected role changes presentation when only one role requires recovery;
- no QML/platform shell code listens for network changes to issue reconnect commands;
- recovery phase/reason is exposed for role detail/diagnostics where useful.

**Exit gate:** physical handoff and resume no longer require a systemd/network watchdog and
do not cause unconditional full reconnects; Linux and macOS UI accurately render the same
core-owned state transitions.

## G8 - normal multi-VPN ownership

**Purpose:** make the Go client the normal owner of all supported configured VPN roles.

Deliverables:

- all supported roles use Go drivers;
- bounded concurrent protocol recovery after a global physical handoff;
- serialized kernel routing transactions;
- role-isolated failures and status;
- versioned migration of VPN driver/profile configuration;
- extension of the Leshy-facing role/zone schema only where Leshy actually supports
  additional roles.

Even with several roles, all have the same topology:

```text
role -> physical underlay
```

Recovery concurrency is an implementation optimization, not a dependency relationship.

UI coordination:

- `RolesModel` remains list-based instead of hard-coding exactly two C++ objects;
- the current product can still label/display primary and secondary specifically;
- global Connect/Disconnect is convenience orchestration only and never hides per-role
  partial failure.

**Exit gate:** no managed role requires an external VPN client's lifecycle for normal
operation and the desktop frontend can represent every configured role independently.

## G9 - retire route-watch/network-watchdog architecture

**Purpose:** remove the legacy implementation only after parity.

`leshy-route-watch.service` can be removed only when Go owns all of its required
responsibilities:

- readiness/publication;
- endpoint providers and table `51890`/platform equivalent;
- endpoint pending/safe transitions;
- Leshy route observation;
- fail-closed parking;
- cached hard-disappearance recovery;
- physical-underlay observation;
- controlled VPN recovery.

Systemd/launchd continue to supervise `kikimora-core`. What disappears is a separate
systemd-supervised network-watch policy architecture.

A separate DNS/Leshy health service may remain temporarily if still useful; it must not
own or duplicate VPN underlay/reconnect decisions.

UI coordination:

- frontend never depends on the old runtime watcher files/services;
- role/Leshy/parking status comes from the Go API before legacy removal;
- legacy removal therefore requires no QML behavior change.

**Exit gate:** removing the legacy watcher changes no route, endpoint, parking,
publication, recovery or frontend-observable result in the acceptance suite.

## G10 - diagnostics and operator API

**Purpose:** make the new state machine observable enough for CLI, desktop UI and support.

Final status/control API should expose at least:

```text
underlay epoch and selected physical path
last underlay change
per-role desired/runtime state
per-role driver/interface
per-role last validated epoch
endpoint ready/pending state
recovery reason/action
Leshy publication state
parked route count
last driver health result
capabilities/platform/backend kind
structured recent logs/debug-bundle operation
```

Logs must identify role, reconcile generation, old/new physical identity and the selected
recovery action. Generic `network changed; reconnecting` logging is not sufficient.

`kk status`/JSON and the desktop control API should consume the same canonical core state
where practical instead of implementing independent interpretations.

UI coordination:

- U11 finalizes Diagnostics on these structures;
- debug bundle is one high-level privileged core operation, not shell commands issued by
  QML;
- sensitive VPN material must be redacted by default.

**Exit gate:** CLI/JSON/UI/debuglog can reconstruct why a role is Ready, Waiting,
Recovering or Failed without manually reading internal runtime files.

# Desktop UI parallel roadmap

## U0 - architecture/state contract freeze

**Purpose:** establish the desktop product and ownership boundary before implementation.

Deliverables:

- Qt/QML + C++ presentation architecture;
- Linux/macOS production + Windows stub platform policy;
- MVP screens and role-state vocabulary;
- capability-driven feature model;
- visual direction inspired by Amnezia's compact dark utility UI but using Kikimora-owned
  theme/assets;
- no-networking-authority rule for the frontend.

**Exit gate:** every MVP screen can be defined from core/fake DTOs without querying OS
networking itself.

## U1 - portable Qt skeleton and three-OS CI

**Purpose:** make portability an invariant before product code accumulates.

Deliverables:

- Qt 6/CMake desktop application;
- one QML source tree;
- Linux build/test job;
- macOS arm64 plus x86_64/universal strategy;
- Windows x86_64 build/test job;
- minimal headless/application smoke tests.

**Exit gate:** the same `Main.qml` launches on all three target OSes.

## U2 - local control protocol

**Purpose:** give GUI/CLI future frontends a stable process boundary to the Go core.

Deliverables:

- version/capability handshake;
- snapshot + monotonic event revision model;
- high-level command envelope with request IDs;
- Unix-domain local transport on Linux/macOS;
- Windows transport abstraction/stub compatibility;
- peer authorization and bounded framing;
- no arbitrary privileged shell API.

**Exit gate:** a test client can negotiate and observe Go state; Windows can negotiate a
truthful stub backend.

## U3 - FakeCore and Windows StubCore

**Purpose:** decouple frontend development from VPN-driver timing and keep Windows build
honest.

FakeCore scenarios include:

```text
all stopped
all ready
one failed / one ready
underlay absent
underlay changed
one recovering / one ready
parking active
endpoint pending
Leshy failed
core restart
protocol mismatch
```

Windows production stub returns `UnsupportedCapability` for VPN/Leshy/routing mutations
and never simulates Ready tunnels.

**Exit gate:** all important UI states are deterministic in tests while Windows product
state remains truthful.

## U4 - Kikimora visual system

**Purpose:** reproduce the desired Amnezia-like design language without copying branding or
assets.

Deliverables:

- singleton semantic theme;
- near-black dark palette with restrained warm accent;
- shared spacing/radius/typography/motion/focus tokens;
- reusable cards/buttons/status chips/drawers/dialogs/navigation controls;
- circular multi-state connect control;
- component gallery;
- keyboard/focus behavior in the component layer.

**Exit gate:** product pages add composition, not one-off styling systems.

## U5 - app shell/navigation/core reconnect

**Purpose:** complete window lifecycle and navigation before complex networking pages.

Deliverables:

- compact resizable main window;
- Home/Profiles/Routing/Settings bottom navigation;
- stack navigation for details;
- core unavailable/reconnecting surface;
- protocol mismatch surface;
- resnapshot after core restart;
- no daemon teardown on GUI close.

**Exit gate:** shell works against FakeCore on Linux/macOS/Windows.

## U6 - multi-VPN Home

**Purpose:** expose actual multi-role truth rather than a single `isConnected` boolean.

Deliverables:

- physical underlay/header summary;
- one card per configured role;
- independent per-role state/action;
- optional Connect all / Disconnect all convenience commands;
- clear partial failure/recovery representation;
- no UI-triggered recovery based on OS network events.

**Exit gate:** `primary=Ready, secondary=Failed/Recovering` is a normal, unambiguous UI
state.

## U7 - role details and routing safety

**Purpose:** make recovery understandable while keeping Home compact.

Deliverables:

- role drawer;
- endpoint underlay status;
- Leshy publication status;
- parking count;
- last validated underlay epoch;
- last recovery reason/action;
- typed failure presentation.

**Exit gate:** normal recovery failure can be explained without reading logs.

## U8 - Profiles

**Purpose:** make profile selection a GUI operation through the core contract.

First scope:

- list profiles;
- active profile;
- per-role driver/provider summary;
- activate profile;
- typed validation errors.

Editing follows when the Go profile schema is stable.

**Exit gate:** GUI never edits `vpn.conf`/profile files directly.

## U9 - Routing/Leshy

**Purpose:** expose real Leshy orchestration on both production platforms.

Deliverables:

- Leshy state;
- DNS integration;
- default zone and role-zone mapping;
- endpoint safety summary;
- parking summary;
- high-level repair/rediscover actions where supported.

Windows keeps the page but renders capability unavailable from StubCore.

**Exit gate:** Linux and macOS show real state through one model; Windows shows explicit
unsupported state through the same QML page.

## U10 - desktop shell integration

**Purpose:** make Kikimora behave like a native desktop utility.

Deliverables:

- Linux tray where available;
- native/reliable macOS menu-bar status integration;
- Windows tray shell;
- notifications;
- UI login startup;
- multi-role tray menu;
- close-to-tray preference;
- explicit separation between `Quit UI` and core/VPN stop.

**Exit gate:** UI process lifecycle is independent from daemon-owned VPN lifecycle.

## U11 - diagnostics

**Purpose:** expose enough state to debug the new reconciler.

Deliverables:

- versions/protocol/backend/capabilities;
- underlay snapshot/epoch;
- per-role state and recovery audit;
- endpoint/parking/Leshy summaries;
- bounded structured logs;
- copy status;
- redacted debug-bundle export.

**Exit gate:** support can reconstruct the current state and last recovery from the GUI.

## U12 - Linux + macOS real-core gate

**Purpose:** make both production desktop platforms genuine clients at the same time.

This is one gate with two parallel tracks, not `Linux first, macOS later`.

Linux and macOS must both pass:

```text
real local-core authentication
snapshot/event stream
connect/disconnect role
profile activation
underlay state updates
parking/recovery observation
real Leshy/routing state
core restart -> UI resnapshot
```

Windows continues passing the same frontend contract against StubCore.

**Exit gate:** no production Linux/macOS product surface depends on FakeCore or a stub
networking backend.

## U13 - recovery UX validation

**Purpose:** validate the exact underlay/recovery behavior that motivated the architecture.

Both Linux and macOS cover:

```text
Wi-Fi <-> Ethernet
source-address change
gateway change
physical network loss/return
sleep/resume
one-role recovery while another remains Ready
failed recovery with parking retained
core restart
```

Linux additionally preserves regressions for irrelevant NetworkManager connectivity-only
changes and virtual-interface churn.

**Exit gate:** the frontend never becomes a recovery authority and accurately renders G7
state transitions.

## U14 - production packaging

**Purpose:** distribute coherent UI/core products.

Linux package includes UI, Go core, system service/socket metadata and Leshy integration.

macOS distribution includes `Kikimora.app`, the launchd/privileged core path, Leshy/runtime
support, signing and notarization.

Windows package includes the same UI plus StubCore and clearly states that VPN/Leshy
networking is not yet implemented.

**Exit gate:** clean systems install the intended platform behavior without launching the
GUI as root.

## U15 - automated UI/protocol test gate

Required classes:

```text
C++ model/controller unit tests
protocol framing/revision tests
QML control tests
FakeCore E2E on all three OSes
Linux/macOS real-core E2E
selected visual regression tests
Windows unsupported-capability tests
```

**Exit gate:** product behavior is protected primarily at protocol/model boundaries, with
visual snapshots used for stable key surfaces.

## U16 - accessibility/localization/HiDPI polish

Required:

- complete keyboard path;
- visible focus;
- accessible names;
- state not communicated by color alone;
- reduced-motion support where practical;
- English/Russian translation pipeline;
- long-string layout tests;
- representative HiDPI tests on Linux/macOS.

**Exit gate:** the desktop line is a usable production application rather than only a
working networking frontend.

## Cross-stage rules

The following rules apply to every roadmap stage.

### No VPN chaining

Do not add:

```text
physical -> VPN A -> VPN B
```

or a generic tunnel dependency DAG. This is not Kikimora's target configuration.

### Parking is the fail-closed transaction guard

Do not invent a parallel recovery-only blackhole mechanism if the existing parking model
can carry the invariant. Controlled tunnel disruption must integrate with parking.

### Endpoint and user-route planes stay separate

VPN transport endpoints must remain reachable through the physical network while user
routes for an unavailable VPN stay fail closed.

### Events are not commands

Rtnetlink, NetworkManager, macOS network callbacks and sleep/resume events trigger
resnapshot/reconcile in core. They do not call `RestartVPN()` directly, and they are not
forwarded to QML as instructions to reconnect.

### Per-role isolation

A problem in one VPN role may change that role's publication and routes. It must not
implicitly restart or withdraw another healthy role.

### One writer

No migration stage is complete if shell and Go can both mutate the same
route/rule/runtime publication resource.

### UI is not a networking authority

The desktop UI does not query platform networking to decide tunnel truth. QML/C++ consume
core snapshot/events and issue high-level commands only.

### One common desktop product

Do not fork product pages into Linux/macOS/Windows QML trees. Platform differences live
behind C++ desktop adapters and core capabilities.

### Linux and macOS move together

A desktop production milestone is not complete with a real Linux backend and a fake/stub
macOS backend. Both are first-class targets from the start.

### Windows is truthful, not absent

Windows must remain in the build/test matrix. Until its networking backend exists it
reports VPN/Leshy/routing capabilities as unsupported. Production StubCore never fakes a
Ready VPN.

### GUI exit is not VPN exit

The Go core owns tunnel lifetime. Closing/quitting the GUI does not implicitly disconnect
healthy VPN roles unless the user invokes a separate explicit core action designed for
that purpose.

## CI progression

The core roadmap grows CI in ownership order:

```text
G0 current parity harness
G2 underlay snapshot/event tests
G3 race/generation tests
G4 endpoint-underlay Go parity
G5 parking/publication Go parity
G6 fake + real driver lifecycle
G7 physical handoff/recovery regression suite
G8 multi-role isolation suite
G9 legacy-removal parity run
```

Final required Go-oriented gates include at least:

```text
go test ./...
go test -race ./...
netns underlay-change suite
endpoint-underlay parity suite
route-parking parity suite
multi-role isolation suite
legacy configuration migration suite
```

The desktop lane adds mandatory continuous builds from U1 onward:

```text
Linux Qt build/test
macOS Qt build/test
Windows Qt build/test
QML lint/import checks
FakeCore protocol/E2E tests on all three OSes
Windows StubCore capability tests
```

After U12:

```text
Linux real-core UI smoke/E2E
macOS real-core UI smoke/E2E
core restart/resnapshot test
real role connect/disconnect test
profile activation test
recovery presentation test
```

A desktop PR that breaks Windows compilation is not acceptable merely because Windows has
no real networking core yet.

## Completion criteria for the Go multi-VPN roadmap line

The Go multi-VPN architecture is considered complete when:

1. Kikimora owns all configured supported VPN tunnels.
2. Every managed VPN transport uses the physical underlay directly.
3. Physical-network changes are interpreted from canonical platform state inside Go.
4. Resume validates actual transport usability rather than trusting a persistent UP TUN.
5. Recovery is per role and selects the least disruptive safe action.
6. Controlled disruptive recovery parks user destinations before tunnel teardown.
7. Endpoint policy is changed only at a safe transport boundary.
8. Leshy sees a role only while Kikimora considers it usable.
9. Healthy roles remain unaffected by another role's failure/recovery.
10. Connectivity-only events and unrelated virtual-interface churn cause zero tunnel
    restarts where applicable.
11. No systemd/network watchdog owns VPN recovery.
12. `leshy-route-watch.service` has been retired after behavior parity on Linux.
13. macOS has equivalent core ownership required by the desktop product.
14. The full unit, race, platform-network, parity and multi-role suites pass.
15. The control API exposes canonical state/actions needed by CLI and desktop clients.

## Completion criteria for the first desktop roadmap line

The first Linux/macOS Kikimora desktop generation is considered complete when:

1. one Qt 6/QML frontend source tree builds on Linux, macOS and Windows;
2. Linux and macOS use real Go cores and real Leshy/routing integration;
3. Windows uses the same frontend contract against an explicit capability-reporting stub;
4. the frontend survives core restart through reconnect + handshake + fresh snapshot;
5. primary and secondary VPN roles are independently visible and controllable;
6. Home never reduces multi-role truth to one authoritative `isConnected` boolean;
7. underlay state, recovery reason/action, endpoint safety, publication and parking are
   observable through core-provided state;
8. Profiles can be selected without directly editing configuration files from QML;
9. Routing/Leshy is a real production page on Linux and macOS and an explicit unsupported
   page on Windows;
10. tray/menu-bar state is multi-role aware;
11. closing the GUI does not tear down daemon-owned VPNs;
12. all pages use one semantic design system with a compact Amnezia-inspired but
    Kikimora-owned visual language;
13. no Amnezia logo/icon/art resource is copied into Kikimora;
14. keyboard, focus, localization and HiDPI gates pass;
15. Linux/macOS real-core E2E and three-OS FakeCore/StubCore tests pass;
16. Linux packaging and signed/notarized macOS distribution are validated;
17. Windows distribution truthfully states that its networking core is not implemented.
