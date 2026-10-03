# Kikimora Go VPN orchestration v2 — architecture/executor reference

Status: **partially implemented; direct execution of this monolithic plan is superseded**.

Audit baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79` (2026-09-21).

A large implementation push landed most packages described below before the older Stage 0 simultaneous-protocol prerequisite was closed. The code must now be repaired and accepted from concrete current behavior rather than replaying this document from section 1.

The authoritative execution sequence is:

1. `docs/toad-steps/06a-current-head-baseline.md`;
2. `docs/toad-steps/06-multi-toad-isolated.md`;
3. `docs/toad-steps/07a-authoritative-state-and-capabilities.md`;
4. `docs/toad-steps/07b-routing-parking-failclosed.md`;
5. `docs/toad-steps/07c-underlay-resume-networkmanager.md`;
6. `docs/toad-steps/07d-privileged-cutover-acceptance.md`.

Use `docs/toad-post-push-audit.md` for the code-review findings that caused this split.

The remainder of this document is retained as the architectural intent/behavior ledger. Do not ask an executor to re-investigate or reimplement a section already present in code; use the remediation packets above, which name current files/symbols and required diffs.

The existing Go core, three Toad protocol implementations, local IPC and Qt 6 real backend are the implementation baseline.

The result of this plan is one Go-owned VPN control plane that:

- owns all managed Toad processes and their tunnel lifecycle;
- observes the physical network without a shell/systemd network watchdog;
- keeps every VPN transport on the physical underlay;
- owns endpoint routing table `51890` and its policy rules;
- preserves fail-closed Leshy route parking;
- publishes only validated roles to Leshy;
- recovers each role independently after an underlay change;
- exposes the complete state through the existing local API and Qt 6 UI.

The implementation order in this document is mandatory. In particular, automatic
disruptive recovery must not be enabled before Go owns endpoint safety, parking and
Leshy publication.

---

## 1. Current implementation baseline

The following code is present and must be evolved rather than replaced by a separate
prototype:

```text
toad/cmd/kikimora-core/main.go
    existing daemon CLI and Unix-socket entry point

toad/internal/control/control.go
    existing role map, desired state, process supervision and snapshots

toad/internal/control/api.go
    existing length-prefixed JSON IPC

toad/internal/control/exec.go
    existing kikimora-toad process launcher

toad/cmd/kikimora-toad/main.go
    existing per-Toad runtime

toad/internal/backend/{awg2,xray,openconnect}
    existing protocol implementations

toad/internal/platform/tun*.go
    existing Linux TUN ownership for AWG2

toad/internal/state/state.go
    existing atomic state.json publication

desktop/src/core/RealCoreClient.{h,cpp}
    existing Qt 6 local IPC client

desktop/src/models/RoleListModel.{h,cpp}
    existing QML role projection
```

The current core is a process supervisor, not yet a complete network orchestrator. The
following current behavior must be corrected before route ownership is moved:

1. `control.Manager` combines API, process supervision, state-file polling and product
   state in one type.
2. `ConnectAll` and `DisconnectAll` return on the first error and can leave later roles
   untouched.
3. `watchState` polls every role's `state.json` every 20 milliseconds.
4. `snapshotRoleLocked` reads and parses `state.json` while the manager mutex is held.
5. Toad writes `Generation = 1`; a stale state file can be accepted after process restart.
6. `ExecLauncher.Stop` only sends `SIGINT`; it has no wait deadline, kill fallback or
   process-group handling.
7. A Toad crash is reported, but there is no bounded automatic restart policy.
8. Core cannot ask a running Toad to validate, quiesce, rebind or restart only its
   transport.
9. Core snapshots hard-code `UnderlaySummary = "unknown"` and `LeshySupported = false`.
10. `RealCoreClient::roleAction` ignores the `retry` action.
11. The desktop application silently chooses FakeCore when the real socket is initially
    absent, hiding a failed production daemon.
12. Real snapshots do not populate underlay, endpoint, publication, parking or recovery
    fields already present in the Qt role model.

All later stages assume these defects have been handled as specified below.

---

## 2. Legacy behavior ledger

Every row in this table needs either a Go implementation and a regression test, or an
explicit deletion gate.

| Legacy behavior/problem | Source | v2 disposition |
| --- | --- | --- |
| VPN endpoint must never use another managed VPN | `route-watch`: physical default selection and table `51890` | preserve; add native transport binding |
| Primary and secondary endpoint policy transition independently | priorities `50` and `51` | preserve; generalize priority allocation to N roles |
| Protected endpoint cannot fall through when no physical path exists | unreachable default in table `51890` | preserve |
| Exact endpoint change must not rewrite a live external tunnel path | `*.pending` safe-boundary state | replace manual disconnect with controlled Go recovery |
| A proven live rotating endpoint may be added without deleting old protection | `dynamic-additive` provider mode | replace with Toad-reported live endpoint; keep compatibility adapter temporarily |
| Provider failure must keep previous safe policy | `reconcile_endpoint_role` | preserve |
| Unresolved hostname must not replace policy with a partial address set | `reconcile_endpoint_role` | preserve |
| IPv4-mapped IPv6 answers are not endpoint routes | resolver filter | preserve |
| Managed TUN, loopback and `leshy-dns0` are not physical underlay | `is_managed_interface` | preserve |
| Interface existence is not enough for readiness | readiness stabilization plus later Toad health | replace with layered driver validation |
| Public HTTP URL is not mandatory readiness | removal of the earlier curl probe | preserve |
| Role loss must immediately withdraw Leshy publication | `.dev` removal | preserve |
| Leshy routes must not fall through after TUN loss | route parking | preserve |
| Hard TUN deletion requires cached route ownership | `owned.routes` last observation | preserve |
| Pre-existing/user routes must not be claimed | `before.routes` baseline | preserve initially; later add explicit Leshy owner tag |
| Park must survive until a real replacement route wins | metric `42760` and observation-based release | preserve |
| Endpoint routes must never be parked | separate endpoint table | preserve |
| Leshy failed-add cache needs re-evaluation when a role returns | controlled Leshy restart/cache flush | keep compatibility path; replace with explicit Leshy sync API later |
| Watcher restart must not repeat ready transitions | `active.devices` | replace with durable in-process revision/generation state |
| Profile switch must clean the old role before publishing the replacement | observe/begin/cleanup sequence | preserve as one role transaction |
| Ordinary stop and internal restart have different parking semantics | `restart.pending` | represent as explicit shutdown intent, not marker files |
| Endpoint policy clear while a VPN is active is unsafe | stop/disable preflight | unnecessary for Go-owned Toads; core first quiesces them itself |
| NM connectivity classification is not tunnel truth | documented architecture rule | enforce in snapshot equality tests |
| Resume can stale sockets with an otherwise equal route snapshot | documented architecture rule | forced per-role validation |
| DNS health fallback is separate from VPN recovery | `leshy-health-watch.service` | keep separate until DNS ownership moves to Go |

---

## 3. Non-negotiable invariants

1. Every managed VPN transport uses the selected physical underlay directly.
2. No role uses another managed VPN as transport underlay.
3. One role's failure, retry or recovery never restarts another role.
4. Only `Ready` roles are published to Leshy.
5. User destinations stay fail closed during role loss and disruptive recovery.
6. Endpoint routes stay available through the physical path while user routes are parked.
7. A kernel resource has exactly one writer during every migration stage.
8. Netlink, NetworkManager and resume notifications are invalidations, not restart
   commands.
9. Async completions calculated for an old operation/generation cannot commit new state.
10. Runtime files are reconstructible caches, never proof of current readiness.
11. No mandatory public Internet probe is introduced.
12. Secrets never enter IPC snapshots, logs, diagnostics or process arguments.

---

## 4. Target repository layout

Add the following packages inside the existing `toad` Go module:

```text
toad/internal/core/
    controller.go
    model.go
    events.go
    reconciler.go
    role_executor.go
    recovery.go
    snapshot.go

toad/internal/supervisor/
    supervisor.go
    process_linux.go
    process_unsupported.go
    backoff.go

toad/internal/toadctl/
    protocol.go
    client.go
    server.go
    framing.go

toad/internal/netstate/
    snapshot.go
    identity.go
    compare.go
    coalescer.go

toad/internal/underlay/
    resolver.go
    monitor.go

toad/internal/platform/linux/netlink/
    watcher.go
    snapshot.go
    routes.go
    rules.go

toad/internal/platform/linux/networkmanager/
    watcher.go
    metadata.go

toad/internal/platform/linux/logind/
    sleep.go

toad/internal/routing/
    executor.go
    transaction.go
    fake.go

toad/internal/endpoint/
    manager.go
    resolver.go
    providers.go
    policy.go
    state.go

toad/internal/parking/
    manager.go
    ownership.go
    checkpoint.go

toad/internal/leshy/
    bridge.go
    publication.go
    routes.go
    recovery.go

toad/tests/netns/
    underlay_test.go
    endpoint_test.go
    parking_test.go
    recovery_test.go
```

`internal/control` remains the public local control API. It must not contain kernel
routing policy or protocol-specific recovery decisions.

---

## 5. Shared data contracts

### 5.1 Underlay model

Add `toad/internal/netstate/snapshot.go`:

```go
package netstate

type Path struct {
    Family       int
    IfIndex      int
    Interface    string
    Gateway      netip.Addr
    PreferredSrc netip.Addr
    MTU           int
    Table         int
    Metric        uint32
}

type Metadata struct {
    ConnectionID string
    BSSID        string
}

type Snapshot struct {
    Epoch      uint64
    IPv4       *Path
    IPv6       *Path
    Metadata   Metadata
    ObservedAt time.Time
}

type ChangeReason string

const (
    ChangeInitial          ChangeReason = "initial"
    ChangeInterface        ChangeReason = "default-interface-changed"
    ChangeGateway          ChangeReason = "gateway-changed"
    ChangePreferredSource  ChangeReason = "source-address-changed"
    ChangeAvailability     ChangeReason = "underlay-availability-changed"
    ChangeWiFiIdentity     ChangeReason = "wifi-identity-changed"
    ChangeResumeValidation ChangeReason = "resume-validation"
)
```

`Epoch` changes only for material path identity changes. NetworkManager connectivity
classification is intentionally absent from `Snapshot` identity.

### 5.2 Core role model

Add `toad/internal/core/model.go`:

```go
type RoleState string

const (
    RoleStopped             RoleState = "Stopped"
    RoleWaitingForUnderlay  RoleState = "WaitingForUnderlay"
    RolePreparingEndpoint   RoleState = "PreparingEndpoint"
    RoleStarting            RoleState = "Starting"
    RoleValidating          RoleState = "Validating"
    RoleReady               RoleState = "Ready"
    RoleRecovering          RoleState = "Recovering"
    RoleStopping            RoleState = "Stopping"
    RoleFailed              RoleState = "Failed"
)

type RoleRuntime struct {
    Desired          bool
    State            RoleState
    Reason           string
    Operation        uint64
    ToadGeneration   uint64
    ValidatedEpoch   uint64
    Toad             toadctl.Snapshot
    Endpoint         endpoint.State
    Publication      leshy.PublicationState
    Parking          parking.State
    Recovery         RecoveryState
    LastError        string
}
```

Do not map `Recovering` to generic `Degraded` inside core. The Qt client may choose a
presentation label but must retain the machine state.

### 5.3 Toad control protocol

Add `toad/internal/toadctl/protocol.go` with a separate version from the desktop/core API:

```go
const ProtocolVersion = 1

type StartRequest struct {
    Generation uint64
    Underlay   UnderlayBinding
}

type UnderlayBinding struct {
    IPv4 *PathBinding
    IPv6 *PathBinding
}

type PathBinding struct {
    IfIndex   int
    Interface string
    Source    netip.Addr
}

type Capabilities struct {
    Validate                    bool
    Rebind                      bool
    RestartTransportKeepingTUN bool
    ReportsLiveEndpoints       bool
}

type TransportEndpoint struct {
    Network    string
    Address    netip.AddrPort
    Hostname   string
    ObservedAt time.Time
    Active     bool
}
```

Required methods:

```text
Handshake
Start
Quiesce
Stop
Inspect
Validate
Rebind
RestartTransport
Subscribe
```

The Toad control socket is per process and placed inside its configured state directory.
It must be deleted and recreated for every process instance.

### 5.4 Desktop/core snapshot schema v2

Keep the current outer framing. Add protocol negotiation to `Handshake` and make snapshot
schema additive:

```json
{
  "schema": 2,
  "revision": 84,
  "core_state": "Ready",
  "aggregate_state": "Recovering",
  "active_profile": "home",
  "underlay": {
    "epoch": 17,
    "available": true,
    "ipv4": {
      "interface": "wlan0",
      "ifindex": 3,
      "gateway": "192.168.1.1",
      "preferred_source": "192.168.1.52",
      "mtu": 1500,
      "table": 254,
      "metric": 600
    },
    "last_change_reason": "source-address-changed",
    "last_change_at": "..."
  },
  "roles": [{
    "id": "primary",
    "desired_state": "enabled",
    "state": "Recovering",
    "reason": "transport-health-failed",
    "operation": 31,
    "validated_underlay_epoch": 16,
    "endpoint": {
      "state": "ready",
      "applied_underlay_epoch": 17,
      "configured": ["vpn.example:443"],
      "resolved": ["203.0.113.5:443"],
      "live": ["203.0.113.5:443"],
      "last_error": ""
    },
    "publication": {"published": false, "zone": "primary"},
    "parking": {"active": true, "count": 8},
    "recovery": {
      "action": "restart-transport",
      "reason": "source-address-changed",
      "attempt": 1,
      "next_retry_at": null
    }
  }]
}
```

---

## 6. Stage N0 — harden the existing core and supervisor

No kernel route ownership changes occur in this stage.

### 6.1 Split `control.Manager`

Modify:

```text
toad/internal/control/control.go
toad/internal/control/api.go
toad/internal/control/control_test.go
```

Add:

```text
toad/internal/core/controller.go
toad/internal/core/snapshot.go
toad/internal/supervisor/supervisor.go
```

Pseudo-diff:

```diff
-type Manager struct {
-    mu       sync.Mutex
-    launcher Launcher
-    roles    map[string]*role
-    ...
-}
+type Manager struct {
+    controller *core.Controller
+}

-func (m *Manager) ConnectRole(ctx context.Context, name string) error
+func (m *Manager) ConnectRole(ctx context.Context, name string) error {
+    return m.controller.SetRoleDesired(ctx, name, true)
+}
```

`control.Manager` may remain as a compatibility name during this refactor. Its mutex must
not protect filesystem I/O, JSON decoding or process waits.

### 6.2 Correct aggregate operations

```diff
-for _, name := range names {
-    if err := m.ConnectRole(ctx, name); err != nil {
-        return err
-    }
-}
+var errs []error
+for _, name := range names {
+    if err := controller.SetRoleDesired(ctx, name, true); err != nil {
+        errs = append(errs, fmt.Errorf("%s: %w", name, err))
+    }
+}
+return errors.Join(errs...)
```

The desired state change is committed for all roles before any individual start result is
reported. The API response always contains the resulting per-role snapshot.

### 6.3 Replace state-file polling

Delete `Manager.watchState` after the Toad control subscription from N1 is available.
During the short N0/N1 transition, replace the 20 ms interval with one filesystem watcher
or a minimum 250 ms compatibility poll outside the manager lock.

State parsing rules:

1. read the complete file outside the controller mutex;
2. require the expected role name;
3. require the expected process instance ID;
4. require the expected generation;
5. compare the parsed semantic snapshot with the stored snapshot;
6. bump core revision only if content changed.

### 6.4 Harden process lifecycle

Move `ExecLauncher` into `internal/supervisor` and change `Process`:

```go
type Process interface {
    PID() int
    Wait() error
    Signal(os.Signal) error
    Kill() error
}
```

Linux launch requirements:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setpgid:  true,
    Pdeathsig: syscall.SIGTERM,
}
```

Stop sequence:

```text
mark operation Stopping
→ SIGINT process group
→ wait up to configured grace period
→ SIGTERM process group
→ wait short final grace period
→ SIGKILL process group
→ Wait exactly once
```

Do not use an IPC request context as the lifetime context of a child process. A client
disconnect must not kill its Toad.

### 6.5 Restart policy

Add `internal/supervisor/backoff.go`:

```text
delays: 1s, 2s, 5s, 10s, 30s, 60s
jitter: ±20%
reset: role continuously Ready for 60s
pause: no physical underlay
scope: per role
```

An explicit user disconnect cancels pending retries. A manual retry clears the current
delay but does not reset the crash counter until the role is stable.

### 6.6 N0 tests

Add to `control_test.go` or move into `core/controller_test.go`:

```text
TestConnectAllContinuesAfterOneStartFailure
TestDisconnectAllStopsEveryRoleAfterOneStopFailure
TestStaleStateGenerationIsIgnored
TestStateReadDoesNotHoldControllerMutex
TestUnchangedStateDoesNotBumpRevision
TestRoleCrashDoesNotAffectOtherRoles
TestExplicitDisconnectCancelsRetry
TestIPCContextCancellationDoesNotKillToad
```

Exit gate:

- process lifecycle is bounded and leak-free;
- aggregate commands affect every role;
- stale runtime state cannot produce `Ready`;
- no 20 ms per-role steady-state polling remains after N1.

---

## 7. Stage N1 — make each Toad controllable while running

No automatic underlay recovery is enabled in this stage.

### 7.1 Refactor `kikimora-toad run`

Current `runCommand` performs one linear start and owns local variables for the backend and
tunnel. Replace it with a long-lived runtime object.

Add `toad/internal/toadruntime/runtime.go`:

```go
type Runtime struct {
    mu         sync.Mutex
    cfg        *config.Config
    generation uint64
    tunnel     platform.Tunnel
    backend    backend.Backend
    state      state.Snapshot
    changed    chan struct{}
}

func (r *Runtime) Start(ctx context.Context, req toadctl.StartRequest) error
func (r *Runtime) Quiesce(ctx context.Context) error
func (r *Runtime) Stop(ctx context.Context) error
func (r *Runtime) Validate(ctx context.Context) toadctl.ValidationResult
func (r *Runtime) Rebind(ctx context.Context, binding toadctl.UnderlayBinding) error
func (r *Runtime) RestartTransport(ctx context.Context, binding toadctl.UnderlayBinding) error
func (r *Runtime) Snapshot() toadctl.Snapshot
```

Pseudo-diff in `cmd/kikimora-toad/main.go`:

```diff
-create backend
-backend.Start(ctx)
-waitForManagedInterface(...)
-write state.json every 250ms
+runtime := toadruntime.New(cfg)
+server := toadctl.NewServer(controlSocket, runtime)
+go runtime.RunHealthLoop(ctx)
+return server.Serve(ctx)
```

The health loop updates state only when a semantic field changes or when a low-frequency
diagnostic checkpoint is due. It must not generate a desktop/core revision every 250 ms
when only `UpdatedAt` changed.

### 7.2 Extend backend capabilities without fake implementations

Keep the required minimal interface:

```go
type Backend interface {
    Start(context.Context) error
    Health(context.Context) Health
    Close() error
}
```

Add optional interfaces in `internal/backend/backend.go`:

```go
type Validator interface {
    Validate(context.Context) Validation
}

type EndpointReporter interface {
    TransportEndpoints(context.Context) ([]TransportEndpoint, error)
}

type Rebindable interface {
    Rebind(context.Context, UnderlayBinding) error
}

type TransportRestarter interface {
    RestartTransport(context.Context, UnderlayBinding) error
}
```

Unsupported capabilities are absent, not implemented as success no-ops.

### 7.3 AWG2 implementation

Modify:

```text
toad/internal/backend/awg2/backend.go
toad/internal/backend/awg2/health.go
toad/cmd/kikimora-toad/main.go
```

Requirements:

- the runtime owns the original TUN FD for the full process lifetime;
- closing/recreating the AWG device uses duplicated FDs and does not close the owner FD;
- `Validate` requires a recent handshake using the existing 30-second window;
- the UAPI-reported endpoint is returned through `EndpointReporter`;
- `RestartTransport` closes and recreates the AWG device while retaining TUN name and
  ifindex;
- tests assert identical TUN ifindex before and after transport restart.

### 7.4 Xray/VLESS implementation

Modify:

```text
toad/internal/backend/xray/backend.go
toad/internal/backend/xray/config.go
```

Requirements:

- expose the configured and active VLESS remote endpoint;
- add an internal Xray session/outbound event or statistics hook;
- distinguish `core running`, `TUN ready` and `outbound session proven`;
- initial `Ready` requires a protocol-native outbound proof, not a public URL;
- if no session is currently active after an underlay epoch change, return `unknown` so
  core selects controlled recovery;
- initial capability is `RestartTransportKeepingTUN=false` because Xray currently owns
  its TUN inside the Xray instance.

Do not declare stable-TUN support until Xray can consume a Toad-owned TUN or another tested
mechanism preserves ifindex across restart.

### 7.5 OpenConnect implementation

Modify:

```text
toad/internal/backend/openconnect/backend.go
toad/internal/backend/openconnect/script_linux.go
```

Requirements:

- expose configured gateway and actual connected peer;
- distinguish process alive, authentication, CSTP/data phase and TUN ready;
- `Validate` requires the data phase plus live managed interface;
- retain current secret redaction;
- initial capability is `RestartTransportKeepingTUN=false`;
- quiesce and stop use the existing bounded OpenConnect close path.

### 7.6 N1 tests

```text
toad/internal/toadctl/protocol_test.go
toad/internal/toadctl/server_test.go
toad/internal/toadruntime/runtime_test.go
toad/internal/backend/awg2/restart_test.go
toad/internal/backend/xray/validation_test.go
toad/internal/backend/openconnect/validation_test.go
```

Required cases:

- generation mismatch rejected;
- duplicate Start is idempotent;
- Quiesce followed by Start works;
- unsupported Rebind returns a typed capability error;
- subscription delivers strictly increasing semantic revisions;
- AWG2 transport restart preserves TUN identity;
- Xray/OpenConnect report full-restart capability honestly;
- state and logs contain no private key, password or token.

---

## 8. Stage N2 — canonical physical-underlay monitor in shadow mode

### 8.1 Linux netlink watcher

Use the existing `vishvananda/netlink` dependency. Subscribe to:

```text
RTM_NEWLINK / RTM_DELLINK
RTM_NEWADDR / RTM_DELADDR
RTM_NEWROUTE / RTM_DELROUTE
```

Callbacks write only a small invalidation value into a bounded channel. If the channel is
already full, keep one pending invalidation instead of blocking the netlink receiver.

### 8.2 Snapshot builder

Build IPv4 and IPv6 paths independently.

Initial compatibility semantics:

1. inspect normal main-table default routes;
2. reject loopback;
3. reject `leshy-dns0`;
4. reject every interface registered by a managed role;
5. reject known Kikimora virtual plumbing;
6. select the lowest effective metric path;
7. capture preferred source from the selected route/interface;
8. capture ifindex, gateway, MTU, table and metric.

Deterministic tie-break for equal metrics:

```text
lowest route priority
→ lowest ifindex
→ lexical interface name
```

Do not base identity on interface names alone.

### 8.3 Coalescing

Default values:

```text
settle delay: 150 ms
maximum burst delay: 1 s
consistency audit: 60 s
```

All values are injectable in tests. The consistency audit only rebuilds and compares the
snapshot. It never directly requests reconnect.

### 8.4 NetworkManager enrichment

Add optional DBus integration for:

```text
active connection UUID
Wi-Fi BSSID
NetworkManager owner restart
```

NetworkManager absence is supported. Global connectivity classification is logged only as
metadata and is excluded from identity comparison.

### 8.5 Suspend/resume

Subscribe to logind `PrepareForSleep`:

```text
PrepareForSleep(true)  → record suspended state
PrepareForSleep(false) → force full snapshot + ResumeValidation event
```

Resume validation is emitted even when the rebuilt snapshot is equal.

### 8.6 Shadow comparison output

Expose through diagnostics:

```text
current snapshot
current epoch
last invalidation source
last comparison result
last material change reason
proposed per-role action
```

No route or Toad mutation is allowed in shadow mode.

### 8.7 N2 tests

```text
TestConnectivityClassificationDoesNotChangeEpoch
TestManagedTunnelDoesNotBecomeUnderlay
TestUnrelatedVethDoesNotChangeEpoch
TestGatewayChangeAdvancesEpoch
TestPreferredSourceChangeAdvancesEpoch
TestSelectedInterfaceChangeAdvancesEpoch
TestRouteMetricChangeWithoutWinnerChangeIsNoop
TestResumeForcesValidationWithoutEpochChange
TestBurstIsCoalesced
TestSustainedBurstHonorsMaximumDelay
```

Exit gate: shadow output correctly explains old/new path on real Wi-Fi, Ethernet, DHCP,
Docker and resume scenarios without touching a Toad or route.

---

## 9. Stage N3 — generation-safe reconciler

### 9.1 Event model

Add `internal/core/events.go`:

```go
type Event interface{ isCoreEvent() }

type UnderlayInvalidated struct{ Source string }
type UnderlayChanged struct {
    Old, New netstate.Snapshot
    Reason   netstate.ChangeReason
}
type ResumeValidation struct{}
type DesiredRoleChanged struct{ Role string }
type ToadStateChanged struct{ Role string; Generation uint64 }
type OperationCompleted struct {
    Role       string
    Operation  uint64
    Epoch      uint64
    Result     OperationResult
}
type RouteStateChanged struct{}
type RetryDue struct{ Role string; Operation uint64 }
```

### 9.2 Controller loop

Only the controller loop commits role state:

```go
func (c *Controller) Run(ctx context.Context) error {
    for {
        select {
        case event := <-c.events:
            c.applyEvent(event)
            c.reconcile()
        case <-ctx.Done():
            return c.shutdown(ctx)
        }
    }
}
```

Blocking work is submitted to a per-role executor or serialized routing executor. An
executor never mutates `RoleRuntime` directly.

### 9.3 Operation identity

Before scheduling role work:

```go
role.Operation++
op := role.Operation
epoch := controller.Underlay.Epoch
executor.Submit(role.ID, op, epoch, work)
```

On completion:

```go
if completion.Operation != role.Operation {
    recordStaleCompletion(completion)
    requestReconcile(role.ID)
    return
}
if completion.Epoch != controller.Underlay.Epoch {
    requestReconcile(role.ID)
    return
}
```

### 9.4 Routing executor

All route and rule mutations are serialized:

```go
type Executor interface {
    Apply(context.Context, Transaction) error
    Snapshot(context.Context) (KernelState, error)
}

type Transaction struct {
    ID         uint64
    Role       string
    Operations []Operation
}
```

Each operation is declarative and idempotent: replace route, delete exact route, replace
rule, delete exact rule, publish file, withdraw file.

### 9.5 N3 tests

```text
TestStaleStartCompletionCannotPublishRole
TestStaleRecoveryCannotRestoreOldUnderlay
TestRepeatedInvalidationsProduceOneDesiredState
TestRoutingTransactionsAreSerialized
TestRoleExecutorsRunIndependently
TestShutdownDrainsOrCancelsOperations
```

---

## 10. Stage N4 — endpoint-underlay ownership in Go

This is the first kernel-mutation cutover.

### 10.1 Configuration

Extend `internal/config.Config` additively:

```go
type Config struct {
    ...
    LeshyZone      string         `toml:"leshy_zone"`
    EndpointPolicy EndpointPolicy `toml:"endpoint_policy"`
}

type EndpointPolicy struct {
    Source       string `toml:"source"` // native, static, command-compat
    StaticFile   string `toml:"static_file"`
    Command      string `toml:"command"`
    RulePriority int    `toml:"rule_priority"`
}
```

Defaults:

```text
primary   → zone primary, priority 50
secondary → zone secondary, priority 51
other     → explicit zone and priority required
```

Reject duplicate priorities. Do not automatically invent Leshy zones for additional
roles.

### 10.2 Native endpoint extraction

Add protocol-neutral helpers to config:

```go
func (c *Config) ConfiguredTransportEndpoints() []EndpointSpec
```

Mapping:

```text
AWG2.Endpoint
VLESS.Endpoint
OpenConnect.Gateway
```

Hostname resolution returns a complete candidate set per family. If a configured hostname
fails to resolve, retain the last-known-good applied set and report degraded/pending state.
Never replace it with a partially resolved set from the same desired generation.

### 10.3 Linux policy shape

For each family:

```text
ip route replace unreachable default table 51890 metric 32767
ip route replace ENDPOINT/PREFIX via GATEWAY dev UNDERLAY onlink table 51890 metric 1
ip rule replace priority ROLE_PRIORITY to ENDPOINT/PREFIX lookup 51890
```

Use netlink APIs, not `exec ip`. Verification reads back exact rules/routes and performs a
route lookup for every protected endpoint.

### 10.4 Transport binding

Endpoint policy and Toad transport binding are both required:

```text
kernel endpoint route protects system route selection
Toad UnderlayBinding constrains protocol transport implementation
Toad reports actual live endpoint and bound ifindex/source
core verifies the report against the current underlay epoch
```

A Toad cannot become `Ready` if it reports a live endpoint on a managed VPN interface or a
different physical path.

### 10.5 Safe live changes

For a configured/physical path change:

```text
if role is stopped:
    apply exact desired policy

if only new Toad-proven endpoints are added on the same underlay:
    add rules/routes
    retain old endpoints

otherwise:
    request controlled recovery
    do not mutate destructive policy until Quiesce completed
```

There is no user-visible requirement to disconnect manually. `endpoint.state=pending`
means that the core has scheduled or is retrying the safe transition.

### 10.6 Compatibility providers

Port `static` and `command` providers as compatibility adapters. Provider output is parsed
inside a sandboxed, bounded command execution with:

```text
timeout
maximum output size
validated hostname/IP grammar
no shell interpolation
environment allowlist
```

Do not port Happ's process/socket inference as the final implementation. Retain it only
while an external Happ-owned transport still exists. Delete it after the corresponding
role is migrated to a native Toad that reports its endpoints.

### 10.7 Single-writer cutover

Add one installation-level owner setting:

```text
endpoint_owner = legacy | go
```

Do not add independent flags per role. Cutover sequence:

```text
stop leshy-route-watch
→ run Go endpoint preflight in read-only mode
→ Go adopts/verifies existing exact policy
→ set endpoint_owner=go
→ start core mutation mode
→ never restart route-watch as endpoint writer
```

Rollback requires stopping Go mutation mode before starting the legacy writer.

### 10.8 N4 tests

Port the assertions from:

```text
linux/tests/endpoint-underlay.sh
linux/tests/endpoint-underlay-netns.sh
linux/tests/endpoint-underlay-service.sh
linux/tests/endpoint-underlay-signature-adoption.sh
linux/tests/endpoint-provider-dynamic.sh
linux/tests/endpoint-providers.sh
linux/tests/happ-transport-owner.sh
```

Add:

```text
TestNativeEndpointInstalledBeforeToadStart
TestLiveReportedEndpointMatchesPhysicalBinding
TestDestructiveChangeWaitsForQuiesce
TestPendingTransitionNeedsNoManualDisconnect
TestProviderFailureKeepsLastKnownGoodPolicy
TestNoUnderlayKeepsEndpointFailClosed
TestTwoRolesDoNotRewriteEachOthersRules
TestMappedIPv6AnswerIsIgnored
```

Exit gate: shell performs no write to table `51890`; all endpoint tests pass against Go.

---

## 11. Stage N5 — Go parking and Leshy publication

### 11.1 Route observation

The parking manager subscribes to route changes and periodically audits kernel state. It
tracks routes for currently published role interfaces.

Compatibility owner predicate:

```text
family = IPv4
prefix length = 32
protocol = static
output interface = published role interface
route absent from role baseline
```

Store prefixes internally as `netip.Prefix` and family-neutral records:

```go
type OwnedRoute struct {
    Role      string
    Interface string
    IfIndex   int
    Prefix    netip.Prefix
}
```

The design must permit a later owner predicate based on a dedicated Leshy route protocol,
mark or netlink cookie without changing the parking transaction API.

### 11.2 Baseline and last observation

Create a baseline when a role begins a new publication lifecycle. Update owned routes on
every relevant route event and audit.

Persist an atomic runtime checkpoint under:

```text
/run/kikimora/parking/state.json
```

Checkpoint fields:

```text
boot ID
core instance ID
role
tunnel ifindex/name
baseline prefixes
last observed owned prefixes
parked prefixes
updated time
```

The checkpoint is used after core restart within the same boot, but is verified against
kernel state. It is not readiness proof.

### 11.3 Parking operations

Initial route shape remains exactly compatible:

```text
unreachable PREFIX proto static metric 42760
```

`PrepareWithdrawal(role)`:

```text
refresh current owned routes if interface exists
→ merge with verified last observation
→ remove exact live role routes where still present
→ replace unreachable parks
→ verify every owned destination resolves to the park or a stricter fail-closed route
```

`ObserveRestoration(role)`:

```text
observe real Leshy route for PREFIX
→ verify it wins over metric 42760 park
→ delete exact park
→ delete checkpoint record
```

Never release every park because the tunnel interface merely exists.

### 11.4 Publication bridge

The first Go bridge keeps the current Leshy contract:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

Publication operations use same-directory temporary file, `fsync`, `rename` and directory
`fsync`. The desired publication set is derived only from `RoleReady` states.

```go
type Bridge interface {
    Publish(context.Context, RolePublication) error
    Withdraw(context.Context, string) error
    Resync(context.Context, string) error
}
```

Additional roles without an explicitly supported Leshy zone stay unpublished and expose a
configuration error.

### 11.5 Controlled withdrawal transaction

The reconciler must execute this exact order:

```text
role State = Recovering/Stopping
→ parking.PrepareWithdrawal(role)
→ leshy.Withdraw(role)
→ verify publication absent
→ verify parks present
→ allow Toad Quiesce/Stop
```

Failure before Toad disruption leaves the existing Toad running. Failure after disruption
keeps the role unpublished and parks installed.

### 11.6 Leshy route-state compatibility

Until Leshy exposes an explicit resync API:

```text
publish role
→ mark internal Leshy refresh intent in Go memory/checkpoint
→ preserve parks
→ controlled restart of leshy.service
→ flush systemd-resolved cache
→ observe recreated real routes
→ release matching parks
```

Do not use a standalone periodic watcher for this action.

Target Leshy API:

```text
SyncRole(role, interface)
ResyncRoutes(role)
RouteApplied(role, prefix)
```

After that API exists, delete the controlled Leshy restart path and its checkpoint field.

### 11.7 Stop intent semantics

Model shutdown intent explicitly:

```go
type ShutdownIntent string

const (
    ShutdownRole       ShutdownIntent = "role-disconnect"
    ShutdownProfile    ShutdownIntent = "profile-switch"
    ShutdownCoreRestart ShutdownIntent = "core-restart"
    ShutdownProduct    ShutdownIntent = "product-stop"
)
```

Rules:

```text
role disconnect  → withdraw and keep parks
profile switch   → keep parks until replacement routes appear
core restart     → preserve parks and reconstruct
product stop     → stop Leshy/DNS integration, cleanup owned routes, clear parks
```

### 11.8 N5 tests

Port assertions from:

```text
linux/tests/route-parking-netns.sh
linux/tests/route-parking-service.sh
linux/tests/route-watch-readiness.sh
```

Add:

```text
TestBaselineRouteIsNeverParked
TestExpiredHealthyRouteIsNotResurrected
TestHardTunnelDeletionUsesLastObservation
TestPrepareWithdrawalParksBeforeUnpublish
TestParkReleasedOnlyAfterWinningRouteObserved
TestCoreRestartReconstructsParkedState
TestProductStopClearsParking
TestProfileSwitchKeepsParkingUntilReplacement
TestEndpointDestinationsAreNeverParkingCandidates
```

Exit gate: Go is the sole writer of parking routes and `.dev` publication files.

---

## 12. Stage N6 — activate controlled recovery

### 12.1 Decision order

For each role independently:

```text
1. desired disabled
   → Stopped

2. no physical path for required endpoint family
   → controlled withdrawal if previously published
   → WaitingForUnderlay
   → no restart timer

3. current Toad generation absent/dead
   → controlled withdrawal
   → bounded process restart

4. role not validated for current epoch or resume requested
   → Validate

5. validation healthy and endpoint binding matches
   → set ValidatedEpoch=current
   → Ready

6. safe Rebind capability available
   → Rebind
   → Validate

7. stable-TUN transport restart available
   → controlled withdrawal
   → RestartTransport
   → Validate
   → Publish

8. fallback
   → controlled withdrawal
   → full Toad restart
   → Validate
   → Publish
```

### 12.2 Recovery transaction

Implement in `internal/core/recovery.go` as explicit steps, not one large function:

```go
type RecoveryStep string

const (
    RecoveryObserveRoutes    RecoveryStep = "observe-routes"
    RecoveryPark             RecoveryStep = "park"
    RecoveryWithdraw         RecoveryStep = "withdraw"
    RecoveryQuiesce          RecoveryStep = "quiesce"
    RecoveryApplyEndpoint    RecoveryStep = "apply-endpoint"
    RecoveryStartTransport   RecoveryStep = "start-transport"
    RecoveryValidate         RecoveryStep = "validate"
    RecoveryPublish          RecoveryStep = "publish"
    RecoveryResyncLeshy      RecoveryStep = "resync-leshy"
    RecoveryObserveRestore   RecoveryStep = "observe-restoration"
)
```

Store current step, operation ID, epoch, attempt and last error in `RoleRuntime.Recovery`.
Every step must be retryable or reconstructible after core restart.

### 12.3 Protocol capability matrix

Initial required matrix:

| Protocol | Rebind | Restart transport with stable TUN | Full restart |
| --- | --- | --- | --- |
| AWG2 | optional after bind implementation | required | supported |
| Xray/VLESS | no | no until Toad-owned TUN exists | required |
| OpenConnect | no | no | required |

Do not implement core special cases based on protocol strings. The recovery selector uses
reported capabilities only.

### 12.4 Hard physical loss

On underlay loss:

```text
invalidate every role's validated epoch
→ park and withdraw Ready roles
→ retain endpoint unreachable protection
→ transition enabled roles to WaitingForUnderlay
→ cancel active retry timers
→ do not repeatedly stop/start Toads
```

Whether a quiesced Toad process remains alive is a driver resource decision. It does not
change publication or parking state.

### 12.5 Resume

Resume creates validation work for every Ready role even if the underlay epoch is equal.
A healthy transport stays Ready. A stale or unknown result uses the normal recovery
selector.

### 12.6 N6 tests

```text
TestConnectivityOnlyEventCausesNoValidationOrRestart
TestVethChurnCausesNoValidationOrRestart
TestWiFiToEthernetRecoversOnlyStaleRoles
TestSourceAddressChangeAdvancesEpoch
TestGatewayChangeAdvancesEpoch
TestResumeKeepsHealthyRole
TestResumeRecoversStaleRole
TestOneStaleRoleDoesNotWithdrawHealthyRole
TestFailedRecoveryRemainsParkedAndUnpublished
TestEventBurstMakesOldCompletionStale
TestNoUnderlayDoesNotSpinRetries
TestAWG2RecoveryPreservesTunnelIfIndex
TestXrayRecoveryUsesFullRestartAndParking
TestOpenConnectRecoveryUsesFullRestartAndParking
```

---

## 13. Stage N7 — desktop/core API and Qt 6 completion

### 13.1 Core API

Modify:

```text
toad/internal/control/api.go
toad/internal/control/control.go
toad/cmd/kikimora-core/main.go
```

Handshake response:

```json
{
  "version": 2,
  "min_version": 1,
  "max_version": 2,
  "capabilities": [
    "GetSnapshot", "Subscribe", "ConnectAll", "DisconnectAll",
    "ConnectRole", "DisconnectRole", "RetryRole",
    "SetActiveProfile", "RediscoverEndpoints",
    "ValidateRole", "GetDiagnosticsSnapshot"
  ]
}
```

Add command IDs and structured command errors:

```go
type APIError struct {
    Code      string `json:"code"`
    Message   string `json:"message"`
    Role      string `json:"role,omitempty"`
    Retryable bool   `json:"retryable"`
}
```

Commands change desired state and return immediately. Progress arrives through revisioned
snapshots; an API request must not wait for a real VPN handshake.

### 13.2 Snapshot projection

`core.Controller` owns the product snapshot. `control` only serializes it. Derive:

```text
CoreState:
  Starting, Ready, Degraded, ShuttingDown

AggregateState:
  Stopped
  Connecting
  Ready
  PartiallyReady
  WaitingForUnderlay
  Recovering
  Degraded
  Failed
```

Do not collapse `WaitingForUnderlay` into `Connecting` or `Recovering` into `Degraded`.

### 13.3 `CoreBackend`

Modify `desktop/src/core/CoreBackend.h`:

```diff
 Q_PROPERTY(QString underlaySummary READ underlaySummary NOTIFY snapshotChanged)
+Q_PROPERTY(QVariantMap underlay READ underlay NOTIFY snapshotChanged)
+Q_PROPERTY(QString lastCommandError READ lastCommandError NOTIFY snapshotChanged)
-Q_PROPERTY(bool leshySupported READ leshySupported CONSTANT)
+Q_PROPERTY(bool leshySupported READ leshySupported NOTIFY snapshotChanged)

+Q_INVOKABLE virtual void retryRole(int row) = 0;
+Q_INVOKABLE virtual void validateRole(int row) = 0;
+Q_INVOKABLE virtual void rediscoverEndpoints(int row) = 0;
```

Update FakeCore with the same state schema and explicit recovery scenarios. FakeCore
remains test/demo-only.

### 13.4 `RoleListModel`

Extend `RoleSnapshot` and role names:

```diff
+bool desiredEnabled;
+qulonglong operation;
+qulonglong validatedUnderlayEpoch;
+QString endpointState;
+QStringList configuredEndpoints;
+QStringList liveEndpoints;
+bool leshyPublished;
+QString leshyZone;
+bool parkingActive;
+int parkedRoutes;
+QString recoveryAction;
+QString recoveryReason;
+int recoveryAttempt;
+QString nextRetryAt;
+QString validationState;
+QString validationReason;
```

Keep raw machine state separately from localized/presentation text.

### 13.5 `RealCoreClient`

Modify `RealCoreClient.cpp`:

```diff
-displayState maps recovering to a generic presentation state before storage
+store exact core state in RoleSnapshot.state
+derive localized stateText separately

-roleAction chooses disconnect else connect
+roleAction follows available_actions in priority:
+  retry, disconnect, connect

-any failed response sets whole core state to Error
+store structured request error in lastCommandError
+leave subscribed core state authoritative
```

Reconnect state machine:

```text
Disconnected
→ Connecting
→ Handshaking
→ Synchronizing
→ Subscribed
```

Use backoff `0.5s, 1s, 2s, 5s, 10s`; reset after successful subscription. After reconnect,
always perform Handshake, GetSnapshot and Subscribe. A revision gap requests one resync and
does not discard the eventual resync snapshot merely because another command response was
seen first.

### 13.6 Production backend selection

Modify `desktop/src/main.cpp`:

```diff
-useFake = useFake || !RealCoreClient::isSocketAvailable(socketPath)
+useFake = parser.isSet(fakeOption)
+// Production always creates RealCoreClient; it displays Unavailable and reconnects.
```

Windows may retain an explicit platform policy, but Linux/macOS must not silently display
demo data when the daemon is down.

### 13.7 QML

Modify:

```text
desktop/qml/Main.qml
desktop/qml/Controls/ConnectCircle.qml
desktop/qml/Controls/RoleRow.qml
desktop/qml/Drawers/RoleDrawer.qml
```

Required UI behavior:

- global circle shows partial, waiting and recovering states;
- role row distinguishes desired-off, waiting, connecting, ready, recovering and failed;
- role drawer shows physical path, endpoint state, publication, parks, validation epoch,
  recovery action and next retry;
- failed role shows Retry only when `available_actions` contains it;
- Routing page shows table `51890` ownership and parked destination count;
- daemon unavailable is an explicit production error, never FakeCore data;
- UI never decides whether a role should recover.

### 13.8 N7 tests

Extend:

```text
desktop/tests/RealCoreClientTest.cpp
desktop/tests/RealCoreEndToEndTest.cpp
desktop/tests/RealCoreUiTest.cpp
desktop/tests/UiBehaviorTest.cpp
```

Add:

```text
snapshot v2 mapping
unknown additive fields ignored
WaitingForUnderlay rendering
Recovering rendering
retry action dispatch
command error does not destroy subscribed state
revision-gap resync
daemon restart and reconnect
production does not silently fall back to FakeCore
```

---

## 14. Stage N8 — system service and legacy retirement

### 14.1 Service files

Add:

```text
linux/files/kikimora-core.service
linux/files/kikimora-core.tmpfiles.conf
linux/files/kikimora-core.sysusers.conf
```

Target unit:

```ini
[Unit]
Description=Kikimora VPN control plane
Before=leshy.service
Wants=leshy.service
After=network-pre.target

[Service]
Type=notify
ExecStart=/usr/local/bin/kikimora-core serve --config-dir /etc/kikimora/toads
Restart=on-failure
RestartSec=2s
RuntimeDirectory=kikimora
RuntimeDirectoryMode=0750
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
```

If `Type=notify` is not implemented in the same change, use `Type=simple`; do not declare
notify readiness without sending it.

### 14.2 Local socket access

Target socket:

```text
/run/kikimora/core.sock
owner root
group kikimora
mode 0660
```

Validate Linux peer credentials with `SO_PEERCRED`. Only high-level API methods are
accepted. No arbitrary command, config path or route operation is exposed.

### 14.3 Atomic ownership cutover

Use two monotonic installation states:

```text
routing_owner = legacy | go
tunnel_owner  = external | go
```

Supported combinations during migration:

```text
legacy + external
go + external       // endpoint/parking parity stage only
go + go             // final
```

Reject `legacy + go`: Go-owned Toad recovery must not run without Go-owned safety
barriers.

### 14.4 Delete legacy components only after parity

Delete after the listed Go owner passes its gate:

| Legacy component | Delete after |
| --- | --- |
| `linux/files/reconcile` | Toad validation and Go publication are active |
| `linux/files/route-watch` | endpoint, parking, publication and recovery owners are Go |
| `linux/files/route-lifecycle` | Go parking passes hard-disappearance and restart tests |
| `linux/files/leshy-route-watch.service` | all route-watch responsibilities have Go owners |
| `linux/files/route-cleanup.conf` | Go shutdown/parking lifecycle is installed |
| readiness counter files | Toad validation is authoritative |
| `active.devices` | controller revision/generation is authoritative |
| endpoint `*.pending` files | controlled recovery is authoritative |
| `restart.pending` | shutdown intent/checkpoint is authoritative |
| Happ socket inference | all affected transports are native Toads |

Keep `leshy-health-watch.service` until DNS fallback ownership is separately migrated. It
must not gain any tunnel or underlay responsibility.

---

## 15. Stage N9 — platform completion

Linux is the first production target for the complete orchestration stack.

All platform-specific code stays behind these interfaces:

```go
type UnderlaySource interface {
    Watch(context.Context, chan<- Invalidation) error
    Snapshot(context.Context, ExclusionSet) (netstate.Snapshot, error)
}

type RouteManager interface {
    ApplyEndpointPolicy(context.Context, endpoint.Policy) error
    ApplyParking(context.Context, parking.DesiredState) error
    Snapshot(context.Context) (routing.KernelState, error)
}

type SleepSource interface {
    Watch(context.Context, chan<- SleepEvent) error
}
```

macOS implementation follows with routing socket/SystemConfiguration/launchd adapters.
Until those adapters and fail-closed routing pass parity, the API reports orchestration as
unsupported instead of simulating Linux behavior. Windows remains outside the complete
stack until it has equivalent underlay, routing and service implementations.

---

## 16. Required end-to-end acceptance matrix

Every scenario must verify core snapshot, kernel routes/rules, Toad identity and packet
path where applicable.

1. Cold boot with no physical underlay.
2. Physical underlay appears after core start.
3. One enabled role and other roles stopped.
4. AWG2, VLESS and OpenConnect Toads enabled concurrently.
5. One role fails to start; remaining roles still reach Ready.
6. One Toad crashes; other roles and publications stay unchanged.
7. One TUN is deleted before cleanup; cached destinations become parked.
8. A route removed while the role is healthy is not resurrected as a park later.
9. A pre-existing static route is never claimed or parked.
10. A parked route remains until the real lower-metric Leshy route is observed.
11. Endpoint rules exist before Toad transport starts.
12. Endpoint route always resolves through physical underlay while another VPN is default.
13. Physical underlay absent leaves endpoint destinations unreachable, not routed through
    a VPN.
14. Static endpoint change during Ready performs controlled recovery without manual
    disconnect.
15. Live endpoint rotation adds only the endpoint reported by the Toad.
16. Endpoint provider failure retains last-known-good policy.
17. Wi-Fi to Ethernet advances epoch and validates every enabled role independently.
18. DHCP preferred-source change advances epoch.
19. Gateway change advances epoch.
20. NetworkManager SITE/GLOBAL transitions do not advance epoch or restart a Toad.
21. NetworkManager restart with unchanged kernel path is a no-op.
22. Docker/veth creation and deletion is a no-op.
23. Resume with healthy transport does not restart it.
24. Resume with stale AWG2 handshake restarts AWG2 transport while preserving TUN.
25. Resume with stale Xray/OpenConnect uses full parked recovery.
26. Event burst during recovery cannot commit an old completion.
27. Core crash preserves/reconstructs parking and revalidates before publication.
28. Product stop restores DNS/system state and clears owned parking.
29. Profile switch parks old-zone routes until replacement routes appear.
30. Leshy unavailable never causes parks to be released early.
31. Desktop reconnects after core restart and resynchronizes revisions.
32. No snapshot, diagnostic or log contains secrets.
33. At every cutover stage exactly one component writes each routing namespace.

---

## 17. CI progression

### N0–N1

```text
go test ./...
go test -race ./internal/control/... ./internal/core/... ./internal/toadctl/...
desktop C++ unit tests
```

### N2–N3

```text
pure underlay/reconciler tests on every Go CI platform
Linux netlink tests where supported
race detector for controller/executor tests
```

### N4–N6

```text
privileged Linux netns job
legacy endpoint/parking regression scripts against compatibility mode
new Go endpoint/parking/recovery tests
isolated AWG2/Xray/OpenConnect jobs
```

### N7–N8

```text
Qt RealCoreClient tests
real-core UI tests
packaging tests
systemd service lifecycle test
upgrade and rollback cutover test
```

A legacy script may be removed from CI only after its behavior exists in a Go/netns test
and the corresponding legacy production component has been deleted.

---

## 18. Implementation sequence and merge boundaries

Use small mergeable changes in this order:

```text
1. N0 controller/supervisor split and generation correctness
2. N1 Toad control protocol and runtime refactor
3. N1 AWG2 capability implementation
4. N1 Xray/OpenConnect validation implementation
5. N2 underlay shadow monitor
6. N3 reconciler and executors
7. N4 endpoint manager in read-only parity mode
8. N4 endpoint single-writer cutover
9. N5 parking manager in read-only parity mode
10. N5 parking/publication single-writer cutover
11. N6 automatic recovery behind one global feature gate
12. N7 API v2 and Qt state surface
13. N8 service/install cutover
14. legacy deletion
15. N9 macOS implementation
```

Do not combine a new kernel-state owner and deletion of the old owner in the same initial
implementation commit. First land read-only comparison and parity tests, then perform one
explicit ownership-cutover change, then remove dead legacy code in a following change.

---

## 19. Definition of done

The v2 orchestration migration is complete only when all conditions hold:

1. Go core owns desired state, reconciliation, endpoint policy, parking, publication and
   Toad lifecycle.
2. All three current Toad protocol types implement honest validation and capability
   reporting.
3. Physical network changes are derived from canonical kernel state.
4. NetworkManager connectivity-only events and unrelated virtual interfaces cause no
   reconnect.
5. Resume validates every Ready role and recovers only stale roles.
6. Every disruptive transition parks before publication withdrawal and Toad teardown.
7. Parking is released only after a real replacement route is observed.
8. Endpoint transport never uses another managed VPN.
9. No shell/systemd network watchdog decides Toad recovery.
10. `leshy-route-watch.service`, shell `reconcile` and shell `route-lifecycle` are absent
    from the installed runtime.
11. The DNS health component, if still present, has no tunnel lifecycle responsibilities.
12. Qt 6 displays real underlay, endpoint, publication, parking and recovery state.
13. Linux production never silently falls back to FakeCore.
14. Core/Toad crashes recover with bounded backoff and without affecting unrelated roles.
15. The complete acceptance matrix and CI gates are green.
