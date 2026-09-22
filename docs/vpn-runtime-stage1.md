# Stage 1 plan — managed client state contract and Bash observability

Stage 1 makes managed VPN clients explicitly observable by the existing Bash Kikimora without changing routing authority yet.

The governing rule is: **Stage 1 may improve truthfulness and diagnostics, but it may not make routing availability depend on the new client state contract.** That dependency begins only in Stage 2 after the contract has been exercised in real deployments.

## 1. Goals

- freeze and document client state schema v1;
- make every managed client publish it atomically;
- let Bash `kk status`, `kk interfaces`, `kk diag`, `kk doctor` and debug bundles read managed-client facts;
- distinguish transport health from TUN/route readiness in user-visible output;
- detect stale/missing state without guessing protocol internals;
- preserve the existing route-watch/reconcile path as the routing source of truth;
- keep external NetworkManager VPN behavior unchanged.

## 2. Non-goals

Stage 1 does not:

- remove 3/3 readiness polling;
- remove ifindex recreation tracking;
- remove endpoint pending markers;
- change `.dev` publication logic;
- add an event bus dependency;
- add endpoint lease IPC;
- add arbitrary named Leshy targets;
- replace the Bash orchestrator.

## 3. State schema v1

Every managed client atomically writes:

```text
/run/kikimora/vpn/clients/<name>/state.json
```

Required top-level fields:

```json
{
  "schema": 1,
  "name": "awg-main",
  "protocol": "amneziawg2",
  "generation": 17,
  "updated_at_unix_ms": 1788255000000,
  "state": "online",
  "reason": "handshake-established",
  "route_ready": true,
  "interface": {
    "name": "kk-awg0",
    "ifindex": 42,
    "mtu": 1380
  },
  "endpoint": {
    "address": "192.0.2.10",
    "port": 443
  },
  "session": {
    "connected": true,
    "last_handshake_age_ms": 3812
  },
  "counters": {
    "reconnects": 2,
    "rx_bytes": 12345,
    "tx_bytes": 67890,
    "dropped_packets": 0
  }
}
```

Fields that are not meaningful to a backend may be `null` or omitted only where the schema explicitly permits it.

## 4. Schema semantics

### `schema`

Integer contract version. Bash rejects unsupported future major schema values rather than silently interpreting them.

### `name`

Exact configured client instance name.

Must match the directory name used to discover/read the snapshot.

### `protocol`

Stable protocol identifier, initially:

```text
amneziawg2
vless-reality
```

Later additions are additive.

### `generation`

Monotonically increasing for semantic state changes within one process lifetime. Process restart may reset generation only if another process-instance identifier is added; preferred design is to persist a boot/process UUID separately or use a random `instance_id` field.

Stage 1 should therefore include:

```json
"instance_id": "6d4b..."
```

A changed `instance_id` means a process/TUN lifetime boundary may have occurred.

### `updated_at_unix_ms`

Wall-clock timestamp for operator readability and stale detection across processes.

The client may also track monotonic time internally; monotonic timestamps are not serialized as cross-process absolute facts.

### `state`

One of:

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

No ad-hoc states are permitted in schema v1.

### `reason`

Stable semantic reason code. It is not free-form prose.

Initial taxonomy:

```text
startup
config-loaded
tun-created
waiting-underlay
connect-started
handshake-established
transport-established
handshake-timeout
transport-reset
endpoint-unreachable
endpoint-reresolved
retry-backoff
retry-window-exceeded
config-invalid
tun-fatal
backend-fatal
shutdown-requested
```

Backend-specific detail may be written to a separate optional `detail` string but Bash logic may not branch on it.

### `route_ready`

Client assertion that its TUN exists and the client is prepared to fail-close packets sent to it.

It does **not** mean the remote peer is currently online.

Stage 1 displays this value but does not use it to publish/withdraw Leshy `.dev` files.

### `interface`

Contains public runtime identity:

```text
name
ifindex
mtu
addresses (optional public operational values)
```

If `route_ready=false`, interface data may be absent while starting.

### `endpoint`

Contains only the currently selected remote address/port if safe under diagnostic policy.

Never includes credentials, VLESS UUID, REALITY private keys, AWG private keys or imported config fragments.

### `session`

Backend-neutral health fields when available:

```text
connected
last_handshake_age_ms
connected_since_unix_ms
```

VLESS may interpret `connected` as transport/session established; AWG interprets it as a valid current peer session/handshake state.

### `counters`

Monotonic within one `instance_id` where practical.

Counters are observability only and not routing decisions.

## 5. Atomicity and permissions

Snapshot updates use:

```text
write temporary file in same directory
set final mode
flush file
rename over state.json
```

Readers never observe partially serialized JSON.

Runtime directory ownership:

```text
root:root
```

Snapshot mode should be `0644` only if every serialized field is explicitly approved as non-secret. Otherwise use `0640`/`0600` and teach CLI diagnostics to read with privilege. The preferred Stage 1 contract keeps snapshots non-secret so normal `kk status` remains usable without root.

## 6. Staleness

Bash computes snapshot age:

```text
now - updated_at_unix_ms
```

Suggested presentation thresholds:

- fresh: within expected heartbeat/update window;
- stale: process exists but snapshot age exceeds threshold;
- orphaned: snapshot exists but systemd/process instance is absent;
- missing: managed instance is configured but snapshot is absent.

The client should update the snapshot on semantic changes and periodically at a low heartbeat frequency so stale detection can distinguish a wedged process from a quiet healthy connection.

Heartbeat writes must not increase `generation` unless semantic state changed.

## 7. Discovery

Stage 1 must not scan arbitrary `/run` names and trust them blindly.

Preferred discovery source is configured managed clients. For each configured name, Bash reads the corresponding snapshot and validates:

- safe instance name;
- file is regular, not symlink where unsafe;
- expected ownership/mode;
- JSON schema;
- snapshot `name` matches configured name.

Unknown state directories may be shown in verbose diagnostics as orphan candidates but are never treated as configured clients.

## 8. Bash parsing boundary

Do not spread JSON parsing throughout shell files.

Add one helper module, conceptually:

```text
linux/files/kikimora-cli/managed-vpn.sh
```

Responsibilities:

- discover configured managed clients;
- validate snapshot file properties;
- parse schema with `jq` if it is an accepted dependency, or provide a narrowly scoped parser/helper binary if not;
- normalize missing/stale/invalid states;
- expose shell-safe functions for status/diag.

Callers should consume normalized fields rather than reparsing JSON.

## 9. `kk status`

Add a managed clients section, for example:

```text
Managed VPN clients
  awg-main    amneziawg2   kk-awg0   online        route-ready   handshake 4s ago
  xray-main   vless-reality kk-xray0  reconnecting  route-ready   transport-reset
```

Crucial presentation rule:

```text
reconnecting + route_ready=true
```

must not be rendered as `ready` without qualification. The entire purpose is to stop conflating interface presence with protocol health.

Existing primary/secondary status remains visible during Stage 1 so operators can compare old routing readiness against new client facts.

## 10. `kk interfaces`

For a managed interface, show:

- configured client name;
- interface/ifindex from snapshot;
- kernel-observed interface/ifindex;
- whether they match;
- route_ready;
- protocol state/reason.

A mismatch is diagnostic evidence, not an automatic Stage 1 routing action.

## 11. `kk diag`

Extend the diagnostic bundle with:

```text
== MANAGED VPN CLIENT SNAPSHOTS ==
```

For each configured client include:

- redacted snapshot;
- systemd unit status;
- recent client journal;
- current kernel interface facts;
- comparison of snapshot interface identity vs kernel identity;
- snapshot age;
- no secret config contents.

Existing broad interface/route/journal capture remains.

This gives future incidents direct statements such as:

```text
client says reconnecting: handshake-timeout
TUN kk-awg0 ifindex 42 unchanged
```

instead of inferring a reconnect from repeated interface creation.

## 12. `kk doctor` and debug bundles

Doctor checks:

- configured client has a state snapshot;
- schema valid;
- snapshot fresh;
- expected systemd unit active when desired;
- snapshot interface matches kernel when route_ready;
- no snapshot exposes forbidden key names.

Debug bundles include snapshots but not secret configs.

## 13. No routing authority in Stage 1

This is the most important migration safety rule.

Even if snapshot says:

```text
route_ready=false
```

Stage 1 does not directly remove `primary.dev`/`secondary.dev`.

Even if snapshot says:

```text
route_ready=true
```

Stage 1 does not directly publish a device.

The legacy reconcile logic remains authoritative. Stage 1 compares and reports disagreement.

This creates valuable shadow-mode metrics:

```text
legacy=ready, client=route_ready=true       -> agree
legacy=validating, client=route_ready=true  -> migration timing difference
legacy=ready, client=route_ready=false      -> important bug/invariant violation
```

These disagreements should be captured in diagnostics before Stage 2 switches authority.

## 14. Shadow comparison counters/logging

Bash may log a rate-limited warning when legacy and client state disagree.

Examples:

```text
managed-state disagreement client=awg-main legacy=ready client_route_ready=false
managed-state disagreement client=xray-main legacy=validating client_route_ready=true
```

Do not trigger reconnect/restart from these warnings.

Collect enough field evidence to decide whether Stage 2 can safely trust the client.

## 15. Testing

### Unit/shell tests

Use fixture snapshots:

- online;
- reconnecting but route-ready;
- failed but route-ready;
- starting/no interface;
- stale;
- malformed JSON;
- unsupported schema;
- wrong instance name;
- forbidden secret field fixture;
- ifindex mismatch;
- missing snapshot.

Verify human and JSON CLI output.

### Namespace integration

Run a Stage 0 client in isolated netns and invoke Bash status/diag against an injected runtime root/config root.

Verify:

- Bash reads the real client-generated snapshot;
- reconnect fault changes displayed protocol state;
- legacy interface readiness can remain ready at the same time;
- no routing action occurs because of the state change.

### Secret regression

CI recursively checks serialized snapshots/diagnostic fixture output for forbidden keys/patterns:

```text
PrivateKey
private_key
UUID secret value
reality private key
password
```

Tests should use explicit sentinel secret values and assert they never occur in snapshot or normal diagnostic output.

## 16. Implementation sequence

### 1A. Freeze schema v1

- add Rust serialization types with explicit `serde` field names;
- add schema version constant;
- add reason enum;
- add validation tests;
- document optional/required fields.

### 1B. Snapshot publisher hardening

- atomic writes;
- heartbeat without generation bump;
- `instance_id`;
- timestamp semantics;
- permissions/redaction tests.

### 1C. Bash managed-state reader

- single parsing module;
- injectable roots for tests;
- snapshot security validation;
- stale/missing/invalid normalization.

### 1D. Status/interfaces integration

- new managed-client section;
- show route readiness and transport state separately;
- compare snapshot and kernel identity;
- retain current role status unchanged.

### 1E. Diagnostics integration

- add snapshots/systemd/journal to `kk diag`;
- add managed-client checks to doctor/debuglog;
- redact and verify secrets.

### 1F. Shadow-mode disagreement reporting

- compare legacy role readiness to managed client `route_ready` when a role points to a managed interface;
- rate-limit warnings;
- no control-plane action.

### 1G. Stage 1 soak gate

Before Stage 2:

- collect real suspend/resume runs;
- collect endpoint outages/reconnect loops;
- confirm TUN identity stability;
- confirm client `route_ready` matches actual fail-closed capability;
- investigate every persistent legacy/client disagreement;
- ensure external `vpn0` path is unaffected.

## 17. Exit criteria for Stage 1

Stage 1 is complete when:

- schema v1 is stable and tested;
- all managed clients publish it;
- Bash status and diagnostics consume it;
- snapshot staleness and identity mismatches are visible;
- no secrets leak;
- routing remains unchanged;
- real-world fault tests show that client facts are more accurate than heuristic inference;
- there are no unexplained persistent disagreements between client `route_ready` and legacy readiness.

Only then may Stage 2 switch managed-target routing readiness from heuristic polling to the explicit client contract.
