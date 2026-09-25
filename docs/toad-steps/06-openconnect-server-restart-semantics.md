# Step 06 follow-up — OpenConnect server restart semantics

Status: **design resolved from CI evidence; apply to step 06 gate**.

Evidence runs:

- graceful-stop evidence: Toad core run `35614657729`, job `106382348383`;
- full-crash/restart evidence: Toad core run `35615338691`, job `106384643711`;
- Ubuntu OpenConnect: `v9.12-1ubuntu1.24.04.1`;
- ocserv: `1.2.4`.

## Observation

The simultaneous test successfully reached:

```text
multi-toad identity:
  AWG         pid=3593 ifindex=2
  Xray        pid=3594 ifindex=3
  OpenConnect pid=3595 ifindex=4

payloads:
  AWG=ok Xray=ok OpenConnect=ok
```

The AWG server-failure phase passed and recovered with the same AWG TUN.

The Xray server-failure phase passed and recovered with the same Xray TUN.

The original OpenConnect phase then stopped ocserv with SIGTERM. Immediately afterwards `kk-oc0` no longer existed.

The OpenConnect log gives the cause explicitly:

```text
Received server disconnect
Send BYE packet: Server request
Session terminated by server; exiting.
```

Therefore SIGTERM of this ocserv version is not an ordinary unreachable/crashed-server transport failure. It performs a graceful protocol-level administrative disconnect. The official OpenConnect client treats that as terminal and exits; its owned TUN is removed.

This differs from the existing isolated OpenConnect underlay-loss gate, where connection reachability disappears without a server-requested disconnect and the same child/TUN survives recovery.

A second experiment killed both ocserv processes with SIGKILL, so no protocol BYE was sent. In that run:

- `kk-oc0` survived the immediate server crash with the same ifindex;
- the selected route remained pinned to `kk-oc0`;
- OpenConnect entered its documented reconnect path after a TLS read error;
- after the fresh ocserv process started, the client reconnected to HTTPS but received `401 Cookie is not acceptable`;
- OpenConnect then logged `Cookie was rejected by server; exiting.` and removed its child-owned TUN.

So a complete ocserv process restart is also not a same-session recovery fixture: the replacement server has lost the in-memory authenticated cookie/session authority required for fast reconnect. This is a server-authentication-state reset, not evidence that transport loss itself destroys the TUN.

## Contract decision

Do **not** weaken or fake the stable-TUN assertion.

Split two semantics:

### Recoverable transport/server failure

Examples:

- physical underlay loss;
- packets to server disappear;
- server crashes without sending an AnyConnect/CSTP disconnect.

Expected:

- official OpenConnect client remains in reconnect path;
- `kk-oc0` remains the same interface;
- payload fails closed during outage;
- connection recovers after server/transport restoration without restarting Toad.

This is the server-failure semantic that belongs in the Stage 0 simultaneous gate.

### Administrative/server-requested disconnect

A valid server-requested disconnect is terminal from the official OpenConnect client’s perspective.

Expected at the current architecture level:

- openconnect child exits;
- its child-owned `kk-oc0` may disappear;
- Toad reports degraded/child-exited state;
- other Toads must remain untouched;
- product routing must fail closed;
- later Go recovery may choose a full OpenConnect child/Toad restart.

The “stable TUN through ordinary recovery” invariant does not redefine an explicit server instruction to terminate as an ordinary transient failure.

Step 07A/07B must preserve this distinction when structural readiness/recovery is implemented.

## Step 06 implementation

The simultaneous Stage 0 gate must exercise a **server-side session worker crash**, not a graceful whole-server shutdown and not a whole-server authentication-state reset.

For `phase_openconnect_failure`:

1. keep the ocserv main process/listener and its authenticated cookie authority alive;
2. enumerate ocserv processes in the disposable server namespace;
3. select only ocserv child/session processes whose executable matches `OCSERV_BIN` and whose PID differs from the recorded main `OCSERV_PID`;
4. SIGKILL those session worker process(es), which produces a real server-side process failure without a CSTP administrative BYE;
5. assert:
   - ocserv main/listener remains alive;
   - OpenConnect Toad remains alive;
   - `kk-oc0` keeps the same ifindex;
   - no `DELLINK` for `kk-oc0` is observed and the same ifindex remains;
   - if OpenConnect's reconnect script flushes/re-adds the negotiated address and Linux drops the harness-owned synthetic /32, the client namespace still has no default/split-default route, so the destination is unreachable rather than falling through;
   - the harness, as the Stage 0 route owner, reconciles its synthetic /32 back onto the same `kk-oc0`;
   - AWG and Xray remain alive, same-ifindex and usable;
6. wait for the official OpenConnect cookie reconnect path to recover real payload through a newly created ocserv worker;
7. assert the OpenConnect Toad and `kk-oc0` identity are still unchanged.

The separate OpenConnect underlay-loss test already covers complete transport unreachability while keeping server authentication state alive. Together these two tests cover client-side network loss and server-side session-process failure.

A full ocserv restart is intentionally recorded as a different condition that requires re-authentication and therefore later core-level full OpenConnect recovery. Normal cleanup may still terminate ocserv gracefully because cleanup does not claim recovery semantics.

## Failure condition

If killing only the active ocserv session worker (while the main/cookie authority remains alive) causes the official client to exit or produces a kernel `DELLINK`/new TUN identity for `kk-oc0`, stop step 06 again. Do not weaken the stable-TUN assertion. Loss of a harness-owned selected route during the route-free script's address flush is not TUN recreation; prove fail-closed absence of fallback, then let the harness reconcile the route.
