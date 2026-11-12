# 08D1 post-739e1ab audit remediation plan

Status: **AUTHORITATIVE CORE REMEDIATION PACKET — ordered by docs/roadmap.md**

Master cross-core/UI execution order:

~~~text
docs/roadmap.md
~~~

This packet remains authoritative for R1-R8 implementation details. The master
roadmap decides when these repairs run relative to desktop/UI work and later
G/H/I/J/K/L phases.

Audit base:

~~~text
Kikimora master/origin/master:
    739e1ab3cb9cab359e32ba036057616ce7f10910

Previous local/audit comparison base:
    9765c81763b697a0fbf15f519f67b402cdd1f44d

Pinned Leshy companion for all managed-routing reruns:
    61786d489da2acf50d6b4b53a77a39184d746852

Audit date:
    2026-11-11
~~~

Current evidence addendum (2026-11-12):

~~~text
docs/toad-steps/08d1-current-state-audit-2026-11-12.md
~~~

The 739e1ab findings and required acceptance criteria below remain the
implementation authority. The addendum records the newer source/CI snapshot,
R1–R6 source review, and exact remaining evidence gaps. In particular, the
current tagged Phase F CI selection does not run
`TestRecoveryWithdrawSelectedMarkerOrdering`; do not mark R2/R7 coverage fully
closed until that tagged-only case is selected and its exact test name passes.
The R3 held-marker snapshot assertion and R5 QDCOUNT fixture validation are
also follow-up test/fixture hardening items; these do not change the planned
architecture or hosted-vs-VM boundary.

This packet is the mandatory continuation after the audit of 739e1ab. It
overrides any earlier claim that Phase F is complete or accepted, and it
reopens the Windows Phase E upgrade path while preserving the already-green
fresh-install evidence.

The executor implements this packet exactly. Do not choose a different
migration model, recovery-barrier location, test trigger, Ready-Toad
semantics, DNS fixture, or CI topology. If a language/library detail requires
a signature adjustment, preserve the architecture and invariants below and
record only that mechanical adjustment in the execution log.

Parent architecture:

~~~text
docs/toad-steps/08d1-managed-selected-routes-completion.md
~~~

Parent production plan:

~~~text
docs/toad-steps/08d1-production-readiness-plan.md
~~~

Execution journal:

~~~text
docs/toad-steps/08d1-execution-log.md
~~~

---

# 1. Corrected status and mandatory order

The state at 739e1ab is:

~~~text
Phase D: accepted
Phase E Linux: accepted
Phase E Windows fresh install: accepted
Phase E Windows upgrade: REOPENED
Phase F: REJECTED / REOPENED
Phase G and later: BLOCKED
~~~

Do not start Phase G until every remediation item in this packet is green.

Mandatory landing order:

~~~text
R1  Windows core-state layout migration + upgrade proof
R2  Recovery barrier at the real recovery-step boundary
R3  TestTriggerManagedRecovery through the real production recovery request
R4  Ready-Toad contract completion
R5  Shared DNS fixture repair and deduplication
R6  Windows Row G API-proof correction
R7  CI wiring + release-absence proof
R8  native Windows upgrade rerun + Phase F exact-head re-acceptance
-> Phase G
~~~

Each R-stage is committed separately unless a mechanically inseparable test
change must land with its implementation. Never combine R1 with the Phase F
fixture work.

---

# 2. Audit findings this packet must close

## 2.1 Windows state-root change has no migration

Current production entry point:

~~~text
toad/cmd/kikimora-core/service_windows.go
    serviceOptions()
~~~

The service state root changed from:

~~~text
C:\ProgramData\Kikimora\state
~~~

to:

~~~text
C:\ProgramData\Kikimora\state\core
~~~

without migrating the old core-owned files. A managed install can therefore
start with a surviving schema-2 marker in `toads\leshy-managed.json` but a
missing committed-generation file in the new root. The new root reads as
generation 0 and startup fails closed on marker-generation mismatch.

The existing Windows cutover gate is a fresh-install gate: it moves the old
state aside before installing the candidate and therefore cannot prove an
upgrade.

## 2.2 The Phase F recovery barrier recurses and is on the wrong boundary

Current files:

~~~text
toad/internal/control/recovery_barrier.go
toad/internal/control/recovery_barrier_default.go
toad/internal/control/recovery_barrier_failpoints_testbuild.go
toad/internal/control/recovery_driver.go
~~~

The tagged implementation assigns `recoveryBarrier = recoveryBarrierWait`
and `recoveryBarrierWait` calls `recoveryBarrier`, causing unbounded
recursion and a runtime stack overflow.

The barrier is also invoked inside driver methods before the mutation. The
required acceptance point is after a successful RecoveryStep and before the
next step. In particular, the `withdraw-selected` barrier must observe:

~~~text
selected route absent
exact managed park present and winning
next recovery step not yet started
~~~

## 2.3 TestTriggerManagedRecovery is a stub

Current file:

~~~text
toad/internal/control/recovery_barrier_failpoints_testbuild.go
~~~

`TestTriggerManagedRecoveryImpl` returns nil and does not call the real
production recovery request. No tagged control method currently reaches
`Manager.requestManagedSelectedRecovery`.

## 2.4 Ready-Toad does not satisfy the fixed fixture contract

Current files:

~~~text
toad/internal/testtoad/cmd/ready-toad/main.go
toad/internal/testtoad/cmd/ready-toad/main_test.go
~~~

Known defects:

- test configs use unsupported `protocol = "awg2"`;
- Rebind and RestartTransport are no-ops;
- Start tolerates a missing interface instead of failing startup;
- SessionConnected is never true;
- family readiness requires every configured prefix instead of at least one
  address in the configured family;
- Snapshot.Addresses is not limited to configured families;
- Subscribe test does not cause a revision change;
- files are not gofmt-clean.

## 2.5 New DNS fixture parses QTYPE from QDCOUNT and is not shared

Current files:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
linux/tests/toad/managed-leshy-smoke.sh
~~~

The new stub reads bytes 4:6 as QTYPE; those bytes are QDCOUNT. AAAA queries
therefore receive an A answer. The existing smoke still generates separate
inline Python helpers, so the shared fixture is not actually shared.

## 2.6 Windows Row G does not prove SetRoutingList API

Current files:

~~~text
packaging/windows/test_managed_cutover.ps1
toad/internal/control/testhelper/routeprobe/main.go
~~~

The execution log says Row G uses `routeprobe set-list` /
`SetRoutingList`, but the script edits the list with `Add-Content` and
restarts the core. The current gate proves file-based regeneration, not the
public control API path.

## 2.7 Phase F exit gates are not wired into CI

Current workflow:

~~~text
.github/workflows/toad.yml
~~~

At 739e1ab it does not build the Ready-Toad test artifact, does not run the
tagged Phase F tests, and does not run the Linux managed-success E2E.
The exact-head Toad workflow is already red on gofmt before unit tests.

---

# 3. R1 — Windows core-state layout migration

## 3.1 Architecture

The new layout remains authoritative:

~~~text
C:\ProgramData\Kikimora\state\core
~~~

Do not revert `serviceOptions().stateDir` to the parent directory.

The migration is a one-time, crash-resumable **copy-and-verify** migration.
Do not rename or delete the legacy files. They remain available for package
rollback; after migration completes, only `state\core` is authoritative.

Create:

~~~text
toad/cmd/kikimora-core/state_layout_windows.go
~~~

with these fixed concepts:

~~~go
const windowsCoreStateLayoutVersion = 2

func migrateLegacyWindowsCoreState(root string) error
~~~

Paths:

~~~text
legacy root = <root>\state
new root    = <root>\state\core
completion marker =
    <root>\state\core\core-state-layout.json
~~~

The completion marker is JSON:

~~~json
{"schema":1,"layout":2}
~~~

and must be written with the existing durable fsutil primitive only after all
authority-bearing files have been copied/verified.

Exactly these legacy files participate:

~~~text
desired.json
leshy-mode-generation.json
leshy-mode-transition.json
control-route-auth.key
~~~

Do not migrate:

~~~text
core.lock
leshy-writer.lock
diagnostics/
per-Toad state directories
selected-routes.json files inside per-Toad StateDir
~~~

`leshy-writer.lock` intentionally remains at
`C:\ProgramData\Kikimora\state\leshy-writer.lock`.

Migration algorithm, in this order:

1. create `state\core` if missing;
2. if `core-state-layout.json` exists, strict-read it and require
   schema=1/layout=2; return success without reading legacy authority;
3. for each of the four files above:
   - source absent + destination absent: continue;
   - source present + destination absent: read source bytes and durably write
     identical bytes to destination;
   - source present + destination present: require byte-for-byte equality;
     mismatch is fatal and must not be auto-resolved;
   - source absent + destination present: keep destination and continue;
4. after all files pass, write the completion marker durably;
5. never delete the parent copies.

A crash after copying any subset simply reruns step 3. A conflicting partial
migration fails closed.

## 3.2 Production entry points

Wire the migration in both installed-service paths.

Primary entry:

~~~text
toad/cmd/kikimora-core/service_windows.go
    (*kikimoraService).Execute
~~~

When `s.opts == nil` (real installed service), call
`migrateLegacyWindowsCoreState(windowsProgramDataRoot)` before constructing
the default `serveOptions` and before `runServe` can acquire the new
`core.lock`.

Injected unit-test `s.opts` must not implicitly migrate the real ProgramData
root.

Secondary entry:

~~~text
toad/cmd/kikimora-core/service_windows.go
    serviceCommand("install")
~~~

After creating/hardening the ProgramData directory set and before starting the
service, invoke the same idempotent migration helper. This covers portable
installation and makes the migration error visible before service start.

Do not put the migration in `control.StartupManagedAuthority`; the layout is
Windows packaging policy, not cross-platform managed-mode policy.

## 3.3 Portable installer pre-upgrade status

Update:

~~~text
packaging/windows/install.ps1
~~~

Define both:

~~~text
$CoreStateDir       = "$DataRoot\state\core"
$LegacyCoreStateDir = "$DataRoot\state"
$AuthKey            = "$CoreStateDir\control-route-auth.key"
$LegacyAuthKey      = "$LegacyCoreStateDir\control-route-auth.key"
~~~

Before the old core is stopped, choose the credential path deterministically:

~~~text
new key exists -> use new key
else legacy key exists -> use legacy key
else no prior managed status can be queried
~~~

`Get-LeshyModeStatus` must accept the selected auth path as an argument for
the pre-upgrade query. After the new core starts, all subsequent calls use
`$AuthKey` in `state\core`.

If the prior status was managed, the existing upgrade branch remains:
do not run a new Enable; start Leshy and require the exact prior generation
and activation fingerprint after migration.

## 3.4 R1 unit tests

Add Windows-only tests next to the migration helper.

Required cases:

~~~text
TestWindowsCoreStateMigrationFresh
TestWindowsCoreStateMigrationLegacyLocal
TestWindowsCoreStateMigrationLegacyManaged
TestWindowsCoreStateMigrationResumesPartialCopy
TestWindowsCoreStateMigrationRejectsConflict
TestWindowsCoreStateMigrationCompletionMarkerIsAuthoritative
TestWindowsCoreStateMigrationCopiesInFlightTransition
~~~

Assertions:

- bytes of all four migrated files are exact;
- source files remain;
- completion marker is only present after all copies verify;
- rerun is idempotent;
- conflicting destination bytes return an error;
- once a valid completion marker exists, stale parent copies do not overwrite
  the new root;
- an in-flight `leshy-mode-transition.json` reaches the new root unchanged.

## 3.5 R1 native Windows upgrade gate

Add:

~~~text
packaging/windows/test_state_layout_upgrade.ps1
~~~

This is distinct from the fresh-install A-H gate.

The script must test both legacy-local and legacy-managed layouts against the
candidate MSI/portable payload. It stages the old layout under
`C:\ProgramData\Kikimora\state`, leaves `state\core` absent, then starts
the candidate through the real service.

Managed row must stage a valid matching pair:

~~~text
legacy leshy-mode-generation.json: generation N > 0
toads\leshy-managed.json: same generation N + valid activation id
legacy control-route-auth.key: valid key
legacy desired.json: a configured role desired=true
~~~

Required proof after startup:

~~~text
state\core\core-state-layout.json == schema1/layout2
new auth bytes == old auth bytes
new generation == old generation
managed marker generation/fingerprint unchanged
desired role remains desired
core reaches startup readiness
managed config points at the new auth path
legacy source files still exist
~~~

Also test a staged in-flight transition copied from the old root and require
normal `RecoverManagedModeTransition` convergence.

R1 exit:

~~~text
Windows unit tests green
windows cross-build green
fresh-install A-H gate still green
state-layout upgrade gate green for local + managed + in-flight transition
~~~

Only then mark Windows Phase E fully re-accepted.

---

# 4. R2 — recovery barrier on the real step boundary

## 4.1 Delete the incorrect control-layer barrier

Remove:

~~~text
toad/internal/control/recovery_barrier.go
toad/internal/control/recovery_barrier_default.go
toad/internal/control/recovery_barrier_failpoints_testbuild.go
~~~

and remove every `recoveryBarrierWait(...)` call from:

~~~text
toad/internal/control/recovery_driver.go
~~~

No recovery driver method may contain the process barrier.

## 4.2 Implement the barrier in core

Create:

~~~text
toad/internal/core/recovery_barrier_default.go
toad/internal/core/recovery_barrier_testbuild.go
~~~

Build tags:

~~~text
default:
    !kikimora_test_failpoints

test:
    kikimora_test_failpoints
~~~

Both files provide exactly:

~~~go
func processRecoveryBarrier(ctx context.Context, role string, step RecoveryStep) error
~~~

Default build: immediate nil.

Tagged build reads:

~~~text
KK_RECOVERY_BARRIER_STEP
KK_RECOVERY_BARRIER_ROLE
KK_RECOVERY_BARRIER_DIR
~~~

Rules:

- if STEP is empty or does not equal `string(step)`: return nil;
- if ROLE is non-empty and differs from `role`: return nil;
- on a matching STEP, DIR is mandatory; missing DIR is an error;
- create DIR with 0700 if needed;
- atomically create
  `<DIR>/<role>.<step>.waiting` with O_CREATE|O_EXCL, mode 0600;
- poll every 50ms for
  `<DIR>/<role>.<step>.continue`;
- timeout is 30s, shortened by an earlier context deadline;
- on continue: remove `.continue`, remove `.waiting`, return nil;
- on timeout/cancel: return an error and leave `.waiting` for diagnostics;
- a pre-existing `.waiting` is an error, not an automatic cleanup.

The hook performs zero route/state mutation other than its own marker files.

## 4.3 Exact call site

Modify:

~~~text
toad/internal/core/engine.go
    Engine.Recover
    local work(ctx, step) closure
~~~

Dispatch the existing RecoveryDriver method first. Only when that method
returns nil, call:

~~~go
processRecoveryBarrier(ctx, role, step)
~~~

and return its error if any.

Do not modify the RecoveryStep order in
`ManagedRecoverySequenceForAction`.

This makes the tagged `withdraw-selected` marker mean exactly:

~~~text
FreezeSelected      complete
ObserveRoutes       complete
Park                complete
WithdrawSelected    complete
BARRIER WAITING
Withdraw             not started
~~~

## 4.4 R2 tests

Add core tests under the test tag proving:

~~~text
non-matching step does not wait
non-matching role does not wait
matching step creates .waiting
.continue releases the call
timeout returns error
driver failure does not create .waiting
barrier error prevents the next RecoveryStep
withdraw-selected marker occurs after WithdrawSelected and before Withdraw
~~~

The ordering test must use a recording RecoveryDriver and inspect its recorded
calls at the moment `.waiting` exists.

---

# 5. R3 — TestTriggerManagedRecovery through production recovery

## 5.1 Remove the exported variable/stub model

There must be no common-build symbol named:

~~~text
TestTriggerManagedRecovery
TestTriggerManagedRecoveryImpl
~~~

Delete that model completely.

## 5.2 Production request result

Modify:

~~~text
toad/internal/control/control.go
    Manager.requestManagedSelectedRecovery
~~~

Keep the existing production path and semantics, but change it to return
`bool`:

~~~text
true  = recovery worker was successfully scheduled
false = prerequisites prevented scheduling
~~~

Every current production caller keeps using the same function and may discard
the bool. Do not create a second recovery implementation.

The function still owns:

~~~text
role exists
role enabled
process present
recovery driver present
not already recoveryInFlight
capture underlay epoch + operation
set recoveryInFlight
SetRecovery(RoleRecovering)
goroutine -> recoverRole(...)
~~~

If `SetRecovery` fails, clear recoveryInFlight exactly as today and return
false.

## 5.3 Tagged control method

Create build-tag pair:

~~~text
toad/internal/control/test_control_default.go
toad/internal/control/test_control_testbuild.go
~~~

Both provide an unexported method:

~~~go
func (m *Manager) handleTestControlMethod(
    ctx context.Context,
    request Request,
) (handled bool, err error)
~~~

Default build always returns `false, nil`.

Tagged build recognizes exactly one method string:

~~~text
TestTriggerManagedRecovery
~~~

For that method:

- `request.Role` is mandatory;
- call
  `m.requestManagedSelectedRecovery(request.Role, "test-trigger")`;
- if it returns false, return a deterministic error
  `managed recovery was not scheduled for role "<role>"`;
- if true, return handled=true, err=nil.

No other test method is added.

Modify:

~~~text
toad/internal/control/api.go
    Manager.Handle
~~~

After version/auth validation and before the production switch, call
`handleTestControlMethod`. If handled, finish the response through the same
OK/error formatting used by normal commands and return.

The release build therefore reaches the existing default switch and returns
`unsupported method "TestTriggerManagedRecovery"`.

Do not add the test method to Handshake capabilities.

## 5.4 R3 tests

Tagged unit test must construct a manager with a recording recovery driver and
real product role state, issue the API request through `Manager.Handle`, and
prove the recording driver enters the real managed recovery sequence.

A process-level test must:

1. start a tagged core;
2. issue `TestTriggerManagedRecovery` through the AF_UNIX control API;
3. set `KK_RECOVERY_BARRIER_STEP=withdraw-selected`;
4. observe the `.waiting` marker;
5. prove the core is actually in recovery rather than merely returning OK.

Release test must issue the same method to a normal core and require
unsupported-method failure.

---

# 6. R4 — Ready-Toad contract completion

## 6.1 Fixed runtime architecture

Keep the binary and CLI:

~~~text
toad/internal/testtoad/cmd/ready-toad
ready-toad run -config <role.toml>
~~~

It remains test-only and must never be packaged.

Introduce a resolver seam in `readyRuntime`:

~~~go
type interfaceResolver func(string) (ifaceInfo, error)

resolve interfaceResolver
~~~

Production main sets `resolve = resolveInterface`. Unit tests inject a
deterministic resolver. All Start/poll/Rebind/RestartTransport paths use that
same field.

## 6.2 Config fixture

All Ready-Toad unit tests use a valid normal role config with:

~~~toml
protocol = "openconnect"

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
~~~

plus the normal required name/interface/address/state_dir fields.

Do not use `protocol = "awg2"` and do not weaken `config.Load`.

## 6.3 Snapshot semantics

For a started fixture:

- interface name is always `cfg.Interface`;
- ifindex/MTU come from the current resolver result;
- `Snapshot.Addresses` contains only parsed addresses whose IP family is
  configured in `cfg.Address`;
- IPv4.Configured / IPv6.Configured derive only from `cfg.Address`;
- one family Ready=true when at least one parsed current interface address of
  that family exists;
- readiness does not require every configured prefix to be present;
- `RouteReady = Families.Ready()` using the existing OR compatibility rule;
- `SessionConnected = true` while the synthetic runtime is started;
- state is `ready` when RouteReady=true, otherwise `degraded`;
- Stop sets SessionConnected=false and state=stopped.

Do not change the global `toadctl.RouteFamilies.Ready()` semantics.

## 6.4 Lifecycle behavior

Start:

- store request.Generation, generating one only when zero;
- resolve the configured interface immediately;
- if it is missing, return a startup error; do not publish a successful
  degraded start;
- build/publish the first real snapshot and increment revision.

pollLoop:

- sample every 100ms;
- if current ifindex, relevant addresses, family readiness, state or reason
  changes, increment revision and publish;
- later disappearance of the interface publishes degraded state and wakes
  subscribers.

Rebind:

- synchronously resolve the same configured interface name;
- preserve Generation;
- rebuild snapshot from the newly resolved ifindex/addresses;
- increment revision even when the resulting values are equal;
- publish before returning;
- resolver failure returns an error after publishing degraded state.

RestartTransport:

- implement exactly the same resample/revision behavior as Rebind.

Handshake stays:

~~~text
Validate=true
Rebind=true
RestartTransportKeepingTUN=true
ReportsLiveEndpoints=false
~~~

## 6.5 Required Ready-Toad tests

Replace/fix the current tests and include:

~~~text
TestReadyToadCLIRequiresRunAndConfig
TestReadyToadLoadsNormalRoleConfig
TestReadyToadStartStoresGenerationAndPublishesReady
TestReadyToadStartFailsWhenInterfaceMissing
TestReadyToadHandshakeCapabilities
TestReadyToadValidateHealthy
TestReadyToadFamilyReadinessIndependent
TestReadyToadSnapshotFiltersUnconfiguredFamilyAddresses
TestReadyToadRebindRefreshesIfIndexAndRevision
TestReadyToadRestartTransportRefreshesIfIndexAndRevision
TestReadyToadPollPublishesFamilyLossAndRestore
TestReadyToadSubscribeWakesOnRevisionChange
TestReadyToadStopClearsSessionConnected
TestReadyToadContextShutdownRemovesControlSocket
~~~

The Subscribe test must create the revision change after recording the initial
revision, by changing the injected resolver result and invoking Rebind or one
poll iteration.

Run gofmt before committing.

---

# 7. R5 — shared DNS fixtures

## 7.1 Repair parser

Modify:

~~~text
linux/tests/toad/fixtures/dns-stub.py
~~~

Parse the first question from byte 12:

1. walk labels until the terminating zero;
2. read QTYPE from the next two bytes;
3. read QCLASS from the following two bytes;
4. reject/truncate malformed frames safely.

Return:

~~~text
A    -> 203.0.113.10
AAAA -> 2001:db8:dead::10
other QTYPE -> NOERROR/NODATA
~~~

Keep a single question and class IN fixture contract.

Rename the environment spelling to:

~~~text
DNS_STUB_PORT
~~~

and update all repository consumers in the same commit. No compatibility alias
is required because the new shared fixture has not yet been an accepted API.

## 7.2 Remove inline duplicates

Modify:

~~~text
linux/tests/toad/managed-leshy-smoke.sh
~~~

Delete the inline `dns-stub.py` and `dnsq.py` heredocs.

Resolve the fixture directory relative to the script and run:

~~~text
fixtures/dns-stub.py
fixtures/dnsq.py
~~~

inside the same network namespace. Network namespaces share the mount
namespace, so no generated copy is required.

The future managed-success script must use the same two files.

## 7.3 Fixture self-test

Add:

~~~text
linux/tests/toad/fixtures/test_dns_fixtures.py
~~~

using only Python stdlib `unittest`.

It must build raw A and AAAA questions and assert:

~~~text
A response:    ANCOUNT=1, answer TYPE=A,    RDATA=203.0.113.10
AAAA response: ANCOUNT=1, answer TYPE=AAAA, RDATA=2001:db8:dead::10
TXT response:  ANCOUNT=0
malformed frame: no reply
~~~

---

# 8. R6 — Windows Row G must use SetRoutingList API

## 8.1 Gate input

Modify:

~~~text
packaging/windows/test_managed_cutover.ps1
~~~

Add mandatory parameter:

~~~text
-RouteProbe C:\path\routeprobe.exe
~~~

The harness that uploads/runs the gate must build
`toad/internal/control/testhelper/routeprobe` for windows/amd64 from the exact
candidate head and pass its path.

Do not add route-list mutation to the production CLI only for the test.

## 8.2 Row G

Replace direct `Add-Content` with:

1. read and save original list bytes;
2. create a temporary file containing original content plus the canary;
3. invoke:

~~~text
routeprobe --socket <core.sock> set-list --list <Role> --file <temp>
~~~

4. require exit 0;
5. do not restart the core merely to load the list; `SetRoutingList` already
   regenerates config and reloads Leshy through the production API path;
6. continue with connect/DNS/selected-registry proof;
7. restore the exact original content through the same `set-list` API;
8. require exit 0.

No direct write to `$ListFile` is allowed in Row G.

Update the execution log to distinguish the old 09505a4 fresh-install
evidence from the corrected API-path rerun.

---

# 9. R7 — CI and release-absence proof

Modify:

~~~text
.github/workflows/toad.yml
~~~

## 9.1 Normal unit gate

Keep the existing all-OS `go-unit` matrix and require, in order:

~~~text
gofmt clean
go vet ./...
go test ./...
go test -race ./...
~~~

No formatting failure may skip the evidence used to claim Phase F accepted.

## 9.2 Tagged Phase F job

Add a Linux job named:

~~~text
phase-f-test-build
~~~

It runs focused tagged tests, not every historical privileged test:

~~~text
go test -count=1 -tags kikimora_test_failpoints ./internal/core   -run 'RecoveryBarrier|RecoveryStepBarrier'

go test -count=1 -tags kikimora_test_failpoints ./internal/control   -run 'TestTriggerManagedRecovery|TestControlMethod'
~~~

Then build:

~~~text
go build -tags kikimora_test_failpoints ./cmd/kikimora-core
go build -tags kikimora_test_failpoints ./internal/testtoad/cmd/ready-toad
~~~

## 9.3 Release absence

In a normal untagged build:

~~~text
go build -o <tmp>/kikimora-core ./cmd/kikimora-core
~~~

assert with both `go tool nm` and `strings` that it does not contain:

~~~text
TestTriggerManagedRecovery
KK_RECOVERY_BARRIER_STEP
KK_RECOVERY_BARRIER_ROLE
KK_RECOVERY_BARRIER_DIR
~~~

The generic no-op helper symbol `handleTestControlMethod` may exist; the test
method name, recovery trigger implementation and barrier environment protocol
must not.

Also issue the test method to a running untagged core and require
unsupported-method failure.

## 9.4 Ready-Toad artifact

Build Ready-Toad in the Linux managed-test artifact job:

~~~text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64   go build -o ready-toad ./internal/testtoad/cmd/ready-toad
~~~

and Windows acceptance preparation:

~~~text
CGO_ENABLED=0 GOOS=windows GOARCH=amd64   go build -o ready-toad.exe ./internal/testtoad/cmd/ready-toad
~~~

Do not add it to release packages.

## 9.5 DNS self-test and existing denial smoke

Run:

~~~text
python3 linux/tests/toad/fixtures/test_dns_fixtures.py
~~~

before `managed-leshy-smoke.sh`. Keep the existing denial smoke; it must now
consume the shared fixtures.

## 9.6 Managed-success Linux CI

Add the versioned success script required by the parent plan:

~~~text
linux/tests/toad/managed-leshy-routing-netns.sh
~~~

Use the exact pinned Leshy SHA and the Ready-Toad artifact. Implement the
parent packet section 20 assertions; do not weaken or replace them.

The initial CI job may run the non-crash/non-ifindex-replacement rows that fit
hosted Linux privileges. The complete ifindex-replacement/crash matrix remains
the Ubuntu VM Phase G gate. Hosted CI must still prove at least:

~~~text
Ready-Toad role reaches Ready
A and AAAA create exact selected rows
duplicate queries do not churn kernel state
external selected delete is restored
foreign same-prefix row is preserved/refused
family suppression works
DisconnectAll drains selected/Park state
~~~

---

# 10. R8 — acceptance and documentation

## 10.1 Required local/static gates

Before any VM rerun:

~~~text
git status --short
gofmt -l . == empty under toad/
go vet ./...
go test -count=1 ./...
Ready-Toad focused tests green
DNS fixture self-test green
Windows cross-build green
packaging/windows/test_package.ps1 green
ShellCheck green
~~~

## 10.2 Required GitHub gates on exact head

Require:

~~~text
Toad core: success
ShellCheck: success
Build packages: success
all go-unit OS jobs green
phase-f-test-build green
Linux existing integration jobs green
Linux managed-success job green
~~~

Record exact run IDs in `08d1-execution-log.md`.

## 10.3 Windows native reruns

Run on win10:

1. corrected fresh-install `test_managed_cutover.ps1` with the exact
   routeprobe binary; require every A-H assertion green;
2. `test_state_layout_upgrade.ps1` legacy-local row;
3. legacy-managed row;
4. in-flight transition row.

No skipped mandatory row.

## 10.4 Phase F acceptance

Phase F is accepted only when all of the following are simultaneously true:

~~~text
Ready-Toad unit/process contract green
tagged recovery barrier green
test trigger reaches production recovery path
release build lacks trigger/barrier strings and rejects the test method
shared DNS A/AAAA self-test green
existing managed denial smoke uses shared fixtures and stays green
Linux managed-success CI green
all-OS normal CI green
Windows corrected Row G green
Windows state-layout upgrade gate green
~~~

Only after that append:

~~~text
Phase E Windows re-ACCEPTED (fresh install + upgrade)
Phase F ACCEPTED
~~~

to the execution log and allow Phase G.

---

# 11. Exact commit sequence

Use these commit boundaries and messages unless a repository hook imposes a
mechanical prefix:

~~~text
fix(windows): migrate legacy core state into state-core layout
test(recovery): place managed recovery barrier at step boundary
test(control): wire tagged managed-recovery trigger to production path
test(toad): complete deterministic ready-toad fixture
test(dns): share and verify managed routing DNS fixtures
test(windows): prove routing-list mutation through control API
ci(toad): enforce phase-f fixtures and release absence
docs(08d1): reaccept phase e and phase f after remediation
~~~

After each commit run the focused tests for that commit. Do not defer all
testing to the final commit.

---

# 12. Executor stop conditions

The executor does **not** stop to ask for design choices. Stop only when one of
these conditions occurs:

1. an external dependency is unavailable and no repository change can make the
   required gate runnable;
2. a native VM is unreachable;
3. an exact invariant in this packet is impossible because the current source
   model contradicts it.

In case 3, do not invent a substitute architecture. Record the precise source
contradiction in the execution log and stop before modifying the invariant.

All ordinary compile/test failures are implementation work and must be fixed
within this packet.

---

# 13. Post-b751134 Phase G investigation addendum

This section is authoritative over the Phase G journal entry added by
`b751134`.

Observed repository state:

~~~text
origin/master documentation head:
    b751134879a72f65fded8ad68fbb391e374d0a17

runtime/test code under that head:
    unchanged from 739e1ab3cb9cab359e32ba036057616ce7f10910

pinned Leshy:
    61786d489da2acf50d6b4b53a77a39184d746852
~~~

The b751134 G0/G1 work is useful investigation only. It is **not Phase G
acceptance work** and does not change the R1-R8 ordering.

## 13.1 Correct the phase boundary

The following execution-log facts are historical observations, not accepted
gates:

~~~text
ready-toad with kikimora_test_failpoints built from 739e1ab
core/toad binaries deployed to ubuntu2604 from 739e1ab
G1 ad-hoc script attempted
Phase F CI described as "running"
~~~

At the audited 739e1ab SHA the real GitHub outcomes are:

~~~text
Build packages 43016857864: SUCCESS
ShellCheck     43016857909: SUCCESS
Toad core      43016857823: FAILURE
~~~

The documentation-only b751134 head reran the relevant checks and did not
change that verdict:

~~~text
ShellCheck 43024324194: SUCCESS
Toad core  43024324300: FAILURE
~~~

All three go-unit matrix jobs on 43024324300 fail at `Check formatting`;
Vet/Build/Unit/Race are skipped. The healthy integration jobs do not override
the red all-OS unit gate.

Therefore:

~~~text
Phase F was not CI-green.
Phase G was not authorized to start.
No result produced by the G0/G1 attempt can satisfy a Phase G exit gate.
~~~

Do not continue the ad-hoc G1 investigation while R1-R8 are incomplete.
First implement and accept R1-R8. Only then restart Phase G from a clean exact
accepted head.

The binaries copied to `/usr/local/sbin` during the b751134 investigation are
stale acceptance artifacts. Future Phase G commands must never discover or
reuse them implicitly.

For every new Phase G run, build into a SHA-scoped directory:

~~~text
/tmp/kikimora-acceptance/<KIKIMORA_FULL_SHA>/
    kikimora-core
    ready-toad
    routeprobe
~~~

and pass those absolute paths to every harness. Record:

~~~text
git rev-parse HEAD
sha256sum of every test binary
Leshy --version / exact companion SHA
~~~

in the execution log before the first privileged mutation.

Do not install Ready-Toad into a system search path. It is test-only.

## 13.2 Ubuntu VM preflight

Current lab observation:

~~~text
ubuntu2604 last observed address: 192.168.3.74
disk during b751134 attempt: ~1.6 GiB free
~~~

The address remains dynamic evidence, not configuration. Rediscover it before
each native run and update `docs/lab-vms.md` when it changes.

Before building/running a Phase G harness require:

~~~text
at least 2 GiB free on /
no stale kk-leshy-local-* or kk-managed-routing-* netns
no stale fixture Leshy/core/ready-toad processes
no stale fixture route-target files under /run/kk-*
~~~

Cleanup may remove only known test namespaces/processes/files. It must not
remove installed product state or arbitrary caches merely to make a gate pass.

## 13.3 G1 is a versioned standalone-Leshy gate

Add:

~~~text
linux/tests/toad/local-leshy-routing-netns.sh
~~~

CLI contract:

~~~text
local-leshy-routing-netns.sh \
  --leshy <absolute pinned-Leshy binary> \
  --dns-stub <absolute dns-stub.py> \
  --dnsq <absolute dnsq.py>
~~~

All three arguments are mandatory. The script validates that every path is
absolute/readable before creating the namespace.

This becomes the only G1 harness. Do not continue using an unversioned
`/tmp/g1_leshy_gate.sh`.

G1 tests **standalone Leshy local routing only**. It does not start
kikimora-core or Ready-Toad. Its purpose is to prove the local Leshy
RouteRegistry/backend before the managed vertical in G2.

The harness uses the exact pinned Leshy binary and the shared DNS fixtures
produced by R5:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
~~~

Canonical deterministic DNS answers for every Phase F/G shared fixture are:

~~~text
A:    203.0.113.10
AAAA: 2001:db8:dead::10
~~~

This value is authoritative over the first 739e1ab fixture's
`2001:db8::10`. R5 must update the stub, its self-test and every consumer in
one commit.

### 13.3.1 Fixed G1 topology

Use:

~~~text
namespace:       kk-leshy-local-$$
managed device:  kklocal0
managed IPv4:    10.203.0.1/24
managed IPv6:    2001:db8:203::1/64
selected A:      203.0.113.10/32
selected AAAA:   2001:db8:dead::10/128
Leshy DNS:       127.0.0.1:15353
stub DNS:        127.0.0.1:15354
zone/domain:     local / g1.test
work root:       /run/kk-leshy-local
target file:     /run/kk-leshy-local/vpn/local.dev
~~~

Create `kklocal0` as a dummy interface in the namespace and assign both
managed addresses.

Write exactly:

~~~text
kklocal0
~~~

plus newline to `local.dev`. This file is the zone's **route-target input**
(the same `<zone>.dev` contract Kikimora normally publishes). It is not a DNS
route publication.

Use this Leshy configuration shape:

~~~toml
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
max_dynamic_routes = 4096
routing_backend = "local"

[[zones]]
name = "local"
route_type = "dev"
route_target = "/run/kk-leshy-local/vpn/local.dev"
domains = ["g1.test"]
ipv6 = true
~~~

Do not create files such as:

~~~text
<publication_dir>/g1.test/.../A/203.0.113.10
<publication_dir>/g1.test/.../AAAA/...
~~~

and do not use any publication metadata as an input signal. The b751134
investigation established that dynamic route creation is DNS-driven:
classification happens on the query, then Leshy writes the route.

### 13.3.2 Process ownership

The versioned G1 script owns all of its long-lived children:

~~~text
DNS stub
Leshy
ip monitor
~~~

Start them inside the script, record their PIDs, and terminate only those PIDs
from one EXIT/ERR trap.

The host helper is transport only. Invoke the uploaded versioned script as one
privileged foreground command:

~~~text
python3 scripts/lab/vm.py put ubuntu linux/tests/toad/local-leshy-routing-netns.sh /tmp/local-leshy-routing-netns.sh
python3 scripts/lab/vm.py put ubuntu linux/tests/toad/fixtures/dns-stub.py /tmp/dns-stub.py
python3 scripts/lab/vm.py put ubuntu linux/tests/toad/fixtures/dnsq.py /tmp/dnsq.py
python3 scripts/lab/vm.py run ubuntu "sudo bash /tmp/local-leshy-routing-netns.sh --leshy /usr/local/bin/leshy --dns-stub /tmp/dns-stub.py --dnsq /tmp/dnsq.py"
~~~

Before the fourth command, verify that `/usr/local/bin/leshy` is the exact
pinned 61786d489da2acf50d6b4b53a77a39184d746852 build; otherwise upload/use
the exact pinned binary and pass that absolute path instead.

Do not use an SSH stdin/inline-script mode for this gate and do not spread one test run
across multiple interactive SSH commands. No nested shell/TOML construction is
allowed in the host command line; all quoting lives in the versioned script.

### 13.3.3 DNS query mechanism

Use `dnsq.py` for the acceptance traffic. Every invocation must have a
bounded timeout in the shell. `dig` may be printed as a diagnostic but is not
a harness dependency.

The first A and AAAA queries are the only allowed mechanism that creates the
initial dynamic selected routes.

After a route has converged, repair tests must not issue another DNS query
until the repair assertion finishes. Otherwise the gate would prove request
reconciliation rather than observer/audit repair.

### 13.3.4 Exact G1 rows

Run these rows in order.

**G1.1 initial add/read-back**

Before any DNS query require both selected prefixes absent.

Query:

~~~text
g1.test A
g1.test AAAA
~~~

Require NOERROR and the deterministic answer, then require exact main-table
rows through `kklocal0`. Capture `ip -details -4/-6 route` identities.

**G1.2 converged duplicate churn**

After both rows are active:

1. capture exact route-table hashes;
2. start `ip -ts monitor route` inside the isolated namespace;
3. issue exactly 1000 A plus 1000 AAAA queries through `dnsq.py`;
4. stop the monitor;
5. require selected route hashes unchanged;
6. require zero route add/delete/replace events for the two selected prefixes.

This row stays in G1. It must **not** be deferred to G2. G2 repeats the same
property for the managed backend; it does not replace the local-backend proof.

**G1.3 IPv4 external-delete repair**

Delete only the exact owned /32 with an explicit identity. Issue no DNS query.
Poll the kernel every <=100ms and require the same exact route identity to
return within 5s.

**G1.4 IPv6 external-delete repair**

Same for /128. Issue no DNS query. Require exact restoration within 5s.

**G1.5 foreign same-prefix preservation**

While the owned selected route exists, add a second same-prefix route whose
identity is deliberately foreign (different output interface and metric).
Capture its exact `ip -details` text. Re-run the DNS query and allow the
observer/audit interval to pass. Require the foreign identity byte-for-byte
unchanged. Remove this fixture only from the harness.

Never use `ip route replace` for the foreign fixture; use an identity that
can coexist with the owned row.

**G1.6 exact previous-owned replacement**

Do not synthesize previous ownership in the Kikimora shell harness.

Use the already-versioned pinned-Leshy evidence path, in this fixed order:

~~~text
cd <exact Leshy checkout at 61786d489da2acf50d6b4b53a77a39184d746852>
cargo test --lib exact_previous_owned_target_is_replaced
cargo test --test linux_route_registry_netns --no-run
cargo test --test netns_monitor_probe --no-run
sudo -E env \
  LESHY_REGISTRY_TEST_BIN=<absolute built linux_route_registry_netns test binary> \
  LESHY_MONITOR_PROBE_BIN=<absolute built netns_monitor_probe test binary> \
  ./tests/linux-route-registry-netns.sh
~~~

The first command drives the production Linux backend's exact
previous-Owned replacement branch. The native netns gate immediately
re-proves the same pinned backend's real-kernel add/read-back, foreign-row
refusal, cleanup exactness, event repair and interface-recreate behavior.

G1.6 is GREEN only when both the focused replacement unit and the native
route-registry gate are green on the exact pinned companion checkout. Do not
change the companion SHA merely to invent a new G1 helper, and do not duplicate
the backend ownership algorithm in shell.

**G1.7 SIGTERM exact cleanup**

With dynamic owned rows active, send SIGTERM to the exact Leshy PID and wait
for it. Require all Leshy-owned dynamic rows gone and every unrelated/foreign
fixture unchanged.

**G1.8 pre-existing compatible foreign survives**

In a fresh subcase, install a compatible selected route before starting/querying
Leshy. Start Leshy, issue the matching DNS query, stop Leshy gracefully and
require the pre-existing row to remain. This proves it was adopted as
unowned/foreign rather than converted into Leshy-owned cleanup authority.

G1 is GREEN only when all eight rows pass in the same versioned harness run.
No row may be marked INFO/SKIP.

## 13.4 Phase G restart rule after remediation

When R1-R8 finally pass:

1. append `Phase E Windows re-ACCEPTED` and `Phase F ACCEPTED` with exact
   GitHub/native evidence;
2. fetch/checkout that exact accepted Kikimora SHA on ubuntu2604;
3. rebuild the SHA-scoped test binaries;
4. rerun G1 from the versioned standalone-Leshy harness;
5. only after G1 GREEN start G2 managed-success E2E.

The b751134 G0 deployment does not save any of these steps.

In G2, use the repaired Ready-Toad and barrier from R2-R4. Any Ready-Toad
binary built from 739e1ab is forbidden as evidence because Rebind,
SessionConnected, test config and barrier semantics at that SHA are known
wrong.
