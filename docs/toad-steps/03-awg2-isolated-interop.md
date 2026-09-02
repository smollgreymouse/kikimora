# Toad step 03 — real isolated AWG2 client/server interop

## Goal

Prove that the Toad AWG2 client from step 02 is a real working client against the official AmneziaWG implementation, with encrypted traffic and recovery, while preserving the Toad-owned TUN identity.

This is the first protocol production gate. Do not start Xray work until this gate is green and the result has been reviewed.

## Prerequisites

Steps 01 and 02 must be complete and green.

Read:

1. `docs/toad-steps/01-platform-linux-tun.md`
2. `docs/toad-steps/02-awg2-official-core.md`
3. `docs/vpn-native-core-stage0.md`
4. current `toad/internal/backend/awg2`
5. existing Rust-experiment AWG interop scripts only as test-infrastructure reference; do not copy the Rust backend/protocol code.

## Reference implementation

Use official pinned server/reference code only:

```text
amnezia-vpn/amneziawg-go
v3.1.20260828
b5928efb6ca19f0153958460c3d141f04abc5c2e
```

Use official `amneziawg-tools` only on the reference/server side if needed to configure the reference interface.

The Toad client side must use the embedded official Go dependency implemented in step 02.

## Isolation topology

Run the test in disposable Linux network namespaces.

Minimum topology:

```text
client-ns                           server-ns
---------                           ---------
veth-c 192.0.2.2/30 <-----------> veth-s 192.0.2.1/30

kk-awg0                             awg-ref0
10.77.0.2/24                        10.77.0.1/24
```

Rules:

- neither namespace may have a default route;
- no NAT;
- no forwarding to the GitHub runner/public Internet;
- the reference endpoint must be the private veth address;
- all generated keys/configs are test-only and destroyed during cleanup;
- root namespace must never receive `kk-awg0` or `awg-ref0`.

## Test implementation

Create a dedicated script under:

```text
linux/tests/toad/awg2-interop.sh
```

and a dedicated CI job.

The test should build `kikimora-toad` once and run it in `client-ns`.

Build/download the pinned official reference server deterministically. Do not use an unpinned distro package as the protocol oracle.

## Production AWG2 profile

The final passing gate must use non-default AWG2 obfuscation parameters, including representative values for:

```text
Jc/Jmin/Jmax
S1/S2/S3/S4
H1/H2/H3/H4
at least I1; preferably I1-I5 when supported by the chosen test profile
```

Client and reference server must receive matching values.

Do not make the final CI gate pass by setting AWG fields to ordinary WireGuard defaults.

## Required test phases

### Phase A — baseline setup

1. create namespaces and veth;
2. prove no default route exists in either namespace;
3. start official reference server;
4. start Toad client;
5. wait with a bounded timeout for a successful AWG handshake;
6. record client TUN ifindex `X`.

Failure must dump both client and reference state before cleanup.

### Phase B — real data plane

Prove real encrypted tunnel traffic, not just handshake:

- `ping` from `10.77.0.2` to `10.77.0.1` succeeds through `kk-awg0`;
- RX/TX counters advance on Toad/client state;
- reference counters advance as well.

If ICMP behavior becomes ambiguous, add a local TCP/UDP echo service in server-ns, but do not remove the ICMP check without reporting why.

### Phase C — reference server restart

1. stop the reference protocol process/interface while leaving client Toad running;
2. verify client process remains alive;
3. verify `kk-awg0` remains present;
4. verify its ifindex is still `X`;
5. restart the reference with identical keys/config;
6. wait for a new successful handshake;
7. verify tunnel traffic recovers;
8. verify ifindex remains `X`.

A systemd/process restart of the Toad is not allowed as the recovery mechanism.

### Phase D — underlay loss/restore

1. bring the client private veth link down or otherwise remove private underlay reachability without terminating Toad;
2. verify Toad process remains alive;
3. verify `kk-awg0` remains present and ifindex remains `X`;
4. restore veth/link/address as required;
5. wait for handshake/data traffic recovery;
6. verify ifindex still equals `X`.

Do not trigger recovery by deleting/recreating the Toad TUN.

### Phase E — cleanup ownership

1. terminate Toad cleanly;
2. verify `kk-awg0` disappears after the owner fd is closed;
3. verify no Toad/reference interface leaked into root namespace;
4. remove namespaces.

## Readiness/state assertions

Do not mark Toad `online` merely from process/device startup.

Before first successful handshake, state should be `connecting` or equivalent.

After a real successful handshake/data plane, state may become `online`.

During reference/underlay outage, state should become `reconnecting`/`degraded` when the existing health policy can prove this; `route_ready` may remain true because the TUN remains a fail-closed route target.

Do not change the global state schema only to make this test convenient. If the current health contract cannot express an observed case, report it for plan revision.

## Diagnostics on failure

Before deleting namespaces, print/save at least:

Client:

```text
Toad stdout/stderr
state.json
ip link show
ip addr show
ip route show table all
ss -lunp
TUN ifindex
```

Reference:

```text
reference process log
official AWG/WG show/UAPI state
ip link show
ip addr show
ip route show table all
ss -lunp
```

Also show veth packet counters from both sides.

Never print private or preshared keys in normal successful logs. If a debug tool would expose secrets, redact them before artifact output.

## Timeouts

All waits must be bounded. Prefer polling the real condition with a deadline over fixed sleeps.

Examples of real conditions:

- handshake timestamp becomes non-zero/newer;
- ping/echo succeeds;
- state transition appears;
- expected socket becomes bound.

Do not add arbitrary long sleeps to hide races.

## CI acceptance criteria

The AWG2 job is green only if all of these pass in one run:

- isolated namespaces with no default route/NAT;
- official reference server at the pinned revision;
- real AWG2 handshake;
- real encrypted IP traffic;
- RX/TX counters advance;
- server restart recovery without Toad restart;
- underlay down/up recovery without Toad restart;
- same client TUN ifindex across both failures;
- clean owner-driven TUN removal at final shutdown;
- no root-namespace interface leakage.

Keep cross-platform Toad unit CI green as well.

## Forbidden shortcuts

Do not:

- replace the reference with a fake UDP server;
- use ordinary WireGuard parameters in the final production gate;
- restart `kikimora-toad` to recover network failures;
- recreate `kk-awg0` during normal recovery;
- add a default route or NAT to make connectivity easier;
- route test traffic through the public Internet;
- weaken the gate because GitHub runner timing differs.

## Executor report

Return:

1. commit SHA(s);
2. CI run URL/id and final result;
3. exact pinned reference revision used;
4. AWG2 profile fields exercised;
5. initial handshake/data-plane result;
6. client TUN ifindex before failure, after reference restart, and after underlay restore;
7. recovery times observed;
8. sanitized failure diagnostics if any assertion failed.

If the official client and official reference do not interoperate, stop with the captured wire/runtime diagnostics. Do not replace the official core or change protocol parameters to a weaker mode without a revised plan.
