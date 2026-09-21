# Step 06 follow-up — OpenConnect server restart semantics

Status: **design resolved from CI evidence; apply to step 06 gate**.

Evidence run:

- workflow: Toad core run `35614657729`;
- job: `linux-multi-toad-interop` / `106382348383`;
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

For `phase_openconnect_failure`, simulate an actual ocserv **crash**, not administrative shutdown:

1. find processes whose network namespace is the disposable OpenConnect server namespace;
2. among those, select only processes whose `/proc/<pid>/exe` resolves to the configured `OCSERV_BIN`;
3. SIGKILL those ocserv main/worker processes;
4. do not kill the private HTTP payload process;
5. wait until TCP 4443 is gone;
6. assert:
   - OpenConnect Toad remains alive;
   - same `kk-oc0` ifindex;
   - explicit payload route remains on `kk-oc0`;
   - new OpenConnect payload fails;
   - AWG and Xray payloads continue;
7. restart ocserv from the same fixture;
8. prove OpenConnect payload recovers with the same Toad and ifindex.

Normal cleanup may still use graceful process termination because cleanup does not claim recovery semantics.

## Failure condition

If a crash with no protocol-level server disconnect still destroys `kk-oc0`, stop step 06 again. That would prove server transport loss itself violates the expected official-client stable-TUN contract and would require a larger OpenConnect lifecycle change before Stage 0 can be accepted.
