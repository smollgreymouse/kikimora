# Stage 0 production plan — Kikimora managed VPN clients on official protocol cores

This document defines the production successor to the Rust-native experiment in PR #25.

The purpose of Stage 0 is unchanged: build stable standalone managed VPN clients that can be orchestrated by the existing Bash Kikimora and routed by the existing Leshy. What changes is the protocol-engine decision. Stage 0 will not implement AWG2 or VLESS/REALITY protocol machinery itself.

## 1. Production protocol decision

Use official upstream cores as pinned dependencies:

- **AmneziaWG:** `amnezia-vpn/amneziawg-go` tag `v3.1.20260828`, commit `b5928efb6ca19f0153958460c3d141f04abc5c2e`.
- **Xray:** `XTLS/Xray-core` release `v26.7.28`, commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`.

The client wrapper is our code, but cryptography, handshake, AWG2 wire format, VLESS, REALITY, Vision and transport protocol behavior come from the official projects.

This deliberately trades a larger Go dependency graph for protocol correctness and faster production delivery.

## 2. Separation from the Rust-native experiment

PR #25 remains the Rust-native research branch with its roadmap and current interop failures.

This branch starts directly from `master` and must not inherit Rust-native protocol code or commit history.

Useful architectural findings from the experiment are retained as requirements, not copied implementation:

- one independent process per managed VPN instance;
- stable TUN across ordinary transport recovery;
- session health separate from route readiness;
- no NetworkManager `CONNECTED_GLOBAL` reconnect trigger;
- real isolated reference interop tests;
- multi-instance operation;
- current Bash Kikimora remains the orchestrator during Stage 0.

## 3. Process model

Stage 0 uses a single Go executable with protocol-specific backends:

```text
kikimora-vpn-native@awg-main.service
kikimora-vpn-native@xray-main.service
kikimora-vpn-native@awg-backup.service
kikimora-vpn-native@xray-backup.service
```

Each process owns exactly one managed VPN instance.

Example topology:

```text
awg-main      -> kk-awg0
xray-main     -> kk-xray0
awg-backup    -> kk-awg1
xray-backup   -> kk-xray1
corporate     -> vpn0     # external NetworkManager/OpenConnect, unchanged
```

A crash or restart of one managed instance must not affect any other instance.

## 4. Responsibility boundary

### Client wrapper owns

- instance config parsing;
- instance name and interface name;
- lifecycle and shutdown;
- state snapshot publication;
- protocol-core start/stop boundaries;
- reconnect policy only where the official core does not already recover internally;
- health sampling from official-core state;
- import normalization later in Stage 0;
- systemd process integration.

### Official protocol core owns

- protocol cryptography;
- handshakes;
- rekey;
- AWG2 framing/obfuscation;
- VLESS protocol;
- REALITY/Vision;
- protocol sockets and normal transport/session recovery.

### Existing Bash Kikimora owns

- start/stop policy at deployment level;
- old primary/secondary readiness orchestration during Stage 0;
- endpoint-underlay routing until the later explicit lease stage;
- Leshy configuration;
- diagnostics/user-facing status integration later in Stage 1.

## 5. TUN lifecycle invariant

The essential Stage 0 invariant remains:

> ordinary transport failure must not cause the managed route target interface to disappear and reappear.

The exact ownership mechanism differs by official core.

### 5.1 AmneziaWG

`amneziawg-go` exposes `tun.CreateTUNFromFile` / `CreateUnmonitoredTUNFromFD` and `device.NewDevice`.

Preferred boundary:

1. Kikimora wrapper creates the Linux TUN and configures interface name/address/MTU.
2. Wrapper keeps an owner fd for the lifetime of the process.
3. Wrapper duplicates the TUN fd.
4. The duplicate is wrapped by official `amneziawg-go/tun` and passed into `device.NewDevice`.
5. The official device can close its duplicate when protocol state is stopped, while the wrapper's owner fd keeps the kernel TUN identity alive.
6. Normal handshake/rekey/recovery should normally happen inside the official device without stopping it at all.

No fork of `amneziawg-go` is planned for Stage 0.

### 5.2 Xray

Current Xray-core has an official TUN inbound. Unlike `amneziawg-go`, its public TUN config does not expose an externally supplied Linux TUN fd.

For the fastest reliable Stage 0 implementation, Xray-core is embedded in the Kikimora client process as a Go library and its official TUN inbound owns the data-plane attachment while the outer Kikimora process owns the **lifecycle decision**.

The wrapper must never restart the Xray core merely because a VLESS/REALITY connection is lost. The Xray `core.Instance` remains alive; transport recovery stays inside Xray. Therefore the official TUN inbound remains stable during ordinary server/underlay failures.

A later refinement may add an external-fd adapter if Xray exposes one upstream or if strict fd ownership becomes necessary. This is not a Stage 0 blocker because the failure mode we are removing is transport-driven full client teardown/recreation.

The Xray config must not enable automatic system routing. Leshy/Kikimora remain responsible for which destinations are routed into the managed TUN.

## 6. AmneziaWG2 backend design

The AWG backend is a thin adapter around official packages:

```text
our instance config
      |
      v
our Linux TUN owner
      |
      +-- owner fd retained by wrapper
      |
      +-- dup fd
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

Configuration is applied through the official device/UAPI implementation rather than duplicating AWG parsing logic.

Required AWG2 fields:

- private key;
- peer public key;
- optional preshared key;
- endpoint;
- allowed IPs;
- persistent keepalive;
- `Jc`, `Jmin`, `Jmax`;
- `S1`, `S2`, `S3`, `S4`;
- `H1`, `H2`, `H3`, `H4`;
- `I1`..`I5`.

AWG3-specific fields are accepted only when explicitly implemented later; they are not a Stage 0 release criterion.

### AWG health

Health must come from official device/UAPI state, especially:

- latest handshake timestamp;
- RX/TX byte counters;
- current endpoint;
- device/core fatal errors.

No interface-flap inference is used for protocol health.

## 7. Xray VLESS/REALITY backend design

The Xray backend embeds official `github.com/xtls/xray-core` packages.

Lifecycle:

```text
our normalized client config
      |
      v
generate minimal Xray JSON/core config
      |
      v
serial.LoadJSONConfig
      |
      v
core.New(config)
      |
      v
Instance.Start()
```

The generated config contains only the features required for this managed client:

- one TUN inbound;
- explicit interface name such as `kk-xray0`;
- explicit MTU/address/gateway;
- no Xray auto-system-routing tables;
- one VLESS outbound;
- REALITY settings from the imported/profile config;
- Vision flow when requested;
- Freedom only where required internally by the test/reference configuration, never as a bypass route for managed client traffic.

Normal outbound connection failures do not cause `Instance.Close()` / `core.New()` cycles.

### Xray health

Stage 0 needs only robust coarse states:

- process/core started;
- TUN created;
- at least one successful real tunneled probe for readiness;
- transport failure/recovery observed through test traffic and core logs/events where available.

Do not claim `online` simply because `core.Start()` succeeded.

For production runtime without active synthetic probes, Stage 1 can improve observability through Xray stats/metrics APIs. Stage 0 routing compatibility can still use the existing Bash interface readiness logic while isolated protocol tests prove real traffic.

## 8. Configuration

Proposed Stage 0 config:

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
i1 = "<r 8>"
```

and:

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

Secrets must never be copied into state snapshots or normal logs.

## 9. Share-link import

Stage 0 retains the user requirement to add servers from links.

Required inputs:

- `wg://`;
- `awg://`;
- `amneziawg://`;
- `wireguard://`;
- `vless://`.

Import output is normalized Kikimora TOML.

WG/AWG payload handling:

- base64 standard or URL-safe;
- padded or unpadded;
- parse `[Interface]` / `[Peer]`;
- reject `PreUp`, `PostUp`, `PreDown`, `PostDown`, `Table` and arbitrary shell/routing directives;
- retain AWG2 parameters.

VLESS/REALITY import must preserve at least:

- UUID;
- endpoint;
- `security=reality`;
- `pbk`;
- `sni`;
- `sid`;
- `fp`;
- `flow`;
- transport type;
- `spx` when present.

CLI should support stdin import so secrets need not appear in shell history/process argv.

## 10. State snapshot

Keep the same stable external schema concept used in the experiment:

```json
{
  "schema": 1,
  "name": "awg-main",
  "protocol": "amneziawg2",
  "generation": 1,
  "state": "online",
  "reason": "handshake-established",
  "route_ready": true,
  "interface": {
    "name": "kk-awg0",
    "ifindex": 42,
    "mtu": 1380
  },
  "session": {
    "connected": true,
    "last_handshake_age_ms": 1800
  }
}
```

Stage 0 does not make this snapshot routing-authoritative. Existing Bash readiness remains authoritative until Stage 2.

## 11. Systemd

Use a template unit:

```text
kikimora-vpn-native@awg-main.service
kikimora-vpn-native@xray-main.service
```

Rules:

- `Restart=on-failure`;
- no scheduled restart for ordinary handshake/transport failures;
- one config file per instance;
- independent process cgroups;
- minimal required capabilities;
- secrets readable only by the service account/root;
- no dependency on NetworkManager connectivity state.

## 12. CI safety model

No protocol test may use the runner's public network data path.

### Unit tests

- config validation;
- share-link import;
- state snapshot redaction;
- malformed secrets/keys;
- lifecycle state transitions.

### Real TUN smoke

One disposable network namespace proves:

- TUN creation;
- address/MTU;
- stable ifindex while process lives;
- cleanup;
- no interface leakage into the host namespace.

### AWG2 reference interop

Two namespaces connected only by private veth:

```text
client-ns 10.250.0.2 <----> 10.250.0.1 server-ns
     |                              |
 kk-awg0                       reference AWG
10.77.0.2                       10.77.0.1
```

Server is official pinned `amneziawg-go`/`amneziawg-tools`.

Required assertions:

1. real AWG2 handshake;
2. real ICMP traffic over encrypted tunnel;
3. RX/TX counters advance on both sides;
4. server restart recovers;
5. client private-veth underlay down/up recovers;
6. managed TUN ifindex remains unchanged through both failures;
7. no default route/NAT/public reachability exists in either namespace;
8. production AWG2 `J/S/H/I` values are enabled in the final gate.

This gate must be green before AWG2 is called working.

### Xray VLESS/REALITY reference interop

Two namespaces connected only by private veth.

Server namespace contains:

- official pinned Xray-core server;
- local REALITY decoy;
- local tunneled HTTP/echo target.

Required assertions:

1. VLESS + REALITY auth succeeds;
2. real application payload reaches target and returns;
3. Vision is tested when configured;
4. Xray reference restart recovers without recreating client TUN;
5. private underlay down/up recovers without recreating client TUN;
6. no public/default route/NAT exists;
7. the tunnel is fail-closed while reference/underlay is unavailable.

This gate must be green before VLESS/REALITY is called working.

## 13. Simultaneous-client gate

After both independent protocol gates are green, run both clients at once in one client namespace (or separate client namespaces sharing a simulated underlay):

```text
kk-awg0   -> AWG reference
kk-xray0  -> Xray reference
```

Assert:

- both TUNs exist simultaneously;
- traffic sent explicitly through each reaches only its matching reference target;
- restarting AWG reference does not affect Xray traffic/interface;
- restarting Xray reference does not affect AWG traffic/interface;
- both recover independently;
- neither introduces a public/default route.

## 14. Compatibility with current Bash Kikimora

Stage 0 deliberately avoids replacing current orchestration.

The old Bash stack should be able to use:

```text
primary.dev   -> kk-awg0
secondary.dev -> vpn0
```

or:

```text
primary.dev   -> kk-xray0
secondary.dev -> vpn0
```

and later multiple managed interfaces as the routing model evolves.

No Stage 0 change should require rewriting Leshy.

## 15. Implementation sequence

### Slice A — skeleton

- create `linux/vpn-native/` Go module;
- config types and validation;
- state snapshot writer;
- template systemd unit;
- no protocol implementation yet;
- unit CI.

### Slice B — AWG2 official core

- Linux TUN owner;
- duplicate fd boundary;
- `amneziawg-go` device adapter;
- UAPI config generation;
- state sampling;
- isolated official-reference interop;
- restart/underlay recovery.

### Slice C — Xray official core

- minimal Xray TUN/VLESS/REALITY config generation;
- `serial.LoadJSONConfig`;
- `core.New` / `Start` / controlled `Close`;
- no automatic system routes;
- isolated official-reference interop;
- restart/underlay recovery.

### Slice D — import

- WG/AWG links;
- VLESS links;
- secure atomic output;
- stdin mode;
- regression vectors.

### Slice E — simultaneous + soak

- AWG2 and Xray concurrently;
- repeated server/underlay failures;
- independent recovery;
- stable interfaces;
- resource/process cleanup.

### Slice F — Bash compatibility documentation/test

- demonstrate old primary/secondary orchestration using the new interfaces;
- no change to routing authority yet.

## 16. Stage 0 completion gate

Stage 0 is complete only when all of the following are green on the same commit:

- Go format/vet/unit tests;
- ShellCheck for harnesses/install scripts;
- real TUN namespace smoke;
- real official AWG2 client/server interop;
- AWG2 peer restart recovery with stable ifindex;
- AWG2 underlay loss recovery with stable ifindex;
- real official Xray VLESS/REALITY client/server payload interop;
- Xray server restart recovery with stable ifindex;
- Xray underlay loss recovery with stable ifindex;
- simultaneous AWG2 + Xray operation;
- share-link import tests;
- secret/state redaction tests;
- no public/default route in protocol CI namespaces;
- no host-interface leakage after tests.

Do not mark the PR production-ready if either protocol gate is skipped, softened to a mock, or only proves process startup without real tunneled payload.
