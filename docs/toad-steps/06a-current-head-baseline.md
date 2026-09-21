# Toad step 06A — restore current-HEAD baseline before multi-Toad gate

Status: **deferred after step 06 by explicit 2026-09-21 execution decision**.

The Xray health/counter validation in this packet is intentionally postponed. Do not treat this as complete; return to it after the simultaneous three-Toad gate.

Reviewed code baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

This packet exists because the large orchestration/UI push landed before the older simultaneous multi-Toad Stage 0 gate and left the PR CI red. Do not start privileged cutover or later recovery hardening until this packet is green.

This packet is intentionally narrow: restore deterministic CI and correct the Xray false-online regression. Do not implement the later routing/resume redesign here.

## Acceptance target

At the end of this packet:

- every workflow currently red because of deterministic source/test regressions is green;
- `linux-xray-lifecycle` again proves an unreachable VLESS/REALITY server is **not** reported as a connected session;
- existing real Xray interop still reaches online after actual tunneled traffic;
- no test is weakened to match an incorrect production state;
- no real-VPS test is run.

## 1. Fix Xray state semantics, not the lifecycle test

### Current defect

File: `toad/internal/backend/xray/backend.go`.

Current HEAD lines 90-129 make `net.Interface.FlagUp` equivalent to a connected VLESS/REALITY session:

```go
connected := iface.Flags&net.FlagUp != 0
state := "connecting"
...
if connected {
    state = "online"
}
...
Connected: connected,
```

This caused CI `linux-xray-lifecycle` to fail: with no real Xray server, `state.json` immediately became `online`.

### Required state split

A TUN that exists and is UP proves route-target readiness, not remote transport success.

Change the Xray backend so:

- Xray instance not running -> `degraded`;
- TUN absent/down -> `connecting`, `Connected=false`;
- TUN up but no proven tunneled payload yet -> `connecting`, `Connected=false`;
- after actual TUN payload has crossed the Xray inbound -> `online`, `Connected=true`.

Do not use a synthetic public probe inside the backend.

### Concrete implementation

File: `toad/internal/backend/xray/config.go`.

Extend `xrayConfig` with Xray’s built-in stats and policy configuration. Use the pinned Xray API, not guessed fields:

```go
type xrayConfig struct {
    Log       xrayLog        `json:"log"`
    Inbounds  []xrayInbound  `json:"inbounds"`
    Outbounds []xrayOutbound `json:"outbounds"`
    Policy    xrayPolicy     `json:"policy"`
    Stats     xrayStats      `json:"stats"`
}

type xrayPolicy struct {
    System xraySystemPolicy `json:"system"`
}

type xraySystemPolicy struct {
    StatsInboundUplink   bool `json:"statsInboundUplink"`
    StatsInboundDownlink bool `json:"statsInboundDownlink"`
}

type xrayStats struct{}
```

When building the TUN inbound tagged `toad-tun`, set both inbound stats flags true and emit `"stats": {}`.

Pinned Xray evidence already checked by the planner:

- `proxy/tun/handler.go` registers `inbound>>>toad-tun>>>traffic>>>uplink` and `...>>>downlink` counters when system inbound stats are enabled;
- `core.Instance.GetFeature(stats.ManagerType())` is available at the pinned revision;
- `features/stats.Manager.GetCounter` returns those counters.

File: `toad/internal/backend/xray/backend.go`.

Add fields:

```go
stats stats.Manager
lastRX uint64
lastTX uint64
everTransferred bool
```

Import Xray feature stats under an unambiguous alias, for example:

```go
featurestats "github.com/xtls/xray-core/features/stats"
```

After `instance.Start()` at current lines 80-86:

```go
manager, _ := instance.GetFeature(featurestats.ManagerType()).(featurestats.Manager)
b.stats = manager
```

Add a locked helper:

```go
func (b *Backend) trafficLocked() (rx, tx uint64) {
    if b.stats == nil {
        return 0, 0
    }
    up := b.stats.GetCounter("inbound>>>toad-tun>>>traffic>>>uplink")
    down := b.stats.GetCounter("inbound>>>toad-tun>>>traffic>>>downlink")
    if up != nil && up.Value() > 0 { tx = uint64(up.Value()) }
    if down != nil && down.Value() > 0 { rx = uint64(down.Value()) }
    return
}
```

In `Health`, TUN UP only establishes the route-target half. Read the counters. If either counter is nonzero, set `everTransferred=true`. Populate `RXBytes/TXBytes`.

Expected replacement of current lines 116-128:

```go
tunUp := iface.Flags&net.FlagUp != 0
rx, tx := b.trafficLocked()
if rx > 0 || tx > 0 {
    b.everTransferred = true
}

state := "connecting"
reason := "Xray managed TUN is up; tunneled session not yet proven"
connected := false
if !tunUp {
    reason = "Xray managed TUN exists but link is not up"
} else if b.everTransferred {
    state = "online"
    reason = "Xray tunneled traffic observed"
    connected = true
}

b.health = backend.Health{
    State: state, Reason: reason, Connected: connected,
    RXBytes: rx, TXBytes: tx, Endpoint: endpoint,
}
```

Reset `stats`, counters and `everTransferred` in `Close`.

### Tests

Add/extend `toad/internal/backend/xray/config_test.go`:

- generated config has `stats`;
- `policy.system.statsInboundUplink=true`;
- `policy.system.statsInboundDownlink=true`;
- inbound tag remains exactly `toad-tun`.

Do not attempt to unit-test Xray feature internals by mocking private Xray types.

Keep the existing real gates as acceptance:

```bash
./linux/tests/toad/run-isolated.sh xray-lifecycle
./linux/tests/toad/run-isolated.sh xray-interop
```

Expected lifecycle: `connecting` without reference server.
Expected interop: actual payload causes counters to advance and eventually `online`.

**STOP/DESIGN:** if the pinned Xray stats manager does not expose the counters under the exact names above after enabling policy/stats, stop. Do not invent another health mechanism. Record the generated Xray config plus available counter names and create `docs/toad-steps/06a-xray-health-followup.md`.

## 2. Repair Windows test/config portability

Current Windows Go job fails before product semantics because tests construct TOML using raw Windows paths.

File: `toad/internal/control/control_test.go`.

Find helper(s) that format:

```toml
state_dir = "%s"
```

with `t.TempDir()`/Windows paths. Replace raw interpolation with a helper using `strconv.Quote`:

```go
func tomlString(value string) string {
    return strconv.Quote(value)
}
```

Then format as:

```go
fmt.Sprintf("state_dir = %s\n", tomlString(stateDir))
```

Apply the same helper to every path inserted into TOML in this test file. Do not replace backslashes manually in each test.

File: `toad/internal/endpoint/manager_test.go:43+`.

`TestCommandProviderUsesAllowlistedEnvironment` writes a POSIX `provider.sh`; Windows cannot execute it. The product command-provider contract is OS command execution, while this fixture specifically tests POSIX environment semantics.

Add:

```go
if runtime.GOOS == "windows" {
    t.Skip("POSIX provider fixture")
}
```

and import `runtime`.

Do not skip the whole endpoint package.

## 3. Make directory durability best-effort only on platforms that do not support directory fsync

Current Windows failures occur in:

- `state.Writer.Write` after successful rename;
- `leshy.FileBridge` atomic publication/resync;
- similar atomic writers should be searched once by exact `.Sync()` on opened directories.

Linux durability should remain strict.

Implement one shared helper rather than sprinkling `runtime.GOOS` checks.

Create:

`toad/internal/fsutil/syncdir.go`

```go
package fsutil

func SyncDir(path string) error {
    dir, err := os.Open(path)
    if err != nil { return err }
    defer dir.Close()
    if err := dir.Sync(); err != nil {
        if runtime.GOOS == "windows" {
            return nil
        }
        return err
    }
    return nil
}
```

Then replace directory-open/sync blocks in:

- `toad/internal/state/state.go`;
- `toad/internal/leshy/bridge.go`;
- `toad/internal/parking/checkpoint.go`;
- any endpoint/core atomic state writer found by the exact pattern.

Do not suppress file `Sync()`; only directory sync gets this platform exception.

Add `fsutil` unit coverage for empty/nonexistent path and a successful temp directory. Windows CI itself is the unsupported-directory-sync regression.

## 4. Shorten Darwin Unix-socket test paths

macOS unit failure:

```text
dial unix /var/folders/.../TestCoreIPC.../core.sock: connect: invalid argument
```

The fixture exceeded Darwin `sockaddr_un.sun_path`.

File: `toad/internal/control/control_test.go`, tests:

- `TestCoreIPCControlsThreeToadsEndToEnd`;
- `TestCoreIPCUsesFakeToadBinaryWithoutNetwork`;
- any other test creating a Unix socket below `t.TempDir()`.

Add a helper:

```go
func shortSocketDir(t *testing.T) string {
    t.Helper()
    dir, err := os.MkdirTemp("", "kk-")
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = os.RemoveAll(dir) })
    return dir
}
```

Use only for socket directories; keep ordinary fixture files in `t.TempDir()`.

Do not disable the macOS IPC tests.

## 5. Fix current shell/CLI regressions

### ShellCheck

File: `linux/files/kikimora-cli/orchestration.sh:90`.

Current code:

```bash
for unit in leshy-route-watch.service; do
    ...
done
```

There is exactly one unit. Replace the loop with one direct `printf`; do not add an SC2043 suppression.

### Completion contract

The new top-level `orchestration` command is missing from at least one completion/help fixture. Run:

```bash
bash linux/tests/completions.sh
```

Update all three installed completion implementations and their expected command lists together:

- `linux/completions/kikimora.bash`;
- Zsh completion file in `linux/completions/`;
- Fish completion file in `linux/completions/`;
- `linux/tests/completions.sh`;
- help text if the test identifies a mismatch.

The canonical top-level command set must include `orchestration`, with subcommands:

```text
status
cutover
rollback
retire-legacy
```

including `--go` and `--confirm` only where valid.

### JSON dispatch

Current `linux/tests/json_api.py` fails on:

```text
kikimora status legacy-arg
```

Audit only the dispatcher branch touched by adding orchestration. Preserve the legacy contract that existing commands and their positional arguments dispatch exactly as before.

Do not “fix” the test by removing `legacy-arg`.

Run:

```bash
python3 linux/tests/json_api.py
```

## 6. Make desktop revision assertions monotonic, not absolute

Current Linux Desktop E2E failure at `desktop/tests/RealCoreEndToEndTest.cpp:111` expects revision exactly 1 but receives 2.

The new core legitimately has additional initialization observations, so the API contract should be monotonic revision, not a magic initial value.

Replace the absolute assertion with:

1. capture initial revision after first authoritative snapshot;
2. after command/stream update assert `revision > initialRevision`;
3. where testing deduplication, assert unchanged only around the operation that is supposed to be idempotent.

Do not simply change expected 1 to 2.

Run all four CTest targets.

## 7. Current-head verification

Run locally where possible:

```bash
cd toad
go test ./...
go test -race ./...
go vet ./...
cd ..

bash linux/tests/completions.sh
python3 linux/tests/json_api.py
bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
git diff --check
```

Then let PR CI verify Linux/macOS/Windows.

Privileged isolated commands are required only for the Xray lifecycle/interop part of this packet. If local sudo is unavailable, CI evidence is acceptable for those two gates.

## Completion report

Return:

1. commit SHA(s);
2. exact CI run ID;
3. all workflow conclusions;
4. Xray lifecycle state before real traffic;
5. Xray interop state/counter evidence after real traffic;
6. Windows/macOS fixes made;
7. any tests changed and why;
8. confirmation that no acceptance test was weakened;
9. any **STOP/DESIGN** follow-up created.

Only after this packet is green proceed to `docs/toad-steps/06-multi-toad-isolated.md`.
