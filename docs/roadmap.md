# Kikimora roadmap

This file is the project roadmap for the next architecture transition. Current runtime
behavior remains documented in the existing focused documents; future design is described
in separate target/plan documents so current and planned behavior are not mixed.

Target architecture:
[`go-multi-vpn-architecture.md`](go-multi-vpn-architecture.md)

Detailed implementation plan:
[`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md)

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

Reference documents:

- [`vpn-endpoints.md`](vpn-endpoints.md)
- [`endpoint-providers.md`](endpoint-providers.md)
- [`vpn-readiness.md`](vpn-readiness.md)
- [`route-parking.md`](route-parking.md)
- [`orchestration.md`](orchestration.md)
- [`profiles.md`](profiles.md)

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

Systemd remains a process supervisor. Physical-network detection, VPN recovery, endpoint
routing and per-role state machines move into the Go daemon.

## Roadmap overview

```text
Current shell/systemd baseline
        |
        v
G0  Freeze safety contracts and acceptance tests
        |
        v
G1  Go daemon skeleton + N-role internal model
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
G10 Complete status, diagnostics and operator tooling
```

The detailed work and tests for every stage are in
[`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md).

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

**Exit gate:** current safety invariants are represented by tests and every mutable
resource has one named owner.

## G1 - Go daemon and role model

**Purpose:** create the future control-plane process without changing networking.

Deliverables:

- long-running Go daemon;
- structured logging and clean shutdown;
- internal model that supports N managed VPN roles;
- compatibility reader for current primary/secondary profiles;
- future driver configuration model;
- read-only status exposing loaded role state.

Hard rule introduced here:

```text
every role transport underlay = physical
```

There is no supported role-to-role underlay configuration.

**Exit gate:** Go can run beside the current implementation in observer mode with zero
kernel mutations.

## G2 - canonical physical-underlay monitor

**Purpose:** establish one source of interpreted physical-network state inside Go.

Deliverables:

- rtnetlink route/link/address subscriptions;
- canonical IPv4/IPv6 physical path snapshot;
- preferred source address in path identity;
- underlay epoch/material comparison;
- optional NetworkManager metadata watcher;
- logind suspend/resume integration;
- event coalescing with bounded settle time;
- low-frequency consistency resnapshot.

NetworkManager events are hints. Global connectivity-state transitions are not restart
commands.

Required regression:

```text
CONNECTED_GLOBAL -> CONNECTED_SITE -> CONNECTED_GLOBAL
same interface/gateway/source
=> zero tunnel recovery actions
```

**Exit gate:** shadow logs correctly distinguish material underlay changes from irrelevant
network churn.

## G3 - central reconciler

**Purpose:** ensure all later mutation passes through one race-safe authority.

Deliverables:

- reconciler-owned desired/current state;
- reconcile generation numbers;
- watcher events as invalidations only;
- serialized routing executor;
- asynchronous per-role protocol executors;
- stale-completion rejection/reconcile.

**Exit gate:** event bursts, resume and slow driver completions cannot commit state
calculated from an obsolete underlay generation.

## G4 - move endpoint-underlay ownership into Go

**Purpose:** make Go own the transport-safety plane before it owns VPN reconnects.

Deliverables:

- table `51890` and rule management in Go;
- current static/happ/command provider contracts;
- `dynamic-additive` semantics;
- role-specific endpoint policy;
- physical-underlay unreachable fallback;
- safe pending behavior while VPN lifecycle is still externally owned;
- endpoint desired/applied/pending state in diagnostics.

The shell route watcher stops writing endpoint policy when this stage is enabled.

**Exit gate:** the existing endpoint-underlay suite passes against the Go owner with no
dual writer.

## G5 - move parking and Leshy publication into Go

**Purpose:** establish the safety barrier required before automatic VPN recovery is
allowed.

Deliverables:

- Go observation of Leshy-owned destination routes;
- baseline exclusion of pre-existing routes;
- cached last-owned set for hard disappearance;
- fail-closed unreachable parking;
- parking retained until a real replacement route wins;
- Go ownership of role `.dev` publication files;
- controlled-withdraw primitive:

```text
refresh route ownership
-> park
-> unpublish role
-> permit disruptive tunnel operation
```

This stage intentionally reuses the current parking concept instead of introducing a new
recovery-specific leak-prevention mechanism.

**Exit gate:** Go parking/publication passes current parity tests and can safely withdraw a
role before a future controlled restart.

## G6 - first Go-owned VPN driver

**Purpose:** prove end-to-end ownership with one protocol/client before multiplying driver
work.

Deliverables:

- protocol-neutral tunnel driver interface;
- fake driver for deterministic state-machine tests;
- one production driver;
- start/stop/quiesce/inspect/validate operations;
- driver capability reporting for rebind or transport-only restart where available;
- layered readiness replacing `TUN UP + IPv4` as the only truth.

Only one role should be enabled as a canary during first real cutover. Other roles remain
independent and continue using the legacy path until their driver is ready.

**Exit gate:** one role is fully owned by Go without weakening endpoint safety, parking or
Leshy routing.

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
- a newer underlay epoch arriving during an older recovery.

**Exit gate:** physical handoff and resume no longer require a systemd/network watchdog and
do not cause unconditional full reconnects.

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

**Exit gate:** no managed role requires an external VPN client's lifecycle for normal
operation.

## G9 - retire route-watch/network-watchdog architecture

**Purpose:** remove the legacy implementation only after parity.

`leshy-route-watch.service` can be removed only when Go owns all of its required
responsibilities:

- readiness/publication;
- endpoint providers and table `51890`;
- endpoint pending/safe transitions;
- Leshy route observation;
- fail-closed parking;
- cached hard-disappearance recovery;
- physical-underlay observation;
- controlled VPN recovery.

Systemd continues to start/stop/restart `kikimora.service` after this stage. What disappears
is systemd-supervised network-watch logic as an architectural component.

A separate DNS/Leshy health service may remain temporarily if still useful; it must not
own or duplicate VPN underlay/reconnect decisions.

**Exit gate:** removing the legacy watcher changes no route, endpoint, parking,
publication or recovery result in the acceptance suite.

## G10 - diagnostics and operator surface

**Purpose:** make the new state machine observable enough to operate and debug.

Final status should expose at least:

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
```

Logs must identify role, reconcile generation, old/new physical identity and the selected
recovery action. Generic `network changed; reconnecting` logging is not sufficient.

**Exit gate:** `kk status`/JSON/debuglog can reconstruct why a role is Ready, Waiting,
Recovering or Failed without reading internal runtime files manually.

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

Rtnetlink, NetworkManager and logind events trigger resnapshot/reconcile. They do not call
`RestartVPN()` directly.

### Per-role isolation

A problem in one VPN role may change that role's publication and routes. It must not
implicitly restart or withdraw another healthy role.

### One writer

No migration stage is complete if shell and Go can both mutate the same route/rule/runtime
publication resource.

## CI progression

The roadmap should grow CI in the same order as ownership:

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

Final required Go-oriented gates should include at least:

```text
go test ./...
go test -race ./...
netns underlay-change suite
endpoint-underlay parity suite
route-parking parity suite
multi-role isolation suite
legacy configuration migration suite
```

## Completion criteria for this roadmap line

The Go multi-VPN architecture is considered complete when:

1. Kikimora owns all configured supported VPN tunnels.
2. Every managed VPN transport uses the physical underlay directly.
3. Physical-network changes are interpreted from canonical kernel state inside Go.
4. Resume validates actual transport usability rather than trusting a persistent UP TUN.
5. Recovery is per role and selects the least disruptive safe action.
6. Controlled disruptive recovery parks user destinations before tunnel teardown.
7. Endpoint policy is changed only at a safe transport boundary.
8. Leshy sees a role only while Kikimora considers it usable.
9. Healthy roles remain unaffected by another role's failure/recovery.
10. NetworkManager connectivity-only events and unrelated veth churn cause zero tunnel
    restarts.
11. No systemd/network watchdog owns VPN recovery.
12. `leshy-route-watch.service` has been retired after behavior parity.
13. The full unit, race, netns, parity and multi-role suites pass.
