# 09G-W4 — Per-profile default-route policy and complete profile lifecycle

Status: **EXECUTION PLAN**

Closes: JRN-12, JRN-13, JRN-18, JRN-19, JRN-29 and the profile/default
parts of JRN-16.

Depends on: W3 live role composition.

## Fixed architecture

There are two different concepts and they must remain different:

1. **Configured per-profile default preference**:
   durable map `profile -> direct | role`.
2. **Effective active default owner**:
   existing `bypass.conf` and the existing protected selected-route handover.

Do not replace the current bypass handover/rollback/quarantine engine.
Do not make QML issue `direct -> SetActiveProfile -> role` as three commands.

The profile preference file lives next to the profile registry:

```text
/etc/kikimora/toads/profile-defaults.json
```

It is profile metadata, not generated Leshy state.

## Commit W4.1 — durable profile-default preference store

### New file

Create `toad/internal/config/profile_defaults.go`.

Format:

```json
{
  "schema": 1,
  "profiles": {
    "home": "awg-home",
    "work": "direct"
  }
}
```

API:

```go
const ProfileDefaultsSchema = 1

type ProfileDefaults struct {
    Schema   int               `json:"schema"`
    Profiles map[string]string `json:"profiles"`
}

func ProfileDefaultsPath(configDir string) string
func ReadProfileDefaults(configDir string) (ProfileDefaults, error)
func WriteProfileDefaults(configDir string, value ProfileDefaults) error
```

Rules:

- missing file -> empty map, nil error;
- malformed JSON/schema/name/mode -> hard error;
- profile keys use `ValidateInstanceName`;
- value is `direct` or a valid role name;
- file mode 0600;
- durable atomic JSON write.

### Manager state

Add to `Manager`:

```go
profileDefaults map[string]string
```

In `newManagerWithDeps`, after declared profiles and loaded roles are known:

1. read profile-defaults;
2. validate every preference:
   - referenced profile exists;
   - non-direct role exists;
   - role belongs to that profile;
3. load into memory.

### Startup reconciliation rule

The **active/effective owner is authoritative in `bypass.conf`**.

Add in `toad/internal/control/config_api.go`:

~~~go
func (m *Manager) ReconcileActiveProfileDefault(
    desired PersistedDesiredState,
) error
~~~

The method is startup-only and runs after
`StartupManagedAuthority` has strict-loaded bypass.conf, but before
`ValidateStartupDesiredPolicy`.

Algorithm under policyMu + short m.mu:

1. resolve desired.ActiveProfile exactly like startup desired validation:
   empty -> default; unknown -> default only when default exists, otherwise
   error;
2. resolve current strict `bypassModeLocked()`;
3. if mode is non-direct require the role exists and belongs to the resolved
   profile; otherwise fail startup;
4. if `profileDefaults[profile]` already equals mode, return;
5. clone the map, assign `profile -> mode`;
6. durably `WriteProfileDefaults(configDir, clone)`;
7. assign the cloned map in memory and bump.

Modify `toad/cmd/kikimora-core/main.go` in both desired startup branches.

For an existing desired.json, immediately after Load succeeds and before
ValidateStartupDesiredPolicy:

~~~go
if err := manager.ReconcileActiveProfileDefault(desired); err != nil {
    return fmt.Errorf("reconcile active profile default: %w", err)
}
~~~

For missing desired.json, build the already-used
`PersistedDesiredState{ActiveProfile:"default"}`, call reconciliation first,
then ValidateStartupDesiredPolicy and PrepareStartupRoutingIntent with that same
value.

Do not run this reconciliation in the `!goOwnsLifecycle` branch because that
core is not the active lifecycle-policy owner.

This rule resolves a crash after preference write but before/after effective
handover: whatever strict bypass recovery proves as active becomes the active
profile's preference. Inactive profile preferences are untouched.

Add startup tests for missing file, corrupt file, stale active preference,
non-direct preference outside active profile, and valid inactive preferences.

## Commit W4.2 — include profile defaults in existing crash-atomic composition transaction

Modify:

- `toad/internal/config/create_profiles.go`
- `toad/internal/config/profile_undo.go`
- corresponding tests.

Extend:

```go
type ProfileCompositionPlan struct {
    Memberships      []ProfileMembership
    DeclaredProfiles *[]string
    ProfileDefaults  *map[string]string
}
```

Extend `ProfileCompositionTxn` with one optional defaults file snapshot and
expose:

```go
func (tx *ProfileCompositionTxn) Defaults() *ProfileUndoImage
```

Prepare/commit/rollback order:

1. role TOMLs;
2. profiles.list;
3. profile-defaults.json.

Every file retains exact original bytes/mode/existence for rollback.

Extend `ProfileUndoFile.Kind` with:

```text
defaults
```

and map it in `undoFilePath` to
`ProfileDefaultsPath(configDir)`.

Existing recovery remains before NewManager loads role/profile state.

### CreateProfile integration

In `setProfileRolesPolicyLocked(..., declare=true)`:

- clone current `m.profileDefaults`;
- if new profile has no entry, add `name -> direct`;
- include that complete map in `ProfileCompositionPlan.ProfileDefaults`;
- after commit update `m.profileDefaults`;
- on rollback restore old map.

The default profile may exist implicitly; ensure it also resolves to a
preference through the accessor below, defaulting to direct when absent.

## Commit W4.3 — profile-default API and protected bypass helper refactor

### Manager API

Add:

```go
func (m *Manager) ProfileDefaults() map[string]string
func (m *Manager) ProfileDefault(profile string) string
func (m *Manager) SetProfileDefault(ctx context.Context, profile, mode string) error
```

`ProfileDefault` returns `direct` for a known profile with no explicit entry.

### Refactor SetBypassMode

Keep public compatibility:

```go
func (m *Manager) SetBypassMode(mode string) error
```

Implementation:

1. lock policyMu;
2. read active profile;
3. call private:
   `setBypassModeForProfilePolicyLocked(ctx, activeProfile, mode)`;
4. unlock.

Move the existing protected handover body into that private helper.
It assumes policyMu is already held and must never lock it again.

Refactor validation to:

```go
func (m *Manager) validateBypassRoleForProfileLocked(
    role string,
    profile string,
    requireEnabled bool,
) error
```

It must check:

- role exists;
- role belongs to the supplied profile;
- managed zone identity valid;
- not removalPending;
- if `requireEnabled`, role enabled.

Do not weaken any existing selected-route preflight/drain/rollback logic.

All existing SetBypassMode tests remain and call the public wrapper.

### SetProfileDefault semantics

Under policyMu:

**Inactive profile**

- mode must be direct or a role belonging to that profile;
- role need not be connected;
- update profile-defaults file transactionally;
- update `m.profileDefaults`;
- bump revision;
- do not touch bypass.conf or running roles.

**Active profile, mode=direct**

- snapshot old profile-defaults;
- write new preference;
- call private protected bypass helper to direct;
- if handover fails, restore exact old defaults image/map;
- success only when both preference and effective bypass commit.

**Active profile, mode=<role>**

- role must already be enabled;
- write preference;
- protected handover to role;
- rollback exact preference image/map on failure.

A disconnected role is **not** submitted to SetProfileDefault for the active
profile. The UI disables that choice and shows the explicit precondition
"Сначала подключите роль". This preserves the existing rule that policyMu is
never held while waiting for a Toad start and eliminates the current invisible
guaranteed rejection.

For an **inactive** profile a disconnected role may be stored as its preference;
when that profile later becomes active, SetActiveProfile performs the staged
start + protected handover described in W4.4.

### Snapshot/API

Add `profile_defaults` to `control.Snapshot`:

```go
ProfileDefaults map[string]string `json:"profile_defaults"`
```

Add API method `SetProfileDefault` using Request.Profile + Request.Mode.

Keep `bypass_mode` in snapshot/API as the effective active owner for
diagnostics/compatibility.

Extend `subscriptionFingerprint` in the same commit:

1. marshal `snapshot.ProfileDefaults` with `encoding/json`; string-key map
   ordering is deterministic in Go's JSON encoder;
2. append the resulting JSON string to fingerprint parts;
3. after W4.4 adds the composition-transition snapshot, append its
   Active/Kind/From/To fields too.

Add a test where revision changes only because a profile default or transition
field changes; the subscription fingerprint must change.

Do not include the random transition ID in the UI snapshot or fingerprint.

## Commit W4.4 — crash-safe atomic profile switch with different defaults

### New journal

Create `toad/internal/control/profile_switch_journal.go`, **not** a config
package file. The journal embeds `PersistedDesiredState`, so keeping it in
control avoids a config -> control import cycle.

Path:

```text
<core-state-dir>/profile-switch.json
```

Schema:

```go
const ProfileSwitchJournalSchema = 1

type ProfileSwitchPhase string
const (
    ProfileSwitchPrepared         = "prepared"
    ProfileSwitchBypassInProgress = "bypass_in_progress"
    ProfileSwitchBypassCommitted  = "bypass_committed"
    ProfileSwitchDesiredCommitted = "desired_committed"
)

type ProfileSwitchJournal struct {
    Schema          int                   `json:"schema"`
    Phase           ProfileSwitchPhase    `json:"phase"`
    ID              string                `json:"id"`
    FromProfile     string                `json:"from_profile"`
    ToProfile       string                `json:"to_profile"`
    FromDefault     string                `json:"from_default"`
    ToDefault       string                `json:"to_default"`
    PreviousDesired PersistedDesiredState `json:"previous_desired"`
    TargetDesired   PersistedDesiredState `json:"target_desired"`
}
```

Write mode 0600 with durable atomic JSON. ID is crypto-random 128-bit lowercase
hex generated by:

```go
func NewProfileSwitchID() (string, error)
```

using `crypto/rand` 16 bytes + `hex.EncodeToString`.

Add exact APIs:

```go
func ProfileSwitchJournalPath(stateDir string) string
func WriteProfileSwitchJournal(stateDir string, j ProfileSwitchJournal) error
func ReadProfileSwitchJournal(stateDir string) (ProfileSwitchJournal, bool, error)
func MarkProfileSwitchPhase(stateDir, id string, phase ProfileSwitchPhase) error
func RemoveProfileSwitchJournal(stateDir string) error

func RecoverProfileSwitchJournal(
    ctx context.Context,
    stateDir string,
    configDir string,
    bypassPath string,
    desired DesiredStateStore,
) error
```

Every write uses `fsutil.WriteAtomicFile(..., 0600)`; removal uses
`fsutil.RemoveDurable`.

`ReadProfileSwitchJournal` validates:
- schema;
- ID exactly 32 lowercase hex;
- known phase;
- valid From/To profile names;
- FromProfile != ToProfile;
- default values are direct or valid role names;
- both embedded desired states have schema 1 and nonempty role maps.

### Startup recovery order

In `toad/cmd/kikimora-core/main.go::runServe`, hoist desired-store
construction into the existing `opts.stateDir != ""` block **before**
`NewManagerWithConfigDir`:

~~~go
var desiredStore control.DesiredStateStore
if opts.stateDir != "" {
    desiredStore = control.FileDesiredStateStore{
        Path: filepath.Join(opts.stateDir, "desired.json"),
    }

    // existing core.lock acquisition
    // existing RecoverProfileCompositionUndo

    bypassPath := opts.bypassConfig
    if bypassPath == "" {
        bypassPath = config.DefaultBypassPath()
    }
    if err := control.RecoverProfileSwitchJournal(
        ctx,
        opts.stateDir,
        opts.configDir,
        bypassPath,
        desiredStore,
    ); err != nil {
        return fmt.Errorf("recover profile switch: %w", err)
    }
}
~~~

Later, after Manager is created, reuse this exact `desiredStore` variable in
the existing `manager.SetDesiredStateStore(desiredStore)` block; delete the
second FileDesiredStateStore construction currently around main.go line 435.

Run switch-journal recovery whenever StateDir exists and a journal is present,
regardless of the *current* ownership file. This is identical in principle to
ProfileCompositionUndo recovery: it finishes/rolls back an already-started
durable transaction before any role/profile state is loaded. It does not start
a new VPN lifecycle mutation. After recovery, the later existing
`goOwnsLifecycle` gate still decides whether desired state is applied to
processes.

Because Manager does not exist yet, recovery validates profile/role identity
directly from disk:
1. read declared profiles from `config.ReadDeclaredProfiles(configDir)`;
2. load every `*.toml` role with `config.Load`;
3. reconstruct the same implicit `default` + explicit profile membership map;
4. require FromProfile/ToProfile exist;
5. require every non-direct FromDefault/ToDefault role exists and belongs to
   the corresponding profile;
6. then read strict `bypass.conf` and execute the phase rule below.

Recovery reads strict current `bypass.conf` and journal:

- phase prepared:
  - no effective handover was committed by contract;
  - TargetDesired **may already have been written** because Phase C persists it
    before marking bypass_in_progress;
  - overwrite desired.json with PreviousDesired exactly;
  - remove journal only after that restore succeeds.
- phase bypass_in_progress:
  - if strict bypass == FromDefault: restore PreviousDesired; remove journal;
  - if strict bypass == ToDefault: write TargetDesired; remove journal;
  - anything else: fail startup closed.
- phase bypass_committed or desired_committed:
  - require strict bypass == ToDefault;
  - write TargetDesired;
  - remove journal.
- malformed journal or impossible profile/default identity: fail startup.

Existing selected-route checkpoint startup recovery remains responsible for
finishing/proving an interrupted protected bypass handover toward the strict
bypass owner. Do not duplicate route repair in this journal.

### Manager transition state

Add:

```go
type compositionTransition struct {
    Active     bool
    Kind       string // "profile-switch"
    ID         string
    From       string
    To         string
    StagedRole string
}
compositionTransition compositionTransition
```

Publish a non-secret snapshot field:

```json
"composition_transition": {
  "active": true,
  "kind": "profile-switch",
  "from": "home",
  "to": "work"
}
```

No random journal ID needs to reach UI.

Every profile/membership/default/role-config CRUD operation acquires policyMu,
then checks `compositionTransition.Active` under m.mu and rejects while a
switch is active. This transition token is what allows SetActiveProfile to
release policyMu while waiting for a target Toad without allowing another
policy transaction to interleave.

### Prepared target-default warmup primitive

Before replacing SetActiveProfile, add a non-persistent warmup state to the
existing `role` struct in `toad/internal/control/control.go`:

~~~go
profileWarmupID    string
profileWarmupReady bool
~~~

This state exists only while compositionTransition is active and is never
written to desired.json. A core crash therefore falls back to the last durable
profile/desired state through the journal recovery.

Add helpers, called with m.mu held:

~~~go
func (m *Manager) beginProfileWarmupLocked(role, transitionID string) error
func (m *Manager) profileWarmupReadyLocked(role, transitionID string) bool
func (m *Manager) clearProfileWarmupLocked(role, transitionID string)
~~~

`beginProfileWarmupLocked` requires:
- role exists;
- no existing warmup;
- role belongs to the transition target profile, checked by the caller;
- role is currently disabled and process is nil.

It sets:
- profileWarmupID=transitionID;
- profileWarmupReady=false;
- enabled=true;
- product Desired=true;
- increments role operation;
- **does not call persistDesiredLocked**.

This temporary Desired=true is allowed only under compositionTransition and
never survives a crash.

#### Suppress normal publication/recovery while warming

Modify `scheduleRouteTargetRecovery`:
- if `r.profileWarmupID != ""`, return without starting recovery.

Modify `scheduleValidation`.

After `ValidateRole` succeeds and before `activateReadyRole`:

1. lock m.mu;
2. if the same process still belongs to a role with nonempty profileWarmupID:
   - set profileWarmupReady=true;
   - bump;
   - unlock;
   - call `finishValidationSchedule`;
   - return **without** `activateReadyRole`.

For `ErrValidationPending` during warmup:
- keep the existing Recovering/validation-pending state so later Toad snapshots
  can retry validation;
- do **not** schedule `recoverRole` or RecoveryStartTransport from the core;
- the 30-second profile-switch deadline owns failure/timeout.

For any other validation error during warmup:
- record the error/Failed state as normal;
- do not start automatic recovery.

Modify `activateReadyRole` with a defensive first check:
- if the role has nonempty profileWarmupID, return
  `"role <id> is prepared for profile switch and cannot publish yet"`.
No code path may publish the role's ordinary Leshy zone during warmup.

The warmup is allowed to run `prepareRoleStart/ApplyEndpoint`: endpoint
underlay policy is required to establish the tunnel and does not publish the
role's ordinary traffic zone. The Toad process/interface may exist, but
`product.Publication.Published` must remain false.

Add:

~~~go
func (m *Manager) rollbackProfileWarmup(
    ctx context.Context,
    role, transitionID string,
) error
~~~

It verifies both `compositionTransition.ID` and
`compositionTransition.StagedRole`, sets enabled/product Desired=false,
clears warmup/start retry state, marks product Stopped, then outside m.mu calls
`stopRoleProcess` + `releaseRoleResources`. It returns only after endpoint/
park/publication residue is proved released. It is valid both before and after
`promoteProfileWarmup` clears the warmup fields, as long as the owning
compositionTransition still names the staged role.

Add:

~~~go
func (m *Manager) promoteProfileWarmup(
    ctx context.Context,
    role, transitionID string,
) error
~~~

It verifies `profileWarmupReady`, clears the warmup fields under m.mu, bumps,
then calls `activateReadyRole(ctx, role)` outside m.mu. Success means the
ordinary role zone is now published under the newly committed active profile.

#### Warmup tests

Add tests proving:
- warmup Toad reaches healthy validation with live ifindex;
- `Publication.Published` remains false before promotion;
- role-specific Leshy Publish is never called during warmup;
- selected-route bypass preflight accepts the healthy unpublished warmup role;
- promotion publishes once;
- rollback stops process and removes endpoint/resource residue;
- validation pending does not start automatic recovery;
- core crash before bypass commit restores old durable profile and does not
  resurrect warmup intent.

### Replace SetActiveProfile algorithm

`SetActiveProfile(name)` remains the public API but uses this algorithm.

**Phase A — prepare under policyMu**

1. validate target profile;
2. no-op if already active;
3. reject an existing compositionTransition;
4. resolve:
   - `fromDefault = effective bypass owner`;
   - `toDefault = ProfileDefault(name)`;
5. validate toDefault belongs to target profile if non-direct;
6. capture PreviousDesired;
7. build TargetDesired:
   - ActiveProfile=name;
   - Roles true exactly for target members;
8. generate a fresh 128-bit transitionID and write it into the prepared journal;
9. set in-memory compositionTransition Active/Kind/ID/From/To;
10. if toDefault is non-direct and currently disabled:
    - set `compositionTransition.StagedRole = toDefault`;
    - call `beginProfileWarmupLocked(toDefault, transitionID)`;
11. if toDefault is already enabled, require it is already valid under the
    existing runtime (normally because it is shared with the old profile) and
    leave StagedRole empty;
12. do not change any other target role yet;
13. bump snapshot;
14. release m.mu and **release policyMu before any process start/wait**.

The SetActiveProfile worker clears compositionTransition on every proved
terminal success/failure path, and only while its ID still matches.

Do **not** persist TargetDesired yet.

**Phase B — stage only the target default role with no policy lock held**

Create `readinessCtx, readinessCancel := context.WithTimeout(ctx, 30*time.Second)`
and defer/call readinessCancel when Phase B ends.

If Phase A staged toDefault, start **only that role** with existing
`startRoleProcess(readinessCtx,...)`. No other target-profile role may start
before the durable active-profile commit; otherwise its role-specific routing
can become effective while the old profile is still active.

While compositionTransition is active, every other policy mutation may acquire
policyMu but must observe the transition and reject; therefore no long-held
policy lock is needed.

If toDefault is non-direct, wait event-driven on Manager revision/changed
notifications using readinessCtx.

**Staged warmup target readiness is exact:**

Under m.mu require all of:

~~~go
compositionTransition.Active
compositionTransition.ID == transitionID
compositionTransition.StagedRole == toDefault
role.profileWarmupID == transitionID
role.profileWarmupReady == true
role.process != nil
role.stateValid == true
~~~

Then read the product role and require:

~~~text
Desired == true
Validation.Healthy == true
ToadGeneration != 0
Toad.IfIndex > 0
Publication.Published == false
~~~

Do not call SelectedRouteManager.PreflightBypassTarget as the wait predicate.
That preflight belongs to Phase C after policyMu is reacquired and the
transition token is rechecked.

**Already-enabled/shared target readiness is exact:**

Require the role belongs to both the old and target profile, then require:

~~~text
Desired == true
Validation.Healthy == true
ToadGeneration != 0
Toad.IfIndex > 0
~~~

Its existing Publication.Published may remain true because it is already part of
the active runtime and was not staged by this transition.

Terminal Failed or readiness timeout:
- if this transition staged toDefault, call
  rollbackProfileWarmup(context.Background(), toDefault, transitionID);
- reacquire policyMu;
- under m.mu verify compositionTransition.ID still matches;
- if warmup rollback succeeded, restore PreviousDesired on disk if the prepared
  phase had already prewritten TargetDesired, remove journal, clear transition,
  bump and return the readiness error;
- if cleanup is unproved, keep transition/journal evidence, quarantine policy
  authority and return the joined error.

All other target roles remain exactly in PreviousDesired until Phase D. They
start only through the normal post-commit profile settlement.

### Desired-store helper used by the transition

Reuse `Manager.saveDesiredSnapshot` introduced by W3.6 in
`toad/internal/control/desired_state.go`. Do not add a second implementation.

Its contract remains: copy only the desired store under m.mu, perform the
durable Save with no Manager lock held, then reacquire m.mu only to update
desiredStateStatus. It never mutates activeProfile or role enabled flags.

**Phase C — persist target intent, then perform protected effective handover**

1. acquire policyMu;
2. under m.mu verify compositionTransition is still active and its ID matches;
   otherwise abort this stale worker without policy mutation;
3. call `saveDesiredSnapshot(ctx, TargetDesired)` **before changing runtime
   desired flags or bypass owner**;
4. if saving TargetDesired fails:
   - release policyMu;
   - if a warmup role was staged, call
     `rollbackProfileWarmup(context.Background(), toDefault, transitionID)`;
   - reacquire policyMu, verify transition ID, remove the prepared journal,
     clear transition and return the save error;
   - if warmup cleanup is unproved, quarantine policy authority and keep the
     in-memory transition active until restart;
5. target desired is now durable, but runtime remains PreviousDesired/A;
6. mark journal `bypass_in_progress`;
7. call private
   `setBypassModeForProfilePolicyLocked(ctx, name, toDefault)`;
8. if the handover returns failure:
   - when the bypass helper proved rollback to fromDefault, restore
     `PreviousDesired` with `saveDesiredSnapshot`;
   - if that desired restore succeeds, the durable product policy is A again;
     remove the profile-switch journal, release policyMu, rollback the staged
     warmup role, then clear transition;
   - if desired restore, bypass rollback or warmup cleanup is unproved, leave
     the journal/transition needed for recovery, quarantine policy authority
     and return the joined error;
9. after a successful handover, mark journal `bypass_committed`.
   If this phase-mark write itself fails, keep the existing
   `bypass_in_progress` journal: strict bypass is now toDefault and
   TargetDesired is already durable, so startup recovery still deterministically
   rolls forward. Record the phase-mark failure in diagnostics but continue the
   committed switch.

The old profile roles remain runtime-enabled through this phase. The staged
target default is healthy but its ordinary role zone is unpublished. The only
traffic ownership moved before the profile commit is the protected global
bypass handover.

**Phase D — publish the already-durable profile commit in memory and converge**

At entry:
- TargetDesired is durable;
- strict bypass owner is toDefault/direct;
- journal is bypass_committed or bypass_in_progress with strict bypass==toDefault;
- old profile processes are still running;
- staged target default, if any, is warm-ready and ordinary publication is off.

Under m.mu:

1. verify transition ID;
2. set `activeProfile=name`;
3. set every role enabled flag exactly from TargetDesired;
4. call `product.SetRoleDesired` for each changed role;
5. increment `profileSwitchEpoch`;
6. capture `changed/want/epoch` for settlement;
7. bump.

Do **not** call persistDesiredLocked here: TargetDesired was already durably
saved in Phase C.

Mark journal `desired_committed`. A failure to advance this phase is not a
rollback condition because desired+bypass are already a consistent target
commit; keep the older recoverable journal phase and record a diagnostics
warning.

Attempt to remove the journal. If removal fails, keep running the committed
target profile and record a diagnostics warning; startup recovery is
idempotent and will remove/settle the retained journal.

Release policyMu.

If this transition staged toDefault:
1. call `promoteProfileWarmup(ctx, toDefault, transitionID)`;
2. success publishes the ordinary target role zone;
3. failure is **post-commit convergence failure**, not a profile rollback:
   - keep TargetDesired/toDefault committed;
   - mark that role Recovering with the promotion error;
   - invoke the existing bounded recovery pipeline for that role so publication
     can converge;
   - include the warning in debug/diagnostics.

Run the existing fenced profile settlement with the captured epoch:
- start newly desired non-default target roles;
- stop/release departing old-profile roles;
- converge enabled roles.

The SetActiveProfile API keeps this settlement asynchronous exactly as today. It returns after the committed target intent is installed and the settlement is scheduled; individual role Ready/Failed state continues through snapshots.

Finally under m.mu clear compositionTransition if the ID still matches and bump.
No other policy mutation is admitted before this clear.

### Concurrency tests

Add:
- A->B with different defaults, no direct window;
- A->B target default initially disconnected -> staged start then handover;
- target default start failure leaves A fully active;
- bypass handover failure leaves A;
- TargetDesired save failure before handover leaves A fully active and bypass unchanged;
- crash after TargetDesired prewrite while journal is prepared restores PreviousDesired/A;
- crash/failpoint during bypass_in_progress with strict bypass A rolls back A;
- crash/failpoint during bypass_in_progress with strict bypass B rolls forward TargetDesired/B;
- crash after bypass commit before in-memory commit rolls forward B;
- failure to mark desired_committed or remove journal still restarts deterministically to B;
- target-default ordinary publication failure after commit leaves B committed and the role Recovering, never rolls back traffic to A;
- failpoint/restart coverage for every journal phase;
- concurrent SetProfileRoles/SetProfileDefault rejected during transition;
- rapid A->B->A only newest committed transition survives; no stale settlement.

## Commit W4.5 — profile rename

Add Manager:

```go
func (m *Manager) RenameProfile(ctx context.Context, from, to string) error
```

Rules:

- default cannot be renamed;
- from exists;
- to valid and does not exist;
- reject while compositionTransition is active.

Under policyMu build one `ProfileCompositionPlan`:

1. for every role membership replace from -> to;
2. declaredProfiles replace from -> to;
3. profileDefaults move key from -> to;
4. if activeProfile==from, desired state's ActiveProfile becomes to.

Use existing ProfileCompositionTxn + ProfileCompositionUndo, including the new
defaults image.

After durable commit:
- update all in-memory cfg.Profiles;
- declaredProfiles;
- profileDefaults;
- activeProfile when needed;
- rebuildProfileMapLocked;
- persist desired;
- mark undo committed;
- bump.

No process start/stop and no bypass handover is needed because role set and
default owner are unchanged by a pure profile-name rename.

Rollback on desired save failure uses existing exact file images and restores
all in-memory maps/name.

API: `RenameProfile` with Request.Profile=from and add
`NewProfile string` to Request.

## Commit W4.6 — profile remove

Add:

```go
func (m *Manager) RemoveProfile(ctx context.Context, name, fallback string) error
```

Rules:

- default cannot be removed;
- profile must exist;
- fallback required only when removing active profile;
- fallback must exist and differ from name.

If removing active profile:

1. call the new `SetActiveProfile(fallback)` **before** taking the delete
   transaction's policyMu;
2. if switch fails, no deletion occurs;
3. a crash between successful switch and delete leaves the old profile present
   but inactive — safe and retryable.

Then under policyMu delete profile metadata:

- remove name from declaredProfiles;
- remove name from every role cfg.Profiles; if no custom membership remains,
  persist `["default"]` so existing membership transaction validation stays
  satisfied;
- remove name from profileDefaults;
- commit through ProfileCompositionTxn + undo journal;
- update memory/rebuild map/bump.

Since profile is now inactive, desired running composition does not change.

API `RemoveProfile` uses Request.Profile + add
`FallbackProfile string`.

## Commit W4.7 — safe disconnect action for current default owner

Do **not** weaken `DisconnectRole`. Keep
`TestDisconnectRoleRejectsCurrentBypassOwner` unchanged.

Add a higher-level method:

```go
func (m *Manager) DisconnectDefaultRole(ctx context.Context, role string) error
```

Contract:

1. verify role is the current effective bypass owner;
2. call `SetProfileDefault(ctx, activeProfile, direct)`;
3. after successful protected handover, call ordinary `DisconnectRole`.

A crash between steps leaves role connected but default direct, which is safe.
Retry finishes disconnect.

In `snapshotRoleLocked`:

- when role is enabled and is current bypass owner, advertise
  `disconnect-and-direct` instead of bare `disconnect`;
- ordinary enabled non-owner keeps `disconnect`;
- during compositionTransition, advertise no mutating per-role actions.

API method: `DisconnectDefaultRole`.

RealCoreClient maps action `disconnect-and-direct` to that method.
UI label: `Отключить и пустить остальное напрямую`.

This preserves the old rejection invariant while giving the user a valid
action.



### Handshake capabilities for W4 semantics

In `toad/internal/control/api.go` define and append:

~~~go
const (
    CapabilityProfileDefaultsV1 = "profile-defaults-v1"
    CapabilityProfileCRUDV1     = "profile-crud-v1"
)
~~~

Also advertise method strings:

~~~text
SetProfileDefault
RenameProfile
RemoveProfile
DisconnectDefaultRole
~~~

Keep the current API version unchanged. The desktop shows per-profile default
controls only with `profile-defaults-v1`; rename/delete controls require
`profile-crud-v1`. On an older core, retain read-only profile selection and
the old compatibility surfaces rather than sending unknown methods.

## Commit W4.8 — desktop profile/default UI

### CoreBackend/RealCoreClient/FakeCore

Add property:

```cpp
Q_PROPERTY(QVariantMap profileDefaults READ profileDefaults NOTIFY snapshotChanged)
Q_PROPERTY(bool compositionTransitionActive READ compositionTransitionActive NOTIFY snapshotChanged)
```

Add invokables returning W2 request IDs:

```cpp
setProfileDefault(profile, mode)
renameProfile(from, to)
removeProfile(name, fallback)
```

In RealCoreClient::commandClass add SetProfileDefault, RenameProfile and
RemoveProfile as CommandClass::Mutate. Add DisconnectDefaultRole as
CommandClass::Mutate as well: it changes default-route policy before disconnect,
so it must require safeToMutate rather than the looser Disconnect class.
SetActiveProfile remains Mutate.

`setBypassMode` remains compatibility/diagnostics but normal Home UI no longer
calls it directly.

### KDefaultRouteRow

Bind displayed configured preference to:

```qml
Core.profileDefaults[Core.activeProfile] || "direct"
```

Choices are current profile roles + direct.

For the active profile, disconnected roles remain visible but disabled with the
inline reason `Сначала подключите роль`. Do not send SetProfileDefault for a
disabled choice.

For an inactive profile's settings surface, the same role may be stored as the
future preference even while disconnected; SetActiveProfile will stage it when
that profile is selected.

Do not locally change the displayed preference before matching command success
+ snapshot.

### Profile selector

On one user selection call only `setActiveProfile(target)`.

While compositionTransitionActive:
- selector disabled;
- show `Переключение профиля A → B…`;
- disable every per-role mutation;
- disable aggregate Connect/Disconnect;
- Settings mutations that change policy/config are disabled.

The transition is bounded by the 30-second target-default readiness deadline
plus the existing bounded bypass-handover operations. The current control
protocol handles one command at a time per connection, so do not implement a
UI cancellation control that cannot be processed until SetActiveProfile
returns. Other control clients that connect concurrently are rejected by the
compositionTransition gate rather than racing the transition.

### Profile CRUD UI

In ProfileDrawer header add menu/buttons:
- Rename;
- Delete.

Rename dialog:
- current name read-only;
- required new name;
- closes only on matching command success.

Delete:
- inactive: direct confirmation;
- active: require fallback profile ComboBox; one RemoveProfile command handles
  switch then delete;
- no fallback => delete disabled.

After CreateProfile success, automatically call/open the newly created profile
in UI only after it appears in authoritative snapshot. This is presentation,
not another persisted mutation.

### Empty-profile hint

Use the same filtered role model/count as Home. Remove the global
`root.roles.rowCount()` condition from ProfileDrawer.

## W4 mandatory gates

Keep every existing bypass handover/rejection test green.

Add Go tests for:
- defaults store validation and crash-durable writes;
- profile composition defaults rollback;
- active/inactive SetProfileDefault;
- active-profile disconnected SetProfileDefault rejects with zero mutation;
- inactive profile may persist a disconnected role preference;
- A->B with that disconnected target preference stages start then handover;
- 30s timeout/failure rollback;
- A->B different defaults;
- every profile-switch journal failpoint/restart;
- concurrent DisconnectAll from a second control connection is rejected while compositionTransition is active and performs zero mutation;
- rename active/inactive profile;
- remove inactive profile;
- remove active with fallback;
- old DisconnectRole owner rejection;
- new DisconnectDefaultRole handover then disconnect.

Desktop:
- one-action A->B switch;
- no intermediate direct selection visible to user;
- active-profile disconnected default choice is disabled with visible reason;
- inactive profile may save a disconnected future default, and switching to it
  stages/connects that role before handover;
- owner role action is disconnect-and-direct, not impossible disconnect;
- profile rename/delete/create-open;
- empty profile hint in Home and ProfileDrawer.

W4 is done only when profiles behave as complete user-owned contexts and a
profile switch with different default VPNs is one core operation with no
unprotected route interval.
