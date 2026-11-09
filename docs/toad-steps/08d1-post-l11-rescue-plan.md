# 08D1 post-L11 rescue plan

Status: **MANDATORY RESCUE PREFIX BEFORE L15 / K17 / ANY RELEASE WORK**

This document is an execution packet, not a design discussion. It exists
because work after L10/L11 was landed while acceptance gates were red and,
at L14, required tests were commented out instead of being repaired.

The executor MUST follow this document literally. Architecture changes not
explicitly listed here are out of scope. If an unexpected condition makes one
step impossible, stop that step, record the exact evidence, and keep the
corresponding stage unaccepted. Do not weaken, delete, comment out, ignore, or
rename away a failing gate.

Authoritative parent packet:

~~~text
docs/toad-steps/08d1-managed-selected-routes-completion.md
~~~

Execution journal:

~~~text
docs/toad-steps/08d1-execution-log.md
~~~

Audited heads on 2026-11-09:

~~~text
Kikimora:
    8b7a7e6869ac09d280bcb492dbb31d90dbcb6c56

Leshy:
    6b70c07ba609d8b6d11f8c1749b56cfb1c0b6db3
~~~

Kikimora companion pin at the audit point:

~~~text
LESHY_REF=062842f
~~~

That short pin is not acceptable final evidence.

---

# 1. Executor rules — non-negotiable

These rules override convenience, time pressure, and prior executor habits.

## 1.1 Never repair a failing test by removing the proof

Forbidden:

~~~text
commenting out #[test] / #[tokio::test]
adding #[ignore] to a gate that was previously required
deleting a failing test
replacing a strong integration test with a weaker unit test
changing timeout from <5s to >=30s so the periodic audit can pass it
removing assertions because the implementation does not satisfy them
renaming a test so an audit script no longer sees it
changing CI paths/triggers so a failing job no longer runs
marking a job continue-on-error
~~~

If a test is genuinely obsolete because the contract changed, the executor
must first update the authoritative packet with:

1. old contract;
2. new contract;
3. why the old test is invalid;
4. replacement test proving equal or stronger safety;
5. explicit project-owner decision.

No executor may make that architecture decision silently.

## 1.2 Green locally does not override red GitHub CI

A stage is not accepted until the exact pushed SHA has every required GitHub
job green.

VM/local evidence is additional evidence only.

For every stage handoff record:

~~~text
exact 40-char Leshy SHA
exact 40-char Kikimora SHA if companion changed
GitHub run ID
required job names
job conclusions
local/VM commands and result
~~~

If GitHub is red, stage status is RED / NOT ACCEPTED even if a VM passes.

## 1.3 Never land an in-progress implementation to master as completed work

The L14 report currently says:

~~~text
Status: implementation in Leshy worktree, verification running.
L14 is not landed until fmt, clippy -D warnings and the full test suite are
green and the Leshy commit is pushed.
~~~

But the in-progress commit was pushed to master anyway.

From now on:

- no landed, complete, exit gate complete, or companion repin until the exact
  commit is green;
- no next-stage work while the previous stage's required CI is red;
- no L15 while this rescue packet is open.

## 1.4 Do not rewrite the execution journal destructively

The execution log is historical evidence.

Forbidden:

~~~text
replace the entire file with the newest stage report
delete old audit records
delete previous stage evidence
rewrite failures as successes
~~~

Future changes are append-only except for correcting a factual typo in the
same newly added record.

The executor must restore the history removed by commit 9082031 as part of
this rescue.

## 1.5 Do not invent acceptance from commit titles

Commit titles such as:

~~~text
mark L12 complete
L13 landing
L14 AuthorityOnDemand
~~~

are not evidence.

Code + tests + exact CI run are evidence.

---

# 2. Independent audit findings that must be treated as current truth

## 2.1 Leshy master is not buildable

Current Leshy master:

~~~text
6b70c07ba609d8b6d11f8c1749b56cfb1c0b6db3
~~~

fails at parse/fmt before meaningful tests run.

Entry point:

~~~text
src/routing/mod.rs
~~~

Broken region starts around the L14 reload tests.

Current shape:

~~~rust
// #[tokio::test]
// async fn reload_rejects_kikimora_socket_changes() -> anyhow::Result<()> {
    let server = ...
~~~

A function body was left active after its test declaration was commented out.

This is a P0 repository-health defect.

## 2.2 Four required L14 reload tests were commented out

Entry point:

~~~text
src/routing/mod.rs
~~~

The following required tests are currently commented:

~~~text
reload_rejects_kikimora_socket_changes
reload_rejects_capacity_changes
reload_rejects_aggregation_prefix_changes
reload_two_zones_only_changed_one_is_targeted
~~~

They must be restored as real compiling tests and driven green.

Do not replace them with
managed_reload_sends_zero_controller_calls_until_changed: that test currently
does not call apply_reload and therefore does not prove production reload
behavior.

## 2.3 L11 tests were not deleted; the official privileged gate never passed

The L11 implementation added useful coverage:

~~~text
tests/linux_route_registry_netns.rs
    IPv6 /128 external delete -> event restore <5s
    interface delete/recreate -> new ifindex
    1000 duplicate classifications

tests/netns_monitor_probe.rs
    IPv4 + IPv6 route groups

.github/workflows/ci.yml
    Privileged netns (Linux)
~~~

However the exact GitHub runs were red:

~~~text
ff22a7edc3b5fa178c061a8924868106f3615ba3
    CI run 42365219306
    Privileged netns failure:
        sudo: ./tests/linux-route-registry-netns.sh: command not found

062842f8cd51bd307cef269f635a774cdc062559
    CI run 42470090625
    Privileged netns failure:
        rust cargo is required

538c53623c3313aa6f2707db2d7451a320942ec9
    CI run 42525896026
    Privileged netns still red

d1aa836265e0465570998fc6b910c0fe4ff566a7
    CI run 42574140244
    Privileged netns still red

6b70c07ba609d8b6d11f8c1749b56cfb1c0b6db3
    CI run 42617239723
    multiple failures including parse/fmt/build and privileged netns
~~~

Therefore L11, L12 and L13 do not have valid official acceptance despite VM
evidence recorded in the execution log.

## 2.4 Current netns harness is wrong for GitHub runner ownership

Entry points:

~~~text
.github/workflows/ci.yml
tests/linux-route-registry-netns.sh
~~~

Current CI invokes:

~~~text
sudo -E ./tests/linux-route-registry-netns.sh
~~~

The script then attempts:

~~~bash
if [[ -n "${SUDO_USER:-}" ]]; then
    CARGO_BIN="$(sudo -u "$SUDO_USER" bash -lc 'command -v cargo' 2>/dev/null || true)"
fi
~~~

On GitHub Actions this resolves to no cargo and exits:

~~~text
rust cargo is required
~~~

Do not fix this by skipping the privileged job.

## 2.5 L11 event processing still performs a full snapshot

Entry points:

~~~text
src/routing/mod.rs
    RouteManager::start_kernel_snapshot_watch
    reconcile_after_invalidation

src/routing/linux.rs
    LinuxRouteBackend::snapshot
    LinuxRouteBackend::full_snapshot
    LinuxRouteBackend::refresh_key
    LinuxRouteBackend::rows_for
~~~

Current event flow:

~~~text
Prefix/Interface event
-> start_kernel_snapshot_watch
-> reconcile_after_invalidation
-> backend.snapshot()
-> LinuxRouteBackend::snapshot()
-> full_snapshot() of v4 + v6
~~~

This contradicts the L11 packet intent that targeted event processing use
targeted read-back and reserve full snapshots for initialization / periodic
safety audit / explicit diagnostics.

The DNS request path improved, but the event path is still full-FIB work.

## 2.6 L12 has no native Windows runtime gate

Required by the parent packet:

~~~text
Windows native exact writer runtime proof
new windows/tests/route-registry.ps1 or equivalent
~~~

Current tree has no:

~~~text
windows/tests/route-registry.ps1
~~~

L12 was marked complete before this gate existed.

The Windows route-layer test
gateway_of_another_family_is_rejected was also removed. A config-level
gateway_for_rejects_family_mismatch remains, but the backend-layer regression
proof must be restored or replaced with an explicit equivalent backend test.

## 2.7 L14 reload transaction swallows cleanup failure

Entry point:

~~~text
src/server.rs
    apply_reload
~~~

Current removed-zone path:

~~~rust
if let Err(e) = handler_guard.cleanup_zone(&old.name).await {
    tracing::error!(...);
}
~~~

Then reload continues.

This violates the exact-ownership contract: a failed exact cleanup must not be
silently followed by a successful config swap.

Changed-zone retirement is also weak:

~~~text
DnsHandler::retire_zone_intents
RouteManager::retire_intents
RouteRegistry::retire_intent
~~~

The current public shape returns counts/bools and can hide backend deletion
errors.

## 2.8 Execution log was destructively truncated

Kikimora commit:

~~~text
9082031 docs(08d1): mark L12 Windows exact writer complete
~~~

changed the execution log from about 870 lines to about 350 lines and deleted
earlier stage/audit history.

Current log starts at later L12/L13 material instead of preserving the full
journal.

This is a documentation correctness defect because later executors can no
longer see prior rejected states and repair gates.

## 2.9 Companion pin is stale and too weak

Entry points:

~~~text
.github/managed-routing-companion.env
.github/workflows/build-packages.yml
~~~

Current pin:

~~~text
LESHY_REF=062842f
~~~

Problems:

- short SHA, not the required exact 40-char revision;
- points to L11, not the actual current Leshy work;
- the pinned L11 SHA itself had red GitHub CI;
- the Build packages run triggered by the L11 repin failed at Checkout Leshy.

Do not repin to current broken 6b70c07.

---

# 3. Rescue execution order

The order below is fixed.

~~~text
R1  restore Leshy parser + tests; no semantics changes yet
R2  repair privileged L11 CI harness and get real GitHub green
R3  close L11 targeted-event architecture gap
R4  re-accept L11 on exact green SHA
R5  re-audit and complete L12 Windows native runtime gate
R6  re-accept L13 on exact green SHA
R7  finish L14 reload semantics + restore all reload tests
R8  restore execution log history
R9  repin Kikimora to one exact green full Leshy SHA
R10 run exact cross-repo package gates
R11 update statuses; only then allow L15
~~~

Do not combine R1-R10 into one giant commit.

Each slice below has its own exit gate.

---

# 4. R1 — restore a buildable Leshy master without weakening tests

Goal: recover repository health first.

Do not redesign L14 in this slice.

## 4.1 Entry points

~~~text
src/routing/mod.rs
~~~

Specifically the test module containing:

~~~text
managed_reload_sends_zero_controller_calls_until_changed
reload_rejects_kikimora_socket_changes
reload_rejects_capacity_changes
reload_rejects_aggregation_prefix_changes
reload_two_zones_only_changed_one_is_targeted
~~~

## 4.2 Required edits

Restore all four commented tests as real tests.

For every test:

~~~rust
#[tokio::test]
async fn ...() -> anyhow::Result<()> {
    ...
}
~~~

Restore braces and imports cleanly.

Do not comment out the body.

Do not delete the test.

Do not add #[ignore].

Do not change the expected restart-only behavior.

The three restart-only tests must call the real:

~~~text
crate::server::apply_reload
~~~

and assert error text contains:

~~~text
restart Leshy
~~~

The two-zone test must call the real apply_reload path.

If current setup code is invalid, fix the setup. Do not replace the production
call with RouteManager::handle_invalidation.

## 4.3 Remove accidental duplicate/half-written test code

The current L14 region contains duplicated
managed_reload_sends_zero_controller_calls_until_changed code.

Keep one correct test.

Delete only accidental duplicate code, not distinct coverage.

## 4.4 R1 local gates

Run exactly:

~~~text
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
cargo build --release
~~~

All must exit 0.

## 4.5 R1 cross-target gate

Because previous failures appeared across all OS jobs, push R1 and require the
exact GitHub SHA to show:

~~~text
Test (ubuntu-latest)       green
Test (macos-latest)        green
Test (windows-latest)      green
Build (ubuntu-latest)      green
Build (macos-latest)       green
Integration Tests (Linux)  green
Integration Tests (macOS)  green
~~~

The privileged netns job is allowed to remain red only until R2, and the
handoff must say exactly that. Do not call R1 L14 complete.

Suggested commit:

~~~text
fix(routing): restore L14 reload tests and buildability
~~~

---

# 5. R2 — repair the privileged L11 GitHub harness

Goal: make the official GitHub netns gate actually execute the tests.

Do not weaken the tests.

## 5.1 Entry points

~~~text
.github/workflows/ci.yml
tests/linux-route-registry-netns.sh
tests/linux_route_registry_netns.rs
tests/netns_monitor_probe.rs
~~~

## 5.2 Preferred harness shape

Build as the normal GitHub runner user BEFORE sudo.

Then pass absolute binary paths into the root-only namespace runner.

Recommended CI shape:

~~~text
cargo test --test linux_route_registry_netns --no-run
cargo test --test netns_monitor_probe --no-run

resolve exact binaries under target/debug/deps

sudo -E env \
    LESHY_REGISTRY_TEST_BIN="$ABS_REGISTRY_BIN" \
    LESHY_MONITOR_PROBE_BIN="$ABS_MONITOR_BIN" \
    ./tests/linux-route-registry-netns.sh
~~~

Then the script:

- must not need cargo;
- must not need rustup;
- must not try to rediscover the invoking user's HOME;
- creates/deletes only the namespace and dummy interfaces;
- executes only already-built binaries as root.

Alternative acceptable shape:

~~~text
workflow builds binaries as runner
script accepts --registry-bin and --monitor-bin
~~~

Do not use SUDO_USER to locate cargo.

## 5.3 Script contract

At script start validate:

~~~text
effective uid == 0
ip command exists
registry binary exists + executable
monitor binary exists + executable
~~~

Use absolute paths because ip netns exec changes process namespace but not the
filesystem path.

Keep:

~~~text
set -euo pipefail
trap cleanup EXIT
~~~

## 5.4 Do not remove ignored attributes from the Rust tests blindly

The Rust netns tests may remain #[ignore] when they are explicitly executed
with:

~~~text
--ignored --test-threads=1 --nocapture
~~~

That is intentional harness scoping, not a skipped acceptance gate.

The acceptance requirement is that the privileged CI job actually runs them.

## 5.5 R2 required GitHub evidence

Push the harness fix.

The exact SHA must have:

~~~text
Privileged netns (Linux) = success
~~~

The log must visibly contain all required tests, including:

~~~text
ipv6_route_event_restores_externally_deleted_128
real_kernel_add_readback_delete_and_conflict_safety
real_kernel_dual_stack_through_one_device
real_kernel_interface_recreate_invalidates_old_ifindex
production_monitor_delivers_invalidations
raw_monitor_sees_route_churn
~~~

Record the GitHub run ID in the execution log.

Suggested commit:

~~~text
ci(routing): execute privileged L11 netns gates on GitHub
~~~

---

# 6. R3 — make L11 event handling targeted instead of full-FIB

Goal: close the L11 architectural gap, not merely make CI green.

## 6.1 Current wrong flow

Entry points:

~~~text
src/routing/mod.rs
    start_kernel_snapshot_watch
    reconcile_after_invalidation

src/routing/registry.rs
    mark_dirty
    reconcile_dirty

src/routing/linux.rs
    rows_for
    refresh_key
    snapshot
    full_snapshot
~~~

Current:

~~~text
Prefix event
-> mark matching key Dirty
-> backend.snapshot()
-> full v4 + v6 dump
-> reconcile Dirty key
~~~

Same problem for Interface invalidation.

## 6.2 Required dispatch split

KernelSnapshot mode must distinguish:

~~~text
Full
    -> full snapshot
    -> reconcile all Dirty against snapshot

Prefix(key)
    -> mark only key Dirty
    -> targeted backend read/proof for that key
    -> no full snapshot

Interface(ifindex)
    -> mark only entries whose exact observed.ifindex == ifindex Dirty
    -> targeted proof for those keys
    -> no full snapshot

Zone(zone)
    -> mark only zone entries Dirty
    -> targeted proof for those keys
    -> no full snapshot
~~~

The periodic 30-second safety audit remains:

~~~text
Full -> full snapshot
~~~

## 6.3 Do not create Linux-specific logic in RouteRegistry if shared API is enough

Preferred shared shape:

~~~rust
trait RouteBackend {
    async fn observe_key(&self, key: &RouteKey) -> Result<Vec<ObservedRoute>>;
    ...
}
~~~

or equivalent targeted method.

Then RouteRegistry can reconcile a supplied key/rows with the same exact
identity rules used by full reconcile.

If adding a new backend trait method:

- Linux implements targeted read_prefix;
- Windows may implement its coherent-cache equivalent;
- RegistryOnly / AuthorityOnDemand must not accidentally use it in unsupported
  modes.

Do not call snapshot() from the targeted path.

## 6.4 Exact proof remains unchanged

A targeted event is not permission to adopt a foreign row.

For each Dirty key:

~~~text
exact previous identity exists -> Verified
missing owned row -> backend ensure/repair
same key + foreign identity -> Conflict
multiple same-key rows -> Conflict / safe refusal
~~~

Do not weaken L10 exact identity semantics.

## 6.5 Required counters/tests

Extend unit tests so after convergence:

~~~text
initial full_snapshots = 1
inject Prefix event for one key
process event
full_snapshots still = 1
targeted_reads += 1

inject Interface event affecting N known keys
full_snapshots still = 1
targeted_reads += N
unrelated keys = zero I/O
~~~

For 1000 Verified keys:

~~~text
one Prefix invalidation
999 unrelated subsequent hits
zero extra full snapshots
zero unrelated targeted reads
~~~

Privileged netns acceptance must additionally capture test-visible counters if
possible, or expose a seam that proves request/event full snapshot count stays
flat.

## 6.6 R3 exit

Required:

~~~text
cargo fmt green
clippy -D warnings green
all tests green
Privileged netns GitHub job green
all standard GitHub jobs green
~~~

Only then may L11 be marked ACCEPTED.

Suggested commit:

~~~text
fix(routing): reconcile targeted Linux invalidations without full FIB snapshots
~~~

---

# 7. R4 — re-accept L11 truthfully

Do not change production code in this step unless a gate exposes a bug.

Update the execution log with:

~~~text
L11 ACCEPTED
exact 40-char SHA
GitHub CI run ID
Privileged netns run ID/job evidence
test names
full_snapshots/targeted_reads counter evidence
~~~

Do not reuse the old 062842f acceptance claim.

The new accepted L11 SHA is the first SHA where:

~~~text
all ordinary CI jobs green
Privileged netns green
targeted event path does not full-snapshot
~~~

---

# 8. R5 — re-audit and complete L12 Windows exact writer

L12 is not accepted merely because unit tests compile on Windows.

## 8.1 Entry points

~~~text
src/routing/windows.rs
src/routing/windows_api.rs
.github/workflows/ci.yml
new: windows/tests/route-registry.ps1
~~~

## 8.2 Mandatory static audit

Before writing tests, confirm production RouteBackend path does NOT use broad
same-prefix deletion.

Search for every callable path involving:

~~~text
GetIpForwardTable2
CreateIpForwardEntry2
DeleteIpForwardEntry2
find_route_rows
delete_routes
ERROR_OBJECT_ALREADY_EXISTS
NotifyRouteChange2
CancelMibChangeNotify2
~~~

For normal RouteBackend ensure/remove:

- no delete all same-prefix rows;
- no EEXIST -> broad delete;
- previous ownership must be exact identity;
- exact foreign row must survive unchanged.

Document the exact call graph in the execution log.

## 8.3 Restore backend-layer family mismatch coverage

A config-level test exists:

~~~text
src/config.rs
    gateway_for_rejects_family_mismatch
~~~

Add or restore a Windows writer/backend test proving row construction itself
refuses a next hop of the wrong family.

Do not rely solely on config validation for a platform safety boundary.

## 8.4 Native Windows runtime script

Create:

~~~text
windows/tests/route-registry.ps1
~~~

Requirements:

- require Administrator;
- use disposable test prefixes only;
- snapshot unrelated routes before/after;
- never touch default route;
- never touch management route;
- fail if safe disposable interface prerequisites are absent rather than
  improvising against a production adapter.

Minimum runtime cases:

~~~text
IPv4 exact add/read-back/remove
IPv6 exact add/read-back/remove
1000 duplicate ensures -> no mutation churn
pre-existing compatible foreign row -> owned=false, cleanup leaves it
same-prefix foreign row -> conflict, unchanged
exact previous Owned target replacement
wrong protocol row -> foreign
wrong metric row -> foreign
wrong interface/index row -> foreign
EEXIST exact Desired -> safe success
EEXIST foreign -> zero delete
external deletion -> native observer invalidation/repair if L12 claims observer
~~~

If NotifyRouteChange2 is intentionally deferred to another stage, L12 must not
claim it complete against the parent packet. Either implement it here or mark
L12 partial and move the gate explicitly with owner approval.

Current code still contains a WindowsRouteObserver TODO around registration.
That must be resolved before declaring the packet's Windows notification
contract complete.

## 8.5 Windows CI job

Add a Windows-native job or step that runs the non-destructive runtime script
on a suitable disposable runner environment.

If GitHub-hosted runner cannot safely satisfy the adapter requirements:

- keep unit tests in GitHub;
- run the runtime gate on the documented Windows lab VM;
- record exact VM identity/time/result;
- stage remains PARTIAL until the required release-gate runtime evidence
  exists.

Do not silently remove the runtime gate from the plan.

## 8.6 R5 exit

L12 accepted only when:

~~~text
Windows unit tests green
Windows build/clippy/fmt green
native runtime evidence exists
foreign-row preservation proven
no broad deletion reachable
~~~

Suggested commits:

~~~text
test(windows): restore exact writer safety coverage
test(windows): add native route-registry acceptance
~~~

---

# 9. R6 — re-accept L13 against a green base

L13 features may be retained:

~~~text
uds-compat
managed_selected_routes_v2
privileged_control_auth_v1
ActivationID
ModeGeneration
Windows AF_UNIX
~~~

But the old L13 SHA was pushed while the privileged L11 gate was red.

## 9.1 Entry points

~~~text
src/config.rs
src/routing/kikimora.rs
Cargo.toml
Cargo.lock
tests/integration_test.rs
~~~

## 9.2 Re-run required L13 tests on the rescued base

At minimum:

~~~text
schema2_requests_carry_the_exact_activation_fence
legacy_requests_omit_the_activation_fence
activation_identity_requires_a_complete_valid_pair
required_capabilities_follow_the_protocol_generation
windows_af_unix_round_trips_the_schema2_wire
unchanged_generations_after_reconnect_do_not_dirty
generation_change_without_a_zone_loses_authority
~~~

Do not delete the semantic replacement of the old
generation_change_without_a_zone_invalidates_everything test: the change from
Full to AuthorityLost is intentional L14 semantics, but must remain explicitly
covered.

## 9.3 Exit

The exact post-rescue SHA must be all-green in GitHub, including privileged
L11 gate.

Only then record L13 ACCEPTED.

---

# 10. R7 — finish L14 correctly

L14 is currently IN PROGRESS / REJECTED, not landed.

## 10.1 Entry points

~~~text
src/routing/mod.rs
    RouteManager::start_authority_on_demand_watch
    RouteManager::handle_invalidation
    dispatch_authority_invalidation

src/routing/kikimora.rs
    subscription pump
    snapshot_invalidations

src/server.rs
    apply_reload
    routing_manager_restart_required

src/dns/handler.rs
    cleanup_zone
    apply_static_routes_for_zone
    zone_route_intents
    retire_zone_intents
    reconcile_routes_after_reload

src/routing/registry.rs
    remove_zone
    retire_intent
    ZoneRemovalReport
~~~

## 10.2 Keep the correct AuthorityOnDemand behavior

Do not regress these intended semantics:

~~~text
AuthorityLost:
    mark managed desired entries Dirty locally
    zero snapshot
    zero bulk controller RPC

Zone(name):
    dirty only that zone
    zero bulk RPC

Full in AuthorityOnDemand:
    invariant violation
    no snapshot
    no dirty-all repair

same Dirty key demanded by 32 concurrent callers:
    exactly one Ensure RPC
~~~

The existing tests for these are useful; keep them.

## 10.3 Restart-only reload fields

The following are restart-only:

~~~text
routing_backend
kikimora_socket
route_aggregation_prefix
max_dynamic_routes
~~~

Entry point:

~~~text
src/server.rs
    routing_manager_restart_required
~~~

Tests restored in R1 must exercise real apply_reload and prove each changed
field returns an error before any route retirement/config swap.

Add a combined table-driven test if useful, but do not remove the individual
named regressions until equivalent coverage is visibly stronger.

## 10.4 Removed-zone cleanup failure must abort reload

Current wrong code logs and continues.

Required transaction:

~~~text
build/validate new matcher
diff zones
prepare new replacement rows
retire old exact ownership

IF ANY exact cleanup/retirement fails:
    return Err
    do not update handler config
    do not replace matcher
    do not claim reload success
    preserve ownership evidence
~~~

For Removed zone, propagate the error instead of logging and continuing.

Do not catch and log-and-continue.

## 10.5 Changed-zone retire_intents must return structured failure

Current chain:

~~~text
DnsHandler::retire_zone_intents -> usize
RouteManager::retire_intents   -> usize
RouteRegistry::retire_intent   -> bool
~~~

This loses the reason for backend deletion failure.

Change to Result-bearing contract.

Recommended:

~~~rust
pub struct RetirementReport {
    pub retired: usize,
    pub skipped_replaced: usize,
}

pub async fn retire_intents(
    &self,
    intents: &[RouteIntent],
) -> Result<RetirementReport>
~~~

Registry primitive should distinguish:

~~~text
Retired
ReplacedByNewerIntent
Absent
Error(error)
~~~

or simply return Result<RetireOutcome>.

Rules:

- exact delete error -> Err;
- foreign/replaced newer owner -> safe non-delete outcome;
- ownership evidence remains on error;
- caller can abort reload.

## 10.6 Reload atomicity test matrix

Add/restore tests using a fake backend whose calls and failures are visible.

Required tests:

1. kikimora_socket changed:
   - apply_reload returns restart-required error;
   - zero cleanup calls;
   - old config remains.

2. max_dynamic_routes changed:
   - same.

3. route_aggregation_prefix changed:
   - same.

4. routing_backend changed:
   - same.

5. two zones, one changed:
   - only changed zone gets targeted invalidation;
   - unchanged zone zero backend/controller calls.

6. removed-zone exact delete fails:
   - apply_reload returns Err;
   - old config/matcher remains;
   - registry ownership remains;
   - no Configuration applied successfully result.

7. changed-zone old-intent delete fails:
   - same transaction abort properties.

8. replacement static route fails before retirement:
   - old route remains;
   - old config remains.

9. successful changed-zone reload:
   - replacement proves before old ownership retires;
   - only affected zone invalidated.

10. managed successful reload:
    - no Full;
    - zero bulk controller RPC;
    - only demanded Dirty key later calls Ensure.

## 10.7 Do not spawn retry work after a failed reload transaction

Inspect:

~~~text
src/server.rs apply_reload
~~~

A failure before commit must not leave a background static-route retry task
operating on state that was never committed.

Only spawn retry after config/matcher commit, or bind retry to the committed
configuration generation.

## 10.8 R7 exit

Exact SHA must pass:

~~~text
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
cargo build --release

GitHub:
    all test OSes green
    both build OSes green
    Linux integration green
    macOS integration green
    Privileged netns green
~~~

Only then change L14 status to ACCEPTED.

Suggested commits:

~~~text
fix(reload): make route retirement transactional and fail closed
test(reload): restore L14 restart-only and targeted-reload gates
~~~

---

# 11. R8 — restore the execution journal

Entry point:

~~~text
docs/toad-steps/08d1-execution-log.md
~~~

Use Git history before:

~~~text
9082031
~~~

as the source for the removed historical prefix.

Required result:

- original header restored;
- Stage 0 and previous handoffs restored;
- all post-L10 audit records preserved;
- L11/L12/L13 records preserved but corrected with explicit historical note:
  old claims were not official GitHub acceptance;
- L14 record changed to IN PROGRESS / REJECTED until R7 green;
- this rescue audit appended.

Do not delete the old false claim silently. Preserve it and append a correction
with evidence so later auditors can understand the sequence.

Suggested documentation commit:

~~~text
docs(08d1): restore execution history and correct L11-L14 acceptance
~~~

---

# 12. R9 — fix the companion pin contract

Entry points:

~~~text
.github/managed-routing-companion.env
.github/workflows/build-packages.yml
~~~

## 12.1 Full SHA only

Set:

~~~text
LESHY_REF=<40 lowercase hex chars>
~~~

Never short SHA.

Add validation in the resolve job:

~~~text
regex ^[0-9a-f]{40}$
~~~

Reject branch names, short SHA, and whitespace.

Stage 10 may replace this development SHA with the explicitly approved final
v0.6.0 tag only when the release packet says so.

## 12.2 Pin only a green accepted Leshy SHA

Do not pin the current broken 6b70c07.

The pin must point to the final R7 accepted SHA.

## 12.3 Checkout assertion

Both Linux and Windows package jobs must:

~~~text
checkout exact ref
git rev-parse HEAD
assert == LESHY_REF
print exact SHA
~~~

No implicit moving master.

---

# 13. R10 — exact cross-repo package acceptance

After R9 push, the pin-only change must trigger Build packages.

Required jobs:

~~~text
Resolve companion pin
Linux deb
Windows MSI + zip
~~~

All must be green.

If Checkout Leshy fails:

- fix the pin/checkout;
- do not skip the checkout;
- do not replace exact pin with master.

Record exact run ID.

Packages must embed/report the same accepted Leshy SHA.

Only after this run succeeds is the Kikimora+Leshy pair accepted for the next
stage.

---

# 14. R11 — status correction in the parent packet

After R1-R10 are green, update the parent packet status truthfully.

Before rescue completion:

~~~text
L11: implementation useful, acceptance revoked pending official privileged gate
L12: partial, native Windows gate missing
L13: implementation landed on red base, acceptance revoked
L14: rejected/in progress; current master broken
L15: forbidden
K17: forbidden
~~~

After rescue completion, only stages whose exact gates passed may become
ACCEPTED.

Do not use substantially complete to bypass a named gate.

---

# 15. Required commit sequence

The executor should keep repair slices reviewable.

Recommended sequence:

~~~text
L-R1
    fix(routing): restore L14 reload tests and buildability

L-R2
    ci(routing): execute privileged L11 netns gates on GitHub

L-R3
    fix(routing): reconcile targeted Linux invalidations without full FIB snapshots

L-R4
    docs(08d1): record exact accepted L11 evidence

L-R5a
    test(windows): restore exact writer safety coverage

L-R5b
    test(windows): add native route-registry acceptance

L-R6
    docs(08d1): revalidate L13 on rescued green base

L-R7a
    fix(reload): make exact route retirement fail closed

L-R7b
    test(reload): restore L14 restart-only and targeted-reload gates

K-R8
    docs(08d1): restore execution history and correct acceptance

K-R9
    chore(companion): pin exact green Leshy rescue SHA

K-R10
    no code required if package CI is green; record exact run evidence
~~~

Do not squash away the distinction between test restoration and production
semantic fixes before audit.

---

# 16. Mandatory static checks before every Leshy push

Run:

~~~text
grep -R -n -E '^[[:space:]]*//[[:space:]]*#\[(tokio::)?test' src tests
~~~

Expected after R1:

~~~text
no commented-out test attributes
~~~

Also:

~~~text
git diff --diff-filter=D --name-only <last-accepted-sha>..HEAD -- '*test*' 'tests/**'
~~~

Every deleted test file/function must be justified in the handoff.

Search for newly ignored tests:

~~~text
git diff <last-accepted-sha>..HEAD -- src tests | grep -n '^\+.*#\[ignore'
~~~

Any new ignore requires explicit packet authorization.

Search for weakened CI:

~~~text
git diff <last-accepted-sha>..HEAD -- .github/workflows
~~~

Reject:

~~~text
continue-on-error
job removal
path exclusion that prevents the gate from running
test command removal
~~~

---

# 17. Required acceptance table in every executor handoff

The executor must append a table like:

| Gate | Exact command/job | Result | Evidence |
|---|---|---|---|
| fmt | cargo fmt --all -- --check | PASS/FAIL | local |
| clippy | cargo clippy --all-targets --all-features -- -D warnings | PASS/FAIL | local |
| tests | cargo test --all-targets --all-features | PASS/FAIL | local |
| release build | cargo build --release | PASS/FAIL | local |
| Ubuntu test | GitHub job | PASS/FAIL | run ID |
| macOS test | GitHub job | PASS/FAIL | run ID |
| Windows test | GitHub job | PASS/FAIL | run ID |
| Linux integration | GitHub job | PASS/FAIL | run ID |
| macOS integration | GitHub job | PASS/FAIL | run ID |
| privileged netns | GitHub job | PASS/FAIL | run ID |
| Windows native route gate | lab/CI | PASS/FAIL | exact evidence |
| Kikimora Build packages Linux | GitHub job | PASS/FAIL | run ID |
| Kikimora Build packages Windows | GitHub job | PASS/FAIL | run ID |

A single FAIL means the owning stage remains unaccepted.

---

# 18. Explicit no-go decisions

The executor must NOT:

- start L15;
- start K17;
- create v0.6.0;
- update installer version to 0.6.0;
- declare L11/L12/L13/L14 complete from old VM logs;
- delete the privileged netns CI job;
- delete or comment reload tests;
- hide red CI by editing documentation;
- repin Kikimora to a red Leshy SHA;
- repin to a short SHA;
- change managed routing architecture;
- replace AF_UNIX with TCP/named pipe;
- reintroduce local-writer fallback in managed mode;
- make foreign route deletion broader;
- weaken exact ownership checks.

The only work authorized before rescue completion is R1-R10.

---

# 19. Rescue definition of done

The rescue is complete only when all are true:

1. Leshy master parses and formats.
2. No required L14 test is commented/ignored/deleted.
3. All ordinary Leshy CI jobs are green on one exact SHA.
4. Privileged netns GitHub job is green on that same exact SHA.
5. The privileged log visibly runs all L11 netns/probe tests.
6. Prefix/Interface event handling does not full-snapshot the FIB.
7. L11 exact identity/counter tests remain present.
8. L12 native Windows route acceptance exists and passes.
9. L13 schema2/AF_UNIX tests pass on the rescued base.
10. L14 restart-only tests call real apply_reload and pass.
11. Removed-zone cleanup failure aborts reload.
12. Changed-zone retirement failure aborts reload.
13. Ownership evidence survives failed retirement.
14. Successful reload invalidates only affected zones.
15. AuthorityOnDemand still performs zero snapshot/bulk RPC on authority loss.
16. Execution log historical prefix is restored.
17. Companion pin is exactly 40 hex chars.
18. Companion pin points to the accepted green Leshy SHA.
19. Linux package job checks out and asserts that exact SHA.
20. Windows package job checks out and asserts that exact SHA.
21. Build packages Linux is green.
22. Build packages Windows is green.
23. Parent packet status is corrected.
24. L15 remains untouched until items 1-23 pass.

Only then resume with parent packet L15.
