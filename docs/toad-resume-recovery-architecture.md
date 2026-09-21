# Toad/Kikimora suspend-resume and fail-closed recovery architecture

Status: **design input / not implemented**.

This document records a real legacy failure observed on 2026-09-21 and turns it into architectural requirements for the Go Toad runtime and the future Go Kikimora control plane. It must not be interpreted as a completed Stage 0 item.

Current roadmap position when this document was written:

- Toad steps 01-05 are complete;
- step 06 (simultaneous AWG2 + Xray isolation) is still the current unfinished Stage 0 packet;
- Stage 0 is not complete;
- the Go control-plane work described here belongs after Stage 0, beginning with planned step 07.

Canonical roadmap: `docs/toad-roadmap.md`.
Planned executor packet: `docs/toad-steps/07-go-reconcile-resume.md`.

## Legacy incident: what actually failed

The old deployment used:

- an Amnezia desktop client owning `amn0`;
- an external secondary VPN owning `vpn0`;
- Bash/systemd Kikimora route/health watchers;
- Leshy DNS classification plus route injection.

After a long suspend/resume:

1. `vpn0` was no longer usable.
2. The Amnezia process and its `wireguard-go -f amn0` child were still alive.
3. `amn0` still existed and the TUN fd was still owned by `wireguard-go`, but the interface no longer had its expected IP configuration and the AWG handshake did not recover.
4. The old route watcher correctly removed the primary/secondary device readiness files.
5. Leshy continued returning DNS answers even when it could not install the selected VPN route. The kernel therefore had an opportunity to fall through to the ordinary physical/default route.
6. Primary destinations were observed resolving successfully while their selected route went directly through Wi-Fi instead of `amn0`.

The important distinction is:

> a live VPN process, a live TUN fd, a link with `UP/LOWER_UP`, and an actually usable route target are four different facts.

The new architecture must represent them separately.

## Upstream Amnezia / NetworkManager interaction

The source audit used public Amnezia client revision
`94b51df24790bf52427afe82d81c87a95460bdfd`. The diagnostic strings from the incident match this code path.

Relevant upstream files:

- `client/platforms/linux/linuxnetworkwatcherworker.cpp`;
- `client/platforms/linux/linuxnetworkwatcher.cpp`;
- `service/server/localserver.cpp`;
- `client/vpnConnection.cpp`;
- `client/core/protocols/wireGuardProtocol.cpp`;
- `client/mozilla/localsocketcontroller.cpp`;
- `client/daemon/daemon.cpp`.

### What the Amnezia watcher does

`LinuxNetworkWatcherWorker` subscribes to the NetworkManager **system D-Bus**:

- `org.freedesktop.NetworkManager.StateChanged`;
- Wi-Fi device `org.freedesktop.DBus.Properties.PropertiesChanged`.

Its state handler maps:

- NetworkManager state 10 (`NM_STATE_ASLEEP`) -> emits `wakeup()`;
- NetworkManager state 70 (`NM_STATE_CONNECTED_GLOBAL`) -> emits `networkChanged()`.

The naming is misleading: on Linux the `wakeup` signal is emitted for the NetworkManager *asleep/disabled* state, not for the later transition back to a usable underlay.

The service forwards both signals over IPC. `VpnConnection::createProtocolConnections()` connects both `wakeup` and `networkChanged` to the same slot:

`VpnConnection::reconnectToVpn()`.

That slot is edge-triggered and state-gated:

1. it only proceeds when the UI state is exactly `Connected`;
2. it changes the state to `Reconnecting`;
3. it calls protocol `stop()`;
4. it immediately calls protocol `start()`;
5. any later reconnect event received while still `Reconnecting` is ignored.

This is the critical failure pattern for suspend/resume.

### What the incident showed on resume

The diagnostic captured the following sequence after the machine returned from suspend:

1. NetworkManager rediscovered `amn0` as an external TUN and transitioned it through `unmanaged -> unavailable -> disconnected`, with managed type becoming `full`.
2. The Amnezia daemon kept polling for the AWG handshake.
3. Before the physical Wi-Fi route was fully restored, the AWG engine logged a handshake send failure with `no route to host`.
4. Wi-Fi/DHCP then recovered.
5. NetworkManager reached `CONNECTED_GLOBAL` and Amnezia logged `NMStateChanged 70`.
6. At that exact point the desktop client logged `Reconnect triggered on Reconnecting ... ignoring slot` three times.
7. The process and `amn0` survived, but the route-target configuration did not recover and the handshake polling continued.

The most defensible causal conclusion is:

- the Amnezia state machine was already stuck in `Reconnecting` when the real underlay became globally usable;
- the event that should have retried recovery was therefore discarded;
- process/TUN liveness was not enough to restore address configuration or data-plane health.

The log does **not** prove that NetworkManager alone removed the `amn0` address. NetworkManager's transition is correlated with the broken state, but the Amnezia stop/start/reconnect sequence also mutates the interface. The new design must make that distinction irrelevant by giving one component authoritative ownership and reconciliation of the managed TUN configuration.

## Architectural root causes to avoid

The new design must not repeat these legacy properties:

1. **Coarse OS connectivity state as a reconnect command.** `CONNECTED_GLOBAL` is a host observation, not a protocol lifecycle instruction.
2. **One-shot edge handling.** Recovery must not depend on receiving one event while the FSM happens to be in exactly one state.
3. **"Already reconnecting" as a reason to discard convergence work.** Repeated recovery requests must coalesce, not vanish.
4. **Process/TUN existence treated as readiness.** A route target is ready only when its interface identity and local configuration are intact.
5. **Ambiguous TUN ownership.** NetworkManager must not become an accidental configuration owner for Kikimora-managed `kk-*` interfaces.
6. **Route deletion as failure handling.** Removing a selected VPN route exposes the ordinary main/default route and creates fail-open behavior.
7. **DNS answer success treated as route success.** DNS and routing are separate transactions.
8. **Duplicate event subscriptions creating repeated reconnect attempts.** The controller must have one idempotent desired-state/reconcile path.

## Target ownership model

### Go Kikimora control plane owns

- desired instance state: up/down;
- Toad registry and config generations;
- endpoint-underlay pinning and reconciliation;
- host suspend/resume observation;
- physical-underlay observations;
- route-policy reconciliation;
- explicit fail-closed state when a selected route target is unavailable;
- retry/backoff policy for control operations;
- durable/idempotent reconciliation rather than one-shot event scripts.

Kikimora may observe NetworkManager, systemd-logind and netlink, but none of those event streams directly mean "restart this VPN".

### Toad owns

- the managed route-target interface;
- interface name and ifindex identity;
- configured addresses;
- MTU and link state;
- protocol-core attachment;
- protocol/session health sampling;
- traffic counters;
- idempotent reconnect/reload operations;
- restoration of its own interface configuration if an external host component disturbs it.

A Toad must not subscribe to NetworkManager `CONNECTED_GLOBAL` and turn it into a protocol restart.

### Leshy / routing plane owns

During migration Leshy remains the DNS/routing policy engine. The future Go control plane supplies authoritative route-target facts.

For every selected destination the routing layer must have one of two explicit outcomes:

- route to the selected ready Toad interface; or
- explicit deny/unreachable/blackhole policy for that destination.

"Do not install a route" is not a valid failure outcome, because the kernel can then fall through to another Toad or the physical default route.

The same rule applies independently to IPv4 and IPv6.

### NetworkManager owns

NetworkManager owns the physical host connectivity it is configured to manage.

On Linux, Kikimora-managed `kk-*` TUNs must be deliberately excluded from NetworkManager ownership using an integration appropriate to the installed NetworkManager version. The Linux platform integration must verify the resulting state instead of merely assuming it.

This is defense in depth, not the only correctness mechanism. Even if NetworkManager is absent, restarted, reconfigured or temporarily observes the device, Toad remains authoritative for the TUN's expected addresses/MTU/link state and reconciles drift.

External VPNs such as legacy `vpn0` remain externally owned. Kikimora may observe their readiness but must not silently pretend to own their reconnect semantics.

## State model

Do not encode all health into one enum.

Each managed Toad needs at least these independent facts:

```text
desired_state          = up | down
process_alive          = true | false
interface_present      = true | false
interface_identity_ok  = true | false
interface_config_ok    = true | false
underlay_ready         = true | false
protocol_healthy       = true | false
route_ready            = true | false
config_generation      = N
underlay_generation    = M
```

`route_ready` means the interface is a valid fail-closed target. It does **not** require the remote peer to be healthy.

Example:

```text
desired_state=up
interface_present=true
interface_config_ok=true
underlay_ready=false
protocol_healthy=false
route_ready=true
```

is a valid degraded state: selected traffic still enters the stable TUN and cannot leak to the physical default route while the underlay recovers.

A process alive with missing addresses is instead:

```text
process_alive=true
interface_present=true
interface_config_ok=false
route_ready=false
```

and must trigger reconciliation.

## Reconciliation instead of reconnect events

The controller operates from desired state to observed state.

Events only wake the reconciler early. Missing an event must not prevent eventual convergence.

Conceptually:

```text
for each instance:
    observe()
    compare desired vs observed

    if desired=down:
        converge to stopped
        keep routing policy explicitly safe during teardown
        return

    ensure Toad process exists
    ensure managed TUN identity exists
    ensure TUN addresses/MTU/link configuration match desired config
    ensure endpoint underlay policy is valid for current physical network
    publish route_ready

    if underlay unavailable:
        mark degraded
        keep TUN
        do not recreate interface
        return

    allow official protocol core to recover naturally

    if protocol health remains stale beyond recovery grace:
        request idempotent transport/session reset
        keep TUN identity
        continue reconciling until healthy
```

A repeated `resume`, `route changed`, `link changed`, or `network changed` observation while recovery is already in progress merely causes another reconcile pass. It is never rejected as "inappropriate state".

## Suspend/resume contract

Suspend/resume is an underlay discontinuity, not a VPN-interface lifecycle event.

### Before suspend

An authoritative OS sleep signal, such as systemd-logind `PrepareForSleep(true)`, may be used to:

- mark the host as suspending;
- stop aggressive retry timers;
- snapshot diagnostic state;
- keep desired Toad state unchanged;
- keep Toad-owned route-target TUNs alive.

Do not stop/recreate a Toad solely because the host is entering suspend.

### After resume

On `PrepareForSleep(false)`:

1. bump an `underlay_generation`;
2. observe physical links/addresses/routes through netlink;
3. wait until the endpoint has a valid physical route;
4. reconcile endpoint-underlay pinning for the new generation;
5. reconcile Toad TUN identity and address/MTU configuration;
6. keep routing fail-closed throughout;
7. let the official protocol core recover;
8. after a bounded grace period, request an idempotent transport/session reset if health is still stale;
9. continue periodic reconciliation until the desired and observed states converge.

NetworkManager state may be recorded as telemetry but must not be the sole gate for steps 2-9.

## Endpoint-underlay rule

The overlay endpoint must never recurse into its own Toad route.

For every underlay generation the controller must derive the endpoint path from the physical routing state and reconcile the endpoint pin before declaring `underlay_ready=true`.

If the physical default route disappears temporarily:

- keep the previous desired endpoint identity;
- mark underlay not ready;
- keep the Toad route target stable;
- do not redirect endpoint traffic into another Toad unless explicit policy says so.

## Fail-closed routing contract

The 2026-09-21 incident proved that "route injection failed" cannot be a warning-only condition.

For domain-derived destinations:

1. DNS classification chooses a policy/Toad.
2. Resolution yields A/AAAA addresses.
3. Routing reconciliation atomically establishes either:
   - a route to the selected Toad, or
   - an explicit unreachable/blackhole rule for that address.
4. Only then is the answer considered safely usable.

A DNS answer may still be returned while the selected VPN is unavailable if an explicit deny route is already installed. This is preferable to relying on DNS SERVFAIL because applications may cache addresses, use existing sockets, or use other resolver paths.

When a selected Toad disappears completely, its existing selected destinations must transition to explicit deny state before any Toad-specific routes are removed.

## External VPN compatibility

External interfaces such as `vpn0` are different from managed Toads.

Kikimora cannot guarantee their internal reconnect state machine. Therefore:

- readiness is observed, not inferred from a GUI/process;
- if the external route target becomes unusable, selected destinations become explicitly fail-closed;
- when the interface returns with valid addressing, the routing reconciler may restore selected routes;
- the external client is not restarted unless a future explicit adapter grants Kikimora that ownership.

This preserves migration compatibility without inheriting the external client's recovery bugs.

## Linux NetworkManager integration

For managed `kk-*` interfaces the Linux platform layer must deliberately prevent NetworkManager from becoming the configuration owner.

Acceptable implementations may use, depending on the installed NetworkManager API/version:

- a per-device NetworkManager configuration marking matching `kk-*` devices unmanaged;
- a runtime D-Bus managed-state operation;
- an equivalent supported integration that produces the same observable ownership result.

Acceptance is behavioral:

- NetworkManager must report the managed Toad TUN as not owned for IP configuration;
- suspend/resume must not cause NetworkManager to activate a synthetic connection on it;
- Toad's expected address/MTU must remain or be reconciled automatically;
- the TUN ifindex must remain stable through ordinary resume recovery.

Do not make correctness depend only on a NetworkManager configuration file. Toad must still detect and repair interface drift.

## Required tests before production migration

The post-Stage-0 Go control-plane work needs explicit regression gates:

1. **Repeated underlay down/up** with a stable Toad TUN and unchanged ifindex.
2. **Interface-address drift**: delete a Toad-owned address externally and prove Toad repairs it without process/TUN recreation.
3. **NetworkManager observation**: prove `kk-*` is excluded from NetworkManager configuration ownership.
4. **Resume-like event storm**: duplicate/reordered notifications must coalesce into convergence, not duplicate stop/start sequences.
5. **Stuck-reconnecting regression**: force a transport reset to remain in progress, then restore underlay; recovery must continue rather than discard the new observation.
6. **Endpoint route generation change**: physical gateway/address change must refresh endpoint pinning before transport is considered recoverable.
7. **Fail-closed primary routing**: selected destination must never fall through to physical default while its Toad is unavailable.
8. **Fail-closed default-secondary routing**: same property for the default policy.
9. **IPv6 parity**: AAAA-selected destinations must not escape through the physical IPv6 default route.
10. **Real host suspend/resume manual gate** after hermetic tests: suspend the workstation for several minutes, resume, and prove interface identity/configuration, endpoint underlay, protocol recovery, DNS/routing policy and no direct leak.

## Non-goals for the current Stage 0 packet

Do not broaden step 06 into this control-plane migration.

Specifically, step 06 must remain the simultaneous protocol/process isolation gate. It must not:

- replace Bash Kikimora;
- replace Leshy;
- add a new host suspend manager;
- add production NetworkManager integration;
- implement the Go route reconciler.

Those are post-Stage-0 tasks.

## Design consequence

The key rule is:

> host events request reconciliation; they do not directly command protocol lifecycle.

A future Go Kikimora should therefore behave more like a controller than a collection of reconnect scripts. Desired state persists, observations may be transient, and the system keeps reconciling until they agree. A missed or duplicated NetworkManager/suspend event must not decide whether the VPN eventually works.
