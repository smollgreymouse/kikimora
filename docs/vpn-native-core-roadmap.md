# Kikimora managed VPN roadmap — official protocol cores

This is the **living roadmap and handoff document** for the production managed-VPN path in PR #27 / branch `feat/native-core-vpn-clients`.

It must be updated whenever an architectural decision, implementation stage, upstream pin, acceptance criterion, or major blocker changes. The goal is that another ChatGPT conversation, Codex session, IDE agent, or human contributor can continue the work from the repository without reconstructing decisions from chat history.

The Rust-native research path is intentionally isolated in PR #25. Its roadmap and experimental findings remain there.

## 0. Current status

Production direction:

- client/runtime language: **Go**;
- AmneziaWG protocol engine: official `amnezia-vpn/amneziawg-go`;
- Xray protocol engine: official `XTLS/Xray-core`;
- Stage 0 orchestrator: existing Bash Kikimora;
- routing/DNS policy: existing Leshy;
- external corporate VPN: NetworkManager/OpenConnect `vpn0`, externally owned;
- future GUI target: native desktop UI broadly inspired by `goxray/desktop`, likely Fyne + tray, but implemented as a frontend to the Kikimora control plane rather than as the VPN owner.

Pinned protocol revisions at the start of Stage 0:

- AmneziaWG: tag `v3.1.20260828`, commit `b5928efb6ca19f0153958460c3d141f04abc5c2e`;
- Xray-core: release `v26.7.28`, commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`.

Current implementation state:

- [x] production branch split from Rust-native PR #25;
- [x] detailed Stage 0 plan in `docs/vpn-native-core-stage0.md`;
- [x] upstream APIs investigated for embedded official cores;
- [ ] Go runtime skeleton committed;
- [ ] AWG2 official-core backend working;
- [ ] AWG2 isolated reference interop green;
- [ ] Xray official-core backend working;
- [ ] VLESS/REALITY/Vision isolated reference interop green;
- [ ] simultaneous AWG2 + Xray gate green;
- [ ] Stage 0 complete.

No protocol backend is considered production-ready until its real isolated client/server data-plane gate is green.

## 1. Product topology

The target is not a single desktop VPN connection. Kikimora must support several simultaneously active managed VPNs plus external system-owned VPNs.

Example:

```text
managed awg-main       -> kikimora-vpn@awg-main      -> kk-awg0
managed xray-main      -> kikimora-vpn@xray-main     -> kk-xray0
managed awg-backup     -> kikimora-vpn@awg-backup    -> kk-awg1
managed xray-backup    -> kikimora-vpn@xray-backup   -> kk-xray1
external corporate     -> NetworkManager/OpenConnect -> vpn0
```

Leshy ultimately routes by named target:

```text
routing zone -> named target -> route-ready interface
```

Examples:

```text
blocked -> awg-main  -> kk-awg0
ai      -> xray-main -> kk-xray0
work    -> corporate -> vpn0
```

`primary` / `secondary` remain compatibility concepts during early migration only.

## 2. Hard architectural invariants

These rules are stronger than implementation details and should not be weakened by later refactors.

1. **Protocol correctness comes from official cores.**
   Kikimora must not reimplement AWG2 framing/crypto or VLESS/REALITY/Vision unless there is a separately justified future project.

2. **One managed VPN instance is independently restartable.**
   A fault in one instance must not terminate or recreate unrelated managed VPNs.

3. **Ordinary transport recovery must not recreate the route-target TUN.**
   Handshake timeout, server restart, REALITY connection reset, rekey, temporary packet loss, suspend/resume, or underlay down/up are protocol/session events, not reasons to destroy the managed interface.

4. **Managed TUNs are fail-closed.**
   When a protocol is unavailable, traffic routed to its TUN may queue/drop/fail, but must not silently escape through the physical default route.

5. **NetworkManager connectivity-state changes are not VPN reconnect commands.**
   `CONNECTED_SITE` / `CONNECTED_GLOBAL` and connectivity-check changes must never reproduce the Amnezia desktop failure mode where a state transition causes full protocol teardown/recreation.

6. **systemd supervises process death, not normal network health.**
   `Restart=on-failure` is for crash/fatal process exit. Normal peer/server outages are handled by the official protocol core and wrapper state machine.

7. **Client reports facts; orchestrator decides policy.**
   Protocol/client state is reported explicitly. Kikimora decides desired state, routing target publication, endpoint underlay policy, and Leshy policy.

8. **Secrets never appear in state snapshots or normal logs.**

9. **External VPNs remain externally owned.**
   `vpn0` is observed and routed through, not activated/deactivated by managed-client code in this roadmap.

10. **Repository documents are the continuity source.**
    Changes to plans/status must be reflected in this roadmap and the current stage document before a stage is considered complete.

## 3. Runtime process model

Stage 0 uses one executable with independent per-instance processes:

```text
kikimora-vpn@awg-main.service
kikimora-vpn@xray-main.service
kikimora-vpn@awg-backup.service
```

One process = one protocol instance = one route-target TUN.

Longer term the processes are controlled by a central Kikimora orchestrator, but they remain independently supervised.

## 4. Official-core boundaries

### 4.1 AmneziaWG

Use official packages from `amneziawg-go`:

```text
Kikimora Linux TUN owner
        |
        +-- retained owner fd
        |
        +-- duplicated fd
                |
                v
amneziawg-go/tun.CreateTUNFromFile
                |
                v
amneziawg-go/device.NewDevice
                |
                v
conn.NewDefaultBind
```

Configuration should be applied through official device/UAPI semantics.

The wrapper should sample official UAPI state for:

- latest handshake;
- RX/TX bytes;
- endpoint;
- fatal device errors.

AWG2 Stage 0 fields include private/public/preshared keys, endpoint, AllowedIPs, keepalive, `Jc/Jmin/Jmax`, `S1-S4`, `H1-H4`, and `I1-I5`.

AWG3 is future work; architecture must not block it.

### 4.2 Xray

Embed official `github.com/xtls/xray-core` packages.

Lifecycle target:

```text
normalized Kikimora VLESS config
        |
        v
minimal Xray config
        |
        v
serial.LoadJSONConfig
        |
        v
core.New
        |
        v
Instance.Start
```

Use the official Xray TUN inbound. It supports Linux L3 TUN input and allows OS-level routing to be managed externally. Do **not** enable Xray automatic system routing; Leshy/Kikimora decide which destinations are routed into the interface.

The important lifecycle rule is that normal VLESS/REALITY reconnect remains inside the live Xray `core.Instance`; Kikimora must not close/recreate the instance merely because its upstream transport temporarily fails.

A stricter external-fd TUN ownership boundary may be pursued later if upstream exposes an appropriate API, but it is not a Stage 0 blocker as long as transport recovery preserves the same Xray TUN identity.

## 5. Configuration and import

Normalized per-instance configuration lives under:

```text
/etc/kikimora/vpn/clients/<name>.toml
```

Required share-link imports:

- `wg://`;
- `awg://`;
- `amneziawg://`;
- `wireguard://`;
- `vless://`.

WG/AWG import:

- standard and URL-safe base64;
- padded/unpadded;
- `[Interface]` / `[Peer]` parsing;
- preserve AWG2 fields;
- reject `PreUp`, `PostUp`, `PreDown`, `PostDown`, `Table`, and arbitrary executable/routing directives.

VLESS import must preserve at least UUID, endpoint, `security=reality`, `pbk`, `sni`, `sid`, `fp`, `flow`, transport type, and `spx` when present.

Import via stdin must be supported so secret links do not have to appear in process argv/history.

## 6. State and future IPC

Per-instance authoritative snapshot target:

```text
/run/kikimora/vpn/clients/<name>/state.json
```

State model separates interface routability from transport health:

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

and independently:

```text
route_ready=true|false
```

A managed TUN may remain `route_ready=true` while transport state is `reconnecting`, because it remains a fail-closed route target.

Future IPC:

```text
/run/kikimora/vpn/clients/<name>/control.sock
/run/kikimora/vpn/events.sock
```

Snapshots are authoritative; events are advisory wakeups.

Control commands eventually include semantic operations such as `status`, `reconnect`, `reload`, `re-resolve`, `dump-debug`, and `shutdown`. `reconnect` means protocol/session recovery, not systemd process restart.

## 7. Development stages

### Stage 0 — standalone official-core clients

Detailed execution plan: `docs/vpn-native-core-stage0.md`.

Goal: independently working AWG2 and VLESS/REALITY managed clients, still orchestrated by existing Bash Kikimora.

Acceptance:

- real TUN operation;
- official AWG2 core;
- official Xray core;
- real isolated AWG2 client/server traffic;
- real isolated VLESS/REALITY/Vision traffic;
- peer restart recovery;
- private-underlay down/up recovery;
- unchanged TUN ifindex through normal transport recovery;
- simultaneous AWG2 and Xray;
- import from share links;
- Bash/Leshy compatibility.

### Stage 1 — state contract in shadow/observability mode

Freeze state schema v1.

Existing Bash `status`, `diag`, and `doctor` consume managed-client snapshots for visibility, but existing readiness/reconcile remains routing-authoritative.

This validates the new explicit protocol facts in real use without changing routing correctness.

### Stage 2 — remove heuristic readiness for managed clients

For managed clients, Bash trusts explicit `route_ready`, instance identity, and state rather than reconstructing health from:

- interface appearance polling;
- 3/3 readiness streaks;
- same-name ifindex recreation inference;
- protocol-process discovery.

External NetworkManager VPNs continue to use the old observation path.

### Stage 3 — event bus and semantic control sockets

Add Unix-domain IPC.

Orchestrator reacts immediately to client transitions, but always rereads snapshots as source of truth.

Replace `systemctl restart` as a normal reconnect mechanism with client semantic commands.

### Stage 4 — explicit endpoint-underlay leases

Replace managed-client pending files/polling with:

```text
client resolves endpoint
        -> endpoint.prepare
orchestrator pins/verifies physical route
        -> endpoint.granted
client dials
```

This prevents tunnel recursion and removes much of the current endpoint-watch machinery.

### Stage 5 — dedicated Kikimora orchestrator daemon

Move steady-state control logic from Bash watchdog/reconcile scripts into an explicit control-plane daemon.

State tree:

```text
clients
targets
zones
external targets
underlay leases
Leshy state
```

The existing `kk` CLI becomes a frontend to this API. Shell remains installation/recovery tooling.

### Stage 6 — N targets and N zones

Remove the hard `primary`/`secondary` routing model.

Examples:

```text
awg-main
xray-main
awg-backup
xray-backup
corporate
```

Leshy zones map to named targets. Multiple zones may share one target, and multiple VPNs may remain connected simultaneously.

### Stage 7 — desktop GUI and tray

Target a desktop experience broadly similar to `goxray/desktop`:

- Fyne is the leading GUI candidate;
- system tray integration;
- server/profile list;
- import from clipboard/file/share link;
- per-client status and traffic counters;
- start/stop/reconnect controls;
- warnings for degraded clients/underlay;
- future routing-zone/target assignment UI.

Important difference from `goxray/desktop`: Kikimora must support multiple simultaneously active VPN clients. Therefore the GUI must **not** encode a single-active-connection model.

Preferred boundary:

```text
Fyne GUI / tray
       |
       v
Kikimora orchestrator API
       |
       +--> awg-main process
       +--> xray-main process
       +--> awg-backup process
       +--> external vpn0 adapter
       |
       v
Leshy policy
```

The GUI is a control/observation frontend. It must not directly own protocol cores or destroy VPN processes when the window/tray restarts.

Useful `goxray/desktop` design ideas to reuse conceptually:

- thin client-facing interface around connect/disconnect/status/counters;
- Fyne UI separated from connection item model;
- tray as first-class UI;
- link import at the UI boundary;
- traffic counter/graph presentation.

Things **not** to copy:

- single active connection invariant;
- UI process being the owner whose exit necessarily disconnects the VPN;
- protocol-specific state leaking into widgets;
- routing policy living in the GUI.

### Stage 8 — cleanup of compatibility watchdogs

After managed targets use explicit client state and IPC:

- remove managed-client ifindex flap inference;
- remove managed-client 3/3 readiness polling;
- remove managed-client endpoint pending markers;
- reduce route-watch to external/compatibility responsibilities;
- keep systemd focused on process supervision;
- keep heuristic observation only for truly external black-box VPNs.

## 8. Testing strategy

### Unit

No privileges/network:

- config validation;
- share-link parsing;
- state serialization/redaction;
- lifecycle/state-machine logic;
- failure classification.

### Real TUN

Disposable Linux network namespace, no public/default route.

Verify creation, addresses/MTU, stable ifindex, cleanup, and no host-interface leakage.

### AWG2 interop

Two isolated namespaces connected only by private veth. Official `amneziawg-go` reference peer/server. Real encrypted traffic required.

Test handshake, ICMP/data traffic, RX/TX counters, peer restart, underlay loss/restore, and stable client TUN identity.

### Xray interop

Isolated namespaces with official Xray-core server, local REALITY decoy, and local tunneled HTTP/echo target. No public routing.

Test VLESS+REALITY authentication, real payload return, Vision, server restart, underlay loss/restore, fail-closed behavior, and stable client TUN identity.

### Multi-client

AWG2 and Xray simultaneously. Failure/restart of either must not affect the other.

## 9. Documentation/handoff discipline

Before ending a substantial implementation session:

1. update **Current status** in this file;
2. update the active stage execution document with completed/open items;
3. record any upstream pin changes with exact tag + commit;
4. record failed approaches only when they materially affect future decisions;
5. keep PR body aligned with the current production direction;
6. never leave the only copy of a decision in chat.

When handing off to another agent/session, the minimum reading order is:

1. `docs/vpn-native-core-roadmap.md`;
2. `docs/vpn-native-core-stage0.md` (or current stage document);
3. PR #27 description and CI status;
4. relevant implementation/tests.

## 10. Immediate next actions

1. Commit the Go Stage 0 skeleton (`linux/vpn-native/`): config, validation, state writer, CLI, unit CI.
2. Implement Linux TUN owner and official `amneziawg-go` adapter.
3. Make real isolated AWG2 reference interop green before moving its status to working.
4. Embed official Xray-core with TUN inbound and VLESS/REALITY/Vision config generation.
5. Make real isolated Xray interop green.
6. Add simultaneous-client gate.
7. Finish share-link import and Bash compatibility.
8. Mark Stage 0 complete only when all required gates are green.
