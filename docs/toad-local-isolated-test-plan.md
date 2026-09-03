# Toad local isolated-network test plan

## Purpose

Run the same privileged, hermetic Linux protocol gates used by CI on a developer workstation.

These tests deliberately do **not** use a real VPS or the public Internet. They create disposable Linux network namespaces and private veth links, with no default route and no NAT.

Real-VPS validation is a separate manual acceptance layer. For the current workstation smoke flow, **both AWG and VLESS start from provider share links**, not hand-written Toad configs.

Protocol-specific real-VPS runbooks:

```text
docs/toad-real-vps-awg-link-test.md
docs/toad-real-vps-vless-link-test.md
```

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

Current protocol/lifecycle gates include:

1. Linux TUN owner gate;
2. AWG2 attachment gate;
3. AWG2 isolated client/server interop gate;
4. Xray lifecycle gate;
5. Xray REALITY/VLESS/Vision isolated interop gate.

The official reference executables are built deterministically from the modules pinned by `toad/go.mod`.

## Run one gate

```bash
bash linux/tests/toad/run-isolated.sh tun-owner
bash linux/tests/toad/run-isolated.sh awg2-attachment
bash linux/tests/toad/run-isolated.sh awg2-interop
bash linux/tests/toad/run-isolated.sh xray-lifecycle
bash linux/tests/toad/run-isolated.sh xray-interop
```

These modes are useful while debugging one lifecycle/protocol phase.

## Expected AWG2 interop behavior

The AWG2 gate uses a private client/server namespace topology and asserts:

- no IPv4/IPv6 default route in either namespace;
- no NAT and no runner/public data path;
- official pinned AmneziaWG reference server;
- real handshake;
- encrypted traffic;
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

## Expected Xray interop behavior

The Xray gate uses the production embedded Xray client plus an official pinned Xray reference server in private namespaces.

It proves:

- REALITY authentication;
- VLESS application payload delivery and response;
- `xtls-rprx-vision` in the final profile;
- no default route/NAT/public data path;
- reference-server restart recovery without restarting Toad;
- private-underlay down/up recovery without restarting Toad;
- stable `kk-xray0` ifindex across both failures;
- clean Xray-owned TUN removal at final Toad shutdown.

A successful run includes:

```text
Toad Xray interop passed: REALITY+VLESS+Vision payload, server restart and underlay recovery kept ifindex=<N>
```

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

## Build Toad for real-VPS link tests

From repository root:

```bash
cd toad
go build -o ../.tmp-kikimora-toad ./cmd/kikimora-toad
cd ..
```

Do not place a real provider link directly in a command argument or committed script. Read it silently and pipe it through stdin.

### AWG link import smoke

```bash
read -rsp 'AWG share link: ' AWG_LINK
echo
printf '%s\n' "$AWG_LINK" | ./.tmp-kikimora-toad import \
  -name real-awg \
  -interface kk-awg0 \
  -state-dir /run/kikimora/toads/real-awg \
  > /tmp/real-awg.toml
unset AWG_LINK

./.tmp-kikimora-toad validate -config /tmp/real-awg.toml
```

Current AWG/WG URI schemes understood by the importer are:

```text
wg://
wireguard://
amneziawg://
```

They may carry an encoded WireGuard/AWG profile or explicit query fields. There is no universal WireGuard share-URI standard, so a provider-specific unsupported scheme must be added to the importer rather than manually transcribed for the acceptance test.

Full real-VPS procedure:

```text
docs/toad-real-vps-awg-link-test.md
```

### VLESS REALITY link import smoke

```bash
read -rsp 'VLESS share link: ' VLESS_LINK
echo
printf '%s\n' "$VLESS_LINK" | ./.tmp-kikimora-toad import \
  -name real-vless \
  -interface kk-xray0 \
  -state-dir /run/kikimora/toads/real-vless \
  > /tmp/real-vless.toml
unset VLESS_LINK

./.tmp-kikimora-toad validate -config /tmp/real-vless.toml
```

The current direct VLESS importer accepts REALITY over TCP/raw, including the Stage 0 UUID/SNI/public-key/short-id/fingerprint/spiderX/Vision fields.

Full real-VPS procedure:

```text
docs/toad-real-vps-vless-link-test.md
```

Treat all imported links/generated configs as credentials. Do not paste real links into CI logs, issues, commits, screenshots, or test fixtures.

## Real VPS test boundary

Do not modify the hermetic namespace gates to reach a VPS. A real-VPS test necessarily requires an Internet underlay and therefore has different isolation/security properties.

The first workstation smoke must route only one selected `/32` application destination into the Toad interface. Do not replace the workstation default route on the first run.

This preserves the normal underlay path to the VPN server and reduces the chance of losing SSH/desktop connectivity while validating a real provider.

## Failure cleanup

The hermetic protocol scripts register cleanup traps. If a test is killed hard and leaves namespaces behind, inspect first:

```bash
ip netns list
ip link show
```

Then remove only namespaces clearly created by the Toad test:

```bash
sudo ip netns delete <toad-test-namespace>
```

Check for leaked managed interfaces:

```bash
ip link show kk-awg0
ip link show kk-xray0
```

A normal successful hermetic run must leave neither interface in the root namespace.
