# Toad step 04 — embedded official Xray core

## Goal

Implement the production `vless-reality` Toad backend using the official pinned Xray-core Go dependency and Xray's own TUN inbound.

At the end of this step, `kikimora-toad run` must start an embedded Xray instance for a normalized VLESS + REALITY + TCP/raw profile, create the managed `kk-xray0` TUN through the official Xray TUN implementation, publish lifecycle state, remain alive when the remote endpoint is unavailable, and shut the Xray instance/TUN down cleanly.

This step does **not** claim protocol success. Real REALITY authentication, Vision payload traffic and failure recovery are step 05.

## Pinned dependency

Use only:

```text
github.com/xtls/xray-core
v26.7.28
5ca6f4b7d4dc20a881d4330e498892697627ec0c
```

Verified upstream APIs at this revision:

```go
serial.LoadJSONConfig(io.Reader) (*core.Config, error)
core.NewWithContext(context.Context, *core.Config) (*core.Instance, error)
(*core.Instance).Start() error
(*core.Instance).Close() error
(*core.Instance).IsRunning() bool
```

The executable imports `_ "github.com/xtls/xray-core/main/distro/all"`; embedded Toad must make the same protocol/transport registrations available.

Verified official TUN JSON contract:

```json
{
  "protocol": "tun",
  "settings": {
    "name": "kk-xray0",
    "mtu": 1380,
    "gateway": ["10.255.0.2/30"]
  }
}
```

On Linux, Xray's official TUN implementation may apply interface addresses from `gateway`. Do not create a second Toad-owned platform TUN for Xray in this step.

## Ownership model

AWG2 and Xray intentionally differ:

```text
AWG2: Toad owns TUN fd -> duplicate -> official AWG device
Xray: official Xray instance owns its own TUN inbound for its full lifetime
```

The Xray instance must stay alive across ordinary upstream failures. Do not implement reconnect by closing/recreating the Xray instance or TUN.

## Package

Create:

```text
toad/internal/backend/xray/
  backend.go
  config.go
  config_test.go
```

Suggested backend fields:

```go
type Backend struct {
    mu       sync.Mutex
    cfg      *config.Config
    instance *core.Instance
    health   backend.Health
}
```

Constructor:

```go
func New(cfg *config.Config) *Backend
```

## Xray configuration builder

Build Xray's JSON configuration from the normalized Toad config in memory; do not shell out to an Xray executable and do not write a secret-bearing temporary Xray config file.

Minimum structure:

```json
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "toad-tun",
      "protocol": "tun",
      "settings": {
        "name": "kk-xray0",
        "mtu": 1380,
        "gateway": ["10.255.0.2/30"]
      }
    }
  ],
  "outbounds": [
    {
      "tag": "toad-vless",
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "server.example",
            "port": 443,
            "users": [
              {
                "id": "UUID",
                "encryption": "none",
                "flow": "xtls-rprx-vision"
              }
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "raw",
        "security": "reality",
        "realitySettings": {
          "serverName": "www.example.org",
          "fingerprint": "chrome",
          "publicKey": "...",
          "shortId": "...",
          "spiderX": "/"
        }
      }
    }
  ]
}
```

Use `tcp`/`raw` normalized input to select the Xray raw transport accepted at this revision. Reject unsupported transports in Toad config validation/import rather than silently changing them.

Parse the normalized endpoint with `net.SplitHostPort`; support hostnames and IPv6 literals correctly.

Never include secrets in errors or health reasons.

## Lifecycle

`Start(ctx)`:

1. require normalized VLESS config;
2. build the in-memory Xray JSON;
3. call official `serial.LoadJSONConfig`;
4. create official instance with `core.NewWithContext`;
5. call `Start()`;
6. retain the instance for the entire Toad lifetime;
7. report `connecting`, not `online`, because startup is not proof of REALITY/session success.

`Close()`:

- idempotent;
- call official `Instance.Close()` once;
- clear the retained instance;
- report `stopped`;
- do not separately delete/recreate the Xray TUN behind the official core.

`Health()` in step 04 is lifecycle-only:

- stopped before/after lifecycle;
- connecting while Xray is running but real protocol success is unproven;
- degraded only when an internal lifecycle/config error can be proven.

Do not invent handshake telemetry. Step 05 may add a concrete observation mechanism if official Xray exposes one suitable for the gate.

## CLI wiring

Refactor `kikimora-toad run` by protocol:

```text
amneziawg2    -> existing Toad-owned platform TUN -> awg2 backend
vless-reality -> xray backend -> official Xray TUN
```

Do not call `platform.CreateTunnel` for Xray.

State publishing should be protocol-neutral. After Xray starts, resolve the actual interface by configured name and publish:

- name;
- ifindex;
- MTU;
- route_ready=true while the managed TUN exists;
- lifecycle health from the backend.

The common state snapshot helper must no longer require a `platform.Tunnel` object when all it needs is interface identity.

## Tests

Unit tests must include:

- endpoint hostname/port parsing;
- IPv6 endpoint parsing;
- generated config contains exactly one TUN inbound with configured name/MTU/gateway;
- generated config contains VLESS UUID, endpoint and `encryption=none`;
- REALITY serverName/publicKey/shortId/fingerprint/spiderX are preserved;
- Vision flow is preserved when configured;
- raw/tcp maps to the supported Xray network;
- generated JSON can be consumed by pinned `serial.LoadJSONConfig`;
- no private credential appears in lifecycle health/reason strings;
- `Close()` is idempotent.

Cross-platform Go compilation/tests must remain green.

## Linux smoke

Add a privileged CI gate that starts `kikimora-toad run` in one disposable network namespace with a deliberately unreachable private VLESS endpoint.

Assertions:

- no default route is created;
- `kk-xray0` appears only inside the test namespace;
- configured gateway/address and MTU are present;
- Toad process remains alive without a server;
- state is `connecting`, not `online`;
- TUN ifindex stays unchanged while the endpoint remains unavailable;
- clean Toad shutdown removes the Xray-owned TUN;
- nothing leaks to root namespace.

Use `linux/tests/toad/lib/netns.sh` for shared isolation assertions.

## Acceptance

- pinned official Xray dependency in `go.mod`/`go.sum`;
- no Xray subprocess for production backend;
- no independent VLESS/REALITY/Vision implementation;
- official Xray TUN inbound owns `kk-xray0`;
- `kikimora-toad run` supports `vless-reality`;
- unavailable endpoint is not fatal and does not cause a TUN recreation loop;
- unit matrix green on Linux/macOS/Windows;
- Linux Xray lifecycle/TUN smoke green;
- existing AWG2 gates remain green.

## Forbidden shortcuts

Do not:

- mark `online` just because `Instance.Start()` succeeded;
- shell out to `xray run` for production;
- copy Xray's VLESS or REALITY implementation;
- create a separate Toad platform TUN for Xray and then ignore Xray's official TUN implementation;
- add default routes/NAT to lifecycle smoke;
- weaken existing AWG2 gates.

## Executor report

Return:

1. commit SHA(s);
2. resolved Xray module version;
3. exact official APIs used;
4. changed files;
5. unit/CI results;
6. Xray lifecycle smoke result and ifindex evidence;
7. any API/config discrepancy from this packet.
