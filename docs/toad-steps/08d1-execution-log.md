# 08D1 execution log

Executor log for `08d1-managed-selected-routes-completion.md` (the authoritative
completion packet). One handoff record per stage, per packet §34. This file is
updated as stages land; packet statuses stay authoritative in the packet itself.

Companion revision: `.github/managed-routing-companion.env` pins `LESHY_REF`.
Lab VMs for vertical stages: `docs/lab-vms.md` (note: guest IPs follow the
workstation Wi-Fi subnet; on 2026-11-05 the lab moved 192.168.1.x -> 192.168.3.x,
ubuntu=.53, win=.54).

---

## Stage 0 — freeze contracts, baselines and red tests

Stage: 0
Kikimora SHA: 378179d094933abd811e0074d1f8f2e101281ec2
Leshy SHA: 84e6e9ab9bab82e49cdba85c344b30c410e710b9 (fresh clone = packet baseline)
Implemented packet sections: §32 Stage 0 work items 1-6.
Wire capability/version: unchanged (managed_selected_routes_v1).
Persistent schema: unchanged (selected-routes.json schema 1, marker schema 1).

Work landed:

- Recorded both baselines here (above) and pinned `LESHY_REF` in
  `.github/managed-routing-companion.env` (§31.18). The cross-repo CI job that
  consumes this ref lands with the first cross-repo job (§24.1) and reads the
  env file from day one; no moving master is ever referenced.
- CI matrices fail-fast=false: Kikimora `toad.yml` go-unit already had it;
  Leshy `ci.yml` `test` matrix now `fail-fast: false` (§24.2).
- Named test skeletons, explicitly skipped with the owning stage in the skip
  message (§32 Stage 0 item 4):
  - Kikimora `toad/internal/routing/selected_schema2_skeleton_test.go` — schema2
    model/planner skeletons (K11).
  - Kikimora `toad/internal/control/managed_selected_stage_skeletons_test.go` —
    mode-journal crashes (1D/6.2), writer fence (1B), privileged auth (1C),
    core StateDir singleton (1B), production recovery selector (K14),
    observation-sequence race (3B), ServeWithReady barrier (1B).
  - Kikimora `toad/internal/platform/windows/routes/selected_exact_replacement_skeleton_test.go`
    — Windows exact replacement + park identity + effective metric (K15/3A).
  - Leshy: Linux IPv6 event restore skeleton added in
    `tests/linux_route_registry_netns.rs` (L11) as an `#[ignore]`d test.
- Baseline counters recorded (§32 Stage 0 item 5):
  - Kikimora unit soak (`TestSoakTenThousandDuplicateEnsuresStayFlat`): 100
    unique /32 intents converge with exactly 100 kernel mutations; 10,000
    duplicate classifications afterwards produce 0 additional mutations,
    DuplicateHits=10000, registry bounded at 100. Fast path is zero-I/O.
  - Leshy baseline: `cargo test --all-targets --all-features` green on
    windows host (3 suites in tail; full suite exit 0); counters for the
    request-path FIB-dump problem are captured by the L11 netns tests
    (`full_snapshots`/`targeted_reads`) which do not exist yet — the *absence*
    of those counters is itself recorded as the known baseline deficit to be
    closed by L11.
- v1 runtime behavior unchanged: Stage 0 contains no production code changes.

Gates run:

- Kikimora: `go test ./...` green (baseline and after skeletons), `gofmt -l`
  clean, `go vet ./...` clean.
- Leshy: `cargo test --all-targets --all-features` green, `cargo fmt --check`
  clean.

Native OS gates run: windows host (this workstation) for both repos.
Privileged gates run: none (Stage 0 requires none).
Crash failpoints run: none (failpoint seams land in Stage 2/7).
Known skipped gate + reason: cross-repo CI job does not exist yet — its
scaffolding (env-pinned checkout) lands with the first cross-repo job; the
packet allows this because no cross-repo evidence was claimed in Stage 0.
Foreign-row proof: n/a (no behavior change).
No-churn counters: recorded above as baseline.
Next-stage entry criteria satisfied: yes — baseline failures none, test
locations fixed, no behavior change hidden in setup.

---

## Stage 1 — durability, fencing and security foundation

Stage: 1 (1A + 1B + 1C + 1D)
Kikimora SHA: 725d2f1 (stack: 2eadad4 1A, 7da0d55 1B, 039d7d2 1C, 725d2f1 1D)
Leshy SHA: 807a633 (stack: 569d81d 1B, 807a633 1C)
Implemented packet sections: §31.1/§31.23 (journal plumbing), §31.2/§31.15 (OS
fences), §31.3/§31.22 (readiness barrier + packaged dependency), §31.4
(ActivationID/ModeGeneration types + counter), §31.6 (privileged auth), §31.7
(durable file primitive).
Wire capability/version: unchanged (managed_selected_routes_v1; v2 constants
land with schema2 activation).
Persistent schema: selected-routes.json schema 1 unchanged; marker schema 1
unchanged (schema2 write flips with Stage 6.2 per §31.5 rollout order).

Work landed:

- 1A: `fsutil.WriteAtomicFile/WriteAtomicJSON/RemoveDurable` — POSIX
  rename+SyncDir, Windows `MoveFileExW(REPLACE_EXISTING|WRITE_THROUGH)`; the
  three duplicated hand-rolled writers (selected checkpoint, parking
  checkpoint, control marker) now delegate to the shared primitive.
- 1B: `internal/lockfile` OS lifetime lock (flock / LockFileEx,
  FAIL_IMMEDIATELY); core acquires `<StateDir>/core.lock` before any state
  read (dev cores without a state dir stay unlocked by contract); Leshy
  `WriterLease` + `writer_lock` generated-config key (platform default path
  written by the core generator, deterministic for §31.23 digests);
  `control.ServeWithReady` (ready fires exactly once after bind/protect,
  `Serve` is a wrapper), `startup_ready` + `mode_transition:"none"` snapshot
  fields, `kikimora-core wait-startup --socket --timeout`, Windows service
  Running only after the ready callback (30s startup budget, timeout cancels
  runServe), `leshy.service` gained
  `Requires/After=kikimora-core.service` + `ExecStartPre wait-startup`;
  Leshy Windows service install gained `--depend-on <service>`.
- 1C: `control-route-auth.key` (32 random bytes, 64 lowercase hex + newline
  on disk, mode 0600 / SYSTEM+Administrators protected DACL, durable atomic
  creation, valid key reused, local installs regenerate malformed keys,
  managed installs fail closed on missing/malformed); `Request.privileged_auth`;
  Handle middleware denies wrong tokens with non-retryable
  `privileged_auth_required` before intent parsing (zero mutation); empty
  token rides the isolated, counted legacy v1 branch until schema2 activation
  (Stage 6.2) per §31.6 rollout; Leshy `kikimora_auth_file` config, fail-closed
  credential load (64 hex + newline), token travels on Ensure/Forget/Flush.
- 1D: `ManagedLeshyTransition` journal types + strict §31.23 validation
  (ID/From/To/Phase/generations/activation/socket/zones/digest), durable
  journal write/read/remove, `leshy-mode-generation.json` counter (missing =
  0), `planTransitionRecovery` roll-forward/verify-clean/stale-fails-closed
  decision, activation fingerprint (12 hex of SHA-256) and deterministic
  config digest helpers. No production Enable/Disable switchover yet.

Gates run:

- Kikimora: `go test -count=1 ./...` 25 suites green, `gofmt -l` clean,
  `go vet` clean.
- Leshy: `cargo test` 8 suites green, `cargo fmt --check` clean, clippy
  `-D warnings` clean.

Native OS gates run: windows host natively for both repos (fsutil
replace-existing contract, LockFileEx contention, DACL denial for unelevated
tokens, SC tests); POSIX branches compile for CI.
Privileged gates run: none required this stage.
Crash failpoints run: journal boundary crash tests are Stage 6.2 (failpoint
seams land in Stage 2/7); Stage 1D proves the type/decision layer.
Known skipped gate + reason: "privileged call without auth has zero mutation"
is fully enforced only under a schema2 marker (Stage 6.2); pre-schema2 the
empty-token legacy branch is isolated and counted, wrong tokens are always
denied. Package dependency contract tests for the unit graph land with the
packaging stages.
Foreign-row proof: n/a (no route-mutation surface changed).
No-churn counters: unchanged from Stage 0 baseline.
Next-stage entry criteria satisfied: yes — authority holes closed before
schema2 depends on them (§32 Stage 1 no-go respected: no schema2 restore, no
v2 activation).

---

## Stage 2 progress — Kikimora exact transaction core

### K11 — exact route + park transaction model (landed)

Commit: 8d4d1e1 `feat(routing): persist exact selected and park transactions (08D1 K11)`.

Landed per §1-§3 + addendum:

- `routing.SelectedRouteIdentity` / `SelectedParkIdentity` with exact
  validation, `InterfaceLuid` in both identities (§31.19), `Equal` is exact
  field equality.
- Shared `ManagedSelectedRouteTable`/`ManagedParkMetric` in routing; NO
  cross-platform protocol constant (§31.26): Linux `RTPROT_STATIC`(4) lives
  in the netlink writer, Windows value stays with the writer until stage 3A
  flips it to NetMgmt(3).
- Two independent state machines: selected pending/applying/active/retiring
  and park pending/applying/active/retiring with `RetireDesired` semantics
  exactly per §1.2/§1.4; invalid transitions are errors, never coerced.
- Checkpoint schema 2 (`SelectedRoutesCheckpointSchema` = 2) with
  `ActivationID`/`ModeGeneration` (§31.4) and `Platform` (§31.26); schema 1
  decodes only to an explicit unsupported error; strict
  `ValidateSelectedRoutesCheckpoint` (§2.2); checkpoint writes go through
  the shared durable primitive.
- Exact planner (§3) with `SelectedKernelRoute/Park/Foreign` projection,
  state-dependent park-row recognition, ordered conflict rules, and no
  protocol-class ownership anywhere (`selectedRowProtocolOwned` deleted).
- Platform ripple to keep the tree compiling: `NormalizeSelectedRoute`
  entered the RouteManager contract early (it is required to build any
  identity; the rest of §4 remains K12), Linux/Windows selected writers
  project exact identities with foreign rows never dropped and state-aware
  `RemoveSelectedRoute`; parking managed helpers mechanically adapted (they
  move into SelectedRouteManager and die in K14).
- Registry parks are now recorded through the independent park transaction
  (provisional `MarkParked` per-prefix Pending→Applying→Apply→Active with a
  checkpoint after each transition; full §7.2 resume semantics land in K14).
- Ensure/Restore bracket every mutation with Reserve→Pending checkpoint →
  MarkApplying→Applying checkpoint → reconcile → MarkActive→Active
  checkpoint, and carry the real `RoleRuntime.ToadGeneration` (§5.3).
- Startup route generation seeds at 1 (§2.4 step 7 minimum).

Gates: `go test -count=1 ./...` 25 suites green; `go test -race` green for
control+routing; gofmt/vet clean; linux/darwin/windows cross-builds green.

K12 next: full §4 platform contract (Preflight, Reconcile results, park
methods, SnapshotSelectedRoutes, SubscribeSelectedRouteInvalidations),
fakes/Darwin unsupported, Linux netns to schema2.

### K12 — full platform contract + Linux exact writer (landed)

Commit: 88c0468 `feat(platform): full selected/park platform contract with
exact identities (08D1 K12)`; bcba51f wires the preflight into the Ensure
transaction.

- RouteManager now carries the complete §4 surface: Normalize/Preflight/
  Reconcile(with SelectedReconcileResult{Mutated,Verified})/Remove for
  routes, Normalize/Preflight/Reconcile/Observe/Remove for managed parks,
  SnapshotSelectedRoutes and SubscribeSelectedRouteInvalidations; both
  ErrSelectedRouteUnsupported and ErrSelectedRouteObserverUnsupported live
  in platform.
- Linux (§8): RTN_UNREACHABLE projection to exact park identities, foreign
  rows never dropped, state-aware RemoveSelectedRoute, park reconcile that
  proves Desired before cleaning stale Owned, one live full-family snapshot
  grouped by canonical prefix for the audit, and netlink route+link
  subscriptions classified onto the invalidation model. The invalidation
  and reconcile-result types live in routing (platform aliases them) to
  keep the import graph acyclic.
- Windows (§9 shape, full §9 matrix is stage 3A): identity projection with
  loopback park recognition, state-aware removal honoring the persisted
  ifindex (rowForOperation no longer substitutes the current loopback for
  persisted identities), winipcfg route-change subscription emitting
  invalidation material only.
- darwin/unsupported return the typed unsupported errors; the latent
  unqualified SelectedRouteDesired debt in default_routes_unsupported.go is
  gone (fully qualified routing types).
- Test fakes implement the entire surface explicitly (no embedded no-ops).

### K13 — crash-safe SelectedRouteManager (landed)

Commit: 2adc842 `feat(control): crash-safe selected ownership ... (08D1 K13)`.

- §22.1/§22.2 failpoint seams: the `selectedFailpoint` unit hook plus the
  build-tag process seam (`KK_SELECTED_FAILPOINT*` exits 86) at the stable
  boundary names route.after_*; park.* boundaries attach with K14.
- §5.2 verification tokens: per-role map keyed by prefix carrying
  ToadGeneration/RouteGeneration/Identity; the zero-I/O fast path requires
  an Active record with Owned==Desired plus a current token; tokens are
  minted only after the durable Active checkpoint; restore mints none;
  ClearVerification drops them for invalidations/recovery.
- §5.1 wire state: converged duplicates now answer already-applied.
- §31.20 liveness: every Ensure/Forget/FlushZone runs under a 4s in-core
  deadline; Freeze is context-aware through the inFlightZero channel
  (0->1 opens, 0 closes exactly once); a timed-out Freeze stays frozen.
- §2.3/§2.4 strict startup: readManagedMarker fails closed on malformed
  JSON/unsupported schema/empty socket/unknown or duplicate zones; the
  constructor applies the marker, restores marker-bound schema-2
  checkpoints all-or-nothing (platform-fenced per §31.26), seeds
  generation=checkpoint+1, freezes non-empty ownership with
  restorePending and restores no token.
- §2.5 CompleteStartupRestore: clears restorePending/frozen only when every
  non-Retiring record is Active with a current token, Park==nil and no
  Retiring record remains (reconciler-owned gate; the reconciler lands in
  K16).
- MarkApplying is idempotent from Applying so a durable Applying record
  resumes its authorized transaction; the restart-adoption crash matrix
  row (route.after_kernel_reconcile) is unit-proven.

Gates: 25 Go suites green; linux/darwin/windows builds green.

### K14 — managed parking inside SelectedRouteManager + reachable recovery (landed)

Commit: 9c77ab0 `fix(recovery): make managed parking transactional and
reachable (08D1 K14)`.

- §7.1/§7.2 `ParkRegistered`: the full per-state resume machine (nil-Park /
  Pending with identity-change forcePending / Applying without a second
  preflight / Active zero-mutation exact observation / Retiring completion
  before re-protection), frozen-role precondition, applyMu held for the
  whole operation, checkpoint after every state/identity change, park.*
  failpoints at every durable boundary.
- §5.7/§7.3 `WithdrawSelected`: validates every record's exact ParkActive
  Owned==Desired and observes every persisted park BEFORE deleting any
  selected row; removes only exact selected identities; re-verifies every
  park afterwards; failure stops recovery before Withdraw/Quiesce.
- §7.5 `ObserveRestoration`: Pending/Applying parks keep the role frozen
  (ErrRoutesStillParked, never cleared); ParkActive retirement runs the
  seven-condition proof (exact selected identity, exact persisted park, no
  foreign row), MarkParkRetiring + checkpoint before the unpark syscall,
  exact selected-remains/park-absent verification, then ClearPark;
  ParkRetiring resumes exactly. ErrRoutesStillParked is derived from the
  final registry state.
- §7.1 `ParkingState` derives proven protection only (ParkActive/Retiring
  with exact Owned); the recovery driver publishes it and never consults
  parking.Manager on the managed path.
- §7.3/§11 `RecoveryWithdrawSelected` ("withdraw-selected") added between
  Park and Withdraw in the managed sequence; core RecoveryDriver gains
  WithdrawSelected; sequence ordering Park < WithdrawSelected < Withdraw <
  Quiesce holds.
- §0.8 CLOSED: the one production recoverRole engine now carries
  `ManagedSelectedRoutes: m.managedSelected` — the managed sequence is
  reachable from production recovery, not only core tests.
- §10.1 `requestManagedSelectedRecovery` lands (dedupes via
  recoveryInFlight, leaves restorePending/frozen untouched when
  prerequisites are missing) — the reconciler (K16) starts calling it.
- §7 managed helpers deleted from parking.Manager
  (PrepareRegisteredWithdrawal/registeredRowVerified/RegisteredRestoration)
  together with their tests: managed parking has exactly one authority —
  SelectedRouteManager/schema2. Legacy baseline/delta parking stays for
  legacy-file installs.

Gates: 25 Go suites green; race clean for control; three-OS builds green.

### Stage 2.5 — Linux privileged selected netns to schema2 (landed)

The privileged netns acceptance (`linux/tests/toad/selected-routes-netns.sh`
driving the prebuilt `netlink.test` inside a disposable namespace with a
dual-stack dummy device) now runs the 08D1 pipeline against the real kernel:

- schema-2 Reserve with `NormalizeSelectedRoute` identities (exact table,
  ifindex, RTPROT_STATIC, IPv6 metric normalization, scope);
- first apply mutates and verifies the exact identity read-back
  (`proto static`, v6 `metric 1024`);
- 50 converged replays leave the kernel table byte-stable;
- external /32 delete restored by exact reconcile;
- foreign row conflicts and survives both the refused ensure and
  RemoveSelectedRoute;
- independent park transaction: ReservePark → MarkParkApplying →
  ReconcileManagedPark (exact Verified) → MarkParkActive, exact
  ObserveManagedPark, wrong-identity observation refuses, MarkParkRetiring →
  RemoveManagedPark → ClearPark completes the transaction;
- schema-2 checkpoint roundtrip with all-or-nothing RestoreRole and the
  checkpoint+1 startup seeding.

Platform notes locked by the run: the IPv6 kernel carries no route scope
attribute, so Linux identities normalize v6 scope to 0 (read-back truth);
`ip route` prints host routes without the /32//128 suffix.

Exit gate: PASS on the ubuntu2604 VM (2026-11-05), exit 0. One §4 signature
adjustment was required by the §7.4 coexistence invariant and is recorded
per §31 preamble: PreflightManagedPark/ReconcileManagedPark take the whole
registry record (`*routing.SelectedRoute`) instead of the bare
`*routing.SelectedPark`, so the platform can tolerate the record's exact
authorized selected identities while the park installs — semantics and
naming preserved.

### Stage 3A progress — Windows exact writer (part 1 landed)

- §31.26 CLOSED for the writer: `ownedRouteProtocol` is now
  `int(winipcfg.RouteProtocolNetMgmt)` (3). A same-prefix protocol-4 row
  (the Linux static value, i.e. ICMP on Windows) projects as foreign and is
  never adopted or deleted; fresh schema2 Windows rows are NetMgmt(3) only.
  Linux remains RTPROT_STATIC(4). Compile-time semantics asserted in tests.
- §31.25 row creation: every CreateIpForwardEntry2 row goes through
  `row.Init()` (InitializeIpForwardEntry) before field overrides and
  carries infinite Valid/Preferred lifetimes; read-backs with finite
  lifetimes are not exact desired rows (projected foreign).
- §31.19 InterfaceLuid: `routing.Operation` carries InterfaceLuid through
  the neutral row view; the Windows projection fills it from
  MIB_IPFORWARD_ROW2.InterfaceLUID; NormalizeSelectedRoute resolves the
  LUID from the desired ifindex; exact deletes construct rows from the
  persisted LUID+index and never resolve the current adapter; a persisted
  LUID makes any same-prefix row with a different LUID foreign.
- §31.25 effective metric: the executor exposes `SelectedWinsOverPark`
  (route offset + interface metric by LUID, uint64 arithmetic, seam-injected
  IP interface lookup), and the unpark path (§7.5 condition 7) uses it with
  a typed restoration-not-winning failure that keeps ParkActive and the
  role frozen; platforms without the prover fall back to the absolute
  Linux metric comparison.
- Windows unit matrix landed: Init-before-create, infinite lifetimes,
  NetMgmt(3) tag, LUID identity inclusion, ICMP-lookalike foreign,
  persisted-LUID deletion, effective-metric win/lose/uint32-max cases.

Gates: 25 Go suites green.

### Post-executor independent audit — 2026-11-06

Audited exact heads after fast-forwarding Kikimora from origin:

~~~text
Kikimora: b856c02d4666b5c432804543ecde2d3da907929c
Leshy:    807a6339ebba374a35f0bf53eea731e7c8bd8965
~~~

The previous Stage 3A handoff is no longer sufficient. Code titled K16 has
landed, but K16 is NOT accepted. The authoritative continuation is now
sections 36-45 of 08d1-managed-selected-routes-completion.md.

Audit verdict:

- K11: accepted;
- K12 Linux: accepted;
- K13 core transaction path: accepted, but K16 paths bypass it;
- K14 core recovery path: accepted, but retirement callers bypass it;
- K15a: partial only;
- K15b: not done;
- K16: repair required (K16R);
- Leshy Stage 1: repair required before L10;
- L10 and later: not started.

Confirmed blocking defects:

1. K16 opens route authority after failed observer/initial audit.
2. K16 full audit does not double-sample observation sequence.
3. observer subscription uses context.Background.
4. Active drift repair calls MarkApplying directly from Active and therefore
   cannot repair.
5. Pending audit resume skips Preflight and durable Applying checkpoint.
6. retirement continuation erases registry ownership without Park cleanup.
7. Forget/FlushZone bypass role applyMu and duplicate retirement logic.
8. Ensure creates a 4s context but platform Normalize/Preflight/Reconcile use
   context.Background, so the timeout is ineffective.
9. normal Active interface invalidation does not request full managed recovery.
10. quarantine retry helper is not wired into Manager.RetryRole.
11. audit has weaker foreign-row classification than PlanSelectedRoute.
12. Windows K15b cache and exact desired-first Replace remain missing.
13. current Kikimora unit baseline is red because several tests write /etc.
14. current Kikimora CI is red; tracked core binaries reappeared.
15. current Leshy CI is red; its writer lease has an unconditional Windows-only
    import on non-Windows targets.
16. Kikimora companion pin still points to old Leshy 84e6e9 although current
    Stage-1 Leshy code is newer; do not repin until Leshy is green.
17. Leshy L10 has not started; its model/backend/registry and Windows local
    broad-delete implementation remain pre-L10.

Mandatory continuation order:

~~~text
R0-K + R0-L
-> update companion pin to green Leshy repair SHA
-> K15b
-> K16R
-> K-R3 startup/shutdown ordering repair
-> L10
-> L11/L12
-> L13
-> L14
-> K17 + L15 convergence
-> deterministic integration
-> Linux vertical
-> Windows vertical
-> release
~~~

Do not treat the existing b856c02 K16 commit as an exit gate. Sections 36-45
supersede this stale execution-log endpoint.

### R0 — baseline repair (landed)

Commits (Kikimora): 67307e7 `test(control): make managed selected tests
hermetic; chore(ci): restore green 08D1 baseline (K-R0a/K-R0b)`;
(leshy): 7a03491, c1281af, 2103bb5, e42509a→rebased, 5a2ff9c, 52b09ca→4ab8798,
7e54dec.

- §36.2 Kikimora red: the four /etc-writing control tests are hermetic
  (temp configDir/routingDir/bypass paths); the Go 1.26 formatter was
  reproduced from the actual toolchain binary (`gofmt` 1.26.10 differs from
  1.27 — the PATH gofmt lie is recorded here so it is not repeated); the
  tracked `toad/kikimora-core{,.exe}` are removed and ignored; the landed-
  stage skeletons are replaced by real tests (writer fence, core singleton,
  privileged auth through Handle, production recoverRole managed sequence
  with Park<WithdrawSelected<Withdraw ordering, §31.9 observation-sequence
  suppression, startup freeze).
- §36.2 Leshy red: `src/lease.rs` Windows import is cfg-gated (L-R0); the
  linux monitor fixtures are fixed (u8 table literal §16.5, pre-sized
  netlink serialize buffer, address family in classify fixtures); the
  `--depend-on` flag is gated to windows targets (clippy); the
  `spawn_monitor_probe` dead-code export is allowed in the bin module tree;
  the docker integration `test_fallback_on_route_failure` red is fixed at
  the fixture level — every test shares one container netns and Leshy routes
  persist past process exit, so the fallback test now uses a domain no
  other test routes (example.com's row from basic.toml conflicted before
  the fallback could engage; example.net resolved to the same real-world IP
  and was replaced by example.org 93.184.215.14 via the coredns fixture).
- §36.3 companion pin: `.github/workflows/build-packages.yml` now consumes
  `LESHY_REF` from one env block (the deleted windows-support branch was
  hard-red); `.github/managed-routing-companion.env` updates to the green
  Leshy SHA at the R0 exit.
- macOS CI lane: the Test(macos) job keeps being cancelled by runner
  reclamation, not by code; reruns were requested and all other lanes are
  green (Build mac/ubuntu, Test ubuntu/windows, Integration Linux). The
  lane is re-armed for the next push.

### K15b-1/K15b-2 — Windows cache + exact Replace (landed, windows host)

- §38.4 one `windowsRouteApi` seam: all Get/Create/Delete/Register/metric/
  LUID/loopback/unicast calls flow through the injected interface; the
  package-global function variables are retired from production paths and
  the test harness builds `fakeRouteApi` (counters + call order + scripted
  errors + onCreate hook).
- §38.1 coherent cache: `selectedCache` with notify/cache generations, the
  observer registers before the initial stable snapshot, generation-
  mismatch requests refresh exactly once, converged duplicates perform
  zero FIB scans, and local mutations re-prove through one coherent
  refresh. The reconcile path holds the executor mutex, so the cache
  helpers are *Locked variants; the callback takes the mutex only to bump
  the generation (§38.5: invalidation-only, never a FIB scan, never direct
  cache mutation).
- §38.2 desired-first Replace: create/verify the exact Desired BEFORE
  deleting the exact previous Owned; the delete builds its row from the
  persisted LUID+index+next-hop identity and never resolves the current
  adapter (§31.19).
- §38.3 EEXIST classification: the selected path bypasses applyOperation's
  generic broad EEXIST replacement — the create goes straight through the
  seam and EEXIST classifies (exact Desired → success; foreign → conflict,
  zero delete; duplicate Desired → conflict).

Native windows matrix (§38.6) landed: zero-scan duplicates, one-refresh
generation mismatch, Add, desired-first Replace order, EEXIST exact/foreign,
wrong-LUID foreign untouched, duplicate Desired conflict, callback
invalidation-only, observer-before-snapshot, effective metric non-winning.

### R0 exit — green baseline achieved (2026-11-06)

- Leshy: full CI green at `7e54dec9323c097e2d2503bf607efd06b1f500c4`
  (ubuntu/macos/windows test lanes, both integration jobs, both builds).
- Kikimora: Toad core + ShellCheck + Build packages green on
  `83d083e` (the follow-up fixes: darwin AF_UNIX socket-length in the
  readiness test, the Windows FileMode/DACL assertion split in the auth-key
  test, the IPv6-toggle hermetic redirect, the shellcheck directive moved
  out of the brace group).
- `.github/managed-routing-companion.env` and the package workflow both pin
  `LESHY_REF=7e54dec…` (§39.3 satisfied: the pin names a green, existing,
  non-moving Leshy SHA).
- Cross-OS gate notes locked in: gofmt must be run from the 1.26.x
  toolchain binary (the PATH gofmt is 1.27 and formats differently — the
  §39.1 CI formatter gate reproduced); windows-latest runs elevated, so
  DACL-denial branches and POSIX mode assertions must be keyed to
  processIsElevated/GOOS (the win DACL denial is proven in the unelevated
  local runs and the ACL contract test on the release VM).

Entry criteria for K16R-1: met (both repos green, pin green).

### K16R-1..K16R-4 — reconciler repair (landed)

Commits: 4dcbad7 (K16R-1), 280edb1 (K16R-2), f44c3d0 (K16R-3), 038e40d
(K16R-4).

- §37.4 BeginRepair: registry demotion legal only from Active, preserving
  Desired/Owned/Park evidence; the audit's drift branch demotes, checkpoints
  the Pending boundary (route.after_pending_checkpoint), and the canonical
  transaction drives from Pending.
- §37.5 one state-aware `reconcileRecordLocked`: Pending path (Preflight →
  after_preflight → MarkApplying → checkpoint → after_applying_checkpoint →
  drive), Applying path (drive directly), Active path (BeginRepair →
  fallthrough). No observer/audit helper composes MarkApplying/MarkActive
  directly.
- §37.6 one `retireRecordLocked`: durable Retiring evidence → exact
  selected removal → the independent park transaction retires in full
  (Pending clears without a delete; Applying/Active go through
  MarkParkRetiring → checkpoint → exact removal → absence proof →
  ClearPark; Retiring resumes) → only then registry.Remove → checkpoint →
  verification clear. Forget/FlushZone/observer-retirement all call it
  under applyMu (§37.7); FlushZone holds one applyMu for the sorted batch.
- §37.8 the Ensure deadline context now reaches Normalize/Preflight/
  Reconcile (applySelectedRouteCanonical replaces the background-context
  path, which is deleted).
- §37.1 initialAuthorityProof: subscribe + stable snapshot + successful
  reconcile + processed sequence — only then does the gate open; failures
  record authorityError and keep it closed (the observer-loss path also
  re-proves, never blindly reopens).
- §37.2 stableSnapshot double-samples the observation sequence around
  SnapshotSelectedRoutes; a move under the read discards the stale
  snapshot; the processed sequence advances only after a fully successful
  audit (§40.4).
- §37.3 subscriptions ride the reconciler lifecycle context.
- §37.9 interface invalidation requests the full managed recovery for
  every affected managed role (quarantined roles observe only).
- §37.10 RetryRole processes the quarantine first
  (retryQuarantinedRole): conflict remains → keep quarantine, zero
  mutation; conflict gone → clear + request managed recovery, role stays
  frozen.
- §37.11 the audit classifier is now the planner itself
  (PlanSelectedRoute per record): unauthorized parks, other selected
  identities, duplicate Desired and explicit foreign rows all quarantine;
  no token is minted while such a row coexists.
- §40.3 the audit re-loads each registry record under applyMu and skips
  stale snapshot results.
- §37.12 the owning suite lands: observer/audit failure gates, scripted
  stale-snapshot discard, audit sequence discipline, drift repair through
  the Pending checkpoint, quarantine + Retry, park-preserving retirement,
  interface recovery, plus the §31.9 suppression test updated to the
  re-drive semantics.

Gates: 25 suites green; race clean for control.
