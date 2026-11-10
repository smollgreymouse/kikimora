# 08D1 post-D7 audit and autonomous continuation

Status: **AUTHORITATIVE EXECUTION PREFIX AFTER D7**

This packet is mandatory before Production Plan Phase E.

Parent execution plan:

~~~text
docs/toad-steps/08d1-production-readiness-plan.md
~~~

Parent architecture:

~~~text
docs/toad-steps/08d1-managed-selected-routes-completion.md
~~~

Audited exact heads:

~~~text
Kikimora:
    e88c52cf63e1c8a2b81d0d5709f5f5988a9c494e

Kikimora D7 code:
    0f964a4f577a5cbd7cb29653977394f701012be5

Leshy:
    d6cfd2be1a2b3cf8c5032c68a5103a730b01af5f
~~~

The executor stopped after D7. Do not continue directly with D8. The
independent source audit found release-blocking defects in D5-D7 and the
not-yet-implemented D8/D9 boundary.

Correct continuation:

~~~text
D7R-1 committed-mode reload/recovery correctness
D7R-2 checkpoint activation fencing + local-target cleanup
D7R-3 mode transaction gate + strict mode preconditions
D8    route request auth + ActivationID/ModeGeneration fence + v2 capability
D9    diagnostics completeness
D10   deterministic crash matrix on unit tests + Ubuntu VM + Windows VM
Phase D re-acceptance
E -> F -> G -> H -> I -> J -> K -> L
~~~

The executor is authorized to continue autonomously through all phases once
their exact exit gates are green.

---

# 1. Evidence audit

## 1.1 Leshy Phase A/C base remains usable

Current Leshy master:

~~~text
d6cfd2be1a2b3cf8c5032c68a5103a730b01af5f
~~~

GitHub CI:

~~~text
run 42799804290
workflow CI
conclusion success
attempt 1
~~~

The Phase A Windows exact-writer work and Phase C graceful-lifecycle work are
retained. Do not reopen them unless a later integrated test exposes a real
regression.

## 1.2 D3-D6 GitHub evidence correction

The execution log currently names run 42822965032 as Toad core. That is
incorrect.

Actual evidence on:

~~~text
bbc347b45874459060508ac5c7cd660d05c9f0b2
~~~

is:

~~~text
Toad core:
    42822964907
    attempt 1
    15/15 jobs success

ShellCheck:
    42822965032
    success
~~~

Correct the execution log; do not delete the historical entry.

## 1.3 D7 CI is genuinely green after rerun

D7 code:

~~~text
0f964a4f577a5cbd7cb29653977394f701012be5
~~~

Evidence:

~~~text
Toad core 42823334878
attempt 2
15/15 success

Build packages 42823334890
Resolve companion pin  success
Linux deb              success
Windows MSI + zip      success
~~~

Latest docs head also has a green Toad run:

~~~text
e88c52cf63e1c8a2b81d0d5709f5f5988a9c494e
Toad core 42824297217
attempt 2
15/15 success
~~~

The green CI proves compilation/current unit coverage. It does not prove D7
crash semantics; the source defects below are outside that coverage.

---

# 2. Audit verdict

| Area | Verdict |
|---|---|
| Phase A / final L12 | retained |
| Phase B | retained, but startup strictness needs D7R integration repair |
| Phase C / L15 | accepted |
| D1/D2 schema2 types/render | useful, retain |
| D3 API/CLI | partial; auth gate is not actually final |
| D5/D6 transaction | partial; preconditions/gating incomplete |
| D7 recovery | **REJECTED pending D7R** |
| D8 | **NOT IMPLEMENTED** |
| D9 | partial plumbing only |
| D10 | **NOT IMPLEMENTED** |
| Phase E+ | blocked |

Phase E must not start until D7R-D10 are green.

---

# 3. P0 findings

## P0-1 — deferred marker snapshot becomes stale across D7 roll-forward

Files:

~~~text
toad/internal/control/control.go
toad/internal/control/mode_transaction.go
toad/cmd/kikimora-core/main.go
~~~

Production construction with DeferManagedRestore still reads
leshy-managed.json and caches it in pendingManagedMarker before
RecoverManagedModeTransition runs.

Unsafe restart cases:

~~~text
Local -> Managed crash before marker write:
    constructor sees no marker
    recovery writes schema2 marker
    cached pending marker is still nil
    normal restore skips managedSelectedRoles/checkpoints

Managed -> Local crash before marker removal:
    constructor caches old managed marker
    recovery removes marker and rolls Local
    normal restore can re-apply the stale cached managed marker
~~~

D7 cannot be accepted with this behavior.

## P0-2 — ordinary startup does not prove marker generation == committed generation

With no transition journal, startup does not enforce marker.ModeGeneration
equals leshy-mode-generation.json.

ReadCommittedModeGeneration errors are later printed as diagnostics warnings
instead of failing startup.

A stale marker can therefore be accepted without a journal.

## P0-3 — generated Leshy config failure is non-fatal

runServe logs RegenerateLeshyConfig errors and continues.

Managed startup must fail before Serve/readiness when config render, write,
read-back or semantic verification fails.

## P0-4 — schema2 checkpoint ActivationID/ModeGeneration exist but are unused

Files:

~~~text
toad/internal/routing/selected_registry.go
toad/internal/control/selected_routes.go
~~~

SelectedRoutesCheckpoint already contains ActivationID and ModeGeneration, but:

~~~text
writeCheckpoint writes neither
validation does not require either
restore does not compare them with the active marker
~~~

An old activation checkpoint can therefore be restored after Disable +
re-Enable of the same role/zone.

## P0-5 — route request fence is discarded by Kikimora

Leshy already sends activation_id and mode_generation inside leshy_route.

Kikimora LeshyRouteIntent currently has only Zone/Prefix/Source, so JSON
decoding discards the fence and no comparison occurs before mutation.

## P0-6 — local mode can enter the Kikimora selected-route writer

SelectedRouteManager.ensure uses managedSelected(role) only for the
route-authority readiness check. A non-managed role can continue to the
platform transaction.

Ensure, Forget and Flush must all require an active managed role/zone before
registry or kernel mutation.

## P0-7 — schema2 auth enforcement is stubbed

Current schema2ManagedActive returns false unconditionally.

Empty privileged_auth can therefore reach the legacy branch after schema2
activation. EnableManagedLeshy/DisableManagedLeshy can also use that empty-token
branch while local.

## P0-8 — core Handshake still advertises only managed_selected_routes_v1

A schema2/v2 Leshy can reject the core handshake even though binary preflight
accepted v2 support.

## P0-9 — live mode transition is invisible to Snapshot/wait-startup

modeTransitionInFlight exists but D5/D6 never set it. Snapshot can report
mode_transition=none while config/marker/generation are changing.

## P0-10 — Disable does not require selected registry empty

Disable checks role state/Parking/Recovery but not the selected registry.

Parent contract requires roles stopped, selected registry empty, Park nil and
no recovery before Managed -> Local.

## P0-11 — Enable does not prove current committed mode is Local

Enable builds a Local -> Managed journal without first rejecting an already
Managed committed state.

## P0-12 — Local target does not remove empty selected checkpoints

Disable does not durably remove now-empty selected-routes.json files.

D7 local-target recovery also does not inspect/remove them.

## P0-13 — D7 target verification is weaker than the journal

Managed verifyTransitionTarget checks ActivationID/ModeGeneration but not exact
Socket + ManagedZones + schema equality.

## P0-14 — FlushLeshyZone nil request can panic

Handle reads request.LeshyRoute.Zone before checking LeshyRoute for nil.

Malformed input must return an API error, never panic the core.

---

# 4. P1 findings

## P1-1 — activation fingerprint is lost on normal startup

applyManagedMarker does not set the fingerprint and runServe later calls
SetManagedModeDiagnostics(generation, "").

## P1-2 — transition diagnostics have phase only

Final status needs transition phase plus a fingerprint of transition ID.

## P1-3 — D10 failpoint boundaries are incomplete

Current process failpoints cover only after_prepared, after_config_written,
after_marker_written, after_apply and after_generation_commit.

Missing boundaries include before journal, raw config write before journal
advance, raw marker operation before journal advance and before journal delete.

---

# 5. D7R architecture — committed state is loaded only after journal recovery

This section supersedes the current pendingManagedMarker production design.

## 5.1 Production constructor must not cache marker state in deferred mode

File:

~~~text
toad/internal/control/control.go
~~~

When restorePolicy == DeferManagedRestore:

~~~text
do not read leshy-managed.json
do not populate pendingManagedMarker
do not apply marker
do not restore checkpoints
~~~

Manager construction loads only the role/config topology required by transition
recovery.

Immediate-restore unit constructors may retain explicit behavior, but
production NewManagerWithConfigDir must use the post-recovery loader below.

## 5.2 Add one post-recovery committed descriptor

Add a private immutable descriptor, conceptually:

~~~go
type committedManagedState struct {
    Generation uint64
    Managed    bool
    Marker     *managedModeMarker
}
~~~

Add:

~~~go
func (m *Manager) LoadCommittedManagedState() (committedManagedState, error)
~~~

It runs only after RecoverManagedModeTransition and performs fresh disk reads.

Exact order:

~~~text
assert transition journal absent
read committed generation
strict-read current marker

marker absent:
    committed mode = Local
    return local descriptor

marker present:
    require schema2
    require marker.ModeGeneration == committed generation
    require ActivationID/socket/zones valid
    return managed descriptor
~~~

No pre-recovery marker snapshot participates.

## 5.3 RestoreCommittedManagedState consumes the descriptor

Production startup becomes:

~~~text
RecoverManagedModeTransition
LoadCommittedManagedState
EnsurePrivilegedAuthKey(state.Managed)
RestoreCommittedManagedState(state)
RegenerateAndVerifyLeshyConfig(state)
platform/reconciler
Serve
~~~

For Managed restore:

~~~text
apply exact marker
set managedSelectedRoles
set ModeGeneration
set ActivationID
set activation fingerprint
restore checkpoints using exact marker fence
~~~

For Local restore:

~~~text
managedSocket empty
managedActivationID empty
all managedSelectedRoles false
ModeGeneration = committed generation
activation fingerprint empty
no managed checkpoint restore
~~~

## 5.4 Startup config is a fatal consistency gate

Files:

~~~text
toad/internal/control/config_api.go
toad/cmd/kikimora-core/main.go
~~~

Replace log-and-continue config regeneration.

Any render/write/read-back/parse failure returns startup error before
StartSelectedRouteReconciler or Serve.

For Managed, read back and require exact:

~~~text
routing_backend = kikimora
kikimora_socket == marker.Socket
kikimora_auth_file == PrivilegedAuthKeyPath(StateDir)
kikimora_activation_id == marker.ActivationID
kikimora_mode_generation == marker.ModeGeneration
writer_lock == configured platform writer lock
~~~

For Local require managed-only fields absent.

## 5.5 Ordinary no-journal corruption tests

Add tests at the production startup boundary:

~~~text
marker generation lower than committed -> startup fails
marker generation higher than committed -> startup fails
corrupt generation file -> startup fails
managed marker + stale local config -> rewrite/verify before Ready
managed config write failure -> no Serve/Ready
local descriptor + stale managed config -> rewrite/verify local before Ready
~~~

Suggested commit:

~~~text
fix(control): reload committed mode only after journal recovery
~~~

---

# 6. D7R checkpoint activation fencing

## 6.1 Schema2 checkpoint validation requires the activation fence

File:

~~~text
toad/internal/routing/selected_registry.go
~~~

ValidateSelectedRoutesCheckpoint must require:

~~~text
ActivationID exactly 32 lowercase hex characters
ModeGeneration > 0
~~~

Update fixtures/tests rather than weakening validation.

## 6.2 Every checkpoint write carries the active fence

File:

~~~text
toad/internal/control/selected_routes.go
~~~

Give SelectedRouteManager an explicit production activation-fence provider.

Recommended shape:

~~~go
type selectedActivationFence struct {
    ActivationID   string
    ModeGeneration uint64
}

activationFence func(role string) (selectedActivationFence, bool)
~~~

newSelectedRouteManager wires it from Manager.

writeCheckpoint for a managed role:

~~~text
requires active fence
writes ActivationID
writes ModeGeneration
writes Platform
writes exact role entries
~~~

A managed checkpoint write without a valid fence is an error.

Unit tests that persist checkpoints inject a deterministic fence.

## 6.3 Restore compares checkpoint to current marker

Change the restore path to receive the committed marker.

Before registry.RestoreRole require:

~~~text
checkpoint.ActivationID == marker.ActivationID
checkpoint.ModeGeneration == marker.ModeGeneration
checkpoint.Platform == runtime platform
checkpoint.Role == configured role
every entry zone == configured managed zone
~~~

Mismatch is startup failure before kernel mutation.

Mandatory tests:

~~~text
stale ActivationID -> fail
stale ModeGeneration -> fail
same socket/zone but previous activation -> fail
current activation -> restore
cross-platform checkpoint -> fail
~~~

## 6.4 Disable removes empty current-activation checkpoints

Add a helper that for every role managed by the From marker:

~~~text
read selected-routes.json if it exists
require fence == FromActivationID/FromGeneration
require Entries empty
durably remove file
~~~

Use fsutil.RemoveDurable.

Run it after the local target config/marker operation is durable and before
ToGeneration is committed.

## 6.5 Local-target recovery performs the same drain

D7 recovery to Local must directly inspect checkpoint files before committing
the target generation:

~~~text
missing file -> okay
matching current-from fence + empty Entries -> durable remove
non-empty entries -> fail closed
wrong activation/generation -> fail closed
malformed checkpoint -> fail closed
~~~

Never discard non-empty ownership evidence.

Suggested commit:

~~~text
fix(routing): fence selected checkpoints to the active managed activation
~~~

---

# 7. D7R transaction gate and strict preconditions

## 7.1 Make transition state real

Files:

~~~text
toad/internal/control/control.go
toad/internal/control/mode_transaction.go
toad/internal/control/selected_routes.go
~~~

Use explicit fields:

~~~text
modeTransitionInFlight
modeTransitionPhase
modeTransitionIDFingerprint
~~~

After acquiring the shared Leshy writer lock and before the first durable
transition mutation, set the gate.

Route mutation APIs must deny while this gate is active.

Clear it only when:

~~~text
the attempt failed before any journal was created
OR
the committed transaction/recovery durably removed the journal
~~~

If an error occurs after journal creation, leave the live core fail-closed and
require recovery.

## 7.2 Enable preconditions

After writer-lock acquisition and before WriteTransitionJournal require:

~~~text
committed descriptor is Local
managed marker absent
transition journal absent
no stale selected checkpoint file from an earlier activation
ToGeneration == FromGeneration + 1
~~~

An Enable while already Managed returns an explicit error with zero config,
marker or generation mutation.

## 7.3 Disable preconditions

Move the final precondition check to after writer-lock acquisition and after
the transition gate is active.

Require:

~~~text
committed descriptor is Managed
all product roles stopped/disconnected
no managed recovery in flight
selected registry empty for every managed role
Park nil for every selected record
selected inFlight == 0
current marker == committed descriptor
~~~

Only then create the Disable journal.

## 7.4 Full target verification

For Managed verify exact marker equality:

~~~text
Schema
Socket
ManagedZones
ActivationID
ModeGeneration
~~~

For Local verify:

~~~text
marker absent
validated empty checkpoint drain complete
generated config is local
~~~

Config digest remains mandatory.

## 7.5 Production-startup integration tests for the stale-marker bug

Do not test D7 only through an isolated Manager.

Add tests around the actual runServe-equivalent sequence.

Required cases:

### Enable crash before marker exists

~~~text
constructor sees no marker
journal target managed
recovery writes marker
fresh LoadCommittedManagedState sees new marker
restore arms managedSelectedRoles
checkpoint fence uses new activation
~~~

### Disable crash while old marker still exists

~~~text
constructor must not cache old marker in deferred mode
recovery removes marker
fresh LoadCommittedManagedState sees Local
old managed marker is never reapplied
~~~

### committed == To Managed

~~~text
verify exact target
remove journal
fresh load/restore current marker
~~~

### committed == To Local

~~~text
verify local config
marker absent
empty checkpoint drain complete
remove journal
fresh load remains Local
~~~

Suggested commit:

~~~text
fix(control): close mode-transition authority during cutover and recovery
~~~

---

# 8. D8 — final route fence, auth and v2 Handshake

D8 closes the actual one-writer boundary. It must be implemented before any
package cutover testing.

## 8.1 Extend Kikimora LeshyRouteIntent

File:

~~~text
toad/internal/control/leshy_routes_api.go
~~~

Final v2 shape:

~~~go
type LeshyRouteIntent struct {
    Zone           string
    Prefix         string
    Source         LeshyRouteSource
    ActivationID   string
    ModeGeneration uint64
}
~~~

JSON names must be activation_id and mode_generation. This matches the fields
Leshy already sends.

## 8.2 Echo the fence in Ensure result

Extend LeshyRouteResult with ActivationID and ModeGeneration.

For a current v2 request return the active fence.

In Leshy:

~~~text
src/routing/kikimora.rs
~~~

before accepting a controller verdict, require the response fence to equal the
configured ManagedIdentity.

A mismatched response is a protocol/authority failure and fails closed.

## 8.3 Finish privileged authentication

Files:

~~~text
toad/internal/control/control.go
toad/internal/control/api.go
~~~

Remove the schema2ManagedActive stub.

Rules:

### Development/no StateDir

~~~text
do not advertise v2 privileged managed routing
Enable/Disable -> persistent_state_required
v2 selected mutation -> persistent_state_required
~~~

### EnableManagedLeshy and DisableManagedLeshy

Always require the correct bearer token.

No local-mode empty-token compatibility bypass.

### Schema2 Ensure/Forget/Flush

Always require the correct bearer token.

### Temporary v1 compatibility

It may exist only in one explicit compatibility branch and only when:

~~~text
no schema2 marker
no schema2 transition
request carries no ActivationID/ModeGeneration
~~~

The current new core rejects active schema1 markers, so do not broaden this
branch to ordinary packaged local mode.

Wrong/empty token is rejected before intent parsing.

## 8.4 Strict request validation order

For every Ensure/Forget/Flush:

~~~text
1 privileged auth
2 committed schema2 marker exists
3 no mode transition in flight
4 ActivationID exact
5 ModeGeneration exact
6 zone maps to exactly one marker-managed role
7 route authority ready where required
8 selected registry/platform transaction
~~~

Failure before step 8 performs zero:

~~~text
registry mutation
checkpoint write
kernel mutation
~~~

Use stable non-retryable API errors for stale activation/generation.

## 8.5 Local mode is never a Kikimora selected writer

Add explicit managed-role/zone authorization to:

~~~text
SelectedRouteManager.Ensure
SelectedRouteManager.Forget
SelectedRouteManager.FlushZone
~~~

or one shared pre-mutation helper they all call.

Do not use routeAuthorityReady as a substitute for mode authorization.

## 8.6 Fix Flush nil handling

Check LeshyRoute for nil before reading Zone.

Test malformed input over the real control handler and prove the server remains
alive.

## 8.7 Dynamic capability set

Replace the single v1 constant with explicit constants:

~~~text
af_unix_control
managed_selected_routes_v1
managed_selected_routes_v2
privileged_control_auth_v1
~~~

Handshake is generated from runtime support.

Before Phase I:

~~~text
persistent StateDir + valid auth machinery:
    advertise af_unix_control
    advertise managed_selected_routes_v2
    advertise privileged_control_auth_v1

legacy v1:
    advertise only when the explicit compatibility branch is available

active schema2:
    v1 requests are denied before mutation
~~~

A no-StateDir development core does not claim v2.

## 8.8 D8 regression matrix

Mandatory:

~~~text
current activation/current generation succeeds
stale ActivationID denied zero mutation
stale generation denied zero mutation
old request after Disable denied
old request after Disable + re-Enable same socket/zone denied
local/no marker denied zero mutation
transition in flight denied zero mutation
zone not active in marker denied
empty auth denied for Enable
empty auth denied for Disable
empty auth denied for schema2 Ensure/Forget/Flush
wrong auth denied before intent parsing
v1 under schema2 denied
development no-StateDir does not advertise v2
v2 Handshake advertises all three required support capabilities
Flush nil returns API error, no panic
Leshy rejects mismatched result fence
~~~

For every denial assert counters/checkpoint bytes/fake-kernel rows remain
unchanged.

Suggested cross-repo commits:

~~~text
Kikimora:
    fix(control): fence managed route RPCs to the active schema2 activation

Leshy:
    fix(routing): verify the controller activation fence in managed verdicts
~~~

---

# 9. D9 — diagnostics describe committed and transitional truth

## 9.1 Committed fields

The D7R descriptor is the only source for:

~~~text
managed/local mode
ModeGeneration
ActivationID fingerprint
~~~

Do not later overwrite a valid fingerprint with an empty value.

## 9.2 Transition fields

Expose in Snapshot and leshy-mode status:

~~~text
mode_transition = none | prepared | config_written | marker_written
mode_transition_id_fingerprint
~~~

The fingerprint is a short hash of transition.ID.

Do not expose full transition ID, full ActivationID or auth token.

## 9.3 Route authority fields

Retain:

~~~text
route_authority_ready
route_authority_error
~~~

Managed wait-startup requires:

~~~text
startup_ready
mode_transition == none
route_authority_ready
~~~

Local wait-startup does not require managed route authority.

## 9.4 Tests

~~~text
managed restart retains activation fingerprint
local restart has empty activation fingerprint
transition phase visible while transaction is active
transition fingerprint survives until journal removal
fingerprint disappears after commit/cleanup
auth token absent from status/Snapshot/bundle/log
full ActivationID absent from ordinary diagnostics
~~~

Suggested commit:

~~~text
fix(diagnostics): report committed and transitional managed-mode authority
~~~

---

# 10. D10 — deterministic crash proof on both VMs

## 10.1 Complete process failpoint boundaries

Keep existing names and add:

~~~text
before_journal
after_prepared
after_config_write
after_config_written
after_marker_operation
after_marker_written
after_apply
after_generation_commit
before_journal_delete
~~~

Environment:

~~~text
KK_MODE_FAILPOINT=<enable|disable>/<boundary>
~~~

Test builds terminate immediately without deferred cleanup. Release builds are
no-op.

## 10.2 Unit crash matrix

For every boundary assert pre-restart durable state:

~~~text
journal phase
config digest/content
marker content/presence
committed generation
checkpoint files
transition gate
~~~

Then restart through the production startup path and assert final convergence.

## 10.3 Ubuntu VM D10 process matrix

VM:

~~~text
ubuntu2604
~~~

Use the real filesystem, writer lock and real Leshy process.

For each Enable and Disable boundary:

~~~text
establish known source mode
trigger failpoint
core exits
restart without failpoint
wait startup
verify target mode
verify exact generation
verify marker/config
verify no journal
verify writer lock
verify no foreign route change
~~~

Full reboot-at-boundary testing remains Phase G; D10 requires process crash
coverage for every boundary.

## 10.4 Windows VM D10 process matrix

VM:

~~~text
win10
~~~

Run the same matrix using real ProgramData files, LockFileEx and SCM service
lifecycle.

Require:

~~~text
state survives process death
core recovers before dependent Leshy may run
marker/config/generation converge
no stale journal
~~~

Full VM reboot matrix remains Phase H.

## 10.5 Mandatory negative cases on both OSes

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

## 10.6 Phase D acceptance

Phase D is ACCEPTED only after:

~~~text
D7R tests green
D8 auth/fence tests green
D9 diagnostics tests green
D10 unit matrix green
Toad core all jobs green on exact SHA
ShellCheck green
Build packages green
ubuntu2604 D10 matrix green
win10 D10 matrix green
~~~

Append exact SHAs, GitHub run IDs and VM evidence to the execution log.

Only then proceed to Phase E.

---

# 11. Phase E — package cutover, service dependency and upgrade choreography

Do not begin until Phase D is re-accepted.

Repositories: Kikimora + Leshy.

## 11.1 Linux product entry points

Inspect and modify as needed:

~~~text
packaging/linux/stage-release.sh
linux/install.sh
linux/files/kikimora-core.service
linux/files/leshy.service
linux/tests/test_release_packaging.sh
linux/tests/toad/service-cutover.sh
linux/tests/toad/orchestration-cutover.sh
~~~

Required installed lifecycle:

~~~text
Kikimora core installed first
core starts and reaches startup-ready
Leshy local mode may start only after core readiness
capability/auth preflight
stop Leshy gracefully
prove shared writer lock acquirable
leshy-mode enable transaction
start Leshy managed
verify mode generation/fingerprint/config identity
~~~

Keep the hard unit dependency already present:

~~~text
Requires=kikimora-core.service
After=kikimora-core.service
ExecStartPre=kikimora-core wait-startup ...
~~~

Package tests assert the actual staged unit bytes.

## 11.2 Windows product entry points

Files:

~~~text
packaging/windows/install.ps1
packaging/windows/uninstall.ps1
packaging/windows/stage.ps1
packaging/windows/test_package.ps1
packaging/windows/kikimora.wxs
~~~

Required order:

~~~text
stage/copy payload
create ProgramData + hardened ACL
install KikimoraCore
start/wait core readiness
install Leshy using its own service installer
set DependsOn=KikimoraCore
start Leshy local
preflight capabilities
stop Leshy gracefully
prove writer lock free
enable schema2 managed mode
start Leshy managed
verify generation/fingerprint
~~~

Use the Leshy service installer dependency contract. Do not replace it with an
ad-hoc bare service create.

Required command shape:

~~~text
leshy.exe service install --config <path> --name leshy --depend-on KikimoraCore
~~~

Verify the resulting SCM dependency natively.

## 11.3 Persistent Windows paths

Final core arguments:

~~~text
--state-dir C:\ProgramData\Kikimora\state\core
--config-dir C:\ProgramData\Kikimora\toads
--leshy-publication-dir C:\ProgramData\Kikimora\leshy\vpn
~~~

Auth key:

~~~text
C:\ProgramData\Kikimora\state\core\control-route-auth.key
~~~

ACL:

~~~text
SYSTEM full
Administrators full
ordinary Users cannot read
~~~

## 11.4 Failed cutover rollback

If cutover fails after local Leshy stopped:

~~~text
stop any Leshy instance
inspect mode status
if managed commit completed:
    DisconnectAll / prove empty
    disable transaction
start Leshy local
verify local mode
return installer failure
~~~

Never start local Leshy while marker says managed.

## 11.5 Upgrade classes

Previous local release:

~~~text
backup
stop Leshy gracefully
upgrade payload
start core local
start Leshy local
fresh schema2 cutover
~~~

Existing schema2 managed candidate:

~~~text
stop Leshy
stop core
backup state/config
install payload
start core first
recover same generation/activation/checkpoints
start Leshy
verify no new activation was invented
~~~

Legacy schema1:

~~~text
stop Leshy
DisconnectAll on old core
prove selected registry/Park empty
capture routes
stop old core
backup
remove legacy marker only after emptiness proof
install candidate
start local
fresh schema2 activation
~~~

If ownership cannot be proven empty, restore the old installation and abort.

## 11.6 Phase E VM scripts

Add or extend explicit VM-oriented acceptance:

~~~text
linux/tests/toad/managed-mode-cutover-vm.sh
packaging/windows/test_managed_cutover.ps1
~~~

Both prove:

~~~text
Enable while Leshy writer lock held -> fail zero mutation
after graceful Leshy stop -> lock free
transaction succeeds
managed Leshy starts
Disable while selected registry nonempty -> fail
after DisconnectAll/empty proof -> Disable succeeds
local Leshy starts
zero writer overlap
~~~

Run Linux on ubuntu2604 and Windows on win10.

---

# 12. Phase F — deterministic full integration fixture

The fixture is currently absent and must be implemented.

## 12.1 Ready-Toad

Add:

~~~text
toad/internal/testtoad/cmd/ready-toad/main.go
~~~

with focused tests next to it.

Contract:

~~~text
ready-toad run -config <role.toml>
load normal role config
resolve configured real test interface
publish actual ifindex/addresses
sample every 100 ms
independent IPv4/IPv6 Ready
Start/Validate/Rebind/RestartTransportKeepingTUN
normal revision/Subscribe behavior
no VPN transport
no route mutation
~~~

This is the only synthetic Toad for Linux and Windows managed-success tests.

## 12.2 Recovery barrier

Current repository has no processRecoveryBarrier.

Add a build-tag pair in the core package, for example:

~~~text
toad/internal/core/recovery_barrier_default.go
toad/internal/core/recovery_barrier_testbuild.go
~~~

Integrate it in the real managed RecoveryRunner immediately after each
successful recovery step and before advancing.

Test-build environment:

~~~text
KK_RECOVERY_BARRIER_STEP
KK_RECOVERY_BARRIER_ROLE
KK_RECOVERY_BARRIER_DIR
~~~

On match atomically create a .waiting marker and wait for .continue with a
bounded timeout.

Release build is a no-op.

## 12.3 TestTriggerManagedRecovery

Current repository has no test trigger.

Add it only under kikimora_test_failpoints.

The test control method calls the real requestManagedSelectedRecovery path.

Release build:

~~~text
does not advertise the method
rejects the method
contains no trigger behavior
~~~

## 12.4 Shared DNS fixtures

Extract/reuse DNS stub/query helpers used by the current managed-leshy-smoke:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
~~~

The existing denial smoke and new success smoke use the same helpers.

## 12.5 CI

Update:

~~~text
.github/workflows/toad.yml
~~~

Build ready-toad in the Linux test artifact job.

Keep the current managed denial smoke.

Add a Linux managed-success job using the exact pinned Leshy revision.

Windows hosted CI remains unit/compile/package-contract proof. Privileged
managed E2E remains a win10 VM gate.

Phase F exit:

~~~text
ready-toad unit tests green
recovery barrier unit/process tests green
test trigger reaches real recovery path
release build lacks all test behavior
Kikimora all-OS CI green
~~~

---

# 13. Phase G — Ubuntu VM vertical acceptance

VM:

~~~text
ubuntu2604
~~~

Do not substitute hosted CI.

Use clean source checkouts/worktrees at the exact SHAs under test.

## 13.1 Local Leshy vertical

Prove:

~~~text
A /32 exact route
AAAA /128 exact route
1000 duplicate queries -> zero mutation churn
external v4 delete repaired <5s
external v6 delete repaired <5s
foreign OIF/gateway/protocol/metric/scope unchanged
SIGTERM removes only owned exact rows
pre-existing compatible row survives
~~~

Archive before/after route and rule snapshots.

## 13.2 Managed success E2E

Add:

~~~text
linux/tests/toad/managed-leshy-routing-netns.sh
~~~

Use actual:

~~~text
kikimora-core
leshy
ready-toad
AF_UNIX
schema2 auth
~~~

Mandatory sequence:

~~~text
connect Ready-Toad role
both families Ready
A query creates selected /32 through managed interface
AAAA query creates selected /128
1000+1000 duplicates -> zero additional selected mutations
external v4/v6 deletion -> repair <5s
foreign same-prefix -> fail closed and foreign unchanged
recovery barrier proves Park -> WithdrawSelected ordering
replace managed interface while withdraw barrier is held
restored selected route uses new ifindex
drop only IPv6 address -> IPv4 remains Ready, AAAA suppressed
restore IPv6 -> Ready returns
kill core -> Leshy remains alive with no local fallback
restart core -> checkpoint ownership restored before normal ACK
Forget/Flush -> exact retirement
DisconnectAll -> registry/Park empty
Disable -> local config
start local Leshy with zero writer overlap
~~~

Run every Linux MUST row from the parent regression matrix, not only this
summary.

## 13.3 Transition reboot matrix

D10 already proves process crashes. Now reboot the entire VM at:

~~~text
prepared
config_written
marker_written
generation committed before journal delete
Selected Active checkpoint
ParkApplying
ParkRetiring
~~~

After every reboot require:

~~~text
core recovers before Leshy start
wait-startup blocks until authority is safe
target mode/generation exact
no stale journal
no writer overlap
no unrelated route/rule drift
~~~

## 13.4 Linux package fresh install

Use the exact candidate .deb artifact.

~~~text
fresh snapshot
install package
core ready
Leshy dependency/readiness correct
managed cutover
A/AAAA routing smoke
VM reboot
repeat smoke
uninstall
verify owned resources removed
compare unrelated network state
~~~

## 13.5 Linux package upgrade

Test:

~~~text
previous released local install -> candidate
legacy schema1 drain fixture -> candidate
schema2 candidate reinstall -> same activation/generation
ambiguous/nonempty schema1 ownership -> upgrade abort + old install restored
~~~

Archive package SHA256, logs, before/after network snapshots and exact source
SHAs.

Phase G is green only when every Linux MUST row passes.

---

# 14. Phase H — Windows VM vertical acceptance

VM:

~~~text
win10
~~~

Use Administrator and real Windows route/service APIs.

## 14.1 Re-run final Leshy Windows local-writer gate

Use:

~~~text
windows/tests/route-registry.ps1
tests/windows_route_registry.rs
~~~

Run the extended exact identity/cache/LUID matrix at the current Leshy SHA.

## 14.2 Managed success E2E

Add:

~~~text
packaging/windows/test_managed_leshy.ps1
~~~

Inputs include exact paths to kikimora-core.exe, ready-toad.exe, leshy.exe,
the work directory, KikimoraTestA and KikimoraTestB.

Before mutation abort if either test adapter owns the management/default route.

Mandatory:

~~~text
A/AAAA exact selected rows
InterfaceIndex + LUID exact identity
Protocol NetMgmt
1000 duplicate queries -> zero mutation churn
NotifyRouteChange2 deletion repair <5s
foreign same-prefix row unchanged
stale ActivationID denied
stale ModeGeneration denied
Park exact loopback identity
effective selected metric < Park metric before unpark
high selected-interface metric -> Park stays/frozen
move configured adapter alias to second adapter
restored selected row uses new ifindex + LUID
core kill -> no local fallback
core restart -> checkpoint recovery before normal ACK
DisconnectAll -> empty
Disable -> local
local Leshy graceful stop -> exact cleanup
~~~

Compare the complete unrelated route baseline after destructive scenarios.

## 14.3 SCM lifecycle

Prove:

~~~text
Leshy DependsOn KikimoraCore
KikimoraCore remains StartPending until startup authority proof
Leshy cannot start after failed core recovery
normal Leshy Stop is graceful, not task abort
auth-key ACL blocks ordinary user
desktop nonprivileged methods still work
~~~

## 14.4 Windows reboot matrix

Reboot win10 at:

~~~text
prepared
config_written
marker_written
generation committed before journal delete
Selected Active checkpoint
ParkApplying
ParkRetiring
~~~

Use actual SCM auto-start.

Core must recover before dependent Leshy reaches Running.

## 14.5 Windows package fresh install + upgrade

Test the exact MSI and portable artifacts.

Fresh:

~~~text
install
verify service dependency/readiness
managed cutover
A/AAAA smoke
reboot
repeat smoke
uninstall
compare unrelated route state
~~~

Upgrade:

~~~text
previous local release -> candidate
legacy schema1 drain -> candidate
schema2 reinstall -> same activation/generation
ambiguous legacy ownership -> abort + restore previous install
~~~

Archive route snapshots, logs, package hashes and exact source SHAs.

Phase H is green only when every Windows MUST row passes.

---

# 15. Phase I — final v2-only cut

Only after Phases D-H are green.

## 15.1 Leshy entry points

Files:

~~~text
/home/nmkartsev/Projects/leshy/src/routing/kikimora.rs
/home/nmkartsev/Projects/leshy/src/config.rs
/home/nmkartsev/Projects/leshy/src/main.rs
/home/nmkartsev/Projects/leshy/tests/
~~~

Remove final runtime support for managed_selected_routes_v1, managed requests
without ActivationID/ModeGeneration and v1 capability output.

Keep local mode unaffected.

A managed config without auth file + activation + generation must fail startup.

## 15.2 Kikimora entry points

Files:

~~~text
toad/internal/control/leshy_routes_api.go
toad/internal/control/api.go
toad/internal/control/control.go
~~~

Remove the temporary v1 route compatibility branch.

Final privileged selected methods require persistent state, valid bearer auth,
active schema2 marker, exact activation/generation and an active managed zone.

Final Handshake exposes v2 + privileged auth + AF_UNIX support, not v1.

## 15.3 Upgrade compatibility remains in packaging only

Do not make the final runtime activate schema1 just to support upgrade.

Legacy schema1 is drained by the old pair before candidate replacement, as
specified in Phase E.

## 15.4 Re-run VM managed smokes

After removing v1, rerun on both VMs:

~~~text
managed A/AAAA
core restart
stale v1 client denied
stale activation denied
managed -> local rollback
~~~

No release version bump until this is green.

---

# 16. Phase J — exact release candidates

## 16.1 Leshy v0.6.0

Files:

~~~text
/home/nmkartsev/Projects/leshy/Cargo.toml
/home/nmkartsev/Projects/leshy/Cargo.lock
~~~

Set version 0.6.0 only on the final v2-only source.

Run all local gates and the full GitHub CI.

Tag only the exact SHA with all required jobs green:

~~~text
v0.6.0
~~~

Verify the tag resolves to that SHA. Do not move/reuse the tag.

## 16.2 Kikimora companion provenance

Files:

~~~text
.github/managed-routing-companion.env
.github/workflows/build-packages.yml
~~~

Final pin contains:

~~~text
LESHY_REF=v0.6.0
LESHY_SHA=<exact 40-char tag commit>
~~~

Workflow checks out the tag, resolves HEAD and fails unless it equals
LESHY_SHA.

## 16.3 Kikimora 1.4.0 candidate

Files:

~~~text
VERSION
linux/install.sh
packaging/linux/
packaging/windows/
package contract tests
~~~

Set Kikimora VERSION=1.4.0 and expected Leshy version=0.6.0.

Remove stale 0.4.0/windows-support/moving-master references from active package
paths.

Both package families consume the same Leshy v0.6.0 SHA.

## 16.4 Candidate artifact proof

Required:

~~~text
Toad core green
ShellCheck green
Build packages green
desktop/package contracts green
Linux package embeds leshy 0.6.0
Windows package embeds leshy 0.6.0
artifact SHA256 recorded
~~~

Install those exact artifacts again on ubuntu2604 and win10 and run a fresh
managed smoke + reboot.

---

# 17. Phase K — real Linux workstation canary

Do not start until both VM package/upgrade/reboot matrices are green.

Use the exact candidate deb artifact already proven on ubuntu2604.

## 17.1 Preflight and backup

Capture:

~~~text
OS/kernel
IPv4/IPv6 routes
rules
NetworkManager/resolved state
kikimora-core + leshy unit contents/status
current packages/binaries
/etc/kikimora
/var/lib/kikimora
/etc/leshy when present
~~~

Create a root-preserving timestamped backup under kikimora-canary-backup.

## 17.2 Independent rollback

Before installation create an idempotent root rollback script restoring the
previous package/binaries, units, configs/state and service enable/start state.

Arm an independent five-minute systemd dead-man rollback before cutover.

If connectivity is lost, rollback must execute without CTUN or the executor.

## 17.3 Canary

Use one existing role that is already valid and working. Do not alter
credentials or unrelated roles.

If needed, temporarily add one harmless test domain through the normal routing
list source of truth and restore the original list afterward.

Prove:

~~~text
managed schema2 active
routeAuthorityReady true
mode_transition none
no transition journal
Leshy 0.6.0
selected A/AAAA routes use the chosen managed interface
real HTTP/TLS request succeeds through selected routing
unrelated direct destination remains on physical/default path
role disconnect/reconnect recovers
core normal restart recovers
Leshy normal restart recovers
no writer overlap
no repeated route churn
no observer/audit loop
no Wi-Fi/rfkill/default-route regression
~~~

Observe a five-minute stability window before canceling the dead-man timer.

On any failure execute rollback, verify the old installation/network, keep the
release unpublished and return to the owning VM/source phase.

---

# 18. Phase L — completion and release

Only after the workstation canary passes.

## 18.1 Final source/tag state

Require both repositories clean and all exact evidence recorded.

Tag the exact canary-tested Kikimora source:

~~~text
1.4.0
~~~

Verify the tag resolves to the canary-tested SHA.

## 18.2 Release evidence

Execution log must contain:

~~~text
Kikimora SHA/tag
Leshy SHA/tag
all required GitHub run IDs
ubuntu2604 D10/G evidence
win10 D10/H evidence
crash/reboot matrices
fresh package installs
upgrade/rollback tests
artifact SHA256
real workstation canary
~~~

## 18.3 Documentation

Only now mark these COMPLETE:

~~~text
docs/toad-steps/08d-leshy-managed-selected-routes.md
docs/toad-steps/08d1-managed-selected-routes-completion.md
docs/toad-steps/08d1-execution-log.md
~~~

Keep historical audit corrections.

## 18.4 Publish

Publish Kikimora 1.4.0 with the already-proven Linux and Windows artifacts and
checksums.

If publication rebuilds artifacts and hashes change, rerun the package smoke
against those rebuilt artifacts before publishing them.

---

# 19. Required commit slices from the audited D7 state

Recommended order:

~~~text
K-D7R1 fix(control): reload committed mode after transition recovery
K-D7R2 fix(routing): fence selected checkpoints to the active activation
K-D7R3 fix(control): close mode authority while cutover is in flight

K-D8   fix(control): fence managed route RPCs to schema2 authority
L-D8   fix(routing): verify controller activation on managed verdicts
K-D9   fix(diagnostics): expose safe managed-mode transition state
K-D10  test(control): prove mode transition crashes on both platforms

K-E1   feat(packaging): orchestrate Linux one-writer managed cutover
K-E2   feat(packaging): install dependent Leshy after Windows core
K-E3   test(packaging): prove managed cutover and upgrade rollback

K-F    test(toad): add ready-toad and deterministic recovery fixtures
K-G    test(linux): prove managed routing vertically
K-H    test(windows): prove managed routing vertically

L-I    refactor(routing): remove managed v1 runtime compatibility
K-I    refactor(control): remove managed v1 route compatibility

L-J    chore(release): prepare Leshy 0.6.0
K-J    chore(release): prepare Kikimora 1.4.0 with exact Leshy pin
K-L    docs(release): record final production evidence
~~~

The executor may split a slice further. Do not combine all safety repairs into
one opaque commit.

---

# 20. Evidence and autonomous execution rules

After every slice/phase append to 08d1-execution-log.md:

| Field | Required |
|---|---|
| Stage | D7R/D8/... |
| Kikimora SHA | exact 40-char |
| Leshy SHA | exact 40-char |
| Files/functions | concrete entry points |
| Local tests | commands + result |
| GitHub | workflow/run IDs + attempt |
| ubuntu2604 | command/script + result when required |
| win10 | command/script + result when required |
| Foreign-state proof | before/after |
| Artifact hash | once packages exist |
| Blocker | none or exact |
| Next stage allowed | YES/NO |

The executor works autonomously. Ordinary implementation/test/harness failures
are fixed and rerun without asking for another architectural decision.

Stop and leave the release unpublished only for:

~~~text
unsafe access to non-test foreign networking that cannot be isolated
missing real-role credentials at final workstation canary
external infrastructure outage preventing mandatory evidence
irrecoverable repository corruption
~~~

Never respond to a red gate by deleting/commenting/ignoring the test,
weakening assertions/timeouts, removing CI jobs or skipping a VM gate.
