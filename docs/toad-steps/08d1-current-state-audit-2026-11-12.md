# 08D1 current-state audit — 2026-11-12

This addendum reconciles the older 739e1ab remediation snapshot with the
current source and exact-head GitHub evidence. The ordered gates in
`docs/roadmap.md` and implementation requirements in
`08d1-post-739e1ab-audit-remediation.md` remain authoritative. This report does
not accept Phase E Windows, Phase F, or Phase G.

For current source identity and CI conclusions, this snapshot supersedes the
older roadmap baseline `b751134`, remediation audit base `739e1ab`, and
execution-log M0 head `4faebf2`. Those SHAs and their stage notes remain
historical evidence; this update does not rewrite them or replace the planned
acceptance requirements.

## Source and CI identity

```text
Kikimora origin/master: 8937ce7bca084d069013e7d8e3bb526d27764ab4
Leshy companion:        61786d489da2acf50d6b4b53a77a39184d746852
```

The remote master was independently confirmed at this SHA. Four workflows
completed successfully on that exact head:

| Workflow | Run | Moscow completion | Result |
|---|---:|---|---|
| Toad core | [43122919975](https://github.com/smollgreymouse/kikimora/actions/runs/43122919975) | 2026-11-12 02:55:55 | 18/18 jobs successful |
| Desktop UI | [43122920038](https://github.com/smollgreymouse/kikimora/actions/runs/43122920038) | 2026-11-12 02:58:55 | Resolve + Linux/macOS/Windows jobs successful; see Windows evidence defect below |
| Build packages | [43122920107](https://github.com/smollgreymouse/kikimora/actions/runs/43122920107) | 2026-11-12 03:00:17 | Resolve + Linux deb + Windows MSI/zip successful |
| ShellCheck | [43122920059](https://github.com/smollgreymouse/kikimora/actions/runs/43122920059) | 2026-11-12 02:51:26 | Successful |

The Toad run includes the three-OS Go unit/race jobs, tagged Phase F barrier
and trigger tests/builds, release-absence checks, Linux managed denial and
success jobs, interop jobs, cutover jobs, and Windows cross/package checks.
The DNS fixture self-test log reports five tests, 5.224 seconds, `OK`.

## Evidence levels and current gate state

**CI job outcomes:** the four workflows and exact run results above succeeded.
The Windows Desktop job is a pass at the job level but not acceptable test
evidence because its wrapper accepted nine empty suite logs. The R2 CI command
also omits one ordering test described below.

**Source and targeted tests:** R2–R6 implementation and tests are present to
different levels as detailed below. The exact-head tagged CI runs provide
direct evidence only for the selected test names and commands.

**Acceptance not evidenced:** no exact-head native Windows Row G or state
layout upgrade result was found, and Phase G's full VM suite has not been
established. Do not infer Phase E Windows or Phase F acceptance from successful
CI jobs.

- R7 job outcomes are green for the hosted scope, but full tagged coverage is
  incomplete until the omitted R2 ordering case is selected and passes. The
  managed-success workflow invokes `--suite hosted`; by design that covers
  rows 1–11, 14, 15, 17, and 18. Rows 12, 13, and 16 are reserved for
  `--suite full` on the Ubuntu Phase G VM. This boundary is intentional and is
  not a hosted-CI defect (`managed-leshy-routing-netns.sh:11-18`,
  `.github/workflows/toad.yml:477-489`).
- R1 Windows migration code, unit tests, and the native upgrade harness exist,
  but this audit found no exact-head native upgrade result for the legacy-local,
  legacy-managed, and in-flight-transition rows. Keep R1 and R8 open.
- R6's corrected Row G source uses `routeprobe set-list` for both mutation and
  restoration, but no exact-head Windows native A–H run was established here.
  Keep the Row G native acceptance and R8 open.
- Phase E Windows re-acceptance and Phase F acceptance remain open until the
  remediation packet's full acceptance list is evidenced, including both
  Windows native gates. Do not infer acceptance from green builds or hosted CI.
- Phase G remains gated on its full VM suite, including rows 12/13/16; the
  hosted subset does not satisfy those rows.

## R1–R6 source audit

### R1 — Windows state-layout migration: implemented; native gate open

`state_layout_windows.go:32-42,54-110` defines the four-file copy-and-verify
migration, preserves legacy files, checks the completion marker, and writes the
marker durably. `state_layout_windows_test.go:43-208` covers fresh, local,
managed, partial-copy/resume, conflict, authoritative-marker, and in-flight
transition cases. `test_state_layout_upgrade.ps1:3-22` defines the three native
upgrade rows. The exact-head CI success does not establish those native rows.

### R2 — recovery barrier: source test exists; tagged CI selection misses it

`engine.go:87-99` calls the real recovery driver first and invokes the barrier
only after a successful step. `recovery_barrier_testbuild_test.go:289-359`
covers barrier failure stopping progression and the exact
Freeze → Observe → Park → WithdrawSelected sequence at the waiting marker,
before Withdraw. The CI command selected by `.github/workflows/toad.yml:118-121`
uses `-run 'RecoveryBarrier|RecoveryStepBarrier'`. It runs seven tagged tests
but does not select `TestRecoveryWithdrawSelectedMarkerOrdering`, whose name
does not match that regex. The remaining barrier tests passed, but the exact
ordering assertion has not been evidenced in tagged CI. Add this exact test to
the selection and rerun before claiming the complete R2 test contract is green.

### R3 — managed recovery trigger: production path tested; acceptance remains
within R8

`api.go:362-378` routes the tagged method through the normal response path;
`test_control_testbuild_test.go:83-150` checks driver entry and scheduling
errors; lines 152-249 exercise a tagged child through the AF_UNIX API, wait for
the `withdraw-selected` marker, release it, and require the role to return to
Ready. The exact-head tagged trigger tests and untagged release rejection
passed. The process test does not sample the snapshot while the marker is held,
so it proves the worker reached the real recovery step and later converged,
but does not independently capture a `Recovering` snapshot at the barrier. This
is a P2 evidence gap: add a direct snapshot assertion while the marker is held
to satisfy the packet's explicit “actually in recovery” check.

### R4 — Ready-Toad: specified fixture behavior and unit coverage present

`ready-toad/main.go:206-243,320-364,439-528` implements synchronous resampling,
revision publication, polling, configured-family filtering, and readiness.
`main_test.go:328-536` covers family filtering; missing/rebound interface
behavior; ifindex, generation and revision; restart; poll loss/restore;
subscription wake; and stopped-session state. Toad CI builds the tagged test
artifact and the hosted suite uses Ready-Toad. No uncovered specified R4
requirement was found in the inspected source/tests. This is not a claim of a
native Windows Ready-Toad acceptance run.

### R5 — shared DNS fixtures: implementation and self-test pass; one parser
contract edge remains unverified

`fixtures/dns-stub.py:36-86` parses QTYPE/QCLASS from the first question,
returns the canonical A/AAAA answers, NODATA for other types, and drops
truncated input. `test_dns_fixtures.py:113-151` checks A, AAAA, TXT, malformed
frames, and that `dnsq.py` reaches the same stub. The denial smoke uses those
shared paths (`managed-leshy-smoke.sh:20-21,85-86,100-116`); managed success
uses the same fixture directory. CI recorded all five self-tests passing.
The success harness resolves the shared directory for both stub and query
helper (`managed-leshy-routing-netns.sh:65,176,247-248`).

The parser does not validate the DNS header's QDCOUNT before parsing one
question (`dns-stub.py:36-55`), and the self-test has no multi-question case.
The packet specifies a single-question fixture contract. This is a P2/P3
fixture robustness gap, not a production DNS vulnerability. Add an explicit
QDCOUNT=1 check and regression cases for zero and multiple questions before
claiming that the parser enforces the whole fixture contract.

### R6 — Windows Row G: source corrected; native acceptance open

`test_managed_cutover.ps1:34-49` requires a RouteProbe. Row G at
`:597-619` sends the canary list through `routeprobe ... set-list`; restoration
at `:676-692` uses the same API and verifies the bytes. The script does not
write the product list directly or restart the core for this mutation. No
exact-head native Windows run was established; R6's source correction is not
the Row G acceptance result.

## P1 Desktop UI evidence blocker

The Windows required-suite wrapper is fail-open for empty native test output.
`desktop/tests/run-required-suites.ps1:81-88` creates an empty log when native
output is null; `:89-106` then checks only exit status and scans whatever log
exists for skips/warnings. It never requires a `Totals:` line or a positive
executed-test count (`:119-137`).

For Desktop UI run 43122920038, Windows job 130867710975 reported all nine
suites as `<no log>` and “all green, zero mandatory skips.” Artifact 13901534356
(`desktop-ui-logs-windows-latest`, 1256 bytes) contains nine zero-byte logs;
artifact SHA-256:

```text
d4c69a1cac45e4d48bb554390a24ea3b28a1ad7109605208ea85d93a155c7d63
```

This proves the test-output evidence is absent and the wrapper accepted that
absence. It does **not** prove the suite binaries did not launch. Linux and
macOS artifacts have nonempty per-suite logs with positive counts
`[29, 11, 5, 38, 9, 4, 6, 10, 4]`, and zero failures/skips.

The next desktop release gate is BLOCKED until the wrapper requires output
existence, valid QtTest totals with a positive executed count, zero failures
and skips, and no unexpected warnings; then rerun Desktop UI on the fixed exact
head and independently inspect all three OS artifacts. This is separate from
core R7 and the planned Phase G full VM suite.

## Ordered continuation

1. Fix and review the Windows Desktop UI required-suite evidence contract;
   no acceptance from the current empty-log Windows result.
2. Add the R2 ordering test to the tagged CI selection, add the R3 held-marker
   snapshot assertion, and enforce/test the R5 single-question fixture
   contract.
3. Run the four workflows on the resulting fixed exact head; independently
   verify nonempty logs, valid QtTest totals, positive counts, and zero
   failures, skips, or unexpected warnings for all nine Windows suites. Confirm
   the R2 exact test name appears as `=== RUN` and `--- PASS`.
4. On that exact candidate head, run the corrected Windows fresh-install A–H
   gate (including Row G) and the separate state-layout upgrade harness's
   legacy-local, legacy-managed, and in-flight-transition rows. Preserve the
   per-row evidence.
5. Evaluate the full R8 checklist.
   Only append Phase E Windows re-accepted / Phase F accepted after every
   required row is green.
6. Start Phase G only after that acceptance. Run `--suite full` on the Ubuntu
   VM and record rows 12, 13, and 16 in addition to the hosted subset.

No build, test, runtime script, commit, or push was run as part of this audit.
