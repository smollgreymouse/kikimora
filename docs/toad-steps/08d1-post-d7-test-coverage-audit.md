# 08D1 post-D7 test-coverage audit

Status: **COMPANION TO `08d1-post-d7-audit-and-continuation.md`**

No production code was changed by this audit.

Audited baseline:

~~~text
Kikimora docs head:
    302bc6da2c876c2c468c4bbe43b78aead0f6db37

underlying D7 implementation:
    0f964a4f577a5cbd7cb29653977394f701012be5

Leshy:
    d6cfd2be1a2b3cf8c5032c68a5103a730b01af5f
~~~

Current targeted baseline tests are green:

~~~text
cd toad
go test ./internal/control ./internal/routing

internal/control  PASS
internal/routing  PASS
~~~

This is important evidence: the post-D7 P0 findings are not already caught by
the current suite. The suite is green while the production invariants below
are false.

---

# 1. Deferred managed restore coverage is currently misleading

Production entry points:

~~~text
toad/internal/control/control.go
    newManagerWithDeps
    CommittedManagedMode
    RestoreCommittedManagedState

toad/internal/control/mode_transaction.go
    RecoverManagedModeTransition

toad/cmd/kikimora-core/main.go
    runServe
~~~

Current test:

~~~text
toad/internal/control/startup_ready_test.go
    TestDeferredManagedRestoreAppliesMarkerOnDemand
~~~

That test stages a marker before construction, lets DeferManagedRestore cache
it in `pendingManagedMarker`, and later restores the same cached object. It
does not model a transition recovery that changes the marker on disk between
construction and restore.

Required regression tests:

~~~text
TestDeferredProductionRestoreReadsMarkerAfterRecoveryEnable
TestDeferredProductionRestoreReadsAbsenceAfterRecoveryDisable
TestCommittedMarkerGenerationMustEqualCommittedCounter
~~~

These tests must construct a fresh production/deferred Manager, run journal
recovery, then re-read committed mode from disk before restore.

---

# 2. Existing D7 recovery tests do not reconstruct the crashed process

Current tests:

~~~text
toad/internal/control/mode_transaction_test.go
    TestRecoverManagedModeTransitionRollsForward
    TestRecoverManagedModeTransitionVerifiesACommittedTarget
    TestRecoverManagedModeTransitionFailsClosedOnAStaleJournal
~~~

They run recovery on one already-created Manager. They prove the journal
algorithm in isolation but cannot detect stale pre-recovery in-memory state.

Mandatory fresh-manager matrix:

~~~text
Local -> Managed, committed == From
Managed -> Local, committed == From
Local -> Managed, committed == To
Managed -> Local, committed == To
~~~

For every row:

~~~text
stage exact durable crash state
discard old Manager
construct production/deferred Manager
RecoverManagedModeTransition
fresh committed-state load
RestoreCommittedManagedState
verify final mode/config/checkpoint fence
~~~

---

# 3. startup_order_test is not the production startup-order gate

Current test:

~~~text
toad/internal/control/startup_order_test.go
    TestConstructorDoesNotRegenerateBeforeModeRestore
~~~

It uses `NewManager`, whose test/default path immediately applies the marker.
Production uses `NewManagerWithConfigDir` with deferred managed restore.

Keep the old test only if it remains explicitly scoped to the immediate/test
constructor. Add production startup-order tests that prove:

~~~text
transition recovery
-> fresh committed-state load
-> auth policy selection
-> committed-state restore
-> Leshy config regeneration/read-back verification
-> route-authority startup proof
-> Serve/readiness
~~~

Any earlier error must prevent Serve/readiness.

---

# 4. Current checkpoint tests serialize the fence but do not require it

Production entry points:

~~~text
toad/internal/control/selected_routes.go: writeCheckpoint / restoreManagedCheckpoints
toad/internal/routing/selected_registry.go: ValidateSelectedRoutesCheckpoint
~~~

`writeCheckpoint` currently writes Schema/Role/RouteGeneration/Entries/Platform
and omits ActivationID and ModeGeneration.

`ValidateSelectedRoutesCheckpoint` currently accepts schema2 checkpoints with
empty ActivationID and zero ModeGeneration.

Existing round-trip coverage already shows the fields can serialize, but many
valid fixtures omit them.

Required test surgery:

~~~text
all valid schema2 fixtures carry a real 32-hex ActivationID
all valid schema2 fixtures carry ModeGeneration > 0
empty ActivationID -> reject
malformed ActivationID -> reject
ModeGeneration == 0 -> reject
checkpoint activation != marker activation -> reject before RestoreRole
checkpoint generation != marker generation -> reject before RestoreRole
~~~

`seededCheckpoint` in `selected_restore_test.go` must use the current marker
fence, not an unfenced schema2 fixture.

Add one production-path test that drives a real managed selected transaction,
reads `selected-routes.json`, and proves the current marker fence was written.

---

# 5. Current wire test explicitly enforces the obsolete three-field request

Current test:

~~~text
toad/internal/control/leshy_routes_api_test.go
    TestLeshyRouteWireTypesRoundTrip
~~~

It explicitly asserts that the request contains exactly:

~~~text
zone
prefix
source
~~~

D8 must replace that expectation with exactly:

~~~text
zone
prefix
source
activation_id
mode_generation
~~~

The result must also round-trip ActivationID and ModeGeneration.

Add a decode fixture using the exact JSON shape emitted by the current Leshy
`src/routing/kikimora.rs`; do not invent a second wire shape in Go tests.

---

# 6. Current Handshake test only proves v1

Current test:

~~~text
toad/internal/control/leshy_routes_api_test.go
    TestAPIV3HandshakeAdvertisesManagedRoutesCapability
~~~

The constant it checks is currently `managed_selected_routes_v1`.

Required capability matrix:

~~~text
development/no persistent StateDir:
    no v2 privileged capability

persistent local install with auth machinery:
    af_unix_control
    managed_selected_routes_v2
    privileged_control_auth_v1

active schema2:
    same v2 support
    v1 route requests denied before mutation

final Phase I:
    v1 not advertised anywhere
~~~

Do not merely rename the constant while keeping unconditional advertisement.

---

# 7. Current auth tests do not cover the empty-token bypass

Current API test proves only a non-empty wrong credential.

Happy-path transaction tests directly call:

~~~text
EnableManagedLeshy(..., "", ...)
DisableManagedLeshy("")
~~~

Those direct tests may remain for transaction mechanics but cannot count as
the privileged API gate.

Add API-level tests:

~~~text
Enable + empty token + persistent key -> denied
Disable + empty token + persistent key -> denied
Ensure + empty token under schema2 -> denied
Forget + empty token under schema2 -> denied
Flush + empty token under schema2 -> denied
wrong token rejected before malformed intent parsing
development/no StateDir cannot advertise/activate v2
~~~

For every denial assert no journal/marker/generation/registry/checkpoint/kernel
mutation.

---

# 8. Local-mode writer authorization needs defense-in-depth tests

Production entry:

~~~text
toad/internal/control/selected_routes.go
    SelectedRouteManager.Ensure
    SelectedRouteManager.Forget
    SelectedRouteManager.FlushZone
~~~

The API layer must reject local mode, but the selected-route manager itself
must also reject a non-managed role so an internal caller cannot violate the
one-writer invariant.

Add direct tests:

~~~text
Ensure local role -> denied, zero writer calls
Forget local role -> denied, zero writer calls
FlushZone local role -> denied, zero writer calls
~~~

Existing managed transaction tests must explicitly activate the role/fence
before calling these methods.

---

# 9. Flush nil regression must go through Manager.Handle

Current code reads `request.LeshyRoute.Zone` before checking whether
`request.LeshyRoute` is nil.

Add a real control-handler test with `LeshyRoute: nil` and require:

~~~text
no panic
OK == false
stable APIError
a subsequent Snapshot request still succeeds
~~~

---

# 10. Enable/Disable precondition matrix is incomplete

Add:

~~~text
Enable while already Managed -> denied before journal write
Enable with stale selected checkpoint -> denied
Disable with Active selected registry entry -> denied
Disable with Pending/Applying/Retiring entry -> denied
Disable with selected inFlight > 0 -> denied
Disable with non-empty current-activation checkpoint -> denied
Disable with empty matching checkpoint -> succeeds and removes it
Disable with wrong-activation empty checkpoint -> fail closed
~~~

Every denial proves config/marker/generation unchanged.

---

# 11. Target verification needs one-field-at-a-time corruption tests

For a committed Managed target mutate exactly one marker field:

~~~text
Schema
Socket
ManagedZones
ActivationID
ModeGeneration
~~~

Each mismatch must make recovery fail and retain the journal.

For Local target stage:

~~~text
surviving marker
non-empty selected checkpoint
wrong-activation empty checkpoint
~~~

Each must fail closed.

---

# 12. D10 must be a real process-crash matrix

Current test-build failpoint mechanism is appropriate, but only these
boundaries are wired:

~~~text
after_prepared
after_config_written
after_marker_written
after_apply
after_generation_commit
~~~

Add the missing raw boundaries specified in the main audit packet.

Then prove four levels:

~~~text
1. unit durable-state assertions
2. subprocess exit/restart on host
3. ubuntu2604 real filesystem/writer-lock/service process matrix
4. win10 ProgramData/LockFileEx/SCM process matrix
~~~

A returned injected error is not equivalent to a process crash because normal
deferred cleanup and in-memory state survive.

---

# 13. Existing integration inventory is useful but Phase F/G/H is not done

Existing Linux assets include:

~~~text
linux/tests/toad/managed-leshy-smoke.sh
linux/tests/toad/selected-routes-netns.sh
linux/tests/toad/service-cutover.sh
linux/tests/toad/orchestration-cutover.sh
~~~

Reuse assertions from them where applicable.

At this audit point the repository still has no:

~~~text
toad/internal/testtoad/cmd/ready-toad
process recovery barrier
TestTriggerManagedRecovery
packaging/windows/test_managed_leshy.ps1
~~~

Therefore the deterministic managed-success fixture and the full Linux/Windows
verticals remain real work.

---

# 14. Exact executor start point

The first implementation slice remains **D7R-1**, not D8.

Start at:

~~~text
toad/internal/control/control.go
    newManagerWithDeps
    CommittedManagedMode
    RestoreCommittedManagedState

toad/internal/control/mode_transaction.go
    RecoverManagedModeTransition

toad/cmd/kikimora-core/main.go
    runServe
~~~

First acceptance target:

~~~text
remove pre-recovery pendingManagedMarker from production deferred restore
fresh-load marker/generation only after journal recovery
make config regeneration/read-back verification fatal
add all four fresh-manager recovery regression cases
~~~

Only after that commit is green proceed to checkpoint fencing D7R-2.
