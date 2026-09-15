# Go multi-VPN target architecture

Status: target architecture. This document describes the intended replacement for the
current shell/systemd route-watch based VPN lifecycle. It does not describe the current
runtime implementation.

See also:

- [`route-parking.md`](route-parking.md) for the existing fail-closed route ownership model;
- [`vpn-endpoints.md`](vpn-endpoints.md) for the existing physical-underlay endpoint policy;
- [`vpn-readiness.md`](vpn-readiness.md) for the current publication semantics;
- [`go-multi-vpn-implementation-plan.md`](go-multi-vpn-implementation-plan.md) for the staged implementation plan;
- [`roadmap.md`](roadmap.md) for project sequencing.

## 1. Goal

Kikimora becomes a long-running Go daemon that owns the lifecycle of several VPN tunnels
at the same time and coordinates their availability with Leshy.

The current system has to infer VPN state from interfaces created by unrelated clients.
The target system owns the VPN clients and therefore can make underlay changes, endpoint
routing, fail-closed parking, tunnel recovery and Leshy publication one coherent state
machine.

The target design deliberately does **not** introduce VPN chaining.

Every managed VPN transport uses the physical network directly:

```text
                     physical underlay
                    /        |         \
                   /         |          \
              VPN role A  VPN role B  VPN role C
                   \         |          /
                    \        |         /
                         Leshy
                           |
                   user traffic policy
```

Leshy may select a VPN role for user traffic. That does not make one VPN a transport
underlay for another VPN.

## 2. Hard invariants

The implementation must preserve these invariants.

1. **Physical-only VPN transport.** Every managed VPN endpoint must use the selected
   physical underlay. A managed VPN interface is never a valid underlay candidate for
   another managed VPN.
2. **Per-role lifecycle.** A failure or recovery decision for one VPN role does not imply
   restarting all other roles.
3. **Fail closed for user destinations.** When a role is unavailable or being restarted,
   Leshy-owned destinations previously assigned to that role must not fall through to the
   physical default route or another VPN.
4. **VPN endpoint routes are not parked.** VPN transport endpoints must remain reachable
   through the physical underlay while user destinations remain fail closed.
5. **Kernel state is the source of truth.** NetworkManager, logind and netlink events are
   invalidation/wakeup signals. An event is never itself a command to restart a VPN.
6. **No global reconnect on connectivity classification.** A NetworkManager transition
   such as `CONNECTED_SITE -> CONNECTED_GLOBAL` with the same physical path must produce
   no tunnel restart.
7. **Single writer per routing resource.** During migration, only one component may own a
   particular route/rule/parking namespace at a time. Go and shell implementations must
   never concurrently mutate the same state.
8. **Publication means usable.** A role is published to Leshy only after endpoint safety,
   structural readiness and the driver's required transport/data-plane readiness have
   succeeded.
9. **Recovery is idempotent.** Repeated equivalent events and repeated reconciliation of
   the same desired state must not create route churn, repeated restarts or duplicated
   state.
10. **Runtime state is reconstructible.** `/run` state and in-memory state are caches of
    reconstructible runtime truth, not persistent proof that the system is healthy.

## 3. Process model

The target Linux process model is intentionally smaller than the current watcher model.

```text
systemd
  |
  +-- kikimora.service  -> Go control plane and VPN client owner
  |
  +-- leshy.service     -> DNS classification and user-route producer
  |
  `-- resolver integration remains a separate boundary until its own migration
```

Systemd remains responsible for process startup, shutdown and crash restart. It must not
be used as the network event bus or as the mechanism for restarting individual VPN roles.

`leshy-route-watch.service` is retired only after its readiness, endpoint-underlay and
parking responsibilities have moved into the Go control plane and reached parity. The
existing DNS health path may remain separate until a later milestone; it is not a reason
to keep an underlay watchdog.

## 4. Core component model

The Go daemon is split into components with explicit ownership.

```text
rtnetlink   NetworkManager   logind/resume   periodic resync
    \            |               |                /
     \           |               |               /
      +----------+---------------+--------------+
                         |
                  Network invalidation
                         |
                         v
                +-------------------+
                | Underlay Monitor  |
                +---------+---------+
                          |
                  canonical snapshot
                          |
                          v
                +-------------------+
                | Reconciler        |
                | single authority  |
                +--+------+------+--+
                   |      |      |
           +-------+      |      +----------------+
           v              v                       v
  Endpoint Underlay   Tunnel Supervisors    Route Parking
       Manager           per role             Manager
           \              |                       /
            \             |                      /
             +------------+---------------------+
                          |
                          v
                    Leshy Bridge
```

### 4.1 Underlay Monitor

Responsibilities:

- subscribe to Linux route, address and link changes through rtnetlink;
- optionally subscribe to NetworkManager for higher-level connection identity and Wi-Fi
  metadata;
- subscribe to suspend/resume notification through logind;
- coalesce event bursts and rebuild a canonical physical-network snapshot;
- periodically rebuild the snapshot as a low-frequency consistency audit in case an event
  was lost;
- never restart a tunnel directly.

Rtnetlink is mandatory. NetworkManager is enrichment, not a hard dependency. Kikimora
must still work on a Linux host where NetworkManager is not the network owner.

### 4.2 Underlay Resolver

The resolver selects the physical path independently for IPv4 and IPv6.

Initial semantics should intentionally match today's endpoint-underlay behavior: inspect
normal physical defaults, exclude all managed VPN interfaces, loopback and Kikimora/Leshy
virtual plumbing, and select the effective best remaining physical default.

A canonical path contains at least:

```text
family
ifindex
interface name
gateway
preferred source address
MTU
route table
route metric/priority metadata
```

Optional NetworkManager metadata may add:

```text
active connection UUID
Wi-Fi BSSID
```

The optional metadata may help classify a physical handoff, but NetworkManager's global
`Connectivity` state must not be part of physical-path identity.

### 4.3 Underlay snapshot and epoch

The daemon keeps a canonical `UnderlaySnapshot` and a monotonically increasing in-process
underlay epoch.

Conceptually:

```text
UnderlaySnapshot
  epoch
  ipv4 path or unavailable
  ipv6 path or unavailable
  physical link identity metadata
```

The epoch advances only when a material physical-path identity changes, for example:

- selected physical interface changes;
- gateway changes;
- preferred source address changes;
- the physical link disappears or becomes available;
- a Wi-Fi handoff changes physical link identity in a way that requires validation.

The epoch does not advance merely because NetworkManager reclassifies internet
connectivity while the selected physical path is unchanged.

Suspend/resume is special: resume forces a full resnapshot and per-role validation even
if the resulting snapshot compares equal, because transport sockets may be stale after a
sleep interval.

### 4.4 Reconciler

The reconciler is the state authority. Event sources only invalidate its view.

The desired flow is:

```text
OS event(s)
   |
   v
coalesce / settle
   |
   v
rebuild snapshot
   |
   +-- equivalent --> no routing action, no reconnect
   |
   `-- changed ----> new underlay epoch
                         |
                         v
                 reconcile every role
```

All kernel route/rule mutations are serialized through the reconciler or a dedicated
routing executor owned by it. Long-running tunnel starts/stops must not block observation
of new network events; they execute asynchronously and report completion tagged with the
reconcile generation that requested them. A stale completion causes another reconcile
rather than overwriting newer state.

## 5. Managed VPN role model

The internal model should not hard-code exactly two roles even if the first migration
continues to expose `primary` and `secondary` to Leshy.

A role contains conceptually:

```text
name
Leshy zone / publication identity
desired enabled state
VPN driver + driver configuration
expected tunnel interface
endpoint provider + provider configuration
runtime tunnel state
last validated underlay epoch
last recovery reason
```

All roles have the same underlay dependency:

```text
role -> physical underlay
```

There is no role-to-role underlay dependency graph and no supported nested VPN topology.

## 6. Tunnel driver contract

Protocol-specific lifecycle details belong behind a driver interface. The core owns the
policy; the driver owns protocol mechanics.

A driver needs operations equivalent to:

```text
Start
Quiesce/Stop
Inspect runtime state
Validate health/readiness
Report the tunnel interface
Optionally rebind/restart only the transport while preserving the TUN
```

The exact Go API is defined during implementation, but the policy boundary is important:
a driver must not install Kikimora's Leshy routes, parking routes or endpoint-underlay
policy on its own.

Drivers may expose recovery capabilities. The core should be able to choose the least
disruptive safe action:

```text
underlay changed
   |
   +-- transport still valid + health passes -> keep running
   |
   +-- driver can rebind transport ----------> rebind
   |
   +-- driver can restart transport only ----> keep stable TUN, restart transport
   |
   `-- otherwise ----------------------------> full role restart
```

A public Internet URL is not a mandatory health primitive. Driver-native information,
protocol handshakes, socket state and role-specific probes are preferred. A driver that
cannot prove continued health may report `unknown`; after a material underlay change the
safe fallback may then be a controlled role recovery rather than assuming that an
UP+addressed TUN is healthy.

## 7. Role state machine

Desired state and runtime state are separate. A useful runtime model is:

```text
Stopped
WaitingForUnderlay
Starting
Validating
Ready
Recovering
Stopping
Failed
```

`Ready` is the only normal state published to Leshy.

Important transitions:

```text
Stopped -> Starting -> Validating -> Ready

Ready -> Recovering -> Validating -> Ready

Ready -> WaitingForUnderlay
WaitingForUnderlay -> Recovering -> Validating -> Ready

any active state -> Stopping -> Stopped
```

The tunnel interface may remain present while the role is `Recovering` or
`WaitingForUnderlay`. Therefore interface existence must no longer be treated as the
role's availability truth.

## 8. Route parking as the recovery safety barrier

The existing parking design is not discarded. It becomes a core primitive of the Go
recovery transaction.

Today parking protects Leshy-owned IPv4 host routes when an externally owned VPN device
disappears. In the target architecture the same invariant also protects deliberate
Kikimora-controlled recovery.

For a role that requires disruptive recovery:

```text
role Ready
   |
   v
capture/update last observed Leshy-owned destinations
   |
   v
install/confirm fail-closed parking
   |
   v
withdraw role publication from Leshy
   |
   v
quiesce or stop VPN transport/tunnel
   |
   v
repair endpoint underlay for the new physical path
   |
   v
start/rebind VPN
   |
   v
validate endpoint safety + tunnel readiness
   |
   v
publish role to Leshy
   |
   v
Leshy recreates real lower-metric routes
   |
   v
observe real routes, then remove matching parking
```

The important ordering rule is:

```text
park before a controlled disruptive transition;
unpark only after the replacement real route is observed.
```

For a hard failure where the old route disappears before Kikimora can park it, the
manager uses the last observed ownership set exactly as the current parking design does.

Endpoint-underlay routes are excluded from this parking set. They must remain available
so the VPN can reconnect.

## 9. Endpoint underlay in the Go control plane

The existing table `51890`, provider model, role-specific priorities, unreachable
fallback and safe-boundary semantics should be preserved conceptually.

The difference is ownership: because Kikimora now owns tunnel lifecycle, an unsafe
endpoint-underlay change no longer has to wait for a manual user disconnect.

A pending change becomes an internal recovery condition:

```text
new physical path / endpoint policy differs
   |
   v
role enters Recovering
   |
   v
parking + publication withdrawal if disruption is required
   |
   v
quiesce transport
   |
   v
apply endpoint-underlay change at a safe boundary
   |
   v
restart/rebind role
```

The current `dynamic-additive` contract remains useful: newly proven live transport
endpoints may be added while a role is running, but destructive removal/move of an
existing live endpoint remains a safe-boundary operation.

## 10. Leshy integration

Leshy remains responsible for domain classification and creation of user destination
routes. Kikimora becomes the authority for whether a VPN role is usable.

The first Go migration should keep the current role publication compatibility surface:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

This avoids coupling the VPN-client rewrite to a simultaneous Leshy protocol rewrite.
The Go daemon owns those files and writes/removes them from role-state transitions rather
than inferring role state from them.

For additional roles, the Leshy-facing configuration/API must be extended explicitly;
internal support for N roles must not silently invent unsupported Leshy zones.

When a role leaves `Ready`, its publication is withdrawn even if the TUN still exists.
When it becomes `Ready` again, publication occurs only after endpoint and transport
validation.

Any cache flush/reload needed to make Leshy revisit previously failed route additions is
performed as part of the publication transition, not by a periodic watcher guessing from
interface state.

## 11. Recovery on physical-network change

A material underlay change is reconciled independently for every enabled role. It is not
an instruction to restart every role.

Example: Wi-Fi to Ethernet.

```text
wlan0 -> eth0
   |
   v
new underlay snapshot / epoch
   |
   +--> role A validate -> survives -> remains Ready
   |
   +--> role B validate -> stale transport -> controlled recovery
   |
   `--> role C already stopped -> no action
```

All roles are peers over the same physical underlay. Recovery may be scheduled with
bounded concurrency, but ordering is an implementation/resource-control decision, not a
semantic dependency between VPNs.

If no physical underlay exists, enabled roles move to `WaitingForUnderlay`. Their user
routes remain fail closed. When a physical path returns, roles reconcile against the new
epoch.

## 12. Event semantics

The following behavior is required.

| Event | Expected result |
| --- | --- |
| `CONNECTED_SITE -> CONNECTED_GLOBAL`, same physical snapshot | no epoch change, no restart |
| unrelated Docker/veth creation or removal | no epoch change, no restart |
| managed TUN link event | tunnel lifecycle input, not physical-underlay change |
| preferred source address changes | new epoch, per-role validation/recovery |
| physical default gateway changes | new epoch, per-role validation/recovery |
| Wi-Fi -> Ethernet | new epoch, per-role validation/recovery |
| Wi-Fi BSSID handoff with otherwise equal L3 path | forced role validation; restart only if required |
| NetworkManager restarts | rebuild snapshot; do not restart solely because NM restarted |
| suspend -> resume | forced resnapshot and role validation |
| physical network absent | roles wait, endpoints fail closed appropriately, user routes remain parked |

## 13. Concurrency model

Network event handlers must never mutate routing or tunnel state directly.

Recommended model:

1. watcher goroutines emit small invalidation events;
2. a coalescer collapses bursts;
3. the reconciler owns desired/current state and computes role actions;
4. a routing executor serializes kernel route/rule transactions;
5. per-role executors perform potentially blocking protocol operations;
6. executor completions carry a reconcile generation and are discarded/reconciled if
   stale.

This model prevents overlapping `resume`, NetworkManager, netlink and driver-completion
events from producing competing route rewrites or duplicate reconnects.

## 14. Configuration direction

Current profiles describe external interfaces and endpoint providers. A client-owning
profile must eventually describe the VPN driver as well.

Conceptually a role needs:

```text
role name
Leshy zone
VPN driver
driver configuration reference
endpoint provider and provider args
enabled state
optional stable interface name
```

Secrets should be referenced from protected files or driver-specific secret stores rather
than copied into general status/config output.

`underlay = physical` is an invariant, not a per-role user-selectable topology option.

The migration must read existing `primary`/`secondary` profile semantics until a versioned
Go-native profile format is introduced.

## 15. State ownership matrix

| State/resource | Owner in target architecture |
| --- | --- |
| physical link/address/route truth | Linux kernel |
| NetworkManager connection metadata | NetworkManager, read-only hint to Kikimora |
| underlay snapshot/epoch | Kikimora Go core |
| managed VPN processes/transports | Kikimora tunnel drivers |
| managed VPN role state/readiness | Kikimora Go core |
| VPN endpoint policy/table `51890` | Kikimora endpoint manager |
| Leshy-owned destination parking | Kikimora parking manager |
| domain classification | Leshy |
| normal Leshy destination routes | Leshy |
| role publication to Leshy | Kikimora Leshy bridge |
| process supervision | systemd |

## 16. Observability requirements

At minimum status/debug output must expose:

```text
current underlay epoch
selected IPv4/IPv6 physical path
last material underlay change and reason
per-role desired state
per-role runtime state
per-role last validated epoch
per-role recovery reason/action
published/unpublished state
endpoint-underlay ready/pending state
parking destination count
last driver health result
```

Recovery logs must identify the role and generation. Logs such as `network changed,
reconnecting` without the old/new physical identity are insufficient for debugging.

## 17. Explicit non-goals

The first Go architecture does not support:

- VPN-over-VPN chaining;
- a VPN role using another managed VPN as its endpoint underlay;
- a generic dependency DAG between tunnel roles;
- mandatory public-Internet health URLs;
- treating NetworkManager's global connectivity classification as tunnel truth;
- replacing Leshy's domain-classification function;
- changing unrelated VPN/firewall software on behalf of the user.

These exclusions are deliberate. They keep recovery and fail-closed behavior aligned with
Kikimora's actual deployment model.
