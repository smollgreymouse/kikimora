# Toad Stage 0 — official-core standalone clients

Stage 0 builds independently working Kikimora-managed **Toads** while preserving the existing Bash Kikimora + Leshy orchestration.

This is a stage-level contract, not an executor checklist. Detailed executor plans exist only for the next few known steps under `docs/toad-steps/` and must be revised after real results are returned.

Canonical roadmap: `docs/toad-roadmap.md`.
Canonical naming: `docs/toad-naming.md`.

## Goal

Deliver standalone managed VPN client instances that:

- run as `kikimora-toad`;
- use official protocol cores;
- expose stable route-target interfaces;
- survive ordinary peer/transport/underlay failures without recreating those interfaces;
- can run several managed clients at the same time;
- remain compatible with the current Bash Kikimora and Leshy during migration.

Stage 0 does **not** replace the Bash orchestrator or Leshy.

## Core protocol decision

Use official upstream implementations as dependencies:

- AmneziaWG: `amnezia-vpn/amneziawg-go`, tag `v3.1.20260828`, commit `b5928efb6ca19f0153958460c3d141f04abc5c2e`;
- Xray: `XTLS/Xray-core`, release `v26.7.28`, commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`.

Kikimora/Toad code must not independently implement AWG2 crypto/framing or VLESS/REALITY/Vision protocol behavior.

## Runtime/process model

One Toad process owns one managed VPN instance.

```text
kikimora-toad@awg-main.service  -> kk-awg0
kikimora-toad@xray-main.service -> kk-xray0
kikimora-toad@awg-backup.service -> kk-awg1
```

An external NetworkManager/OpenConnect interface such as `vpn0` is not a Toad.

A crash, stop, or reconnect of one Toad must not recreate or stop another Toad.

## Cross-platform rule

The runtime is not Linux-only.

Common code lives under:

```text
toad/
```

Common packages include:

- config;
- state;
- backend interfaces;
- lifecycle/control logic;
- protocol adapters where the upstream API is portable.

OS-specific TUN, privileges, service integration and packaging stay behind platform-specific code/build tags.

Linux is implemented first for real network integration because Linux network namespaces provide a safe hermetic test environment. This does not change the cross-platform architecture.

## Responsibility boundary

### Toad owns

- normalized instance config;
- instance identity;
- lifecycle/shutdown;
- state snapshot publication;
- platform attachment boundary;
- protocol-core construction/configuration;
- health sampling from official-core facts;
- stable route-target interface policy;
- later share-link normalization/import.

### Official core owns

- protocol crypto;
- handshakes;
- rekey;
- AWG2 framing/obfuscation;
- VLESS;
- REALITY/Vision;
- normal protocol transport/session recovery.

### Kikimora/Leshy own

- desired start/stop policy;
- route-target publication;
- endpoint underlay policy;
- routing/DNS policy;
- migration compatibility with current primary/secondary concepts.

## Stable-TUN invariant

Ordinary network failure is not an interface-lifecycle event.

The following must not by themselves recreate the managed TUN:

- handshake timeout;
- peer/server restart;
- rekey;
- temporary packet loss;
- underlay link down/up;
- suspend/resume;
- VLESS/REALITY connection reset;
- NetworkManager connectivity-state changes.

The managed TUN remains a fail-closed route target while transport recovers.

## AWG2 implementation boundary

For Linux, Toad owns the kernel TUN fd and duplicates it for official `amneziawg-go`.

```text
Toad-owned kk-awg0
    |
    +-- owner fd retained
    |
    +-- dup fd
          |
          v
amneziawg-go/tun.CreateTUNFromFile
          |
          v
amneziawg-go/device.NewDevice
```

The protocol attachment may be closed/recreated only when lifecycle semantics require it; closing the duplicate must not remove the Toad-owned interface.

Configuration is applied through official device/UAPI semantics. Health uses official facts such as handshake timestamp, RX/TX counters and endpoint.

The final AWG2 gate must use real non-default AWG2 J/S/H/I settings. Ordinary WireGuard defaults are not an acceptable production gate.

## Xray implementation boundary

Use original Xray-core as a Go dependency.

Target lifecycle:

```text
Toad normalized VLESS config
    -> minimal Xray config
    -> serial.LoadJSONConfig
    -> core.New
    -> Instance.Start
```

Use the official Xray TUN inbound/official data-plane facilities available at the pinned revision. Do not enable Xray automatic system routing; Leshy/Kikimora remain routing authority.

Normal VLESS/REALITY transport failures must recover without restarting the Toad process or recreating its route-target interface.

Detailed Xray executor plans are intentionally deferred until the current AWG work is reviewed.

## Normalized configuration

Per-instance TOML remains the Stage 0 configuration format.

Example AWG2 shape:

```toml
name = "awg-main"
protocol = "amneziawg2"
interface = "kk-awg0"
address = ["10.40.0.2/32"]
mtu = 1380
state_dir = "/run/kikimora/vpn/clients/awg-main"

[awg2]
private_key = "..."
peer_public_key = "..."
endpoint = "example.net:443"
allowed_ips = ["0.0.0.0/0", "::/0"]
persistent_keepalive = 25
jc = 4
jmin = 40
jmax = 80
s1 = 15
s2 = 15
s3 = 15
s4 = 15
h1 = "1001"
h2 = "1002"
h3 = "1003"
h4 = "1004"
i1 = "..."
```

Example VLESS/REALITY shape:

```toml
name = "xray-main"
protocol = "vless-reality"
interface = "kk-xray0"
address = ["10.41.0.2/30"]
mtu = 1380
state_dir = "/run/kikimora/vpn/clients/xray-main"

[vless_reality]
endpoint = "example.net:443"
uuid = "..."
server_name = "..."
public_key = "..."
short_id = "..."
flow = "xtls-rprx-vision"
fingerprint = "chrome"
transport = "raw"
```

Secrets never enter normal logs or state snapshots.

## Share-link requirement

Before Stage 0 completion, Toad import must accept:

- `wg://`;
- `awg://`;
- `amneziawg://`;
- `wireguard://`;
- `vless://`.

Import output is normalized Toad TOML.

WG/AWG import must support standard/URL-safe base64, padded/unpadded payloads and preserve AWG2 fields. Executable/routing directives such as `PreUp`, `PostUp`, `PreDown`, `PostDown`, and `Table` are rejected.

VLESS import preserves the required REALITY/Vision parameters including endpoint, UUID, `pbk`, `sni`, `sid`, `fp`, `flow`, transport and `spx` when present.

Stdin import is required so secret links do not have to appear in argv/history.

## State snapshot

Authoritative per-instance snapshot target:

```text
/run/kikimora/vpn/clients/<name>/state.json
```

Schema v1 separates transport/session health from route-target readiness.

Expected coarse states:

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

Stage 0 does not yet make this state routing-authoritative for all existing Bash logic. That migration happens later.

## CI safety model

Protocol acceptance tests must never use the runner's public data path.

### Unit/cross-platform

Run common Go checks on Linux, macOS and Windows.

### Real Linux TUN

Run in a disposable network namespace with no default route. Prove TUN creation/configuration, duplicate-fd ownership semantics, stable ifindex, cleanup and no root-namespace leak.

### AWG2 interop

Two private namespaces connected only by veth. Use official pinned reference implementation. Require:

- real AWG2 handshake;
- real encrypted traffic;
- advancing counters;
- server restart recovery;
- underlay loss/restore recovery;
- no Toad process restart;
- unchanged client TUN ifindex;
- no NAT/default/public route.

### Xray interop

Use official pinned Xray-core server with local REALITY decoy and local tunneled target. Require real payload return, Vision when configured, server/underlay recovery, stable interface and fail-closed behavior.

### Multi-Toad

Run AWG2 and Xray simultaneously. Failure of one must not affect the other's process, interface or traffic.

## Current implementation horizon

Only these three detailed packets are active now:

1. `docs/toad-steps/01-platform-linux-tun.md`;
2. `docs/toad-steps/02-awg2-official-core.md`;
3. `docs/toad-steps/03-awg2-isolated-interop.md`.

Do not write detailed Xray packets until the results of step 03 are returned and reviewed.

## Stage 0 final gate

Stage 0 will eventually require, on the same production revision:

- cross-platform format/vet/unit checks;
- real TUN test;
- real official AWG2 interop and recovery;
- real official Xray VLESS/REALITY/Vision interop and recovery;
- stable interfaces through ordinary failures;
- simultaneous AWG2 + Xray operation;
- share-link import;
- secret redaction;
- compatibility with current Bash/Leshy routing model;
- no public/default route in protocol CI namespaces;
- no host-interface leakage.

Do not mark Stage 0 complete if either protocol gate is mocked, skipped, reduced to process startup, or passes only by recreating the Toad/TUN.
