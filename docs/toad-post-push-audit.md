# Post-push audit of PR #27

Audit baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

Status: **reviewed, remediation required before Go ownership cutover**.

This document is a code-review record, not an implementation claim. It reconciles the large Go orchestration/UI push with the older Toad roadmap and records the exact blockers that must be fixed before privileged cutover.

## Roadmap position

The old protocol Stage 0 is **not complete**. Steps 01-05 remain complete, but the former step 06 simultaneous multi-Toad gate was never implemented: `linux/tests/toad/run-isolated.sh` runs AWG2, Xray and OpenConnect sequentially in `core-isolated`/`core-ui-isolated`; it has no simultaneous `multi-toad` mode or CI job.

The new push nevertheless implements much of the previously planned Go control plane: desired/observed state, per-Toad IPC, process supervision, endpoint policy, parking, Leshy publication, underlay observation, recovery ordering, systemd cutover, Qt/QML UI and partial macOS adapters.

Therefore the project now has two facts at once:

- substantial post-Stage-0 code exists;
- its prerequisite simultaneous protocol gate and several safety contracts are still unverified or incorrect.

Do not delete the new control-plane work. Stabilize it in the packets linked from `docs/toad-roadmap.md`.

## What is structurally good

The new code moved in the right architectural direction:

- `core.Controller` separates desired state from observed Toad state;
- Toad IPC has generation/revision identity instead of relying only on `state.json`;
- `internal/supervisor` centralizes child lifetime and process-group shutdown;
- endpoint, routing, parking and Leshy are explicit packages instead of shell side effects hidden in reconnect scripts;
- recovery is represented as an ordered sequence with injectable steps;
- cutover has a global ownership file and rollback command;
- underlay and sleep have platform boundaries;
- the desktop talks to one core rather than directly owning VPN processes;
- protocol selection is capability-based rather than protocol-name branches in the controller.

Those choices should be preserved while fixing the issues below.

## P0 findings

### A1 — advertised Toad transport capability is not executable

Current code:

- `toad/internal/toadruntime/runtime.go:118-123` returns `capability_unsupported` from both `Runtime.Rebind` and `Runtime.RestartTransport`;
- `runtime.go:218-221` nevertheless advertises `RestartTransportKeepingTUN=true` whenever the backend implements `backend.TransportRestarter`;
- AWG2 implements `TransportRestarter` in `toad/internal/backend/awg2/backend.go`.

Result: the core can select `ActionRestartTransport`, then the live Toad rejects the method that its own capability report advertised.

Required fix is specified in `docs/toad-steps/07a-authoritative-state-and-capabilities.md`.

### A2 — Xray transport health is currently TUN-link health

`toad/internal/backend/xray/backend.go:90-129` sets:

```go
connected := iface.Flags&net.FlagUp != 0
...
state = "online"
Connected: connected
```

A VLESS/REALITY server can be absent while the Xray TUN is still UP. This is not theoretical: current CI run `35593479886`, job `linux-xray-lifecycle`, failed because an unreachable-server lifecycle fixture immediately published `state: online`.

This also makes resume validation too weak because `Runtime.Validate` currently derives health from `Backend.Health`.

The fix must preserve the distinction:

- TUN/interface usable as a fail-closed route target;
- protocol/session proven healthy.

Do not weaken `xray-lifecycle-netns.sh` to accept `online` without a server.

### A3 — passive Toad snapshots can forge current-underlay validation

`toad/internal/core/snapshot.go:118-145` currently promotes every Toad snapshot with state `ready` or `online` to:

```go
r.State = RoleReady
r.ValidatedEpoch = c.underlay.Epoch
```

That means a streamed snapshot can undo `RoleValidating`/`RoleRecovering` after an underlay generation change without an epoch-bound validation completing.

`ToadStateChanged` in the same file also directly mutates product state from transport strings.

Rule required:

> passive observation updates Toad facts only; only a successful epoch-bound validation/recovery completion may advance `ValidatedEpoch`.

### A4 — semantic core events can be silently dropped

`core.Controller.Submit` at `toad/internal/core/snapshot.go:44-48` uses a non-blocking send and silently drops when the 64-entry channel is full.

Desired-state changes, operation completion and resume invalidation are semantic events and cannot be lossy. Only “please resnapshot” invalidations may be coalesced.

### A5 — parking is IPv4-only

Both `toad/internal/parking/manager.go` and `toad/internal/parking/ownership.go` accept only `/32`.

That violates the documented IPv4/IPv6 fail-closed contract. A selected IPv6 `/128` can lose its Toad route and fall through to the physical IPv6 default route.

The predicate must use host-prefix semantics:

```go
prefix.IsValid() && prefix.Bits() == prefix.Addr().BitLen()
```

and the privileged regression must cover direct IPv6 escape.

### A6 — Linux route executor is serialized, but not transactional or ownership-reconciling

`toad/internal/platform/linux/netlink/routes.go:24-33` applies operations sequentially and returns after the first error without rollback.

`ApplyEndpointPolicy` adds/replaces desired endpoint routes/rules but never deletes stale previously-owned endpoint routes/rules when the candidate set changes.

`applyOperation` constructs a `netlink.Route` without copying `Operation.Protocol`, although parking ownership logic later filters by protocol.

The result is a mismatch between the documented “complete desired set / transaction” model and actual additive kernel state.

### A7 — route parking ownership can capture unrelated static routes

`PrepareWithdrawalFromKernel` discovers main-table static host routes via an interface and parks them. The existing `Checkpoint.Baseline`/ownership helpers are not wired into the recovery path that calls it.

A user-created static host route using the same interface can therefore look Kikimora-owned.

Before Go cutover, route ownership must be explicit enough to distinguish Kikimora/Leshy-selected routes from unrelated static routes.

### A8 — stable-TUN recovery omits endpoint rebind

`toad/internal/core/recovery_runner.go:39-43`:

- `ActionRebind` has no `RecoveryApplyEndpoint`;
- `ActionRestartTransport` has no `RecoveryApplyEndpoint`;
- only full Toad restart applies endpoint policy.

After Wi-Fi/gateway/source change, restarting AWG transport while retaining the old physical endpoint pin can leave the VPN unable to recover.

For stale-underlay actions the new endpoint policy must be committed before transport/rebind validation.

### A9 — recovery may declare Ready while parking remains active

The automatic underlay and resume callers invoke:

```go
Engine.Recover(..., includeRestoration=false)
```

at `control.go:641` and `control.go:731`.

`Engine.Recover` then unconditionally sets `RoleReady` after the selected sequence. With `includeRestoration=false`, `RecoveryObserveRestore` never runs. There is no separate continuously-wired route restoration observer that guarantees release before Ready.

A role must not be advertised Ready while its selected routes remain parked/blocked.

### A10 — Toad cannot detect the address-loss failure that motivated the redesign

`state.InterfaceState` contains only name/ifindex/MTU. `toadruntime.inspectInterface` does not capture expected/current addresses. `fromHealth` treats `IfIndex > 0` as `RouteReady`.

Thus a managed TUN can exist with its address removed — the exact legacy suspend symptom — and still be reported route-ready.

AWG/Xray have configured addresses and must detect/repair drift. OpenConnect uses a negotiated address and at minimum must detect disappearance and clear route readiness until the official client/script re-establishes it.

### A11 — NetworkManager ownership exclusion is still a stub

`toad/internal/platform/linux/networkmanager/watcher.go` is:

```go
type Watcher struct{}
func (Watcher) Watch(context.Context, chan<- string) error { return nil }
```

There is no code that makes `kk-*` interfaces unmanaged by NetworkManager and no acceptance gate proving it.

This is a cutover blocker because the legacy incident specifically demonstrated problematic interaction between an external VPN TUN and NetworkManager across resume.

### A12 — underlay observation loses source identity and event semantics

`platform/linux/netlink/snapshot.go` sets `PreferredSrc` only when the default route itself carries `route.Src`. Common Linux default routes omit it; the kernel-selected source then stays empty.

`control.Manager.refreshUnderlay` does not use `netstate.Compare`; every identity difference is reported as `ChangeInterface`.

`watchUnderlay` immediately rescans on every netlink event and only starts periodic auditing if the subscription fails, despite the design requiring settle/coalescing plus a low-frequency consistency scan.

`Snapshot()` itself calls `refreshUnderlay()`, so a read-only API/UI snapshot can trigger kernel observation and recovery side effects.

### A13 — Linux sleep observation can disappear permanently

`platform/linux/logind/sleep.go` shells out to `dbus-monitor`. `Manager.watchSleep` returns permanently if that process exits once.

For a production daemon either use native D-Bus or run a supervised/retrying source. A transient monitor failure must become degraded diagnostics, not permanent loss of resume handling.

### A14 — Leshy publication API ignores the explicit zone

`leshy.RolePublication` contains `Zone`, but `FileBridge.path(role)` writes `<role>.dev`.

Today this happens to work only if role IDs remain literally `primary`/`secondary`. It is incompatible with the N-role model.

Either make publication/withdrawal keyed by zone, or explicitly constrain compatibility mode to role ID == Leshy zone and reject other mappings. Silent mismatch is not acceptable.

### A17 — endpoint normalization is wrong for hostname:port and OpenConnect URLs

There are two related defects in the endpoint-reporting/config path:

- AWG2/Xray `Backend.TransportEndpoints` use `netip.ParseAddrPort(raw)`; on a normal hostname endpoint such as `vpn.example:443`, failure falls back to `Hostname: raw`, so the hostname field incorrectly contains the port and cannot be passed directly to DNS resolution.
- OpenConnect reports the whole gateway string as `Hostname`; a value such as `https://ve.example:4443` is not a hostname.
- `Config.ConfiguredTransportEndpoints` feeds the raw OpenConnect gateway into `endpoint.ParseSpecs`; URL syntax contains `/` and is rejected. The error is swallowed and becomes an empty endpoint set, which later degrades endpoint policy with “provider returned no endpoints”.
- the backend/Toad endpoint DTO currently has no separate hostname port field, so a hostname endpoint cannot be represented losslessly.

This must be fixed before endpoint/routing ownership is accepted. Step 07B now requires one shared endpoint-normalization path and an explicit port in the live endpoint DTO.

### A16 — installed Go core starts with every role desired=false

`kikimora-core.service` only runs `kikimora-core serve`. `control.NewManager` initializes configured roles disabled, and neither service startup nor `kk orchestration cutover --go` calls `ConnectAll`.

Result: the current cutover can report success merely because the daemon is systemd-active while no Toad is running. The same problem returns after reboot: desired connectivity is not persisted/restored.

This is a production cutover blocker, not a UI preference issue. Step 07D must add an explicit persisted product-level desired-state contract and make cutover verify API/role readiness rather than only `systemctl is-active`.

### A15 — IPv4-mapped IPv6 filtering is wrong

`endpoint.Resolve` currently accepts:

```go
if (a.Is4() && !a.Is4In6()) || a.Is6()
```

An IPv4-mapped IPv6 address is `Is6()==true`, so it passes the second branch. This contradicts the progress document.

Use:

```go
if a.Is4In6() {
    continue
}
if a.Is4() || a.Is6() {
    ...
}
```

and add an exact resolver regression.

## Current CI is not green

At the audited HEAD:

- Route parking: green;
- VPN endpoint underlay: green;
- Toad core: red;
- Desktop UI: red;
- ShellCheck: red;
- CLI JSON API: red;
- VPN profiles: red.

Notable failures:

- `linux-xray-lifecycle`: real semantic regression described above;
- Windows Go tests: hand-built TOML does not escape Windows paths; shell provider fixture is POSIX-only; directory `fsync` assumptions fail on Windows;
- macOS Go tests: long `t.TempDir()` Unix socket paths exceed Darwin socket limits;
- Desktop Linux E2E: brittle absolute revision assertion expected 1 and received 2;
- ShellCheck: `orchestration.sh:90` SC2043;
- CLI JSON dispatch and completion regressions: the new orchestration command changed dispatch/help/completion contracts without updating all fixtures.

These are included in step 06A.

## Execution order

Use this order and do not skip forward:

1. `docs/toad-steps/06a-current-head-baseline.md` — restore a trustworthy deterministic baseline and fix the Xray false-online semantic regression.
2. `docs/toad-steps/06-multi-toad-isolated.md` — simultaneous real AWG2 + Xray + OpenConnect Stage 0 gate.
3. `docs/toad-steps/07a-authoritative-state-and-capabilities.md` — capability execution, epoch authority, event loss, real Toad readiness.
4. `docs/toad-steps/07b-routing-parking-failclosed.md` — IPv4/IPv6 fail-closed, route ownership, endpoint reconciliation and parking release.
5. `docs/toad-steps/07c-underlay-resume-networkmanager.md` — canonical source identity, coalescing/audit, TUN drift repair, NetworkManager ownership and sleep observation.
6. `docs/toad-steps/07d-privileged-cutover-acceptance.md` — real namespace/systemd/cutover/suspend acceptance.

If an executor reaches a question explicitly marked **STOP/DESIGN**, it must stop without inventing behavior. Return the evidence to the planner and create a dedicated sub-plan before coding that part.
