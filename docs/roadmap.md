# Kikimora authoritative implementation roadmap — 2026-11-11

Status: **MASTER EXECUTION ROADMAP / START HERE**

This file is the single execution-order authority for the current Kikimora
core + tests + desktop UI + UI↔core test work.

## Current evidence update — 2026-11-12

The source identity and CI snapshot in §1 below records the planning baseline
at consolidation time (`b751134`), not the current source. Its old failed-run
facts are historical. The current source/CI audit is
[08D1 current-state audit — 2026-11-12](toad-steps/08d1-current-state-audit-2026-11-12.md),
which records exact-head `8937ce7bca084d069013e7d8e3bb526d27764ab4` and the
four successful workflow runs. This update does not accept Phase E Windows,
Phase F, or Phase G and does not change the ordered gates below.

The Windows Desktop UI job is green at the workflow level but its nine suite
logs are empty and the required-suite wrapper accepts that condition. Treat
Desktop UI release acceptance as BLOCKED until the runner requires valid
QtTest totals and positive counts, then rerun and independently inspect the
fixed-head artifacts. This is a separate desktop evidence gate, not a change
to core R7 or the planned Phase G VM scope.

Core CI is green for the hosted R7 scope, but its focused barrier command omits
the tagged-only `TestRecoveryWithdrawSelectedMarkerOrdering` case. R2/R7
coverage remains incomplete until that exact test is selected and passes on a
fixed exact head; see the audit addendum for the R3/R5 source-test follow-ups.

It consolidates:

- the current 08D1 core/managed-routing audit and remediation;
- the production-readiness core phases and their VM/package gates;
- the Desktop UI/UI↔core audit;
- the Desktop UI executor/test coverage packet;
- the Phase G investigation recorded after 739e1ab;
- the current real Windows core/Leshy implementation reality.

An executor must not choose an alternative architecture or reorder stages.
Where an older document conflicts with this roadmap, this roadmap wins on
status, platform scope and execution order. The referenced subordinate packet
wins on implementation detail only where this roadmap explicitly delegates to
it.

---

# 1. Source identity and current status

Planning source at the start of this consolidation:

~~~text
origin/master:
    b751134879a72f65fded8ad68fbb391e374d0a17
    docs(08d1): Phase G start — VM preparation, G1 blocking investigation

last core/test implementation change:
    739e1ab3cb9cab359e32ba036057616ce7f10910
    feat(08d1): Phase F — deterministic integration infrastructure

last desktop implementation change:
    a2bc44102e93ca53e3d1733e991a2bc1b11d37c5
    feat(desktop): per-role IPv6 toggle on the role drawer

local audit/remediation planning already rebased above origin/master:
    2de8f98 docs(08d1): add post-audit remediation plan
    08bf120 docs(08d1): fold phase-g investigation into remediation

pinned managed-routing Leshy companion:
    61786d489da2acf50d6b4b53a77a39184d746852
~~~

Core status:

~~~text
Phase A: accepted
Phase B: accepted
Phase C: accepted
Phase D: accepted
Phase E Linux: accepted
Phase E Windows clean/fresh install: accepted evidence retained
Phase E Windows upgrade path: REOPENED
Phase F: REJECTED / REOPENED
Phase G: BLOCKED
Phase H: BLOCKED
Phase I-L: BLOCKED
~~~

Desktop status:

~~~text
two-page Home + Settings product structure: RETAIN
Desktop production source since a2bc441: unchanged
RealCoreClient current-core mapping: incomplete
managed routing safety presentation: incomplete
mandatory live UI↔core tests: not reliable
Desktop UI workflow: not a release gate
Ubuntu installed UI gate: not implemented as a hard gate
Windows installed UI gate: existing runner is not a hard gate
UI release acceptance: BLOCKED
~~~

The b751134 Phase-G attempt against 739e1ab is exploratory evidence only.
It does not satisfy any G-stage exit gate.

Actual CI evidence for 739e1ab:

~~~text
Build packages 43016857864: SUCCESS
ShellCheck     43016857909: SUCCESS
Toad core      43016857823: FAILURE
~~~

Documentation-only b751134 confirms the same failure state:

~~~text
ShellCheck 43024324194: SUCCESS
Toad core  43024324300: FAILURE
~~~

All three go-unit jobs in 43024324300 fail at formatting before Vet/Build/Unit/Race.

---

# 2. Authority hierarchy

Read and execute in this order.

## 2.1 Master order/status authority

~~~text
docs/roadmap.md
~~~

This file decides:

- current state;
- release/platform scope;
- stage ordering;
- which older statements are stale;
- cross-core/UI dependencies;
- final release gate.

## 2.2 Core remediation implementation authority

~~~text
docs/toad-steps/08d1-post-739e1ab-audit-remediation.md
~~~

Mandatory for R1-R8 and the corrected G1 contract.

## 2.3 Core production/VM/release behavior authority

~~~text
docs/toad-steps/08d1-production-readiness-plan.md
docs/toad-steps/08d1-managed-selected-routes-completion.md
docs/toad-steps/08d1-post-d7-audit-and-continuation.md
~~~

Use these for detailed managed-routing invariants, G/H/I/J/K/L behavior and
regression matrices, subject to the exact execution-order and harness
supplements in this roadmap.

## 2.4 UI audit and implementation authority

~~~text
docs/desktop-ui-core-audit-2026-11-10.md
docs/desktop-ui-executor-coverage-plan-2026-11-11.md
docs/desktop-ui-test-execution-and-coverage-plan.md
~~~

The 2026-11-11 executor packet remains the detailed UI implementation packet.
The supplements in this roadmap are mandatory where that packet left an
implementation choice.

## 2.5 Evidence only

~~~text
docs/toad-steps/08d1-execution-log.md
docs/ui-coverage-matrix.md
~~~

Execution logs never override a red current test or a newer audit.
The 2026-11-12 audit addendum supplies the current workflow and source evidence;
it does not substitute hosted CI for a native or VM acceptance row.

Historical architecture/implementation documents remain useful background but
are not execution-order authorities:

~~~text
docs/desktop-ui-architecture.md
docs/desktop-ui-implementation-plan.md
docs/go-core-ui-toads-integration-plan.md
docs/toad-roadmap.md
docs/toad-steps/09-desktop-ui.md
~~~

---

# 3. Current platform truth

Old roadmap statements saying Windows is FakeCore-only are obsolete.

The current platform contract is:

~~~text
Linux
    real kikimora-core
    real Toads
    real Leshy
    real Qt/QML UI
    full managed-routing release gate

Windows
    real KikimoraCore Windows service
    real Toads / native route writer
    real Leshy Windows integration
    real AF_UNIX core control socket
    real Qt/QML UI
    full managed-routing release gate

macOS
    shared Qt/QML UI
    darwin core/toad builds
    macOS staging/package sources exist
    hermetic desktop/API CI remains mandatory
    native managed-routing vertical acceptance is NOT part of the current
    08D1 Linux+Windows release acceptance packet

FakeCore
    explicit --fake developer/test backend only
    never the automatic Windows production backend
~~~

Current 08D1 release scope is Linux + Windows native managed routing.
Do not claim native macOS managed-routing release acceptance from build-only or
desktop-hermetic evidence. A later native macOS vertical must be a separately
versioned packet before macOS managed networking is advertised as accepted.

Desktop CI still builds/tests the shared frontend on Linux/macOS/Windows.

---

# 4. Product and architecture invariants

These rules are fixed for all subsequent stages.

## 4.1 One network authority

~~~text
QML
    -> CoreBackend
    -> RealCoreClient
    -> versioned local control API
    -> kikimora-core
    -> Toad / route / Leshy ownership
~~~

QML never:

- shells out;
- parses human CLI output;
- writes routes/interfaces;
- independently decides network truth;
- bypasses the core's available_actions for role action eligibility.

## 4.2 One dominant UI structure

Retain exactly two top-level pages:

~~~text
Home
Settings
~~~

Profiles, role configuration, routing-list editing and role details remain
drawers/panels within that two-page product. Do not restore the historical
five-tab UI to satisfy stale tests or docs.

## 4.3 Managed-routing safety truth

A managed system is never presented as protected when:

~~~text
managed_mode == true
&& route_authority_ready == false
~~~

Mode transition, API incompatibility and reconnect-before-authoritative-snapshot
must all make additive/network mutations unavailable.

## 4.4 Family truth remains independent

IPv4 Ready does not imply IPv6 Ready and vice versa.

The UI consumes:

~~~text
roles[].families.ipv4
roles[].families.ipv6
~~~

and never reconstructs them from route_ready.

## 4.5 One writer and exact ownership

All core routing tests preserve the existing 08D1 invariants:

- exact selected-route identity;
- exact park identity;
- no prefix-wide ownership inference;
- no local fallback during managed authority loss;
- no local + managed writer overlap;
- checkpoint/fence/generation semantics stay fail-closed.

## 4.6 Tests are part of the product contract

A stage is not green when:

- a mandatory QtTest case QSKIPs;
- a mandatory Go test was skipped by an earlier formatting failure;
- an unexpected QML warning is merely logged;
- a VM runner prints a failure but exits zero;
- an ad-hoc /tmp script substitutes for a required versioned harness;
- a test touches product /etc, /var/lib, /run or ProgramData when it claims to
  be hermetic Layer C.

## 4.7 Audit-finding ownership index

Every release-relevant finding from the two current audits has one owning
stage. Do not close a finding from a different stage merely because a nearby
test is green.

Core audit:

| Finding | Owning stage |
|---|---|
| A1 Windows state -> state\\core without migration | C1 |
| A2 recursive tagged recovery barrier | C2 |
| A3 recovery barrier before, not after, RecoveryStep | C2 |
| A4 TestTriggerManagedRecovery no-op | C3 |
| A5 Ready-Toad unit suite red | C4 |
| A6 Ready-Toad lifecycle contract incomplete | C4 |
| A7 shared DNS QTYPE parser broken | C5 |
| A8 shared DNS helpers not actually shared | C5 |
| A9 Windows Row G bypasses SetRoutingList API | C6 |
| A10 Phase-F CI jobs missing | C7 |
| A11 exact-head Toad core CI red | C7 -> C8 -> X1 |

Desktop/UI↔core audit:

| Finding | Owning stage |
|---|---|
| P0-UI1 route-authority safety ignored | U1 + U3 + U8e/U9e |
| P0-UI2 independent IPv4/IPv6 readiness ignored | U1 + U3 |
| P0-UI3 selected-route diagnostics dropped | U1 + U3 |
| P0-UI4 ordinary CTest omits live UI↔core coverage | U5 + U6 + U7 |
| P0-UI5 live suites stale/red when actually run | U4 + U5 + U6 |
| P0-UI6 core API changes do not trigger Desktop UI CI | U7 |
| P0-UI7 no current exact-master Desktop UI evidence | U7 + X1 + U12 |
| P0-UI8 Windows guest runner can hide overall failure | U9i + U9e |
| P1-UI1 leshyIpv6 lost by RoleListModel::get | U1 |
| P1-UI2 FakeCore diagnostics schema differs from real core | U2 |
| P1-UI3 FakeCore Windows Leshy support stale | U2 |
| P1-UI4 RealCoreClient uses API v2 / weak Handshake | U1 |
| P1-UI5 command error lifetime stale | U1 |
| P1-UI6 reconnect command gating incomplete | U1 + U3 |
| P1-UI7 live tests do not reproduce production QML context | U4 |
| P1-UI8 QML warnings do not fail tests | U4 + U6 |
| P1-UI9 routing-list live test is not hermetic | U5 |
| P1-UI10 live core fixture leaks into product defaults | U5 |

The audit's previously-unproved functional list is also owned explicitly:

~~~text
authority/mode/family/selected diagnostics -> U1/U3
mode generation + redacted fingerprints -> U1/U3
managed_interfaces shape -> U1/U2/U3
IPv6 toggle reflects leshy_ipv6 -> U1/U3
autostart/show/hide/close-to-tray -> U3 + U8e/U9e
clipboard paste/import -> U3 + Layer-B test
diagnostics capture through real core -> U5
API incompatibility -> U1/U3
command error clear/recovery -> U1
real reconnect -> U5
keyboard/focus/high-DPI/visual safety -> U10
~~~

---

# 5. Unified execution order

Execute exactly this sequence:

~~~text
M0  baseline/document authority freeze

CORE REPAIR
C1  R1 Windows state-layout migration
C2  R2 recovery barrier at post-step boundary
C3  R3 tagged TestTriggerManagedRecovery -> real production recovery request
C4  R4 Ready-Toad contract completion
C5  R5 shared DNS repair/deduplication
C6  R6 Windows Row G through SetRoutingList API
C7  R7 CI + release-absence proof
C8  R8 native reruns + Phase E/F re-acceptance

UI + UI↔CORE IMPLEMENTATION BEFORE VM VERTICALS
U1  current core truth in CoreBackend/RealCoreClient/model
U2  FakeCore schema identity
U3  one C++ safety policy + QML/tray fail-closed consumption
U4  production-equivalent QML bootstrap + warning gate
U5  hermetic real-core fixture + core runtime-path injection
U6  zero-skip/zero-warning required-suite wrappers
U7  Desktop UI all-OS workflow as a core/API contract gate

RELEASE PACKAGING ARCHITECTURE BEFORE PACKAGE VM GATES
P1  unify integrated Linux/Windows release artifact contracts and workflow ownership
T1  version every G/H privileged/reboot/package acceptance harness before execution

COMBINED-HEAD REVALIDATION
X1  rerun core Phase-F/nonprivileged + desktop/package/harness gates on the exact post-T1 SHA

CORE VERTICAL ACCEPTANCE ON THE COMBINED HEAD
G1  standalone local Leshy gate
G2  Linux managed success E2E
G3  Linux transition crash matrix
G4  Linux reboot matrix
G5  Linux package fresh install
G6  Linux package upgrade
H1  Windows local-writer/native identity gate
H2  Windows managed success E2E
H3  Windows transition crash + reboot matrix
H4  Windows SCM dependency/readiness
H5  Windows auth/ACL
H6  Windows package fresh install + upgrade

FINAL MANAGED WIRE CUT
I1-I3 v2-only managed compatibility cut + both VM smokes

DESKTOP COMPLETENESS BEFORE CANDIDATE
U8i  implement Linux installed UI runner (do not execute release acceptance yet)
U9i  harden Windows installed UI runner (do not execute release acceptance yet)
U10  accessibility/geometry/deterministic safety screenshots
U11  native C++ source coverage

RELEASE CANDIDATE
J1-J5 exact Leshy/Kikimora candidate versions, pins and artifacts

INSTALLED DESKTOP ACCEPTANCE AGAINST EXACT CANDIDATE ARTIFACTS
U8e Ubuntu installed UI acceptance
U9e Windows installed UI acceptance
U12 UI evidence/status closure

REAL HOST CANARY
K1-K8 Linux workstation candidate canary

FINAL RELEASE
L1-L5 source freeze, tag, artifacts, docs, publish
~~~

Why U1-U7/P1/X1 are before G/H:

U5 changes kikimora-core's test/dev runtime-path plumbing. Production defaults
must remain unchanged, but the change still touches core startup/config paths.
P1 changes the package builders/contracts that G5/G6/H6 are supposed to
accept. X1 then proves the exact combined head before privileged deployment.

G/H must validate that combined exact head. Do not accept G/H first and then
alter runServe/Manager path wiring or release-package composition afterward.

---

# 6. M0 — baseline and document freeze

Before code changes:

~~~text
git status --short
git rev-parse HEAD
git rev-parse origin/master
cat .github/managed-routing-companion.env
~~~

Record exact values in the execution log.

Do not stage/remove the user's unrelated untracked files.

The current known unrelated local files are not roadmap inputs:

~~~text
.gigacode/
docs/vbox-guest-network-analysis.md
kikimora1.png
kikimora2.png
kikimora3.png
kikimora4.png
~~~

Canonical lab-VM transport for every later native gate:

~~~text
scripts/lab/vm.py
~~~

Use:

~~~text
python3 scripts/lab/vm.py discover ubuntu --source
python3 scripts/lab/vm.py discover windows --source
~~~

immediately before a native acceptance run. Address resolution, authentication
fallbacks and upload semantics are documented in `docs/lab-vms.md`.

Do not use ignored `tmp/vm_*.py` / `tmp/win_*.py` helpers as acceptance
contracts and do not hardcode historical `192.168.1.x` addresses.

M0 exit:

- this roadmap is the documented START HERE;
- older Windows-FakeCore-only plan text is explicitly marked superseded;
- execution log points back to this roadmap;
- the tracked lab helper self-test is green.

---

# 7. C1-C8 — core remediation and Phase F re-acceptance

Execute R1-R8 exactly from:

~~~text
docs/toad-steps/08d1-post-739e1ab-audit-remediation.md
~~~

No Phase G work while any R-stage is red.

## C1 / R1 — Windows state migration

Production files:

~~~text
toad/cmd/kikimora-core/service_windows.go
toad/cmd/kikimora-core/state_layout_windows.go        # add
packaging/windows/install.ps1
packaging/windows/test_state_layout_upgrade.ps1       # add
~~~

Keep new authoritative state root:

~~~text
C:\ProgramData\Kikimora\state\core
~~~

Copy-and-verify exactly the four legacy core-owned files defined by R1.
Do not move per-Toad selected-routes state or the parent writer lock.

Exit is not unit-only: native legacy-local + legacy-managed + in-flight
transition upgrade rows must pass.

## C2 / R2 — recovery barrier

Delete the incorrect control-layer recursive barrier.

Implement:

~~~text
toad/internal/core/recovery_barrier_default.go
toad/internal/core/recovery_barrier_testbuild.go
~~~

Call only after a successful RecoveryStep in Engine.Recover before the next
step advances.

## C3 / R3 — test trigger

The tagged API method:

~~~text
TestTriggerManagedRecovery
~~~

must enter the real:

~~~text
Manager.requestManagedSelectedRecovery
    -> production recovery worker
    -> Engine.Recover
    -> real RecoveryDriver
~~~

No common-build exported trigger variable.

## C4 / R4 — Ready-Toad

Ready-Toad must:

- load a valid normal role config;
- fail Start when the configured interface is initially absent;
- report SessionConnected while started;
- publish family-filtered addresses;
- resample ifindex/address state on Rebind and RestartTransport;
- use one injected resolver seam in unit tests;
- produce revision changes that Subscribe can actually observe.

## C5 / R5 — DNS fixture

One fixture identity:

~~~text
A    203.0.113.10
AAAA 2001:db8:dead::10
~~~

The existing 739e1ab value 2001:db8::10 is superseded.

Both denial and managed-success tests use:

~~~text
linux/tests/toad/fixtures/dns-stub.py
linux/tests/toad/fixtures/dnsq.py
~~~

## C6 / R6 — Windows Row G

No Add-Content mutation of the real routing list.

Use:

~~~text
routeprobe set-list
    -> SetRoutingList
    -> production control API
~~~

Restore original list content through the same API.

## C7 / R7 — CI

Exact-head required:

- gofmt clean;
- all-OS normal Go unit/race path;
- focused tagged Phase F tests;
- release absence;
- shared DNS self-test;
- old denial smoke;
- managed-success Linux CI;
- Windows cross/package contract.

## C8 / R8 — re-acceptance

Only after all R1-R8 gates are green append:

~~~text
Phase E Windows re-ACCEPTED (fresh + upgrade)
Phase F ACCEPTED
~~~

Do not carry forward the historical 739e1ab "Phase F COMMITTED" as acceptance.

---

# 8. U1 — current core truth in the desktop backend

Detailed packet:

~~~text
docs/desktop-ui-executor-coverage-plan-2026-11-11.md §4
~~~

Implementation points:

~~~text
desktop/src/core/CoreBackend.h
desktop/src/core/RealCoreClient.h
desktop/src/core/RealCoreClient.cpp
desktop/src/models/RoleListModel.h
desktop/src/models/RoleListModel.cpp
desktop/tests/RealCoreClientTest.cpp
desktop/tests/RoleListModelTest.cpp
~~~

Required protocol/snapshot properties remain those in the UI executor packet.

Add these exact UI policy properties as part of U1; this closes an ambiguity in
the older plan:

~~~text
safeToMutate: bool
safeToDisconnect: bool
diagnosticsAvailable: bool
diagnosticsMutable: bool
~~~

They belong to CoreBackend and both backend implementations.
QML does not reconstruct these booleans.

RealCoreClient state definitions:

~~~text
apiCompatible =
    compatible Handshake v3 accepted
    AND current socket connection still belongs to that handshake

commandChannelReady =
    apiCompatible
    AND authoritative Snapshot received on the current connection
    AND startup_ready == true
    AND mode_transition == "none"

safeToMutate =
    commandChannelReady
    AND (!managed_mode || route_authority_ready)

safeToDisconnect =
    commandChannelReady

diagnosticsAvailable =
    apiCompatible

diagnosticsMutable =
    apiCompatible
    AND startup_ready
    AND mode_transition == "none"
~~~

On socket disconnect:

~~~text
apiCompatible = false
commandChannelReady = false
safeToMutate = false
safeToDisconnect = false
diagnosticsAvailable = false
diagnosticsMutable = false
startupReady = false
routeAuthorityReady is not actionable stale truth
~~~

Do not keep a stale compatible-handshake flag through reconnect.

## 8.1 Backend-side command guard

Do not rely only on button.enabled.

RealCoreClient must reject programmatic calls by class before sendCommand.

Exact classes:

~~~text
safeToMutate:
    ConnectAll
    ConnectRole
    RetryRole
    ValidateRole
    RediscoverEndpoints
    SetActiveProfile
    CreateTunnel
    CreateProfile
    SetTunnelProfiles
    RemoveTunnel
    SetRoutingList
    SetBypassMode
    SetRoleIPv6

safeToDisconnect:
    DisconnectAll
    DisconnectRole

diagnosticsAvailable:
    GetDiagnosticsSnapshot
    GetRoutingLists

diagnosticsMutable:
    SetDiagnosticsCapture
~~~

importTunnel is a local parse followed by CreateTunnel, so the CreateTunnel
guard is authoritative.

nextDemoScenario is FakeCore-only and never maps to production IPC.

When a blocked mutating method is called programmatically:

- send no IPC request;
- set a command-scoped human-readable blocked reason;
- emit snapshotChanged once;
- do not alter optimistic profile/member state.

In particular, SetActiveProfile and SetTunnelProfiles must test the guard
before changing local optimistic caches.

## 8.2 Role action classification

roleActionByName maps exactly:

~~~text
connect    -> safeToMutate
disconnect -> safeToDisconnect
retry      -> safeToMutate
validate   -> safeToMutate
rediscover -> safeToMutate
~~~

Unknown action remains a no-op.

## 8.3 U1 tests added to the existing packet

In addition to the named U1 tests already required, add:

~~~text
safePolicyBlocksConnectDuringAuthorityLoss()
safePolicyAllowsDisconnectDuringAuthorityLoss()
safePolicyBlocksAllCommandsBeforeHandshakeSnapshot()
programmaticBlockedMutationSendsNoFrame()
blockedProfileSwitchDoesNotChangeOptimisticProfile()
blockedMembershipEditDoesNotChangeOptimisticMembership()
diagnosticsReadAllowedDuringAuthorityLoss()
diagnosticsCaptureAllowedDuringAuthorityLossWhenNoTransition()
modeTransitionBlocksDiagnosticsCaptureButNotRead()
disconnectPolicyClearsOnSocketLoss()
~~~

---

# 9. U2 — FakeCore schema identity

Detailed packet:

~~~text
docs/desktop-ui-executor-coverage-plan-2026-11-11.md §5
~~~

FakeCore is a schema/state simulator, not a Windows product fallback.

It implements the same:

~~~text
safeToMutate
safeToDisconnect
diagnosticsAvailable
diagnosticsMutable
~~~

rules from U1 so QML behavior does not depend on backend kind.

Required FakeCore scenarios include:

- local stopped;
- local ready;
- managed healthy;
- managed authority lost;
- transition prepared/config_written/marker_written;
- API incompatible;
- reconnect/no authoritative snapshot;
- v4-only ready;
- v6-only ready;
- IPv6 disabled;
- selected conflict/denial/park;
- nested managed_interfaces.

Windows FakeCore reports Windows Leshy capability consistently with real core,
but normal production startup still chooses RealCoreClient unless --fake was
explicitly supplied.

---

# 10. U3 — one fail-closed presentation policy for QML and tray

The older UI packet listed only part of the call sites. Use this complete set:

~~~text
desktop/qml/Main.qml
desktop/qml/Pages/DiagnosticsPage.qml
desktop/qml/Drawers/ProfileDrawer.qml
desktop/qml/Controls/KConnectControl.qml
desktop/qml/Controls/ConnectCircle.qml
desktop/qml/Controls/KRoleDetailDrawer.qml
desktop/src/DesktopIntegration.h
desktop/src/DesktopIntegration.cpp
desktop/tests/UiBehaviorTest.cpp
desktop/tests/TrayMenuTest.cpp
desktop/tests/TrayLiveTest.cpp
~~~

Every network/config mutation consumes Core.safeToMutate or
Core.safeToDisconnect. There is no separate local QML safety formula.

Exact call-site policy:

~~~text
Main aggregate action:
    DISCONNECT -> safeToDisconnect
    CONNECT    -> safeToMutate

profile selector / profile create:
    safeToMutate

ProfileDrawer import:
    safeToMutate

role action:
    disconnect -> safeToDisconnect
    all other production role actions -> safeToMutate

role membership edit:
    safeToMutate

remove tunnel:
    safeToMutate

routing list save:
    safeToMutate

bypass/default route:
    safeToMutate

role IPv6 policy:
    safeToMutate

refresh diagnostics:
    diagnosticsAvailable

diagnostics capture toggle:
    diagnosticsMutable

DesktopIntegration autostart:
    independent local desktop setting; no core safety gate

show/hide/quit:
    independent local desktop action; no core safety gate
~~~

## 10.1 Tray is part of the safety surface

Current DesktopIntegration::refreshMenu enables aggregate connect action from
only:

~~~text
coreState != Unavailable/Error
~~~

Replace that policy.

Tray action enabled state:

~~~text
if aggregateActionText == DISCONNECT:
    enabled = CoreBackend::safeToDisconnect()
else:
    enabled = CoreBackend::safeToMutate()
~~~

Tray state text must also obey the same "never Protected during managed
authority loss" rule as Main.qml.

Add:

~~~text
trayAuthorityLossNeverClaimsProtected()
trayConnectDisabledWhenMutationUnsafe()
trayDisconnectAvailableDuringAuthorityLoss()
trayDisabledBeforeAuthoritativeReconnectSnapshot()
~~~

## 10.2 Safety banner

Use one banner with stable objectName:

~~~text
routeAuthorityBanner
~~~

Priority of explanation:

~~~text
1 API incompatible
2 mode transition
3 managed route authority lost
4 reconnect/no authoritative snapshot where relevant
~~~

Do not leak full activation/transition IDs.

## 10.3 Mode/authority diagnostics rows

`desktop/qml/Pages/DiagnosticsPage.qml` must expose the current core contract
without reconstructing it.

Add stable rows/object names:

~~~text
diagStartupReady
diagManagedMode
diagModeGeneration
diagModeTransition
diagActivationFingerprint
diagTransitionFingerprint
diagRouteAuthority
diagRouteAuthorityError
~~~

Values come only from CoreBackend properties.

Rules:

- fingerprints show only the core-provided redacted fingerprints;
- empty fingerprint renders "—";
- mode transition renders "none" when no journal is active;
- route authority renders "ready"/"not ready";
- routeAuthorityError is visible only when nonempty;
- no full ActivationID, transition ID or auth token is ever accepted by the
  backend/QML model.

Add Layer-B tests:

~~~text
diagnosticsRenderModeGenerationAndFingerprints()
diagnosticsNeverRenderFullAuthoritySecrets()
diagnosticsRenderAuthorityError()
~~~

## 10.4 Clipboard import is local; Add is a mutation

`pasteImportButton` remains usable when core mutation is unsafe because it
only copies local clipboard text into the editor.

`addTunnelButton` obeys:

~~~text
enabled =
    Core.safeToMutate
    && importField.text.trim().length > 0
~~~

The import preview/description remains local and may render while mutation is
blocked.

Layer-B test uses the real Qt clipboard under the shared offscreen
QGuiApplication:

~~~text
QGuiApplication::clipboard()->setText(<fixture link>)
open ProfileDrawer
click pasteImportButton
assert importField.text == fixture link
assert zero Core mutation calls
force safeToMutate=false
assert addTunnelButton disabled
invoke blocked import programmatically
assert RealCore/FakeCore mutation counter unchanged
~~~

Required test name:

~~~text
clipboardPasteIsLocalAndImportHonorsSafetyGate()
~~~

---

# 11. U4 — production-equivalent QML bootstrap

Detailed packet:

~~~text
docs/desktop-ui-executor-coverage-plan-2026-11-11.md §7
~~~

Create one shared test bootstrap:

~~~text
desktop/tests/support/UiTestFixture.h
desktop/tests/support/UiTestFixture.cpp
desktop/tests/support/QmlWarningGuard.h
desktop/tests/support/QmlWarningGuard.cpp
~~~

It owns:

1. QQuickStyle::setStyle("Basic") before Controls imports;
2. Core context;
3. Desktop context;
4. ThemeManager context;
5. SystemIconProvider;
6. qrc:/qml/Main.qml loading;
7. warning capture.

Apply it to every full-QML suite. Do not maintain separate miniature bootstrap
copies in RealCoreUiTest and RealCoreMatrixTest.

Unexpected warnings are failures, not log decoration.

---

# 12. U5 — hermetic real-core fixture and exact core path architecture

This stage intentionally lands before G/H because it modifies core startup
plumbing.

Detailed UI packet §8 is supplemented here with an exact implementation.

## 12.1 New core runtime-path value object

Add:

~~~text
toad/internal/control/runtime_paths.go
~~~

Define:

~~~go
type RuntimePaths struct {
    LeshyConfig       string
    RoutingDir        string
    BypassConfig      string
    LeshyPublication  string
}

func (m *Manager) SetRuntimePaths(paths RuntimePaths) error
~~~

Contract:

- may be called only before StartupManagedAuthority;
- empty field means existing production default;
- non-empty paths are cleaned with filepath.Clean;
- SetRuntimePaths does not create files;
- once StartupManagedAuthority begins, later SetRuntimePaths returns an error;
- production serviceOptions omits overrides and retains current defaults.

Manager existing internal fields remain the implementation storage:

~~~text
m.leshyConfigPath
m.routingDir
m.bypassConfigPath
m.leshyPublicationDir
~~~

Add exactly one helper:

~~~go
func (m *Manager) leshyPublicationDirectory() string
~~~

It returns `m.leshyPublicationDir` when set and
`config.DefaultLeshyVPNDir()` otherwise.

Change:

~~~text
toad/internal/control/config_api.go
    (*Manager).buildLeshyZones
~~~

to pass `m.leshyPublicationDirectory()` into `config.BuildLeshyZones`.
The current direct `config.DefaultLeshyVPNDir()` call is forbidden after U5.

Do not add a second path source.

Add a manager boolean that closes runtime-path configuration at the beginning
of StartupManagedAuthority.

## 12.2 serve CLI

Extend only development/test serve flags:

~~~text
kikimora-core serve
    --leshy-config <path>
    --routing-dir <path>
    --bypass-config <path>
~~~

Existing flags remain:

~~~text
--config-dir
--state-dir
--leshy-publication-dir
--socket
--toad-binary
~~~

Add fields to serveOptions:

~~~text
leshyConfig
routingDir
bypassConfig
diagnosticsHostSources *bool
~~~

The existing `leshyPublicationDir` serveOptions field is also passed into
`RuntimePaths.LeshyPublication`; the same exact value continues to configure
`leshy.FileBridge`. Generated Leshy zone `route_target` and FileBridge must
therefore point at one identical publication root.

Add serve flag:

~~~text
--diagnostics-host-sources=true|false
~~~

The console `serve` default is true. `serviceOptions()` leaves the pointer
nil, and runServe interprets nil as true, so packaged production service
behavior cannot change accidentally from a zero-value bool.

runServe order:

~~~text
acquire core state lock
construct Manager with deferred managed restore
SetRuntimePaths({
    LeshyConfig,
    RoutingDir,
    BypassConfig,
    LeshyPublication: opts.leshyPublicationDir,
})
StartupManagedAuthority(...)
wire desired store
SetDiagnosticsDir(...)
SetDiagnosticsHostSources(resolved diagnostics-host-sources value)
StartDiagnostics(...)
wire route manager/recovery/publication
Serve
~~~

SetRuntimePaths must happen before StartupManagedAuthority because committed
mode recovery reads/writes the generated Leshy config.

Windows service path does not pass these test/dev flags.

## 12.3 One generated-config path

Fix:

~~~text
toad/internal/control/api.go
    Handle("GetRoutingLists")
~~~

It currently reads zones from:

~~~text
config.DefaultLeshyConfigPath()
~~~

Change it to the Manager's:

~~~text
m.leshyConfigFile()
~~~

All generated-config reads/writes during a run use that one path.

## 12.4 Hermetic diagnostics and ambient host sources

Current diagnostics Collector still reads ambient product logs through
platform `logGlobs()` even when `hostSources=false`; the existing flag only
suppresses journal/event-log/networkState. That is not hermetic enough for
Layer C.

Modify:

~~~text
toad/internal/diagnostics/collector.go
toad/internal/control/control.go
toad/cmd/kikimora-core/main.go
~~~

Add Manager method:

~~~go
func (m *Manager) SetDiagnosticsHostSources(enabled bool) error
~~~

Contract:

- `SetDiagnosticsDir` must already have created the collector;
- the method returns an error when diagnostics are not configured;
- runServe calls it before `StartDiagnostics`;
- it is not exposed as a control API method;
- production default is true.

When `Collector.hostSources == false`, one collection pass must not:

- invoke journalctl;
- invoke wevtutil;
- expand/read platform `logGlobs()`;
- run networkState commands.

It may still write the core's own in-memory diagnostics snapshot and static
host/os metadata into the temp diagnostics spool.

Implement the ambient-file rule in `collectFiles` itself:

~~~go
if !c.hostSources {
    return nil
}
~~~

so future callers cannot accidentally bypass the isolation.

In `toad/cmd/kikimora-core/main.go`, add:

~~~go
func resolvedDiagnosticsHostSources(value *bool) bool
~~~

with exact behavior:

~~~text
nil   -> true
true  -> true
false -> false
~~~

Console `serve` passes the parsed bool pointer; packaged service options leave
it nil.

Layer-C LiveCoreFixture always passes:

~~~text
--diagnostics-host-sources=false
~~~

This allows `diagnosticsCaptureRoundTrip()` to exercise the real collector
without reading the developer/CI host's installed product logs or network
state.

## 12.5 Core tests for runtime paths and diagnostics

Add:

~~~text
toad/internal/control/runtime_paths_test.go
toad/cmd/kikimora-core/serve_paths_test.go
~~~

Required:

~~~text
emptyRuntimePathsKeepProductionDefaults
runtimePathsOverrideAllFourConfigSurfaces
runtimePathsMustBeSetBeforeStartupAuthority
getRoutingListsUsesInjectedLeshyConfig
setRoutingListWritesOnlyInjectedRoutingDir
setBypassModeWritesOnlyInjectedBypassConfig
buildLeshyZonesUsesInjectedPublicationDir
fileBridgeAndGeneratedRouteTargetsSharePublicationDir
managedConfigTransactionUsesInjectedLeshyConfig
diagnosticsHostSourcesDefaultToTrue
diagnosticsHostSourcesFalseSkipsAmbientCollectors
serveFlagsReachManagerBeforeStartupAuthority
hermeticServeCreatesNothingOutsideTempRoot
~~~

`diagnosticsHostSourcesFalseSkipsAmbientCollectors` must enable diagnostics
capture and run a real collection pass while substituting/observing the command
and file-glob seams; zero ambient host command/log reads are allowed.

`hermeticServeCreatesNothingOutsideTempRoot` uses a temp root and every
override described below. It snapshots known product paths before/after and
requires no test-created file there.

## 12.6 Desktop LiveCoreFixture

Create:

~~~text
desktop/tests/support/LiveCoreFixture.h
desktop/tests/support/LiveCoreFixture.cpp
~~~

Use exact binaries passed by the test runner.

Fixture owns:

~~~text
<temp>/configs
<temp>/state
<temp>/publication
<temp>/providers
<temp>/leshy/config.toml
<temp>/leshy/domains
<temp>/leshy/bypass.json
<temp>/core.sock
<temp>/core-restart.pending
~~~

It starts the core with exactly:

~~~text
serve
--config-dir <temp>/configs
--state-dir <temp>/state
--leshy-publication-dir <temp>/publication
--leshy-config <temp>/leshy/config.toml
--routing-dir <temp>/leshy/domains
--bypass-config <temp>/leshy/bypass.json
--endpoint-provider-dir <temp>/providers
--socket <temp>/core.sock
--toad-binary <exact fake-toad helper>
--diagnostics-host-sources=false
~~~

Child environment includes exactly:

~~~text
KIKIMORA_RESTART_MARKER=<temp>/core-restart.pending
~~~

The existing RestartMarkerPath env seam is reused; do not add a second restart
marker flag.

Before launch create the empty providers directory. No helper may fall back to
`/usr/local/libexec/kikimora/endpoint-providers`.

The fixture asserts generated Leshy zone `route_target` files are rooted
under `<temp>/publication`, proving the Manager config and FileBridge share
the same publication root.

It passes every mutable/product path explicitly.

Deterministic roles/profiles/lists are the alpha/beta/work/home fixture from
the UI executor packet.

## 12.7 Layer-C scope is explicit

Hermetic Layer C proves real process + real IPC + real filesystem/config API.
It is not required to manufacture every privileged managed-authority failure
inside unprivileged CI.

Layer ownership is:

~~~text
Layer A:
    scripted protocol replies -> parser/API incompatibility/authority field maps

Layer B:
    FakeCore -> every safety-state UI variant

Layer C:
    real kikimora-core + real AF_UNIX/local socket
    local-mode startup/reconnect/profile/routing/config round trips
    actual current Snapshot field presence and transport behavior

Layer D:
    installed Ubuntu/Windows managed core
    real managed authority healthy/lost/recovery and selected-route truth
~~~

Therefore tests such as "managed authority lost reaches real UI" are mandatory
in Layer D and in A+B, not faked into Layer C by manually forging production
state files.

Layer C still verifies that a real core local-mode Snapshot maps the authority
fields with their real local-mode values.

This closes the older coverage ambiguity without weakening managed VM proof.

---

# 13. U6 — required suite wrappers

Add exactly:

~~~text
desktop/tests/run-required-suites.sh
desktop/tests/run-required-suites.ps1
~~~

Required groups:

~~~text
Layer A
    real-core-client-test
    role-list-model-test
    system-theme-test

Layer B
    ui-behavior-test
    tray-menu-test

Layer C
    real-core-e2e-test
    real-core-ui-test
    real-core-matrix-test
    tray-live-test
~~~

Wrapper failure conditions:

- child exit nonzero;
- mandatory QSKIP/SKIP;
- QML ReferenceError/TypeError/binding loop/load failure;
- QQuickStyle ordering warning;
- required-property/assignment QML warning;
- missing requested binary;
- LiveCoreFixture cannot reach compatible authoritative Snapshot.

Every suite log is preserved under one artifact directory.

Do not parse only the final CTest summary.

---

# 14. U7 — Desktop UI GitHub workflow

Modify:

~~~text
.github/workflows/desktop-ui.yml
~~~

Required trigger scope:

~~~text
desktop/**
toad/**
VERSION
.github/managed-routing-companion.env
.github/workflows/desktop-ui.yml
~~~

on pull_request and push to master, plus workflow_dispatch.

Build core/test helper on:

~~~text
ubuntu-latest
macos-15
windows-latest
~~~

Use exact pinned Qt and LinguistTools.

Use current managed-routing Leshy pin, never historical ftelnov/leshy v0.4.0.

All OSes run Layer A/B/C through the required-suite wrapper.

Linux additionally runs deterministic screenshots and coverage once U10/U11
land.

U7 exit:

~~~text
Linux green
macOS green
Windows green
mandatory skips 0
unexpected warnings 0
exact source SHA printed
exact companion SHA printed/verified
~~~

The Desktop UI workflow is a test/coverage workflow, not the release artifact
owner. Remove its historical "Build native Linux release packages" /
"kikimora-linux" upload lane once U7 lands. Release-named Linux/Windows
artifacts are produced only by `.github/workflows/build-packages.yml` at J.

This prevents two workflows from emitting different packages with the same
product/version identity.

---

# P1 — unify release packaging before package VM acceptance

P1 is source work and must land before X1/G/H. Do not postpone these packaging
changes to Phase J: G5/G6/H6 must validate the same builders that later create
the release candidate.

## P1.1 One release artifact workflow

The sole release-artifact workflow fixed at P1 is:

~~~text
.github/workflows/build-packages.yml
~~~

Remove the release-package build/upload lane from:

~~~text
.github/workflows/desktop-ui.yml
~~~

Desktop UI CI may upload only test logs, JUnit, screenshots and coverage.

Every package job prints:

~~~text
Kikimora full source SHA
VERSION
Leshy requested ref
Leshy resolved full SHA
Qt version
~~~

and artifact checksums are retained with the workflow artifact.

## P1.2 Linux integrated release mode

Canonical builder stays:

~~~text
packaging/linux/build-release.sh
    -> packaging/linux/stage-release.sh
~~~

Add environment contract:

~~~text
KIKIMORA_REQUIRE_UI=1
~~~

Behavior:

~~~text
unset/0:
    preserve headless console/runtime staging for its independent contract

1:
    KIKIMORA_UI_BIN must name an executable
    missing UI is fatal
    stage UI + desktop entry + XDG autostart + icon
~~~

When UI is staged, the generated Debian Depends must be:

~~~text
existing runtime dependencies
+ libqt6core6t64 | libqt6core6
+ libqt6gui6t64 | libqt6gui6
+ libqt6network6t64 | libqt6network6
+ libqt6qml6 | libqt6qml6t64
+ libqt6quick6 | libqt6quick6t64
+ libqt6quickcontrols2-6 | libqt6quickcontrols2-6t64
+ libqt6widgets6t64 | libqt6widgets6
~~~

Keep:

~~~text
gnome-shell-extension-appindicator
~~~

as Recommends.

The UI-present package description must state that the desktop client is
included. The UI-absent package keeps the console/runtime description.

Keep the current headless contract:

~~~text
linux/tests/test_release_packaging.sh
~~~

Add:

~~~text
linux/tests/test_integrated_release_packaging.sh
~~~

The integrated test consumes already-built `.deb`, `.tar.gz` and
`SHA256SUMS`; it must not rebuild.

It verifies both archives contain:

~~~text
/usr/bin/kikimora-ui
/usr/local/bin/kikimora-core
/usr/local/bin/kikimora-toad
/usr/local/bin/leshy
/usr/share/applications/kikimora.desktop
/etc/xdg/autostart/kikimora.desktop
/usr/share/icons/hicolor/256x256/apps/kikimora.png
~~~

and verifies the exact Qt Depends set above plus matching archive checksums.

The Linux job in build-packages.yml builds the UI first and invokes the
canonical builder with:

~~~text
KIKIMORA_UI_BIN=<that exact binary>
KIKIMORA_REQUIRE_UI=1
KIKIMORA_LESHY_SRC=<exact pinned checkout>
~~~

Then it runs both headless-source contract and integrated-artifact contract.

## P1.3 Windows integrated release mode

Canonical builder remains:

~~~text
packaging/windows/stage.ps1
~~~

The Build packages release lane always supplies:

~~~text
-UiExe
-QtDir
-LeshyDir
-WintunDll
~~~

and never `-AllowMissingWintun`.

Keep:

~~~text
packaging/windows/test_package.ps1
~~~

as source/static contract.

Add:

~~~text
packaging/windows/test_release_payload.ps1
~~~

Parameters:

~~~powershell
-OutDir <already-built dist>
-RequireUi
-RequireLeshy
-RequireWintun
~~~

It inspects, without rebuilding, the produced MSI/zip/manifest/checksums and
requires:

~~~text
core + toad + wintun
Leshy runtime/service payload
kikimora-ui.exe
Qt runtime + QML + styles + qwindows + qoffscreen
MSVC runtime payload
UI shortcuts/autostart installer rows
KikimoraCore service rows
Leshy -> KikimoraCore dependency
manifest entries for mandatory components
checksums for every portable payload file
MSI File/Component/Feature rows for mandatory release components
~~~

A "UI payload regression skipped" path is forbidden in
`test_release_payload.ps1`.

## P1.4 P1 gate

On one exact source SHA require:

~~~text
headless Linux package contract GREEN
integrated Linux artifact contract GREEN
Windows source/static package contract GREEN
Windows integrated artifact contract GREEN
Build packages workflow GREEN
Desktop UI workflow emits no release-named package
~~~

G5/G6/H6 start only after this packaging architecture is fixed.

Suggested commit:

~~~text
fix(packaging): unify integrated desktop release artifacts
~~~

---

# T1 — version all privileged acceptance harnesses before G/H

T1 is test-source work only, but it still changes the repository SHA. Therefore
every G/H harness must be committed before X1. G/H execution itself does not
create or edit acceptance scripts.

Required Linux files by the end of T1:

~~~text
linux/tests/toad/local-leshy-routing-netns.sh
linux/tests/toad/managed-leshy-routing-netns.sh
linux/tests/toad/managed-reboot-matrix-vm.sh
linux/tests/toad/managed-package-lifecycle-vm.sh
linux/tests/toad/mode-transition-crash-vm.sh       # existing, retained
~~~

Required Windows files by the end of T1:

~~~text
packaging/windows/test_managed_cutover.ps1          # existing, corrected
packaging/windows/test_state_layout_upgrade.ps1
packaging/windows/test_managed_leshy.ps1
packaging/windows/test_mode_transition_crash.ps1    # existing, retained
packaging/windows/test_managed_reboot.ps1
packaging/windows/test_managed_package_lifecycle.ps1
packaging/windows/test_release_payload.ps1
~~~

Their exact runtime contracts are fixed by:

~~~text
core remediation packet R1-R8 + §13.3
docs/roadmap.md §§15-16
docs/toad-steps/08d1-managed-selected-routes-completion.md §§20-22
~~~

No ad-hoc `/tmp/g1.sh`, one-off PowerShell session or uncommitted VM helper is
accepted once T1 lands.

## T1.1 Static/parser gates

Linux:

~~~text
bash -n <every tracked *.sh above>
shellcheck <every tracked *.sh above>
python3 linux/tests/toad/fixtures/test_dns_fixtures.py
~~~

Windows CI parses every required PowerShell script before VM use and runs every
non-privileged contract mode available from it. A syntax/parser failure is a
T1 failure, not a VM discovery.

All harnesses must:

- have deterministic argument parsing;
- reject missing mandatory binary/artifact paths before mutation;
- use one ERR/finally cleanup path;
- preserve the original failure exit code;
- print source/binary hashes at start;
- mutate only their named fixture namespace/adapters/files/routes;
- produce a failure evidence directory rather than deleting diagnostics.

## T1.2 No source edits during acceptance

After X1 starts, G/H scripts are immutable acceptance inputs.

If a G/H run exposes a harness bug:

~~~text
stop the current acceptance claim
fix the versioned harness
commit it
rerun T1 static gates
rerun X1
restart the owning G/H phase from the corrected exact SHA
~~~

Do not patch a copy on the VM and continue counting rows as accepted.

Suggested commits:

~~~text
test(linux): version managed routing vm acceptance harnesses
test(windows): version managed routing vm acceptance harnesses
~~~

---

# X1 — combined-head revalidation after U1-U7, P1 and T1

C8 initially re-accepts Phase F before desktop work. U5 then changes core
startup/config-path and diagnostics plumbing, P1 changes packaging/workflow
sources, and T1 adds the final versioned acceptance harnesses. Therefore the C8
exact-head proof must not be carried forward blindly.

After T1, freeze one exact combined SHA and rerun:

~~~text
git diff --check

cd toad
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race ./...

focused kikimora_test_failpoints Phase-F tests
release-absence proof
DNS fixture self-test
existing denial smoke
Windows cross-build
Windows package contract
Linux package contract
ShellCheck
~~~

Then require both workflows on that same SHA:

~~~text
Toad core: GREEN
Desktop UI: GREEN
Build packages: GREEN when triggered by the core/UI changes
ShellCheck: GREEN
~~~

U5-specific gates are mandatory:

~~~text
runtimePathsOverrideAllFourConfigSurfaces
fileBridgeAndGeneratedRouteTargetsSharePublicationDir
getRoutingListsUsesInjectedLeshyConfig
diagnosticsHostSourcesFalseSkipsAmbientCollectors
hermeticServeCreatesNothingOutsideTempRoot
~~~

X1 does not replace the native G/H VM gates. It proves that the combined
post-UI core has not invalidated Phase-F/nonprivileged contracts before
privileged testing starts.

If any Phase-F invariant regresses at X1:

~~~text
Phase F status -> REOPENED
fix the owning C/U stage
rerun X1 from the new exact SHA
~~~

Only the X1-green SHA may be deployed as the starting source for G1/H1.

---

# 15. G1-G6 — Linux core vertical on the combined head

Start only after C8, U7, P1, T1 and X1 are green on one exact source head.

Use SHA-scoped test artifacts:

~~~text
/tmp/kikimora-acceptance/<FULL_SHA>/
~~~

Never use the stale 739e1ab binaries copied into /usr/local/sbin during the
exploratory b751134 attempt.

## 15.1 G1

Use the exact versioned harness contract from remediation §13.3:

~~~text
linux/tests/toad/local-leshy-routing-netns.sh
~~~

Mandatory eight rows include 1000 A + 1000 AAAA in G1 itself.

## 15.2 G2

Use:

~~~text
linux/tests/toad/managed-leshy-routing-netns.sh
~~~

All parent §20 assertions are mandatory.

## 15.3 G3 exact existing harness

Do not invent a new crash harness.

Use:

~~~text
linux/tests/toad/mode-transition-crash-vm.sh
~~~

Run every Enable/Disable durable journal boundary required by the production
plan against the exact current binary.

Record case-by-case exit and restart result.

## 15.4 G4 reboot harness — missing plan now fixed

Add:

~~~text
linux/tests/toad/managed-reboot-matrix-vm.sh
~~~

CLI:

~~~text
managed-reboot-matrix-vm.sh
    --case <prepared|config-written|marker-written|generation-committed|selected-active|park-applying|park-retiring>
    --phase <prepare|verify>
    --core <absolute binary>
    --leshy <absolute binary>
    --work-dir <absolute persistent dir>
~~~

prepare phase:

- clears only its own prior case state;
- stages one exact durable boundary using the existing test failpoint/barrier
  mechanisms;
- verifies the expected on-disk journal/checkpoint before returning;
- writes <work-dir>/reboot-case.json with source SHA, case and expected state;
- returns 0 only when it is safe for the host to reboot.

Host then reboots ubuntu2604.

verify phase after SSH returns:

- reads the exact case file;
- starts/observes packaged service ordering as required by the case;
- requires core recovery before Leshy availability;
- validates generation/marker/journal/selected/Park result;
- compares unrelated route/rule snapshot;
- deletes only its own case marker on success.

No one-shot script may call reboot and then pretend to verify its own post-boot
state.

## 15.5 G5/G6 package harness — missing plan now fixed

Add:

~~~text
linux/tests/toad/managed-package-lifecycle-vm.sh
~~~

CLI:

~~~text
--mode fresh
--mode upgrade-local
--mode upgrade-schema1-empty
--mode upgrade-schema2
--mode upgrade-schema1-ambiguous
--candidate <absolute .deb>
--previous <absolute previous .deb>          # upgrade modes
--work-dir <persistent evidence dir>
~~~

Each mode is independent and restores/records its own baseline.

fresh must include install, managed smoke, reboot prepare/verify using G4
mechanism, uninstall and owned-resource removal.

upgrade-schema1-ambiguous must prove abort + previous install restored.

G5/G6 are not accepted from hand-entered apt/dpkg sequences.

---

# 16. H1-H6 — Windows core vertical on the combined head

Resolve win10 address immediately before use.

Use exact source/binary hashes.

## 16.1 H1

Use the pinned Leshy native route-registry acceptance plus current Kikimora
Windows route tests. Do not treat cross-build as native route proof.

## 16.2 H2

Add/run:

~~~text
packaging/windows/test_managed_leshy.ps1
~~~

Use exact Ready-Toad from the combined accepted source head.

## 16.3 H3 exact crash + reboot split

Existing transition crash harness:

~~~text
packaging/windows/test_mode_transition_crash.ps1
~~~

remains the process-crash gate.

Add the separate reboot harness:

~~~text
packaging/windows/test_managed_reboot.ps1
~~~

Parameters:

~~~powershell
-Case prepared|config_written|marker_written|generation_committed|selected_active|park_applying|park_retiring
-Phase Prepare|Verify
-Core <path>
-Leshy <path>
-WorkDir <persistent path>
~~~

Prepare/Verify semantics mirror Linux G4. The host reboots between invocations.

## 16.4 H4/H5

SCM dependency/readiness and auth/ACL checks are explicit rows in Windows
acceptance, not incidental diagnostics.

Extend:

~~~text
packaging/windows/test_managed_cutover.ps1
~~~

for H4/H5 rows that belong to clean cutover, and keep managed-success-specific
rows in test_managed_leshy.ps1.

Ordinary-user auth-key denial must be executed under a real non-admin token,
not inferred from icacls text alone.

## 16.5 H6 package harness — missing plan now fixed

Add:

~~~text
packaging/windows/test_managed_package_lifecycle.ps1
~~~

Modes:

~~~text
FreshMsi
FreshPortable
UpgradeLocal
UpgradeSchema1Empty
UpgradeSchema2
UpgradeSchema1Ambiguous
~~~

The state-layout-specific legacy local/managed/in-flight cases from C1 remain
in:

~~~text
packaging/windows/test_state_layout_upgrade.ps1
~~~

Do not duplicate or weaken them.

H6 exit requires both scripts green.

---

# 17. Phase I — v2-only managed compatibility cut

Run only after G and H are green.

Execute I1-I3 exactly from the production-readiness plan.

The UI ordinary API remains v3. The managed-selected-route v1/v2 capability
cut is between Leshy and the core; nevertheless Desktop UI CI must rerun on the
I commit because it watches toad/** and diagnostics/capability strings can
change.

I3 must rerun managed Linux and Windows smokes.

---

# 18. U8i/U9i/U10/U11 — finish desktop proof before candidate artifacts

These stages may land after I because they do not alter the core networking
state machine.

## 18.1 U8i Linux runner implementation

Add:

~~~text
desktop/tests/guest-run-suites.sh
~~~

Implement the exact installed-Linux runner contract from the UI executor
packet, but do not claim release acceptance from source-tree binaries.

## 18.2 U9i Windows runner implementation

Repair:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

It must aggregate failures/skips and exit nonzero.

Readiness timeout is a failure.

## 18.3 U10 accessibility/geometry/screenshots

Use the exact tests and screenshot names in the UI executor packet.

Add concrete support files:

~~~text
desktop/tests/AccessibilityUiTest.cpp
desktop/tests/VisualStateTest.cpp
desktop/tests/support/UiTestFixture.*
~~~

Register CTest names:

~~~text
ui-accessibility
ui-visual-state
~~~

Pinned Linux visual lane:

- fixed locale ru_RU unless a case explicitly tests untranslated English;
- Basic style;
- software renderer;
- fixed 360x720 reference size for golden images;
- additional geometry-only 340x620 and 480x800 cases;
- DPR 2 run is semantic/geometry only unless its rendering is stable enough to
  version independently.

Store reference images under:

~~~text
desktop/tests/golden/linux-qt6.8.3/
~~~

Do not mix golden images from different Qt versions.

## 18.4 U11 coverage exact files

Add:

~~~text
desktop/cmake/Coverage.cmake
desktop/tests/run-coverage.sh
~~~

Coverage build uses:

~~~text
-DKIKIMORA_ENABLE_COVERAGE=ON
~~~

and gcovr thresholds already fixed by the UI executor packet.

The runner invokes the same required-suite shell wrapper; coverage is never
collected from a reduced test set.

---

# 19. Phase J — exact release candidate artifacts

Execute J1-J5 only after:

~~~text
C1-C8 green
U1-U7 green
P1 + T1 + X1 green
G1-G6 green
H1-H6 green
I1-I3 green
U8i/U9i implemented
U10/U11 green
all current core + desktop CI green
~~~

Candidate artifacts must contain the exact UI and core source being accepted.

J does not redesign or repair packaging. All builder/contract/workflow source
changes belong to P1 and must already have passed G5/G6/H6. During J, allowed
packaging-related source changes are limited to the version/pin/provenance
edits explicitly required by J1-J3 in the production-readiness packet. Any
other packaging source fix sends the branch back to P1 -> X1 -> owning G/H
package gates.

## 19.1 P1 packaging source is frozen

The sole release-artifact workflow is:

~~~text
.github/workflows/build-packages.yml
~~~

`.github/workflows/desktop-ui.yml` may publish test logs/screenshots/coverage
but must not publish a second `kikimora-linux` or release-named package.

Both package jobs print and record:

~~~text
Kikimora full source SHA
VERSION
Leshy ref
resolved Leshy full SHA
Qt version used for the UI
artifact SHA256
~~~

## 19.2 Linux release candidate is the integrated canonical package

The canonical builder remains:

~~~text
packaging/linux/build-release.sh
    -> packaging/linux/stage-release.sh
~~~

J invokes it with:

~~~text
KIKIMORA_UI_BIN=<exact just-built desktop/kikimora-ui>
KIKIMORA_REQUIRE_UI=1
KIKIMORA_LESHY_SRC=<exact pinned Leshy checkout>
~~~

P1 must already have added `KIKIMORA_REQUIRE_UI` handling in `stage-release.sh`. J only uses that contract:

~~~text
unset/0:
    current optional-UI behavior remains for console/runtime contract tests

1:
    missing/non-executable KIKIMORA_UI_BIN is fatal
    staged package must contain UI + desktop entry + autostart + icon
~~~

Keep the existing headless contract:

~~~text
linux/tests/test_release_packaging.sh
~~~

It deliberately unsets KIKIMORA_UI_BIN and proves the runtime package can still
be staged/tested independently.

P1 must already provide the second contract for the artifact uploaded at J:

~~~text
linux/tests/test_integrated_release_packaging.sh
~~~

Input is the already-built `.deb`, `.tar.gz` and `SHA256SUMS`; it does not
silently rebuild a different package.

It must prove both archives contain:

~~~text
/usr/bin/kikimora-ui
/usr/local/bin/kikimora-core
/usr/local/bin/kikimora-toad
/usr/local/bin/leshy
/usr/share/applications/kikimora.desktop
/etc/xdg/autostart/kikimora.desktop
/usr/share/icons/hicolor/256x256/apps/kikimora.png
~~~

and that the `.deb` Depends field contains the runtime dependencies plus this
exact Qt runtime set:

~~~text
libqt6core6t64 | libqt6core6
libqt6gui6t64 | libqt6gui6
libqt6network6t64 | libqt6network6
libqt6qml6 | libqt6qml6t64
libqt6quick6 | libqt6quick6t64
libqt6quickcontrols2-6 | libqt6quickcontrols2-6t64
libqt6widgets6t64 | libqt6widgets6
~~~

`gnome-shell-extension-appindicator` remains a Recommends item, not a hard
dependency.

P1 must already have implemented `stage-release.sh` control-file generation conditionally:

~~~text
UI absent:
    current console/runtime Depends set; no Qt hard dependencies

UI present:
    same runtime Depends + exact Qt hard dependencies above
    product description says desktop client is included
~~~

Remove/replace the stale generated description saying the optional Qt UI is
packaged separately when the UI was actually staged.

The integrated contract also verifies `SHA256SUMS` matches the exact uploaded
archives.

## 19.3 Windows release candidate is one integrated payload

Canonical builder:

~~~text
packaging/windows/stage.ps1
~~~

J always passes:

~~~text
-UiExe <exact MSVC Release kikimora-ui.exe>
-QtDir <same Qt kit used to build UiExe>
-LeshyDir <exact pinned Leshy release payload>
-WintunDll <reviewed/pinned redistributable>
~~~

The release lane may not use `-AllowMissingWintun`.

Keep:

~~~text
packaging/windows/test_package.ps1
~~~

as the source/static packaging unit contract.

P1 must already provide the artifact-level contract:

~~~text
packaging/windows/test_release_payload.ps1
    -OutDir <the already-built J dist directory>
    -RequireUi
    -RequireLeshy
    -RequireWintun
~~~

It inspects the existing MSI/zip rather than restaging. It must require:

~~~text
core + toad + wintun
Leshy service/runtime payload
kikimora-ui.exe
Qt runtime/QML/styles/windows+offscreen platform plugins
MSVC runtime payload
UI shortcuts/autostart installer rows
KikimoraCore service
Leshy -> KikimoraCore dependency
all expected manifest components
checksums for every staged portable payload file
MSI File/Component/Feature rows for mandatory release components
~~~

Any optional-UI skip message is fatal in this release contract.

## 19.4 Required candidate set

~~~text
Linux integrated .deb
Linux integrated .tar.gz
Linux SHA256SUMS
Windows integrated MSI
Windows integrated portable zip
artifact-level packaging contract logs
exact source/pin/version provenance
~~~

U8e installs the Linux integrated `.deb`.
U9e installs the Windows integrated MSI first; portable acceptance is the H6
secondary package mode and does not replace MSI installed-UI proof.

macOS build/staging CI remains useful but does not become native managed-routing
acceptance without a separate native macOS packet.

---

# 20. U8e/U9e — installed UI acceptance against the candidate

Do not run final installed-UI acceptance before J produces immutable candidate
artifacts.

## 20.1 Ubuntu U8e

Install exactly the J candidate artifact.

Run:

~~~text
desktop/tests/guest-run-suites.sh
~~~

against installed:

~~~text
kikimora-ui
kikimora-core
Leshy
real /run/kikimora/core.sock
~~~

Required managed assertions include:

- route authority healthy is rendered correctly;
- during a controlled core restart UI immediately becomes non-actionable;
- stale Protected is never retained;
- reconnect requires new compatible handshake + authoritative snapshot;
- family readiness visible;
- selected route diagnostics visible;
- tray follows the same safety policy;
- designated role connect/disconnect restores initial desired state;
- autostart restored to initial state.

Any inability to restore initial lab state is failure.

## 20.2 Windows U9e

Install exactly the J MSI/portable candidate.

Run repaired:

~~~text
desktop/tests/guest-run-suites.ps1
~~~

against installed AF_UNIX core.

Additionally prove real Windows production backend selection:

~~~text
backend=real-core
socket=C:/ProgramData/Kikimora/core.sock
~~~

No --fake flag in installed acceptance.

Verify ordinary desktop user can use non-privileged API while privileged mode/
route secrets remain inaccessible.

## 20.3 UI release evidence

U8e/U9e require:

~~~text
runner exit 0
mandatory skips 0
unexpected warnings 0
candidate package hash exact
source SHA exact
initial state restored YES
~~~

---

# 21. U12 — desktop evidence/status closure

After U8e and U9e:

Update:

~~~text
docs/desktop-ui-core-audit-2026-11-10.md
docs/desktop-ui-test-execution-and-coverage-plan.md
docs/desktop-ui-executor-coverage-plan-2026-11-11.md
docs/ui-coverage-matrix.md
docs/toad-steps/08d1-execution-log.md
~~~

Do not rewrite historical findings as if they never existed.

Append a final acceptance record containing:

- exact Kikimora SHA;
- exact Leshy SHA/tag;
- Desktop UI workflow run ID;
- all OS job results;
- skip/warning counts;
- coverage numbers;
- Ubuntu candidate hash + runner result;
- Windows candidate hash + runner result;
- screenshots/evidence artifact names.

Only then mark UI/UI↔core accepted.

---

# 22. Phase K — real Linux workstation canary

Execute K1-K8 from the production-readiness plan after:

~~~text
candidate artifacts fixed
core Linux/Windows VM verticals green
installed Ubuntu/Windows UI candidate gates green
~~~

The real workstation canary is primarily a core/network release gate.
Do not run test-failpoint binaries there.

UI is not allowed to mutate the workstation automatically for canary setup.
Use production core/CLI operations and observe UI only if desired.

Dead-man rollback remains mandatory.

---

# 23. Phase L — final release

Release only when all rows below are green on the same candidate lineage.

~~~text
Core remediation C1-C8
Desktop U1-U7
Linux G1-G6
Windows H1-H6
v2-only I1-I3
Desktop U8i/U9i/U10/U11
candidate J1-J5
installed UI U8e/U9e/U12
real-host K1-K8
~~~

Then execute L1-L5.

No source change after canary and before tag except documentation/evidence that
does not alter produced artifacts. If source/artifact bytes change, return to
the owning acceptance phase.

---

# 24. Unified test ownership matrix

| Surface | Deterministic unit/model | Hermetic process/IPC | Privileged/installed |
|---|---|---|---|
| core state/config/API | Go unit + race | core test process | G/H |
| selected route/Park transactions | Go failpoint + fakes | tagged process seams | G2/H2 + crash/reboot |
| local Leshy route registry | Leshy unit | pinned native netns | G1/H1 |
| mode transaction | Go crash matrix | process failpoint harness | G3/H3 + reboot |
| package cutover/upgrade | package contract | — | G5/G6/H6 |
| RealCoreClient parser | Qt Layer A scripted server | — | installed UI |
| RoleListModel | Qt Layer A | — | indirectly |
| QML behavior/safety | FakeCore Layer B | real local core Layer C | U8e/U9e |
| tray | FakeCore menu test | real local core tray-live | U8e/U9e |
| accessibility/geometry | Layer B | — | smoke/evidence |
| managed authority lost UI | Layer A + FakeCore Layer B | not forged in Layer C | U8e/U9e managed scenario |
| source coverage | Linux gcovr | same required suites | — |

No layer substitutes for the one to its right.

---

# 25. Gate after every source stage

Before every commit:

~~~text
git diff --check
~~~

When Go changed:

~~~text
cd toad
gofmt -w <changed Go files>
go vet ./...
go test -count=1 ./...
~~~

Use focused tagged/privileged gates additionally when the owning stage requires
them.

When desktop changed:

~~~text
cmake -S desktop -B build/desktop-ui -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop-ui --parallel

QT_QPA_PLATFORM=offscreen \
QT_QUICK_BACKEND=software \
ctest --test-dir build/desktop-ui --output-on-failure -V
~~~

After U6, the required-suite wrapper is the authoritative desktop gate, not
bare CTest.

When core API/snapshot changed after U7:

~~~text
Desktop UI workflow is mandatory
~~~

because it watches toad/**.

A stage is not complete until its current exact-head required gates have
actually run.

---

# 26. Exact commit slices

Core remediation keeps the R packet's commit sequence.

Then use:

~~~text
fix(desktop): map current core authority and safety policy
test(desktop): align FakeCore with current core schema
fix(desktop): gate QML and tray mutations on backend safety policy
test(desktop): share production QML bootstrap and fail on warnings
test(control): expose hermetic runtime paths for desktop live-core tests
test(desktop): own real core fixtures in mandatory live suites
test(desktop): fail required suites on skips and warnings
ci(desktop): gate UI-core contract on all supported build OSes

fix(packaging): unify integrated desktop release artifacts
test(linux): version standalone leshy and managed reboot/package gates
test(windows): version managed routing vm acceptance harnesses

test(desktop): add installed Linux UI gate
test(desktop): make installed Windows UI gate fail closed
test(desktop): add accessibility and deterministic visual gates
ci(desktop): enforce native UI coverage

docs(release): record exact core and desktop acceptance evidence
~~~

Do not combine all core + UI remediation in one commit.

---

# 27. Explicit deferred/non-goals

Not part of this remediation/release path:

- redesigning the two-page UI into historical five tabs;
- new secret-editing tunnel settings API;
- silently editing protocol credentials from QML;
- advertising native macOS managed-routing acceptance without a new native
  vertical packet;
- VPN-over-VPN chains;
- using FakeCore as Windows production fallback;
- weakening exact route ownership for test convenience;
- treating CI success in unrelated jobs as replacement for a red mandatory
  job;
- using ad-hoc /tmp scripts as final VM acceptance.

The existing deferred transport-settings API remains a separate future feature.

---

# 28. Executor stop rules

The executor does not ask for architecture decisions.

Stop only when:

1. an exact external VM/dependency is unavailable and repository work cannot
   make the gate runnable;
2. a required native platform cannot be reached;
3. current source makes an invariant in this roadmap impossible.

For case 3:

- record the exact source contradiction;
- do not invent a substitute architecture;
- stop before weakening the invariant.

Ordinary compile/test failures are implementation work, not stop conditions.

---

# 29. Final completion definition

Kikimora current release line is complete only when one exact candidate lineage
has all of:

1. Windows legacy state migration proven.
2. Recovery barrier/test trigger/Ready-Toad/DNS fixtures corrected.
3. Phase F exact-head CI green.
4. UI consumes API v3 and all safety-relevant snapshot truth.
5. One C++ safety policy gates QML and tray.
6. Mandatory UI tests have zero skips/warnings.
7. Hermetic UI↔real-core tests cannot touch product filesystem state.
8. Desktop workflow runs on every core/API change.
9. P1 integrated Linux/Windows package contracts are green.
10. T1 contains every versioned privileged/reboot/package harness.
11. X1 is green on the exact combined pre-VM SHA.
12. Linux G1-G6 green.
13. Windows H1-H6 green.
14. v2-only managed compatibility cut green.
15. Accessibility/visual/source-coverage gates green.
16. Exact candidate packages built and hashed.
17. Installed Ubuntu UI gate green and state restored.
18. Installed Windows UI gate green and state restored.
19. Real Linux workstation canary green with rollback protection.
20. Exact evidence recorded.
21. Final source/tag/artifacts match the accepted bytes.

Only then proceed to publication.
