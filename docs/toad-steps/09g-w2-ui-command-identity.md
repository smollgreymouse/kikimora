# 09G-W2 — Stable role identity, live drawer binding and operation-scoped command results

Status: **EXECUTION PLAN**

Closes: JRN-08, JRN-15, JRN-16, JRN-23, JRN-26, JRN-27 and prepares
JRN-20/JRN-29 for W3/W4.

Depends on: W1.

## Fixed decisions

- Every UI mutation addresses a role by stable role id/name, never a model row.
- Remove client-side optimistic profile/membership mutation. The local IPC is
  fast; UI waits for command result + authoritative snapshot.
- One generic request-scoped completion signal replaces special-case
  `profileCreationFinished` and normal UI use of global
  `lastCommandMessage/lastCommandError`.
- Keep global last-command strings for diagnostics only.
- Use one canonical top-level `RoleDrawer`; remove the inline editable role
  detail from `ProfileDrawer/KRoleList`.
- A drawer stores only `roleId`; live role data is re-resolved from the model
  on every core revision. Draft form state is separate and is never overwritten
  by a snapshot while dirty.

## Commit W2.1 — stable-id RoleListModel surface

### Files

- `desktop/src/models/RoleListModel.h`
- `desktop/src/models/RoleListModel.cpp`
- `desktop/tests/RoleListModelTest.cpp` (or the existing role-list model test file)

### Add

```cpp
Q_INVOKABLE QVariantMap getById(const QString &id) const;
Q_INVOKABLE int rowForId(const QString &id) const;
```

Identity source is `RoleSnapshot::id`, not label.

`getById` returns an empty map when absent.

Add tests:

- lookup first/middle/last;
- absent id;
- reorder model and verify id returns the same role;
- insert/remove before target and verify target lookup remains correct.

Do not expose a persistent QModelIndex to QML.

## Commit W2.2 — change CoreBackend role mutation signatures

### Files

- `desktop/src/core/CoreBackend.h`
- `desktop/src/core/RealCoreClient.h/.cpp`
- `desktop/src/core/FakeCoreBackend.h/.cpp`
- all compile failures in tests/QML callers.

Replace row-based methods:

```cpp
roleAction(int row)
roleActionByName(int row, action)
retryRole(int row)
validateRole(int row)
rediscoverEndpoints(int row)
setRoleIpv6(int row, bool)
```

with:

```cpp
Q_INVOKABLE virtual QString roleAction(const QString &roleId) = 0;
Q_INVOKABLE virtual QString roleActionByName(const QString &roleId,
                                             const QString &action) = 0;
Q_INVOKABLE virtual QString retryRole(const QString &roleId) = 0;
Q_INVOKABLE virtual QString validateRole(const QString &roleId) = 0;
Q_INVOKABLE virtual QString rediscoverEndpoints(const QString &roleId) = 0;
Q_INVOKABLE virtual QString setRoleIpv6(const QString &roleId, bool enabled) = 0;
```

RealCoreClient resolves the current RoleSnapshot with `getById`/internal id
lookup only to validate available actions. The request body always carries the
provided stable id.

No public mutation accepts a row after this commit.

## Commit W2.3 — generic command completion protocol in Qt

### CoreBackend API

Change mutating invokables to return request id `QString`:

- `connectAll`
- `disconnectAll`
- `toggleAll`
- role actions above
- `setActiveProfile`
- `createTunnel` temporarily, until W3 replaces it
- `createProfile`
- `setTunnelProfiles`
- `importTunnel` temporarily
- `removeTunnel`
- `setBypassMode`
- `saveRoutingList`
- `setRoleIpv6`
- `setDiagnosticsCapture`.

Read-only refresh methods remain `void`; only user-visible mutating operations return request IDs.

Add signal:

```cpp
void commandFinished(const QString &requestId,
                     const QString &method,
                     bool ok,
                     const QString &message,
                     const QVariantMap &fieldErrors);
```

Remove `profileCreationFinished` after all callers are migrated.

### RealCoreClient request handling

In `RealCoreClient.h`:

```cpp
struct PendingCommand {
    QString method;
};
QHash<QString, PendingCommand> m_pendingCommands;

QString sendMutation(const QString &method,
                     const QJsonObject &params = {});
void sendReadRequest(const QString &method,
                     const QJsonObject &params = {},
                     const QString &id = {});
void sendRequestFrame(const QString &method,
                      const QJsonObject &params,
                      const QString &id);
QString blockedMutation(const QString &method, const QString &reason);
```

`sendMutation` is used **only** for CommandClass::Mutate,
CommandClass::Disconnect and CommandClass::DiagnosticsMutate:

1. determine commandClass and assert/fail locally if it is DiagnosticsRead or
   Channel;
2. allocate a fresh UUID;
3. if class is blocked:
   - do not send a frame;
   - emit `commandFinished(id, method, false, reason, {})`;
   - set diagnostics global error;
   - return id;
4. store `m_pendingCommands[id] = {method}`;
5. call sendRequestFrame;
6. return id.

`sendReadRequest` is used for GetRoutingLists/GetDiagnosticsSnapshot and other
read protocol traffic. It applies the read gate but never inserts a
PendingCommand and never emits commandFinished.

Handshake/subscribe/channel frames keep their existing explicit IDs and use
sendRequestFrame/sendMessage directly.

Do not reuse fixed ids such as `createprofile` for mutations. Fixed ids remain
only for internal protocol/read requests.

### Response processing

Refactor `RealCoreClient::processIncomingMessage` into this exact ordering for
every decoded object:

1. read `id`;
2. if this is the handshake response, validate API range and update
   capabilities/connection state as today;
3. if `snapshot` is an object, call `handleSnapshot` **before any mutation
   completion signal**;
4. if `id` exists in `m_pendingCommands`:
   - read boolean `ok`;
   - read `message`, string/object `error`, optional `field_errors`;
   - update global diagnostic message/error as secondary state only;
   - erase the pending entry;
   - emit exactly one `commandFinished` for that id;
5. subscribe/read responses not present in `m_pendingCommands` never emit
   commandFinished.

The snapshot-before-completion order is mandatory. A form that receives
successful completion must already be able to resolve the newly authoritative
profile/role/config state from Core.

On disconnect, schedule a queued failed completion for every pending command
with `"core disconnected before command completed"`, then clear the map.

Do not infer command success from a later subscription snapshot; the matching
response is still the success/failure authority.

### Baseline subscription fingerprint repair

Modify `toad/internal/control/api.go::subscriptionFingerprint` in W2 because
the UI already renders fields the current fingerprint omits.

For every role append these material fields in addition to the existing state:

~~~go
role.ID
role.Label
strings.Join(role.Profiles, ",")
role.State
role.Reason
strings.Join(role.AvailableActions, ",")
strconv.FormatBool(role.DesiredEnabled)
role.Endpoint.State
strconv.FormatBool(role.Publication.Published)
strconv.FormatBool(role.Parking.Active)
strconv.FormatBool(role.LeshyIPv6)
~~~

Keep RX/TX counters and handshake age excluded.

Add a Go test that starts from two snapshots with the same state/reason but
changes, one at a time:
- Label;
- Profiles;
- AvailableActions;
- DesiredEnabled;
- LeshyIPv6.

Each material change must produce a different fingerprint.

### Go response extension

In `toad/internal/control/api.go` extend the response type with:

```go
FieldErrors map[string]string `json:"field_errors,omitempty"`
```

No existing command must populate it yet. W3/W5 forms will use it.

### Immediate completion ordering

The invokable must return its request id **before** QML can receive completion.
Add one helper in CoreBackend or each backend:

~~~cpp
void emitCommandFinishedQueued(const QString &id,
                               const QString &method,
                               bool ok,
                               const QString &message,
                               const QVariantMap &fieldErrors)
{
    QMetaObject::invokeMethod(this, [=] {
        emit commandFinished(id, method, ok, message, fieldErrors);
    }, Qt::QueuedConnection);
}
~~~

Use queued completion for:
- locally blocked RealCore mutations that send no frame;
- FakeCore mutations;
- any future validation failure completed entirely inside the Qt client.

Real IPC responses remain naturally asynchronous and emit directly when the
response frame is processed.

This ordering is mandatory because QML uses:

~~~qml
pendingRequestId = Core.someMutation(...)
~~~

A synchronous signal would arrive before `pendingRequestId` contains the
returned id and would be lost by the operation-local handler.

### FakeCoreBackend

Use a monotonically increasing id:

~~~text
fake-1
fake-2
...
~~~

Every mutation applies its FakeCore state synchronously, then schedules exactly
one queued `commandFinished` through the helper above. The semantic contract
matches RealCore and QML always owns the returned id before completion arrives.

## Commit W2.4 — remove RealCore optimistic profile/membership caches

### RealCoreClient

Delete:

- `m_pendingProfileName`
- `m_pendingProfileSwitch`
- `m_profileSwitchFallback`
- `m_pendingTunnelProfilesRole`
- `m_pendingTunnelProfilesFallback`
- special response branches that roll them back.

`setActiveProfile(name)` sends only the request. `m_activeProfile` changes
only in `handleSnapshot/updateModelFromSnapshot`.

`setTunnelProfiles(role, profiles)` sends only the request. `m_roleProfiles`
changes only from authoritative snapshot membership.

After W3 makes role composition fully live, remove the merge-retention fallback
at `RealCoreClient.cpp` around the current comment
"snapshot carries the membership of the running composition only". Until W3,
leave that merge in place so W2 does not silently erase staged disk-only roles.

### Tests

Update existing profile rejection tests to assert:

- selector/model remains authoritative while request pending;
- failed command emits local result;
- no optimistic value appears then rolls back.

## Commit W2.5 — canonical RoleDrawer and live role binding

### QML topology

Modify:

- `desktop/qml/Main.qml`
- `desktop/qml/Drawers/RoleDrawer.qml`
- `desktop/qml/Drawers/ProfileDrawer.qml`
- `desktop/qml/Controls/KRoleList.qml`
- `desktop/qml/Controls/KRoleDetailDrawer.qml`.

### RoleDrawer contract

Replace:

```qml
property int roleIndex
property var roleData
signal roleActionRequested(int roleIndex, string action)
```

with:

```qml
property string roleId: ""
readonly property var liveRoleData: {
    Core.revision
    return Core.roles.getById(roleId)
}
signal roleActionRequested(string roleId, string action)
```

Pass `liveRoleData` to KRoleDetailDrawer.

Do not reset section/drafts on every liveRoleData change. Reset only when
`roleId` changes.

### Draft separation

In `KRoleDetailDrawer` keep:

- `draftProfiles`
- route editor text
- future W5 config draft

as explicit local draft state.

On `roleId` change: load drafts from authoritative state.

On live snapshot change:
- update non-draft display fields immediately;
- if a draft is clean, refresh its baseline;
- if dirty, preserve user text/checks until command success/discard.

### Remove inline editable role detail

In `KRoleList.qml`:

- remove inline `KRoleDetailDrawer` instantiation and `expandedRole` behavior;
- role activation always emits stable `roleActivated(roleId)`.

In `ProfileDrawer.qml`:

- selecting a role emits `roleSelected(roleId)`;
- Main handles it by setting `roleDrawer.roleId` and opening canonical drawer.

Delete dead/unreachable `roleMoveRequested` wiring if no remaining sender.

This removes the JRN-23 duplicate action path rather than repairing two copies.

## Commit W2.6 — operation-local UI result state

### Common pattern

Each mutating view owns:

```qml
property string pendingRequestId: ""
property string commandError: ""
property string commandMessage: ""
```

and a `Connections { target: Core; function onCommandFinished(...) }` that
handles only matching request id.

Pending controls are disabled until completion.

### Apply to these exact surfaces

1. Main profile selector:
   - request id from `setActiveProfile`;
   - selector disabled pending;
   - failure text directly below selector/banner;
   - no local active-profile assignment.
2. `KDefaultRouteRow.qml`:
   - pending request from `setBypassMode`;
   - error shown under row;
   - displayed mode remains `Core.bypassMode`.
3. `RoleDrawer/KRoleDetailDrawer`:
   - primary/secondary role action;
   - IPv6 toggle;
   - membership save;
   - routing-list save.
4. Profile create dialog:
   - generic command completion replaces `profileCreationFinished`.
5. Remove dialog:
   - while W3 is not yet live, keep current behavior semantics but dialog closes
     **only** after command success; failure remains visible.
6. Diagnostics capture:
   - local pending/error in Settings.

Do not use `Core.lastCommandError` for normal form feedback after this commit.

## Commit W2.7 — safety gate every role action

In `KRoleDetailDrawer.qml` add:

```qml
function actionAllowed(action) {
    return action === "disconnect" ? Core.safeToDisconnect : Core.safeToMutate
}
```

Apply `enabled: root.actionAllowed(modelData) && root.pendingRequestId === ""`
to every action button, primary and Repeater secondary.

Do not special-case only `actions[0]`.

## Commit W2.8 — composite dirty close policy

In `KRoleDetailDrawer` expose:

```qml
readonly property bool dirty: routeDirty || profilesDirty
```

W5 will add configDirty to this expression.

`RoleDrawer.closePolicy`, Close button, Escape/outside behavior and discard
dialog use `roleDetails.dirty`, not routeDirty.

Discard dialog text names changed sections:

- routes;
- profiles;
- both.

Discard restores both drafts from authoritative current role data.

## W2 tests

### Layer/model

Extend:

- RoleListModel tests with stable-id/reorder cases.
- RealCoreClientTest:
  - request id emitted;
  - completion matched;
  - disconnect fails pending;
  - blocked command emits completion without frame;
  - no optimistic profile/membership state.
- UiBehaviorTest:
  - open role B, reorder/insert/remove before it in FakeCore, command still
    targets B;
  - open drawer, mutate snapshot state/actions, drawer follows B;
  - dirty draft survives live snapshot;
  - ProfileDrawer path opens same RoleDrawer;
  - every secondary action disabled when unsafe;
  - profile draft close warning;
  - rejected profile/default/membership/route action shows local error.
- CoreSemanticContractTest from W0 updated for request result equality.

### Required gate

```bash
cmake --build <build>
ctest --test-dir <build> --output-on-failure
desktop/tests/run-required-suites.sh <existing args>
```

W2 is done only when no QML mutation addresses a role by row index and no normal
form depends on global lastCommandError/lastCommandMessage.
