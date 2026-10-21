# Toad step 05 — real isolated VLESS + REALITY + Vision interop

## Goal

Prove that the embedded official Xray client from step 04 is a real working VLESS + REALITY client, including Vision when configured, with real payload traffic and recovery while preserving the same managed Xray TUN identity.

This is the Xray production protocol gate. Do not call the Xray backend working until this gate is green.

## Prerequisites

- steps 01–04 complete and green;
- shared local/CI netns helpers in `linux/tests/toad/lib/netns.sh`;
- local test runner in `linux/tests/toad/run-isolated.sh`;
- VLESS share-link import available for real-VPS follow-up testing.

## Pinned reference

Use only official Xray-core:

```text
github.com/xtls/xray-core
v26.7.28
5ca6f4b7d4dc20a881d4330e498892697627ec0c
```

The reference/server must run the same pinned official implementation. A fake TLS/VLESS/REALITY endpoint is forbidden.

## Isolation topology

Use disposable Linux network namespaces:

```text
client-ns                               server-ns
---------                               ---------
veth-c 198.18.0.2/30 <--------------> veth-s 198.18.0.1/30

kk-xray0                               local application/echo endpoint
10.88.0.2/30                           bound inside server-ns
```

The Xray REALITY server listens only on the private server veth address. Neither namespace may have a default route. No NAT or runner/public test data path.

A local deterministic TCP payload endpoint must run inside `server-ns` behind the reference Xray inbound/fallback path or another explicit local target appropriate for the official REALITY server config.

## REALITY material

Generate test-only keys at test runtime using official Xray tooling/APIs or a deterministic test helper based on the pinned official module. Never commit production-like private REALITY keys.

The client config must exercise:

- VLESS UUID;
- `security=reality`;
- REALITY server name;
- public key;
- short id;
- browser fingerprint;
- spiderX;
- `flow=xtls-rprx-vision` in the final production gate;
- TCP/raw transport.

## Required phases

### A — baseline

1. create client/server namespaces and private veth;
2. assert no IPv4/IPv6 default routes;
3. start a local deterministic target service in `server-ns`;
4. start pinned official Xray reference server;
5. start Toad Xray client;
6. wait for `kk-xray0` and record ifindex `X`;
7. prove Toad stays alive.

### B — real REALITY/VLESS payload

Do not use ping as the sole protocol proof: Xray TUN ICMP echo can be handled locally by the TUN stack.

Create actual TCP application traffic from the client namespace through `kk-xray0` to an address routed into Xray and verify an exact payload/response from the server-side target.

Required evidence:

- REALITY authentication succeeded;
- VLESS transported application bytes;
- Vision path was selected when configured;
- returned payload matches exactly;
- server/reference connection counters/log events demonstrate the reference handled it.

### C — reference restart

1. stop only the reference Xray server;
2. keep Toad alive;
3. verify `kk-xray0` remains and ifindex is still `X`;
4. restart reference with identical test credentials;
5. retry real TCP payload until bounded recovery;
6. verify response and same ifindex `X`.

Never restart Toad as recovery.

### D — underlay down/up

1. bring private client veth down;
2. verify Toad alive and `kk-xray0` ifindex `X`;
3. restore veth/address/link as needed;
4. wait for real application payload recovery;
5. verify same ifindex `X`.

### E — cleanup

1. terminate Toad cleanly;
2. verify `kk-xray0` disappears;
3. terminate reference/target services;
4. verify no test interfaces leak to root namespace;
5. remove namespaces.

## Health/state

Do not invent a cryptographic handshake timestamp if Xray does not expose one through a stable embedded API.

For this step, choose the strongest trustworthy observation mechanism available from the pinned core, such as official stats/features or explicit session counters. If the current common health schema cannot represent a proven Xray session without heuristic log parsing, keep lifecycle state conservative and use the integration gate as the protocol release proof. Document the limitation for the later control-plane plan.

## Diagnostics

On failure, sanitize secrets and print:

Client:

- Toad log;
- state.json;
- `ip -s link`;
- addresses/routes/rules;
- sockets;
- TUN ifindex.

Reference:

- Xray reference log with credentials redacted;
- reference sockets;
- namespace network state;
- local target service state.

Do not print UUID, REALITY private key, imported real-VPS links, or other bearer credentials in successful logs.

## CI

Create dedicated `linux-xray-interop` job. Build both Toad and pinned official Xray reference deterministically from checked-in module pins.

The gate is green only when all phases succeed in one run.

## Acceptance

- pinned official client and reference;
- private namespace underlay only;
- no default route/NAT/public data path;
- real REALITY auth;
- real VLESS application payload round trip;
- Vision enabled and verified in final profile;
- server restart recovery without Toad restart;
- underlay loss/restore recovery without Toad restart;
- stable `kk-xray0` ifindex across both failures;
- clean Xray-owned TUN removal on final shutdown;
- existing AWG2 gates and cross-platform unit matrix remain green.

## Forbidden shortcuts

Do not:

- replace REALITY server with a generic TLS listener;
- prove success only with process startup/TUN creation;
- rely only on ICMP echo;
- disable Vision to make the final gate pass;
- use public Internet as the protocol test data plane;
- recreate/restart Toad during recovery;
- weaken the gate if timing differs on GitHub runners.

## Executor report

Return:

1. commit SHA(s);
2. CI run id/result;
3. exact Xray revision used on client and reference;
4. REALITY/Vision fields exercised;
5. exact application payload proof;
6. ifindex initial/server-restart/underlay-restore;
7. recovery durations;
8. sanitized diagnostics for any failed iterations.
