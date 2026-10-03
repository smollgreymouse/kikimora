# Kikimora roadmap

This file is the project roadmap for the Go multi-VPN architecture transition and the desktop UI built on top of it.

Detailed documents:

- [`toad-roadmap.md`](toad-roadmap.md) — canonical current execution order;
- [`toad-post-push-audit.md`](toad-post-push-audit.md) — 2026-09-21 code/CI review of the large Go push;
- [`go-vpn-orchestration-v2-plan.md`](go-vpn-orchestration-v2-plan.md) — architectural behavior ledger; direct monolithic execution is superseded;
- [`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md);
- [`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md);
- [`desktop-ui-architecture.md`](desktop-ui-architecture.md);
- [`desktop-ui-implementation-plan.md`](desktop-ui-implementation-plan.md).

## 2026-09-21 audited execution status

The branch already contains much of G1-G8 and U1-U7 in code, but those stages are **not accepted by presence alone**. The audited baseline `2c0fa833177c49c60cd0c58291490e1a28a16f79` has red CI and several safety gaps.

Current mandatory order:

```text
06A restore deterministic baseline / Xray health semantics
 -> 06 simultaneous AWG2 + Xray + OpenConnect gate
 -> 07A authoritative state/capabilities
 -> 07B endpoint/routing/IPv4+IPv6 parking
 -> 07C underlay/TUN drift/NM/suspend
 -> 07D persisted desired state + privileged cutover
```

Do not run production `kk orchestration cutover --go` before 07D. Current cutover can consider a merely active daemon successful even though configured roles start desired=false.

The G/U sections below remain the target architecture and feature ledger; completion is now recorded only through the Toad remediation/acceptance packets.

## Current baseline

The existing Linux shell/systemd implementation already provides safety primitives that must survive migration:

- independent primary/secondary VPN roles;
- role publication through runtime `.dev` files after readiness stabilization;
- endpoint-underlay policy in table `51890`;
- role-specific endpoint priorities;
- safe `underlay-pending` behavior for live externally owned VPNs;
- `dynamic-additive` endpoint-provider semantics;
- fail-closed parking of Leshy-owned destinations;
- cached route ownership for hard tunnel disappearance;
- separate Leshy/DNS health and resolver repair;
- versioned machine-readable CLI JSON.

Reference documents include `vpn-endpoints.md`, `endpoint-providers.md`, `vpn-readiness.md`, `route-parking.md`, `orchestration.md`, `profiles.md`, and `cli-json-api.md`.

## Target platform matrix

The platform policy is now explicit:

```text
Linux
  Qt/QML UI
  real Go Kikimora core
  real VPN drivers
  real Leshy
  production target

macOS
  same Qt/QML UI
  real Go Kikimora core
  real VPN drivers
  real Leshy
  production target

Windows
  same Qt/QML UI
  FakeCore only
  no real Kikimora networking yet
  frontend portability/test target
```

Leshy currently supports Linux and macOS. A real Kikimora networking backend is therefore not planned for Windows until the required Leshy/routing substrate exists there.

A production desktop milestone is incomplete if Linux works but macOS does not.

## Target networking topology

Kikimora becomes a Go multi-VPN client/control plane instead of supervising interfaces created by unrelated VPN clients.

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

Every managed VPN transport goes directly through the physical underlay. VPN-over-VPN chains and generic tunnel dependency DAGs are out of scope.

systemd/launchd supervise processes. Physical-network interpretation, endpoint routing, parking, role state machines and recovery belong in the Go core.

## Target desktop UX

The desktop client intentionally follows the useful Amnezia interaction hierarchy instead of presenting one large card per VPN role.

Home is:

```text
+----------------------------------+
| Kikimora              profile    |
| physical underlay                |
|                                  |
|              (  O  )             |
|          CONNECT / DISCONNECT    |
|                                  |
|         aggregate state          |
|                                  |
| Primary                 Ready  > |
| Secondary            Stopped  > |
| ...                              |
|                                  |
| Home Profiles Routing Settings   |
+----------------------------------+
```

Hard UX rules:

```text
one large global circle
-> ConnectAll when all enabled roles are stopped
-> DisconnectAll whenever any role is active/starting/recovering/partially ready

below circle
-> compact collapsed status row per VPN role/toad

click role row
-> expand/open Amnezia-style detail drawer for that role
```

There are no separate giant per-role connection circles on Home.

Per-role commands remain available as secondary actions in the expanded role detail.

## Combined roadmap overview

```text
NETWORK CORE                                  DESKTOP UI
------------                                  ----------
G0 safety contracts ------------------------> U0 UX/state contract freeze
G1 Go daemon/N-role model ------------------> U1 Qt skeleton
                                              U2 CoreClient + FakeCore
G2 underlay monitor ------------------------> U3 control API/state feed
G3 central reconciler ----------------------> U4 aggregate connection model
G4 endpoint-underlay ownership -------------> U5 role detail endpoint state
G5 parking/publication ---------------------> U6 Routing/Leshy state
G6 first Go VPN driver ---------------------> U7 Linux real-core E2E
G7 controlled recovery ---------------------> U8 macOS real-core E2E
                                              U9 degraded/recovery UX
G8 all roles Go-owned ----------------------> U10 multi-role production UX
G9 retire legacy watchers ------------------> U11 tray/menu-bar + packaging
G10 diagnostics/operator surface ----------> U12 diagnostics
                                              U13 accessibility/localization
                                              U14 packaging
                                              U15 CI matrix
                                              U16 release acceptance
```

The UI line starts early with FakeCore; it does not wait until the networking rewrite is finished.

---

# Go networking roadmap

## G0 - safety contract freeze and test harness

**Purpose:** make current routing behavior an executable specification before rewriting ownership.

Deliverables:

- endpoint-underlay parity matrix;
- route-parking parity matrix;
- readiness/publication parity matrix;
- reusable netns harness;
- explicit single-writer ownership switches;
- underlay-change regression cases.

**Exit gate:** current safety invariants are tests and every mutable resource has one named owner.

## G1 - Go daemon and N-role model

**Purpose:** create the future control-plane process without changing networking ownership yet.

Deliverables:

- long-running Go daemon;
- clean shutdown and structured logs;
- N-role internal model;
- compatibility reader for current primary/secondary profiles;
- future driver configuration model;
- read-only status API.

Hard rule:

```text
every managed role transport underlay = physical
```

**Exit gate:** Go can run beside legacy implementation in observer mode without kernel mutations.

## G2 - canonical physical-underlay monitor

**Purpose:** establish one interpreted physical-network truth inside Go.

Deliverables:

- rtnetlink route/link/address subscriptions on Linux;
- macOS equivalent underlay observation;
- canonical IPv4/IPv6 path snapshot;
- preferred source address in path identity;
- underlay epoch/material comparison;
- optional NetworkManager metadata on Linux;
- suspend/resume integration;
- event coalescing/settle;
- low-frequency consistency resnapshot.

Events are invalidations, not restart commands.

Required regression:

```text
CONNECTED_GLOBAL -> CONNECTED_SITE -> CONNECTED_GLOBAL
same interface/gateway/source
=> zero tunnel recovery actions
```

**Exit gate:** logs distinguish material underlay changes from irrelevant churn.

## G3 - generation-safe central reconciler

**Purpose:** ensure every later mutation passes through one race-safe authority.

Deliverables:

- reconciler-owned desired/current state;
- reconcile generations;
- watcher events as invalidations;
- serialized routing executor;
- asynchronous per-role protocol executors;
- stale-completion rejection.

**Exit gate:** event bursts, resume and slow driver operations cannot commit obsolete decisions.

## G4 - endpoint-underlay ownership moves to Go

**Purpose:** move transport-safety routing before Go owns VPN reconnects.

Deliverables:

- table `51890` management in Go;
- static/happ/command providers;
- `dynamic-additive` semantics;
- role-specific endpoint policy;
- physical-underlay unreachable fallback;
- safe pending while lifecycle remains externally owned;
- desired/applied/pending diagnostics.

**Exit gate:** endpoint-underlay parity tests pass with no dual writer.

## G5 - parking and Leshy publication move to Go

**Purpose:** establish the safety barrier required before automatic disruptive recovery.

Deliverables:

- observation of Leshy-owned routes;
- baseline exclusion of pre-existing routes;
- cached last-owned set;
- fail-closed unreachable parking;
- retain parking until real replacement wins;
- Go ownership of role publication files/contracts;
- controlled-withdraw primitive.

Controlled withdrawal:

```text
refresh route ownership
-> park
-> unpublish role
-> permit disruptive tunnel operation
```

**Exit gate:** parking/publication parity tests pass and Go can safely withdraw a role.

## G6 - first Go-owned VPN driver

**Purpose:** prove complete ownership with one protocol/client.

Deliverables:

- protocol-neutral driver interface;
- fake driver for deterministic tests;
- one production driver on Linux and macOS where supported;
- start/stop/quiesce/inspect/validate;
- driver capabilities for rebind/transport-only restart;
- layered readiness beyond `TUN UP + IPv4`.

**Exit gate:** one role is fully Go-owned without weakening endpoint safety, parking or Leshy routing.

## G7 - physical-underlay change recovery

**Purpose:** implement safe network-change behavior.

A material underlay change or resume triggers per-role reconciliation, not global restart.

Decision order:

```text
role disabled/stopped
    -> nothing

no physical underlay
    -> fail closed + WaitingForUnderlay

transport validates
    -> keep it

driver can rebind
    -> rebind + validate

driver can restart transport while keeping TUN
    -> controlled transport recovery

otherwise
    -> controlled full role restart
```

Disruptive recovery uses parking:

```text
Ready
 -> Recovering
 -> park Leshy-owned destinations
 -> unpublish role
 -> stop/quiesce transport
 -> commit endpoint-underlay at safe boundary
 -> start/rebind
 -> validate
 -> publish
 -> wait for real Leshy routes
 -> unpark
 -> Ready
```

Required regressions:

- connectivity-only NetworkManager transitions;
- unrelated Docker/veth churn;
- Wi-Fi/Ethernet handoff;
- source-address change;
- gateway change;
- NetworkManager restart;
- suspend/resume with TUN still present;
- one stale role while another remains healthy;
- recovery failure retaining parking;
- newer underlay epoch during older recovery.

**Exit gate:** handoff/resume no longer requires a watchdog and does not cause unconditional full reconnects.

## G8 - normal multi-VPN ownership

**Purpose:** make Go the normal owner of all supported configured roles.

Deliverables:

- all supported roles use Go drivers;
- bounded concurrent protocol recovery;
- serialized kernel routing transactions;
- role-isolated failure/status;
- versioned configuration migration;
- role/zone schema extensions only where Leshy supports them.

**Exit gate:** no managed role requires an external VPN client's lifecycle.

## G9 - retire legacy route-watch/network-watchdog ownership

Remove `leshy-route-watch.service` only after Go owns all required responsibilities:

- readiness/publication;
- endpoint providers/table `51890`;
- endpoint pending/safe transitions;
- Leshy route observation;
- fail-closed parking;
- cached hard-disappearance handling;
- physical-underlay observation;
- controlled VPN recovery.

systemd/launchd remain process supervisors.

**Exit gate:** removing legacy watcher ownership changes no acceptance result.

## G10 - diagnostics/operator surface

Final core status must expose at least:

```text
underlay epoch/path
last underlay change
per-role desired/runtime state
driver/interface
last validated epoch
endpoint ready/pending
recovery reason/action
Leshy publication
parked route count
last driver health result
```

Generic `network changed; reconnecting` logs are insufficient.

**Exit gate:** status/debug data explain why every role is Ready, Waiting, Recovering or Failed.

---

# Desktop UI roadmap

## U0 - UX and state-contract freeze

Freeze aggregate, role, core, underlay, Leshy, safety and real/fake backend states.

Freeze global circle command semantics:

```text
all enabled roles stopped -> ConnectAll
anything else active/busy/partial -> DisconnectAll
```

**Exit gate:** no QML networking inference is required to decide the primary action.

## U1 - shared Qt/QML skeleton

Create Qt 6/CMake app and CI builds for Linux, macOS and Windows.

One shared page tree:

```text
Home
Profiles
Routing
Settings
```

**Exit gate:** the same shell launches on all three OSes.

## U2 - CoreClient + FakeCore

Introduce:

```text
CoreClient
RealCoreClient     Linux/macOS
FakeCoreClient     Windows/tests
```

FakeCore provides deterministic scenarios including all-ready, partial failure, recovery, underlay loss, parking and Leshy failure.

Windows runs FakeCore only and clearly identifies simulation.

**Exit gate:** intended UI behavior is testable without a real daemon.

## U3 - Amnezia-like design system

Build theme tokens and reusable controls, especially:

```text
KConnectControl
KRoleSummaryRow
KRoleDetailDrawer
KBottomNav
KDrawer
KStatusChip
```

**Exit gate:** every aggregate state and role-row state renders in isolation.

## U4 - Home aggregate connection surface

Implement one large central circle.

States include:

```text
Disconnected
Connecting
Connected
Recovering
Degraded
Failed
Disconnecting
Unavailable
```

The circle emits one primary activation and a C++ controller maps it to `ConnectAll` or `DisconnectAll`.

**Exit gate:** Home contains exactly one dominant connect/disconnect control regardless of role count.

## U5 - collapsed role/toad list + detail drawer

Below the circle show compact rows:

```text
Primary      Ready       >
Secondary    Recovering  >
```

Clicking a row opens one role detail drawer containing driver, endpoint, interface, underlay, Leshy publication, parking and recovery/error details plus secondary per-role actions.

**Exit gate:** one-click access to role truth without large role cards.

## U6 - Profiles and Routing/Leshy pages

Profiles expose active profile and contained roles.

Routing exposes real Linux/macOS Leshy/DNS/parking/endpoint state. Windows renders FakeCore simulation only.

**Exit gate:** pages consume the same AppModel/CoreClient contract.

## U7 - real local control API

Linux/macOS get versioned local IPC with handshake, snapshot, revisioned events and semantic commands:

```text
ConnectAll
DisconnectAll
ConnectRole
DisconnectRole
RetryRole
SetActiveProfile
RediscoverEndpoints
```

**Exit gate:** QML is unchanged when switching FakeCore to real core.

## U8 - Linux real-core E2E

Validate real daemon/core, VPN roles, Leshy, parking, endpoint state, UI restart and core restart behavior.

**Exit gate:** Linux production UI contains no FakeCore dependency.

## U9 - macOS real-core E2E

Validate the same semantics using launchd, real macOS routing adapters, real VPN drivers and real Leshy.

**Exit gate:** Linux+macOS together satisfy the real desktop milestone.

## U10 - degraded/recovery UX

Home remains simple:

```text
circle = aggregate state
rows = which role differs
role drawer = why
```

Expected automatic recovery does not spam dialogs/toasts.

**Exit gate:** network handoff/recovery can be understood from one screen and one expansion.

## U11 - tray/menu-bar

Primary tray action remains aggregate:

```text
Show Kikimora
aggregate state
Connect all / Disconnect all
role state summaries
Quit
```

Linux uses Qt tray where available. macOS may use native `NSStatusItem`. Windows tray reflects FakeCore only.

## U12 - diagnostics

Expose real/fake backend kind, API versions, underlay epoch, aggregate derivation inputs, per-role state, recovery reasons, endpoint state, parking, Leshy and logs.

## U13 - accessibility/localization

Require keyboard navigation, focus rings, accessible names for the global circle and role rows, reduced motion, non-color-only semantics, localization and HiDPI coverage.

## U14 - packaging

```text
Linux   real UI + real core integration
macOS   signed/notarized app + real core/helper + Leshy integration
Windows UI + FakeCore only
```

Do not package a misleading Windows networking service.

## U15 - CI matrix

Common frontend tests run on all three platforms.

Real networking E2E is required on Linux and macOS.

Windows is a frontend/FakeCore build/test gate.

## U16 - desktop release acceptance

Desktop UI line is complete when:

1. Home has one large global Connect/Disconnect circle.
2. The circle calls core `ConnectAll`/`DisconnectAll` rather than looping roles in QML.
3. Per-role/toad status appears as compact rows below it.
4. Clicking a row expands role details Amnezia-style.
5. Partial/degraded state preserves per-role truth.
6. Linux uses real Go core + real Leshy.
7. macOS uses real Go core + real Leshy.
8. Linux-only success cannot close the real-desktop milestone.
9. Windows uses FakeCore only until Leshy/routing support exists.
10. One QML tree builds on all three OSes.
11. UI exit does not stop real tunnels.
12. UI/core restart resynchronizes from canonical snapshots.
13. Diagnostics explain aggregate/per-role differences.
14. accessibility, localization, packaging and CI gates pass.

---

# Cross-stage rules

### No VPN chaining

Do not add `physical -> VPN A -> VPN B` or a generic tunnel dependency DAG.

### Parking is the fail-closed transaction guard

Controlled disruptive recovery integrates with the existing parking model instead of inventing a second recovery blackhole mechanism.

### Endpoint and user-route planes stay separate

VPN transport endpoints remain reachable through the physical network while user routes for unavailable VPN roles remain fail closed.

### Events are not commands

rtnetlink, NetworkManager, macOS network events and suspend/resume trigger resnapshot/reconcile. They do not directly call `RestartVPN()`.

### Per-role isolation

One failed role may change its own publication/routes. It must not implicitly restart or withdraw a healthy role.

### One writer

No stage is complete if legacy and Go code can mutate the same routing/publication resource.

### UI is not a network controller

QML never shells out, parses human CLI output or mutates routes/interfaces directly.

### Aggregate UX does not erase role truth

The single global circle simplifies normal interaction, but core and detail UI retain independent role state at all times.

### Platform support follows the complete stack

Real Kikimora networking is released only where the required routing/VPN/Leshy stack exists. Today that means Linux and macOS; Windows remains FakeCore.

## CI progression

```text
G0 current parity harness
G2 underlay snapshot/event tests
G3 race/generation tests
G4 endpoint-underlay Go parity
G5 parking/publication Go parity
G6 fake + real driver lifecycle
G7 physical handoff/recovery suite
G8 multi-role isolation
G9 legacy-removal parity

U1 three-platform Qt build
U2 FakeCore scenarios
U3 component tests
U4 aggregate control tests
U5 role expansion tests
U7 IPC contract tests
U8 Linux real-core E2E
U9 macOS real-core E2E
U15 complete desktop matrix
```

## Overall completion criteria

The architecture transition is complete when:

1. Kikimora owns all configured supported VPN tunnels on production platforms.
2. Every managed VPN transport uses the physical underlay directly.
3. Physical-network changes are interpreted from canonical state inside Go.
4. Resume validates transport usability instead of trusting a persistent UP TUN.
5. Recovery is per role and selects the least disruptive safe action.
6. Disruptive recovery parks user destinations before teardown.
7. Endpoint policy changes only at safe transport boundaries.
8. Leshy sees a role only while Kikimora considers it usable.
9. Healthy roles remain unaffected by another role's recovery.
10. Connectivity-only events and irrelevant virtual-interface churn cause zero tunnel restarts.
11. No network watchdog owns VPN recovery.
12. Legacy route-watch ownership is retired after parity.
13. Linux and macOS provide the real Kikimora + Leshy desktop product.
14. Windows shares the frontend but remains FakeCore until full routing/Leshy support is possible.
15. The Home interaction is a single Amnezia-like aggregate circle with compact expandable role status beneath it.
16. Full unit, race, netns, parity, multi-role and desktop E2E suites pass.
