# Toad step 02 — official AmneziaWG2 core attachment

## Goal

Attach the official pinned `amneziawg-go` protocol engine to the Linux-owned TUN from step 01.

At the end of this step, `kikimora-toad run` must be able to start an AWG2 instance using the official Go core and the Toad-owned TUN. This step proves lifecycle/configuration/health plumbing only; the full client↔server encrypted data-plane acceptance test belongs to step 03.

## Prerequisite

Step 01 must be complete and green. Do not work around a missing TUN owner boundary.

Read:

1. `docs/toad-steps/01-platform-linux-tun.md`
2. `docs/vpn-native-core-roadmap.md`
3. `docs/toad-naming.md`
4. current `toad/internal/platform`
5. current `toad/internal/backend/backend.go`

## Dependency pin

Use official module:

```text
github.com/amnezia-vpn/amneziawg-go/v3
```

Pin the release corresponding to:

```text
tag: v3.1.20260828
commit: b5928efb6ca19f0153958460c3d141f04abc5c2e
```

Do not use the Rust-native implementation and do not implement AWG framing/crypto in Kikimora.

## Verified upstream API entry points

The pinned official core exposes these APIs and they are the intended integration path:

```go
// github.com/amnezia-vpn/amneziawg-go/v3/tun
CreateTUNFromFile(file *os.File, mtu int) (tun.Device, error)

// github.com/amnezia-vpn/amneziawg-go/v3/conn
NewDefaultBind() conn.Bind

// github.com/amnezia-vpn/amneziawg-go/v3/device
NewLogger(level int, prepend string) *device.Logger
NewDevice(tunDevice tun.Device, bind conn.Bind, logger *device.Logger) *device.Device
(*device.Device).IpcSetOperation(io.Reader) error
(*device.Device).IpcGetOperation(io.Writer) error
(*device.Device).Up() error
(*device.Device).Down() error
(*device.Device).Close()
```

Use these names. If the Go module resolved by the pin does not expose one of them, stop and report the resolved version and compiler error instead of substituting another library.

## Required code shape

Create package:

```text
toad/internal/backend/awg2/
```

Suggested files:

```text
backend.go
uapi.go
health.go
attach_linux.go
attach_unsupported.go
```

`backend.go` implements the existing common `backend.Backend` interface.

The constructor should accept normalized Toad config and a `platform.Tunnel`; it must not create the Linux TUN itself.

Linux attachment belongs in `attach_linux.go` with `//go:build linux`.

On Linux, obtain a duplicate from the concrete platform tunnel through the already-defined `DuplicateFile() (*os.File, error)` capability. Do not close or replace the Toad owner fd.

Wrap only the duplicate with official:

```go
awgTun, err := awgtun.CreateTUNFromFile(duplicate, mtu)
```

Then create:

```go
bind := conn.NewDefaultBind()
logger := device.NewLogger(device.LogLevelError, "toad/<instance>: ")
dev := device.NewDevice(awgTun, bind, logger)
```

Do not pass the owner fd to `amneziawg-go`.

## Configuration path

Build one official UAPI set payload and apply it through:

```go
dev.IpcSetOperation(strings.NewReader(payload))
```

Then call:

```go
dev.Up()
```

The UAPI payload must use official field names, including:

Device fields:

```text
private_key
jc
jmin
jmax
s1
s2
s3
s4
h1
h2
h3
h4
i1
i2
i3
i4
i5
```

Peer fields:

```text
public_key
preshared_key
endpoint
persistent_keepalive_interval
allowed_ip
```

Use official UAPI encoding requirements. WireGuard private/public/preshared keys in UAPI are hexadecimal 32-byte keys; if normalized Toad config stores base64 share-link keys, convert them deliberately and test that conversion. Do not send base64 to an official field that expects hex.

Terminate the UAPI operation correctly. Inspect pinned `device/uapi.go` if unsure; do not guess separators or field names.

## Health sampling

Use:

```go
dev.IpcGetOperation(&buffer)
```

Parse only the facts needed by the existing normalized `backend.Health`:

- `last_handshake_time_sec` + `last_handshake_time_nsec`;
- `rx_bytes`;
- `tx_bytes`;
- `endpoint`.

Do not expose keys or AWG obfuscation secrets in state/logs.

The backend may classify:

- no successful handshake yet -> `connecting`;
- recent successful handshake -> `online`;
- previously active but stale/unavailable -> `reconnecting` or `degraded`.

Keep classification conservative. Do not claim `online` merely because `dev.Up()` returned nil.

## Lifecycle requirements

`Start()`:

1. validates that the provided tunnel is Linux-attachable;
2. duplicates the TUN fd;
3. creates official TUN wrapper/device;
4. applies UAPI config;
5. calls `Up()`;
6. keeps the official device alive.

`Close()`:

1. is idempotent;
2. closes/stops only the official device attachment;
3. must not close the outer Toad TUN owner;
4. must not execute routing commands.

Ordinary handshake failure must not call `Close()` on the Toad tunnel.

## CLI wiring

Wire only `protocol = "amneziawg2"` into `kikimora-toad run`.

Do not implement Xray in this step.

Do not add `kk toad` shell commands yet unless explicitly required by this step's existing skeleton; `kikimora-toad` remains the executable under test.

## Tests

Unit tests must cover at least:

- base64 WireGuard key -> official hex UAPI conversion;
- malformed key rejection without panic;
- UAPI payload contains all configured AWG2 `J/S/H/I` fields;
- optional preshared key handling;
- multiple AllowedIPs;
- UAPI `get` parser extracts handshake/RX/TX/endpoint;
- state/log output does not contain private/preshared key material;
- Close is idempotent.

A Linux integration smoke may instantiate the official device in an isolated namespace without a server and verify:

- `kikimora-toad run` remains alive while no peer responds;
- `kk-awg0` remains present with the same ifindex;
- backend reports connecting/reconnecting rather than process-fatal solely because a handshake is absent.

Do not call this a working AWG2 client yet. Real encrypted traffic is step 03.

## Cross-platform requirement

macOS and Windows CI must still compile/test the common module.

For non-Linux builds, AWG attachment may return an explicit `unsupported platform in current Stage 0 implementation` error. Do not introduce fake protocol behavior.

## Acceptance criteria

- official dependency is pinned;
- no alternate AWG implementation is present;
- Toad owner fd is distinct from the fd wrapped by `amneziawg-go`;
- official device can be created/configured/up/closed;
- missing peer does not recreate or remove the Toad TUN;
- UAPI health parsing is tested;
- cross-platform unit matrix remains green;
- Linux integration smoke is green.

## Forbidden shortcuts

Do not:

- launch `amneziawg-go` as a subprocess instead of using the Go dependency;
- use `amneziawg-tools` as the client configuration mechanism;
- replace the Toad TUN with one created internally by the official core;
- reimplement Noise/WireGuard/AWG2 packets;
- consider `dev.Up()` proof of connectivity;
- weaken the TUN ownership test.

## Executor report

Return:

1. commit SHA(s);
2. exact resolved `amneziawg-go/v3` version;
3. files changed;
4. unit/CI results;
5. Linux smoke result;
6. evidence that official-device close leaves the Toad-owned TUN and same ifindex alive;
7. any compiler/API discrepancy.

If a verified API behaves differently at runtime, stop and report the exact behavior. Do not redesign around it without a new plan revision.
