# 08A.3R — Linux dual-stack, DNS and route-stability regression

Status: **08A.3R REGRESSION PASSED ON DISPOSABLE UBUNTU VM (2026-10-13)**. All four slices were rerun on the current branch tip after the xhttp transport work and produced PASS evidence. This is a post-acceptance regression supplement, not a cancellation of Linux 08A.2/08A.3 PASS evidence. Work in a disposable Linux VM/netns; do not mutate the developer workstation or activate side-by-side ownership without explicit operator approval.

**Scope caveats (2026-10-14 code-vs-docs audit).** Do not over-read the PASS:
as originally run, the hardening added a netlink route/rule event monitor and
stricter error handling, but it did **not** add DNS write-counter
instrumentation (the Leshy script said so itself), and the installed-VM soak
compared only the IPv4 main-table route count (IPv6 main + endpoint table 51890
were logged, not asserted). The privileged netns gate's zero-mutation check was
also count-based. At that time "Zero route/rule events during the idle window"
and "IPv4 main-table count stable" were proven; "zero DNS writes" and "IPv6
no-churn" were not. See `docs/toad-code-docs-audit-2026-10-14.md` §3.11/§3.12.

**Since remediated in code (2026-10-14, commit `6e209bd`) — re-run required.**
The gaps above are closed in the scripts, but the new assertions have **not yet
been re-executed on a disposable VM**, so this packet's PASS banner still rests
on the pre-remediation run:

- `leshy-dns-integration.sh` now fingerprints `/etc/resolv.conf` plus the Leshy
  DNS state files and, when `inotifywait` is present, watches
  `/run/kikimora/leshy/dns` for modify/create/delete/move during the idle
  window; it fails on any DNS write or fingerprint/file-count change.
- `vm-dualstack-regression.sh` now asserts stability of **v4, v6, and both
  endpoint-table 51890 families**, not only the IPv4 main table.
- `dualstack-dns-routing-netns.sh` Test 6 now compares the **complete**
  route/rule state (all tables, v4+v6) across two identical reconciles instead
  of allowing a +2 count growth.

Parent product contract: `08-dual-stack-dns-route-stability.md`.

## Execution record — 2026-10-13, current branch tip `696f9db..5c23e93`

All four slices were rerun on the disposable Ubuntu VM (IP 192.168.1.51,
Ubuntu 26.04, kernel 7.0.0-38-generic, Go 1.26.0, branch `feat/native-core-vpn-clients`)
after the VLESS/REALITY/xhttp transport work.

### Slice 1 — deterministic Go unit/model tests (no root) — PASS

Already completed in earlier commits (`ad1e007`, `4641171`): 30+ new tests across
Windows/Linux/Darwin covering split-default refusal, family independence,
zero-mutation replay, mapped-IPv6 rejection, managed-interface exclusion,
endpoint per-family resolution, parking idempotency, netstate family readiness.

### Slice 2 — privileged hermetic Linux netns integration — PASS

Script: `linux/tests/toad/dualstack-dns-routing-netns.sh`
Go helper: `toad/internal/platform/linux/dualstack/testhelper/main.go`
Registered as Gate 2/5 in `run-privileged-gates.sh` and `dualstack` mode in `run-isolated.sh`.

8 shell-level checks + 11 Go-level tests (19 checks total; some shell checks share a fixture):
1. Dual-stack endpoint reconciliation (ReconcileEndpointPolicy)
2. No split-default routes created
3. Endpoint routes in table 51890 (verified via RouteListFiltered)
4. Endpoint rules per family (verified via netlink.RuleList)
5. Idempotent reconcile zero-mutation
6. Parking both families
7. Parking silently skips split-default prefixes
8. Parking idempotent on replay
9. Remove endpoint policy cleans up
10. External VPN does not capture endpoint traffic
11. Fail-closed IPv6 unreachable prompt TCP failure
12. Endpoint path pinned to underlay (ip route get)
13. No split-defaults in main table
14. IPv6 prompt failure via Python TCP probe (0.000s, ENETUNREACH)
15. Unrelated routes untouched
16. Zero-mutation on identical reconcile (route count stable)
17. Underlay loss/return family separation
18. IPv6 independence from healthy IPv4

**Bug found and fixed:** `snapshotKernelUnlocked()` used `RouteList(nil, family)`
which only returns main-table (254) routes. The vishvananda/netlink v1.3.1
library skips non-main tables when `RT_FILTER_TABLE` is not set. This caused
`endpointPolicyPlan` and `RemoveEndpointPolicy` to never see table 51890
routes, breaking garbage collection and removal. Fixed by switching to
`RouteListFiltered(family, &Route{Table: RT_TABLE_UNSPEC}, RT_FILTER_TABLE)`.

### Slice 3 — combined Leshy/DNS integration — PASS

Script: `linux/tests/toad/leshy-dns-integration.sh`

8 tests against installed Kikimora + Leshy:
1. A/AAAA classification and per-family route verification
2. DNS ownership determinism (Leshy, NM, systemd-resolved)
3. Zero-mutation on stable DNS state (v4=8 v6=6 ep-v4=3 ep-v6=1)
4. DNS resolution after core reconcile
5. leshy-health-watch.service status (not installed, recorded)
6. Core diagnostics from snapshot (0 active parking, 2 ready endpoints)
7. No split-default routes
8. Family readiness independence (underlay v4=True v6=True)

### Slice 4 — installed VM regression acceptance — PASS

Script: `linux/tests/toad/vm-dualstack-regression.sh`

Evidence captured in `/tmp/vm-dualstack-regression-*`:
1. Before/after route, rule, DNS, endpoint-path capture
2. A/AAAA resolution and per-family curl -4/-6 probes
3. Real AWG/OC application probe capture
4. Zero-mutation soak (3 iterations, 2s apart) — stable **for the IPv4 main
   table only**; IPv6 main and endpoint table 51890 are captured but not
   asserted (see scope caveats above)
5. Core restart recovery — core state Ready after restart
6. No split-default routes accumulated

## Purpose and actual gap

The accepted Linux VM evidence establishes installed AWG/OpenConnect transport, service/recovery and packaging, but does not prove the absence of legacy-host-like blackholes and mutation loops when IPv4 works, IPv6 does not, DNS serves AAAA, and Leshy keeps reconciling. The Go route manager and the remaining legacy Leshy DNS/domain route-owner must both be covered. A stable Go table alone is not evidence that the combined product is quiet.

## Slice 1 — deterministic Go unit/model tests (no root)

Extend or add tests alongside `toad/internal/platform/linux/netlink/{routes,snapshot}_test.go`, `toad/internal/endpoint/`, `toad/internal/parking/` and orchestration tests as warranted by the actual ownership path. Do not force DNS policy tests into Go modules that do not own DNS.

Acceptance cases:

1. IPv4 readiness cannot imply IPv6 readiness; IPv6-only, IPv4-only, both, neither and underlay transition are distinct cases.
2. A selected role must not accidentally add `::/1` + `8000::/1` (or IPv4 `0.0.0.0/1` + `128.0.0.0/1`), including protocol backend startup and recovery. Intentional full tunnel would require a separate family-specific capability and data-plane gate.
3. Endpoint exceptions resolve through the physical underlay, never through their own Toad or an externally owned VPN. Source selection must test global/ULA/link-local, IPv4-mapped and missing-address cases. Specifically, do not mistake a syntactically valid ULA for a publicly routable Internet source.
4. Unavailable selected family: a valid fast-fail reject/unreachable or controlled family suppression; no silent blackhole. Verify independently of Happy Eyeballs.
5. Instrument the actual route/rule/DNS mutation boundary or a recording adapter. Fixed snapshots + classifications => initial convergence then **zero attempted kernel mutations** on subsequent identical reconciles. A one-input change mutates only affected state, and re-converges. Testing identical final route tables alone is insufficient.
6. Verify endpoint-rule deduplication, parking deduplication, no unintended ownership transfer, and no perpetual recovery wakeups under stable conditions.

Keep the tests deterministic. Avoid timing-based claims of no churn when a call-count/operation log can prove it.

## Slice 2 — privileged hermetic Linux netns integration


## Execution record - 2026-10-16, branch tip `d3211b2` (hardened gates)

All four slices were re-run on the disposable Ubuntu VM (192.168.1.51, Ubuntu
26.04.1 LTS, kernel `7.0.0-38-generic`, Go 1.26.0, Qt 6.10.2, cmake 4.2.3,
ninja 1.13.2, branch `feat/native-core-vpn-clients` at `d3211b2`). This is the
**first execution of the hardened scripts** committed in `6e209bd` (Leshy DNS
write monitoring, IPv6 + endpoint-table soak assertions). Result: **all four
slices PASS**, with the product defects listed under "Findings".

### Slice 1 - deterministic Go model tests - PASS

- `linux/tests/toad/run-rootless.sh model` -> `model suite: PASS`.
- Extra uncached evidence: `toad/go test ./... -count=1` -> exit 0.

### Slice 2 - privileged hermetic Linux netns gate - PASS

- `linux/tests/toad/run-isolated.sh dualstack` (root, after the ordinary-user
  prebuild), log `/tmp/08a3r-slice2.log`, 25 PASS assertions, including
  `full route/rule state identical across two identical reconciles (v4=6 v6=6 ep-v4=2 ep-v6=2)`,
  `IPv6 connection attempt fails promptly (not blackhole timeout)` and
  `IPv6 independence - no false capture when IPv4 is healthy`.

### Slice 3 - combined Leshy/DNS integration (hardened) - PASS

- `linux/tests/toad/run-isolated.sh leshy-dns-integration`; the new
  `inotifywait` DNS-state monitor is active, and Test 3 asserts
  `no observed route/rule events, stable route counts, and no DNS-state writes [fingerprint] (v4=6 v6=6 ep-v4=3 ep-v6=1 dns-files=2)`.
- The first attempt legitimately failed at Test 2 (`leshy-dns0 interface
  missing`) because the VM had booted without the Leshy DNS integration - see
  Finding 1. After `leshy-dns enable` the whole slice passes.
- `leshy-health-watch.service` is absent (recorded, unchanged).
- **Closed 2026-10-16 (`e4594c9`).** The gate is now a zero-mutation
  proof instead of an observational one:
  `leshy-dns` records every mutation of the system DNS surface (resolver switch,
  repair, teardown, intent refresh) in `/run/kikimora/leshy/dns-writes.log`, and
  Test 3 no longer idles: it replays the same classification actively (the same
  domains through Leshy, plus `kk status --json` and `leshy-dns check`) and
  requires that the counter delta, the DNS-state fingerprint/file count, the
  netlink route/rule monitor and every table counter stay at zero. Replay rounds
  whose answer legitimately changed are reported as changed inputs and excluded
  from the identical-input count, so the claim is neither vacuous nor
  timing-based. Verified on the VM: `rounds=3 identical=6/6 helper-writes=0`,
  `PASS: unchanged-classification replay produced zero mutations
  [helper-counter+fingerprint]`, and the gate now prints
  `LESHY/DNS ZERO-MUTATION GATE PASSED`.

### Slice 4 - installed VM regression acceptance (hardened) - PASS

- Real role probes were built from the repository's own probe shape
  (`~/kikimora-lab/08a3r/probes/role-probe.sh`): the probe *requires* the
  effective route to the target to traverse the role interface, then curls the
  resolved IP with `--resolve` and checks the HTTP code and `remote_ip`.
- Before **and** after the core restart:
  `awg_telegram=http_code=200 size_download=19919 remote_ip=149.154.167.99 via=kk-awg0`
  and
  `oc_internal_gitlab=http_code=302 size_download=109 remote_ip=172.25.41.169 via=kk-oc0`.
- Zero-mutation soak now covers both families and both endpoint tables:
  3 iterations, `v4=8 v6=6 ep-v4=3 ep-v6=1` unchanged.
- Core restart recovery: `core_state=Ready`. No split-default routes accumulated.
- Evidence directory `/tmp/vm-dualstack-regression-10707`.

### Desktop UI (chapter 09) - PASS

- Fresh configure/build: `cmake -S desktop -B build/desktop-08a3r` +
  `cmake --build build/desktop-08a3r -j` with Qt 6.10.2 -> build rc=0 (only the
  distro's `qquicklayoutsplugin` import warning).
- `ctest`: **5/5 registered targets pass** (`real-core-client`, `real-core-e2e`,
  `ui-behavior`, `system-theme`, `role-list-model`); `real-core-ui` stays
  `Not Run (Disabled)` by design.
- With `KIKIMORA_CORE_BINARY`/`KIKIMORA_FAKE_TOAD_BINARY` set, `real-core-e2e`
  runs for real instead of silently skipping: `3 passed, 0 failed, 0 skipped`.
- `real-core-ui` was executed **for the first time** against a live core
  (`kikimora-core serve --socket ... --toad-binary <fake-toad>` with three fake
  roles): `3 passed, 0 failed, 0 skipped`. The 2026-10-14 audit row "built but
  never registered / never ran" is now closed with evidence.

### Findings (product defects found by this run)

1. **[HIGH] The Leshy DNS integration never enables itself on boot.**
   `/usr/lib/systemd/system/leshy.service.d/kikimora-dns-hooks.conf` runs
   `ExecStartPost=-/usr/local/sbin/leshy-dns resume` and
   `ExecStartPost=-/bin/sh -c '/usr/local/sbin/leshy-dns check || /usr/local/sbin/leshy-dns enable'`.
   At that moment systemd has not yet marked `leshy.service` active, so both
   paths trip and the leading `-` swallows the failure (journal 2026-10-16
   09:30:37, identical on the 2026-10-04 and 2026-10-06 boots):
   ```text
   leshy-dns: error: Leshy responded, but upstream DNS returned an error
   leshy-dns: error: leshy.service is not active
   ```
   Effect: after every cold boot `leshy-dns0` and the resolver switch to
   `127.0.0.1:53053` are missing until `leshy-dns enable` (or `kk start`) runs
   manually, i.e. the installed product silently loses its DNS/split-tunnel
   ownership after a reboot. Slice 3 Test 2 correctly refuses to pass in that
   state. Suggested fix: move the integration into a separate oneshot unit
   ordered `After=leshy.service` (or make `leshy-dns` retry while the unit is
   activating) and drop the `-` prefix so a real failure is visible.

   **FIXED 2026-10-16 (`45484f1`, packaging companion `23a6482` + `d6dda73` on
   `fix/package-leshy-runtime`).** The integration now owns
   `kikimora-leshy-dns.service`: a oneshot unit ordered `After=leshy.service`,
   following its restart/stop (`PartOf=`) and pulled into the boot transaction
   by `kikimora-core.service` (`Wants=leshy.service kikimora-leshy-dns.service`),
   so no operator `systemctl enable` step is required. It restores the
   integration only when the operator enabled it before (no saved intent means
   no DNS change, matching the installer's documented promise), retries the
   transient startup window, and a real failure now lands in the unit state
   instead of being hidden by a `-` prefix. The route-cleanup drop-in keeps only its legacy
   route-watch hooks, `install.sh` installs the unit and verifies the dependency
   (and the installer contract test asserts both), the CLI uninstall path
   removes it, and the `.deb` staging ships the unit while the Leshy drop-in
   keeps only the core ordering line (`linux/tests/test_release_packaging.sh`
   asserts the new contract).

   Verified on the disposable Ubuntu VM: the unit was left **disabled** on
   purpose, the integration was suspended to reproduce the post-boot state, and
   the VM was rebooted twice. Both times `kikimora-leshy-dns.service` started
   through the core's `Wants=` chain, restored `leshy-dns0` (`192.0.2.1/32`) and
   the `127.0.0.1:53053` resolver switch with no manual step, and the hardened
   Slice 3 gate passed on that boot (`PASS: leshy-dns0 interface exists`,
   `PASS: NM reports leshy-dns0 as externally managed`). The packaging fix was
   additionally validated by building the real `.deb` on the VM:
   `chapter-08 Linux console package: PASS`
   (sha256 `89e06bdbe942cb340f3d5d05eb0aea617ac489ece0435684d11be1f634cbdde1`).
2. **[MEDIUM] OpenConnect recovery does not survive a rejected session cookie.**
   `openconnect` logged `Cookie was rejected by server; exiting.`; the role then
   sat in `Recovering`/`default-interface-changed` with `kk-oc0` absent and
   `kk retry --role oc` did not move it for three minutes. Only
   `kk disconnect --role oc` followed by `kk connect --role oc` restored
   `Ready` (`kk-oc0 10.0.0.139/24`).

   **FIXED 2026-10-16 (`a27b688`).** The stall was not protocol-specific: a Toad
   that ends its session on its own left the role enabled and desired with no
   process, no error text and no scheduled restart - a state every repair path
   ignored, because `recoverStaleRoles()` requires a live process,
   `retryPendingStarts()` requires `startPending`, and `RetryRole()` returned
   early when `lastError` was empty. That last guard is why the operator's
   `kk retry --role oc` was a silent no-op. Three changes close the paths:
   `wait()` re-arms the bounded restart supervisor even for a status-0 exit,
   `restartRole()` launches a replacement process when none is registered
   instead of failing the recovery transaction with `Toad "oc" is not running`,
   and `RetryRole()` always repairs a desired role that has no process. Model
   tests cover all three and fail before the change.

   Live verification on the disposable Ubuntu VM: with the previously installed
   core, one `SIGTERM` to the OpenConnect Toad left the role `Stopped` for a
   minute with no restart and with `kk retry --role oc` changing nothing (the
   role was already degraded in `Recovering`/`underlay-availability-changed`
   with no managed interface before the kill). The same binary rebuilt with the
   fix repaired that stuck role to `Ready` on startup and, after the identical
   `SIGTERM`, took the role through `Starting` to `Ready` automatically in about
   35 seconds with a new pid and a restored `kk-oc0`.

Add a dedicated case to the existing `linux/tests/toad/` privileged runner (suggested name `dualstack-dns-routing-netns.sh`) and register it in the appropriate existing test gate; do not create an unrelated runner.

Construct controlled fake uplink(s), managed Toad, optional externally owned `vpn0`, and a dual-stack DNS fixture. Verify:

- working IPv4 and deliberately unroutable IPv6 with an AAAA reply; a non-Happy-Eyeballs application/connect probe must fail promptly, not after a multi-second TCP timeout;
- disabled/unsupported IPv6 does not create an accidental managed split default and cannot be published Ready because IPv4 is healthy;
- `ip -4 route get`, `ip -6 route get`, rules and effective source prove the managed endpoint is pinned to the physical underlay, including when an external VPN has a lower-metric default;
- fail-closed IPv6 unreachable produces prompt failure while leaving unrelated physical and corporate routes untouched;
- after repeated identical reconcile, netlink mutation instrumentation reports zero calls and route/rule state has not grown;
- underlay loss/return, endpoint address-family changes, and selected-role recovery preserve family separation and idempotent cleanup.

Use explicit namespace-local fixtures. No public services, workstation mutations or operator credentials in hermetic tests.

## Slice 3 — combined Leshy/DNS integration

Inspect the actual Linux ownership boundary before coding: Leshy still owns DNS/domain classification and its legacy compatibility watcher may remain active. Add integration assertions for the **combined** Leshy + Go runtime, rather than assuming Go route-manager stability covers Leshy.

- Verify A/AAAA classification and effective route/deny per family; a returned AAAA with dead selected IPv6 may not create an application timeout through a managed blackhole.
- Assert deterministic ownership across Leshy, NetworkManager and systemd-resolved (system/default, split DNS, per-interface scopes).
- With unchanged DNS answers and classification, repeated refresh/health checks cause no route/rule/DNS rewrites or re-addition of the same `/128` destinations.
- Exercise DNS owner change, service restart, underlay loss/resume, and removal/recreation of owned resources; no competing writers, endless repair, or stale DNS scopes.
- Include `leshy-health-watch.service` when installed; separately record whether it is present, enabled and invoked. No unexplained periodic DNS repair.
- Record counters or bounded instrumented mutation events for reconcile wakeups, zero-diff passes, route/rule add/replace/delete, DNS apply/repair, underlay epoch changes, and owned/parked route counts per family.

If the production stack cannot expose those diagnostics, make the minimum observable instrumentation a dependency; do not treat a quiet log as proof.

## Slice 4 — installed Linux VM regression acceptance

Run on disposable Ubuntu VM against the actual candidate installed package and installed Leshy/DNS integration, not arbitrary source-tree binaries. Preserve prior 08A.2/08A.3 PASS records. A packaging lifecycle rerun is necessary only if the fix alters packaging/install contract; relevant functionality and recovery tests must be rerun after fixes.

Capture before/after and post-recovery:

1. `ip -4/-6 route`, `ip -4/-6 rule`, `ip -4/-6 route get <endpoint-or-resolved-address>` and source; managed `/1` and `/128` counts, owner attribution;
2. `resolvectl status`, NetworkManager DNS state, Leshy configuration and watcher status;
3. A/AAAA of a controlled dual-stack fixture, `curl -4/-6` or equivalent and a non-Happy-Eyeballs TCP probe with bounded failure timing;
4. real AWG + OpenConnect application probes and independent per-family Ready/unavailable evidence;
5. unchanged-state repetitive reconcile instrumentation, plus bounded soak and route/rule/DNS mutation counts;
6. recovery after interface link/DHCP change, core/Toad death and at least one real suspend/resume, repeating family, source and DNS checks.

The VM may have no public IPv6. In that case prove **no false IPv6 capture and prompt failure**; do not report IPv6 transport PASS. A separate real IPv6 path is needed before claiming IPv6 traffic works.

## Exit / handoff

- All four slices have evidence, with identified command/test logs and exact tested package/build revision.
- Every family-specific path is either operational, deterministically suppressed by Kikimora-owned policy, or fails promptly; no dead managed tunnel hijacks AAAA traffic.
- Repeat desired-state inputs produce zero kernel/DNS mutation after convergence, including Leshy route classification; counters distinguish unchanged wakeups from actual writes.
- No unauthorized mutation of external VPN or workstation state.
- Only after this packet passes may deferred `08A.1` workstation staging/cutover seek *separate* explicit operator approval; `08B` observation/retirement still remains separate.

Do not call Linux fully deployment-accepted before this regression is closed.
