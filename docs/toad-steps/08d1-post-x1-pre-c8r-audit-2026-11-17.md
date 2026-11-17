# Post-X1 / pre-C8R production audit — 2026-11-17

Status: **IMPLEMENTED BASELINE / HISTORICAL — superseded for current continuation by docs/toad-steps/08d1-post-c709710-pre-c8r-audit-2026-11-17.md**

## 0. Audited lineage

Current docs head:

~~~text
ae69bc35ffcbc41b78b2eb411f8cca3dd013d1e2
~~~

Exact product/source head accepted by the previous P1R/X1 run:

~~~text
85db60d48ec396f118431585ba7b6c2c97a892fb
~~~

Only docs/roadmap.md differs between those heads. Pinned Leshy remains:

~~~text
61786d489da2acf50d6b4b53a77a39184d746852
~~~

Exact X1 hosted evidence for 85db60d is retained:

~~~text
Desktop UI      43945842976    SUCCESS
Toad core       43945843006    SUCCESS
ShellCheck      43945843086    SUCCESS
Build packages  43945842968    SUCCESS
~~~

Local audit on the current product code:

~~~text
go -C toad test ./...          PASS
go -C toad test -race ./...    PASS
~~~

The previous executor did substantial correct work through AUD1-AUD5, T1R, P1R and X1. Do not roll it back.

C8R is nevertheless **not startable yet**. The source audit found invariants not covered by the green gates.

Current cursor:

~~~text
PC8A  NEXT  strict/durable policy truth + crash-atomic profiles + default-route invariant
PC8B        leak-free bypass-owner handover + failure quarantine
PC8C        Windows state migration correctness + C8R harness hardening
T1R2        finish acceptance provenance/identity contracts
P1R2        rebuild/recheck exact integrated artifacts
X1R2        exact combined-head revalidation
C8R         native Windows state-layout upgrade/recovery
G/H         only after C8R
~~~

The release goal remains: two VPN roles may stay active, selected traffic may use both, and changing profiles/default VPN must never send previously protected traffic to the physical default route.

---

# 1. PC8A — fail-closed policy truth

## 1.1 P0: an active profile can drop its live bypass owner when it becomes empty

Code:

~~~text
toad/internal/control/config_api.go
    setProfileRolesPolicyLocked
    requireBypassInProfileLocked
~~~

Current validation is conditional on len(wanted) > 0. Therefore this succeeds today:

~~~text
active work profile = [one]
bypass/default owner = one
SetProfileRoles("work", [])
~~~

The role becomes undesired while the global bypass still names it. A temporary audit test reproduced this and was removed afterwards.

Fix the invariant unconditionally for the active profile:

~~~go
if name == m.activeProfile {
    if err := m.requireBypassInProfileLocked(wanted); err != nil {
        ...
    }
}
~~~

An empty active profile is legal only when bypass mode is direct.

Permanent tests:

~~~text
TestSetProfileRolesRejectsEmptyActiveProfileWithBypassOwner
TestSetProfileRolesAllowsEmptyActiveProfileWhenBypassDirect
TestSetProfileRolesEmptyInactiveProfileDoesNotAffectRunningComposition
~~~

Add one RealCoreMatrixTest row proving command error + authoritative snapshot resync with no optimistic membership surviving.

## 1.2 P0: policy snapshots treat read errors as "file absent"

Code:

~~~text
toad/internal/control/config_api.go
    snapshotFileForPolicy
    rollbackRoutingListPolicy
    rollbackRoleIPv6Policy
~~~

Audit reproduction:

~~~text
pre-existing one.txt is unreadable
SetRoutingListLive replaces it
forced Leshy reload failure
rollback classifies the old file as absent
pre-existing one.txt is deleted
~~~

Replace the tuple helper with one exact snapshot type:

~~~go
type policyFileSnapshot struct {
    Path    string
    Existed bool
    Data    []byte
    Mode    os.FileMode
}

func snapshotPolicyFile(path string) (policyFileSnapshot, error)
func (s policyFileSnapshot) Restore() error
~~~

Rules:

~~~text
os.ErrNotExist             -> Existed=false, nil
any other stat/read error  -> error BEFORE mutation
restore existing           -> fsutil.WriteAtomicFile exact bytes/mode
restore absent             -> durable remove; missing is success
~~~

Use the same helper for routing-list, IPv6 and bypass source rollback.

Mandatory tests prove unreadable prior files fail before mutation and exact bytes/mode/existence survive rollback.

## 1.3 P0: malformed/unreadable persisted policy silently becomes defaults

Permissive readers:

~~~text
toad/internal/config/bypass.go
    ReadBypassMode

toad/internal/config/routing_lists.go
    ReadRoleIPv6Settings

toad/internal/config/leshy_config.go
    ReadLeshyServer
~~~

Current fail-open behavior:

~~~text
bypass read error                    -> direct
invalid bypass role text             -> no catch-all zone
bad role-ipv6.json                   -> empty map -> IPv6 defaults enabled
bad existing Leshy TOML              -> default DNS/server settings
~~~

Audit subtests reproduced all three classes.

Add strict product readers:

~~~go
ReadBypassModeStrict(path string) (string, error)
ReadRoleIPv6SettingsStrict(dir string) (map[string]bool, error)
ReadLeshyServerForRegeneration(path string) (LeshyServer, error)
~~~

Semantics:

~~~text
missing bypass                       -> direct, nil
existing empty/direct/valid role     -> parsed value, nil
invalid/unreadable bypass            -> error

missing IPv6 policy                  -> empty map, nil
malformed/wrong-schema/unreadable    -> error

missing Leshy config                 -> current defaults, nil
existing Leshy config                -> strict TOML parse; malformed/unreadable = error
~~~

Wire strict truth into:

~~~text
toad/internal/control/config_api.go
    captureLeshyRenderPolicyLocked
    buildLeshyServer
    buildLeshyZones
    SetRoleIPv6
~~~

WriteRoleIPv6Setting must not repair a corrupt file by silently replacing it with a one-entry map.

Do not implement ReadLeshyServerForRegeneration as a trivial wrapper around
ReadLeshyServerStrict: the current regeneration path intentionally supplies
defaults for omitted listen/upstream/writer-lock fields. Refactor the parser so
"missing field" still means the current default while "existing file cannot be
read/parsed/validated" is an error. Unknown TOML keys remain forward-compatible.

### Startup/cache ordering

Add Manager-owned committed policy cache:

~~~go
roleIPv6       map[string]bool
roleIPv6Loaded bool
~~~

and retain the existing bypassMode/bypassModeLoaded cache.

Add one strict loader in the control layer:

~~~go
func (m *Manager) loadRuntimePolicySources() error
~~~

Startup order in StartupManagedAuthority:

~~~text
SetStateDir
closeRuntimePaths
loadRuntimePolicySources STRICTLY
RecoverManagedModeTransition
LoadCommittedManagedState
...
RegenerateAndVerifyLeshyConfig
~~~

This ordering matters because transition recovery itself renders the generated
Leshy config. It must never render from permissive fallback policy.

SetRuntimePaths invalidates both caches before StartupManagedAuthority closes
the paths. captureLeshyRenderPolicyLocked uses only the loaded committed
bypass/IPv6 cache. RoleIPv6 and Snapshot read the same cache, not disk.

Live policy mutations update the cache only after source write + generated
config + running Leshy reload commit. A source file modified/corrupted behind
the running core does not silently change runtime truth; the next explicit
reload/restart fails closed and reports the corruption.

In toad/internal/control/api.go::GetRoutingLists, stop ignoring errors from
ReadLeshyZones; malformed generated config must be a visible command error, not
a valid empty state.

## 1.4 P0 crash durability: authoritative policy writers and profile composition

The audit found a second class hidden by ordinary unit tests: several files are
called "atomic"/"durable" but still use raw os.Rename/os.WriteFile paths instead
of the repository's cross-platform durable primitive.

Affected authoritative writers:

~~~text
toad/internal/config/bypass.go
    WriteBypassMode

toad/internal/config/routing.go
    WriteRoutingList

toad/internal/config/routing_lists.go
    WriteRoleIPv6Setting

toad/internal/config/create_profiles.go
    profileFileSnapshot.write / restore

toad/internal/config/profiles_registry.go
    AddDeclaredProfile

toad/internal/control/desired_state.go
    FileDesiredStateStore.Save
~~~

fsutil.WriteAtomicFile / WriteAtomicJSON already provides the required contract:

~~~text
same-directory temp
payload fsync
atomic replace
Windows MoveFileExW(REPLACE_EXISTING | WRITE_THROUGH)
POSIX rename + parent directory fsync
~~~

Use it everywhere above. Use fsutil.RemoveDurable for authoritative rollback
removals. Do not keep parallel home-grown atomic writers.

### Multi-file profile composition still needs an undo journal

Even after each file write becomes individually durable, ProfileCompositionTxn
touches multiple role TOMLs plus profiles.list and may then persist desired.json.
A process/power loss between those writes can leave a half-committed profile
composition that startup currently accepts.

The existing AUD1 rollback covers returned errors only. It is not crash-atomic.

Add a production undo journal under the hardened core state root:

~~~text
<stateDir>/profile-composition-undo.json
schema = 1
~~~

The journal stores only the exact ORIGINAL images needed to undo one
incomplete transaction:

~~~go
type ProfileUndoFile struct {
    Kind    string // "role" | "registry" | "desired"
    Name    string // role name only for Kind=role
    Existed bool
    Mode    uint32
    Data    []byte // JSON base64 encoding is fine
}

type ProfileCompositionUndo struct {
    Schema int
    Files  []ProfileUndoFile
}
~~~

Do not persist arbitrary absolute paths in the journal. Recovery reconstructs:

~~~text
role     -> <configDir>/<validated-role>.toml
registry -> <configDir>/profiles.list
desired  -> <stateDir>/desired.json
~~~

The undo journal may contain original tunnel TOML bytes and therefore may
contain private VPN credentials. It is always written mode 0600 under the
hardened core state root. Diagnostics/support bundles must never include its
payload; they may expose only presence, schema and tx id/operation metadata.

Transaction order for SetTunnelProfiles / SetProfileRoles / CreateProfile:

~~~text
lock policyMu
validate and PrepareProfileComposition
capture exact original desired.json image when a FileDesiredStateStore exists
durably write profile-composition-undo.json BEFORE any policy mutation

commit every touched config file with fsutil durable primitives
apply in-memory membership/profile map
persist desired.json when active composition changed

success:
    durably RemoveDurable(profile-composition-undo.json)
    only then expose successful command completion / schedule settlement

ordinary failure:
    restore config + desired exact original images durably
    restore in-memory state
    remove journal only after rollback is proven
    if rollback/journal removal cannot be proven, return joined error and keep
    the journal for startup recovery
~~~

A journal that still exists at process start always means "the previous profile
composition did not reach its commit point". Recovery therefore rolls it back
to the original images; no extra phase field is needed.

### Startup recovery point

The recovery must happen after the packaged core state lock is acquired but
before NewManager loads any role TOML or profiles.list.

Concrete code point:

~~~text
toad/cmd/kikimora-core/main.go
    runServe
~~~

Order:

~~~text
acquire <stateDir>/core.lock
RecoverProfileCompositionUndo(stateDir, configDir)
NewManagerWithConfigDir(...)   # now reads one coherent old-or-committed set
SetRuntimePaths
StartupManagedAuthority
...
~~~

The config file name list may already have been enumerated by configsFromDir:
profile composition never creates/deletes role TOMLs, so restoring their bytes
before NewManager.Load is sufficient.

For stateDir=="" development runs there is no cross-directory desired-state
authority; keep the existing in-process rollback but still use durable
single-file writes.

### Required crash/failpoint tests

Add a test-build-only profile transaction failpoint with boundaries:

~~~text
after_undo_journal
after_first_role_write
after_all_role_writes
after_registry_write
after_desired_write
before_undo_remove
~~~

Process-crash matrix must restart through the real startup recovery and prove
for every boundary:

~~~text
journal present -> exact ORIGINAL role TOMLs/profiles.list/desired.json restored
no half membership is loaded into Manager
active profile + bypass invariant holds
journal consumed after successful rollback recovery
rerun is idempotent
~~~

Also retain the existing ordinary-error AUD1 tests.

Focused files:

~~~text
toad/internal/config/create_profiles_test.go
toad/internal/control/profile_membership_test.go
toad/internal/control/profile_switch_epoch_test.go
toad/cmd/kikimora-core/profile_composition_recovery_test.go
~~~

## 1.5 P1: custom default-VPN role can lack managed route identity

Confirmed source/reproduction:

~~~text
custom role "one"
leshy_zone = ""
active profile contains one

SetBypassMode("one") succeeds
generated Leshy config contains synthetic zone "bypass" -> one.dev
~~~

But managed activation authorizes roles only when `EffectiveEndpointPolicy().Zone != ""`.

Concrete code:

~~~text
toad/internal/control/mode_transaction.go
    EnableManagedLeshy

toad/internal/control/selected_routes.go
    EnableManagedSelectedRoutes
    applyManagedMarker
    requireManagedRole

toad/internal/control/config_api.go
    validateBypassRoleLocked
~~~

For a non-primary/non-secondary custom role with no explicit `leshy_zone`, the effective zone is empty. The role never enters `managedSelectedRoles`. Leshy can still match synthetic `zone="bypass"` and the resolver can choose role `one`, but `requireManagedRole` then denies every selected-route mutation because the schema-2 marker never authorized that role.

`CreateTunnel` already writes `LeshyZone = Name`, so this is primarily an upgrade/manual-config compatibility trap.

### Required invariant

Do not redesign the marker schema for this edge case. Fail closed before a broken default-route policy commits.

Add:

~~~go
func (m *Manager) validateManagedRouteIdentityLocked(role string) error
~~~

Requirements:

~~~text
role exists
EffectiveEndpointPolicy().Zone != ""
zone is unique across loaded roles
~~~

Use it from:

~~~text
validateBypassRoleLocked
managed-mode startup validation
EnableManagedLeshy / marker preparation
~~~

Exact behavior:

~~~text
local/non-managed install:
    keep existing local semantics

managed mode or transition to managed:
    every role that can own generated managed selected traffic
    (ordinary zone or current non-direct bypass owner)
    must have a non-empty unique managed zone identity
~~~

A persisted `bypass.conf=one` on managed startup must fail before readiness with a migration error, not produce a catch-all zone that the core later denies.

Suggested error:

~~~text
role "one" cannot own the global default route in managed mode because it has
no leshy_zone; add leshy_zone = "one" (or recreate/import the tunnel) and retry
~~~

Tests:

~~~text
custom role without leshy_zone + managed bypass -> SetBypassMode rejected
managed startup + persisted bypass to zone-less role -> startup fails before route authority Ready
custom role with explicit unique leshy_zone -> accepted
duplicate zone identity -> rejected before marker/config commit
~~~

Do not auto-invent a zone in memory for arbitrary existing configs: durable TOML and marker identity must agree across restart.

## 1.6 P1: profile registry corruption is silently normalized at startup

`toad/internal/config/profiles_registry.go::ReadDeclaredProfiles` currently skips invalid names and duplicates instead of rejecting the persisted registry.

That is unsafe after making profile composition crash-recoverable: a corrupted `profiles.list` can silently remove a user-visible profile from startup truth.

Change the production reader to strict semantics:

~~~go
func ReadDeclaredProfiles(dir string) ([]string, error)
~~~

~~~text
missing file -> empty registry, nil
valid non-empty lines -> canonical unique list
blank lines -> ignored
invalid profile name -> error
duplicate profile name -> error
read error -> error
~~~

`NewManagerWithConfigDir` already propagates this error, so no new startup surface is needed.

Tests:

~~~text
invalid persisted profile name -> startup construction fails
duplicate persisted profile -> startup construction fails
missing registry -> valid empty
canonical valid registry -> unchanged
~~~

## 1.7 P1: single-file tunnel CRUD must use the same durable primitive

These APIs are intentionally restart-required rather than hot-loaded, so they do not belong in the multi-file profile journal:

~~~text
toad/internal/config/create.go::CreateTunnel
toad/internal/config/create_profiles.go::RemoveTunnel
~~~

But they are still user-facing persisted state and currently use plain `os.WriteFile` / `os.Remove`.

Required narrow fix:

~~~text
CreateTunnel:
    encode bytes
    fsutil.WriteAtomicFile(path, bytes, 0600)
    Load(path) verification
    if verification somehow fails -> fsutil.RemoveDurable(path)

RemoveTunnel:
    fsutil.RemoveDurable(path)
~~~

Keep the existing API message that restart is required; this is durability, not a hot-add redesign.

Also migrate legacy `AddDeclaredProfile` (if retained) to `fsutil.WriteAtomicFile`, or remove it if production has no callers after ProfileCompositionTxn. Do not leave a second weaker writer for the same registry.

## 1.8 P0: live policy authorities still bypass the durable fsutil primitive

Two user-facing live-policy writers still use hand-written temp+rename:

~~~text
toad/internal/config/bypass.go
    WriteBypassMode

toad/internal/config/routing.go
    WriteRoutingList
~~~

They do not fsync the temporary file, do not sync the parent directory and do
not use Windows MOVEFILE_WRITE_THROUGH.

This is especially critical for bypass.conf because PC8B intentionally makes it
the **single durable desired owner** used to recover an interrupted A->B
handover. A successful SetBypassMode that is lost across power failure can make
checkpoint evidence and desired owner disagree on restart.

Replace both implementations with:

~~~text
validate/render bytes
fsutil.WriteAtomicFile(path, bytes, existing-or-product mode)
~~~

Use fsutil.RemoveDurable for rollback-to-absent.

Also replace the custom role-ipv6 JSON durable writer with
fsutil.WriteAtomicJSON so there is one tested persistence primitive.

Tests:

~~~text
successful bypass/list/IPv6 write uses exact bytes after reopen
mid-write failpoint leaves old bytes
post-rename crash/reopen keeps new bytes
rollback-to-absent is durably absent
Windows durable replace contract remains covered by fsutil tests
~~~

## 1.9 P0: rollback failure in any live-policy transaction must quarantine policy authority

Current error paths:

~~~text
toad/internal/control/config_api.go
    SetRoutingListLive / rollbackRoutingListPolicy
    SetRoleIPv6 / rollbackRoleIPv6Policy
    SetBypassMode / rollbackBypassPolicyLocked
~~~

`errors.Join(primary, rollback)` makes the command visibly fail, but the Manager does not record that rollback itself was unproved. The existing selected-route reconciler may remain Ready, so snapshot `route_authority_ready` can stay true and Desktop `safeToMutate` can allow another policy mutation.

That is unsafe when any rollback step failed:

~~~text
source file may be old/new/absent
generated Leshy config may be old/new
running Leshy may still run old/new policy
selected routes may reflect either policy
~~~

### One shared policy-authority overlay

Introduce at Manager level (not bypass-only):

~~~go
policyAuthorityError string

func (m *Manager) quarantinePolicyAuthorityLocked(reason string)
func (m *Manager) clearPolicyAuthorityAfterProofLocked()
~~~

Extend existing snapshot composition:

~~~text
routeAuthorityReady =
    selected reconciler authority ready
    AND policyAuthorityError == ""

routeAuthorityError =
    policyAuthorityError when nonempty
    else selected reconciler authority error
~~~

Every live-policy transaction follows one rule:

~~~text
primary mutation fails
-> attempt exact rollback

rollback fully succeeds
-> return command error, authority may remain Ready

any rollback step fails
-> set stable policyAuthorityError
-> return joined error
-> safeToMutate becomes false through the existing Desktop policy
~~~

Do not clear quarantine merely because a later unrelated command succeeded.

Recovery/clear proof must be explicit:

~~~text
strict-read durable source policy
regenerate exact config
reload Leshy successfully
run/complete selected-route authority reconciliation
then clear policyAuthorityError
~~~

Expose one internal recovery helper, for example:

~~~go
func (m *Manager) ReprovePolicyAuthority(ctx context.Context) error
~~~

Startup naturally performs the same proof and starts unquarantined only on success.

Required focused tests:

~~~text
routing-list rollback reload failure -> route_authority_ready=false + stable error
IPv6 rollback write/reload failure -> same
bypass rollback failure -> same shared mechanism
subsequent ordinary mutation is blocked by CoreBackend safeToMutate
successful explicit reproof clears error and resumes mutation
restart with corrected durable files re-establishes authority
~~~

PC8B handover recovery reuses this overlay; do not create a second bypass-only UI gate.

---

# 2. PC8B — default-VPN A -> B must be a protected handover

## 2.1 P0: current success path deletes old prefixes and waits for another DNS query

Current SetBypassMode does:

~~~text
arm transition fence
FlushBypassOwner(A)
write bypass.conf=B
regenerate/reload Leshy
cache B
clear fence
~~~

Already-resolved catch-all prefixes are not installed on B.

TestSetBypassModeDrainsOldOwnerBeforeSwitch creates the B route only by calling Ensure after the switch. PROD4 D6 likewise tests direct -> secondary followed by a new DNS lookup; D14/D15 only test compatibility.

User-visible failure:

~~~text
application already uses an IP protected by VPN A
A -> B removes A host route
application sends without another DNS lookup
physical/default route can win
~~~

This is a traffic-leak window and may last for the existing connection.

## 2.2 Required zone-scoped handover

Do not Freeze the entire old role: it may simultaneously own ordinary role-zone routes.

Keep the external bypassTransition fence, which blocks only the synthetic catch-all zone, and add internal zone-scoped operations in:

~~~text
toad/internal/control/selected_parking.go
toad/internal/control/selected_transactions.go
toad/internal/control/selected_routes.go
~~~

Fixed internal shape:

~~~go
type BypassHandoverIntent struct {
    Prefix netip.Prefix
    Source routing.SelectedRouteSource
}

SnapshotBypassIntents(owner string) ([]BypassHandoverIntent, error)
ParkAndWithdrawBypass(ctx, owner string, intents []BypassHandoverIntent) error
ApplyBypassToOwner(ctx, owner string, intents []BypassHandoverIntent) error
CommitBypassHandover(ctx, oldOwner string, intents []BypassHandoverIntent) error
RestoreBypassToOwner(ctx, owner string, intents []BypassHandoverIntent) error
~~~

For role A -> role B:

~~~text
1. policyMu: validate B and preflight every captured prefix family before deleting anything
2. arm bypassTransition
3. under A applyMu, Zone=="bypass" only:
       install/read-back managed parks and checkpoint ParkActive
4. remove A exact selected rows while retaining registry + park evidence
5. commit/reload policy B
6. internally reserve/reconcile the same prefixes on B, preserving DNS/static source
7. prove every B selected identity Active/exact
8. retire A parks by exact identity and remove A bypass records/checkpoints
9. commit cached B and clear fence
~~~

At every point every transferred prefix has at least one of:

~~~text
old selected route
verified managed park
new selected route
~~~

Ordinary A-zone routes remain live.

Transitions:

~~~text
direct -> role    no old prefixes to migrate; commit policy, future DNS adds routes
role -> direct    direct is explicit intent; commit direct, retire old catch-all ownership
role A -> role B  mandatory protected handover above
~~~

### Pinned Leshy ownership model — do not invent a multi-claim registry

Cross-repo audit of the pinned Leshy 61786d489da2acf50d6b4b53a77a39184d746852:

~~~text
src/routing/registry.rs
    entries: HashMap<RouteKey, RouteEntry>
    RouteEntry.desired: Option<RouteIntent>
    retire_intent refuses removal when current desired != retiring intent

src/routing/kikimora.rs
    managed remove sends ForgetLeshyRoute for that current RouteIntent
~~~

So the producer itself has one current logical owner per RouteKey and protects
against stale old-zone retirement. Kikimora should keep one physical
(role,prefix) ownership record with one current Zone/Source; do NOT redesign
the checkpoint schema into simultaneous per-zone claims as part of PC8B.

The audit temporarily reproduced that manually sending an out-of-order Forget
for an older zone can delete the current same-prefix record in Kikimora, but
that sequence bypasses pinned Leshy's per-key serialization/current-intent
guard and is not a valid product-flow blocker. No claim-set change is planned.

PC8B transfers only records whose current Zone == "bypass" and preserves the
existing scalar Zone/Source model.

## 2.3 Confirmed P0: a live bypass checkpoint cannot restore today

The current checkpoint validator makes the synthetic zone impossible to restore.

Code:

~~~text
toad/internal/control/selected_routes.go
    restoreManagedCheckpoints

toad/internal/routing/selected_registry.go
    ValidateSelectedRoutesCheckpoint
~~~

restoreManagedCheckpoints currently calls the validator with exactly one
configured role zone. The validator rejects every entry whose Zone differs from
that role zone.

A real catch-all record is:

~~~text
Role = primary
Zone = bypass
~~~

Audit reproduction staged a valid current-activation checkpoint for primary
whose only entry had Zone=bypass, with strict bypass mode=primary.

Startup failed:

~~~text
entry 0 zone "bypass" does not match the configured zone "primary"
~~~

The temporary audit test was removed.

This means even before implementing A->B handover, a transient restart while a
non-direct default owner has active catch-all selected routes can fail core
startup. PROD4 D16b did not catch it because D6 resets bypass to direct before
the restart rows.

### Validator fix

Make the validator accept an explicit allowed-zone set, not one scalar zone.

Prefer a backwards-compatible variadic shape:

~~~go
func ValidateSelectedRoutesCheckpoint(
    checkpoint SelectedRoutesCheckpoint,
    expectedRole string,
    allowedZones ...string,
) error
~~~

Build a canonical set from allowedZones and require every entry Zone to belong
to it.

For managed role checkpoint restore, pass:

~~~text
configured ordinary role zone
"bypass"
~~~

for every managed role.

Important: accepting Zone=bypass in checkpoint validation is **evidence
validation only**. It does not authorize that role as the current bypass owner.
Current strict bypass.conf decides that later in startup reconciliation.

Permanent tests:

~~~text
current owner + bypass checkpoint restores
ordinary role zone + bypass entries may coexist in one role checkpoint
unknown third zone is still rejected
stale-activation bypass checkpoint is still rejected
cross-platform bypass checkpoint is still rejected
~~~

## 2.4 Crash/restart reconciliation — no second durable policy journal

Do not add a second default-route authority.

Durable authority is exactly:

~~~text
bypass.conf                         final desired owner
selected-routes.json checkpoints    per-prefix selected/park evidence
~~~

This is sufficient if startup classifies bypass checkpoints before treating
them as ordinary role ownership.

### Startup classification

In restoreManagedCheckpoints:

~~~text
1. strict-read bypass.conf once
2. seed the Manager bypass cache
3. validate checkpoint zones allowing:
       configured ordinary zone
       synthetic "bypass"
4. restore registry evidence
5. inspect every restored bypass entry
~~~

Any bypass record with:

~~~text
Park != nil
OR record role != current non-direct bypass owner
OR current bypass == direct
~~~

is a pending bypass-handover recovery, not a normal role restore.

Add only the bypass-recovery owner set:

~~~go
bypassRecoveryOwners map[string]bool
~~~

Reuse the **shared Manager.policyAuthorityError from PC8A §1.9**. Do not add a
second bypass-specific authority error.

Keep bypassTransition as the external synthetic-zone fence. On startup, if any
handover recovery is pending:

~~~text
bypassTransition.Active = true
bypassTransition.To     = strict current bypass owner
policyAuthorityError    = "default-route handover recovery pending"
~~~

Multiple stale source roles are allowed; do not encode recovery as one From
string only.

### Existing route-authority snapshot becomes the UI gate

Extend:

~~~text
Manager.routeAuthorityReadyLocked
Manager.routeAuthorityErrorLocked
~~~

so:

~~~text
routeAuthorityReady =
    selected reconciler authority ready
    AND policyAuthorityError == ""

routeAuthorityError =
    policyAuthorityError when nonempty
    else reconciler authority error
~~~

This makes the existing Desktop safeToMutate policy fail closed without adding
a new UI property.

Do NOT close the underlying selected-route reconciler gate merely to express a
bypass handover. Ordinary role zones may continue routing. External bypass
Ensure/Forget/Flush is already denied by bypassTransition.Active.

### Recovery driver owns deferred completion

At the start of managed role recovery in:

~~~text
toad/internal/control/recovery_driver.go
~~~

check whether that role is in bypassRecoveryOwners.

Call one shared:

~~~go
func (m *Manager) resumeBypassHandover(ctx context.Context, sourceRole string) error
~~~

using the current strict/cached bypass target.

Cases:

~~~text
target == direct:
    retire source bypass selected/park evidence safely

target == sourceRole:
    restore/reconcile source selected rows under existing parks
    prove exact selected winner
    retire parks

target == another role:
    require target role desired/healthy/family-ready
    apply captured prefixes to target while source parks stay Active
    prove target exact selected identities
    retire source parks and source bypass records
~~~

On retryable target-not-ready failure, keep parks, bypassTransition and
policyAuthorityError. Existing recovery backoff retries after desired state and
role processes converge.

When the last bypassRecoveryOwners entry completes:

~~~text
clear bypassTransition
clear policyAuthorityError
bump snapshot
~~~

This works with current startup ordering: the initial reconciler can load
checkpoint evidence before desired state is restored; the pending parks remain
fail-closed, then normal recovery completes after the target role becomes Ready.

It also unifies runtime and crash recovery: runtime A->B marks A pending after
ParkAndWithdrawBypass, writes/reloads durable bypass.conf=B, then immediately
calls the same resumeBypassHandover path. A crash at any point is resumed from
bypass.conf + checkpoints on the next start.

## 2.5 P0 failure path: destructive drain errors clear the fence

Current code can drain old ownership and then fail in WriteBypassMode; it clears bypassTransition without re-establishing the old Leshy/core state.

Audit reproduction forced WriteBypassMode failure after an old-owner row existed. The old route was drained and no additional old-policy reload occurred.

Every failure after the first destructive mutation must:

~~~text
A finish the new handover
or
B restore old policy + old selected rows exactly
or
C remain visibly fail-closed/quarantined
~~~

If rollback cannot be proven, do not clear the policy fence.

Use the policy-authority overlay defined above:

~~~text
bypassTransition.Active = true
policyAuthorityError    = stable rollback/recovery error
~~~

The existing snapshot then reports:

~~~text
route_authority_ready = false
route_authority_error = policyAuthorityError
~~~

without closing ordinary selected-route reconciliation.

A successful in-process resumeBypassHandover may clear the overlay only after
every protected prefix reaches its final exact state. Otherwise a full core
restart reconstructs pending recovery from strict bypass.conf + checkpoints.
Never reopen the policy gate by directly setting authorityReady=true.

Mandatory tests:

~~~text
A -> B same prefix Active on B with NO DNS/Ensure after switch
ordinary A-zone routes unchanged
target family not Ready -> refusal before old removal
park/withdraw/apply/cleanup failpoints never leave prefix unprotected
WriteBypassMode/regenerate/reload failure restores old selected + policy
rollback reload failure closes route authority and keeps bypass intents denied
restart with interrupted handover checkpoints converges to current bypass.conf owner
~~~

Extend both native product harnesses:

~~~text
linux/tests/toad/managed-dual-profile-netns.sh
packaging/windows/test_dual_profile.ps1
~~~

New mandatory row:

~~~text
both VPNs Ready
bypass=primary
query catchall.test ONCE
prove prefix on primary

WITHOUT another DNS query:
    bypass=secondary

after command:
    same prefix Active on secondary
    primary bypass record gone
    no routeprobe Ensure
    no Leshy restart
    ordinary role-zone routes unchanged

repeat secondary -> primary
~~~

This stays mandatory in G2/H2.

---

# 3. PC8C — Windows migration and C8R harness

## 3.1 Confirmed code bug: legacy source absent + destination present fails

Code:

~~~text
toad/cmd/kikimora-core/state_layout_windows.go
    migrateOneLegacyFile
~~~

Documented crash-resume contract says source absent + state\core destination present keeps the destination. Current switch catches srcErr != nil before the later IsNotExist(srcErr) case, making that branch unreachable.

Rewrite with explicit existence booleans:

~~~go
srcData, srcErr := os.ReadFile(src)
srcExists := true
switch {
case errors.Is(srcErr, os.ErrNotExist):
    srcExists = false
case srcErr != nil:
    return fmt.Errorf("read legacy %s: %w", name, srcErr)
}

dstData, dstErr := os.ReadFile(dst)
dstExists := true
switch {
case errors.Is(dstErr, os.ErrNotExist):
    dstExists = false
case dstErr != nil:
    return fmt.Errorf("read migrated %s: %w", name, dstErr)
}

switch {
case !srcExists && !dstExists:
    return nil
case !srcExists && dstExists:
    return nil
case srcExists && !dstExists:
    return fsutil.WriteAtomicFile(dst, srcData, 0o600)
case !bytes.Equal(srcData, dstData):
    return conflict
default:
    return nil
}
~~~

Tests in state_layout_windows_test.go:

~~~text
TestWindowsCoreStateMigrationKeepsDestinationWhenLegacySourceMissing
TestWindowsCoreStateMigrationDestinationOnlyRerunIsIdempotent
TestWindowsCoreStateMigrationLegacyReadErrorIsFatal
TestWindowsCoreStateMigrationDestinationReadErrorIsFatal
~~~

Add native partial-destination C8R row in test_state_layout_upgrade.ps1:

~~~text
one authority file only under state\core
another authority file remains legacy
no completion marker

install:
    destination bytes unchanged
    other source copied
    legacy source retained
    schema1/layout2 marker written
    startup_ready
    service restart idempotent
~~~

## 3.2 P0: schema-1 transition upgrader is not phase-aware

Code:

~~~text
toad/internal/control/mode_transaction.go
    upgradeLegacyTransitionJournal
~~~

Current upgrader reads the durable generated config and, whenever the file exists, requires:

~~~text
legacy raw SHA256(durable config) == journal.ConfigSHA256
~~~

That is correct only after the old transaction reached `config_written`.

For a legitimate legacy `prepared` journal:

~~~text
journal.ConfigSHA256 = digest of the FUTURE target config
durable generated config = still the FROM-mode config
~~~

So a normal prepared crash with an existing from-config is rejected during upgrade.

The opposite fail-open also exists: for `config_written` / `marker_written`, if the generated config is missing, the current code skips the legacy raw-digest relation entirely and may upgrade the journal without proving the target bytes the old core claimed it wrote.

### Required phase-aware compatibility contract

Keep schema-2 semantic digest as the live format. Fix only schema-1 compatibility:

~~~text
phase = prepared:
    target bytes are NOT expected on disk
    validate journal structural/generation/activation invariants
    validate current FROM committed state/marker relation normally
    render target from journal + current authoritative policy
    compute v2 semantic digest
    upgrade journal to schema2
    roll forward normally

phase = config_written:
    generated config MUST exist
    raw v1 SHA256(durable config) MUST equal legacy ConfigSHA256
    v2 semantic digest(durable config) MUST equal v2 digest(rendered target)
    upgrade journal

phase = marker_written:
    same config requirements as config_written
    marker relation is verified by normal recovery/verify path

committed generation == ToGeneration with surviving journal:
    same target config proof
    upgrade journal
    verify-and-clean; never rewrite a different target
~~~

Do not require the old raw target digest to equal the current from-config in `prepared`.

Do not silently accept a missing target config in any phase that claims config_written or later.

Concrete code split:

~~~go
func (m *Manager) upgradeLegacyTransitionJournal(...) (*ManagedLeshyTransition, error) {
    switch legacy.Phase {
    case ManagedTransitionPrepared:
        return m.upgradeLegacyPreparedTransition(...)
    case ManagedTransitionConfigWritten, ManagedTransitionMarkerWritten:
        return m.upgradeLegacyWrittenTransition(...)
    default:
        ...
    }
}
~~~

The prepared path does not need Windows path guessing: it has no durable target bytes to prove. It recomputes the target under the new state root and moves directly to the v2 semantic contract after proving the FROM committed state is coherent.

### Required tests

Extend:

~~~text
toad/internal/control/state_root_move_recovery_test.go
toad/internal/control/mode_transaction_test.go
~~~

Rows:

~~~text
legacy schema1 prepared across state-root move -> converges
prepared with coherent from-state but existing old from-config -> does NOT compare it to target digest
config_written missing generated config -> fail closed, journal retained
config_written raw digest mismatch -> fail closed
marker_written matching target -> converges
generation already committed + surviving schema1 journal -> verify-and-clean
unexplained semantic mismatch beyond auth state-root -> fail closed
~~~

### Native C8R old-release crash matrix

Replace the single broad `in-flight-transition` row with subrows staged by the failpoint core, then rewritten to the old schema-1 journal contract:

~~~text
in-flight-prepared
in-flight-config-written
in-flight-marker-written
in-flight-generation-committed
~~~

Use the existing failpoints:

~~~text
after_prepared
after_config_written
after_marker_written
after_generation_commit
~~~

Every row upgrades through state -> state/core and must converge or verify-clean automatically. A manual refusal is not acceptance.

## 3.3 C8R harness provenance/preflight

packaging/windows/test_state_layout_upgrade.ps1 prints candidate/failpoint/Leshy hashes but not harness SHA or source SHA, and calls Reset-Machine before proving the MSI is a valid Kikimora artifact.

Add mandatory:

~~~powershell
[ValidatePattern('^[0-9a-f]{40}$')]
[string]$SourceSha
~~~

Before first mutation print:

~~~text
source SHA
$PSCommandPath
harness SHA256
candidate MSI SHA256
failpoint core SHA256
Leshy SHA256
~~~

Perform read-only Windows Installer preflight before Reset-Machine:

~~~text
OpenDatabase(candidate, 0)
ProductName == Kikimora
ProductVersion nonempty
UpgradeCode nonempty
~~~

Reuse the small proven logic from test_managed_package_lifecycle.ps1::Test-KikimoraMsiArtifact.

Use one failure/evidence path. Infrastructure/mutation errors are terminating; retain Pass/Fail accounting for expected assertions. Preserve MSI log, service/process state, authority hashes, journal, generated Leshy diagnostics and logs on failure.

## 3.4 Exact C8R artifacts

After PC8A-PC8C/T1R2:

~~~text
P1R2 -> candidate MSI/zip from exact SHA S

failpoint core:
    build from S
    GOOS=windows GOARCH=amd64
    -tags kikimora_test_failpoints

Leshy:
    exact pinned 61786d489da2acf50d6b4b53a77a39184d746852
~~~

C8R records all hashes and SourceSha=S.

Windows VM was unavailable during this audit:

~~~text
scripts/lab/vm.py discover windows --source
192.168.3.72 documented fallback

TCP/22 -> unavailable
~~~

Use scripts/lab/vm.py after the VM is powered/provisioned; do not create another access helper.

---

# 4. T1R2 — complete the acceptance identity contract

T1R parser/lint CI is useful, but the roadmap identity rule is not uniformly implemented.

Missing/partial identity:

~~~text
Linux:
    local-leshy-routing-netns.sh          no source SHA / harness SHA
    managed-leshy-routing-netns.sh        no source SHA / harness SHA
    managed-reboot-matrix-vm.sh           source SHA, no harness SHA
    mode-transition-crash-vm.sh           no source SHA / harness SHA

Windows:
    test_managed_cutover.ps1              no source SHA / harness SHA
    test_state_layout_upgrade.ps1         no source SHA / harness SHA
    test_managed_leshy.ps1                source SHA, no harness SHA
    test_mode_transition_crash.ps1        no source SHA / harness SHA
    test_release_payload.ps1              no source/harness identity
~~~

One contract for every native acceptance script:

~~~text
canonical harness path
harness SHA256
expected source SHA
every verdict-affecting binary/artifact canonical path + SHA256
case/mode/phase
fixture root
~~~

When copied outside a checkout, source SHA is mandatory input:

~~~text
Linux:   --source-sha <40-hex>
Windows: -SourceSha <40-hex>
~~~

Prepare/Verify scripts persist source/harness/core/Leshy/helper hashes and Verify refuses mismatch before mutation.

Extend .github/workflows/toad.yml acceptance-harness jobs so they statically enforce this identity contract in addition to parse/lint/DNS tests.

---

# 5. Revalidation order

Existing P1R/X1 on 85db60d remains legitimate historical evidence but cannot authorize native acceptance after these source/harness fixes.

Mandatory order:

~~~text
PC8A
PC8B
PC8C
T1R2

P1R2
    integrated Linux/Windows artifact contracts on one exact SHA

X1R2
    gofmt
    go vet ./...
    go test ./...
    go test -race ./...
    tagged failpoint suites
    focused PC8 tests
    DNS fixtures
    T1R2 static contracts
    Desktop required suites/coverage
    Toad core workflow
    Desktop UI workflow
    Build packages workflow
    ShellCheck workflow

C8R
    legacy-local
    legacy-managed
    partial-destination
    in-flight-prepared
    in-flight-config-written
    in-flight-marker-written
    in-flight-generation-committed

G1-G6
H1-H6
I/J/U/K/L
~~~

Only the X1R2-green SHA may enter C8R/G/H.

No new Desktop architecture work is required by this audit. Desktop was green on 85db60d; UI reopens only if PC8 changes regress real-core/installed acceptance.

---

# 6. Stop conditions

Do not start C8R while any of these is true:

~~~text
active profile can drop its non-direct bypass owner
persisted policy corruption can silently become direct/default
profiles.list corruption can silently remove declared profiles
a managed default-VPN role can lack a stable leshy_zone identity
authoritative policy writes can acknowledge before durable replacement
multi-file profile composition can survive a crash half-committed
policy rollback can delete a pre-existing unreadable file
any failed live-policy rollback can leave route_authority_ready=true
default-VPN A -> B needs a new DNS query to protect an existing prefix
a current-owner Zone=bypass checkpoint cannot survive restart
bypass rollback failure clears the authority fence
destination-only state\core migration fails
C8R mutates before artifact/provenance preflight
T1R identity contract is incomplete
P1R2/X1R2 are not green on one exact post-fix SHA
~~~

Current entry point is **PC8A**.