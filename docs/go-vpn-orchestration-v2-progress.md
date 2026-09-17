# Go VPN orchestration v2 — progress checkpoint

Updated: 2026-09-17

## Current checkpoint

- User requirement: finish the previous smoke/build request, then execute `docs/go-vpn-orchestration-v2-plan.md` through its final stage.
- Do not run the real-VLESS VPS smoke; the user explicitly excluded it.
- Shared isolated-test build cache is implemented in `linux/tests/toad/run-isolated.sh`:
  - default output directory: `build/toad-smoke`;
  - override: `KIKIMORA_TOAD_BUILD_DIR=/path/to/cache`;
  - concurrent invocations are serialized with `.build.lock`/`flock`;
  - Go keeps its normal incremental package cache.
- `docs/toad-local-isolated-test-plan.md` documents the shared cache.
- `toad/internal/netstate/{snapshot,identity,compare,coalescer}.go` added with canonical underlay contracts, identity comparison and injectable coalescing.
- `toad/internal/toadctl/{protocol,framing,client,server}.go` added with per-Toad framed IPC contracts, client, server and revision subscription.
- `toad/internal/supervisor` added with process-group-aware Linux launcher, bounded stop API and per-role backoff.
- `toad/internal/core` added with role model, generation/epoch-safe completion handling, event loop, recovery/shutdown vocabulary and revisioned snapshots.
- `toad/internal/endpoint`, `routing`, `parking`, `leshy` added with compatibility policy/provider, serialized transaction, checkpoint/parking and atomic `.dev` publication primitives.
- Linux adapter package skeletons for netlink, NetworkManager and logind added behind package boundaries.
- `kikimora-toad run` now owns a long-lived `toadruntime.Runtime`, publishes a per-process generation and serves per-Toad control IPC from `<state_dir>/control.sock`.
- Optional backend capability interfaces and endpoint reporters added for AWG2, Xray and OpenConnect.
- Desktop API now exposes underlay/error/retry/validation/endpoint/recovery fields; production Linux no longer silently falls back to FakeCore.
- `kikimora-core.service`, tmpfiles/sysusers and ownership configuration templates added.
- `linux/package.sh` added for deterministic tar packaging and optional `.deb` packaging from the same Go binaries and service metadata.
- Linux netlink now watches link/address/route invalidations and selects independent IPv4/IPv6 main-table default paths; the route executor uses netlink APIs for declarative route/rule writes.
- Core snapshots now enrich with the canonical Linux underlay source and report physical-network absence explicitly; v2 core model carries the last underlay change reason/time.
- Core IPC gained v2 negotiation metadata, structured error detail and `ValidateRole` compatibility dispatch while retaining v1 wire compatibility.
- Current verification: `go test ./...`, `go test -race ./internal/control/... ./internal/core/... ./internal/toadctl/...`, and desktop `ctest --test-dir build/desktop --output-on-failure` are green.
- Latest verification: full `go test -race ./...`, `go vet ./...`, desktop rebuild + all 4 CTest targets, `bash -n` for installer/package/isolated runner, `git diff --check`, and `bash linux/package.sh` are green after the live-stream/recovery-driver changes.
- Routing/ownership hardening added: serialized routing read/write gate, netlink route/rule read-back, endpoint default fail-closed policy plus exact endpoint routes, per-prefix parking/delete operations, partial restoration, and directory fsync after atomic state/checkpoint/publication updates.
- Endpoint resolution now commits complete IPv4/IPv6 candidate sets and retains last-known-good candidates on resolver failure; mapped IPv6 answers stay excluded.
- Core role events now carry the desired enabled value, and per-role async work cancels the previous operation before accepting a newer one.
- Core/control snapshots now expose additive v2 role fields (desired/operation/validation/endpoint/publication/parking/recovery) while retaining the legacy API framing.
- Connectivity-only metadata is ignored by underlay identity; BSSID changes remain a material Wi-Fi identity event. Aggregate core state now distinguishes `PartiallyReady`, `WaitingForUnderlay`, `Recovering` and `Failed`.
- Recovery runner tests enforce the fail-closed step order and preserve the failed step/operation/epoch for reconstruction.
- `control.Manager` now feeds desired-state, underlay and revisioned Toad observations into `core.Controller`; API aggregate state is projected from the product controller while legacy state-file compatibility remains during cutover.
- Live Toad subscriptions now carry interface, route/session health and endpoint fields into the product projection; state-file polling starts only when a legacy launcher has no control socket.
- `control.Manager.refreshStates` now skips streamed roles, so live Toads have no per-snapshot state-file polling; the compatibility reader is isolated to the no-control-socket fallback.
- Toad generations are reset before every new process instance, preventing a stale snapshot from the previous process from becoming authoritative.
- Added global ownership validation for `legacy + external`, `go + external`, `go + go`; rejects unsafe `legacy + go` routing/tunnel mixing.
- Toad shutdown now handles SIGTERM, runtime construction is nil-safe, and role disconnect snapshots use a lock-safe control-socket copy.
- Core socket is now group-readable/writable (`0660`) and the installed service assigns it to the `kikimora` group after creation.
- `control.Manager.SetAutomaticRecovery` now provides a global opt-in bounded per-role restart gate using the supervisor backoff sequence; healthy observations mark the role stable.
- `control.Manager` now accepts a concrete `core.RecoveryDriver`; Linux `kikimora-core serve` wires netlink, endpoint, parking, Leshy and per-Toad control adapters. The global `--auto-recovery` gate requires an ownership file with `routing_owner=go` and `tunnel_owner=go`.
- `Leshy FileBridge.Resync` rewrites an existing publication atomically, and parking checkpoint restore verifies parked routes against the routing executor before reconstructing state.
- Parking adds kernel-derived ownership candidates and `ObserveRestorationFromKernel`, releasing a park only after a lower-metric non-endpoint route is read back. Fake routing replacements are idempotent.
- Full `go test -race ./...`, `go vet ./...`, desktop build/CTest and shell syntax/diff checks are green after the controller integration.
- N6 underlay behavior is now explicit/tested: loss invalidates enabled roles into `WaitingForUnderlay`, path changes move ready roles to `Recovering`, resume moves them to `Validating`, and healthy ready roles return `ActionNone`.
- Final local package rebuild completed after the latest Go changes; tar and `.deb` are in `dist/`.
- `control.Manager` now refreshes canonical underlay in a cancellable 250-ms invalidation loop, so revision subscribers observe veth/interface loss without polling state.json at 20 ms.
- Linux `control.Manager` underlay loop now consumes `platform/linux/netlink.Watcher` invalidations; it falls back to a 1-s audit only when subscription fails. Unsupported platforms remain explicitly unavailable.
- Qt RealCoreClient now negotiates API v2 while accepting v1 peers, uses reconnect backoff `0.5/1/2/5/10s`, preserves authoritative state during command errors, and protects revision-gap resync from intermediate command responses; client tests cover structured errors and resync.
- Last privileged check: `sudo -n -v` reports `interactive authentication is required`; resume the isolated gates from this exact point with `sudo -v` followed by `bash linux/tests/toad/run-isolated.sh core-ui-isolated` (or `core-isolated` for headless core only). This must be run by a user terminal with sudo authentication.
- The privileged gate was deliberately not retried in this continuation: no non-interactive sudo ticket is available to the agent. The real-VLESS VPS mode remains excluded by user instruction.
- `bash linux/package.sh` completed and produced `dist/kikimora-0.1.0.tar.gz` plus `.deb` on this host.
- Privileged `run-isolated.sh tun-owner` was started after the runtime integration; it reached the sudo TUN phase but had no interactive sudo ticket in this agent and was cancelled. No real-VLESS VPS test was run.
- CI Linux smoke jobs now consume one `kikimora-linux-smoke-binaries` artifact produced by `linux-smoke-build`; repeated per-job Toad/reference builds were removed while the cross-platform unit matrix remains independent.
- The shared-smoke workflow contract was checked with PyYAML, shell syntax checks, and `git diff --check`.
- Service/cutover contract smoke added at `linux/tests/toad/service-cutover.sh` and wired into the Toad workflow; it verifies simple readiness semantics, socket group/mode setup, capability bounds, ownership handoff and package/install references.
- Compatibility command endpoint providers now receive only `PATH`, `LANG` and `LC_ALL`; a regression test proves arbitrary process secrets are not inherited.
- Non-Linux default underlay/route adapters now fail explicitly with `unsupported` errors instead of silently returning empty healthy-looking state; Darwin is now the exception with native build-tagged utun/route adapters, while Windows remains explicit unsupported. Go packages cross-compile for darwin/windows with `CGO_ENABLED=0`.
- Recovery driver resource-manager initialization is now serialized, preventing concurrent underlay/recovery triggers from racing endpoint or parking manager creation; full unit and focused race suites pass.
- `run-isolated.sh` now refuses the real-VPS VLESS mode unless `KIKIMORA_ALLOW_REAL_VPS_VLESS=1` is explicitly set; normal isolated/core/UI modes never set it.
- `kikimora-core serve` now treats a validated `routing_owner=go` + `tunnel_owner=go` file as the installed service’s single global auto-recovery gate; compatibility ownership leaves recovery off, while an explicit flag is still rejected for non-Go lifecycle ownership.
- Final local verification after the service/ownership changes: full Go unit/race/vet, all 4 desktop CTest targets, every isolated-runner shell syntax check, service contract, `git diff --check`, and tar/deb packaging are green. `actionlint` is unavailable in this host; workflow structure was validated with PyYAML earlier. The final Go/package rerun after ownership normalization is also green.
- Ownership loading now normalizes omitted legacy-compatible fields (`tunnel_owner=external`, `endpoint_owner=routing_owner`) before validation; config regression coverage added.
- Re-audit found and removed an unconditional `watchState` launch in `ConnectRole`; a live control-socket Toad is now consumed only through its revisioned stream, with state-file polling reserved for the compatibility fallback in `subscribeToad`.
- Added `toad/internal/legacyconfig`: a shell-free parser for the old `vpn.conf` role/interface/provider assignments, with injection and provider validation tests.
- `kikimora-core serve` and `kikimora-toad run` now accept the same legacy `vpn.conf` plus provider directory; the systemd unit passes `/etc/kikimora/leshy/vpn.conf` and `/usr/local/libexec/kikimora/endpoint-providers`.
- Legacy static endpoint lists now accept bare IPs/hostnames and inherit the protocol transport/port; command/Happ providers use the allowlisted environment and resolve through the existing endpoint manager with last-known-good semantics.
- Current verification after legacy-config integration: `gofmt`, full `go test -race ./...`, and `go vet ./...` are green. No real-VLESS VPS test was run.
- Desktop rebuild plus all four CTest targets, every isolated-runner shell syntax check, service contract, package tar/deb build, and darwin/windows Go cross-compilation checks are green after the legacy-config integration. The first cross-check invocation was corrected to run from `toad/` (the module root).
- Final verification after the compatibility-default/empty-provider hardening: full `go test ./...`, `go vet ./...`, service contract, all isolated shell syntax checks, `git diff --check`, and tar/deb packaging are green. Qt CTest remained green from the immediately preceding source-equivalent run.
- Service contract now also asserts that the installed unit passes the shared legacy `vpn.conf` and endpoint-provider directory into both core and Toad launch paths.
- Added an explicit `endpoint.SourceHapp` constant so the compatibility path remains typed while Happ is still an external-transport bridge.
- Package N8 fix: `linux/package.sh` now ships the three legacy endpoint-provider scripts beside the core service; service contract plus tar/deb content checks verify all three files are present and executable.
- N9 platform block advanced: macOS now has build-tagged utun ownership, route-monitor/default-path underlay discovery, BSD route-manager snapshot/apply/parking adapters, and `LOCAL_PEERCRED` socket authorization; Darwin amd64/arm64 and Windows cross-builds pass. macOS runtime validation still requires a macOS host and privileged route/utun execution.
- Added launchd template `macos/files/com.kikimora.core.plist` and installation notes in `macos/README.md`; the template passes the same core/Toad legacy-config arguments as the Linux unit.
- Darwin platform parser tests compile for macOS, and final Linux verification after the N9 additions is green: full Go tests, focused race tests, vet, desktop rebuild/4 CTest targets, service contract, shell syntax and `git diff --check`.
- Added `kk orchestration status`, `kk orchestration cutover --go` and `kk orchestration rollback`: the cutover stops/disables legacy writers before atomically publishing `go/go/go` ownership, enables/starts the Go core, and restores the prior ownership plus legacy writers if core startup fails. Legacy files remain installed until the post-parity retirement step.
- Installer/package wiring now ships `orchestration.sh` in the CLI library; shell syntax, service contract and diff checks pass after this change.
- Added `linux/tests/toad/orchestration-cutover.sh`; its fake-systemd fixture covers both successful handoff and failed-core rollback without touching systemd, routes or network.
- Latest verification: full `go test ./...`, `go vet ./...`, focused Go race tests, desktop rebuild plus all 4 CTest targets, all Linux shell syntax checks, service/cutover contract, orchestration success/rollback fixture, deterministic tar/deb package contents, and `git diff --check` are green.
- The cutover command also enables `kikimora-core.service` persistently; the package was rebuilt after this change and contains the new CLI module in both tar and `.deb` outputs.
- N2.5/N6 sleep handling is now wired: Linux `logind.Source` listens for `PrepareForSleep` through a bounded `dbus-monitor` subprocess, records suspended state, refreshes canonical underlay on resume, and submits `ResumeValidation`; a hermetic signal parser test covers sleep/resume without D-Bus or VPN.
- Legacy `reconcile`, `route-lifecycle` and `route-watch` now fail closed as no-ops whenever Go owns the corresponding lifecycle, covering the `Wants=leshy.service` compatibility path and accidental manual starts.
- `run-isolated.sh build-only` now prepares all checked-out and pinned reference binaries without sudo; two consecutive runs showed all six outputs reused from the shared fingerprinted cache. Normal network namespace modes continue to use those same paths.
- Cache fingerprints are split by dependency scope: local Toad/helper and cover binaries hash project Go sources, while AWG/Xray reference binaries hash only Go toolchain plus `go.mod/go.sum`; a two-run build-only check confirms reuse after the marker migration.
- N8 retirement path is now explicit: `sudo kk orchestration retire-legacy --confirm` verifies active Go core plus inactive/disabled route-watch, removes only the legacy route writers and Leshy route-cleanup drop-in, and keeps the DNS health watcher; a Go-owned reinstall skips redeploying those removed files.
- N9 sleep parity advanced: Darwin now has a build-tagged power-log `SleepSource` and parser test; the Linux logind listener and controller resume path remain green. Darwin runtime still requires an actual macOS host.
- Resume validation is now an end-to-end core/control action: resume invalidates Ready roles, validates every live role through its Toad control socket, and commits readiness only when the response matches the current underlay epoch; stale validation responses are rejected by a core regression test.
- The resume boundary is synchronous before socket validation, preventing the queued invalidation from racing a fast response; failed validation enters the existing ordered recovery engine when the global Go-owned auto-recovery gate is enabled, otherwise it remains fail-closed as `Failed`.
- Cutover ownership now disables only `leshy-route-watch.service`; `leshy.service` and `leshy-health-watch.service` remain available as the separate DNS compatibility component required by N8.
- Latest verification after resume/cutover changes: full Go race tests, `go vet`, two consecutive `run-isolated.sh build-only` runs with reference/local binary reuse, service contract, cutover fixture, and `git diff --check` are green.
- Desktop rebuild and all four Qt tests (`real-core-client`, `real-core-e2e`, `ui-behavior`, `system-theme`) pass after the latest changes.
- Package rebuilt after the latest changes; tar/deb contents include the core systemd unit, ownership template, all three endpoint providers and the orchestration CLI.
- After the final Go resume changes, the first `build-only` run rebuilt only stale checked-out outputs while reusing pinned references; the immediately following run reused all seven cached binaries.
- N0 process ownership is now consolidated: `control.ExecLauncher` delegates to `internal/supervisor`, passes legacy compatibility arguments through the shared launcher, uses process groups/Pdeathsig and bounded SIGINT → SIGTERM → SIGKILL shutdown; a race-tested supervisor fixture verifies `Wait` is called exactly once.
- N6 recovery now consumes the capability selector: resume-only `Validating` roles use `Validate`, stale-underlay roles prefer `Rebind` then stable-TUN `RestartTransport`, and otherwise use the full Toad restart sequence; action-specific sequence and AWG2 stable-TUN selection tests were added.

## Next work

1. Run privileged `core-isolated`/`core-ui-isolated` with user-authenticated sudo; do not run the real-VLESS VPS mode. `core-ui-isolated` exercises AWG2, Xray and OpenConnect reference servers, not a real VLESS VPS.
2. Run `sudo kk orchestration status`, then (after the privileged parity gate) `sudo kk orchestration cutover --go`; legacy writers can be restored with `sudo kk orchestration rollback` until the explicit `retire-legacy --confirm` step.
3. Validate the macOS routing/utun/launchd adapters on an actual macOS host; Windows remains outside the complete stack.
4. Remove the compatibility state-file adapter and legacy route writers in a separate post-cutover change.

## Test policy

- Allowed: local Go/CMake tests and isolated namespace tests with fake/reference servers.
- Do not invoke `real-vps-vless`.
- Real VPS AWG/OpenConnect tests remain opt-in and are not required for this checkpoint.

## Plan ledger for resume

- N0–N7: implemented and covered by Go/Qt tests; live Toad subscription is authoritative and compatibility state-file reads are fallback-only.
- N8 service: implemented service/socket/ownership/package contracts and safe `go+go` gate; privileged systemd lifecycle and actual ownership cutover still require a host with authenticated root and a staged install.
- N8 legacy retirement: intentionally not deleted yet. The installer still deploys legacy writers in compatibility mode; remove them only after the privileged parity/cutover gate passes.
- N9: Linux and build-tagged macOS adapters are implemented; Darwin runtime/privileged route parity still needs an actual macOS host. Windows explicitly reports unsupported and remains outside the plan’s complete-stack scope.
- Acceptance matrix: deterministic unit/headless Qt and isolated reference-server harnesses are present; privileged namespace execution remains unverified here because sudo requires interactive authentication. Real-VPS VLESS remains prohibited.
- Resume point: run the privileged non-VLESS gates only from a terminal with an authenticated sudo ticket; then validate `sudo kk orchestration status` and perform `sudo kk orchestration cutover --go` only after parity. Do not run `real-vps-vless`.
