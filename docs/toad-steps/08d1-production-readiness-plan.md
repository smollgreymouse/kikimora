# 08D1 autonomous production-readiness execution plan

Status: **CORE PRODUCTION/VM/RELEASE PACKET — ordered by docs/roadmap.md**

Master cross-core/UI execution order:

~~~text
docs/roadmap.md
~~~

This file remains the detailed core G/H/I/J/K/L behavior authority, but the
master roadmap inserts the UI/UI↔core implementation stages before G/H so
core runtime-path testability changes are included in the VM-accepted head.

Goal: take Kikimora + Leshy from the current rescued development state to a
release candidate that is safe to install and use on the real Linux workstation,
prove it on both disposable VMs with real privileged networking, run the real-host
canary, then publish the release.

The executor is authorized to work autonomously through every phase in this
file. Do not stop after a stage merely to ask what to do next. Continue while
the current phase exit gates are green. If a gate fails, fix the implementation
or harness within the architecture specified here, rerun the gate, and continue.
Do not weaken the gate.

Parent design packet:

~~~text
docs/toad-steps/08d1-managed-selected-routes-completion.md
~~~

Historical rescue packet:

~~~text
docs/toad-steps/08d1-post-l11-rescue-plan.md
~~~

Execution journal:

~~~text
docs/toad-steps/08d1-execution-log.md
~~~

Post-739e1ab audit override (mandatory before Phase G):

~~~text
docs/toad-steps/08d1-post-739e1ab-audit-remediation.md
~~~

The 2026-11-11 audit of Kikimora 739e1ab reopens the Windows Phase E upgrade
path and rejects the claimed Phase F completion. The remediation packet above
is authoritative where this older plan describes Phase E/F. Do not continue
to Phase G until R1-R8 in that packet are green and the execution log records
Windows Phase E re-acceptance plus Phase F acceptance.

Current audited heads at plan creation:

~~~text
Kikimora:
    fb40a60edbfb9f449961d1b7ddead9d60b845e03

Leshy:
    0da1b9ffe51f891b41b0c2bc843b6d9250824a49

Kikimora companion pin:
    LESHY_REF=0da1b9ffe51f891b41b0c2bc843b6d9250824a49
~~~

Current planned release versions:

~~~text
Leshy:    0.6.0
Kikimora: 1.4.0
~~~

Do not create those release tags until the exact phase below authorizes it.

---

# 1. Executor operating rules

These rules are mandatory through the entire packet.

## 1.1 The executor implements; this file decides architecture

Do not ask for architecture choices already fixed here.

When implementation details differ from assumptions in this file:

1. preserve the stated safety invariant;
2. choose the fail-closed implementation;
3. update code/tests to the nearest exact equivalent;
4. record the deviation and why it is equivalent in the execution log.

Do not silently change the architecture.

## 1.2 Never make a gate green by deleting the proof

Forbidden:

~~~text
delete/comment a failing required test
add ignore to avoid a required test
weaken an assertion
lengthen an event-repair timeout so periodic audit can satisfy it
remove a CI job
use continue-on-error
narrow workflow paths to avoid a failing job
replace a production-path test with a helper-only test
declare VM evidence a replacement for a required GitHub gate
declare cross-build a replacement for a required native runtime gate
~~~

A test can be replaced only by an equal-or-stronger test that lands in the same
commit and proves the same named invariant.

## 1.3 Exact SHA evidence only

Every accepted phase records:

~~~text
40-char Kikimora SHA when Kikimora changed
40-char Leshy SHA when Leshy changed
GitHub run IDs
VM script/run evidence
package hashes when artifacts exist
~~~

Commit titles and prose claims are not evidence.

## 1.4 Separate implementation commits

Do not make one giant rescue/release commit.

Use the commit slices prescribed by this packet or smaller slices.

## 1.5 Foreign networking state is immutable

No test, installer, recovery path, cleanup path or canary may broadly delete:

~~~text
same-prefix routes
default routes
management routes
rows not proven by exact owned identity
unrelated firewall/rule state
unrelated adapters
~~~

Every privileged test records before/after state and fails on unrelated drift.

## 1.6 Real workstation is last

Do not install the candidate on the real workstation until:

~~~text
all local/unit CI green
Ubuntu VM vertical green
Windows VM vertical green
fresh package install green on both VMs
upgrade package install green on both VMs
~~~

The real workstation canary uses an independent timed rollback service.

---

# 2. Independent audit correction after rescue R1-R11

The rescue report contains useful, genuine evidence. The reported GitHub runs
were independently verified:

~~~text
L11:
    bcb9665709f39ac6c195f6d59f2ed7241a9357da
    run 42673966379 attempt 2
    8/8 jobs green

L12 rescue SHA:
    70c9839326fdd05f0f2fba90478d87d936f37787
    run 42676819410
    8/8 jobs green

L13/L14 rescue SHA:
    0da1b9ffe51f891b41b0c2bc843b6d9250824a49
    run 42677847701
    8/8 jobs green

Kikimora package pair:
    run 42678886758
    Resolve pin + Linux deb + Windows MSI/zip all green
~~~

Those CI facts remain valid.

However the implementation audit found release-blocking gaps not covered by
that rescue. Therefore section 55 L12 acceptance is revoked until Phase A
below passes. L13/L14 code is retained but must be revalidated on the corrected
base.

## 2.1 L12 Windows backend is not the architecture required by the packet

Current production entry point:

~~~text
leshy/src/routing/windows.rs
~~~

Current dead/unwired file:

~~~text
leshy/src/routing/windows_api.rs
~~~

Findings:

1. windows_api.rs claims production uses RealWindowsRouteApi, but routing/mod.rs
   does not declare mod windows_api and WindowsRouteBackend does not use it.
   It is dead source and is not a production seam.

2. The production Windows backend has no coherent process-wide route cache.
   ensure_route calls find_route_rows, and read_prefix calls GetIpForwardTable2.
   A converged request can therefore enumerate the forwarding table instead of
   using a cache proven by NotifyRouteChange2 generation.

3. build_route_row constructs MIB_IPFORWARD_ROW2 from Default rather than
   InitializeIpForwardEntry/Init before CreateIpForwardEntry2.

4. InterfaceLuid is recorded in ObservedRoute but is not part of the desired
   exact-match proof. observed_matches checks target/protocol/metric/scope but
   does not compare the current interface LUID to the desired interface LUID.
   Reused ifIndex with a different LUID can be misclassified.

5. If a Desired row coexists with another same-prefix foreign row,
   ensure_route finds the Desired row and returns success before rejecting the
   foreign co-resident row.

6. Replacement currently deletes the old exact Owned row before creating and
   proving Desired. No delete-first fallback is allowed.

7. The 9-case Windows native rescue gate omitted explicit wrong-protocol,
   wrong-interface/LUID and deterministic Create/EEXIST exact-vs-foreign cases.

## 2.2 Kikimora startup ordering is still not Stage-6 safe

Current entry point:

~~~text
toad/cmd/kikimora-core/main.go
    runServe
~~~

Current order still calls manager.RegenerateLeshyConfig immediately after
NewManagerWithConfigDir, before transition recovery, committed generation,
schema2 marker recovery, checkpoint restore and route-authority proof.

mode_transition.go contains durable types and planTransitionRecovery, but
production runServe does not call them.

## 2.3 Kikimora still activates schema1/v1, not final schema2/v2

Current entry points:

~~~text
toad/internal/control/selected_routes.go
toad/internal/control/leshy_routes_api.go
~~~

Current production state:

~~~text
managedModeMarkerSchema = 1
ManagedSelectedRoutesCapability = managed_selected_routes_v1
EnableManagedSelectedRoutes writes schema1
DisableManagedSelectedRoutes is not the Stage-6 durable transaction
~~~

## 2.4 Application readiness remains weaker than the final contract

wait-startup currently succeeds on StartupReady and ModeTransition==none.
Managed mode must additionally require RouteAuthorityReady.

## 2.5 Kikimora shutdown still has an unbounded production call

finalizeShutdown uses ReleaseProduct(context.Background()) and must be bounded.

## 2.6 L15 is not implemented

Current Leshy has no run_server_until, detached retry/watcher tasks, Windows SCM
uses abort on normal stop, exact local routes can persist, and no top-level
leshy capabilities command exists.

## 2.7 Windows installer still violates final service ordering

packaging/windows/install.ps1 currently installs/starts Leshy before
KikimoraCore and creates Leshy directly without the required KikimoraCore
dependency.

---

# 3. Fixed final architecture

## 3.1 Writers

~~~text
Standalone/local Leshy:
    Leshy is the selected-route FIB writer.

Managed Leshy:
    Kikimora is the only selected-route/Park FIB writer.
    Leshy only sends route intents over AF_UNIX.
    No local fallback exists.
~~~

## 3.2 Managed mode state

Final release authority is schema2 leshy-managed.json plus
leshy-mode-generation.json, an optional in-flight leshy-mode-transition.json,
per-role selected-routes.json, and exact kernel identities.

Schema1 is upgrade input only. Final runtime never activates schema1.

## 3.3 Windows identity

Exact Windows selected identity is prefix/table, InterfaceIndex, InterfaceLuid,
NextHop, Protocol NetMgmt/numeric 3, RouteMetric and model scope 0.

Same ifIndex with another LUID is foreign.

## 3.4 Readiness

Managed core startup is ready only after core singleton, transition recovery,
generation/marker/checkpoint recovery, platform writer, native observer and
initial stable audit all succeed.

Only then may packaged Leshy start.

## 3.5 Cutover

Local -> managed:

~~~text
capability/auth preflight
stop Leshy gracefully
prove writer lock available
EnableManagedLeshy durable transaction
start Leshy
verify exact ActivationID/ModeGeneration
~~~

Managed -> local:

~~~text
DisconnectAll
prove selected registry empty and Park nil
stop Leshy
prove writer lock available
DisableManagedLeshy durable transaction
start Leshy local
~~~

There is never writer overlap.

---

# 4. Phase A — final L12 Windows local-backend correction

This phase is mandatory before L15.

Repository: Leshy.

## A1. Make WindowsRouteApi real and production-injected

Files:

~~~text
src/routing/mod.rs
src/routing/windows.rs
src/routing/windows_api.rs
~~~

routing/mod.rs must declare windows_api on Windows.

Rewrite windows_api.rs; do not keep the current dead implementation.

Required production seam:

~~~rust
pub struct WindowsInterfaceIdentity {
    pub index: u32,
    pub luid: u64,
}

#[async_trait]
pub trait WindowsRouteApi: Send + Sync {
    async fn resolve_device(&self, name: &str)
        -> Result<WindowsInterfaceIdentity>;

    async fn resolve_gateway_interface(&self, gateway: IpAddr)
        -> Result<WindowsInterfaceIdentity>;

    async fn dump_family(&self, family: AddressFamily)
        -> Result<Vec<ObservedRoute>>;

    async fn create(&self, desired: &ResolvedWindowsRoute)
        -> Result<WindowsCreateOutcome>;

    async fn delete_exact(
        &self,
        key: &RouteKey,
        identity: &RouteKernelIdentity,
    ) -> Result<WindowsDeleteOutcome>;

    fn subscribe(
        &self,
        generation: Arc<AtomicU64>,
        tx: mpsc::UnboundedSender<RouteInvalidation>,
    ) -> Result<Box<dyn WindowsRouteSubscription>>;
}
~~~

Exact enum names may differ, but semantics may not.

WindowsRouteBackend owns Arc<dyn WindowsRouteApi>, one coherent cache and one
observer subscription.

Tests construct it with FakeWindowsRouteApi.

Production constructs it with RealWindowsRouteApi.

Delete old direct/package-global test seams only after equivalent injected-seam
coverage exists.

## A2. Desired identity must include LUID

Do not add LUID to the cross-platform RouteIntent.

In windows.rs introduce backend-private resolved state:

~~~rust
struct ResolvedWindowsRoute {
    intent: RouteIntent,
    identity: RouteKernelIdentity,
}
~~~

For Device target:

~~~text
resolve current alias/index
ConvertInterfaceIndexToLuid
Desired identity contains index + LUID
~~~

For Gateway target:

~~~text
resolve best interface
ConvertInterfaceIndexToLuid
Desired identity contains index + LUID + gateway
~~~

Recompute desired identity whenever a Dirty key is revalidated.

Exact match requires every Windows identity field.

## A3. Initialize MIB rows correctly

RealWindowsRouteApi::create must:

~~~text
zero row
InitializeIpForwardEntry
set InterfaceLuid
set InterfaceIndex
set DestinationPrefix
set NextHop
set Metric
set Protocol=MIB_IPPROTO_NETMGMT
set infinite lifetimes
CreateIpForwardEntry2
~~~

Do not create from a plain Default-only row.

Read back the actual kernel row after creation.

## A4. Coherent Windows cache

Add one process-wide cache per WindowsRouteBackend:

~~~rust
struct WindowsRouteCache {
    rows: RwLock<HashMap<RouteKey, Vec<ObservedRoute>>>,
    notify_generation: AtomicU64,
    cache_generation: AtomicU64,
    ready: AtomicBool,
    refresh_lock: tokio::sync::Mutex<()>,
    full_refreshes: AtomicU64,
    cache_hits: AtomicU64,
}
~~~

Observer registration occurs before initial snapshot.

Observer callback order:

~~~text
generation = notify_generation.Add(1)
publish targeted invalidation
~~~

No cache lock before generation increments.

Stable refresh:

~~~text
lock refresh_lock
before = notify_generation
dump IPv4 + IPv6
after = notify_generation
if before != after: retry
publish cache map
verify notify_generation still equals after
cache_generation = after
ready = true
~~~

Normal read:

~~~text
if ready && cache_generation == notify_generation:
    read key from cache
    zero GetIpForwardTable2
else:
    one stable full refresh
    read key from cache
~~~

An external route event may cause one full refresh because IP Helper has no
Linux-style filtered route dump. A converged DNS hit must not.

Expose counters to tests.

## A5. Exact classifier

For one key, classify all rows; never return on the first Desired:

~~~text
0 rows:
    Add

exactly 1 exact Desired:
    AlreadyMatching

Desired + any other same-key row:
    Conflict

duplicate Desired rows:
    Conflict

one row == exact previous Owned, Desired absent:
    Replace candidate

anything else:
    Conflict
~~~

Foreign co-resident row always conflicts even when Desired exists.

## A6. Safe replacement

Replace candidate order:

~~~text
1 create Desired while previous exact Owned still exists
2 stable cache refresh
3 prove Desired exists and no foreign row exists
4 delete exact previous Owned by full recorded identity
5 stable cache refresh
6 prove exactly one Desired remains
7 return owned=true with exact Desired observation
~~~

If Create returns EEXIST:

~~~text
stable refresh
exact Desired only -> success
anything else -> Conflict
zero delete
~~~

Do not delete old Owned first.

If Windows refuses coexistence for a particular tuple, fail closed with
Conflict. Do not add a delete-first fallback.

## A7. Exact delete

Delete uses RouteKey plus complete RouteKernelIdentity.

Missing exact row is safe no-op.

Different live identity is foreign and remains unchanged.

## A8. Unit/API-seam matrix

Mandatory:

~~~text
observer-before-snapshot
event during first snapshot -> retry
1000 converged ensures -> zero full refresh after initial convergence
same ifIndex + different LUID -> conflict
wrong protocol -> conflict
wrong metric -> conflict
wrong next hop -> conflict
wrong interface -> conflict
Desired + foreign -> conflict
duplicate Desired -> conflict
EEXIST exact Desired -> success, zero delete
EEXIST foreign -> conflict, zero delete
replace call order: create/prove/delete
create succeeds then proof fails -> old Owned remains
delete exact previous only after Desired proof
~~~

## A9. Extended Windows VM gate

Extend:

~~~text
tests/windows_route_registry.rs
windows/tests/route-registry.ps1
~~~

Keep all existing cases and add:

~~~text
wrong protocol row foreign
wrong interface row foreign
same-ifIndex/different-LUID seam
Desired + co-resident foreign conflicts
1000 duplicate ensures keep full-refresh counter flat
observer event causes one refresh then fast path resumes
~~~

Deterministic EEXIST exact/foreign must be covered through the injected API
seam. Native pre-existing exact/foreign rows are the runtime analogue.

## A10. Phase A exit

Required:

~~~text
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
cargo build --release
cargo check --target x86_64-unknown-linux-musl --all-targets --all-features
GitHub Leshy all 8 jobs green
Windows VM extended route-registry gate green
~~~

Append exact evidence. Only then mark L12 FINAL ACCEPTED.

Suggested commits:

~~~text
refactor(windows): wire production route API seam and coherent cache
fix(windows): make LUID exact and replacement desired-first
test(windows): close final L12 native ownership matrix
~~~

---

# 5. Phase B — Kikimora startup-order and readiness correction

Repository: Kikimora.

Do this before K17 activation.

## B1. Deferred managed restore in production

Current NewManagerWithConfigDir restores marker/checkpoints too early.

Add explicit construction policy, equivalent to:

~~~go
type ManagedRestorePolicy int

const (
    RestoreManagedImmediately ManagedRestorePolicy = iota
    DeferManagedRestore
)
~~~

Tests may use immediate restore.

Production runServe uses deferred restore.

Add one explicit method that restores committed managed state only after mode
transition recovery.

## B2. Correct runServe order

File:

~~~text
toad/cmd/kikimora-core/main.go
~~~

Final order:

~~~text
1 parse serve options
2 acquire StateDir/core.lock
3 construct manager with deferred managed restore
4 recover transition journal under Leshy writer lock
5 read committed mode generation
6 load/validate committed marker
7 ensure privileged auth under local-vs-managed rules
8 apply marker + restore schema2 checkpoints
9 regenerate/verify Leshy config for recovered committed mode
10 construct platform writer + recovery driver
11 start selected-route reconciler
12 wait for initial observer + stable audit proof
13 restore desired state
14 start ordinary recovery/background work
15 bind/protect control socket
16 StartupReady=true
17 serve
~~~

No generic RegenerateLeshyConfig before step 9.

## B3. Synchronous route-authority startup proof

Refactor StartSelectedRouteReconciler so runServe can wait for its initial
proof. Use one observer session only.

An equivalent API to this is required:

~~~go
ready := manager.StartSelectedRouteReconciler(ctx)
if err := <-ready; err != nil {
    return err
}
~~~

## B4. Snapshot readiness fields

Add:

~~~text
managed_mode
mode_generation
activation_id_fingerprint
mode_transition
route_authority_ready
route_authority_error
~~~

Never expose the auth token or full ActivationID.

## B5. wait-startup

Local mode requires:

~~~text
StartupReady
ModeTransition == none
~~~

Managed additionally requires:

~~~text
RouteAuthorityReady == true
~~~

## B6. Bounded core shutdown

Replace ReleaseProduct(context.Background()) with a 10-second overall graceful
release context.

Timeout is a non-clean shutdown, not silent success.

## B7. Tests

Mandatory:

~~~text
transition recovery before config regeneration
marker/checkpoint restore after transition recovery
no early Regenerate call
managed wait-startup refuses routeAuthorityReady=false
local wait-startup does not require managed authority
observer proof failure prevents startup ready
shutdown timeout bounded
second core same StateDir fails before socket/routes
~~~

Phase exit:

~~~text
gofmt
go vet ./...
go test ./...
go test -race ./...
Kikimora Toad jobs green
~~~

Suggested commit:

~~~text
fix(control): restore committed routing mode before startup readiness
~~~

---

# 6. Phase C — L15 graceful lifecycle and capabilities

Repository: Leshy.

Do not bump Cargo version yet.

## C1. run_server_until

File:

~~~text
src/server.rs
~~~

Introduce:

~~~rust
pub async fn run_server_until<F>(
    config_arg: Option<PathBuf>,
    shutdown: F,
) -> anyhow::Result<()>
where
    F: Future<Output = ()>;
~~~

run_server becomes the console wrapper.

Track every background task; no detached work may outlive server shutdown:

~~~text
static-route retry
config watcher
reload consumer
kernel invalidation watcher if server-owned
~~~

Use one cancellation signal.

Final control flow:

~~~text
DNS server exits
OR
shutdown future resolves

then:
    signal cancellation
    await background tasks with bounds
    DnsHandler::shutdown_routes
    return
~~~

## C2. Shutdown route semantics

Add:

~~~text
DnsHandler::shutdown_routes
RouteManager::shutdown_routes
~~~

By reconcile mode:

~~~text
KernelSnapshot:
    remove_all_owned exact rows

RegistryOnly:
    remove_all_owned explicit rows created by this process

AuthorityOnDemand:
    zero Forget
    zero FlushZone
    zero local FIB mutation
~~~

Cleanup timeout is 10 seconds.

A deletion error is returned; ownership evidence is preserved first.

## C3. Linux SIGTERM

Linux shutdown future handles SIGTERM and Ctrl-C.

Privileged acceptance:

~~~text
start local Leshy
install exact owned test row
install same-prefix foreign fixture
SIGTERM
wait clean exit
owned exact row gone
foreign fixture unchanged
~~~

## C4. Windows SCM graceful stop

File:

~~~text
src/service/windows.rs
~~~

Replace normal server_task.abort.

SCM Stop:

~~~text
report StopPending
signal shutdown watch channel
wait <=15s for run_server_until
success -> Stopped
timeout -> emergency abort + non-clean failure
~~~

Also add application readiness:

~~~text
SCM stays StartPending until config load, writer lease, initial static-route
work and DNS bind have completed.
If server exits before readiness, never report Running.
~~~

Test an extracted coordinator without a real SCM.

## C5. Capabilities command

Add top-level:

~~~text
leshy capabilities --json
~~~

During development before final v1 removal output a stable sorted support list:

~~~text
af_unix_control
managed_selected_routes_v1
managed_selected_routes_v2
privileged_control_auth_v1
~~~

The command performs no config load, socket connect, DNS bind or route mutation.

At the v2-only release phase remove v1 from this output.

Tests:

~~~text
clap parser
exact JSON snapshot
nonexistent config path still succeeds
zero backend calls
Windows build output
~~~

## C6. L15 exit

Required:

~~~text
all Leshy local gates
Linux privileged SIGTERM cleanup
Windows service coordinator tests
all 8 GitHub jobs green
Windows VM service-stop smoke
~~~

Record exact L15 SHA. Do not tag yet.

Suggested commits:

~~~text
feat(service): add graceful server lifecycle and exact shutdown cleanup
feat(cli): expose deterministic managed-routing capabilities
~~~

---

# 7. Phase D — K17 schema2 transactional activation

Repository: Kikimora.

## D1. Final schema2 marker

Replace active schema1 marker with:

~~~go
type managedModeMarker struct {
    Schema         int
    Socket         string
    CoreVersion    string
    ManagedZones   []string
    ActivationID   string
    ModeGeneration uint64
}
~~~

Final active schema = 2.

Schema1 may be parsed only by upgrade detection. Final runtime never applies
schema1 as managed authority.

## D2. Generated Leshy config

Files:

~~~text
toad/internal/config/leshy_config.go
toad/internal/control/config_api.go
~~~

Extend LeshyServer + renderer with:

~~~text
kikimora_auth_file
kikimora_activation_id
kikimora_mode_generation
~~~

Managed config contains:

~~~text
routing_backend = kikimora
kikimora_socket
writer_lock
kikimora_auth_file
kikimora_activation_id
kikimora_mode_generation
~~~

Local config contains no managed-only fields.

Refactor config generation into deterministic render + durable write so the
transaction hashes the exact bytes including final newline.

## D3. Privileged API

Add:

~~~text
GetManagedLeshyMode
EnableManagedLeshy
DisableManagedLeshy
~~~

Enable/Disable require privileged auth.

Enable request carries capabilities observed from installed Leshy.

Core requires:

~~~text
af_unix_control
managed_selected_routes_v2
privileged_control_auth_v1
~~~

v1-only activation is rejected.

## D4. CLI

Add:

~~~text
kikimora-core leshy-mode status --json
kikimora-core leshy-mode enable --leshy <path> [--socket ...] [--auth-file ...]
kikimora-core leshy-mode disable [--socket ...] [--auth-file ...]
~~~

Enable CLI runs Leshy capabilities itself, validates requirements, reads auth
from the file and calls the privileged API.

Never accept the token as a command-line literal.

The API transaction still acquires writer lock, so a caller that forgot to stop
Leshy receives a safe busy error.

## D5. Enable transaction

Under the shared Leshy writer lock:

~~~text
assert no journal
read FromGeneration
ToGeneration = FromGeneration + 1
fresh ToActivationID
sort/unique zones
render exact managed config bytes
SHA256 exact bytes
write journal prepared durably
write managed config durably
read back and verify digest
journal config_written
write schema2 marker durably
journal marker_written
apply managed mode in memory
write committed generation ToGeneration durably
remove journal durably
release writer lock
~~~

Invoke existing stable managed failpoint boundaries.

## D6. Disable transaction

Preconditions:

~~~text
roles disconnected/stopped
selected registry empty
Park nil everywhere
no managed recovery in flight
~~~

Then under writer lock:

~~~text
journal prepared
write local config durably
journal config_written
remove schema2 marker durably
journal marker_written
apply local mode in memory
commit ToGeneration
remove journal
release lock
~~~

## D7. Startup transition recovery

Files:

~~~text
toad/internal/control/mode_transition.go
toad/cmd/kikimora-core/main.go
~~~

If journal exists, acquire writer lock and validate all fields/digest.

Rules:

~~~text
committed == FromGeneration:
    roll forward deterministically to To

committed == ToGeneration:
    verify target state
    remove leftover journal

anything else:
    fail startup closed
~~~

Never infer recovery from timestamps.

Never guess rollback.

## D8. Generation/activation fence

Every v2 Ensure/Forget/Flush request carries exact ActivationID +
ModeGeneration.

Core rejects stale values before route mutation.

Handshake advertises v2 privileged support only when persistent-state/auth
support exists.

## D9. Diagnostics

Expose:

~~~text
mode
committed generation
activation fingerprint only
transition phase/id fingerprint
route authority state/error
~~~

Redact full auth and full ActivationID.

## D10. Crash/failpoint matrix

For both Enable and Disable crash at:

~~~text
before journal
after prepared
after config write
after config-written journal
after marker write/remove
after marker-written journal
after generation commit
before journal delete
~~~

Also prove:

~~~text
writer lock busy -> zero mutation
missing/malformed managed auth -> startup fail
stale ActivationID -> denied zero mutation
stale ModeGeneration -> denied zero mutation
corrupt journal -> fail closed
digest mismatch -> fail closed
second core -> fail before mutation
~~~

Phase exit:

~~~text
Kikimora go/race CI green
transition crash matrix green
Leshy capability preflight works Linux + Windows
~~~

Suggested commits:

~~~text
feat(control): activate managed Leshy through schema2 mode transactions
feat(cli): add privileged Leshy mode control
test(control): prove mode-journal crash recovery
~~~

---

# 8. Phase E — package service lifecycle and one-writer cutover

Repositories: both.

## E1. Linux service lifecycle

Keep current packaged Leshy hard dependency:

~~~text
Requires=kikimora-core.service
After=kikimora-core.service
ExecStartPre=kikimora-core wait-startup ...
~~~

Add package tests that assert those exact contracts.

Fresh install/cutover:

~~~text
install binaries/config in local mode
start core
start Leshy local
preflight Leshy capabilities
stop Leshy gracefully
wait stopped
prove writer lock available
kikimora-core leshy-mode enable ...
start Leshy
wait active
verify managed generation/fingerprint
verify generated Leshy config matches generation
~~~

Any post-stop failure:

~~~text
stop Leshy if needed
disable if managed transaction committed
start local Leshy
verify local mode
return installer failure
~~~

No fallback inside Leshy.

## E2. Windows service order

File:

~~~text
packaging/windows/install.ps1
~~~

Final order:

~~~text
copy payload
create/harden ProgramData
install KikimoraCore first
verify KikimoraCore startup-ready
install Leshy second
make Leshy depend on KikimoraCore
start Leshy local
perform managed cutover
~~~

Use the Leshy service installer dependency contract rather than bare sc.exe:

~~~text
leshy.exe service install
--config C:\ProgramData\leshy\config.toml
--name leshy
--depend-on KikimoraCore
~~~

Assert dependency after install.

## E3. Windows persistent paths

Core service uses:

~~~text
--state-dir C:\ProgramData\Kikimora\state\core
--config-dir C:\ProgramData\Kikimora\toads
--leshy-publication-dir C:\ProgramData\Kikimora\leshy\vpn
~~~

Auth key:

~~~text
C:\ProgramData\Kikimora\state\core\control-route-auth.key
~~~

ACL: SYSTEM + Administrators only.

Upgrade requirement added by the 739e1ab audit: before the new service reads
this root, copy-and-verify the legacy core-owned files from
`C:\ProgramData\Kikimora\state` into `state\core` exactly as specified
by R1 of `08d1-post-739e1ab-audit-remediation.md`. The parent writer lock and
per-Toad state are not moved.

## E4. Upgrade choreography

Previous local release:

~~~text
stop Leshy gracefully
upgrade payload
start core local
start Leshy local
fresh schema2 enable
~~~

Existing schema2 managed:

~~~text
stop Leshy
stop core
backup state/config
install payload
migrate legacy core state -> state\core with the R1 copy/verify transaction
start core first
recover marker/checkpoint using the migrated generation/auth/journal
start Leshy
verify same committed generation/activation
~~~

The migration is mandatory for upgrades from releases whose core StateDir was
the parent `state` directory. A fresh-install gate does not satisfy this row.

Legacy schema1:

~~~text
stop Leshy
DisconnectAll on old core
wait all roles stopped
wait selected ownership empty and no Park
capture kernel rows
stop old core
backup
prove no live old ownership
remove schema1 marker only after proof
install candidate
start local
fresh schema2 enable
~~~

If emptiness cannot be proven, restore old package/state and abort upgrade.

## E5. Zero-overlap tests

On both OSes prove:

~~~text
Enable fails while local Leshy writer lock held
after graceful stop lock becomes available
mode transaction holds the same lock while files mutate
managed Leshy starts only after transaction
Disable follows the same reverse discipline
~~~

No local/managed writer overlap is permitted.

---

# 9. Phase F — deterministic integration infrastructure

> **2026-11-11 correction:** the implementation at 739e1ab is not an accepted
> Phase F. Execute
> `docs/toad-steps/08d1-post-739e1ab-audit-remediation.md` R1-R8 first.
> The detailed requirements below remain the target contract, but the newer
> remediation packet fixes the exact code entry points and acceptance order.

Repository: Kikimora, against the exact accepted Leshy companion SHA.

Implement/reuse one Ready-Toad fixture only.

Required binary:

~~~text
toad/internal/testtoad/cmd/ready-toad
~~~

Do not create shell/PowerShell fake control protocols.

Ready-Toad contract:

~~~text
ready-toad run -config <role.toml>
resolve real interface name to real ifindex
sample real addresses every 100ms
independent IPv4/IPv6 readiness
Start/Validate/Rebind/RestartTransportKeepingTUN
normal Subscribe/revision behavior
no VPN transport
no route mutation
~~~

Build test-only process failpoints with:

~~~text
kikimora_test_failpoints
~~~

Required test-only seams:

~~~text
selected transaction failpoints
park transaction failpoints
managed mode transition failpoints
recovery barrier
TestTriggerManagedRecovery
~~~

Release binary must:

~~~text
not advertise TestTriggerManagedRecovery
not wait on recovery barrier
not honor failpoint env vars
~~~

Shared DNS fixtures:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
~~~

Diagnostics bundles redact auth token/full ActivationID.

Phase F exit:

~~~text
fixture unit tests
test-build failpoint tests
release-build absence tests
Kikimora all-OS CI green
~~~

Suggested commit:

~~~text
test(toad): add deterministic managed-routing recovery fixtures
~~~

---

# 10. Phase G — Ubuntu VM full vertical acceptance

Use the disposable Ubuntu VM with privileged networking and real Internet
access.

Current lab identity from prior evidence:

~~~text
VM name: ubuntu2604
last known address: 192.168.3.74
~~~

The address is observational only. Resolve/check it again before every native
run.

Use clean source worktrees at exact SHAs.

> **b751134 correction:** a G0/G1 investigation was started from 739e1ab
> before Phase F was accepted. It is exploratory evidence only. Toad core CI
> for 739e1ab is red (run 43016857823), so Phase G remains blocked until R1-R8
> of `08d1-post-739e1ab-audit-remediation.md` are complete and Phase F is
> explicitly re-accepted.

Do not reuse the test binaries copied during that investigation. Build
acceptance binaries under a SHA-scoped `/tmp/kikimora-acceptance/<sha>/`
directory and pass absolute paths to every harness.

## G1. Local Leshy gate

Implement/run only the versioned standalone-local harness:

~~~text
linux/tests/toad/local-leshy-routing-netns.sh
~~~

The exact topology, process lifecycle and eight required rows are fixed by
section 13.3 of `08d1-post-739e1ab-audit-remediation.md`.

Important mechanism correction:

~~~text
publication metadata is not a route-add input
<zone>.dev is the route-target input containing the fixture interface name
the first A/AAAA DNS queries trigger dynamic route creation
shared dns-stub.py/dnsq.py provide deterministic answers
~~~

Canonical fixture answers:

~~~text
A    -> 203.0.113.10
AAAA -> 2001:db8:dead::10
~~~

Prove:

~~~text
A/AAAA exact add/read-back through DNS-triggered routing
1000 A + 1000 AAAA zero mutation churn
external /32 delete repair <5s without another DNS query
external /128 delete repair <5s without another DNS query
foreign wrong OIF/metric identity unchanged
exact previous-owned replacement
SIGTERM exact cleanup
pre-existing compatible foreign survives shutdown
~~~

G1.2 is mandatory here and may not be deferred to G2.

Archive route snapshots, route-event monitor output and counters.

## G2. Managed success E2E

Implement/run:

~~~text
linux/tests/toad/managed-leshy-routing-netns.sh
~~~

Use real:

~~~text
kikimora-core
leshy
ready-toad
AF_UNIX
schema2 auth
~~~

Run every Linux MUST row from the parent regression matrix.

Mandatory special cases:

~~~text
A and AAAA independent readiness
1000 duplicate queries zero selected mutations
external v4/v6 repair <5s
foreign same-prefix fail closed and unchanged
Park -> WithdrawSelected -> Quiesce ordering
recovery ifindex replacement through barrier
AuthorityLost: Leshy alive, zero local fallback
core restart: checkpoint ownership restored before normal ACK
Forget/Flush exact retirement
managed -> local rollback zero overlap
~~~

## G3. Mode transition crash matrix

Use process failpoints to kill core at every Enable/Disable journal boundary.

For every case restart core and verify:

~~~text
correct From/To committed decision
exact config digest
marker/generation consistency
journal eventually absent
writer lock semantics
~~~

## G4. Reboot matrix

Reboot the VM at representative durable states:

~~~text
prepared
config_written
marker_written
post-generation/pre-journal-delete
managed Active checkpoint
ParkApplying
ParkRetiring
~~~

Core recovery must finish before Leshy starts.

## G5. Package fresh install

Build candidate Debian package from the exact candidate source.

On clean VM state:

~~~text
install package
verify core service
verify Leshy dependency/readiness
verify managed cutover
create/use Ready-Toad test profile
run A/AAAA managed routing
reboot VM
repeat smoke
uninstall
verify owned resources removed
~~~

## G6. Package upgrade

Install previous released Kikimora package first.

Exercise:

~~~text
previous local release -> candidate
synthetic empty schema1 fixture -> candidate
schema2 candidate reinstall/upgrade
~~~

Abort/rollback fixture:

~~~text
inject non-empty/ambiguous schema1 ownership
upgrade must abort and restore old install
~~~

Phase G exit:

~~~text
all Linux MUST rows green
fresh install green
upgrade green
reboot/crash green
unrelated route/rule state unchanged
~~~

Archive logs in the release-evidence directory.

---

# 11. Phase H — Windows VM full vertical acceptance

Use the disposable Windows VM with Administrator access and real networking.

Current lab identity:

~~~text
VM name: win10
last known address: 192.168.3.69
~~~

Resolve/check current address before use.

Before every case capture:

~~~text
Get-NetRoute
Get-NetIPInterface
Get-NetAdapter
service configuration
test-adapter ifindices/LUIDs
~~~

## H1. Final L12 native gate

Run the extended Phase-A route-registry gate.

## H2. Managed successful E2E

Implement/run:

~~~text
packaging/windows/test_managed_leshy.ps1
~~~

Use real:

~~~text
kikimora-core.exe
leshy.exe
ready-toad.exe
Windows AF_UNIX
schema2 auth
~~~

Mandatory:

~~~text
A/AAAA exact selected rows
1000 duplicate no churn
external v4/v6 notification repair <5s
foreign wrong identity unchanged
stale ActivationID denied
stale ModeGeneration denied
Park exact loopback identity
effective selected metric < Park metric before unpark
high selected interface metric keeps Park/frozen
ifindex/LUID replacement
core kill: no local fallback
core restart checkpoint recovery
DisconnectAll + disable + local restart zero overlap
local Leshy graceful stop exact cleanup
~~~

## H3. Transition crash + reboot matrix

Run the same journal boundaries as Linux, using actual Windows services and VM
reboots.

## H4. SCM dependency/readiness

Assert:

~~~text
Leshy DependsOn KikimoraCore
KikimoraCore remains StartPending until core ready
Leshy cannot start when core recovery fails
normal Leshy Stop is graceful, not abort
~~~

## H5. Auth/ACL

Assert:

~~~text
route auth key readable by SYSTEM/Administrators
ordinary user cannot read it
desktop nonprivileged API still works
privileged route/mode API without token denied
~~~

## H6. Package fresh install and upgrade

Test MSI and portable zip where supported.

Fresh:

~~~text
install
service dependency
managed cutover
routing smoke
reboot
routing smoke
uninstall
~~~

Upgrade:

~~~text
previous local release -> candidate
schema2 candidate reinstall/upgrade
schema1 drain fixture
ambiguous schema1 -> abort/restore
~~~

Compare unrelated route baseline exactly.

Phase H exit:

~~~text
all Windows MUST rows green
fresh install green
upgrade green
reboot/crash green
unrelated route state unchanged
~~~

---

# 12. Phase I — final v2-only compatibility cut

Only after Phases A-H are green.

## I1. Remove v1 activation from Leshy

Files:

~~~text
src/routing/kikimora.rs
src/config.rs
tests
~~~

Remove:

~~~text
managed_selected_routes_v1 capability
legacy requests without ActivationID/ModeGeneration
v1 activation acceptance tests
~~~

Do not remove historical upgrade handling from Kikimora package scripts.

Final managed Leshy requires auth file + ActivationID + ModeGeneration.

## I2. Remove v1 active capability from Kikimora

Files:

~~~text
toad/internal/control/leshy_routes_api.go
toad/internal/control/api.go
~~~

Final Handshake advertises managed_selected_routes_v2 and
privileged_control_auth_v1, plus ordinary control capabilities.

Schema1 marker remains upgrade input only and is rejected as active authority.

## I3. Re-run both managed VM smokes

Because the wire compatibility changed, rerun:

~~~text
Linux managed A/AAAA + restart + rollback
Windows managed A/AAAA + restart + rollback
stale v1 client fails closed
~~~

---

# 13. Phase J — versioning, tag and release-candidate artifacts

## J1. Leshy 0.6.0 candidate

On the exact v2-only green Leshy commit:

~~~text
Cargo.toml version = 0.6.0
Cargo.lock updated
~~~

Run the full Leshy CI again.

Only when that exact SHA has all non-cancelled green jobs:

~~~text
git tag v0.6.0 <exact-sha>
git push origin v0.6.0
verify tag commit == exact-sha
~~~

Do not retag.

## J2. Companion pin provenance

Change .github/managed-routing-companion.env to carry both:

~~~text
LESHY_REF=v0.6.0
LESHY_SHA=<exact 40-char tag commit>
~~~

Build workflow must checkout LESHY_REF, resolve HEAD, assert it equals
LESHY_SHA, and print both.

This intentionally replaces the development-only 40-hex-ref rule.

## J3. Kikimora 1.4.0 candidate

Set:

~~~text
VERSION=1.4.0
linux/install.sh EXPECTED_VERSION=0.6.0
~~~

Remove stale packaging references to Leshy 0.4.0, windows-support and moving
master.

Both Linux and Windows packages consume v0.6.0 and exact LESHY_SHA.

## J4. Candidate package CI

Required:

~~~text
Kikimora go/race/all-OS CI
ShellCheck
Linux package
Windows MSI + zip
desktop package checks
Linux package contract
Windows package contract
embedded leshy --version == leshy 0.6.0
~~~

Store candidate artifacts and SHA256 hashes.

## J5. Re-run package smoke on both VMs

Install exactly those artifacts, not local rebuilds.

Run fresh-install managed smoke and one reboot on each VM.

---

# 14. Phase K — real Linux workstation canary

This phase is required before publishing Kikimora 1.4.0.

Do not run test-failpoint binaries on the real workstation.

## K1. Preflight snapshot

Before mutation capture:

~~~text
date/kernel/OS
ip -details -4 route show table all
ip -details -6 route show table all
ip -details rule
resolvectl status
nmcli device/status
systemctl status kikimora-core leshy
systemctl cat kikimora-core leshy
installed package versions
/etc/kikimora
/var/lib/kikimora
/etc/leshy if present
current Kikimora VERSION
current leshy --version
~~~

Archive to a timestamped directory under:

~~~text
~/kikimora-canary-backup/
~~~

Create a root-preserving tar backup of configuration/state.

Record the current package artifact/binary versions so rollback does not depend
on network access.

## K2. Autonomous rollback script

Before candidate installation create:

~~~text
/root/kikimora-canary-rollback.sh
~~~

It must be idempotent and restore:

~~~text
previous binaries/package
previous systemd units
previous /etc/kikimora
previous /var/lib/kikimora
previous Leshy config
daemon-reload
previous service enable/start state
~~~

It must not touch unrelated network state.

## K3. Dead-man rollback

Before service cutover arm an independent local timer:

~~~text
systemd-run --unit=kikimora-canary-rollback --on-active=5m /root/kikimora-canary-rollback.sh
~~~

Verify the timer exists.

If candidate networking kills CTUN/UI connectivity, rollback must still execute
without the agent.

Cancel the timer only after K4-K7 pass.

## K4. Install exact candidate artifact

Install the same candidate .deb whose SHA256 passed VM testing.

Do not rebuild on the workstation.

Verify:

~~~text
Kikimora 1.4.0 candidate metadata
Leshy 0.6.0
core service active
Leshy service active
writer lock ownership correct
managed schema2 active
routeAuthorityReady true
ModeTransition none
no transition journal
~~~

## K5. Real selected-routing canary

Use one existing configured role that is already valid/known working.

Do not overwrite credentials or reconfigure unrelated roles.

If there is no harmless canary domain already assigned to that role:

~~~text
save the original routing list
temporarily add example.com through the normal Kikimora routing-list source
restore the original list after the test
~~~

Query through Leshy.

For returned A/AAAA addresses assert:

~~~text
selected host route exists
route uses chosen Kikimora role interface
ip route get and ip -6 route get agree
an HTTP/TLS request succeeds
~~~

Check an unrelated direct destination still uses the physical/default path.

## K6. Production recovery canary

Use only ordinary production operations:

~~~text
disconnect/reconnect chosen role
restart kikimora-core normally
restart leshy normally
~~~

Do not inject crashes/failpoints.

After each require:

~~~text
selected routing recovers
foreign/default routes unchanged
routeAuthorityReady returns true
no local/managed writer overlap
DNS is fail-closed during unsafe interval
~~~

## K7. Five-minute stability window

Sample:

~~~text
core status/diagnostics
selected route counters
system journal
Leshy log
route table for selected prefixes
~~~

Require:

~~~text
no repeated mutation churn
no observer restart loop
no audit failure loop
no route-authority flap
no unexpected Wi-Fi/rfkill/default-route mutation
~~~

## K8. Commit canary

Restore any temporary routing-list edit.

Cancel/remove the dead-man rollback unit.

Keep backup/rollback files until release publication.

Record the canary evidence.

If K4-K7 fail:

~~~text
run rollback immediately
verify old services/network restored
keep candidate unpublished
fix source
return to the owning VM phase
~~~

---

# 15. Phase L — final release and publication

Only after the real-host canary is green.

## L1. Final source state

Require:

~~~text
both repos clean
no generated binaries accidentally tracked
no open P0/P1 release TODO
no commented required tests
no newly ignored mandatory gates
all exact release SHAs recorded
~~~

## L2. Kikimora tag

The candidate VERSION already equals 1.4.0.

Tag the exact canary-tested Kikimora SHA:

~~~text
1.4.0
~~~

Verify the tag commit.

Do not change source after the canary and before tagging.

## L3. Final artifacts

Release evidence contains:

~~~text
Kikimora SHA/tag
Leshy SHA/tag
all GitHub run IDs
Ubuntu VM logs
Windows VM logs
transition crash/reboot matrices
fresh-install logs
upgrade logs
real-host canary log
artifact SHA256
embedded Leshy version
activation fingerprint
~~~

If publication rebuilds artifacts and hashes change, rerun package smoke for
the newly produced artifacts before publishing.

## L4. Documentation status

Update:

~~~text
docs/toad-steps/08d-leshy-managed-selected-routes.md
docs/toad-steps/08d1-managed-selected-routes-completion.md
docs/toad-steps/08d1-execution-log.md
~~~

to COMPLETE only now.

Do not delete historical audits/corrections.

## L5. Publish

Create/publish Kikimora release tag 1.4.0 and attach Linux artifacts, Windows
MSI, Windows portable zip if retained, checksums and release notes.

Release notes include:

~~~text
single-writer managed Leshy selected routing
dual-stack A/AAAA
crash-safe Park/selected recovery
Linux + Windows
Leshy v0.6.0
upgrade/cutover semantics
~~~

---

# 16. Required commit slices

The executor may split further but may not merge safety phases into one opaque
commit.

Recommended:

~~~text
L-A1  refactor(windows): wire production route API seam and coherent cache
L-A2  fix(windows): make LUID exact and replacement desired-first
L-A3  test(windows): close final L12 native ownership matrix

K-B   fix(control): recover committed mode before startup readiness

L-C1  feat(service): add graceful lifecycle and exact shutdown cleanup
L-C2  feat(cli): expose managed-routing capabilities

K-D1  feat(control): add schema2 managed Leshy mode transactions
K-D2  feat(cli): add privileged Leshy mode control
K-D3  test(control): prove transition crash recovery

K-E1  feat(packaging): orchestrate one-writer managed cutover on Linux
K-E2  feat(packaging): install dependent Leshy after KikimoraCore on Windows
K-E3  test(packaging): prove cutover and upgrade rollback

K-F   test(toad): add deterministic managed-routing recovery fixtures

K-G   test(linux): prove installed managed routing vertically
K-H   test(windows): prove installed managed routing vertically

L-I   refactor(routing): remove v1 managed activation compatibility

L-J1  chore(release): prepare Leshy 0.6.0
K-J2  chore(release): pin Leshy v0.6.0 by tag and exact SHA
K-J3  chore(release): prepare Kikimora 1.4.0

K-L   docs(release): record 08D1 final production evidence
~~~

---

# 17. VM use policy

The executor has two disposable VMs and must use them.

Linux VM proves:

~~~text
privileged netns
systemd lifecycle
real route/rule changes
package install/upgrade
process crash
VM reboot
~~~

Windows VM proves:

~~~text
Administrator route mutation
NotifyRouteChange2
SCM lifecycle/dependency
ACL
package install/upgrade
process crash
VM reboot
~~~

Unit tests remain on normal development hosts/CI.

Privileged networking behavior must be proved on the matching VM.

---

# 18. Handoff/evidence format after each phase

Append to execution log:

| Field | Required value |
|---|---|
| Phase | A/B/... |
| Kikimora SHA | exact 40-char or unchanged |
| Leshy SHA | exact 40-char or unchanged |
| Code entry points | files/functions |
| Local gates | command + PASS/FAIL |
| GitHub runs | run IDs + jobs |
| Linux VM | script + result if required |
| Windows VM | script + result if required |
| Foreign-state proof | before/after |
| Package hash | when artifact exists |
| Remaining blocker | none or exact blocker |
| Next phase allowed | YES/NO |

A FAIL means Next phase allowed=NO.

---

# 19. Autonomous stop/fix behavior

The executor does not stop to ask for ordinary engineering decisions.

~~~text
CI compile failure:
    fix code; rerun

test exposes bug:
    fix production code; keep test; rerun

VM harness defect:
    fix harness without weakening assertions; rerun

package lifecycle defect:
    fix installer/service contract; rerun fresh install + upgrade

real-host canary failure:
    execute rollback; fix source; return to owning VM phase
~~~

Stop without continuing only for:

~~~text
risk of modifying non-test foreign network state that cannot be isolated
missing required credentials for an existing real role at final canary
external infrastructure outage preventing all required evidence
irrecoverable repository corruption
~~~

When blocked, record exact evidence and leave release unpublished.

---

# 20. Final definition of done

Kikimora/Leshy are ready for release and real use only when all are true:

1. Windows local backend uses the real injected API seam.
2. Windows converged request does zero full FIB scans.
3. Windows exact desired identity includes LUID.
4. Windows MIB create uses InitializeIpForwardEntry.
5. Desired + foreign same-prefix conflicts.
6. Replacement never deletes old Owned before Desired proof.
7. Extended Windows native route matrix passes.
8. Kikimora transition recovery precedes Leshy config regeneration.
9. Managed startup waits for route-authority proof.
10. Shutdown paths are bounded.
11. Leshy graceful shutdown removes only exact local-owned routes.
12. Managed Leshy shutdown sends no Forget/Flush.
13. Linux SIGTERM gate passes.
14. Windows SCM graceful-stop gate passes.
15. Leshy capabilities command exists and is side-effect free.
16. schema2 marker/generation/activation are production authority.
17. Enable/Disable are journaled durable transactions.
18. every transition crash boundary recovers.
19. writer fence proves zero local/managed overlap.
20. stale activation/generation is denied before mutation.
21. Linux package service ordering is correct.
22. Windows Leshy depends on KikimoraCore and starts after readiness.
23. Ready-Toad deterministic fixture exists.
24. release build contains no test failpoint behavior.
25. Linux managed vertical matrix passes.
26. Windows managed vertical matrix passes.
27. both OS transition reboot matrices pass.
28. fresh package install passes on both VMs.
29. previous-release upgrade passes on both VMs.
30. ambiguous schema1 upgrade aborts/rolls back safely.
31. final runtime is v2-only.
32. Leshy exact green candidate is tagged v0.6.0.
33. Kikimora packages pin v0.6.0 and exact tag SHA.
34. Kikimora VERSION is 1.4.0.
35. packaged Linux/Windows Leshy both report 0.6.0.
36. final candidate artifacts have recorded SHA256.
37. real Linux workstation canary passes under dead-man rollback.
38. no route-authority/churn/network regression appears during canary.
39. tag 1.4.0 points to exact canary-tested Kikimora SHA.
40. 08D/08D1 status is COMPLETE.
41. release artifacts are published.
42. execution log contains exact evidence and historical audits.

Only after item 42 is true is the project considered ready for normal work
through managed Kikimora + Leshy on the real machine.

---

# 21. Post-D7 audit correction — 2026-11-10

The executor stopped after D7, but D7 is not accepted by the independent
production-code audit.

Mandatory continuation:

~~~text
docs/toad-steps/08d1-post-d7-audit-and-continuation.md
~~~

This file supersedes the direct D7 -> D8 -> D9 -> D10 continuation above where
the new audit is stricter.

Audited heads:

~~~text
Kikimora:
    e88c52cf63e1c8a2b81d0d5709f5f5988a9c494e

D7 implementation:
    0f964a4f577a5cbd7cb29653977394f701012be5

Leshy:
    d6cfd2be1a2b3cf8c5032c68a5103a730b01af5f
~~~

Corrected state:

~~~text
D1/D2 retain
D3 partial
D4 retain
D5/D6 partial
D7 REJECTED pending D7R
D8 not implemented
D9 partial
D10 not implemented
Phase E blocked
~~~

Mandatory prefix:

~~~text
D7R-1 fresh committed-state load after journal recovery
D7R-2 checkpoint activation fencing
D7R-3 live mode-transition gate and strict preconditions
D8 v2 request fence + privileged auth + dynamic Handshake
D9 complete safe diagnostics
D10 deterministic crash matrix on unit tests + ubuntu2604 + win10
~~~

Phase E is forbidden until the replacement packet records Phase D ACCEPTED
with green GitHub evidence and both VM D10 process matrices.

After that the executor continues autonomously through E/F/G/H/I/J/K/L.

---

# 22. Audit correction after Phase D / start of Phase E — 2026-11-10

Mandatory continuation audit:

~~~text
docs/toad-steps/08d1-post-phase-d-audit-2026-11-10.md
~~~

The prior Phase D ACCEPTED record is reopened by this audit.

Do not continue directly from current Phase E work.

Mandatory repair prefix:

~~~text
D10R-1 durable managed-marker removal + strict one-role-per-zone activation
D10R-2 version native D10 VM harnesses
D10R-3 rerun all D10 boundaries + all 10.5 negatives on ubuntu2604 and win10
D10R-4 exact-head Toad/ShellCheck/package green
Phase D re-acceptance
~~~

Only then resume Phase E.

Linux Phase E architecture is now explicit:

~~~text
Debian postinst does NOT perform a hidden network-authority cutover.
The explicit operator path is kk start / Go-owned orchestration.
That product path performs local Leshy -> graceful stop -> writer-lock proof
-> schema2 Enable -> managed Leshy start and verification.
~~~

The current managed-mode-cutover-vm.sh is only a verifier and must not be
treated as the product implementation.

Phase F remains blocked until Linux and Windows Phase E are both native-green.
