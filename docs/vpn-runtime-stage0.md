# Stage 0 plan — standalone managed VPN clients

Stage 0 deliberately changes only the VPN data plane. The existing Bash Kikimora remains the orchestrator and Leshy remains unchanged.

The purpose of Stage 0 is to produce independently testable, multi-instance managed VPN clients whose TUN lifetime is decoupled from protocol reconnects.

## 0. Scope

Production protocol targets:

1. AmneziaWG 2.x — first and primary backend.
2. VLESS + REALITY — second backend.

Planned but non-blocking:

- AmneziaWG 3.x extension.

External `vpn0` remains owned by NetworkManager and is explicitly outside the Stage 0 runtime.

## 1. Acceptance criteria

Stage 0 is complete only when all of the following are true.

### Runtime

- one `kikimora-vpn` binary runs multiple independent instances by config/name;
- every instance owns exactly one TUN;
- the TUN survives normal protocol reconnects;
- two or more managed instances can run simultaneously without shared mutable process state;
- process exit deletes only that instance's TUN;
- a backend failure cannot silently route traffic outside the TUN;
- no managed client subscribes to NetworkManager connectivity state;
- systemd restarts only on process failure, not on protocol disconnect.

### AmneziaWG 2.x

- imports/accepts AWG2 client parameters required by current deployments;
- supports WireGuard NoiseIK session behavior plus AWG2 obfuscation fields `Jc`, `Jmin`, `Jmax`, `S1-S4`, `H1-H4`, `I1-I5`;
- reconnect/rekey preserves the TUN;
- interoperates with a pinned reference AmneziaWG peer in isolated CI;
- endpoint loss and peer restart recover without interface recreation.

### VLESS + REALITY

- supports VLESS + REALITY client connection for the required profile subset;
- supports TCP traffic from the TUN and the UDP behavior required by the selected Rust core/profile subset;
- reconnects the outbound transport without recreating the TUN;
- interoperates with a pinned reference Xray server in isolated CI;
- peer restart recovers without interface recreation.

### Integration with existing Kikimora

- Bash Kikimora can be configured to treat `kk-awg0`, `kk-xray0`, etc. as ordinary primary/secondary interfaces;
- no existing route-watch/readiness code needs to understand the new runtime yet;
- Stage 0 installation is optional and does not alter existing deployments unless explicitly configured.

## 2. Repository layout

Proposed layout:

```text
linux/vpn-client/
  Cargo.toml
  src/
    lib.rs
    main.rs
    config.rs
    runtime.rs
    state.rs
    tun.rs
    backoff.rs
    backend.rs
    backends/
      mod.rs
      awg2.rs
      vless_reality.rs
      test_stub.rs
  tests/
    lifecycle.rs
    state_snapshot.rs

linux/files/
  kikimora-vpn@.service

linux/tests/vpn-client/
  netns-lib.sh
  tun-smoke.sh
  awg2-interop.sh
  vless-reality-interop.sh
  reconnect-preserves-tun.sh

.github/workflows/vpn-client.yml
```

The Rust crate is intentionally separate from the existing Bash CLI so it can be built, tested and versioned independently during migration.

## 3. Instance configuration

Stage 0 configuration should be explicit and intentionally small.

Common fields:

```toml
name = "awg-main"
protocol = "amneziawg2"
interface = "kk-awg0"
address = ["10.77.0.2/32"]
mtu = 1380
state_dir = "/run/kikimora/vpn/clients/awg-main"
```

Protocol secrets remain in the same root-only file or in a referenced root-only secret file. Stage 0 does not create a general secret-management subsystem.

The parser must reject:

- invalid instance names;
- duplicate/unsafe interface names;
- interface names over Linux IFNAMSIZ constraints;
- unsupported protocol values;
- malformed addresses;
- impossible MTU values;
- missing protocol-required keys;
- secret files that are group/world readable when strict mode is enabled.

## 4. Core runtime decomposition

### `TunDevice`

Responsible only for TUN lifecycle and packet I/O.

Conceptual interface:

```rust
trait TunDevice {
    fn name(&self) -> &str;
    fn ifindex(&self) -> Option<u32>;
    async fn recv(&mut self, buf: &mut [u8]) -> Result<usize>;
    async fn send(&mut self, packet: &[u8]) -> Result<()>;
}
```

A production Linux implementation creates `/dev/net/tun`. Tests use an in-memory implementation except namespace integration tests.

### `VpnBackend`

The backend owns the protocol transport, not the TUN.

Conceptual interface:

```rust
trait VpnBackend {
    async fn start(&mut self) -> Result<()>;
    async fn stop(&mut self) -> Result<()>;
    async fn send_packet(&mut self, packet: &[u8]) -> Result<()>;
    async fn recv_packet(&mut self, packet: &mut [u8]) -> Result<usize>;
    async fn tick(&mut self, now: Instant) -> Result<BackendEvent>;
    fn health(&self) -> BackendHealth;
    fn endpoint(&self) -> Option<EndpointFact>;
}
```

A backend cannot delete or recreate the TUN.

### Runtime state machine

The runtime owns the stable TUN and drives backend reconnects.

Minimum states:

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

`route_ready` becomes true when the TUN exists, has its configured address/MTU and the runtime has entered fail-closed packet handling. It does not depend on current handshake state.

## 5. Packet-flow invariant

Packets from the TUN must have exactly two outcomes while the backend is disconnected:

1. bounded queue, then transmit after recovery; or
2. drop with counters/reason.

They must never be reinjected into a host/default physical path.

Queue policy should be deliberately bounded by packets and bytes. The default should prefer dropping over unbounded memory growth.

## 6. TUN lifecycle

Creation sequence:

```text
parse config
validate secrets/config
create TUN
configure MTU/address
bring link UP
enter fail-closed packet loop
publish route_ready=true
start backend connection
```

Reconnect sequence:

```text
backend error
state=reconnecting
retain TUN and ifindex
apply reconnect backoff
recreate only backend session/socket
state=online when recovered
```

Fatal TUN failure sequence:

```text
TUN read/write fatal error
state=failed
process exits non-zero
systemd may restart the process
```

This is intentionally the rare path where a new TUN instance may appear.

## 7. Backoff and recovery

Reconnect policy belongs inside the process.

Requirements:

- bounded exponential backoff with jitter;
- reset after sustained successful connection;
- no process exit for ordinary endpoint/network failure;
- no NetworkManager state dependency;
- endpoint re-resolution at defined retry boundaries;
- observability of retry number and reason without exposing secrets.

Tests use an injected clock/random source so backoff behavior is deterministic.

## 8. AmneziaWG 2.x backend plan

### 8.1 Protocol boundary

Use a mature Rust WireGuard state machine/crypto implementation where possible, and isolate AWG2 packet transformation from the WireGuard session logic.

The preferred decomposition is:

```text
TUN packet
   -> WireGuard NoiseIK/session core
   -> normal WG datagram
   -> AWG2 encoder (J/S/H/I)
   -> UDP socket

UDP datagram
   -> AWG2 decoder
   -> normal WG datagram
   -> WireGuard session core
   -> TUN packet
```

This makes AWG2 behavior testable independently from cryptography and leaves a clean path for AWG3 extensions.

### 8.2 AWG2 fields

Support and validate:

- `Jc`, `Jmin`, `Jmax`;
- `S1`, `S2`, `S3`, `S4`;
- `H1`, `H2`, `H3`, `H4`;
- `I1`, `I2`, `I3`, `I4`, `I5`;
- standard WireGuard private/public/preshared keys;
- endpoint;
- allowed IPs needed by the tunnel backend;
- persistent keepalive.

AWG3-only fields such as Header Protection and Content Padding are parsed only after the AWG3 stage; Stage 0 must not silently pretend to support them.

### 8.3 Interop gate

A pinned reference peer is started in `server-ns`. `client-ns` runs `kikimora-vpn`.

The test verifies:

- handshake succeeds;
- encrypted traffic crosses the private veth only;
- an IP endpoint behind the tunnel is reachable;
- reference peer restart causes reconnect;
- client TUN ifindex before/after restart is identical;
- deleting the veth temporarily moves client state to reconnecting but does not delete TUN;
- restoring veth recovers the session.

## 9. VLESS + REALITY backend plan

### 9.1 Scope

Stage 0 intentionally supports the profile subset needed by Kikimora deployments rather than claiming full Xray-core compatibility.

Required first profile:

- VLESS;
- REALITY;
- configured server name/public key/short id/flow as required;
- selected transport required by the deployed profile;
- TUN packet ingress/egress through the Rust runtime.

### 9.2 Core selection

Prefer embedding a Rust implementation behind the `VpnBackend` boundary. The exact upstream crate/commit must be pinned. If the chosen Rust Xray core cannot provide a stable library boundary, an adapter may temporarily own a child Rust engine, but the architectural contract remains: transport restart cannot recreate the Kikimora TUN.

The dependency decision is recorded in code and the PR; floating `main` dependencies are forbidden.

### 9.3 Interop gate

A pinned reference Xray server runs in `server-ns`. The managed client runs in `client-ns`.

Verify:

- REALITY authentication/connection;
- traffic through the TUN;
- server process restart;
- client transport reconnect;
- unchanged TUN ifindex;
- no public/default-route escape from the namespace.

## 10. State snapshot in Stage 0

Stage 0 writes an operational snapshot even though Bash does not depend on it yet.

Minimum v0/internal fields:

```json
{
  "schema": 1,
  "name": "awg-main",
  "protocol": "amneziawg2",
  "generation": 3,
  "state": "reconnecting",
  "reason": "handshake-timeout",
  "route_ready": true,
  "interface": {
    "name": "kk-awg0",
    "ifindex": 42,
    "mtu": 1380
  }
}
```

Stage 1 freezes and documents the public contract. Stage 0 already follows the intended shape to avoid unnecessary churn.

Writes must use temp-file + fsync/rename semantics appropriate for `/run`, with mode `0644` or stricter depending on included fields. No secret data is permitted.

## 11. systemd model

Template unit:

```text
kikimora-vpn@.service
```

Expected behavior:

- `%i` selects `/etc/kikimora/vpn/clients/%i.toml`;
- `Restart=on-failure`;
- sensible restart delay for process crashes;
- `CAP_NET_ADMIN`/device access only as required for TUN setup;
- hardening must not block `/dev/net/tun` or runtime-state writes;
- stopping the unit is an explicit TUN teardown boundary.

No unit-level watchdog should restart the service merely because protocol health is reconnecting/degraded.

## 12. Compatibility with Bash Kikimora

Stage 0 uses the existing role/profile mechanism.

Example deployment:

```text
awg-main -> interface kk-awg0
xray-main -> interface kk-xray0
corporate external -> vpn0
```

The existing profile may select:

```text
PRIMARY_INTERFACE=kk-awg0
SECONDARY_INTERFACE=vpn0
```

or use `kk-xray0` as one of the existing two roles.

The current readiness logic still sees the interface like any other VPN. This is intentionally temporary and is removed for managed targets in Stage 2.

## 13. Test isolation design

Real network tests are allowed only inside disposable network namespaces.

### 13.1 Unit tests

No privileges and no networking side effects.

Cover:

- config validation;
- state transitions;
- reconnect/backoff;
- packet queue/drop policy;
- snapshot generation/redaction;
- backend errors;
- TUN fatal vs transport recoverable distinction;
- multiple runtime instances in one test process with no shared state.

### 13.2 Namespace TUN smoke

Create `client-ns` and execute the runtime inside it. Verify:

- TUN appears only in `client-ns`;
- root namespace has no test TUN;
- address/MTU are correct;
- state snapshot reports the same ifindex;
- process stop removes the namespace-local TUN.

### 13.3 Protocol namespaces

Create:

```text
client-ns 10.250.0.2/24
server-ns 10.250.0.1/24
```

connected by a veth pair.

Safety rules:

- no default route in either namespace;
- no NAT;
- no forwarding from the host/root namespace to public interfaces;
- reference services bind only inside `server-ns`;
- tests assert that the namespace route table contains only loopback, private veth and tunnel routes;
- cleanup trap deletes namespaces even on failure.

### 13.4 Fault injection

Faults are implemented by namespace operations, never by touching the runner host route table:

- `ip netns exec client-ns ip link set veth-client down`;
- kill/restart peer process in `server-ns`;
- change private endpoint IP inside the namespace;
- `tc netem` inside the private namespace where available;
- pause backend process/timers for resume-like gaps.

The test records TUN ifindex before and after every recoverable failure.

## 14. CI workflow

Jobs:

### `rust-unit`

```text
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test --all-targets
```

No sudo.

### `linux-netns`

Runs on a disposable Ubuntu GitHub runner with sudo only for namespace/TUN operations.

Checks root namespace before and after the test to ensure no `kk-*` test interface remains.

### `awg2-interop`

Build/pin reference implementation, then run entirely inside isolated namespaces.

### `vless-reality-interop`

Build/download a pinned reference test server and run entirely inside isolated namespaces.

Protocol jobs may initially be marked experimental while backend bring-up is underway, but Stage 0 cannot be declared complete until both are required and green.

## 15. Implementation sequence

### 0A. Runtime skeleton

- create crate;
- define config/state/backend/TUN abstractions;
- implement deterministic stub backend and in-memory TUN;
- implement state machine and bounded packet queues;
- unit tests.

### 0B. Linux TUN implementation

- `/dev/net/tun` creation;
- IFNAMSIZ-safe naming;
- address/MTU/link-up setup;
- ifindex reporting;
- namespace smoke test.

### 0C. Multi-instance/systemd packaging

- template unit;
- root-only config layout;
- two simultaneous stub/TUN instances in isolated namespace tests;
- no integration into default install path yet unless explicitly enabled.

### 0D. AWG2 backend

- WireGuard core adapter;
- AWG2 codec;
- config parser;
- timers/rekey/reconnect;
- reference interop;
- peer restart and underlay-loss fault tests.

### 0E. VLESS + REALITY backend

- pinned Rust core decision;
- config adapter;
- packet data path;
- reconnect semantics;
- reference interop;
- peer restart fault test.

### 0F. Bash compatibility/install commands

- optional installation of the binary/unit;
- documented manual client configs;
- examples mapping `kk-awg0`/`kk-xray0` into current Kikimora profiles;
- no replacement of existing watchdogs yet.

### 0G. Stage 0 hardening gate

- soak reconnect loops in namespace CI;
- state snapshot redaction review;
- memory/queue bounds;
- process crash cleanup;
- simultaneous AWG2 + VLESS instance test;
- documentation complete.

## 16. Explicit deferrals to Stage 1+

Stage 0 does **not** make Bash routing depend on `state.json`.

It does not add:

- event bus;
- control socket semantics beyond optional debug experiments;
- endpoint-underlay lease IPC;
- arbitrary named Leshy targets;
- Rust orchestrator;
- replacement of external `vpn0` heuristics.

This keeps Stage 0 focused on proving that the new clients are stable before they become control-plane dependencies.
