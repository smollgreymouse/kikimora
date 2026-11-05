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
