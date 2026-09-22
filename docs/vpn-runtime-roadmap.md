# Managed VPN runtime roadmap

This document is the architectural roadmap for replacing GUI-owned VPN clients with Kikimora-managed VPN instances while preserving the existing Bash orchestrator and Leshy routing during migration.

The first production protocols are **AmneziaWG 2.x** and **VLESS + REALITY**. AmneziaWG 3.x is a planned extension. Existing NetworkManager-owned VPNs, especially an OpenConnect `vpn0`, remain external targets and are not taken over by the managed runtime.

## 1. Target topology

Kikimora must support an arbitrary number of simultaneously active routing targets:

```text
managed awg-main      -> kikimora-vpn@awg-main     -> kk-awg0
managed xray-main     -> kikimora-vpn@xray-main    -> kk-xray0
managed awg-backup    -> kikimora-vpn@awg-backup   -> kk-awg1
managed xray-backup   -> kikimora-vpn@xray-backup  -> kk-xray1
external corporate    -> NetworkManager/OpenConnect -> vpn0
```

The future routing model is:

```text
Leshy zone -> named target -> current route-ready interface
```

A target name is not a synonym for `primary` or `secondary`. Several zones may use the same target. Managed and external targets must eventually expose the same orchestrator-facing state model.

## 2. Hard architectural invariants

These rules define the design boundary and must survive implementation changes.

1. **VPN clients report facts; the orchestrator makes policy decisions.**
   The client owns protocol/session facts. Kikimora owns desired state, underlay policy, routing targets, Leshy configuration and presentation.
2. **Transport reconnect never implies TUN recreation.**
   Handshake timeout, REALITY TCP reconnect, roaming, endpoint retry, suspend/resume and temporary underlay loss keep the same TUN instance while the process is healthy.
3. **A managed TUN is fail-closed.**
   During reconnect, packets may queue or drop but never fall through to the physical default route.
4. **systemd supervises process liveness, not VPN network health.**
   `Restart=on-failure` handles a crash/fatal exit. Normal network outages are handled inside the client process.
5. **Managed clients do not react to NetworkManager connectivity-state transitions.**
   `CONNECTED_SITE`, `CONNECTED_GLOBAL` and connectivity-check changes are not reconnect triggers.
6. **State snapshots are authoritative; events are advisory.**
   Missing an event may delay reaction but may not corrupt state. Current state can always be reconstructed from a snapshot.
7. **Endpoint underlay is eventually an explicit lease.**
   A managed client resolves an endpoint, asks the orchestrator to prepare/pin the physical underlay, and only dials after an explicit grant.
8. **Secrets never appear in state snapshots, event payloads or normal logs.**
9. **External VPNs remain externally owned.**
   Kikimora observes `vpn0`; it does not activate/deactivate the NetworkManager profile unless a separate explicit feature is introduced later.
10. **Every migration stage is independently usable.**
    Existing Bash Kikimora and Leshy continue to work after each stage.

## 3. Responsibility split

### 3.1 `kikimora-vpn` instance

One process owns one managed VPN instance and one stable TUN. It owns:

- TUN creation, address/MTU configuration and lifetime;
- protocol backend state;
- protocol sockets;
- handshake/connect/reconnect/rekey/roaming/backoff;
- transport recovery after suspend/resume;
- endpoint resolution facts;
- local health/state publication;
- later, an instance control socket.

It does not own:

- Leshy domain/static-route policy;
- system DNS integration;
- arbitrary destination routes;
- NetworkManager connections;
- role/zone selection;
- physical-underlay route policy.

### 3.2 Kikimora orchestrator

Initially the existing Bash stack remains the orchestrator. Later it can move to a dedicated Rust daemon. It owns:

- desired client state;
- target publication to Leshy;
- endpoint-underlay policy;
- external-interface observation;
- routing zones and domain/static-route configuration;
- user-visible health and diagnostics;
- fail-closed policy when a target genuinely disappears.

### 3.3 Leshy

Leshy remains the routing/DNS policy engine. It should not become a VPN supervisor.

## 4. Process and filesystem model

A single executable runs as independent instances:

```text
kikimora-vpn@awg-main.service
kikimora-vpn@xray-main.service
kikimora-vpn@awg-backup.service
```

A failure in one instance must not terminate another.

Planned configuration:

```text
/etc/kikimora/vpn/clients/<name>.toml
```

Planned runtime state:

```text
/run/kikimora/vpn/clients/<name>/state.json
/run/kikimora/vpn/clients/<name>/control.sock
/run/kikimora/vpn/events.sock
```

Stage 0 does not require the sockets; they are reserved now so the runtime layout does not need another incompatible redesign.

## 5. State model

Interface routability and transport health are separate concepts.

Suggested states:

```text
starting
armed
connecting
online
reconnecting
degraded
failed
stopping
```

`route_ready` is independent of the transport state.

Examples:

```text
state=online        route_ready=true
state=reconnecting  route_ready=true
state=failed        route_ready=true   # fail-closed sink can still be intentionally retained
state=starting      route_ready=false
```

This distinction removes the need to withdraw a managed route every time a protocol session reconnects.

A future snapshot shape:

```json
{
  "schema": 1,
  "name": "awg-main",
  "protocol": "amneziawg2",
  "generation": 17,
  "updated_at_unix_ms": 1788255000000,
  "state": "online",
  "reason": "handshake-established",
  "route_ready": true,
  "interface": {
    "name": "kk-awg0",
    "ifindex": 42,
    "mtu": 1380
  },
  "endpoint": {
    "address": "192.0.2.10",
    "port": 443
  },
  "session": {
    "connected": true,
    "last_handshake_age_ms": 3812
  }
}
```

No private key, UUID, REALITY private material, server password or full imported secret config may be emitted.

## 6. Snapshot + event architecture

The future IPC model intentionally avoids a reliable message broker.

### Snapshot

Each client atomically replaces `state.json`. The snapshot is the source of truth.

### Event bus

A future Unix datagram socket `/run/kikimora/vpn/events.sock` carries small notifications such as:

```json
{
  "seq": 241,
  "client": "awg-main",
  "event": "state-changed",
  "from": "reconnecting",
  "to": "online",
  "reason": "handshake-established"
}
```

If a datagram is lost, the orchestrator rereads the snapshot. Events optimize latency; they never own state.

### Control socket

A future per-instance Unix socket supports semantic commands:

```text
status
reconnect
reload
re-resolve
dump-debug
shutdown
```

`reconnect` means protocol/session reconnect and must preserve the TUN. It is deliberately different from restarting the systemd unit.

## 7. Endpoint-underlay lease

The managed client should eventually stop relying on endpoint files plus polling markers.

Target sequence:

```text
client resolves endpoint
        |
        v
endpoint.prepare(address, port, generation)
        |
        v
orchestrator installs/verifies physical-underlay policy
        |
        v
endpoint.granted(generation)
        |
        v
client opens protocol socket / starts handshake
```

When the endpoint changes, the old lease is revoked and a new lease is prepared before dialing.

The orchestrator can therefore prevent recursive routing without guessing whether a GUI client has already created a tunnel.

## 8. Managed reconnect semantics

### AmneziaWG

Loss of handshake or peer reachability:

```text
kk-awg0 remains
state -> reconnecting
protocol session retries/rekeys
state -> online
```

### VLESS + REALITY

Loss of the REALITY transport:

```text
kk-xray0 remains
state -> reconnecting
outbound connection is re-established
state -> online
```

Neither path deletes/recreates the TUN unless the TUN itself is fatally broken or the process is explicitly stopping.

## 9. External target adapter

An external NetworkManager VPN remains a black box. During migration, existing Bash watchers keep handling it.

Long term, an `ExternalInterfaceAdapter` converts external facts into the same target state model:

```text
vpn0 + kernel/NM observations
        -> external adapter
        -> route_ready/state/reason/interface snapshot
        -> orchestrator
```

This isolates heuristic polling to external targets instead of infecting the managed-client design.

## 10. Evolution of Leshy targets

Current Kikimora is constrained around:

```text
primary -> /run/kikimora/leshy/vpn/primary.dev
secondary -> /run/kikimora/leshy/vpn/secondary.dev
```

Long term the generator must support named targets:

```text
/run/kikimora/leshy/vpn/awg-main.dev     -> kk-awg0
/run/kikimora/leshy/vpn/xray-main.dev    -> kk-xray0
/run/kikimora/leshy/vpn/corporate.dev    -> vpn0
```

Zones then point at target names rather than hard-coded primary/secondary roles.

## 11. Migration stages

### Stage 0 — standalone managed VPN clients

Build independent Rust VPN clients while keeping the current Bash Kikimora as the only orchestrator.

Deliver:

- multi-instance `kikimora-vpn` runtime;
- stable per-instance TUN ownership;
- backend abstraction;
- AmneziaWG 2.x backend as the first production backend;
- VLESS + REALITY backend as the second production backend;
- internal reconnect that preserves the TUN;
- state snapshot writing;
- systemd template unit;
- safe isolated CI using network namespaces plus unprivileged unit tests.

Old Bash Kikimora initially sees `kk-awg0`, `kk-xray0`, etc. as ordinary VPN interfaces and continues using the existing readiness/reconcile path.

### Stage 1 — formal state contract, Bash reads it for observability

Formalize state schema v1. Bash `status`, `diag` and diagnostics read managed-client snapshots, but routing/reconcile remains unchanged.

Goal: validate the contract in production without putting it on the critical routing path.

### Stage 2 — managed clients bypass heuristic readiness polling

For managed targets, Bash trusts `route_ready` and client identity rather than deriving readiness from link/address/ifindex polling and 3/3 streaks.

External `vpn0` continues through the legacy observation path.

### Stage 3 — event bus

Introduce `/run/kikimora/vpn/events.sock`. The orchestrator gets immediate change notifications and reconciles against snapshots.

Polling remains a fallback and stale-state detector.

### Stage 4 — explicit endpoint-underlay leases

Replace managed-client endpoint polling/pending markers with `prepare -> grant -> dial` semantics.

Retain compatibility adapters for external clients and older configuration until migration is complete.

### Stage 5 — dedicated Rust orchestrator

Move the control plane from Bash watchdogs into a single orchestrator daemon with an explicit state tree:

```text
clients
targets
zones
underlay leases
external adapters
leshy state
```

The `kk` CLI becomes a frontend to the orchestrator API. Shell remains useful for installation/recovery but no longer carries the steady-state control loop.

### Stage 6 — arbitrary N targets / N zones

Remove the fundamental two-role routing limit. Zones select named targets. Multiple managed clients and external targets can coexist without role-specific code.

### Stage 7 — cleanup and compatibility retirement

Once all managed targets use the new contract:

- remove managed-client ifindex flap inference;
- remove managed-client 3/3 readiness polling;
- remove managed-client endpoint pending markers;
- reduce route-watch to external/compatibility responsibilities;
- simplify health-watch to Leshy/control-plane health rather than guessing VPN protocol health;
- retain only compatibility shims still required by supported deployments.

## 12. Testing strategy

Tests are split by privilege and risk.

### Unit/state-machine tests

- no root;
- no `/dev/net/tun`;
- no sockets leaving the process unless explicitly mocked;
- deterministic time/backoff;
- deterministic backend and TUN doubles.

### TUN integration tests

Run inside a disposable Linux network namespace. The test namespace has no usable host default route. TUN creation, address setup, interface persistence and teardown happen only inside the namespace.

### Protocol interoperability tests

Use two or more disposable namespaces connected by a private veth pair:

```text
client-ns <---- private veth ----> server-ns
```

No namespace receives a route to the public Internet.

AWG2 interoperability must be tested against a pinned official/reference AmneziaWG implementation. VLESS/REALITY interoperability must be tested against a pinned reference Xray server. Tests cover reconnect/restart of the peer while asserting that the client TUN ifindex remains unchanged.

### Fault tests

Inject:

- peer disappearance;
- dropped packets;
- endpoint address change;
- transport reset;
- delayed handshake;
- process-local backend failure;
- simulated suspend gap by pausing timers/process scheduling;
- repeated reconnects.

The core assertion is always: **normal transport failure does not recreate the TUN and does not leak traffic to the physical/default route.**

## 13. Security model

- client config files containing secrets are root-owned and mode `0600`;
- runtime directories are root-owned;
- state snapshots contain only redacted public operational facts;
- Unix control sockets require local authorization/ownership checks;
- no imported client config is echoed by `status` or normal diagnostics;
- protocol endpoints are treated as sensitive operational metadata in debug bundles according to existing diagnostic policy;
- test keys are generated or fixed test-only values and are never production credentials.

## 14. Observability principles

A managed client should emit semantic reasons rather than forcing the orchestrator to infer them from kernel symptoms.

Examples:

```text
starting:tun-create
armed:waiting-underlay
connecting:handshake-started
reconnecting:handshake-timeout
reconnecting:transport-reset
degraded:retry-budget-window
failed:invalid-config
failed:tun-fatal
online:handshake-established
```

The reason taxonomy should be stable enough for diagnostics but not become a second protocol API.

## 15. Non-goals during early migration

- replacing NetworkManager/OpenConnect management;
- immediately replacing the Bash installer;
- immediately replacing Leshy;
- full compatibility with every Xray protocol;
- AWG3 as a Stage 0 release blocker;
- routing arbitrary public traffic from CI protocol tests.

## 16. Decision summary

The migration deliberately starts at the data plane, not the orchestrator. Stage 0 creates trustworthy clients. Stage 1 makes their facts visible. Later stages progressively replace inference with explicit contracts. This avoids rewriting the current working Bash control plane and the VPN data plane simultaneously.
