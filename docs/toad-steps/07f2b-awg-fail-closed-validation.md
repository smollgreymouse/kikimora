# Toad step 07F.2B — AWG fail-closed validation after canonical underlay loss

Status: **IMPLEMENTED LOCALLY; PRIVILEGED VERIFICATION REQUIRED**.

Baseline evidence HEAD: `a57c3d1d876f870820ca3f7e76e6e45ecf790c7f`.

Purpose: close the real Phase B failure exposed by the privileged 07F.2 acceptance run, without weakening AWG health semantics and without changing Xray/OpenConnect behavior unless subsequent evidence requires it.

## Proven baseline

At HEAD `a57c3d1`:

- route-parking: PASS;
- multi-toad: PASS;
- xray-interop: PASS;
- model fallback: PASS;
- orchestration Phase 3: PASS;
- orchestration Phase A: PASS;
- full-tunnel AWG endpoint non-recursive route: PASS;
- canonical underlay mutation: primary AWG underlay -> backup Xray underlay, epoch 1 -> 2: PASS;
- Phase B fail-closed assertion: FAIL.

Observed blocking state after physical AWG-underlay loss:

```text
AWG:
  state=Ready
  route_ready=true
  validated_underlay_epoch=2
  endpoint.applied_underlay_epoch=2
  publication.published=true
  parking.active=false
  session.connected=false
```

This is current production behavior, not a fixture parser/quoting error.

## Root cause

AWG health parsing already has the intended liveness contract:

- recent handshake <= 30 s -> online/connected;
- stale handshake > 30 s -> reconnecting/disconnected;
- no handshake -> connecting/disconnected.

But `awg2.Backend.Validate()` currently ignores that health and returns Healthy whenever the official AWG device is attached to the managed TUN.

The runtime independently derived `RouteReady` from structural TUN readiness, so a structurally intact TUN could remain route-ready while the AWG transport had lost connectivity.

There were therefore two authority gaps:

1. `Validate()` ignored AWG handshake health, so a disconnected transport could validate a new underlay epoch;
2. after such validation, a later stale-handshake snapshot did not invalidate that epoch because `RouteReady` stayed structurally true.

The implementation closes both gaps for AWG while keeping interface-repair decisions structural-only.

## Required contract

Do not change `recentHandshakeWindow`.

For AWG validation/readiness:

1. current AWG health must be readable/parseable;
2. the peer must have a recent handshake (`Connected=true`);
3. otherwise validation is unhealthy and carries a precise reason derived from AWG health;
4. AWG `RouteReady` is true only when the managed interface is structurally ready **and** the AWG peer has a recent handshake;
5. interface repair remains gated only by structural readiness, so a stale handshake cannot trigger pointless TUN/address repair.

A stale/missing handshake must neither validate the role as Ready at a new underlay epoch nor leave an already-validated role route-ready after liveness expires.

## Implementation sequence

### 1. AWG backend validation

File:
- `toad/internal/backend/awg2/backend.go`

Make `Validate(ctx)` evaluate current AWG health instead of checking only `b.dev != nil`.

Avoid nested mutex locking: do not hold `b.mu` while calling `Health()`.

Expected results:
- stopped/no device -> unhealthy;
- health read/parse failure -> unhealthy;
- no handshake -> unhealthy;
- stale handshake -> unhealthy;
- recent handshake -> healthy/ready.

### 2. Runtime readiness + unit coverage

Files:
- `toad/internal/toadruntime/runtime.go`
- `toad/internal/backend/awg2/awg2_test.go`
- `toad/internal/toadruntime/runtime_test.go`

Implemented:
- pure AWG validation mapping for stopped/connecting/stale/degraded/online health;
- AWG `RouteReady=false` without a recent handshake;
- recent handshake restores AWG `RouteReady` when the interface is structurally valid;
- health-only loss does not invoke interface repair;
- structural repair still uses structural readiness rather than transport liveness.

Do not weaken existing health parser tests.

### 3. Acceptance timing

The real AWG health contract intentionally allows a 30-second recent-handshake window. Phase B must allow enough time for that contract to expire plus polling/scheduling margin.

File:
- `linux/tests/toad/go-orchestration-acceptance.sh`

The AWG fail-closed wait budget is 45 s, safely above the existing 30 s handshake window.

Do not change the predicate:
- AWG must become Recovering/Degraded;
- route_ready must be false at core/product projection or the selected route must be parked/fail-closed according to the authoritative state machine;
- unrelated roles may transiently revalidate but must avoid terminal failure/unnecessary identity recreation.

If the stricter AWG validation causes a different state-machine failure, stop and diagnose from evidence instead of weakening the assertion.

### 4. Model/local validation before sudo

Run:

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

Also run targeted AWG tests during iteration.

Because the current MCP executor forbids mapped user namespaces, kernel modes are expected to return environment status 77 here. Do not use sudo from the executor.

### 5. Operator boundary

Only after all local/model gates pass, request the operator rerun:

```bash
bash run-privileged-gates.sh
```

The required fresh privileged evidence is:
- route-parking;
- multi-toad;
- orchestration-acceptance A-F;
- xray-interop.

## Completion

07F.2B is complete only when:
- AWG validation rejects stale/missing handshake;
- all local Go/model/static gates pass;
- privileged orchestration Phase B observes fail-closed and recovery;
- phases C-F also pass;
- route-parking, multi-toad and xray-interop remain green;
- `test-report.txt` is rewritten for the final tested HEAD.
