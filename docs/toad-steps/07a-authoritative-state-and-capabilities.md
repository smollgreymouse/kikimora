# Toad step 07A — authoritative state, validation tokens and executable capabilities

Status: **pending**. Execute after step 06 is green.

Reviewed implementation baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

This packet fixes control-plane authority before routing ownership is allowed to move to Go. It does not yet implement NetworkManager integration or kernel route transactions; those are 07B/07C.

## Goal

Make these statements true:

1. a capability advertised by a Toad is callable and has the advertised semantics;
2. a passive Toad snapshot cannot certify the current underlay epoch;
3. only a validation tied to the current role operation + Toad generation + underlay epoch can make the role Ready;
4. Toad route-target integrity is distinct from remote session telemetry;
5. semantic controller work is never silently dropped;
6. the Toad snapshot can represent address loss, not only TUN existence.

## State semantics to use

Do not invent a new meaning while coding. Use this contract:

- `RouteReady`: the local managed route target is structurally valid and safe to route selected traffic into. It does **not** mean the remote server is reachable.
- `SessionConnected`: protocol-specific evidence that a usable remote/session path has been observed. For on-demand Xray this may remain false until real traffic occurs.
- product `RoleReady`: desired role has a current-generation Toad, `RouteReady=true`, and structural validation for the current underlay epoch completed successfully.
- remote/session failure may be surfaced as degraded session telemetry while `RoleReady` stays true; selected traffic then fails inside the stable TUN rather than escaping to physical routing.
- a missing/invalid local TUN address/MTU/link identity makes `RouteReady=false` and invalidates the role immediately.

This lets Xray be a valid route target before the first application connection while avoiding the existing false claim that “TUN UP == REALITY connected”.

## 1. Make capabilities executable

File: `toad/internal/toadruntime/runtime.go`.

Current lines 118-123 always reject `Rebind` and `RestartTransport`, while lines 218-221 advertise backend capabilities.

Replace them with actual delegation.

Required diff shape:

```diff
-func (r *Runtime) Rebind(context.Context, toadctl.UnderlayBinding) error {
-    return capabilityUnsupported(...)
+func (r *Runtime) Rebind(ctx context.Context, binding toadctl.UnderlayBinding) error {
+    rebindable, ok := r.backend.(backend.Rebindable)
+    if !ok {
+        return &toadctl.APIError{Code:"capability_unsupported", ...}
+    }
+    return rebindable.Rebind(ctx, binding)
 }

-func (r *Runtime) RestartTransport(context.Context, toadctl.UnderlayBinding) error {
-    return capabilityUnsupported(...)
+func (r *Runtime) RestartTransport(ctx context.Context, binding toadctl.UnderlayBinding) error {
+    restartable, ok := r.backend.(backend.TransportRestarter)
+    if !ok {
+        return &toadctl.APIError{Code:"capability_unsupported", ...}
+    }
+    return restartable.RestartTransport(ctx, binding)
 }
```

At current `capabilities()` add `Rebind` from `backend.Rebindable` and preserve `RestartTransportKeepingTUN` only for `backend.TransportRestarter`.

Do not advertise protocol-name-based capabilities.

### Tests

Add `toad/internal/toadruntime/runtime_test.go` if no suitable file exists.

Use tiny fake backends implementing combinations of:

- Backend only;
- Backend + Rebindable;
- Backend + TransportRestarter;
- Backend + both.

Assert:

- capability bit exactly matches interface implementation;
- supported method reaches backend once with the exact binding;
- backend error is returned, not rewritten as unsupported;
- unsupported method returns `APIError.Code == capability_unsupported`.

## 2. Make Runtime.Validate use the Validator contract

Current `runtime.go:108-116` ignores `backend.Validator` and derives validation from `Health.Connected || State=="online"`.

Replace with:

```go
func (r *Runtime) Validate(ctx context.Context) toadctl.ValidationResult {
    r.mu.Lock()
    started := r.started
    r.mu.Unlock()
    if !started { ... }

    if validator, ok := r.backend.(backend.Validator); ok {
        v := validator.Validate(ctx)
        return toadctl.ValidationResult{
            Healthy: v.Healthy,
            State: v.State,
            Reason: v.Reason,
        }
    }

    iface, err := r.readInterface()
    if err != nil || iface.IfIndex <= 0 {
        return toadctl.ValidationResult{
            Healthy:false, State:"degraded",
            Reason:"managed route target is unavailable",
        }
    }
    return toadctl.ValidationResult{
        Healthy:true, State:"ready",
        Reason:"managed route target is structurally ready",
    }
}
```

The fallback is deliberately local/structural. Do not make a generic public-network probe.

### Backend Validator semantics

Audit each existing Validator implementation once, then change as follows:

- AWG2: structural validation must require the Toad-owned TUN identity/configuration to be intact; handshake age remains session telemetry, not the only route-readiness predicate.
- Xray: Validator must require official Xray instance running + managed TUN structurally ready; it must **not** require the traffic counter introduced by step 06A to be nonzero.
- OpenConnect: Validator must require child alive + managed TUN present with current negotiated local address; authentication/session state may make validation unhealthy if the child has exited.

Do not change `Health()` back to structural-only semantics. Health/session telemetry and validation now have intentionally different roles.

**STOP/DESIGN:** if AWG2 or OpenConnect cannot provide structural validation without adding platform address data first, implement the address snapshot part in section 6 of this packet before completing the Validator. Do not substitute process existence.

## 3. Introduce an epoch/generation/operation validation token

Current `core.CompleteValidation(role, epoch,...)` at `toad/internal/core/controller.go:46-71` checks only desired role and current underlay epoch. That is insufficient for a reply from a replaced Toad process.

In `toad/internal/core/model.go` add:

```go
type ValidationToken struct {
    Role           string
    Operation      uint64
    ToadGeneration uint64
    UnderlayEpoch  uint64
}
```

In `core.Controller` add:

```go
func (c *Controller) BeginValidation(role string) (ValidationToken, bool)
func (c *Controller) CompleteValidation(token ValidationToken, result toadctl.ValidationResult) bool
```

`BeginValidation` under the controller mutex:

- role exists;
- desired=true;
- `ToadGeneration != 0`;
- snapshot `RouteReady=true`;
- capture current `Operation`, `ToadGeneration`, `underlay.Epoch`;
- set `RoleValidating`, reason `validation requested`;
- do not increment Operation.

`CompleteValidation` accepts only if all four token fields still match current state.

Success:

```go
r.Validation = result
r.State = RoleReady
r.ValidatedEpoch = token.UnderlayEpoch
r.LastError = ""
```

Failure:

```go
r.Validation = result
r.ValidatedEpoch = 0
r.State = RoleFailed // until 07B recovery/parking state refines this
r.LastError = result.Reason
```

Delete or make private the old epoch-only completion entry point so new callers cannot bypass the token.

## 4. Bind the Toad Validate request to the same generation

File: `toad/internal/toadruntime/runtime.go`.

Before handling mutating/validation methods, reject a nonzero request generation that does not equal `r.generation`.

Add helper under lock:

```go
func (r *Runtime) acceptsGeneration(g uint64) bool {
    return g == 0 || (r.started && g == r.generation)
}
```

For `Validate`, `Quiesce`, `Rebind`, `RestartTransport`, `Stop` sent by the core, use the current generation and reject stale generation with:

```text
code=stale_generation
retryable=true
```

Do not apply this to Handshake/Inspect/Subscribe.

File: `toad/internal/control/control.go`, current `ValidateRole` lines 392-443.

Required flow:

1. call `token, ok := m.product.BeginValidation(name)`;
2. capture current `process` and control socket;
3. call Toad `Validate` with `Generation: token.ToadGeneration`;
4. update passive observed snapshot only if process identity still matches;
5. call `CompleteValidation(token, result)`;
6. if completion returns false, return a retryable stale-result error and do not mutate Ready state.

This removes the current split authority where `Manager.role.validatedEpoch` and `core.RoleRuntime.ValidatedEpoch` can diverge.

## 5. Remove passive positive promotion

File: `toad/internal/core/snapshot.go`.

Current `ObserveToad` lines 118-145 must no longer do:

```go
case "ready", "online":
    r.State = RoleReady
    r.ValidatedEpoch = c.underlay.Epoch
```

New behavior:

- always update `ToadGeneration`/Toad snapshot if generation is accepted;
- if `snapshot.RouteReady == false`:
  - `ValidatedEpoch=0`;
  - if desired and not Stopped, move to `RoleValidating` or `RoleFailed` when Toad snapshot itself is `failed`;
- if `snapshot.RouteReady == true`, do **not** alter `ValidatedEpoch`;
- passive `online/ready` must not promote a non-ready product role;
- passive `failed` may invalidate it.

Apply the same rule to `ToadStateChanged` lines 205-220. Prefer deleting the duplicated state-switch and making that event call one internal `observeToadLocked` helper.

File: `toad/internal/control/control.go`, current subscription callback lines 342-357.

Delete:

```go
if snapshot.State == "online" || snapshot.State == "ready" {
    r.validatedEpoch = m.underlay.Epoch
}
```

Then remove `role.validatedEpoch` from the compatibility `role` struct once all uses are gone; the sole authoritative value is `core.RoleRuntime.ValidatedEpoch`.

## 6. Extend interface snapshots with addresses and structural readiness

This is required to detect the exact resume incident: TUN still exists but its address disappeared.

Change both schemas together.

### `toad/internal/state/state.go`

Extend:

```go
type InterfaceState struct {
    Name      string   `json:"name"`
    IfIndex   int      `json:"ifindex"`
    MTU       int      `json:"mtu"`
    Addresses []string `json:"addresses,omitempty"`
}
```

### `toad/internal/toadctl/protocol.go`

Extend Snapshot:

```go
Addresses []string `json:"addresses,omitempty"`
```

Do not bump the local Toad control ProtocolVersion solely for an additive JSON field. Unknown fields are backward-compatible with current JSON framing.

### `toad/internal/toadruntime/runtime.go`

Extend runtime `Interface` with `Addresses []string`.

Update the platform reader in `cmd/kikimora-toad` to enumerate interface addresses and normalize them to CIDR strings. Sort before comparison.

Change `fromHealth`:

```go
s.RouteReady = interfaceStructurallyReady(cfg, iface)
s.Interface.Addresses = append([]string(nil), iface.Addresses...)
```

Add helper:

- name/ifindex valid;
- MTU > 0;
- for AWG2/Xray, every configured `cfg.Address` exists in observed interface addresses;
- for OpenConnect, at least one non-link-local address exists because address is server-negotiated.

Do not infer route readiness solely from `IfIndex > 0`.

Copy addresses through `toadSnapshotLocked`, `control.toadSnapshot`, `stateFromToadSnapshot`.

### Tests

Required cases:

- interface exists, correct address -> route ready;
- interface exists, address removed -> not route ready;
- AWG/Xray multiple expected addresses require full expected set;
- OpenConnect negotiated IPv4 only -> ready;
- link-local-only OpenConnect interface -> not ready.

Repair of address drift belongs to 07C; this packet only makes it observable and authoritative.

## 7. Schedule initial/current-epoch validation without snapshot promotion

Once passive snapshots no longer promote Ready, a new Toad needs an explicit validation trigger.

In `control.Manager` add per-role:

```go
validationInFlight bool
```

In `subscribeToad`:

- update observation under `m.mu`;
- compute `needValidate := snapshot.RouteReady && product role desired && product.ValidatedEpoch != current underlay epoch && !validationInFlight`;
- set `validationInFlight=true`;
- unlock;
- if needed, start one goroutine calling `ValidateRole`;
- in defer, clear the in-flight bit only if process identity still matches.

Never call Toad IPC while holding `m.mu`.

Repeated snapshots coalesce into one validation.

Resume path should use the same scheduler rather than a separate competing validation mechanism. Refactor `validateEnabledRolesAfterResume` to mark roles stale and request the per-role validation scheduler.

## 8. Fix initial-underlay detection

File: `toad/internal/core/snapshot.go`, current lines 83-113.

Current code assigns `c.underlay = next` before computing `initial`, so:

```go
initial := c.underlay.Epoch == 0
```

is false after the assignment.

Move:

```go
initial := c.underlay.Epoch == 0
```

before epoch normalization/assignment and add a unit test proving the first underlay observation does not spuriously mark an already-starting role as a recovery from a prior underlay.

## 9. Remove lossy semantic event submission

File: `toad/internal/core/snapshot.go:44-48`.

Do not keep:

```go
select {
case c.events <- e:
default:
}
```

Split event types:

- semantic state transitions: desired changes, operation completion, resume invalidation — reliable;
- observation invalidation: may coalesce.

Simplest acceptable change for this packet: make `Submit` context-aware and blocking:

```go
func (c *Controller) Submit(ctx context.Context, e Event) error {
    select {
    case c.events <- e:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

Update `Reconciler` callers.

If a high-frequency netlink path would block on this API, it must not submit raw netlink events to it; 07C owns coalescing.

Do not create an unbounded goroutine per event.

## 10. Tests for authority

Add controller tests that explicitly cover:

- passive online snapshot after underlay epoch changes does not set current ValidatedEpoch;
- successful current token does;
- token with old epoch rejected;
- token with old operation rejected;
- token with old Toad generation rejected;
- passive route_ready=false clears validated epoch immediately;
- duplicate positive snapshots do not produce duplicate validation calls;
- event queue saturation cannot silently lose semantic desired-state transition.

Update the existing UI/API projection tests to use core ValidatedEpoch only.

## Acceptance commands

```bash
cd toad
go test ./internal/toadruntime ./internal/core ./internal/control ./...
go test -race ./internal/toadruntime ./internal/core ./internal/control
go vet ./...
cd ..
./linux/tests/toad/run-isolated.sh awg2-interop
./linux/tests/toad/run-isolated.sh xray-lifecycle
./linux/tests/toad/run-isolated.sh xray-interop
./linux/tests/toad/run-isolated.sh openconnect-interop
```

All current CI workflows must remain green.

## STOP/DESIGN conditions

Stop and return to the planner instead of improvising if:

1. a protocol needs a meaning of structural validation incompatible with the contract at the top of this file;
2. OpenConnect cannot expose a stable observed address without parsing a secret-bearing artifact;
3. making Toad generation mandatory breaks a real startup race not covered by current IPC framing;
4. an existing client depends on passive `online -> Ready` promotion.

Create a dedicated `07a-<topic>.md` with concrete evidence before coding around the issue.

## Executor report

Return:

1. commits;
2. exact state/validation contract implemented;
3. capability matrix before/after;
4. stale token tests;
5. address-loss detection tests;
6. passive-snapshot non-promotion evidence;
7. protocol interop results;
8. CI run IDs;
9. any STOP/DESIGN follow-up.
