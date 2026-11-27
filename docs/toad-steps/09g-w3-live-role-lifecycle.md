# 09G-W3 — Canonical import and live role lifecycle

Status: **EXECUTION PLAN**

Closes: JRN-04, JRN-05, JRN-06, JRN-07, JRN-10, JRN-11, JRN-20,
JRN-22, JRN-24, JRN-28 and the live-role prerequisite for JRN-29.

Depends on: W2 stable role ids and command-result model.

## Fixed decisions

- The Go core is the only protocol import authority.
- Native/share imports are parsed by toad/internal/profileimport.Parse.
- OpenConnect is a structured create wizard, not a fake https:// import.
- config.TunnelSpec remains API compatibility input but is not the canonical
  desktop import representation.
- A successful create is live immediately; no core restart.
- A successful remove is live immediately; no core restart.
- New roles are always created desired=false. Adding configuration never
  silently connects a VPN.
- Role configuration lives under /etc/kikimora/toads; mutable per-role state
  lives under /var/lib/kikimora/toads/<role>; secrets live under
  /etc/kikimora/secrets.
- Durable role TOML publication is the create linearization point. If the
  process crashes after publication but before the response, restart loads the
  role as committed and desired=false.
- Durable role TOML removal is the delete linearization point, but only after
  live resources have been proven drained.
- Managed schema2 marker extension stays add-only. A rolled-back create may
  leave an inert managed-zone tombstone; this is already an allowed marker
  contract.

## Lock/order rule

Every live role composition mutation uses:

~~~text
policyMu
  -> short m.mu sections
  -> Controller internal lock
  -> SelectedRouteManager internal lock
~~~

Never hold m.mu while writing files, reloading Leshy, waiting for a Toad
process, or flushing selected routes. Never call a public method that reacquires
policyMu while already holding it; add private ...PolicyLocked helpers.

## Commit W3.1 — explicit role-state/secret paths and full Config writer

Modify toad/internal/control/runtime_paths.go.

Extend RuntimePaths with RoleStateRoot and SecretsDir. Add Manager fields
roleStateRoot and secretsDir plus helpers:

~~~go
func (m *Manager) roleStateDirectory(name string) string
func (m *Manager) secretsDirectory() string
~~~

Platform defaults are exact and must be derived in platform-specific code,
not by embedding Unix paths in shared logic:

~~~text
Linux:
  RoleStateRoot = /var/lib/kikimora/toads
  SecretsDir    = /etc/kikimora/secrets

Windows:
  RoleStateRoot = C:\ProgramData\Kikimora\state\toads
  SecretsDir    = C:\ProgramData\Kikimora\secrets
~~~

Add serve flags --role-state-dir and --secrets-dir in the normal
toad/cmd/kikimora-core serve parser. Pass them through RuntimePaths.

Modify linux/files/kikimora-core.service ExecStart to include:

~~~text
--role-state-dir /var/lib/kikimora/toads
--secrets-dir /etc/kikimora/secrets
~~~

Change Linux StateDirectory to create both persistent roots:

~~~ini
StateDirectory=kikimora/core kikimora/toads
StateDirectoryMode=0750
~~~

For Windows, extend toad/cmd/kikimora-core/service_windows.go::serviceOptions:
set RoleStateRoot to filepath.Join(windowsProgramDataRoot,"state","toads") and
SecretsDir to filepath.Join(windowsProgramDataRoot,"secrets").

Extend both Windows installers:

- packaging/windows/kikimora.wxs:
  - add state\toads directory below DATAROOTSTATE;
  - add DATAROOTSECRETS below DATAROOT;
  - both get SYSTEM + Administrators only;
  - include them in the deferred HardenDataDirs/icacls action.
- packaging/windows/install.ps1:
  - create state\toads and secrets;
  - include both in the hardened directory loop.

Extend packaging/windows/test_package.ps1 and service_windows_test.go to assert
those exact paths and ACL/layout rows.

In toad/internal/config/create.go factor durable persistence out of
CreateTunnel:

~~~go
func WriteTunnelConfig(dir string, cfg *Config) (string, error)
~~~

Contract: cfg non-nil; Validate; name valid; destination absent; encode the full
Config; atomic durable 0600 write; read back through Load; durably remove on
verification failure.

Keep CreateTunnel(dir, TunnelSpec) only as compatibility:
spec.toConfig -> legacy normalization -> WriteTunnelConfig.

Export the existing address allocator as:

~~~go
func AssignTunnelAddress(dir string) string
~~~

Tests must prove full AWG and VLESS fields survive roundtrip and no create helper
derives /etc/kikimora/state.

## Commit W3.2 — dynamic Controller role registration

Modify toad/internal/core/controller.go and snapshot.go.

Add:

~~~go
func (c *Controller) AddRole(spec RoleSpec) error
func (c *Controller) RemoveRole(id string) error
~~~

AddRole under c.mu rejects empty/duplicate id, inserts spec and
RoleRuntime{State:RoleStopped, Desired:false}, then bumps revision.

RemoveRole under c.mu rejects unknown id and Desired=true, removes role/spec,
then bumps revision.

Do not recreate Controller or restart Controller.Run.

## Commit W3.3 — dynamic SelectedRouteManager role-zone registration

Modify toad/internal/control/selected_routes.go.

Add:

~~~go
func (m *SelectedRouteManager) RegisterRole(role, zone string) error
func (m *SelectedRouteManager) UnregisterRole(role, zone string) error
~~~

RegisterRole: nonempty role; empty zone is not indexed; nonempty zone must not
belong to another role; add exactly one owner.

UnregisterRole: require inFlight==0, no restorePending, no integrity quarantine;
remove mapping and per-role selected state.

Manager must prove route/publication cleanup before unregistering.

## Commit W3.4 — canonical import request and Config construction

In toad/internal/control/api.go add:

~~~go
type ImportTunnelRequest struct {
    Raw     string `json:"raw"`
    Name    string `json:"name"`
    Profile string `json:"profile"`
}

type OpenConnectTunnelRequest struct {
    Name       string `json:"name"`
    Profile    string `json:"profile"`
    Gateway    string `json:"gateway"`
    Username   string `json:"username"`
    AuthGroup  string `json:"auth_group,omitempty"`
    AuthMethod string `json:"auth_method"` // password | token
    Secret     string `json:"secret"`
}
~~~

Add Request fields exactly:

~~~go
Import      *ImportTunnelRequest      `json:"import,omitempty"`
OpenConnect *OpenConnectTunnelRequest `json:"openconnect,omitempty"`
~~~

Add methods ImportTunnel and CreateOpenConnectTunnel. Never log Raw or Secret.
Add an API JSON decode test that sends literal snake_case objects and asserts
AuthGroup/AuthMethod and every single-word field arrive unchanged.

Add toad/internal/control/role_create.go with:

~~~go
func (m *Manager) buildImportedConfig(req ImportTunnelRequest) (*config.Config, error)
func (m *Manager) buildOpenConnectConfig(req OpenConnectTunnelRequest) (*config.Config, func() error, error)
~~~

Export config.ValidateInstanceName and config.TunnelInterface instead of copying
private regex/naming logic.

buildImportedConfig must:

1. require explicit valid Name;
2. require nonempty existing Profile;
3. invoke profileimport.Parse with explicit Name, product interface, MTU 1380,
   and m.roleStateDirectory(name);
4. for VLESS replace the importer fallback address with
   config.AssignTunnelAddress(m.configDir);
5. preserve native/provider AWG addresses;
6. membership: default -> cfg.Profiles=nil; custom -> []string{profile};
7. overwrite StateDir/interface/LeshyZone with product-owned values;
8. Validate.

No down-conversion through TunnelSpec.

## Commit W3.5 — core-owned OpenConnect secret files

Add toad/internal/config/secrets.go.

API:

~~~go
type SecretKind string
const (
    SecretPassword SecretKind = "password"
    SecretToken    SecretKind = "token"
)

func WriteRoleSecret(dir, role string, kind SecretKind, value string)
    (path string, cleanup func() error, err error)
~~~

Rules: valid role; nonempty value; directory 0700; filename
<role>.password or <role>.token; file 0600; durable atomic write; no secret in
errors; cleanup removes only the exact created file.

buildOpenConnectConfig requires Name/Profile/Gateway/Username/AuthMethod/Secret.
password populates PasswordFile only; token populates TokenSecretFile only.
Normalize StateDir/interface/MTU/LeshyZone/membership exactly like import.

## Commit W3.6 — one live add transaction

Add:

~~~go
func (m *Manager) ImportTunnel(ctx context.Context, req ImportTunnelRequest) (string, error)
func (m *Manager) CreateOpenConnectTunnel(ctx context.Context, req OpenConnectTunnelRequest) (string, error)
func (m *Manager) addRoleConfigLive(ctx context.Context, cfg *config.Config,
    cleanupSecret func() error) (string, error)
~~~

Both public methods build Config then call addRoleConfigLive.

Before implementing live CRUD, make the existing desired-composition lock
contract true for bulk actions as well:

- `ConnectAll` acquires `policyMu` before its desired-state mutation and
  releases it immediately after the durable desired commit, before starting any
  Toad process;
- `DisconnectAll` acquires `policyMu` before its desired-state mutation and
  releases it immediately after the durable desired commit, before stopping or
  releasing any Toad process.

This makes create/remove/profile/membership/default and all connect/disconnect
intent commits serialize on the same policy lock. Do not hold `policyMu` while
waiting for a process start/stop.

Add once in `toad/internal/control/desired_state.go` and reuse from W4:

```go
func (m *Manager) saveDesiredSnapshot(
    ctx context.Context,
    desired PersistedDesiredState,
) error
```

Implementation:
1. under `m.mu` copy only `m.desiredStore`, then unlock;
2. if the store is nil, return nil;
3. call `store.Save(ctx, desired)` with no Manager lock held;
4. reacquire `m.mu` only to update `desiredStateStatus`;
5. never change activeProfile or role flags in this helper.

Preflight while policyMu is held and m.mu only briefly:

- configDir exists;
- role name not loaded;
- TOML destination absent;
- target profile exists;
- new managed zone is nonempty/unique when managed mode is active;
- ValidateEndpointPolicies(existing + cfg) must reject a new conflict. Legacy
  boot may warn on old conflicts, but new UI configuration must not add one.

Then:

1. WriteTunnelConfig — durable create linearization point.
2. Under m.mu add Manager role with enabled=false, rebuild profile map, and
   capture `desired := m.desiredStateLocked()`; release m.mu.
3. Controller.AddRole with RoleSpec.
4. SelectedRouteManager.RegisterRole.
5. call `saveDesiredSnapshot(ctx, desired)` with no m.mu held. This explicitly
   overwrites any stale same-name desired entry to false.
6. If managed:
   - refactor extendManagedMarkerForCurrentRoles into a public wrapper plus
     private extendManagedMarkerForCurrentRolesPolicyLocked(state) that assumes
     policyMu;
   - under m.mu copy state := m.startupCommitted, then release m.mu;
   - require state.Managed && state.Marker != nil;
   - extend the marker using that exact committed descriptor;
   - the helper returns the updated descriptor carrying the unioned ManagedZones;
   - under m.mu assign m.startupCommitted = extendedState;
   - only after marker proof set managedSelectedRoles[name]=true.
7. Regenerate generated Leshy config and reload Leshy through existing policy
   helpers.
8. bump Manager revision.
9. return success. Do not start the Toad.

Rollback closure after TOML publication:

1. managedSelectedRoles[name]=false;
2. unregister selected-route role if registered;
3. Controller.RemoveRole;
4. under m.mu remove Manager role, rebuild profiles and capture the restored
   desired snapshot; release m.mu;
5. call `saveDesiredSnapshot(ctx, restoredDesired)` without m.mu held;
6. durably remove TOML;
7. cleanup newly-created secret;
8. regenerate/reload old Leshy policy if changed.

Marker is not shrunk. If rollback cannot be proven, quarantine policy authority
with reason "live role create rollback is unproved: ...".

Crash after TOML write is intentionally safe: restart loads the role as stopped
and existing startup marker extension authorizes it before readiness.

## Commit W3.7 — live remove transaction

Replace current disk-only Manager.RemoveTunnel with implementation in new
toad/internal/control/role_remove.go.

Preconditions under policyMu + short m.mu:

- loaded role exists; remove disk-only special case;
- enabled=false;
- process==nil and startInFlight=false;
- role is not current bypass owner;
- no validation/recovery operation in flight;
- set removalPending=true before releasing m.mu.

If blocked: no disk mutation.

Before config deletion prove cleanup and close the selected-route admission
race. The role is stopped, but Leshy may still have a concurrent selected-route
request in flight; a one-time zero-in-flight observation is not a fence.

1. resolve the role's effective zone before changing any registration;
2. if the role is managed by SelectedRouteManager:
   - call `selectedRoutes.Freeze(ctx, name)`; this sets frozen=true first,
     denies new Ensure calls, and waits for the existing in-flight count to
     reach zero;
   - call the existing package-private
     `selectedRoutes.flushZoneForRole(ctx, name, zone)`, not public
     `FlushZone`: the role is deliberately frozen, and Manager already owns
     the exact role/zone identity;
   - verify the returned registry snapshot for the role is empty and the role
     state has zero in-flight work;
   - call `SelectedRouteManager.UnregisterRole(name, zone)` while the role is
     still frozen.
3. retire any remaining ordinary publication/parking evidence through the same
   canonical release helpers used by disconnect; do not delete kernel rows or
   checkpoint files directly.
4. if any step before durable TOML deletion fails:
   - if UnregisterRole already succeeded, call
     `RegisterRole(name, zone)` to restore the live index;
   - call `Unfreeze(name)` when the role was frozen;
   - clear removalPending;
   - return the error;
   - TOML and Manager/Controller role remain untouched.

Only after this fence+drain+unregister precondition succeeds:

1. call `config.RemoveTunnel` — **delete linearization point**;
2. call `Controller.RemoveRole`; because desired=false was proven before the
   commit this must succeed. If it does not, treat it as an internal
   consistency failure, keep the role absent from disk, quarantine policy
   authority, and continue cleanup; never recreate the TOML.
3. under m.mu delete Manager role and all per-role backoff/recovery/start maps,
   delete managedSelectedRoles[name], rebuild profile map and bump revision.
   A new role is false by default when absent from desired.json, so do not
   perform a new desired-store write merely to add/remove a false map entry.
4. regenerate/reload Leshy policy.
5. delete role routing-list file if present.
6. delete exact core-owned <role>.password and <role>.token secrets.

After TOML deletion, ancillary cleanup failure must never resurrect the role.
Return success with warning message and diagnostics warning for non-safety
orphans. The selected-route admission fence/drain remains a hard pre-delete
gate. Do not Unfreeze an unregistered/deleted role.

Crash after TOML deletion is safe: restart cannot reload the role.

## Commit W3.8 — API handlers and field errors

Add handlers ImportTunnel and CreateOpenConnectTunnel.

Success response contains message plus authoritative current snapshot.

Validation field_errors keys are restricted to:
name, profile, raw, gateway, username, auth_method, secret.

Never echo raw input or secret value.

Upgrade old CreateTunnel handler to call the same live add path so compatibility
tests cannot retain restart-only semantics. Upgrade RemoveTunnel message to
remove all restart wording and include current snapshot.



### Handshake capabilities for W3 semantics

In `toad/internal/control/api.go` define and append these exact capability
strings from `handshakeCapabilities()` when this implementation is present:

~~~go
const (
    CapabilityLiveRoleCRUDV1       = "live-role-crud-v1"
    CapabilityCanonicalImportV1    = "canonical-import-v1"
    CapabilityOpenConnectCreateV1  = "openconnect-create-v1"
)
~~~

Also include method capability strings `ImportTunnel` and
`CreateOpenConnectTunnel`.

Keep the current API version unchanged. Old `CreateTunnel` capability alone
does **not** imply live CRUD because 1.4.0 already advertises that method with
restart-only semantics.

Add once in `desktop/src/core/CoreBackend.h`:

~~~cpp
Q_INVOKABLE bool hasCapability(const QString &capability) const
{
    return coreCapabilities().contains(capability);
}
~~~

This is a non-virtual convenience over the existing virtual
`coreCapabilities()`, so FakeCore and RealCore do not implement duplicate
lookup logic. Add a small CoreBackend/RealCoreClient test for present/absent
capabilities.

ProfileDrawer enables the new import/create UI only when the corresponding
semantic capability is present. RemoveTunnel live UI requires
`live-role-crud-v1`.

## Commit W3.9 — Desktop uses only core import/create APIs

CoreBackend becomes:

~~~cpp
Q_INVOKABLE virtual QString importTunnel(const QString &raw,
                                         const QString &name,
                                         const QString &profile) = 0;
Q_INVOKABLE virtual QString createOpenConnectTunnel(const QVariantMap &form) = 0;
~~~

Remove describeTunnelImport and desktop protocol authority. Delete
desktop/src/core/TunnelImport.cpp/.h from CMake when no caller remains.

ProfileDrawer has two explicit modes.

Import mode:
- required roleNameField;
- multiline importField;
- target profileName shown read-only;
- Add -> Core.importTunnel(raw,name,profileName).

OpenConnect mode:
- name;
- gateway;
- username;
- optional auth group;
- password/token method;
- password-echo secret field;
- Add -> createOpenConnectTunnel.

No bare URL is treated as a complete tunnel.

In RealCoreClient::commandClass add ImportTunnel and CreateOpenConnectTunnel as
CommandClass::Mutate. CreateTunnel/RemoveTunnel stay Mutate. No new product
mutation may fall through to the default Channel class.

Use W2 request ids. Close only after success and after
Core.roles.getById(name) exists with requested membership.

## Commit W3.10 — remove restart-only assumptions

Update CoreBackend comments and KRoleDetailDrawer text.

Rename RealCoreMatrixTest::createTunnelWritesConfiguration to
createTunnelIsLive. Require immediate live role and no "restart" response.

UiBehavior create flow and CoreSemanticContract must use the same expectation.

TestFreshInstallCanCreateProfiles must require live snapshot role immediately
with desired=false.

Remove the RealCoreClient membership merge fallback for roles absent from
running snapshot: after W3 every persisted role is live.

## Mandatory W3 gates

Go:

- canonical import corpus;
- full AWG/VLESS field preservation;
- live create desired=false;
- create into active/inactive custom profile;
- duplicate name/zone rejects before mutation;
- concurrent ConnectAll/DisconnectAll versus live create/remove serializes at
  policyMu and never loses/reenables the created or removed role;
- desired-store/reload failure rollback;
- managed marker extension and selected-route authorization;
- restart after durable create yields stopped committed role;
- live remove;
- connected/default-owner remove rejection;
- selected cleanup failure leaves TOML;
- an Ensure already in flight before remove is drained; an Ensure arriving
  after Freeze is denied; neither can survive past the TOML delete;
- failed pre-delete unregister is rolled back with RegisterRole+Unfreeze and
  leaves the TOML/live role intact;
- restart after remove never resurrects role;
- all old R8/PROD profile/bypass tests remain green.

Qt:

- W0 FakeCore == RealCore semantic contract;
- native AWG with obfuscation values;
- VLESS with fp/type/spx;
- structured OpenConnect;
- explicit name + profile membership;
- no service restart in create/remove journey.

W3 is done only when the same running core can add/import/remove a role and the
next authoritative snapshot reflects it.
