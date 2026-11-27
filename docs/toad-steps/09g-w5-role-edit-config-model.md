# 09G-W5 — Editable role configuration with stable technical identity

Status: **EXECUTION PLAN**

Closes: JRN-14, JRN-25 and the role-maintenance part of JRN-16.

Depends on: W2 stable role identity/result model, W3 live lifecycle, W4 profile
policy/default actions.

## Fixed architecture

- `Config.Name` remains the immutable technical role id.
- User-visible rename is a separate editable `display_name`.
- Protocol is immutable for a role. Changing protocol means create a new role
  and remove the old one.
- Immutable technical identity after create:
  - Name/id;
  - Interface;
  - StateDir;
  - LeshyZone;
  - EndpointPolicy zone/rule priority/source;
  - profile membership (edited only by profile APIs).
- Editable connection configuration:
  - display name;
  - address list;
  - MTU;
  - protocol-specific endpoint and safe fields;
  - credential replacement fields.
- Secrets/private credentials are never returned in snapshots. The snapshot says
  only whether a secret/credential is configured.
- Editing a stopped role changes config live with no process restart.
- Editing a connected role forces the existing fail-closed recovery sequence,
  but with a **new forced Toad replacement step** that reloads the new TOML.
  Ordinary RestartTransport is insufficient because it may keep the old
  in-memory configuration.
- If reconfiguration fails, restore exact old config/secret files and run the
  same forced reload back to the old configuration. If that rollback cannot be
  proven, keep the role fail-closed/quarantined and return a hard error.

## Commit W5.1 — add stable display name to Config

Modify `toad/internal/config/config.go`:

~~~go
type Config struct {
    Name        string `toml:"name"`
    DisplayName string `toml:"display_name,omitempty"`
    ...
}
~~~

Add:

~~~go
func ValidateDisplayName(value string) error
func (c *Config) EffectiveDisplayName() string
~~~

Validation:

- empty DisplayName is valid and means Name;
- otherwise trim must equal original;
- 1..80 Unicode code points;
- reject Unicode control characters, CR/LF/TAB;
- no uniqueness requirement.

`EffectiveDisplayName` returns DisplayName when nonempty, otherwise Name.

Existing configs remain valid with no migration.

In `snapshotRoleLocked`:
- `ID = cfg.Name`;
- `Label = cfg.EffectiveDisplayName()`.

Every control/API command continues to address role ID, never Label.

Add tests proving two roles may have the same display name while IDs remain
unambiguous.

## Commit W5.2 — non-secret configured-role projection in core snapshot

Add types in `toad/internal/control/control.go`:

~~~go
type ConfiguredAWGSnapshot struct {
    Endpoint             string
    PeerPublicKey        string
    PresharedKeySet      bool
    PrivateKeySet        bool
    AllowedIPs           []string
    PersistentKeepalive  int
    JC, JMin, JMax       int
    S1, S2, S3, S4       int
    H1, H2, H3, H4       string
    I1, I2, I3, I4, I5   string
}

type ConfiguredVLESSSnapshot struct {
    Endpoint       string
    UUIDSet        bool
    ServerName     string
    PublicKey      string
    ShortID        string
    Flow           string
    Fingerprint    string
    Transport      string
    SpiderX        string
}

type ConfiguredOpenConnectSnapshot struct {
    Gateway          string
    Username         string
    AuthGroup        string
    AuthMethod       string // password | token
    SecretConfigured bool
    VPNProtocol      string
    UserAgent        string
    ServerCert       string
    DisableUDP       bool
    DisableIPv6      bool
    HttpAuth         string
    ReconnectTimeout int
}

type ConfiguredRoleSnapshot struct {
    DisplayName string
    Interface   string
    Address     []string
    MTU         int
    AWG         *ConfiguredAWGSnapshot
    VLESS       *ConfiguredVLESSSnapshot
    OpenConnect *ConfiguredOpenConnectSnapshot
}
~~~

Add to RoleSnapshot:

~~~go
Configured ConfiguredRoleSnapshot `json:"configured"`
~~~

Populate directly from cached `r.cfg` in `snapshotRoleLocked`.

Never expose:
- AWG private key;
- AWG preshared key;
- VLESS UUID;
- OpenConnect password/token contents;
- secret file contents.

`configured.endpoint` is config truth. Existing `endpoint/session/interface`
remain observed runtime truth.

Add JSON snapshot tests asserting secret literal fixtures never occur in encoded
snapshot bytes.

Extend `subscriptionFingerprint` in the same commit. For every role marshal
`role.Configured` with `encoding/json` and append that JSON string to the
material fingerprint. ConfiguredRoleSnapshot contains only deterministic
struct fields/slices and no secret values.

Add a test where only display name/configured endpoint/MTU changes while runtime
state is unchanged; each change must produce a different fingerprint.

## Commit W5.3 — Qt model separates configured and runtime truth

Modify:

- `desktop/src/models/RoleListModel.h/.cpp`
- `desktop/src/core/RealCoreClient.cpp`.

Extend RoleSnapshot with the configured projection. Represent protocol-specific nested configured fields as one `QVariantMap configuredProtocol`; do not create parallel per-protocol C++ structs. The required top-level fields are explicit:

~~~cpp
QString displayName;
QString configuredInterface;
QStringList configuredAddresses;
int configuredMtu;
QVariantMap configuredProtocol;
~~~

Keep:
- runtime interfaceName/index/mtu;
- sessionEndpoint;
- configuredEndpoints/liveEndpoints.

Set:
- label = configured display name;
- server shown in normal settings from configured endpoint/protocol data;
- sessionEndpoint remains separate.

Update RoleListModel role names/get().

Tests:
- stopped role still shows configured server/interface/address/MTU;
- Ready role shows both configured and live endpoint separately;
- display label may differ from stable roleId.

## Commit W5.4 — explicit role patch API

Add to `toad/internal/control/api.go`:

~~~go
type AWGRolePatch struct {
    Endpoint            *string   `json:"endpoint,omitempty"`
    PeerPublicKey       *string   `json:"peer_public_key,omitempty"`
    PrivateKey          *string   `json:"private_key,omitempty"`
    PresharedKey        *string   `json:"preshared_key,omitempty"`
    ClearPresharedKey   *bool     `json:"clear_preshared_key,omitempty"`
    AllowedIPs          *[]string `json:"allowed_ips,omitempty"`
    PersistentKeepalive *int      `json:"persistent_keepalive,omitempty"`
    JC                  *int      `json:"jc,omitempty"`
    JMin                *int      `json:"jmin,omitempty"`
    JMax                *int      `json:"jmax,omitempty"`
    S1                  *int      `json:"s1,omitempty"`
    S2                  *int      `json:"s2,omitempty"`
    S3                  *int      `json:"s3,omitempty"`
    S4                  *int      `json:"s4,omitempty"`
    H1                  *string   `json:"h1,omitempty"`
    H2                  *string   `json:"h2,omitempty"`
    H3                  *string   `json:"h3,omitempty"`
    H4                  *string   `json:"h4,omitempty"`
    I1                  *string   `json:"i1,omitempty"`
    I2                  *string   `json:"i2,omitempty"`
    I3                  *string   `json:"i3,omitempty"`
    I4                  *string   `json:"i4,omitempty"`
    I5                  *string   `json:"i5,omitempty"`
}

type VLESSRolePatch struct {
    Endpoint    *string `json:"endpoint,omitempty"`
    UUID        *string `json:"uuid,omitempty"`
    ServerName  *string `json:"server_name,omitempty"`
    PublicKey   *string `json:"public_key,omitempty"`
    ShortID     *string `json:"short_id,omitempty"`
    Flow        *string `json:"flow,omitempty"`
    Fingerprint *string `json:"fingerprint,omitempty"`
    Transport   *string `json:"transport,omitempty"`
    SpiderX     *string `json:"spider_x,omitempty"`
}

type OpenConnectRolePatch struct {
    Gateway          *string `json:"gateway,omitempty"`
    Username         *string `json:"username,omitempty"`
    AuthGroup        *string `json:"auth_group,omitempty"`
    AuthMethod       *string `json:"auth_method,omitempty"`
    Secret           *string `json:"secret,omitempty"`
    VPNProtocol      *string `json:"vpn_protocol,omitempty"`
    UserAgent        *string `json:"user_agent,omitempty"`
    ServerCert       *string `json:"server_cert,omitempty"`
    DisableUDP       *bool   `json:"disable_udp,omitempty"`
    DisableIPv6      *bool   `json:"disable_ipv6,omitempty"`
    HttpAuth         *string `json:"http_auth,omitempty"`
    ReconnectTimeout *int    `json:"reconnect_timeout,omitempty"`
}

type RolePatch struct {
    Role        string                    `json:"role"`
    DisplayName *string                   `json:"display_name,omitempty"`
    Address     *[]string                 `json:"address,omitempty"`
    MTU         *int                      `json:"mtu,omitempty"`
    AWG         *AWGRolePatch             `json:"awg,omitempty"`
    VLESS       *VLESSRolePatch           `json:"vless,omitempty"`
    OpenConnect *OpenConnectRolePatch     `json:"openconnect,omitempty"`
}
~~~

These tags define the wire format. Qt sends these exact snake_case keys; do not
rely on encoding/json's case-insensitive field-name matching. Add a literal JSON
decode test covering every multiword key before implementing RealCoreClient
serialization.

Add Request field:

~~~go
RolePatch *RolePatch `json:"role_patch,omitempty"`
~~~

Add API method `UpdateRole`.

Exactly one protocol patch may be non-nil and it must match the role's current
protocol.

Blank credential pointer means keep existing credential. A non-nil credential
pointer replaces it. OpenConnect Secret is never persisted in TOML; it updates
the core-owned secret file from W3.

Do not permit RolePatch to change Name, Interface, StateDir, LeshyZone,
EndpointPolicy or Profiles.

## Commit W5.5 — pure patch/validation builder

Create `toad/internal/control/role_edit.go`.

First add the one canonical deep-copy helper in
`toad/internal/config/config.go`:

~~~go
func (c *Config) Clone() *Config
~~~

Clone is a manual structural copy; do not use TOML/JSON encode-decode as a copy
mechanism. Exact copy rules:

1. nil receiver -> nil;
2. start with `out := *c` so unexported `legacyVPNPath` is preserved;
3. `out.Address = slices.Clone(c.Address)`;
4. `out.Profiles = slices.Clone(c.Profiles)`;
5. when AWG2 != nil, value-copy it and
   `AllowedIPs = slices.Clone(c.AWG2.AllowedIPs)`;
6. when VLESS != nil, value-copy it; when VLESS.XHTTP != nil, value-copy XHTTP
   separately;
7. when OpenConnect != nil, value-copy it;
8. return &out.

Add a config-package test that mutates every cloned slice/nested pointer and
proves the original Config is unchanged.

Then add:

~~~go
func applyRolePatch(old *config.Config, patch RolePatch) (*config.Config, error)
~~~

Implementation:

1. use `candidate := old.Clone()`; never mutate the live pointer during
   validation;
2. require patch.Role == old.Name;
3. apply DisplayName/Address/MTU;
4. apply matching protocol patch;
5. preserve immutable identity fields byte-for-value;
6. for secret replacement:
   - AWG/VLESS inline credential is applied only to the private candidate config;
   - OpenConnect replacement is staged separately; do not write it in this
     pure function;
7. ValidateDisplayName + Config.Validate;
8. ValidateEndpointPolicies with the candidate substituted into the full current
   role set;
9. return candidate.

Validation field errors use W2/W3 field-error model. Field keys are stable:
`display_name`, `address`, `mtu`, and protocol paths such as
`awg.endpoint`, `vless.server_name`, `openconnect.gateway`.

No secret value appears in error strings.

## Commit W5.6 — forced config-reload recovery action

Ordinary `RestartTransport` may leave a Toad process running with its old
loaded TOML. Add a distinct recovery action that **always replaces the process**.

### core recovery model

Modify:

- `toad/internal/core/reconciler.go`
- `toad/internal/core/recovery.go`
- `toad/internal/core/recovery_runner.go`
- `toad/internal/core/engine.go`.

Add:

~~~go
const ActionReloadConfig RecoveryAction = "reload-config"
const RecoveryReloadConfig RecoveryStep = "reload-config"
~~~

Extend RecoveryDriver:

~~~go
ReloadConfig(context.Context, string) error
~~~

Add:

~~~go
func (e Engine) RecoverAction(
    ctx context.Context,
    role string,
    operation, epoch uint64,
    action RecoveryAction,
) error
~~~

Refactor existing `Recover` to select action then call the common internal
implementation used by RecoverAction.

Sequences for ActionReloadConfig:

Legacy:

~~~text
ObserveRoutes
Park
Withdraw
Quiesce
ApplyEndpoint
ReloadConfig
Validate
Publish
ResyncLeshy
ObserveRestore
~~~

Managed:

~~~text
FreezeSelected
ObserveRoutes
Park
WithdrawSelected
Withdraw
Quiesce
ApplyEndpoint
ReloadConfig
Validate
RestoreSelected
ObserveRestore
Publish
ResyncLeshy
UnfreezeSelected
~~~

Do not use RecoveryStartTransport in this action.

### control recovery driver

Implement `recoveryDriver.ReloadConfig` by calling a new Manager helper:

~~~go
func (m *Manager) replaceRoleProcessForConfigReload(
    ctx context.Context,
    name string,
) error
~~~

It:

1. under m.mu captures current Process/control socket, verifies role enabled,
   sets process=nil/state invalid/streamed=false/validationPendingEpoch=0,
   increments role operation and bumps;
2. unlocks;
3. stops old Toad control/process;
4. calls `startRoleProcess(ctx,name)` directly so the replacement reads the
   new TOML;
5. after a successful replacement launch returns
   `core.ErrToadRestartPending`.

It must **not** call `ConnectRole` because desired=true is already committed.

Returning ErrToadRestartPending is deliberate and matches the existing full
process-replacement recovery contract: the new process has not yet published an
authoritative Toad generation, so the old recovery worker must not pretend that
Validate/Publish can run synchronously against it.

Add Controller helper:

~~~go
func (c *Controller) BeginRoleReconfigure(role, reason string) (uint64, bool)
~~~

It increments RoleRuntime.Operation, clears validated epoch, sets Recovering and
reason while keeping Desired=true, then returns the new operation token.

### Update every RecoveryDriver implementation immediately

The interface addition must be completed in the same commit. Add
`ReloadConfig(context.Context,string) error` to these exact test doubles:

- `toad/internal/core/engine_test.go` — `recordingDriver`:
  record `RecoveryReloadConfig` through the same call recorder;
- `toad/internal/core/recovery_barrier_testbuild_test.go` —
  `barrierRecordingDriver`: record ReloadConfig;
- same file — `failingDriver`: route ReloadConfig through its existing
  failure/record helper so barrier failure tests can target the new step;
- `toad/internal/control/control_test.go` —
  `stableTransportPendingDriver`, `routeTargetRecoveryDriver`,
  `parkingRetryDriver`, `blockingRecoveryDriver`;
- `toad/internal/control/endpoint_pending_handback_test.go` —
  `endpointPendingDriver`;
- `toad/internal/control/managed_selected_stage_tests_test.go` —
  `managedSequenceDriver`;
- `toad/internal/control/profile_switch_release_test.go` —
  `releaseRecordingDriver`;
- `toad/internal/control/retry_kick_test.go` — `retryKickDriver`.

For control-package doubles not exercising config reload, implement an explicit
no-op `return nil` unless the double already records every recovery call; in
that case record the ReloadConfig step. Do not satisfy the interface by
embedding a generic stub that hides which test drivers were updated.

After editing the interface, run:

~~~bash
git grep -n 'ReleaseResources(' -- toad/internal/core toad/internal/control
~~~

Every concrete RecoveryDriver implementation returned by that inventory must
also have ReloadConfig before the commit is considered complete.

Tests assert the ReloadConfig sequence exactly and prove ReloadConfig is called
where RestartTransport is not.

## Commit W5.7 — transactional Manager.UpdateRole

Add:

~~~go
func (m *Manager) UpdateRole(ctx context.Context, patch RolePatch) error

type roleConfigUpdateState struct {
    Active bool
    Role   string
    Token  uint64
}

roleConfigUpdateSeq uint64
roleConfigUpdate    roleConfigUpdateState
~~~

Add helpers used only while m.mu is held:

~~~go
func (m *Manager) beginRoleConfigUpdateLocked(role string) (uint64, error)
func (m *Manager) roleConfigUpdateCurrentLocked(role string, token uint64) bool
func (m *Manager) finishRoleConfigUpdateLocked(role string, token uint64)
~~~

begin rejects when `roleConfigUpdate.Active` is already true, increments
roleConfigUpdateSeq, and stores exactly one global in-flight update. There is
never more than one connected-role config replacement at a time.

Every profile/default/membership/create/remove/connect/disconnect/retry
mutation, including ConnectAll/DisconnectAll, rejects while
`roleConfigUpdate.Active` is true. UpdateRole itself rejects a second update.

Publish the non-secret technical role id in the control snapshot:

~~~go
RoleConfigUpdate string `json:"role_config_update,omitempty"`
~~~

Empty means no update.

Append `snapshot.RoleConfigUpdate` to `subscriptionFingerprint`; a transition
into or out of config-update state must push even when role runtime state has
not changed yet.

In CoreBackend add:

~~~cpp
Q_PROPERTY(QString roleConfigUpdate READ roleConfigUpdate NOTIFY snapshotChanged)
~~~

RealCoreClient parses the snapshot field; FakeCore exposes the same property for
behavior tests. While nonempty, both backends make `safeToMutate` and
`safeToDisconnect` false and the UI displays
`Применение конфигурации роли <label>…`.

The current local protocol processes one request at a time per connection.
Do not implement a cancellation button that cannot be serviced on the same
connection while UpdateRole is running. The new-config attempt is bounded to
30 seconds and a rollback attempt to another 30 seconds.

Clear roleConfigUpdate only after:
- new config reaches proved Ready; or
- old config rollback reaches proved Ready.

If rollback is unproved, leave roleConfigUpdate active and keep policy authority
quarantined. Further mutations remain blocked until core restart/recovery.

### Preflight

Acquire policyMu. Reject while:
- compositionTransition active;
- bypass transition active;
- role removal pending;
- role unknown;
- role recovery/validation/start transaction currently in flight.

Snapshot:
- old `*config.Config` deep copy;
- exact old TOML bytes/mode via existing policy-file snapshot helper;
- old OpenConnect secret file image when applicable;
- old generated Leshy render policy.

Add `config.RoleSecretPath(dir, role, kind)` in secrets.go so edit code derives
the exact same path as W3 creation. For an existing OpenConnect secret,
UpdateRole snapshots that file and replaces it through the shared durable
atomic write primitive; rollback calls the exact snapshot Restore. Do not use
W3's "cleanup newly-created secret" closure to restore an overwritten
credential.

Build candidate through `applyRolePatch`.

Stage replacement OpenConnect secret with an atomic temp/replace helper, but
retain exact old image for rollback.

### Stopped role

If enabled=false and process=nil:

1. durably replace TOML with candidate through a new
   `config.ReplaceTunnelConfig(path,candidate)` that validates/read-backs and
   preserves path/mode;
2. under m.mu set `r.cfg=candidate`;
3. always regenerate the generated Leshy config and reload Leshy; do not add field-diff branching in the stopped-role path;
4. bump;
5. return success.

On regenerate/reload failure restore exact old TOML/secret, restore r.cfg and
old generated policy, then return error.

### Connected role

1. under m.mu call `beginRoleConfigUpdateLocked(role)`, retain the returned
   global update token, and bump so the subscription immediately closes UI
   mutation gates;
2. durably write candidate TOML/secret;
3. if that durable write fails, under m.mu finish the same update token and
   return without changing `r.cfg`;
4. under m.mu set `r.cfg=candidate`;
5. regenerate generated Leshy config from the candidate while policyMu is held;
6. reload Leshy;
7. if regenerate/reload fails **before process replacement**:
   - restore exact old TOML/secret;
   - restore r.cfg=oldCfg;
   - write/reload the captured old generated Leshy policy;
   - clear roleConfigUpdate with `finishRoleConfigUpdateLocked(role, token)`;
   - return failure without touching the running Toad;
8. record the current product ToadGeneration as `oldGeneration`;
9. call `product.BeginRoleReconfigure(role,"configuration changed")`;
10. capture current underlay epoch and recovery driver;
11. release policyMu before process replacement/wait;
12. call `core.Engine{...}.RecoverAction(..., ActionReloadConfig)`.

Expected result after a successful replacement launch is
`core.ErrToadRestartPending`, not final success. Treat that sentinel as
**pending**, not rollback.

Then wait event-driven on Manager revisions, with an overall 30-second
deadline, for all of:

- product role ToadGeneration != 0 and != oldGeneration;
- role desired still true;
- product state == Ready;
- validation healthy/current;
- for managed selected routes, no restorePending/frozen recovery remains.

Every other policy mutation may acquire policyMu, but after doing so it checks `roleConfigUpdate.Active` and rejects while this connected-role update is active. The UI gates are also closed from the snapshot field.

When Ready is reached, reacquire policyMu, verify `roleConfigUpdateCurrentLocked(role, token)`, clear it with `finishRoleConfigUpdateLocked`, bump and return success.

A direct nil from RecoverAction is accepted only if the driver proves a new
generation and the same Ready predicates already hold; never accept nil merely
because process launch returned.

### Runtime failure rollback

Rollback starts on:
- any RecoverAction error other than ErrToadRestartPending;
- terminal Failed for the new generation;
- 30-second timeout;
- the stored roleConfigUpdate token no longer matches this worker (treated as an internal consistency failure).

Rollback:

1. reacquire policyMu and verify `roleConfigUpdateCurrentLocked(role, token)`;
2. restore exact old TOML and exact old secret image;
3. under m.mu restore `r.cfg=oldCfg`;
4. restore/reload the captured old generated Leshy policy; if this fails,
   quarantine immediately and do not claim rollback;
5. capture the failed/new generation;
6. call BeginRoleReconfigure again for a fresh operation;
7. release policyMu and run ActionReloadConfig against old config;
8. treat ErrToadRestartPending as rollback-pending and wait another bounded
   30 seconds for a generation newer than the failed generation to reach Ready
   with selected-route restoration proven.

If old-config recovery reaches Ready:
- reacquire policyMu;
- clear roleConfigUpdate with `finishRoleConfigUpdateLocked(role, token)`;
- return the original update error;
- role remains on proven old configuration.

If old-config recovery cannot reach Ready:
- keep recovery/parks fail-closed;
- quarantine policy authority with
  `"role configuration rollback is unproved: ..."`;
- do not clear a safety fence merely to unblock UI;
- return joined original+rollback errors.

Never report update success before new configuration has validated on a
connected role.

### Crash semantics

No extra edit journal is required:
- crash before TOML replace -> old config;
- crash after TOML replace -> new config is durable and startup loads it;
- selected-route checkpoint/recovery protects interrupted connected ownership;
- caller may have unknown command outcome but the host remains fail-closed.

## Commit W5.8 — UpdateRole API response and capabilities

Add UpdateRole handler in `api.go`.

Success response includes current authoritative Snapshot.

In `toad/internal/control/api.go` define and append these exact handshake
capabilities:

~~~go
const (
    CapabilityRoleEditV1       = "role-edit-v1"
    CapabilityConfiguredRoleV1 = "configured-role-v1"
)
~~~

Also advertise the method string `UpdateRole`.

Desktop disables Edit controls when `role-edit-v1` is absent. It consumes the
new configured snapshot only when `configured-role-v1` is present and never
sends UpdateRole to an older core.

Add UpdateRole to RealCoreClient::commandClass as CommandClass::Mutate. It must
never fall through to Channel.

Keep the current APIVersion unchanged. The new snapshot fields and UpdateRole method are additive and are negotiated exclusively through handshake capabilities; do not bump the protocol version in W5.

## Commit W5.9 — editable role UI

Modify `KRoleDetailDrawer.qml`.

### Normal Settings section

Show configured values, not runtime values:

- display name;
- protocol (read-only);
- technical role id (small read-only line);
- configured server/endpoint;
- configured interface (read-only);
- configured addresses;
- MTU.

Add `Редактировать` button when `role-edit-v1` capability exists.

### Edit mode

Maintain `configDraft` separate from live role data.

Common fields:
- display name;
- addresses;
- MTU.

Protocol-specific fields:

**AWG**
- endpoint;
- peer public key;
- allowed IPs;
- persistent keepalive;
- Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5;
- "replace private key" password field, empty means keep;
- "replace preshared key" password field + explicit clear checkbox.

**VLESS**
- endpoint;
- server name;
- public key;
- short id;
- flow;
- fingerprint;
- transport;
- SpiderX;
- "replace UUID" password field, empty means keep.

**OpenConnect**
- gateway;
- username;
- auth group;
- VPN protocol;
- user agent;
- server cert;
- Disable UDP/IPv6;
- http auth;
- reconnect timeout;
- auth method;
- "replace password/token" password field, empty means keep.

Do not prefill hidden credentials.

Save:
- builds only changed RolePatch fields;
- returns W2 request id;
- form stays open/pending until matching result;
- fieldErrors attach to fields;
- success clears configDirty and refreshes from authoritative snapshot;
- failure leaves draft intact.

Add `configDirty` to RoleDrawer composite dirty close guard from W2.

### Runtime diagnostics

Keep separate diagnostics rows:
- current session endpoint;
- runtime interface/index/MTU;
- handshake/traffic;
- recovery state.

Do not label runtime endpoint "configured server".

## W5 mandatory tests

Go:
- DisplayName validation and legacy fallback;
- snapshot redacts every secret fixture;
- stopped role update;
- connected role update invokes ActionReloadConfig exact sequence;
- new Toad process reads changed TOML;
- update failure restores old config and old working role;
- rollback failure leaves fail-closed recovery/quarantine;
- default-owner update keeps protected selected-route coverage;
- OpenConnect secret replacement never enters response/log;
- AWG private/preshared and VLESS UUID never enter snapshot;
- immutable fields cannot be patched;
- concurrent profile/default/remove mutation is serialized/rejected.

Qt:
- stopped role shows configured server before first connect;
- configured vs session endpoint are distinct;
- edit display name changes Label but roleId remains stable;
- drawer remains bound to same id through update/restart;
- credential fields are blank/replace-only;
- failed Save keeps draft and shows field error;
- dirty close guard includes configDraft.

W5 is done only when ordinary role maintenance no longer requires TOML editing.
A connected/default VPN update must validate the new config; on failure it
restores the old proven config without losing fail-closed routing.
