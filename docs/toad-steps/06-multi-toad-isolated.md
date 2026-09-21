# Toad step 06 — simultaneous three-protocol multi-Toad isolation

Status: **pending**. Execute only after `06a-current-head-baseline.md` is green.

Reviewed code baseline for this plan: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

## Why this step is still open

The current runner has:

```text
core-isolated:
    awg2-interop.sh
    then xray-interop.sh
    then openconnect-interop.sh
```

and the UI mode does the same. That proves three protocols individually, not simultaneous multi-Toad isolation. There is no `multi-toad` runner mode and no dedicated CI job.

The production topology now includes AWG2, Xray and OpenConnect, so the old two-protocol packet is strengthened to all three.

## Goal

Run three real Toad processes at the same time with three real protocol servers, distinct TUNs and independent failure domains:

```text
one client namespace
  kikimora-toad awg  -> kk-awg0  -> official amneziawg-go server
  kikimora-toad xray -> kk-xray0 -> official Xray REALITY/VLESS/Vision server
  kikimora-toad oc   -> kk-oc0   -> ocserv

no host default route
no NAT
no public data path
```

The strong form — all three clients in the same client namespace — is required. Separate client namespaces are insufficient because they cannot expose interface/routing/process interference on the same host network stack.

This step remains a protocol/process Stage 0 gate. It does not enable production Go routing ownership or system-wide default routes.

## Files to add/change

Add:

`linux/tests/toad/multi-toad-interop.sh`

Change:

- `linux/tests/toad/run-isolated.sh`;
- `.github/workflows/toad.yml`;
- optionally `linux/tests/toad/lib/netns.sh` only for genuinely generic helpers.

Do not modify protocol implementation code in this step unless the new simultaneous gate exposes a real protocol isolation defect. If it does, stop and write a dedicated fix packet before changing the backend.

## Topology and addresses

Use one client namespace:

`toad-multi-client-$$`

and three server namespaces:

```text
toad-multi-awg-server-$$
toad-multi-xray-server-$$
toad-multi-oc-server-$$
```

Use non-overlapping /30 underlays:

```text
AWG:  192.0.2.1/30      <-> 192.0.2.2/30
Xray: 198.51.100.1/30   <-> 198.51.100.2/30
OC:   203.0.113.1/30    <-> 203.0.113.2/30
```

Use distinct private payload targets:

```text
AWG:  10.77.0.1
Xray: 10.78.0.1:8080
OC:   10.79.0.1:8080
```

Expected client TUNs:

```text
kk-awg0
kk-xray0
kk-oc0
```

No namespace may have a default route.

## Reuse exact existing fixture logic

Do not redesign credentials/protocol setup.

Use the production-tested blocks from the current scripts as source:

### AWG2

From `linux/tests/toad/awg2-interop.sh`:

- UAPI helpers: current lines 50-130;
- client config generation: around lines 352-384;
- reference UAPI config: around lines 385-410;
- official server startup/configuration: around lines 220-255;
- payload/handshake assertions and stable-ifindex logic: current lines 458-524.

Change only namespace/interface/address variables so the fixture coexists with the other two protocols.

### Xray

From `linux/tests/toad/xray-interop.sh`:

- ephemeral REALITY credentials and reference config: current lines 109-150;
- client config: from current line 152 until the client config EOF;
- `payload_probe` and server lifecycle helpers: current lines 221-255;
- stable-ifindex/server-loss checks: current lines 307-358.

Change the Xray underlay from `192.0.2.0/30` to `198.51.100.0/30` and payload to `10.78.0.1`.

Keep the local hermetic cover helper.

### OpenConnect

From `linux/tests/toad/openconnect-interop.sh`:

- certificate/SPKI and synthetic password setup: current lines 76-90;
- ocserv config: current lines 92-118;
- Toad config: current lines 131 onward;
- payload probe: current lines 207-231.

Change the OpenConnect underlay to `203.0.113.0/30`, pool to a non-conflicting subnet, and payload to `10.79.0.1`.

Keep `isolate-workers=false` in this disposable test server for the existing distro/seccomp reason.

## Script structure

The new script must have explicit phases rather than starting three old scripts as background jobs.

Required function layout:

```bash
setup_namespaces
setup_awg_fixture
setup_xray_fixture
setup_openconnect_fixture
start_all_servers
start_all_toads
wait_all_ready
record_initial_identity
prove_all_payloads
phase_awg_failure
phase_xray_failure
phase_openconnect_failure
phase_underlay_isolation
phase_deliberate_toad_stop
cleanup
```

Use one cleanup trap which can kill all children and delete all four namespaces even after a failure.

Record PID and ifindex for each Toad immediately after readiness.

## Initial simultaneous proof

Before any failure injection, assert in one time window:

- all three Toad PIDs alive;
- all three TUN names present;
- all three ifindices distinct and recorded;
- AWG encrypted payload succeeds;
- Xray REALITY/VLESS/Vision payload succeeds;
- OpenConnect/ocserv payload succeeds;
- each Toad state directory is distinct;
- no root namespace `kk-*` interface exists;
- no client namespace default/split-default route exists.

Print one compact evidence line with all PIDs/ifindices.

## Failure phases

### AWG server down

Stop only official AWG reference server.

Assert while it is down:

- AWG Toad remains alive;
- `kk-awg0` same ifindex;
- AWG-selected private payload is not reachable through Xray, OpenConnect or physical underlay;
- Xray payload keeps succeeding;
- OpenConnect payload keeps succeeding;
- Xray/OC PIDs and ifindices unchanged.

Restart AWG server and prove encrypted traffic recovers without restarting any Toad.

### Xray server down

Stop only Xray reference server/cover as appropriate for the existing fixture.

Assert:

- Xray Toad remains alive;
- `kk-xray0` same ifindex;
- new Xray payload does not succeed by another route;
- AWG and OpenConnect continue working.

Restart and prove Xray payload recovery with same Toad/TUN.

### OpenConnect server down

Stop only ocserv.

Assert:

- OpenConnect Toad/child remains in its expected reconnect behavior;
- `kk-oc0` identity follows the current OpenConnect stable-TUN contract;
- OC private payload cannot fall through another Toad/underlay;
- AWG and Xray remain working.

Restart ocserv and prove recovery without touching AWG/Xray.

**STOP/DESIGN:** if official OpenConnect necessarily destroys/recreates the TUN across a *server process restart* even though its ordinary underlay recovery gate keeps it stable, stop and capture the exact openconnect/ocserv behavior. Do not fake stable ifindex. Write `docs/toad-steps/06-openconnect-server-restart-semantics.md` before changing the invariant.

### One underlay down

Bring down only the AWG client-side veth.

Assert Xray and OpenConnect remain fully operational and unchanged. Restore it and prove AWG recovery.

A symmetric Xray or OC underlay phase may be kept if runtime is reasonable; at minimum one independent link failure is mandatory because all three clients share one client namespace.

### Deliberate one-Toad shutdown

Stop only AWG Toad.

Assert:

- `kk-awg0` disappears according to AWG deliberate-shutdown contract;
- Xray and OpenConnect process/interface/payload remain unchanged.

Do not restart all clients as cleanup until this assertion is complete.

## Explicit route/no-fallback proof

Because the client namespace has no default route, add only explicit host routes needed for each test payload.

For each failed protocol payload, record:

```bash
ip -n "$CLIENT_NS" route get <payload>
```

and prove it remains targeted at the failed protocol TUN or becomes explicit unreachable according to the fixture. It must never select either healthy Toad or an underlay veth.

Do the same for IPv6 only if the three-protocol fixture already has IPv6 endpoints; full production IPv6 parking belongs to step 07B.

## Runner change

File `linux/tests/toad/run-isolated.sh`.

Add:

```bash
run_multi_toad_interop() { ... }
```

with all dependencies prepared once.

Add mode:

```text
multi-toad
```

Do **not** add it to `all` until the dedicated CI gate is green. After the first stable green run, include it in `all`.

`core-isolated` must not be renamed to `multi-toad`; it remains a serial integration smoke.

## CI change

File `.github/workflows/toad.yml`.

Add job:

`linux-multi-toad-interop`

It must depend on the shared Linux smoke build and install OpenConnect/ocserv exactly as the existing OpenConnect job does.

Run:

```bash
sudo env   TOAD_BIN=...   AWG_REF_BIN=...   XRAY_REF_BIN=...   XRAY_COVER_BIN=...   OPENCONNECT_BIN=...   OCSERV_BIN=...   bash linux/tests/toad/multi-toad-interop.sh
```

Do not use public networking for VPN test payloads.

## Stage 0 completion rule

After this gate is green, update `docs/toad-roadmap.md`:

- Step 06 -> complete;
- Stage 0 protocol isolation -> complete.

Do **not** mark the Go control plane/cutover complete. That is the 07A-07D sequence.

## Executor report

Return:

1. commits;
2. CI run/job IDs;
3. three simultaneous Toad PIDs;
4. three initial ifindices;
5. proof of simultaneous payload success;
6. each server-failure result;
7. underlay-failure result;
8. no-fallback route evidence;
9. one-Toad shutdown isolation;
10. cleanup/no-root-leak result;
11. any STOP/DESIGN packet created.
