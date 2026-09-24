# Toad step 07F.2C — stable-TUN recovery hand-off after AWG transport reset

Status: **CURRENT**.

Baseline privileged HEAD:
`46c7aaa3cf0ee32c739083f3661bbed44d6ddbe7`.

Purpose: close the Phase B recovery-state-machine failure exposed after AWG
validation became health-aware, while restoring the documented structural
meaning of `route_ready`.

## Proven privileged baseline

Fresh privileged results at `46c7aaa`:

- route-parking: PASS;
- multi-toad: PASS;
- xray-interop: PASS;
- orchestration Phase 3: PASS;
- Phase A: PASS;
- Phase 6 non-recursive endpoint: PASS;
- Phase B canonical underlay epoch 1 -> 2: PASS;
- Phase B recovery: FAIL.

The previous false Ready condition is fixed: AWG loses current-epoch validation
and publication after its transport becomes unhealthy.

The new failure is:

```text
state=Failed
route_ready=false
validated_underlay_epoch=0
publication.published=false
parking.active=false
recovery.step=validate
recovery.last_error=Toad "awg" is not eligible for current-epoch validation
recovery.attempt=>600
```

## Architecture constraint

Existing architecture is authoritative:

`route_ready` means that the managed interface is a valid fail-closed route
target. It does not require the remote peer to be healthy.

Therefore:

```text
TUN structurally valid
protocol unhealthy
route_ready=true
role degraded/recovering
```

is valid and intentional.

Do not use `route_ready=false` as a proxy for protocol liveness.

## Root cause

On underlay epoch change a Ready role becomes Recovering.

For AWG:
- capability selection chooses RestartTransportKeepingTUN;
- recovery prepares fail-closed state and withdraws publication;
- RestartTransport returns while the new AWG session is still connecting;
- recovery immediately calls Validate;
- transient validation failure is classified as fatal;
- Engine moves the role to Failed;
- subsequent recovery attempts restart from the disruptive sequence instead of
  waiting for the same stable Toad/transport to become healthy.

This violates the level-triggered recovery contract.

## Required implementation

### 1. Restore structural RouteReady

File:
- `toad/internal/toadruntime/runtime.go`

Revert the 46c7aaa coupling between AWG `RouteReady` and
`backend.Health.Connected`.

Preserve:
- structural interface identity/address/MTU readiness;
- health-aware AWG `Backend.Validate()`;
- structural-only interface repair decisions.

Update runtime tests accordingly:
- stale AWG handshake does not make the structurally valid TUN disappear as a
  route target;
- stale handshake still must not validate the role healthy.

### 2. Treat transient validation as recoverable

Introduce an explicit recovery classification for a validation result that is
temporarily unhealthy/not yet eligible after transport restart.

Requirements:
- no terminal RoleFailed for expected connecting/reconnecting validation;
- preserve recovery step/operation/epoch;
- do not immediately repeat RestartTransport on every retry;
- same Toad generation/TUN remains authoritative;
- when a later snapshot proves validation can succeed, hand control back to
  normal validation/activation;
- stale process/operation/epoch guards remain mandatory.

Prefer a protocol-neutral mechanism. Do not special-case role name or AWG in
core/control.

### 3. Resume from failed recovery step

When retrying the same operation+epoch after a recoverable validation wait,
resume at `RecoveryValidate` rather than replaying:
- Park;
- Withdraw;
- Quiesce;
- ApplyEndpoint;
- RestartTransport.

After validation succeeds, continue:
- Publish;
- ResyncLeshy;
- ObserveRestoration.

A changed operation, Toad generation or underlay epoch invalidates the resume
context and must recompute recovery normally.

### 4. Validation hand-off from live Toad snapshots

A healthy later snapshot from the same live process must be able to trigger the
normal validation/activation path even while product state is Recovering,
provided no older recovery worker is still authoritative.

Avoid validation/recovery races using existing:
- process identity;
- generation;
- operation;
- underlay epoch;
- recoveryInFlight / validationInFlight guards.

### 5. Correct Phase B acceptance

Do not require `route_ready=false` solely because AWG peer health is stale.

During the AWG underlay outage require:
- AWG is not Ready and is not current-epoch validated;
- recovery state is non-terminal;
- publication is withdrawn while recovery transaction is incomplete;
- TUN ifindex remains stable;
- an AWG-selected destination still resolves to the stable AWG TUN or to an
  explicit fail-closed park;
- the selected destination must never resolve/fall through to the physical
  backup default.

For the current hermetic fixture, the connected AWG payload destination
`10.77.0.1` is suitable evidence:
- route lookup must continue to use `kk-awg0`;
- payload must be unreachable while the physical AWG endpoint path is down;
- route lookup must not select the backup Xray physical interface.

After restoring primary underlay:
- canonical epoch advances again;
- same AWG TUN ifindex survives;
- a fresh handshake occurs;
- validation succeeds at the current epoch;
- publication returns;
- payload recovers.

### 6. Tests before sudo

Add/adjust deterministic tests for:
- structural AWG RouteReady independent of peer health;
- health-aware AWG Validate;
- recoverable validation error does not produce terminal Failed;
- retry resumes at Validate and does not repeat RestartTransport;
- healthy same-process snapshot can hand off Recovering -> validation;
- stale generation/operation/epoch cannot resume old recovery.

Then run:

```bash
cd toad
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
cd ..

bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash desktop/tests/test_packaging.sh
bash linux/tests/toad/run-rootless.sh model
```

Try all four rootless kernel modes; exit 77 is an environment limitation if
`/proc/self/uid_map` remains blocked.

### 7. Operator boundary

Only after all non-privileged gates are green, rerun:

```bash
bash run-privileged-gates.sh
```

Required fresh evidence:
- route-parking PASS;
- multi-toad PASS;
- orchestration A-F PASS;
- xray-interop PASS.

Do not proceed to 08A until this is green.
