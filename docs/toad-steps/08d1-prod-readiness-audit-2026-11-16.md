# Production-readiness audit — core ↔ Leshy, profiles, dual VPN and Desktop UI — 2026-11-16

Status: **HISTORICAL FIRST 2026-11-16 AUDIT — CURRENT CONTINUATION MOVED**

> **Superseded for live status by**
> `docs/toad-steps/08d1-post-e7ec9c2-t1-audit-2026-11-16.md`.
> The implementation commits PROD1-PROD5/UIR1-UIR5 remain useful, but the
> post-e7ec9c2 audit reopened profile durability, bypass transition fencing,
> Leshy leave/rejoin invalidation, restart acceptance and the all-OS Desktop
> gate. Run 43881525349 is **not** globally green: its Windows job failed.
> `docs/roadmap.md` owns the current AUD1 entry point.

Baseline audited:

~~~text
Kikimora HEAD/origin/master:
9654de2498c33dba425e61c823d03ba6fdc9a909
test(desktop): add the U10 high-DPI scenario; it passes at any DPR

Pinned Leshy:
61786d489da2acf50d6b4b53a77a39184d746852
repository: smollgreymouse/leshy
~~~

This audit does not accept a release. It corrects the continuation point after
the executor reached U10/U11 while several earlier production gates were still
open.

The product goal is now explicit:

> A release is not production-ready until the installed product can keep two
> independent VPN roles active simultaneously, route distinct selected traffic
> through both, apply the global default/bypass route safely, switch profiles
> repeatedly (including rapid A → B → A), survive restart/reconnect/reboot, and
> expose every supported runtime mutation through the real Desktop UI without
> stale or optimistic state escaping the core truth.

The master ordering remains `docs/roadmap.md`. This packet owns the new
2026-11-16 findings and exact repair points.

---

## 1. What is healthy on the audited head

Local exact-head checks run during this audit:

~~~text
go -C toad test ./...                         PASS
go -C toad test -race ./internal/control      PASS
~~~

Current GitHub Actions for 9654de2:

~~~text
ShellCheck       43821956780  SUCCESS
Toad core        43821956771  SUCCESS
Build packages   43821956794  SUCCESS
Desktop UI       43821956899  FAILURE
~~~

The Toad core workflow includes a green `linux-multi-toad-interop` job. The
versioned harness `linux/tests/toad/multi-toad-interop.sh` runs AWG, Xray and
OpenConnect concurrently, proves payload routes for each tunnel, and proves
that failure/recovery of one does not take down the siblings.

That is valuable low-level evidence, but it is below the product boundary:
there is no real Kikimora core, profile switch, managed Leshy authority or
Desktop UI in that harness.

G1 standalone Leshy evidence recorded in the execution log is also useful, but
G2-G6 and the Windows H vertical are not complete.

---

## 2. P0 — rapid profile switching has a confirmed latest-intent race

### Reproduction

A temporary audit-only test was created, run, and then removed. It did:

~~~text
roles: one, two
work profile: [two]

ConnectRole(one)
SetActiveProfile(work)
SetActiveProfile(default)      # immediately, before the first switch settles
wait for background settlement

expected:
    one.desired_enabled = true
    one.process != nil

actual:
    one.desired_enabled = true
    one.process == nil
~~~

Command:

~~~text
go -C toad test ./internal/control   -run TestAuditRapidProfileSwitchLeavesDesiredRoleRunning -count=5
~~~

It failed in all five top-level runs, usually within attempts 0-3, with:

~~~text
rapid work->default left role one desired enabled with no process
~~~

The temporary test file was removed after the audit; the working tree was clean
again apart from the user's pre-existing untracked files.

### Root cause

Entry point:

~~~text
toad/internal/control/control.go
Manager.SetActiveProfile
Manager.startRoleProcess
Manager.stopRoleProcess
Manager.wait
~~~

`SetActiveProfile` persists the new desired state and intentionally settles
changed roles in a background goroutine. That solved the old 5.6 second
synchronous profile-switch UI stall, but the goroutine has no switch generation.

The failure sequence is:

~~~text
switch A -> B:
    marks role one disabled
    queues async stop(one)

switch B -> A:
    marks role one enabled
    async start(one) sees the OLD process still present
    startRoleProcess therefore no-ops

old A -> B goroutine finally runs:
    stopRoleProcess(one) clears/stops the process

final state:
    latest desired intent = enabled
    process = nil
~~~

`releaseRoleResources(..., skipIfEnabled=true)` protects publication/resource
release after re-enable, but it runs after the process stop and therefore cannot
save the process. The deliberate stop is also invisible to the normal wait/retry
path because `stopRoleProcess` clears the process pointer.

### Required product fix — PROD1

Do not make profile switching synchronous again.

Add a monotonic profile-settlement generation owned by `Manager`, for example:

~~~text
toad/internal/control/control.go

Manager:
    profileSwitchEpoch uint64

SetActiveProfile:
    validate
    mutate/persist desired state
    profileSwitchEpoch++
    capture epoch
    launch settlement with (role, wanted, epoch)
~~~

Add one dedicated compare-and-act path instead of calling unguarded
`stopRoleProcess` from stale switch goroutines:

~~~text
settleProfileRole(ctx, roleName, wanted, epoch)
stopRoleProcessForProfile(roleName, epoch)
~~~

Before a profile-owned start/stop mutates the process pointer it must hold
`m.mu` and prove:

~~~text
epoch == m.profileSwitchEpoch
role.enabled == wanted
~~~

If either is false, the settlement is stale and exits without process/resource
mutation.

Explicit `DisconnectRole` still needs a force-stop path; do not globally change
`stopRoleProcess` to "skip when enabled".

After the latest settlement finishes, every enabled role must be in exactly one
of the normal executable states:

~~~text
process != nil
OR start/recovery attempt is scheduled
OR an explicit failed state with a retry/error visible in Snapshot
~~~

Never allow `desired_enabled=true` + no process/retry/error merely because a
stale profile goroutine won.

### Mandatory tests

Add permanent tests near:

~~~text
toad/internal/control/control_test.go
toad/internal/control/profile_switch_release_test.go
~~~

Required cases:

~~~text
TestRapidProfileSwitchLatestIntentWins
    work -> default immediately, repeat enough to exercise scheduling
    final role one enabled + process present

TestRapidProfileSwitchABA
    A -> B -> A
    common role remains up
    departing role is stopped only if still departed
    rejoining role returns up

TestRapidProfileSwitchDuringStart
TestRapidProfileSwitchDuringRecovery
TestStaleProfileTeardownDoesNotReleaseReenabledRoleResources
~~~

Run the focused cases under `-race`.

Desktop real-core coverage must add the same semantic path in:

~~~text
desktop/tests/RealCoreMatrixTest.cpp
~~~

Do not only test a switch after waiting for the previous one to settle.

---

## 2.1 P0 — editing membership of the active profile does not reconcile running roles

A second temporary audit-only test was created, run once and removed. It:

~~~text
created profile work = [one, two]
SetActiveProfile(work)
SetProfileRoles(work, [one])
~~~

Expected after the membership mutation:

~~~text
two.desired_enabled = false
two process/resources settle down
~~~

Actual:

~~~text
two.desired_enabled = true
~~~

The test failed exactly with:

~~~text
role two remains desired enabled after removal from active profile
~~~

Root cause:

~~~text
toad/internal/control/config_api.go

Manager.SetProfileRoles
Manager.SetTunnelProfiles
~~~

Both rewrite membership and rebuild `profileToRoles`, but neither re-applies
the already active profile to desired/process state.

This is the same product invariant as PROD1 and must be fixed by the same
central profile reconciler, not by duplicating start/stop logic.

### Required PROD1 extension

Refactor the composition logic currently embedded in `SetActiveProfile` into
one internal transaction/reconciliation path, for example:

~~~text
applyActiveProfileIntentLocked(...)
scheduleProfileSettlement(epoch, deltas)
reconcileActiveProfile(...)
~~~

Call it from:

~~~text
SetActiveProfile
SetProfileRoles                when the edited profile is active
SetTunnelProfiles              when membership in the active profile changes
RestoreDesiredState            where appropriate for the same invariant
~~~

Invariant after every successful profile/membership mutation:

~~~text
for every loaded role:
    role.enabled == profileContains(activeProfile, role)
~~~

and the epoch-fenced settlement from PROD1 converges the processes/resources to
that desired truth.

`SetProfileRoles` also writes multiple role TOMLs sequentially. A failure after
one successful file write can leave disk membership partially updated while
memory is intentionally left old. Make the multi-role membership write
transactional:

~~~text
snapshot original bytes for every affected role config
write all atomically per file
on any failure restore every already-written original
only then mutate in-memory membership + desired state
~~~

Prefer a small helper in:

~~~text
toad/internal/config/create_profiles.go
~~~

with orchestration in:

~~~text
toad/internal/control/config_api.go
~~~

Mandatory permanent tests:

~~~text
editing active profile removes role -> desired false + process/resource teardown
editing active profile adds role -> desired true + process start
editing inactive profile does not change current processes
SetTunnelProfiles removing active membership reconciles immediately
SetTunnelProfiles adding active membership reconciles immediately
mid-SetProfileRoles write failure restores every touched file and memory
rapid membership edit + profile switch obeys the newest profileSwitchEpoch
~~~

---

## 3. P0 — managed catch-all/bypass zone is not mapped back to its role

### Confirmed cross-repo contract

Kikimora generates the global catch-all as a synthetic Leshy zone:

~~~text
toad/internal/config/routing_lists.go

zone name: "bypass"
pattern:   ".*"
route target: publication file of the selected role
~~~

The pinned Leshy managed backend at
`61786d489da2acf50d6b4b53a77a39184d746852` sends the matched
`ZoneConfig.name` in `EnsureLeshyRoute` / `ForgetLeshyRoute`. The backend
wire contract explicitly carries only zone/prefix/source.

Kikimora's managed resolver is:

~~~text
toad/internal/control/selected_routes.go

newSelectedRouteManager(specs):
    zoneRoles[spec.LeshyZone] -> role

ensure/Forget/FlushZone:
    candidates := zoneRoles[intent.Zone]
    require exactly one
~~~

There is no synthetic `"bypass"` mapping.

Therefore in managed mode, a DNS answer matched by the generated catch-all zone
can reach the core as:

~~~text
zone = "bypass"
~~~

and be denied as an unknown zone even though the generated Leshy config points
the zone at a real role.

G2 currently cannot detect this because
`linux/tests/toad/managed-leshy-routing-netns.sh` hardcodes one ordinary role
(`primary`) and does not exercise the catch-all zone.

### Required product fix — PROD2A

Keep ordinary role-zone mapping immutable, but make the synthetic bypass owner
a first-class piece of runtime truth.

Recommended shape:

~~~text
Manager:
    cached bypassMode / bypassRole guarded by m.mu

SelectedRouteManager:
    resolveZoneRole(zone string) instead of raw map indexing at every call

resolveZoneRole:
    ordinary zone -> immutable zoneRoles
    "bypass"      -> current validated bypass role
~~~

Do not read the bypass file from disk on every DNS route intent.

All three managed operations must use the same resolver:

~~~text
EnsureLeshyRoute
ForgetLeshyRoute
FlushLeshyZone
~~~

Changing bypass owner must not leave selected records owned by the old role.
Before switching `roleA -> roleB`, retire/flush the synthetic bypass zone under
the old owner, then commit the new owner/config/reload, then accept new intents.

The transaction needs rollback or a fail-closed visible error if config rewrite
or Leshy reload fails. "saved, but Leshy did not restart" is not sufficient for
a route-authority mutation that changes the default path.

Concrete code points:

~~~text
toad/internal/control/config_api.go
    Manager.BypassMode
    Manager.SetBypassMode
    Manager.RegenerateLeshyConfig

toad/internal/control/selected_routes.go
    newSelectedRouteManager
    ensure

toad/internal/control/selected_transactions.go
    Forget
    FlushZone

toad/internal/control/api.go
    SetBypassMode command/reload result
~~~

### Mandatory tests

~~~text
managed bypass intent maps to the selected role
bypass Ensure/Forget/Flush all use the same owner
bypass role A -> B drains A before B accepts the prefix
unknown/nonexistent bypass role is rejected before persistence
reload failure does not silently expose a half-switched authority
same prefix claimed by two ordinary roles remains a deterministic conflict
~~~

---

## 4. P0 — active profile and global bypass can contradict each other

The UI correctly offers default-route choices only from roles in the currently
visible profile:

~~~text
desktop/qml/Main.qml
desktop/qml/Controls/KDefaultRouteRow.qml
~~~

But the core stores one global bypass mode and `SetActiveProfile` does not
validate it.

Today the following state is legal:

~~~text
bypassMode = secondary
active profile = primary-only
secondary.enabled = false
~~~

The generated catch-all still points at the secondary role while that profile
has disabled the tunnel.

### Required safety invariant — PROD2B

Keep bypass global for this release; do not silently redesign profiles into a
new persistence schema while closing production readiness.

Enforce:

~~~text
SetBypassMode("direct")              -> always valid
SetBypassMode(role)                  -> role must exist AND be enabled by
                                        the active profile

SetActiveProfile(profile):
    if current bypass != direct:
        target profile must contain the bypass role
        otherwise reject BEFORE changing/persisting desired state
~~~

This is fail-closed and contains no hidden profile mutation.

It also keeps the UI workflow possible:

~~~text
set default route -> direct
switch to another profile
choose that profile's default role
~~~

The Desktop UI must surface the core command error and resync its optimistic
profile value. Add a real-core matrix case for incompatible profile selection.

A future feature may persist a default route per profile, but that is a product
redesign and is not required to make the existing global setting safe.

---

## 5. P0 — Windows in-flight managed transition still cannot survive state-root migration

C8 remains open.

Current transition journal schema 1 hashes the exact generated Leshy config
bytes:

~~~text
toad/internal/control/mode_transition.go
    ManagedLeshyTransition.ConfigSHA256
    configDigest

toad/internal/control/mode_transaction.go
    rollForwardTransition
    verifyTransitionTarget
~~~

The generated config embeds:

~~~text
kikimora_auth_file = <stateDir>/control-route-auth.key
~~~

R1 changes the Windows state root from:

~~~text
C:\ProgramData\Kikimora\state
->
C:\ProgramData\Kikimora\state\core
~~~

An interrupted `config_written` transition therefore carries a digest that can
never equal the re-rendered post-migration config, even when every semantic
setting is identical. The current unit
`state_root_move_recovery_test.go` deliberately accepts/documented refusal;
that is useful evidence but not production-ready upgrade behaviour.

### Required fix — PROD3

The release contract is **automatic convergence**, not a permanent service-start
loop and not a documented manual recovery requirement.

Introduce a versioned transition digest contract.

Recommended implementation:

~~~text
ManagedLeshyTransition schema 2:
    ConfigDigestVersion = 2
    ConfigSHA256        = semantic/path-independent digest
~~~

The v2 digest must retain every security-relevant generated setting while
normalizing only the core-owned state-root relocation, specifically the
`server.kikimora_auth_file` root. Do not simply drop the entire server block.

Use parsed TOML/canonical structured data rather than a broad string replace.
The project already has BurntSushi/toml.

For schema-1 journals encountered during R1 migration, add an explicit
compatibility upgrader before normal recovery:

~~~text
toad/cmd/kikimora-core/state_layout_windows.go
toad/cmd/kikimora-core/service_windows.go
toad/internal/control/mode_transition.go
~~~

The upgrader must first prove the old journal/config relation using the legacy
v1 digest where possible, then write a durable v2 journal with the normalized
digest. It may not "accept any mismatch".

The native C8 row must then require:

~~~text
legacy config_written journal
-> state layout migration
-> installed candidate service start
-> roll-forward convergence
-> generation committed
-> journal removed
-> managed authority Ready
~~~

Change `TestRecoverManagedModeTransitionAcrossStateRootMove` from
"either refusal or convergence is accepted" to convergence-only.

---

## 6. Product-level dual-VPN/profile proof is missing

### Existing proof

Good:

~~~text
linux/tests/toad/multi-toad-interop.sh
    three raw Toads simultaneously
    independent payload paths
    independent failure/recovery

linux/tests/toad/managed-leshy-routing-netns.sh
    real core + pinned Leshy managed selected routing
    strong drift/reconciliation semantics
~~~

Missing:

~~~text
real core
+ two concurrently enabled roles
+ two Leshy zones
+ selected routes through both at the same time
+ global bypass/default owner
+ active profiles
+ rapid profile switching
+ persistence/restart
in one acceptance chain
~~~

The current managed harness has one role only.

### Add PROD4 deterministic harness

Add:

~~~text
linux/tests/toad/managed-dual-profile-netns.sh
~~~

Use real Kikimora core + pinned Leshy + two Ready-Toads/deterministic role
fixtures with distinct interfaces and publications.

Required profiles:

~~~text
both           [primary, secondary]
primary-only   [primary]
secondary-only [secondary]
~~~

Required route data:

~~~text
primary.test      -> primary zone -> primary interface
secondary.test    -> secondary zone -> secondary interface
catch-all         -> selected global bypass role
A + AAAA where enabled
~~~

Required rows:

~~~text
D1  both roles Ready simultaneously
D2  both publication files exist and generations are stable
D3  primary.test selected A/AAAA routes point only to primary interface
D4  secondary.test selected A/AAAA routes point only to secondary interface
D5  D3 and D4 coexist in the kernel/registry at the same time
D6  catch-all "bypass" intent maps to the chosen enabled role
D7  disconnect/recover primary leaves secondary traffic/ownership unchanged
D8  disconnect/recover secondary leaves primary traffic/ownership unchanged
D9  live routing-list edit for primary does not churn secondary
D10 per-role IPv6 toggle for one role does not alter the sibling
D11 profile both -> primary-only removes only secondary-owned resources
D12 profile primary-only -> both restores secondary and routes
D13 rapid both -> primary-only -> both: latest intent wins
D14 incompatible profile switch while bypass points at departed role is rejected
D15 set bypass direct -> switch profile -> choose new bypass role succeeds
D16 core restart restores active profile + both desired roles + selected ownership
D17 same selected prefix claimed by two roles produces deterministic conflict
    without mutating the first owner's route
D18 DisconnectAll drains both roles and all owned selected records
~~~

This harness belongs before candidate packaging acceptance. It is not optional
because simultaneous multi-VPN is a product headline feature.

### Windows native companion gate

Add:

~~~text
packaging/windows/test_dual_profile.ps1
~~~

It need not duplicate every deterministic netns negative, but it must prove on
the native service:

~~~text
two real lab roles Ready simultaneously
profile switch changes exactly the desired role set
rapid switch latest intent wins
default/bypass owner stays compatible
one role disconnect does not break the sibling
service restart restores the selected profile
~~~

Record exact source/artifact hashes.

---

## 7. Desktop UI / UI↔core status on current head

### Implemented well

Source inspection plus existing tests show the intended architecture is mostly
present:

~~~text
CoreBackend / RealCoreClient:
    API v3 compatibility
    authoritative-snapshot command channel
    safeToMutate
    safeToDisconnect
    diagnosticsAvailable
    diagnosticsMutable
    reconnect clears actionable truth

QML/tray:
    mutation gates consume CoreBackend policy
    role action drawer
    profile selector
    profile membership editing
    routing-list edit
    default-route chooser
    per-role IPv6
    diagnostics
    tray aggregate action
    local clipboard import

LiveCoreFixture:
    hermetic runtime paths and real core IPC
~~~

`RealCoreMatrixTest` already covers normal sequential real-core paths for:

~~~text
role action
settings state
profile switch
create tunnel
routing lists
role IPv6
bypass mode
diagnostics capture
~~~

Fake-QML coverage is broad and includes import/profile creation/membership,
routing drafts, authority failure, family readiness and diagnostics.

### Gaps that still block release

#### UI1 — rapid profile switching is not tested through the real UI/core

Current `profileSwitchDrivesTheCore` waits for each switch before the next one.
Add a rapid latest-intent case after PROD1.

#### UI2 — not every exposed mutation has a real-core matrix row

Add real-core tests for:

~~~text
CreateProfile
SetTunnelProfiles / move role between profiles
ImportTunnel
RemoveTunnel
ValidateRole
RediscoverEndpoints
incompatible profile/bypass error + resync
~~~

The FakeCore UI tests remain useful but do not replace these IPC/product
round-trips.

#### UI3 — U8i is absent

Required file does not exist:

~~~text
desktop/tests/guest-run-suites.sh
~~~

Implement the exact fail-closed installed-Linux runner already specified in the
UI executor packet.

#### UI4 — U9i Windows runner is still fail-open

Current:

~~~text
desktop/tests/guest-run-suites.ps1

$ErrorActionPreference = 'Continue'
readiness timeout -> prints message then continue
no aggregate failure/skip count
no mandatory zero-skip check
no final nonzero failure exit
~~~

Repair before U9e.

#### UI5 — U11 is still red

Exact-head audit run:

~~~text
desktop/src line coverage:   76.20%  >= 75
desktop/src branch coverage: 61.20%  >= 60
RealCoreClient.cpp           88.30%  >= 80
RoleListModel.cpp            73.40%  <  80
DesktopIntegration.cpp       67.70%  <  80
~~~

Do not lower thresholds.

Current uncovered concentration:

~~~text
RoleListModel.cpp:
    rowCount(valid parent)
    roleNames() full mapping
    remaining guard branch

DesktopIntegration.cpp:
    trayIcon fallback/crop
    disconnect tray branch
    late tray-host callback
    setWindow/show/hide/quit
    separator hide branch
    ensureTray activation callback
    platform autostart branches
~~~

Concrete U11 tests:

~~~text
RoleListModelTest:
    assert every roleNames() entry, not only six
    rowCount(valid parent) == 0
    invalid update/get/roleAt guards

TrayMenuTest / DesktopIntegrationTest:
    trigger connect AND disconnect menu branches
    exercise setWindow/show/hide with a QWindow
    isolate/test quit state without killing the test process via a test seam
    exercise role list -> empty list separator hiding
    expose tray-icon crop helper or resource-loader seam for deterministic test
    make autostart storage testable on both Windows and Unix
~~~

Coverage must be re-measured after the CI portability fix below.

#### UI6 — U10 golden set is not the pinned CI set

Tracked goldens currently exist only under:

~~~text
desktop/tests/golden/linux-6.9.2/
~~~

The roadmap/CI reference lane is Qt 6.8.3. Generate/review and commit the
`linux-6.8.3` set on the pinned renderer before calling U10 accepted.

---

## 8. Current Desktop UI CI is red on every OS

Workflow:

~~~text
Desktop UI run 43821956899 — FAILURE
~~~

Linux and macOS fail before suites execute because:

~~~text
desktop/tests/run-required-suites.sh
git mode = 100644
workflow executes it directly
=> Permission denied
~~~

Fix:

~~~text
git update-index --chmod=+x desktop/tests/run-required-suites.sh
git update-index --chmod=+x desktop/tests/run-coverage.sh
~~~

and assert shell-helper executable mode in a small CI/source contract so it
does not regress.

Windows reaches the suites but fails:

~~~text
TrayMenuTest::autostartEntryIsCreatedAndRemoved
autostart entry missing
~~~

That test was written against the Unix XDG file model, while
`DesktopIntegration` uses the HKCU Run key on Windows.

Do not QSKIP Windows. Introduce a testable autostart storage seam or a
test-specific Windows registry root, then assert the platform contract:

~~~text
Windows:
    enable writes "<exe>" --background
    autostartEnabled == true
    disable removes the test value
    all-users shortcut branch remains separately covered

Unix:
    XDG .desktop file created/removed with valid Exec
~~~

The next Desktop UI workflow must be green on all three OSes with zero mandatory
skips/warnings.

---

## 8.1 Release documentation is materially stale

The root README still presents Kikimora primarily as the old Linux
shell/systemd routing layer, links the generic `ftelnov/leshy` repository and
shows old product/version/platform badges. Current production reality includes
the Go core, managed selected-route authority, Desktop UI, profiles and native
Windows packaging.

This does not block PROD1 implementation, but it **does** block final release
truthfulness.

Add a release-documentation gate before L:

~~~text
README.md
docs/lab-vms.md
platform install docs
release notes
~~~

README must describe, from the exact candidate:

~~~text
current supported release platforms
real core + Toad + pinned Leshy architecture
two-VPN/profile model
global default/bypass semantics
managed-routing safety/failure state
Desktop UI capabilities
actual package/install commands
actual release version
~~~

Do not publish L while the README advertises an architecture/version that is
not the candidate being shipped.

---

## 9. VM helper is infrastructure, not tribal knowledge

Every agent must start from:

~~~text
scripts/lab/vm.py
docs/lab-vms.md
~~~

Do not rediscover addresses by scanning old logs and do not create another
`tmp/vm_*.py` helper.

Canonical commands:

~~~text
python3 scripts/lab/vm.py discover ubuntu --source
python3 scripts/lab/vm.py discover windows --source

python3 scripts/lab/vm.py run ubuntu "<command>"
python3 scripts/lab/vm.py run windows "<command>"

python3 scripts/lab/vm.py put ubuntu <local> <remote>
python3 scripts/lab/vm.py put windows <local> <remote>
~~~

Resolution is already implemented:

~~~text
--host
-> KIKIMORA_UBUNTU_VM_HOST / KIKIMORA_WINDOWS_VM_HOST
-> VirtualBox guest-property discovery
-> documented fallback (.74 Ubuntu / .72 Windows)
~~~

The root repository must make this impossible to miss. This audit requires:

~~~text
AGENTS.md                         new root read-first file
README.md                         top-level agent/developer pointer
docs/roadmap.md                   current-entry reminder
docs/lab-vms.md                   explicit "do not invent another helper" rule
~~~

An agent should only investigate transport after the canonical helper itself
returns a concrete failure.

---

## 10. Revised production-ready execution order

The executor must **stop treating U11 as the current entry point**.

Current entry point after this audit:

~~~text
PROD1  fix profile composition: rapid latest-intent + active-membership reconciliation
PROD2  fix managed bypass-zone ownership + profile/bypass compatibility
PROD3  make state-root-migration transition recovery converge (C8 blocker)

PROD4  add deterministic real-core + Leshy dual-role/profile harness
PROD5  extend real-core Desktop matrix for rapid profiles + missing mutations

UIR1   repair Desktop UI CI portability (script modes + Windows autostart test)
UIR2   implement U8i Linux installed runner
UIR3   harden U9i Windows installed runner
UIR4   finish U11 without lowering thresholds
UIR5   produce pinned Qt 6.8.3 U10 goldens

T1     close remaining harness identity/static evidence
X1     rerun exact combined-head hosted/nonprivileged gates

G1     retain/re-run standalone Leshy on the exact combined head if source/pin changed
G2     managed Linux success, including PROD4 dual-profile rows
G3-G6 Linux crash/reboot/package vertical

H1-H6 Windows native vertical, including test_dual_profile.ps1 and fixed C8 upgrade

I1-I3 v2-only cut + both VM smokes

J1-J5 immutable candidate
U8e/U9e installed Desktop acceptance against that candidate
U12 evidence closure
K real Linux workstation canary
L final release
~~~

P1's packaging architecture is substantially implemented and Build packages is
green on 9654de2, but every product/core fix above changes the exact candidate
lineage. The Build packages gate is therefore rerun as part of X1; old success is
not candidate evidence.

U10 implementation work can be retained, but its acceptance remains open until
the pinned 6.8.3 golden lane is actually green.

---

## 11. Product feature acceptance matrix before J

Before release candidate creation, evidence must exist for every row below.

| Feature | Core/unit | Real core hermetic | Linux native | Windows native | Installed UI |
|---|---|---|---|---|---|
| Connect/disconnect all | required | required | required | required | required |
| Per-role connect/disconnect | required | required | required | required | required |
| Retry/validate/rediscover | required | required | smoke | smoke | required |
| Two VPN roles simultaneously Ready | required | PROD4 | required | required | observe |
| Independent selected routing through both | required | PROD4 | required | required | diagnostics |
| Global default/bypass direct | required | required | required | required | required |
| Global default/bypass through role | PROD2 | PROD4 | required | required | required |
| Create profile | required | real-core matrix | smoke | smoke | required |
| Switch profile | PROD1 | PROD4 | required | required | required |
| Rapid A→B→A profile switch | PROD1 | PROD4 | required | required | required |
| Role membership edit | required | real-core matrix | smoke | smoke | required |
| Create/import/remove tunnel | required | real-core matrix | smoke | smoke | required |
| Routing-list edit | required | real-core matrix | required | required | required |
| Per-role IPv6 toggle | required | PROD4 | required | required | required |
| Managed authority loss/recovery | required | existing layers | required | required | required |
| Core reconnect/restart restores truth | required | PROD4 | required | required | required |
| Diagnostics read/capture | required | existing matrix | smoke | smoke | required |
| Tray connect/disconnect | required | existing tray live | smoke | smoke | required |
| Autostart | platform unit | n/a | installed | installed | required |
| Package fresh/upgrade/reboot | n/a | n/a | G4-G6 | H3/H6 | U8e/U9e |

A row cannot be closed by FakeCore evidence alone when the feature mutates
production state.

---

## 12. Stop rules

No J candidate while any of these is true:

~~~text
rapid profile switch can strand desired-enabled role
active-profile membership edit can leave running/desired composition stale
managed bypass zone is not authoritative
active profile may disable its configured bypass role
state-root transition upgrade is non-convergent
dual-VPN product harness is absent/red
Desktop UI workflow is red
U8i/U9i are not fail-closed
U11 thresholds are red
C8/T1/X1/G/H are open
~~~

The next executor starts at **PROD1 in this packet**, not at U11.
