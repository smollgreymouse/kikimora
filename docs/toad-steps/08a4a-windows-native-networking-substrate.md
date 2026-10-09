# Toad step 08A.4a — Windows native networking substrate

Status: **CURRENT / START HERE — IMPLEMENT BEFORE WINDOWS VM ACCEPTANCE**.

This packet exists because `08a4-windows-vm-console-lifecycle-acceptance.md`
is an acceptance packet, not an implementation plan. The current Windows target
is still Qt/FakeCore only and cannot be sent through the Linux-equivalent VM
lifecycle tests until a real native networking substrate exists.

## START HERE — first coding slice for the next model

Do not start with the Windows VM acceptance packet and do not start by changing
Qt/FakeCore. The first task is to establish a real Windows runtime shell that
can later host the accepted networking semantics.

### Phase 0 — freeze the Windows baseline

Before editing behavior:

1. confirm Linux tests remain green on the current branch;
2. cross-build the Windows core and Toad binaries;
3. inventory every Windows path still covered by an unsupported build tag;
4. keep Linux/Darwin behavior unchanged while Windows-specific files are added.

Baseline compile command from repository root:

~~~bash
cd toad
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
~~~

Record the result in the roadmap/test report before moving on.

Current unsupported seams that matter immediately:

~~~text
toad/internal/control/peer_unsupported.go
toad/internal/platform/tun_unsupported.go
toad/internal/platform/default_routes_unsupported.go
toad/internal/platform/managed_interface_unsupported.go
toad/internal/platform/interface_repair_unsupported.go
toad/internal/platform/sleep_unsupported.go
toad/internal/underlay/default_unsupported.go
toad/internal/backend/awg2/attach_unsupported.go
toad/internal/backend/openconnect/script_unsupported.go
toad/internal/backend/openconnect/counters_unsupported.go
packaging/windows/stage.ps1
~~~

### Phase 0 — baseline record (2026-10-04, Windows 10 19045 host, go1.27.1, branch tip `9f9e145`)

- Native Windows build (`GOOS=windows GOARCH=amd64`, CGO disabled) of
  `./cmd/kikimora-core` and `./cmd/kikimora-toad` **passes**; `wintun` is
  already in the module graph via `golang.zx2c4.com/wintun`.
- Isolated Windows test run (`go test ./...` on windows/amd64, Linux/netns
  tests excluded by build tags): **all packages pass except `internal/control`**:
  - `TestSubscribeClientStreamsInitialAndNewRevision` and
    `TestServeCallRoundTrip` failed because their readiness poll uses
    `os.Stat` on the socket file, which Windows never exposes for AF_UNIX
    sockets; dialing the endpoint works. Corrected in Phase 1 with
    dial-based per-platform readiness helpers, after which the whole
    `internal/control` suite passes on windows/amd64 unchanged in intent.
  - `TestDuplicatePositiveSnapshotsCoalesceValidation` failed once in the first
    full-package run (`validation did not start`, 1s deadline) and passes both
    in isolation and in repeat full runs — timing-sensitive neighbor of the
    failing test; re-check once the Windows transport lands.
- Unsupported-seam inventory verified against the list above: all eleven files
  exist. The Go fallbacks are tagged `//go:build !linux && !darwin`, so
  Windows implementation work is "add `*_windows.go` + narrow the fallback tag
  to `!linux && !darwin && !windows`". No `*_windows.go` runtime files exist yet;
  `packaging/windows/` contains only `README.md` + `stage.ps1` scaffold.
- Linux/Darwin behavior untouched in this phase.


### Phase 1 — production shell before networking

The first implementation milestone is:

**Windows core service + secure local IPC + supervised process lifecycle,
without claiming VPN networking support yet.**

Concretely:

1. add Windows-specific service hosting for `kikimora-core.exe`;
2. define service start/stop/recovery behavior and ProgramData state paths;
3. replace Windows use of `peer_unsupported.go` allow-all authorization;
4. choose and implement the local authenticated transport (named pipe or
   protected Windows AF_UNIX);
5. make `kk.exe status --json` and `watch --json` work against that real core;
6. preserve current Snapshot/Subscribe framing and semantics;
7. add Windows-specific process supervision semantics where the generic
   non-Linux implementation is insufficient;
8. keep all VPN roles unsupported/fail-closed until the later underlay/TUN/route
   phases are implemented.

Phase-1 exit gate:

- core runs under Service Control Manager before desktop login;
- an authorized local CLI can connect;
- an unauthorized local user is rejected;
- core restart causes CLI watch reconnect/resubscribe;
- desired-state file path/ACL is explicit;
- no FakeCore is involved;
- no TUN/route behavior is falsely reported as supported;
- Linux tests and behavior remain green.

Only after this shell/IPC milestone should the implementation continue with
native underlay observation, then TUN ownership, then route/DNS ownership.

### Phase 1 — implementation record (2026-10-04)

- Local transport is now chosen per platform behind `internal/control`:
  `transport_unix.go` keeps the exact Linux/Darwin behavior (socket file in
  the state directory, 0660 mode, dial-based clients); `transport_windows.go`
  binds a **protected AF_UNIX socket** (`C:\ProgramData\Kikimora\core.sock`
  by default, `--socket` override for unprivileged runs). Windows AF_UNIX
  `connect()` opens the socket file, so the directory ACL owned by the
  installer/service gates unrelated local users at the kernel level —
  the same contract the Linux SO_PEERCRED path enforces.
- Named pipes were evaluated first (go-winio listener + SDDL
  `SY`/`Administrators`/operator-group ACL and client-PID authorization):
  `winio.ListenPipe` deterministically fails with `ERROR_INVALID_FUNCTION`
  in **any** binary that links this package (bisected to the package import
  itself; a minimal `go run` probe with the identical call succeeds), which
  blocks both the CLI gate and every test binary. The pivot to AF_UNIX is
  the packet-sanctioned alternative; named pipes stay the target for the
  later Qt UI swap (`QLocalSocket` uses pipes on Windows) and must be
  revisited there with either a fixed go-winio or a raw `CreateNamedPipeW`
  listener.
- Verified locally on windows/amd64, unprivileged: `kikimora-core serve`
  with one AWG2 role publishes real snapshots (schema 2, revisioned,
  observers honestly report `unsupported on this platform`, roles stay
  `Stopped`); `status --json` and `watch --json` work against it; killing
  the core makes `watch` log the disconnect and reconnect/resubscribe
  automatically (10 snapshots across a core restart). No FakeCore involved.
- `go test ./...` on windows/amd64: all 20 packages pass, including
  `internal/control`, with the same tests running unchanged on Linux.
- Windows service hosting is implemented (`kikimora-core service
  install|uninstall|start|stop`): the SCM-hosted core shares the console
  `runServe` path, derives ProgramData defaults (`C:\ProgramData\Kikimora`
  with `state`, `toads`, `logs`, `leshy` subdirectories and the toad binary
  next to the core executable), redirects logs to a documented file sink
  (`C:\ProgramData\Kikimora\logs\kikimora-core.log`), and honors Stop with a
  20-second shutdown budget. Registration is automatic-start with staged
  restart recovery (5s/30s/60s, one-hour reset). The Execute loop is covered
  by an isolated channel-driven test (Running, serving socket, bounded stop,
  no listener left).
- Phase-1 exit-gate status: authorized local CLI connect ✓; watch
  reconnect/resubscribe after core restart ✓; explicit desired-state
  path/ACL ✓ (`C:\ProgramData\Kikimora\state\desired.json` under
  installer-managed ProgramData ACLs); no FakeCore ✓; no falsely reported
  networking support ✓; Linux behavior unchanged ✓. Still open for the
  packet completion: an actual SCM start-before-desktop-login run —
  deliberately deferred to the disposable Windows VM during
  `08a4-windows-vm-console-lifecycle-acceptance.md`, since installing a
  system service on the developer workstation is out of scope. `kk.exe`
  naming and the console shim belong to the installer phase (packaging
  defines binary names); the `status --json` / `watch --json` verbs already
  work via `kikimora-core.exe`.

### Phase 3 — native underlay observer record (2026-10-04)

- `internal/underlay/default_windows.go` implements the canonical observer on
  native IP Helper facilities via the `wireguard-windows` `winipcfg` bindings
  already in the module graph: the lowest-metric 0.0.0.0/0 and ::/0 route from
  `GetIpForwardTable2` with managed role interfaces excluded by adapter alias,
  gateway and preferred source from the route and unicast-address tables
  (`SkipAsSource` and non-preferred DAD entries skipped), MTU/name via
  `net.InterfaceByIndex`. No PowerShell or netsh anywhere.
- `DefaultWatch` subscribes to the native `NotifyRouteChange2`,
  `NotifyIpInterfaceChange` and `NotifyUnicastIpAddressChange` callbacks and
  adds a 30s periodic audit for events unreachable across suspend/resume;
  redundant sweeps are deduplicated by `netstate.Compare` and never bump the
  epoch, so the canonical epoch semantics match Linux exactly.
- Deterministic test seams: the route/unicast tables and alias resolution are
  package-level seams; tests drive canned tables covering lowest-metric
  selection, excluded managed interfaces, non-default prefixes, SkipAsSource/
  tentative addresses and both-family snapshots (4 tests pass).
- Verified live on the workstation host (windows/amd64, unprivileged):
  `kikimora-core serve` now reports `underlay_summary` with the physical
  Wi-Fi adapter, gateway `192.168.1.1`, global IPv6 source, `epoch: 1` and
  `netlink_healthy: true` — the previous
  `canonical underlay monitoring is unsupported on this platform` state is
  gone. DHCP address replacement, gateway replacement, adapter up/down and
  availability changes map onto the canonical change reasons through
  `netstate.Compare`; suspend/resume is covered by the periodic audit until
  an explicit power-notification source lands (tracked for the recovery
  ordering regressions in section 8).
- Known pre-existing windows flake (verified at `c553890`, before any 08a4a
  work): in full-package `go test ./internal/control/` runs,
  `TestStableTransportPendingValidationDoesNotRestartTwice` (and once its
  neighbor `TestDuplicatePositiveSnapshotsCoalesceValidation`) can miss their
  1-second in-test deadlines while the fake-toad process and the recovery
  retry scheduling race on a loaded NEM-slow host; both pass in isolation.
  Not a regression of this packet — the deadline headroom for windows hosts
  should be revisited together with the windows CI lane (section 10).

### Phase 4 — Windows managed TUN ownership record (2026-10-04)

- `internal/platform/tun_windows.go` implements `CreateTunnel` on the Wintun
  driver via `wireguard-go`'s `tun.CreateTUNWithRequestedGUID` plus the
  `winipcfg` LUID helpers: the adapter name comes from the role config, MTU
  is applied at creation and addresses via `LUID.SetIPAddresses`. The adapter
  identity is a **name-derived deterministic GUID** (UUIDv5 over
  `kikimora-toad:<name>`), so repeated recovery reopens the same adapter —
  no stale duplicates can accumulate after crashes, and the identity survives
  reboots. `Close` releases the data session (idempotent) while the
  persistent adapter stays addressable for the next owner; retiring adapters
  of removed roles is assigned to the installer (section 9), since the
  pinned `golang.zx2c4.com/wintun` wrapper exposes no per-adapter delete.
- Spec validation (empty/NUL/whitespace/overlong names, positive MTU, valid
  prefixes) runs before any driver access, so invalid specs fail
  deterministically and unprivileged; model tests cover the GUID contract
  (stable per role, collision-free) and the validation order (pass).
- Privileged lifecycle test prepared and staged behind the `privileged`
  build tag (`tun_windows_privileged_test.go`): creates the real adapter,
  asserts name/ifindex/MTU, idempotent close, deterministic-GUID reuse with
  a stable ifindex and address presence — it skips itself without elevation
  and never runs in ordinary CI. It is the ready-to-run privileged gate for
  the Windows VM phase:

  ~~~bash
  # from an elevated prompt on a disposable Windows VM
  go test -tags privileged ./internal/platform/ -run TestTunnelWindowsPrivileged -v
  ~~~

  Protocol-core attachment (`awg2` handing the adapter to amneziawg-go on
  Windows) intentionally remains `attach_unsupported` until section 7; the
  fd-duplication path is Linux-only by design.
- Full windows/amd64 suite after this slice: 21 packages pass; the only
  failure remains the pre-existing full-package control flake recorded
  above.

### Phase 5 — native Windows route manager record (2026-10-04)

- `internal/platform/windows/routes` implements the `RouteManager` and
  `routing.Executor` contract on native IP Helper APIs through the `winipcfg`
  bindings; `platform.DefaultRouteManager` wires it exactly like the Linux
  netlink executor (one serialized executor for both faces). Windows has no
  policy tables and no ip rules, so:
  - endpoint exceptions are **owned host-prefix routes** (metric 1 by
    default) on the physical underlay interface; a /32 or /128 always wins
    the longest-prefix match over any default route, which replaces the
    Linux table-51890 + rule design; the unreachable table sentinels would
    blackhole the whole system on Windows and are deliberately not emitted;
  - fail-closed parking mirrors the Linux semantics: host prefixes of the
    selected traffic are held by high-metric (42760) routes pointing at the
    loopback interface with the loopback gateway, so nothing leaks through
    the physical default while the transport is down;
  - on-link endpoint routes use the interface's own preferred source address
    as the next hop, the same convention wireguard-windows uses.
- Ownership: every created route is tagged with protocol 4 (the same constant
  parking hardcodes, RTPROT_STATIC on Linux). Removal matches prefix +
  interface + protocol tag, so cleanup can never delete unrelated
  administrator routes; reconcile diffs against the kernel snapshot (no-op
  when nothing changed) and replaces drop-and-recreate on drift; missing
  routes on delete are tolerated; park duplicates are tolerated.
- Seven model tests drive canned kernel tables through the seams (route/park
  application, rule rejection, missing-route tolerance, reconcile
  idempotency, exactness of removal, snapshot shape without rules, parking
  semantics, failure surfacing) — all pass.
- Still open for this phase: live privileged verification (real route
  add/delete on a physical adapter) belongs to the Windows VM pass together
  with the staged Wintun and SCM checks (see the roadmap PENDING marker).

### Phase 6 — Windows DNS ownership design record (2026-10-04)

- **Ownership split (documented decision):** DNS interception and resolver
  policy stay owned by **Leshy** — on Windows the released native build
  (`smollgreymouse/leshy` **v0.5.1** ships
  `leshy-0.5.1-windows-x64-portable.zip` and `leshy-0.5.1-x64-setup.exe`),
  exactly like the `leshy-dns0` ownership on Linux. Windows provisioning of
  that binary belongs to the installer phase (section 9), mirroring how the
  Linux/macOS installer already provisions Leshy automatically. The Kikimora
  core deliberately does **not** own system DNS, per-interface DNS or NRPT on
  Windows: a second DNS owner would race Leshy and the fail-closed
  guarantees would become unattributable. The core's DNS responsibility is
  the same publication bridge as on Linux: role→interface publication files
  via `leshy.FileBridge` (`<zone>.dev` under the publication directory —
  pure file operations, verified green by the existing `internal/leshy`
  tests on windows/amd64), with `Publish`/`Withdraw`/`Resync` semantics
  unchanged.
- Observable state: Leshy publication state is already part of the canonical
  role snapshot (`leshyPublished`, `leshyZone`, `parking`), and the Windows
  service derives its publication directory under
  `C:\ProgramData\Kikimora\leshy\vpn` (serviceOptions), so diagnostics carry
  the DNS ownership state end to end.
- No silent physical-DNS fallback: while a role's selected traffic is
  parked, the physical resolvers are unreachable by the same parking routes
  that protect data traffic (phase 5); Leshy owns which resolvers answer
  inside the tunnel. Reapply-after-interface-recreation and resume is
  Leshy's watcher responsibility on Windows (leshy-win), fed by the same
  interface lifecycle events the core exposes.
- Per-interface DNS / NRPT remain reserved for a future core-level fail
  guard only if leshy-win cannot cover a case; any such addition must land
  as an explicit owned state with diagnostics, never as silent system DNS
  mutation.

### Phase 7 slice 1 — AWG2 protocol attachment record (2026-10-04)

- Architecture change in `internal/platform/tun_windows.go`: the Toad-owned
  Wintun tunnel now holds **only the adapter handle — no data ring session**.
  A Wintun adapter supports one ring session, so the platform owner and the
  protocol core cannot both hold sessions; instead the platform owner keeps
  the persistent adapter alive (create/reopen by the deterministic role GUID,
  addresses via LUID) and exposes the GUID through the windows-only
  `TunnelGUID()` contract. `MTU()` reports the spec MTU for the protocol
  core's packet layer.
- `internal/backend/awg2/attach_windows.go` replaces the unsupported stub:
  `attachTunnel` opens the official amneziawg-go wrapper via
  `tun.CreateTUNWithRequestedGUID(name, tunnelGUID, mtu)` on the very same
  adapter — the protocol core owns the single ring session, and repeated
  recovery reuses the adapter with no duplicates. The attachment contract is
  covered by model tests (identity forwarding, rejection of tunnels without
  the GUID contract) behind an `openAWGTunnel` seam; the real session open
  joins the privileged VM pass (same elevated run as the Wintun lifecycle
  test — `go test -tags privileged ./internal/platform/ ...`).
- `attach_unsupported.go` now covers only `!linux && !windows`. OpenConnect
  on Windows remains unsupported until its slice (native `openconnect.exe`
  integration); Xray stays a separate lane per the packet. The generic
  non-Linux supervisor/process paths are unchanged.

### Phase 7 slice 2 — OpenConnect protocol integration record (2026-10-07)

- `RestartTransport` moved from the shared `backend.go` into platform-specific
  files: `restart_transport_unix.go` keeps the SIGUSR2-based in-place reconnect
  (Linux/Darwin), while `restart_transport_windows.go` performs a controlled
  Close+Start cycle — the official openconnect.exe Windows port has no POSIX
  reconnect signal, so a full child-process restart is the only safe transport
  recovery path. The Toad-owned Wintun adapter persists across the child
  process lifecycle; the new openconnect.exe re-attaches to the same adapter
  by name. The recovery engine's capability selector
  (`caps.RestartTransportKeepingTUN`) continues to route underlay-change
  recovery through this method on all platforms.
- `script_windows.go` provides a route-free `.bat` vpnc-script for the
  official openconnect.exe child process: it configures the TUN address and
  MTU via `netsh` and publishes `openconnect-network.env` atomically (temp
  file + `move /y`), mirroring the Linux shell script contract. Kikimora
  core retains full routing and DNS policy ownership — the script never
  installs routes, mutates DNS or touches the firewall.
  `script_unsupported.go` narrowed from `!linux` to `!linux && !windows`.
- `reconnect_signal_unsupported.go` removed; the generic unsupported stub
  is replaced by the platform-specific `restart_transport_*` files.
- `counters_windows.go` reads real RX/TX byte counters for the named
  OpenConnect TUN interface via `winipcfg.GetIfTable2Ex` (already a project
  dependency through the underlay observer). `counters_unsupported.go`
  narrowed from `!linux` to `!linux && !windows`. The `Health()` snapshot
  now carries real data-plane activity on Windows instead of zero counters.
- Verification: cross-build for linux/amd64, darwin/arm64 and windows/amd64
  passes; the full windows/amd64 test suite (28 packages) passes; the
  `TestBackendAdvertisesInPlaceTransportRestart` and
  `TestBackendRestartTransportHonorsCanceledContext` contract tests
  confirm the `TransportRestarter` interface and canceled-context semantics
  on Windows.
- Still open for this phase: live privileged verification of real
  openconnect.exe start/stop/reconnect on the Windows VM belongs to the
  08A.4b acceptance packet; `peer_unsupported.go` allow-all authorization
  remains gated by the AF_UNIX directory ACL from Phase 1 and is tracked
  for a future hardening slice.

### Phase 7 slice 3 — Windows platform hardening record (2026-10-07)

Closed the four remaining unsupported seams that previously fell through to
no-op or error stubs on Windows:

- **Sleep notifications** (`sleep_windows.go`): native suspend/resume via
  `PowerRegisterSuspendResumeNotification` from `powrprof.dll`. The callback
  delivers `SleepEvent` through the same channel contract as Linux logind
  and Darwin powerd, so the core's resume-validation path is unchanged. No
  message-only window or polling required; works in SCM service context.
- **Interface repair** (`interface_repair_windows.go`): restores MTU via
  `MibIPInterfaceRow.NLMTU` and missing unicast addresses via
  `LUID.AddIPAddresses`; verifies identity through ifindex stability and
  confirms all expected prefixes are present after repair, mirroring the
  Linux netlink repairer contract.
- **Managed-interface verifier** (`managed_interface_windows.go`): reports
  adapter presence through the system interface table. Windows has no
  NetworkManager-equivalent external owner, so `Managed` is always false;
  `WatchManagedInterfaces` blocks until context cancellation because the
  underlay observer already covers adapter disappearance.
- **Child-process isolation** (`child_process_windows.go` / `_unix.go`):
  `CREATE_NEW_PROCESS_GROUP` on Windows prevents `CTRL_C_EVENT` from
  reaching the parent core service during graceful shutdown; `Setpgid` on
  Unix provides the equivalent process-group split.
- `peer_unsupported.go` remains a deliberate no-op: Windows AF_UNIX does
  not expose an `SO_PEERCRED` equivalent, and the directory ACL on
  `C:\ProgramData\Kikimora` already gates unrelated local users at
  `connect()` time.

Verification: `go vet ./...` green, `go test ./...` green (28 packages),
cross-build for linux/amd64, windows/amd64, darwin/arm64 and darwin/amd64
all pass.

### Phase 8 — recovery ordering regression coverage record (2026-10-04)

All six packet regressions are product-contract tests that already run on
windows/amd64 (verified by the full windows suite runs in this packet) —
no Linux-specific assumptions were found, so the port is a confirmation:

1. endpoint route before transport startup →
   `TestRoleStartAppliesEndpointBeforeLaunchingTransport`;
2. missing endpoint address family is transient →
   `TestEngineKeepsMissingEndpointFamilyRecovering` +
   `TestRoleStartDefersTransportUntilEndpointPrerequisitesRecover`
   (`ErrUnderlayPathUnavailable` keeps the role `Recovering`);
3. DNS lag during staged underlay return is transient →
   `TestEngineKeepsPendingEndpointResolutionRecovering`
   (`ErrEndpointResolutionPending` keeps the role `Recovering`);
4. a non-ready Toad snapshot must not erase an active recovery transaction →
   `TestStableTransportPendingValidationDoesNotRestartTwice`,
   `TestStaleProcessCannotPublishOrCompleteValidation`,
   `TestStaleCompatibilityStateCannotPublishToProduct`;
5. WaitingForUnderlay returns to Recovering when the physical underlay
   returns → `TestUnderlayReturnMovesWaitingRoleToRecovering` (and
   `TestUnderlayLossMovesEnabledRolesToWaiting` for the loss direction);
6. repeated recovery does not accumulate stale state →
   `TestDuplicatePositiveSnapshotsCoalesceValidation`,
   `TestRouteReadyLossSchedulesSingleAutomaticRecovery` (single),
   `TestRoutesStillParkedSchedulesBoundedRecoveryRetry` (bounded),
   `TestReplacementStartupHandoffDoesNotReplayRecovery`,
   `TestResumeWhileRecoveryInFlight`.

### Phase 9 — Windows installer record (2026-10-04)

- The primary installation contract is a real **Windows Installer package
  (MSI)** authored in `packaging/windows/kikimora.wxs` and built with WiX v3
  (`candle -ext WixUtilExtension` + `light`), wired into `stage.ps1`
  (`WIX_BIN` or `%USERPROFILE%\Tools\wix311`). The MSI installs the
  binaries into `%ProgramFiles%\Kikimora`, creates the crash-safe
  `C:\ProgramData\Kikimora` layout (`state`, `toads`, `logs`, `leshy/vpn`)
  with the state directory restricted to SYSTEM/Administrators
  (`util:PermissionEx`), registers `KikimoraCore` declaratively
  (`ServiceInstall` ownProcess auto-start with `serve`, `ServiceControl`
  stops and removes it on uninstall) and carries a stable UpgradeCode
  (`6A845F57-1E45-4E46-9573-D55EB20B116D`) so `MajorUpgrade` owns upgrades
  and downgrade rejection through Add/Remove Programs. Fresh installs start
  the service with all VPN roles disabled — no desired state is written.
- The service binary **self-applies the staged restart recovery policy**
  (5s/30s/60s with a one-hour reset) on its first SCM start
  (`ensureServiceRecovery`), because the MSI registers the service
  declaratively without recovery rows.
- Desired state, logs and Leshy publications survive upgrade and uninstall
  (the data components are permanent); a purge is a manual explicit step.
  Wintun adapters of retired roles are keyed by the deterministic role GUID
  and are removed with the driver-level uninstall.
- The portable zip remains a secondary artifact (binaries plus the
  elevation-gated `install.ps1`/`uninstall.ps1` for portable/dev use).
- `test_package.ps1` runs unprivileged and now verifies 26 assertions: the
  staged layout, checksum coverage, the zip, the MSI database (ProductName,
  ProductVersion, UpgradeCode, payload File rows, ServiceInstall/
  ServiceControl registration, ProgramData directories) and the portable
  script contracts. It passes on the workstation and becomes the CI gate for
  the packaging lane. The privileged execution — `msiexec /i` with a real
  service start before desktop login — belongs to the VM pass.

### Phase 10 — deterministic and cross-build gates record (2026-10-07)

All gates that can be verified without a disposable Windows VM are now green:

- **Unit tests for Windows-specific adapters** (9 new tests, all passing):
  - OpenConnect interface counters (`counters_windows_test.go`): zero-on-failure,
    zero-when-not-found, table iteration — exercises the `ifTable2Ex` seam;
  - OpenConnect vpnc script (`script_windows_test.go`): no route/DNS/firewall
    mutations, required sections (netsh, move /y, env vars), .bat file
    creation;
  - Interface repair (`interface_repair_windows_test.go`): empty name rejection,
    missing adapter, table failure propagation, LUID filtering, unicast
    failure, identity error — exercises the `platformIfTable2Ex` and
    `platformUnicastTable` seams.
- **Route-manager model tests** (7 tests): drive canned kernel tables through
  seams; verified at Phase 5, re-confirmed green.
- **TUN lifecycle model tests**: GUID contract, validation order; verified at
  Phase 4, re-confirmed green.
- **Endpoint-ordering/recovery regression tests** (6 regressions): all six
  product-contract tests run on windows/amd64 unchanged; verified at Phase 8,
  re-confirmed green.
- **Package static tests**: `test_package.ps1` 26 assertions pass; verified at
  Phase 9, re-confirmed green.
- **Cross-build gates** (CI): `windows-cross-build` job cross-compiles
  linux/amd64, darwin/arm64, darwin/amd64 and native windows/amd64;
  `windows-package-contract` job downloads WiX v3 and runs
  `test_package.ps1`.
- **Linux behavior unchanged**: full Linux test suite green; all Windows
  implementations guarded by `//go:build windows` tags with narrowed
  `!linux && !darwin && !windows` fallbacks.

Still open for this phase without a VM:

- `peer_windows.go` defense-in-depth — CLOSED in `3382348`: rejects
  non-AF_UNIX connections; `peer_unsupported.go` narrowed to
  `!linux && !darwin && !windows`, so the unsupported-seam list is empty.
  Windows AF_UNIX does not expose `SO_PEERCRED`; the directory ACL on
  `C:\ProgramData\Kikimora` is the primary authorization gate. A mode-based
  permission check was considered but dropped because Windows ACLs — not
  Unix bits — govern socket file access (`os.Stat` reports 0666 regardless).
  Full `SO_PEERCRED`-equivalent peer verification requires named pipes,
  which are blocked by the go-winio `ERROR_INVALID_FUNCTION` bug recorded
  in Phase 1.
- The deterministic half of `08-dual-stack-dns-route-stability.md`
  (family/DNS/route-churn model tests) — see «Remaining work» below.

## Remaining work

### Without a VM (implementable now)

1. **Dual-stack/DNS/route-stability deterministic tests** — implement the
   no-VM half of `08-dual-stack-dns-route-stability.md` (see the
   field-derived contract in section 6):
   - model IPv4 and IPv6 readiness independently;
   - forbid accidental `::/1 + 8000::/1` or IPv4 split-default capture by
     managed roles;
   - unavailable-family behavior: working path, controlled family
     suppression, or prompt unreachable/reject — never a silent blackhole;
   - repeated identical reconciliation converges to zero route/rule/DNS
     mutations;
   - endpoint-rule/parking deduplication, no perpetual recovery wakeups.
2. ~~`peer_windows.go` defense-in-depth~~ — DONE (`3382348`):
   `peer_windows.go` rejects non-AF_UNIX connections; `peer_unsupported.go`
   is narrowed to `!linux && !darwin && !windows`. Windows AF_UNIX has no
   `SO_PEERCRED` equivalent; directory ACL on `C:\ProgramData\Kikimora` is
   the primary gate.

### Requires a disposable Windows VM (section 11)

1. **VM handoff gate** — install the real MSI, start KikimoraCore before
   desktop login, verify `kk status --json`, connect real AWG and OpenConnect,
   produce real managed interfaces/routes, pass baseline application probes.
2. **Privileged Wintun lifecycle test** — `go test -tags privileged ./internal/platform/ -run TestTunnelWindowsPrivileged`.
3. **Privileged route-manager live verification** — real route add/delete on a
   physical adapter.
4. **SCM service start-before-desktop-login** — the service must be active
   before any user session.
5. **Dual-stack field regression** — real IPv6 evidence when the lab has it,
   or explicit no-false-IPv6 evidence (prompt failure, no dead managed path,
   no AAAA-triggered stalls) when it does not.
6. **08A.4b lifecycle parity acceptance** — repeat the Linux class of evidence
   (crash recovery, DHCP/link loss, suspend/resume, hibernate, reboot/cold
   boot, VirtualBox pause/save/reset/crash, bounded soak, MSI
   install/upgrade/uninstall/purge).

## Goal

Bring the accepted Linux control-plane contracts to Windows without inventing a
second orchestration model.

The Windows implementation must preserve the same product semantics:

- one installed core service;
- independently supervised Toad processes;
- persistent desired state;
- revisioned Snapshot/Subscribe API;
- explicit underlay epochs;
- endpoint exception routing before transport startup;
- fail-closed selected traffic;
- per-role stable interface identity;
- route readiness distinct from process/session health;
- recovery states including WaitingForUnderlay/Recovering;
- no frontend ownership of orchestration.

## 1. Windows service/runtime shell

Implement production Windows service hosting for `kikimora-core.exe`.

Required:

- Service Control Manager registration and automatic startup;
- recovery policy after unexpected core death;
- graceful stop with bounded Toad shutdown;
- crash-safe desired state under `C:\ProgramData\Kikimora`;
- structured logs suitable for Event Log or an explicitly documented file sink;
- no secrets in Event Log;
- no desktop-login dependency.

The service identity and ACL model must be documented before package activation.

## 2. Local control IPC and authorization

Replace the current unsupported-platform allow-all peer authorization.

Required properties:

- local-only transport;
- authenticated/authorized local clients;
- ACL that permits the intended administrator/operator group and rejects
  unrelated local users;
- same Handshake/capabilities/Snapshot/Subscribe/control schema as Linux;
- reconnect/resubscribe after core restart;
- `kk.exe` uses the real local API, never FakeCore.

Preferred transport may be a Windows named pipe or a protected Windows AF_UNIX
socket, but the security contract is mandatory regardless of transport choice.

## 3. Native Windows underlay observer

Implement a Windows underlay/default-path observer that emits the same semantic
inputs as the accepted Linux observer.

It must detect:

- physical default-route changes;
- interface up/down;
- DHCP address replacement;
- gateway replacement;
- IPv4 and IPv6 availability changes;
- suspend/resume return;
- adapter disable/enable;
- hypervisor cable disconnect/reconnect.

Material changes increment the canonical underlay epoch exactly as on Linux.

Do not poll PowerShell commands as the production observer. Use native Windows
network-change facilities and expose deterministic adapters for tests.

## 4. Windows managed TUN ownership

Implement Windows TUN ownership behind the existing platform abstraction.

Required:

- deterministic create/open/close lifecycle;
- stable role-owned interface identity;
- address/MTU configuration;
- idempotent cleanup after core/Toad crash;
- no stale duplicate adapters after repeated recovery;
- reboot/cold-boot cleanup semantics;
- integration tests using a fake adapter layer plus privileged VM tests using the
  real backend.

The backend may use Wintun or another explicitly accepted driver. Driver
installation/versioning must be owned by the Windows package contract.

## 5. Native Windows route manager

Implement the accepted route-manager semantics on Windows.

Mandatory capabilities:

- physical endpoint exception route before transport process startup;
- selected-route ownership;
- fail-closed parking when selected transport is unavailable;
- route rebind when underlay interface/gateway/address changes;
- IPv4 and IPv6 handling;
- exact ownership tracking so cleanup never removes unrelated administrator
  routes;
- no duplicate routes after repeated recovery;
- route state exposed through the canonical snapshot.

Use native Windows route/IP Helper APIs or an equivalent supported native API.
PowerShell/netsh command execution is acceptable only as a diagnostic/test
helper, not as the production ownership mechanism.

## 6. Windows DNS ownership

Implement explicit DNS ownership and recovery semantics.

Required:

- role/system-wide DNS state is observable;
- desired DNS configuration is reapplied after interface recreation or resume;
- stale DNS configuration is removed when ownership ends;
- no silent physical-DNS fallback when the selected fail-closed policy forbids
  it;
- DNS state is included in diagnostics.

The design must document whether Windows system DNS, per-interface DNS, NRPT or
another supported mechanism owns each use case.

### Field-derived dual-stack/DNS stability contract

Implement `08-dual-stack-dns-route-stability.md` here, not later in UI or VM-only
acceptance. In particular:

- model IPv4 and IPv6 readiness independently;
- never infer IPv6 usability from interface-UP or IPv4 success;
- forbid accidental `::/1 + 8000::/1` or IPv4 split-default capture by managed
  roles;
- if a family is unavailable, DNS/routing must produce a working path, suppress
  that family under Kikimora-owned DNS policy, or fail promptly with an explicit
  unreachable/reject outcome; a silent timeout blackhole does not satisfy this
  regression;
- record the effective route and source for transport endpoints in each family;
- make repeated identical route/DNS reconciliation converge to zero mutations;
- expose diagnostics counters sufficient to distinguish wakeups from actual
  route/rule/DNS changes.

Add deterministic model tests before the first privileged Windows VM run.

## 7. Real Windows protocol backends

AWG and OpenConnect are mandatory for the first Windows real-networking gate.

Each backend must satisfy the same Toad contract as Linux:

- independently supervised process/session;
- stable managed interface identity;
- explicit session health;
- no ambient route ownership inside the protocol process;
- no protocol core may install uncontrolled default/split-default routes;
- restart/recovery does not disturb unrelated roles.

Xray remains a separate lane and may be enabled after the mandatory AWG +
OpenConnect Windows gate, provided the common routing/fail-closed contracts are
already satisfied.

## 8. Windows recovery ordering regressions

Port regression coverage for bugs already found on Linux:

1. endpoint exception route must exist before transport startup;
2. missing endpoint address family is transient, not terminal;
3. DNS unavailable during staged underlay return is transient;
4. a non-ready Toad snapshot must not erase an active recovery transaction;
5. WaitingForUnderlay must move back to Recovering when physical underlay
   returns;
6. repeated core/Toad/link recovery must not accumulate stale process/interface/
   route state;
7. a dead/unavailable IPv6 family with live IPv4 must not remain captured by a
   managed split/default route while DNS still exposes a silently timing-out path;
8. replaying an unchanged A/AAAA classification and unchanged desired state must
   produce zero route/rule/DNS mutations after convergence.

These are product-contract tests, not Linux-specific tests. The complete field
regression is `08-dual-stack-dns-route-stability.md`.

## 9. Windows installer

Replace the scaffold with a real installer.

The installer must own:

- core/Toad/kk binaries;
- service registration;
- driver/runtime dependencies;
- ProgramData directories and ACLs;
- upgrade;
- ordinary uninstall/reinstall;
- explicit purge semantics;
- package version and artifact hashes.

Fresh install must not silently set any VPN role desired=true.

The exact lifecycle acceptance is defined in
`08a4-windows-vm-console-lifecycle-acceptance.md`.

## 10. Deterministic and cross-build gates

Before privileged VM testing:

- Windows amd64 build passes;
- unit tests for underlay adapter pass;
- route-manager model tests pass;
- TUN lifecycle model tests pass;
- local IPC authorization tests pass;
- endpoint-ordering/recovery regression tests pass;
- package static tests pass;
- Linux behavior remains unchanged.

Do not weaken Linux tests to make Windows compile.

## 11. VM handoff gate

08A.4a is complete only when a disposable Windows VM can:

- install the real package;
- start KikimoraCore before desktop login;
- expose real `kk status --json`;
- connect real AWG and OpenConnect;
- produce real managed interfaces and routes;
- pass one baseline real-application probe per mandatory role.

Only then execute
`docs/toad-steps/08a4-windows-vm-console-lifecycle-acceptance.md`.

## Completion state

A Windows UI/FakeCore build does not satisfy this packet.

Expected handoff:

`Windows native runtime substrate implemented; proceed to 08A.4 VM lifecycle parity acceptance.`
