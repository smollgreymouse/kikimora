# Toad step 07D — persisted desired state and privileged production cutover acceptance

Status: **REOPENED for privileged acceptance rebuild by 07E**. Persistence, startup restore and API-aware cutover fixture are present, but the first `go-orchestration-acceptance.sh` draft does not follow the real core CLI/config/Go-ownership contract and is not valid acceptance evidence. Rebuild it from the proven multi-Toad fixtures and run it under `07e-current-head-proof-closure.md`. Real installed-host cutover and legacy retirement remain manual/operator decisions.

Reviewed implementation baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

This is the final Linux production-cutover packet for the current Go orchestration stage. It must not be executed while any prior packet is red.

## Critical startup defect this packet fixes

Current installed service only runs:

```text
kikimora-core serve ...
```

`control.NewManager` initializes every role with `enabled=false`. Current `kk orchestration cutover --go` only checks `systemctl is-active kikimora-core.service`; it never calls `ConnectAll` and never proves any Toad became Ready.

Therefore current code can print “cutover complete” with zero Toads running, and a reboot loses desired connectivity.

## Goal

After this packet:

- desired per-role enabled state and active profile survive core restart/reboot;
- first migration cutover explicitly seeds desired state;
- service startup restores desired state only after ownership/config are validated;
- cutover succeeds only when the core API is reachable and all desired roles are structurally Ready for the current underlay epoch;
- failed cutover stops Go Toads before restoring legacy ownership/writers;
- rollback is idempotent;
- privileged namespace/systemd/manual suspend tests cover the real ownership boundary;
- legacy writers are retired only after a proven rollback window.

## 1. Add persisted desired-state schema

Create `toad/internal/control/desired_state.go`.

```go
const DesiredStateSchema = 1

type PersistedDesiredState struct {
    Schema        int             `json:"schema"`
    ActiveProfile string          `json:"active_profile"`
    Roles         map[string]bool `json:"roles"`
    UpdatedAt     time.Time       `json:"updated_at"`
}

type DesiredStateStore interface {
    Load(context.Context) (PersistedDesiredState, error)
    Save(context.Context, PersistedDesiredState) error
}

type FileDesiredStateStore struct {
    Path string
}
```

File requirements:

- directory mode 0750 or tighter;
- file mode 0600;
- atomic temp + rename;
- file fsync;
- directory fsync where supported;
- no protocol secrets are written.

Corrupt JSON must not silently become “all disabled”. Return an explicit startup error in Go-owned mode.

## 2. Make Manager persist every desired-state mutation

Extend `control.Manager` with `desiredStore DesiredStateStore`.

Constructor injection is preferred so commands cannot arrive before persistence is configured.

### SetRoleDesired / ConnectAll / DisconnectAll

Persist after the in-memory desired mutation but before returning success to the API caller.

Failure semantics:

- persistence error returns command failure;
- revert in-memory desired state to the previous value;
- do not claim success.

For aggregate commands, save one complete desired-state snapshot after all desired bits are changed, not one file write per role.

### SetActiveProfile

Persist active profile atomically with the current desired-role map.

### Observed failures

Do not rewrite desired state merely because transport/underlay state changes. Desired state is operator intent.

### Product shutdown is not DisconnectAll

Current `Manager.Close()` calls `DisconnectAll()`, which mutates desired=false. Split service shutdown from user disconnect.

Add:

```go
func (m *Manager) Shutdown(ctx context.Context, intent core.ShutdownIntent) error
```

For `ShutdownProduct`:

- stop live Toad processes;
- execute required publication/parking cleanup;
- keep persisted desired bits unchanged.

`DisconnectAll` remains the explicit user action that persists all desired=false.

Required restart test:

```text
ConnectAll -> desired true persisted
product shutdown -> child processes stop
desired file remains true
new core -> roles restore desired true
```

## 3. Restore desired state on core startup

File: `toad/cmd/kikimora-core/main.go`.

Add:

```text
--state-dir PATH
```

Installed Linux path:

```text
/var/lib/kikimora/core
/var/lib/kikimora/core/desired.json
```

Exact startup order:

1. parse/validate all Toad configs;
2. load/validate ownership;
3. construct Manager and recovery driver;
4. construct desired-state store;
5. start canonical underlay observer;
6. if lifecycle ownership is not Go-owned, do not restore/start persisted roles;
7. if Go-owned, load persisted active profile + desired roles;
8. schedule desired=true roles through ordinary ConnectRole/reconcile paths;
9. never bypass endpoint, validation, publication or parking gates during restore.

Missing desired file in already-Go-owned mode means all roles disabled plus an explicit diagnostic reason. Do not fabricate a Ready state.

First migration cutover seeds the file via the core API in section 5.

## 4. Systemd state directory and shutdown

File: `linux/files/kikimora-core.service`.

Add:

```ini
StateDirectory=kikimora/core
StateDirectoryMode=0750
```

and add to ExecStart:

```text
--state-dir /var/lib/kikimora/core
```

If the supported systemd target does not accept nested StateDirectory, stop and use one consistent alternative such as `StateDirectory=kikimora-core` and `/var/lib/kikimora-core`. Do not let installer and systemd own different state paths.

Keep existing capability and socket-group constraints.

SIGTERM must call product shutdown that stops children without changing persisted desired intent.

## 5. Strengthen `kk orchestration cutover --go`

File: `linux/files/kikimora-cli/orchestration.sh`.

Current success criterion is only systemd-active. Replace it with an API readiness gate.

Add testable settings:

```bash
ORCH_CORE_SOCKET="${KIKIMORA_ORCHESTRATION_CORE_SOCKET:-/run/kikimora/core.sock}"
ORCH_READY_TIMEOUT="${KIKIMORA_ORCHESTRATION_READY_TIMEOUT:-60}"
```

### Preflight before any mutation

Require:

- core and Toad binaries executable;
- ownership/config files present;
- every Toad TOML validates;
- duplicate role/interface/endpoint priority checks pass;
- 07C NetworkManager ownership preflight passes when NetworkManager is present;
- current ownership is a supported migration state.

Add a read-only command:

```text
sudo kk orchestration preflight
```

It must not stop units or rewrite ownership.

### Migration sequence

Exact order:

1. save old ownership values;
2. stop/disable legacy route writer;
3. atomically write `go/go/go`;
4. daemon-reload;
5. enable/start `kikimora-core.service`;
6. wait for core socket + successful Handshake/API response;
7. call:
   ```bash
   "$ORCH_CORE_BIN" start --socket "$ORCH_CORE_SOCKET"
   ```
   to seed persisted all-enabled desired state for the first migration;
8. poll machine JSON until the acceptance predicate below is true;
9. only then print cutover complete.

Do not write `desired.json` from shell.

### Readiness predicate

Use `kikimora-core status --json`. Do not grep human text.

Success requires:

- aggregate state `Ready`;
- physical underlay present on at least one family for initial migration;
- every desired role:
  - product state `Ready`;
  - `route_ready=true`;
  - `validated_underlay_epoch == underlay.epoch`;
  - endpoint state is not failed/pending for an older epoch;
  - `parking.active=false`;
- no role Failed.

Xray does not need prior public traffic; its structural Ready contract comes from 07A.

### Failure rollback

On any failure after legacy writer stop:

1. if core API is reachable, disconnect/stop Go roles;
2. stop/disable core;
3. restore old ownership atomically;
4. daemon-reload;
5. enable/start legacy units;
6. verify legacy route writer active;
7. return failure.

Centralize this in one rollback helper/ERR guard. Do not leave Go Toads running after returning ownership to legacy/external.

## 6. Strengthen explicit rollback

`kk orchestration rollback` must:

1. verify legacy writer files still exist;
2. if core API reachable, stop Go roles first;
3. stop/disable core;
4. write `legacy/external/legacy`;
5. daemon-reload;
6. enable/start legacy units;
7. wait for legacy route writer active;
8. report success.

Do not clear persisted desired state during rollback.

## 7. API-aware cutover fixture

Extend `linux/tests/toad/orchestration-cutover.sh`.

The fake core binary must emulate `status --json`, `start --socket`, and disconnect/stop behavior, or use the real core with fake launchers.

Required cases:

- happy path: ConnectAll called and Ready reached;
- systemd core start failure -> rollback;
- service active but socket/API absent -> rollback;
- API present but one role Failed -> rollback;
- API remains Connecting until timeout -> rollback;
- NetworkManager preflight blocker -> no ownership/service mutation;
- repeated already-Go cutover is idempotent and does not reset an intentionally disabled role;
- rollback stops Go roles before legacy ownership is restored.

No real sleeps in fixture state progression.

## 8. Restart/reboot desired-state integration test

Headless Manager test:

```text
ConnectAll
primary=true secondary=true persisted
ShutdownProduct
new Manager + same desired file
both launch

DisconnectRole secondary
primary=true secondary=false persisted
ShutdownProduct/restart
only primary launches
```

Also test corrupt desired file -> explicit Go-owned startup error.

Desktop E2E must verify reconnecting to a restarted core shows restored desired state rather than all Stopped.

## 9. Privileged orchestration acceptance

Add:

```text
linux/tests/toad/go-orchestration-acceptance.sh
run-isolated.sh orchestration-acceptance
```

Use private namespaces and the same real three-protocol fixtures as step 06.

Required phases:

### Initial connect

- core controls all three Toads;
- every desired role Ready/current epoch;
- endpoint underlay routes/rules present;
- Leshy publications present;
- parking inactive;
- no IPv4/IPv6 selected-route leak.

### One underlay loss/recovery

- epoch changes once after coalescing;
- affected role parks/withdraws/applies endpoint/transport recovers/validates/publishes/restores;
- independent roles remain usable;
- affected role is never Ready while parking remains active.

### TUN address drift

- remove AWG/Xray local address;
- route readiness drops;
- same ifindex repaired;
- no physical leak.

### Toad crash

- kill one Toad unexpectedly;
- only that role restarts;
- other role PIDs/ifindices unchanged;
- failed-role selected traffic remains fail closed.

### Core crash/restart

- persisted desired state relaunches intended roles;
- stale generation snapshot rejected;
- endpoint/routing reconciliation is idempotent;
- checkpoint/parking restoration is safe.

### Deliberate per-role disconnect

- only that role persists desired=false;
- after core restart it stays stopped;
- other desired roles restore.

No public VPN data plane is needed.

## 10. Real installed-host acceptance

This is manual/operator-confirmed and cannot be marked complete by CI.

Before mutation:

```bash
sudo kk orchestration preflight
sudo kk orchestration status
```

Capture:

- IPv4/IPv6 routes/rules;
- table 51890;
- NetworkManager managed state for `kk-*`;
- legacy/core service state;
- Toad PIDs/generations;
- endpoint policy;
- parking;
- Leshy publication;
- DNS status.

Then:

```bash
sudo kk orchestration cutover --go
```

The command itself must wait for the readiness predicate.

After successful cutover:

- exercise expected primary/secondary selected destinations;
- restart core:
  ```bash
  sudo systemctl restart kikimora-core.service
  ```
- prove persisted desired roles restore automatically;
- perform the real suspend/resume gate from 07C.

Do not invoke `real-vps-vless`.

Only after a separate stable observation window and explicit operator decision:

```bash
sudo kk orchestration retire-legacy --confirm
```

Do not retire legacy writers in the same first-cutover run.

## 11. Diagnostics acceptance

Before retirement, `kk diag` must collect, bounded/redacted:

- ownership triple;
- core snapshot JSON;
- persisted desired state;
- Toad generations/states;
- underlay epoch/interface/gateway/source;
- endpoint configured/live/applied epoch;
- parking;
- Leshy publication;
- routes/rules/table 51890;
- NetworkManager managed state;
- core/Toad service/process state;
- recent logs.

Never include OpenConnect cookies/token secrets/passwords or protocol private keys.

## 12. macOS boundary

Linux production acceptance does not complete the cross-platform roadmap.

macOS still requires actual-host verification of launchd, utun, BSD routing, sleep/wake, desired-state restart restoration and Leshy integration.

Windows remains UI/FakeCore scope for this roadmap.

## STOP/DESIGN conditions

Stop and write a focused plan if:

1. desired state must mean something other than per-role persisted intent;
2. first cutover must preserve only a subset of currently active legacy roles instead of ConnectAll;
3. rollback must restart external tunnel applications that current migration does not control;
4. a protocol cannot satisfy the 07A structural Ready predicate;
5. target systemd StateDirectory semantics differ from this packet.

Do not invent a shell-only desired-state format.

## Acceptance commands

Non-privileged:

```bash
cd toad
go test ./internal/control ./internal/core ./...
go test -race ./internal/control ./internal/core
go vet ./...
cd ..
bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash linux/tests/completions.sh
python3 linux/tests/json_api.py
```

Privileged:

```bash
sudo bash linux/tests/toad/run-isolated.sh multi-toad
sudo bash linux/tests/toad/run-isolated.sh orchestration-acceptance
```

Then real installed-host preflight/cutover/core-restart/suspend gate.

## Executor report

Return:

1. commits;
2. desired-state schema and restart evidence;
3. cutover readiness predicate;
4. every rollback case;
5. privileged orchestration acceptance output;
6. real host cutover/restart result if performed;
7. suspend/resume result;
8. diagnostics archive;
9. CI run IDs;
10. whether legacy retirement was performed;
11. any STOP/DESIGN packet.
