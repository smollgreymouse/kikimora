# 08D1 audit after Phase D / start of Phase E — 2026-11-10

Status: **AUTHORITATIVE CORRECTION BEFORE FURTHER PHASE E WORK**

This audit reviews the large update after commit 7378999. It does not change
production code. It supersedes the current Phase D ACCEPTED / Phase E
unblocked wording where this file is stricter.

Audited heads:

~~~text
Kikimora:
    9a4a24373a9e6b84dd4b856efd86bb2a715600a3

Leshy:
    61786d489da2acf50d6b4b53a77a39184d746852
~~~

Previous audit baseline:

~~~text
Kikimora:
    7378999172bff1339a2a77c2719fdb350e3bada3
~~~

Changes since that baseline are large: 11 Kikimora commits, about 3850
insertions, D7R/D8/D9/D10 implementation, Leshy verdict-fence verification
and the first Phase E Linux cutover harness.

---

# 1. Evidence that is genuinely good

## 1.1 D7R stale-marker repair is materially implemented

Files:

~~~text
toad/internal/control/committed_state.go
toad/internal/control/control.go
toad/cmd/kikimora-core/main.go
~~~

The deferred production constructor no longer caches a pre-recovery marker.
StartupManagedAuthority performs:

~~~text
RecoverManagedModeTransition
LoadCommittedManagedState
EnsurePrivilegedAuthKey
RestoreCommittedManagedState
RegenerateAndVerifyLeshyConfig
~~~

and the marker generation is checked against the committed generation.

The previous pendingManagedMarker production state is gone.

## 1.2 Checkpoint activation fencing is materially implemented

Files:

~~~text
toad/internal/control/selected_routes.go
toad/internal/routing/selected_registry.go
toad/internal/control/selected_checkpoint_fence_test.go
~~~

Checkpoint write/restore now carries ActivationID, ModeGeneration and
platform fencing, with dedicated tests for stale activation/generation.

## 1.3 D8 request fencing and auth are substantially implemented

Files:

~~~text
toad/internal/control/leshy_routes_api.go
toad/internal/control/api.go
toad/internal/control/leshy_route_gate_test.go
/home/nmkartsev/Projects/leshy/src/routing/kikimora.rs
~~~

Kikimora now decodes activation_id/mode_generation, validates them before
selected-route mutation, has manager-level local-mode denial, and echoes the
active fence on Ensure verdicts.

Leshy commit 61786d4 verifies the returned controller fence.

## 1.4 D9 diagnostics are materially implemented

Transition phase/fingerprint and committed activation fingerprint are now
covered by dedicated tests without exposing full ids/tokens.

## 1.5 Local Go gates are green at the audited head

Executed during this audit:

~~~text
cd toad
go test ./...
go test -race ./internal/control ./internal/routing
~~~

Both passed.

## 1.6 Leshy CI is now fully green, including macOS integration

Current Leshy run:

~~~text
run 42869262829
head 61786d489da2acf50d6b4b53a77a39184d746852
attempt 3
overall success
~~~

All jobs are now success:

~~~text
Integration Tests (macOS)
Privileged netns (Linux)
Integration Tests (Linux)
Build (macos-latest)
Build (ubuntu-latest)
Test (windows-latest)
Test (macos-latest)
Test (ubuntu-latest)
~~~

The earlier macOS runner deferral in the execution log is now obsolete and
must be corrected by an append-only record.

---

# 2. Current CI truth for Kikimora master

Audited head:

~~~text
9a4a24373a9e6b84dd4b856efd86bb2a715600a3
~~~

Current GitHub evidence:

~~~text
Toad core:
    42882177035
    success

ShellCheck:
    42882178480
    FAILURE

Build packages:
    42882177295
    success
    Resolve companion pin success
    Linux deb success
    Windows MSI + zip success
~~~

Therefore current master is not an all-green Phase E base.

ShellCheck fails specifically in:

~~~text
linux/tests/toad/managed-mode-cutover-vm.sh
~~~

with SC2015/SC2002 findings. Do not add a file-scoped suppression; fix the
script.

---

# 3. Verdict

| Area | Verdict |
|---|---|
| D7R-1 fresh committed-state load | retain |
| D7R-2 checkpoint activation fence | retain |
| D7R-3 live transition gate | retain, durability repair required |
| D8 route fence/auth | retain |
| D9 diagnostics | retain |
| D10 unit process matrix | retain |
| D10 VM evidence | incomplete |
| Phase D acceptance | **REOPENED** |
| Phase E Linux | **NOT ACCEPTED / implementation incomplete** |
| Phase E Windows | not started |
| Phase F+ | blocked |

Phase D must be re-accepted after section 4. Phase E must then be implemented
and proven according to sections 5-7.

---

# 4. Phase D release blockers found by this audit

## P0-D1 — schema2 marker deletion is not durable

Production entry:

~~~text
toad/internal/control/selected_routes.go
    clearManagedMarker
~~~

Current implementation uses:

~~~text
os.Remove(path)
~~~

without syncing the parent directory.

That function is used by both the normal Managed -> Local transaction and
the D7 recovery roll-forward.

This violates the durable transaction contract. A process crash is not the
same as power loss: os.Remove can appear successful to the process while the
directory update is not durable across a sudden reboot/power loss.

Required fix:

~~~text
clearManagedMarker -> fsutil.RemoveDurable(path)
~~~

Missing-file semantics remain success.

Required tests:

~~~text
normal Disable uses the durable removal path
local-target recovery uses the same durable removal path
failure of durable removal leaves the transition journal in place
generation is not committed after a removal failure
~~~

Phase G/H reboot matrices remain the final durability proof.

## P0-D2 — duplicate managed zones have inconsistent canonicalization

Entries:

~~~text
toad/internal/control/mode_transaction.go
    managedZoneNames
    NewManagedLeshyTransition

toad/internal/control/selected_routes.go
    writeManagedMarker
    sortStringsCanonical
~~~

Current behavior is inconsistent:

1. managedZoneNames sorts but does not reject/deduplicate duplicate zones.
2. NewManagedLeshyTransition canonicalizes/deduplicates the journal zones.
3. writeManagedMarker receives the original zoneNames.
4. sortStringsCanonical writes unique values at the start but does not
   shrink the slice; duplicate slots become empty strings.
5. managedModeMarker.Validate rejects the resulting empty zone.

This can produce a deterministic bad sequence:

~~~text
two configured roles share zone corp
Enable writes journal + managed config
marker write fails because the helper left an empty zone
transaction returns error with recovery journal
restart rolls forward using transition.Zones, which were deduplicated
recovery now writes a valid one-zone marker and commits Managed
but route intent zone corp still maps to two roles and all selected requests
for that zone fail closed
~~~

Thus a failed live Enable can become a committed but unusable managed mode
after restart.

Architecture decision for the fix:

**Do not silently deduplicate an ambiguous role mapping.**

Managed activation requires each non-empty managed zone to map to exactly one
role. Reject duplicate zone ownership before the first durable mutation.

Required implementation:

~~~text
toad/internal/control/mode_transaction.go
    replace managedZoneNames with managedZoneNamesStrict() ([]string, error)

managedZoneNamesStrict:
    collect zone -> role
    reject a zone owned by a second role
    sort unique zone names
    return the exact canonical list

Enable:
    acquire writer lock
    arm in-memory transition gate
    validate current Local state
    call managedZoneNamesStrict
    require no stale checkpoints
    only then create/write journal

Disable:
    use the current committed marker zones as the transaction From-zone set;
    do not re-derive a different set from mutable role config

writeManagedMarker:
    accept the already canonical list
    copy it
    validate it
    do not run the broken in-place pseudo-deduplication
~~~

Remove sortStringsCanonical if it has no other caller.

Required regression:

~~~text
two roles with the same zone
Enable returns a stable precondition error
zero journal
zero marker
zero config change
zero generation change
restart remains Local
~~~

## P0-D3 — D10 negative cases were not proven on both VMs

The authoritative post-D7 packet section 10.5 requires these cases on both
Ubuntu and Windows:

~~~text
writer lock busy -> zero mutation
missing managed auth -> startup fail
malformed managed auth -> startup fail
stale ActivationID -> zero mutation
stale ModeGeneration -> zero mutation
corrupt journal -> startup fail
digest mismatch -> startup fail
second core same StateDir -> fail before socket/routes
~~~

The execution log substitutes unit-test evidence for most of this list.
Only some VM-specific rows were exercised.

That is not equivalent to the required native process proof.

Phase D acceptance is therefore reopened until all eight rows are explicitly
executed on both ubuntu2604 and win10.

## P0-D4 — Ubuntu D10 bypassed the exact Leshy companion preflight

The execution log explicitly records that ubuntu2604 had installed Leshy
0.5.2, which lacks capabilities --json, and the script directly supplied a
v2 capability list to the core.

This bypasses the cross-process preflight contract.

Windows used a fresh Leshy binary; Ubuntu did not.

Required re-run:

~~~text
build/use exact Leshy 61786d489da2acf50d6b4b53a77a39184d746852
on ubuntu2604
run leshy capabilities --json
drive enable through the real kikimora-core leshy-mode enable CLI
do not inject the capability array directly
~~~

Repeat the D10 Ubuntu process matrix after P0-D1/P0-D2 are fixed.

## P1-D5 — D10 VM scripts/evidence are ephemeral

The execution log references tmp/d10_ubuntu_matrix.sh, tmp/d10_win_matrix.ps1
and result files in /tmp/tmp, but those scripts are not committed and the
artifacts are no longer present on the development checkout.

That makes the acceptance hard to reproduce.

Before Phase D re-acceptance, version the native D10 harnesses at stable paths:

~~~text
linux/tests/toad/mode-transition-crash-vm.sh
packaging/windows/test_mode_transition_crash.ps1
~~~

The scripts must accept binary/work directories as arguments and must not
contain credentials or host-specific secrets.

Execution results can remain summarized in the execution log, but the exact
test procedure must be in the repository.

---

# 5. Phase D re-acceptance repair prefix

Do this before further Phase E implementation.

## D10R-1 — durable marker + strict zone authority

Files:

~~~text
toad/internal/control/selected_routes.go
toad/internal/control/mode_transaction.go
toad/internal/control/mode_gate_test.go
toad/internal/control/committed_state_test.go
~~~

Implement P0-D1 and P0-D2 exactly as section 4 specifies.

Suggested commit:

~~~text
fix(control): make managed-mode marker retirement durable and zone authority strict
~~~

Local gates:

~~~text
cd toad
gofmt changed Go files
go vet ./...
go test ./...
go test -race ./internal/control ./internal/routing
go test -tags kikimora_test_failpoints ./internal/control
~~~

## D10R-2 — version the native VM crash harnesses

Add the two stable scripts from P1-D5.

Required properties:

~~~text
no embedded credentials
exact binary SHA/version printed at start
route table baseline hash captured
writer lock path printed
all 9 enable + 9 disable boundaries
all 8 negative cases from section 10.5
final service state restored
foreign route baseline unchanged
nonzero exit if any required row is skipped
~~~

Suggested commit:

~~~text
test(control): version the native managed-mode crash matrices
~~~

## D10R-3 — rerun both VMs on the exact source pair

Use:

~~~text
Kikimora = exact D10R head
Leshy    = 61786d489da2acf50d6b4b53a77a39184d746852
Ubuntu   = ubuntu2604 / 192.168.3.71
Windows  = win10 / 192.168.3.72
~~~

Ubuntu must use the exact current Leshy capabilities command and real CLI
preflight.

Both VM runs must have zero skipped mandatory rows.

## D10R-4 — exact-head CI

Phase D can be ACCEPTED again only when the same exact Kikimora head has:

~~~text
Toad core green
ShellCheck green
Build packages green
ubuntu2604 D10R green
win10 D10R green
~~~

Leshy 61786d4 already has a fully green attempt-3 CI run including macOS
integration; append this correction to the execution log.

---

# 6. Phase E audit — current Linux work is not an implementation

Current Phase E commit:

~~~text
9a4a243 feat(packaging): orchestrate the Linux one-writer managed cutover
~~~

Changed only:

~~~text
.github/managed-routing-companion.env
linux/tests/test_release_packaging.sh
linux/tests/toad/managed-mode-cutover-vm.sh
~~~

No product/install path calls:

~~~text
leshy-mode enable
leshy-mode disable
EnableManagedLeshy
DisableManagedLeshy
~~~

outside tests.

Therefore the commit adds a test scenario, not product cutover orchestration.

Phase E Linux is not implemented yet.

## E0 — fix the VM harness before using it

File:

~~~text
linux/tests/toad/managed-mode-cutover-vm.sh
~~~

### E0.1 status_field is broken

The helper expects a JSON object key, but calls use strings such as:

~~~text
status_field '["mode"]'
status_field '["generation"]'
status_field '["activation_id_fingerprint"]'
~~~

The Python code executes data.get(the_literal_string), so it looks for a key
named ["mode"] instead of mode.

Required fix:

~~~text
status_field mode
status_field generation
status_field activation_id_fingerprint
~~~

and pass the key through argv/environment rather than interpolating it into
Python source.

### E0.2 ShellCheck is red

Fix SC2015 and SC2002 in the script with explicit if statements / direct
file input.

Do not add a ShellCheck exclusion for this new file.

### E0.3 required row G may not SKIP

Current script returns GREEN when ROLE is absent or cannot reach Ready.

That violates the Phase E gate.

Make role + canary domain mandatory VM-test arguments:

~~~text
managed-mode-cutover-vm.sh <role> <canary-domain>
~~~

If the role cannot become Ready, the test is RED.

### E0.4 role Ready is not proof of a non-empty selected registry

Before the Disable-refusal row:

1. ensure managed Leshy is active;
2. query the canary domain through the installed DNS path;
3. poll the public Snapshot for that role;
4. require role.selected_routes.registered > 0;
5. only then invoke Disable and require refusal;
6. DisconnectAll;
7. poll selected_routes.registered == 0 and Parking empty;
8. then Disable must succeed.

No unit-test substitution is allowed for this VM row.

Suggested commit:

~~~text
test(packaging): make the Linux managed cutover gate deterministic
~~~

---

# 7. Phase E Linux product architecture

Do not put an automatic network cutover into Debian postinst.

Reason: dpkg install/upgrade can run over a remote connection. An implicit
routing-authority switch in postinst is an unnecessary remote-lockout risk.

The explicit operator product path is kk start / Go-owned orchestration.

Current entry:

~~~text
linux/files/kikimora-cli/service.sh
    cmd_service

linux/files/kikimora-cli/orchestration.sh
    orch_core_service_action
~~~

Today the Go-owned start path is only:

~~~text
systemctl start kikimora-core.service
~~~

and systemd Wants starts Leshy behind the core readiness barrier.

## E1 — implement an explicit managed-pair start helper

Add a helper in orchestration.sh, conceptually:

~~~text
orch_start_managed_pair
~~~

Required state machine:

~~~text
1. start kikimora-core.service
   - its Wants may start Leshy in local mode

2. wait for core startup readiness

3. inspect leshy-mode status with the state-dir auth key

4. if already Managed:
       require leshy.service active
       verify generated config generation/fingerprint contract
       return success

5. if Local:
       require current Leshy binary capabilities --json contains
           af_unix_control
           managed_selected_routes_v2
           privileged_control_auth_v1

       ensure local leshy.service is active and owns the shared writer lock

       stop leshy.service gracefully

       prove the writer lock is free

       invoke:
           kikimora-core leshy-mode enable
               --leshy /usr/local/bin/leshy
               --socket /run/kikimora/core.sock
               --auth-file /var/lib/kikimora/core/control-route-auth.key
               --control-socket /run/kikimora/core.sock

       start leshy.service
       wait active
       verify mode Managed and transition none

6. any failure before Enable commit:
       restart local Leshy
       return failure

7. any failure after Managed commit:
       keep the system fail-closed
       do NOT start local Leshy against a managed marker
       print an explicit recovery command/status
       return failure
~~~

Do not auto-DisconnectAll in this failure path because it changes operator
desired role state. Recovery to Local is a separate explicit administrative
action after proving the selected registry empty.

Wire Go-owned cmd_service start and enable --now through this helper.

restart must preserve the already committed mode; it must not generate a new
ActivationID merely because services restart.

stop must stop the pair without silently disabling managed mode.

## E2 — package contract tests

Extend:

~~~text
linux/tests/test_release_packaging.sh
linux/tests/toad/console-orchestration.sh
linux/tests/toad/orchestration-cutover.sh
~~~

Prove the installed CLI calls the managed-pair helper for Go-owned start, and
that package postinst itself does not perform the cutover.

## E3 — Ubuntu installed-package acceptance

After E1/E2 and a green exact-head package build:

1. install that exact deb on ubuntu2604;
2. use kk start, not direct test-only leshy-mode calls, for the success path;
3. run the repaired managed-mode-cutover-vm.sh as a verifier;
4. require zero SKIP;
5. record package SHA256 and source heads.

Only then is Phase E Linux accepted.

---

# 8. Phase E Windows remains not started

Do not begin Windows installer cutover until D10R is green and Linux E0-E3 is
accepted.

Then follow the existing authoritative Phase E Windows plan:

~~~text
packaging/windows/install.ps1
packaging/windows/uninstall.ps1
packaging/windows/stage.ps1
packaging/windows/test_package.ps1
packaging/windows/kikimora.wxs
~~~

Required Windows architecture remains:

~~~text
install/start KikimoraCore first
wait core ready
install Leshy with DependsOn=KikimoraCore
local Leshy preflight
graceful stop
writer lock proof
schema2 Enable
managed Leshy start
exact generation/fingerprint verification
~~~

Use win10 for the native gate.

---

# 9. Future release blockers still intentionally open

These are not regressions from the new commits, but they remain hard stops
for later phases:

~~~text
Ready-Toad is not implemented yet
process recovery barrier is not implemented yet
TestTriggerManagedRecovery is not implemented yet
Windows managed-success E2E script is not implemented yet
Windows selected-route K15 privileged skeleton tests remain skipped
v1 runtime compatibility is not removed yet
Leshy is still version 0.5.2 until Phase J
Kikimora is not yet the final 1.4.0 candidate
~~~

Do not close those by deleting/ignoring tests.

---

# 10. Corrected execution order

From audited head 9a4a243:

~~~text
D10R-1 durable marker removal + strict managed-zone authority
-> D10R-2 version native D10 VM harnesses
-> D10R-3 ubuntu2604 + win10 complete native rerun
-> D10R-4 exact-head Toad/ShellCheck/package green
-> Phase D re-acceptance

-> E0 repair deterministic Linux cutover verifier
-> E1 implement product cutover on kk start / Go-owned orchestration
-> E2 package/orchestration contract tests
-> E3 ubuntu2604 exact-deb acceptance with zero skips
-> Phase E Linux accepted

-> Phase E Windows implementation + win10 acceptance
-> Phase E accepted

-> Phase F
-> Phase G
-> Phase H
-> Phase I
-> Phase J
-> Phase K
-> Phase L
~~~

Phase F must not start before both Linux and Windows Phase E are accepted.

---

# 11. Evidence format for the next executor

After every repair slice append to the execution log:

| Field | Required |
|---|---|
| Stage | D10R-1 / E0 / ... |
| Kikimora SHA | exact 40-char |
| Leshy SHA | exact 40-char |
| Local gates | exact commands + PASS/FAIL |
| Toad CI | run ID + conclusion |
| ShellCheck | run ID + conclusion |
| Package CI | run ID + all jobs |
| ubuntu2604 | versioned script + result |
| win10 | versioned script + result |
| Skipped mandatory rows | must be 0 |
| Foreign route drift | must be 0 |
| Next stage allowed | YES/NO |

A failed required row means NO. Do not continue by substituting a unit test
for a required native VM case.

---

# 12. Final audit summary

The large D7R-D10 update is not wasted: most of the original post-D7 source
defects were genuinely repaired, and Leshy 61786d4 now has a completely green
cross-platform CI run.

But the project is **not yet safely past Phase D / into Phase E** because:

1. Managed -> Local marker deletion is not durable.
2. Duplicate managed zones can produce a failed Enable that later recovers
   into an unusable managed activation.
3. Mandatory D10 negative cases were not executed on both VMs.
4. Ubuntu D10 bypassed the exact Leshy capability preflight.
5. D10 native scripts/evidence are not versioned.
6. Current Kikimora master has a red ShellCheck.
7. The new Linux Phase E harness has a broken status parser.
8. Its required Disable/non-empty-registry row may SKIP and still return GREEN.
9. The product itself has no Linux managed cutover orchestration; only the
   test script calls leshy-mode enable/disable.

The next executor starts at D10R-1 above.
