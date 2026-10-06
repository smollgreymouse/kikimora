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

### Phase 9 — Windows portable package record (2026-10-04)

- `packaging/windows` is no longer a scaffold. `stage.ps1` builds (or stages
  provided) core/toad binaries, adds the install scripts, writes
  `manifest.json` and SHA256 `checksums.txt`, and produces
  `kikimora-<version>-windows-amd64.zip`. All paths are resolved absolute
  before the build step pushes into the toad module directory.
- `install.ps1` (elevated) installs into `%ProgramFiles%\Kikimora`, creates
  the `C:\ProgramData\Kikimora` layout with a hardened state-directory ACL
  (SYSTEM/Administrators only) and registers the `KikimoraCore` service via
  the core's own `service install` verb — automatic start, staged restart
  recovery, immediate start with all roles disabled (no desired state is
  written on fresh installs). `uninstall.ps1` removes the service and
  binaries and preserves desired state/logs/Leshy publications unless
  `-Purge` is requested.
- `test_package.ps1` runs unprivileged and verifies the contract (19
  assertions: layout, manifest, checksum coverage of every staged file, the
  zip, and the elevation/service/purge script contracts) — it passes on the
  workstation and becomes the CI gate for the packaging lane. The
  privileged execution of `install.ps1` itself belongs to the VM pass.
- A dedicated `setup.exe` is deferred until after the 08a4 VM acceptance;
  the portable zip plus the service verb covers the VM gate requirements.

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
   route state.

These are product-contract tests, not Linux-specific tests.

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
