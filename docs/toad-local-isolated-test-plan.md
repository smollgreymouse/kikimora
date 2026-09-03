# Toad local isolated-network test plan

## Purpose

Run the same privileged, hermetic Linux protocol gates used by CI on a developer workstation.

These tests deliberately do **not** use a real VPS or the public Internet. They create disposable Linux network namespaces and private veth links, with no default route and no NAT. Real-VPS validation is a separate manual acceptance layer and can consume profiles produced by `kikimora-toad import`.

## Supported host

Primary target: modern Linux with network namespaces and `/dev/net/tun`.

Known CI baseline:

- Ubuntu 24.04;
- Go 1.26.x;
- `iproute2` (`ip`, `ss`);
- `ping`;
- Python 3;
- sudo/root access.

On Debian/Ubuntu install prerequisites with:

```bash
sudo apt-get update
sudo apt-get install -y iproute2 iputils-ping python3 sudo
```

Install a Go toolchain compatible with `toad/go.mod` separately.

## Pre-flight

From repository root:

```bash
go version
ip -V
python3 --version
sudo -v
```

Optional check for TUN:

```bash
ls -l /dev/net/tun || true
```

The privileged tests create `/dev/net/tun` when the device node is absent, matching CI behavior.

## Run the complete current isolated suite

From repository root:

```bash
bash linux/tests/toad/run-isolated.sh all
```

The runner builds binaries from the checked-out tree as the normal user, then invokes only the network-namespace test phases through `sudo`.

Expected gates:

1. `linux TUN owner gate`;
2. `AWG2 attachment gate`;
3. `AWG2 isolated client/server interop gate`.

The AWG2 reference executable is built deterministically from the module pinned by `toad/go.mod`. The runner verifies that the binary contains:

```text
github.com/amnezia-vpn/amneziawg-go/v3 v3.1.20260828
```

## Run one gate

```bash
bash linux/tests/toad/run-isolated.sh tun-owner
bash linux/tests/toad/run-isolated.sh awg2-attachment
bash linux/tests/toad/run-isolated.sh awg2-interop
```

These modes are useful while debugging one lifecycle phase.

## Expected AWG2 interop behavior

The interop gate creates approximately this topology:

```text
client namespace                     server namespace
----------------                     ----------------
veth-c 192.0.2.2/30  <----------->  veth-s 192.0.2.1/30
kk-awg0 10.77.0.2/24                awg-ref0 10.77.0.1/24
```

Assertions include:

- no IPv4/IPv6 default route in either namespace;
- no NAT and no runner/public data path;
- official pinned AmneziaWG reference server;
- real handshake;
- encrypted ICMP traffic;
- client and server RX/TX counters advance;
- server restart recovery without restarting Toad;
- private-underlay down/up recovery without restarting Toad;
- identical `kk-awg0` ifindex before and after both failures;
- final owner shutdown removes `kk-awg0`;
- no protocol interface leaks into the root namespace.

A successful run ends with output similar to:

```text
Toad AWG2 isolated interop passed: ifindex=<N> server_restart_ms=<N> underlay_recovery_ms=<N> profile=J4/40-80,S15-18,H1001-1004,I1-I5
```

Do not compare recovery milliseconds to a fixed golden value; only bounded recovery and stable TUN identity are acceptance requirements.

## Shared netns harness for new protocol tests

Reusable shell helpers live in:

```text
linux/tests/toad/lib/netns.sh
```

New isolated tests should source that file and reuse:

- `require_root`;
- `require_commands`;
- `netns_create_pair`;
- `netns_delete_if_present`;
- `assert_no_default_route`;
- `assert_no_root_interface`;
- `interface_ifindex`;
- `assert_ifindex`;
- `assert_process_alive`;
- `wait_until`;
- `dump_namespace`;
- `ensure_tun_device`.

Keep protocol-specific UAPI/control logic in the protocol test, not in the generic harness.

## Import a real VPS profile

Build Toad:

```bash
cd toad
go build -o ../.tmp-kikimora-toad ./cmd/kikimora-toad
cd ..
```

VLESS Reality link:

```bash
printf '%s\n' "$VLESS_LINK" | ./.tmp-kikimora-toad import \
  -name real-vless \
  -state-dir /run/kikimora/toads/real-vless \
  > /tmp/real-vless.toml

./.tmp-kikimora-toad validate -config /tmp/real-vless.toml
```

WireGuard/AmneziaWG config file:

```bash
./.tmp-kikimora-toad import \
  -file /path/to/provider.conf \
  -name real-awg \
  -state-dir /run/kikimora/toads/real-awg \
  > /tmp/real-awg.toml

./.tmp-kikimora-toad validate -config /tmp/real-awg.toml
```

Supported WG/AWG import inputs:

- ordinary `[Interface]` / `[Peer]` config text;
- `wg://` and `wireguard://` carrying a URL-escaped or base64-encoded WG config;
- query-form `wg://` / `wireguard://` / `amneziawg://` with explicit key, endpoint, address and AllowedIPs fields.

A direct VLESS import currently accepts VLESS + REALITY over TCP/raw, matching the Stage 0 configuration contract.

Treat imported links/config files as credentials. Do not paste real links into CI logs, issues, commits, or test fixtures.

## Real VPS test boundary

Do not modify the hermetic namespace gates to reach a VPS. A real-VPS test necessarily requires an underlay route to the Internet and therefore has different isolation/security properties.

When real-VPS smoke tests are added, keep them opt-in and separate from the required hermetic CI gate. They should consume a local secret profile and must never commit or print private keys, UUIDs, subscription URLs, or preshared keys.

## Failure cleanup

The protocol scripts register cleanup traps. If a test is killed hard and leaves namespaces behind, inspect first:

```bash
ip netns list
ip link show
```

Then remove only namespaces clearly created by the Toad test:

```bash
sudo ip netns delete <toad-test-namespace>
```

Check for leaked interfaces:

```bash
ip link show kk-awg0
ip link show awg-ref0
```

A normal successful run must leave neither interface in the root namespace.
