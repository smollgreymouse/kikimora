# Linux desktop end-to-end user journey audit — 2026-11-27

Status: **AUDIT COMPLETE — executable remediation packet W0-W6 included**

Scope: current `master`, real product path:

1. install package;
2. first desktop launch;
3. create/select profile;
4. add/import a VPN role;
5. role appears in the selected profile;
6. connect/disconnect one role and all roles;
7. default route / selected routing;
8. restart core/UI/host and recover.

This audit deliberately reads the declarative QML and core/control contracts
instead of relying on screenshots. Every confirmed defect is written here as
soon as it is found.

## Confirmed findings so far

### JRN-01 P0 — fresh Linux install has no acceptable current-session authorization flow

Current package model creates `/run/kikimora` as `root:kikimora` and the socket
as `0660 root:kikimora`. That security boundary is sound, but the desktop user
is not guaranteed to have the supplementary group in the already running
desktop session.

Observed real-host failure: UI receives EACCES on `/run/kikimora/core.sock`.
Even after account membership was changed, a persistent `systemd --user`
manager (`Linger=yes`) survived graphical logout/login and kept the old
supplementary group set, so the replacement GNOME/UI processes remained locked
out.

A desktop product must not require reboot/logout as the normal post-install
completion step.

**Fix direction under audit:** preserve the protected socket, but move desktop
authorization away from “mutate Unix group membership and wait for a new login
credential set”. The preferred design is a narrow polkit-mediated current-user
authorization/provisioning path or a per-user runtime access mechanism granted
by a privileged helper during first launch/install. Exact design must be
validated against core peer authorization before implementation.

### JRN-02 P1 — pre-handshake transport failure is mislabeled API incompatibility

`desktop/qml/Main.qml` renders the safety banner from `!Core.apiCompatible`,
although `apiCompatible=false` also means Connecting/Unavailable/no handshake.
`RealCoreClient` correctly sets `coreState="Unavailable"` on socket errors and
`coreState="Incompatible"` only after a real incompatible handshake.

Result: EACCES is shown as “Ядро несовместимо по API”.

**Fix:** classify the banner by `Core.coreState`; reserve “API incompatible”
strictly for `Incompatible`. Map Unix socket access/missing/refused errors to
actionable transport diagnostics.

### JRN-03 P1 — GNOME/Wayland app identity is incomplete

The package installs `kikimora.desktop` and `kikimora.png`, and `main.cpp`
sets a window icon, but the Qt application does not bind its desktop file name.
GNOME/Wayland can therefore show a generic taskbar/overview icon.

**Fix:** set `QGuiApplication::setDesktopFileName("kikimora")` before windows
are created; keep desktop basename, icon name and app-id identical. Add an
installed Wayland app-id acceptance test.

### JRN-04 P0 — desktop tunnel importer diverges from the canonical importer

The canonical Go importer in `toad/internal/profileimport` accepts native
WireGuard/AmneziaWG `[Interface]/[Peer]`, `wg://`, `wireguard://`,
`amneziawg://` and provider Amnezia exports.

The desktop instead has a separate reduced parser in
`desktop/src/core/TunnelImport.cpp`. Input beginning with `[` is routed to the
JSON parser, so valid native AWG config deterministically fails as JSON
(`illegal number`).

**Fix:** do not add yet another Qt parser. Add a core `ImportTunnel` operation
that accepts raw provider/native text and invokes the canonical Go importer.
UI should be presentation/preview only.

### JRN-05 P0 — “Добавить туннель” has no explicit role name

`desktop/qml/Drawers/ProfileDrawer.qml` exposes only a multiline import field,
Paste and Add. There is no role-name field.

Current implicit naming is transport-dependent (VLESS fragment/host,
OpenConnect host, JSON `name`). This is not a usable role-creation UX.

**Fix:** role name is an explicit required field, validated using the same
instance-name rules as the core. Provider-derived labels may be suggestions,
not authoritative product identity.

### JRN-06 P0 — add-role flow drops the profile the operator is editing

`ProfileDrawer` has `property string profileName`, but Add invokes only:

~~~qml
Core.importTunnel(importField.text)
~~~

The current profile is not passed. The backend `TunnelSpec` already supports a
profile field, but the drawer does not populate it.

Worse, list filtering masks this class of error: both `Main.qml` and
`KRoleList.qml` deliberately show a role with unknown/empty membership in
profiles instead of treating missing membership as incomplete state. A role
can therefore appear to belong to the current profile even when the creation
path never persisted that membership.

**Fix:** creation must be atomic `ImportTunnel{raw,name,profile}`. Empty/unknown
membership must not be interpreted as “show everywhere”; snapshot membership
must be authoritative once the core supports live create/reload.

### JRN-07 P0/P1 — create and remove role are not live operations

`CoreBackend.h` explicitly says `createTunnel` only changes configuration on
disk and the running composition picks it up after a **core restart**.
`KRoleDetailDrawer.qml` tells the user that removal also takes effect only
after restart.

This breaks the ordinary desktop journey: Add/Delete is not complete when the
button succeeds. The product exposes a dynamic controller UI but requires a
manual service lifecycle action outside the UI.

**Fix direction:** core must support atomic live create/remove (or a safe
internal reload/recomposition transaction) and publish the resulting role in
the next authoritative snapshot. UI must wait for that result; no manual core
restart should be part of the normal role lifecycle.

### JRN-08 P1 safety — secondary role action buttons do not apply safety gates

In `KRoleDetailDrawer.qml`, the first action button checks:

~~~qml
disconnect ? Core.safeToDisconnect : Core.safeToMutate
~~~

but buttons generated by `root.actions.slice(1)` have no `enabled` safety
condition at all. They directly emit `roleActionRequested`.

The backend may still reject unsafe mutations, but the declarative UI contract
is inconsistent and can expose clickable operations while the UI claims the
channel is fail-closed.

**Fix:** apply action-class gating to every role action via one shared helper;
never special-case only the first action.

### JRN-09 P0 — integrated desktop fresh install intentionally leaves the core stopped

`packaging/linux/stage-release.sh` generates a postinst that creates directories,
reloads systemd, and only starts `kikimora-core.service` when an
`/run/kikimora-package/restart-after-upgrade` marker exists. On a fresh install
there is no `systemctl enable --now kikimora-core.service` and no equivalent
first-run activation path.

This was not accidental: older staging/console acceptance explicitly required
that package postinst **not start/enable or cut over the service**
(`docs/toad-steps/07f1-release-artifact-hardening.md`,
`08a-gate0-evidence.md`, `08a1-side-by-side-installed-staging.md`). That was a
valid side-by-side/headless safety rule. It became the wrong product contract
when the same package became the integrated desktop client with an autostart UI.

Resulting user journey on a genuinely fresh desktop install:

~~~text
apt install kikimora
    -> UI payload exists / may be launched
    -> core service is still stopped/disabled
    -> UI reports Unavailable
    -> user has no normal in-app activation step
~~~

The real-host clean reinstall only worked because the audit script explicitly
ran `systemctl enable --now kikimora-core.service` after package installation.
That manual audit action was not product behavior and masked the release defect.

**Fix:** distinguish package installation safety from desktop first-run
activation. Do not silently start VPN roles on package install, but the desktop
must be able to activate the control plane immediately and safely without a
terminal. Preferred contract:

- package may install the core service disabled/inert;
- first UI launch detects core-not-running distinctly from socket denial;
- UI exposes one explicit privileged “Initialize Kikimora” action via polkit;
- helper enables/starts the core and establishes current-user authorization;
- core starts with all role desired states disabled on a fresh install, so
  starting the control plane does **not** mean connecting VPN;
- subsequent session autostart only launches UI; system service enablement is
  persistent and independently supervised.

That preserves the old no-surprise VPN cutover rule while giving the desktop
product a complete first-run journey.

**Roadmap coverage / regression risk:** 07f1/08a/08a1 positively asserted
“postinst does not start/cut over”. Later desktop packaging gates checked that
the UI/autostart files were present, but did not replace that old invariant
with a first-run desktop activation gate. New gate must test the full
`fresh deb -> launch UI -> authorize/initialize -> core reachable -> zero roles
connected` path.

### JRN-10 P1 — profile creation is live, tunnel creation is not: lifecycle semantics are inconsistent

`CreateProfile` updates the live profile registry and emits a result that the UI
can observe immediately. `SetTunnelProfiles` was explicitly fixed earlier to
apply live without restart (`TestSetTunnelProfilesAppliesWithoutRestart`). In
contrast, `CreateTunnel` still only writes a TOML and `RemoveTunnel` only
deletes a TOML; the running role composition is fixed until core restart.

This creates a non-obvious split in one screen:

- create profile: live;
- move role between profiles: live;
- set default route: live/transactional;
- edit routing list: live/reloaded;
- create role: restart required;
- delete role: restart required.

That inconsistency is a product defect even if every individual API is
internally correct.

**Fix:** introduce one live composition transaction for add/remove role. It
must update disk, manager role map, desired-state store, profile membership,
endpoint/recovery ownership and snapshot as one fenced operation. Reuse the
same crash-journal/transaction discipline already used by profile composition
rather than adding an ad-hoc reload signal.

**Roadmap coverage / regression risk:** the earlier 2026-10-26 audit caught and
fixed exactly this class of bug for `SetTunnelProfiles` but scoped the repair
only to membership. The fresh-install API test even documents “tunnel file
lands on disk too (it loads on the next start)”, effectively locking the
incomplete behavior in. Replace that expectation with a live-create test and
add symmetric live-remove coverage.



### JRN-11 P0/P1 — advertised OpenConnect import path cannot succeed

`ProfileDrawer.qml` tells the operator:

~~~text
Вставьте ... адрес шлюза OpenConnect (https://…)
~~~

and `TunnelImport.cpp` turns a bare URL into:

~~~text
name = oc-<host>
protocol = openconnect
gateway = <url>
~~~

No username, password file, token secret or auth group is collected.

But `config.TunnelSpec.toConfig()` rejects OpenConnect unless a username is
present **and** either `password_file` or `token_secret_file` is present.
The Qt parser even comments “The core still needs a username and a secret file;
it reports that itself.”

So the primary advertised OpenConnect happy path is knowingly incomplete: the
UI accepts input that cannot become a valid role and delegates the missing form
fields to a backend error message.

**Fix:** protocol-specific creation flow after import classification. For
OpenConnect, collect at least username and one authentication method, and store
the secret through a privileged/core secret API rather than asking the user to
manually create a root-owned secret file path. The raw gateway URL is only the
first step, not a complete import.

The preferred unified import API from JRN-04 should return a structured
“needs fields” result (e.g. name/profile/username/auth method) rather than
forcing every protocol into one textarea.

**Roadmap coverage / regression risk:** config tests correctly reject incomplete
OpenConnect specs, while UI tests validated parser classification/presentation
instead of executing the advertised UI input through CreateTunnel. Add an
end-to-end fixture: paste gateway -> required auth fields appear -> submit ->
role is live and connectable. A test that only asserts “OpenConnect URL was
classified” is insufficient.


### JRN-12 P1/P0 UX — “default route inside profile” is actually one global setting and blocks normal profile switching

The Home UI renders a `KDefaultRouteRow` as the last row of **each profile's**
role list. Its choices are the roles visible in that profile, so the presentation
strongly implies a profile-scoped default route.

The core intentionally stores one **global** bypass/default-route owner.
`SetActiveProfile` rejects a target profile when it does not contain that global
owner. The accepted workflow from the 2026-11-16 PROD2 audit is explicitly:

~~~text
set default route -> direct
switch to another profile
choose that profile's default role
~~~

This is fail-closed, but it is not a normal profile UX. A user who configured
profile A to use role A by default cannot simply select profile B; the selector
optimistically moves and then rolls back with an error until the user manually
performs the three-step handover.

**Fix:** move the *configured* default-route preference into profile policy and
make profile switching one protected transaction:

~~~text
profile A: default = role-a
profile B: default = role-b/direct

SetActiveProfile(B):
    preflight B's desired roles/default owner
    protect/drain old catch-all
    commit profile + desired role set + B default owner
    settle roles / apply new catch-all
    publish one authoritative result
~~~

Reuse the existing protected bypass handover and profile-settlement fences; do
not make QML sequence three independent commands. The all-off effective
`direct` behavior can remain a runtime safety state distinct from persisted
per-profile preference.

If a per-profile persistence redesign is deliberately deferred, the UI must at
least expose the dependency *before* selection and offer an atomic
“switch profile and default route” command. Silent optimistic rollback is not
an acceptable happy path.

**Roadmap coverage / regression risk:** PROD2 correctly found the original
safety contradiction and intentionally chose “keep bypass global for this
release; reject incompatible profile switch”. Its real-core matrix therefore
tests that rejection and calls it success. That gate protects safety but also
locks in the bad UX. Replace/extend it with a product journey test that creates
A/B with different default roles and switches A->B in one user action while
proving no unprotected route window.

### JRN-13 P1 — profile management is create-only

The desktop exposes `createProfile`, profile selection and role-membership
editing, but there is no RemoveProfile/DeleteProfile/RenameProfile API and no
corresponding UI. Repository search finds no product implementation for those
operations.

The create dialog also intentionally does **not** switch to the newly created
profile. Therefore the common journey “create profile, then add its first role”
requires a second manual selector action, while there is no later cleanup path
for accidentally created or obsolete profiles.

**Fix:** add transactional `RenameProfile` and `RemoveProfile` operations with
the same policy/bypass invariants as profile membership edits. Removing the
active profile must require/perform an explicit safe target switch; removing a
profile referenced by role memberships must atomically rewrite those
memberships or reject with a clear preview. UI should offer rename/delete in
profile context and, after successful creation, either select the new profile
or present an explicit “Create and open” default action.

**Roadmap coverage / regression risk:** prior profile work concentrated on
atomic membership, rapid switching and creation because those were the
available API methods. End-to-end acceptance never required CRUD completeness.
Add a product-surface inventory gate: every user-created first-class entity
(profile, role) must have an intentional rename/delete story or an explicitly
documented immutable-identity reason.

### JRN-14 P1 — role settings are mostly read-only; endpoint/credentials/name cannot be maintained

`KRoleDetailDrawer.qml` explicitly tells the user:

~~~text
Адрес и параметры протокола пока не редактируются API ядра.
~~~

There is no `UpdateTunnel`/`RenameTunnel` API. The role drawer can edit
profiles, selected routing lists and IPv6 policy, but not the basic connection
identity/configuration that users routinely need to change:

- role name;
- endpoint/gateway;
- VLESS UUID/SNI/public key/short id;
- AWG keys/peer endpoint/obfuscation parameters;
- OpenConnect username/auth group/credential source.

The practical maintenance path is delete/recreate (itself restart-bound today)
or manual TOML editing outside the desktop.

**Fix:** introduce a transactional role-edit API. Separate immutable runtime
identity from editable display name if renaming the on-disk instance is too
dangerous. Protocol-specific editors should update validated configuration
through the core, perform the required live reconfiguration/restart of only
that role, preserve profile/routing policy, and roll back on failure.

Credentials must have a dedicated secret-write/update operation; the UI must
never ask for an arbitrary filesystem password_file as the normal path.

**Roadmap coverage / regression risk:** U5 verified the fields were *visible*
and deliberately accepted protocol fields as read-only. That was useful for
diagnostics, but it was mistaken for a complete settings surface. New UI gates
must distinguish “displayed” from “operator can complete the lifecycle”.

### JRN-15 P1 — role drawer protects unsaved routes but silently drops unsaved profile membership

`KRoleDetailDrawer` tracks two drafts:

- `routeDirty` for the routing-list editor;
- `profilesDirty` for profile membership checkboxes.

But `RoleDrawer.qml` uses only `roleDetails.routeDirty` in `closePolicy`, the
Close button, warning text and discard dialog. Toggling profile membership and
then closing the drawer/outside-clicking can therefore discard the draft with
no warning.

**Fix:** one composite dirty-state contract for the drawer. Closing must account
for every editable section and offer Save / Discard / Cancel with the changed
sections named. Better still, each small mutation may auto-save atomically if
that is the intended desktop model, but mixed “routes require Save, profiles
require Save, only one gets close protection” must go.

**Roadmap coverage / regression risk:** U5 added both editors, but dirty-state
acceptance focused on routing drafts. Add a table-driven UI test for every
editable draft source and every close path (Close, Escape, outside click, role
switch).

### JRN-16 P1 — command results are global/stale instead of operation-scoped; many failures are effectively silent

The client exposes only global `lastCommandMessage` / `lastCommandError`.
`CreateProfile` is the exception: it has a dedicated completion signal.

Current UI consumption is sparse:

- ProfileDrawer displays the global pair under tunnel import;
- route editor displays only global `lastCommandMessage`;
- Settings/Diagnostics display `lastCommandError`;
- profile selector, default-route row, role actions, membership save and role
  removal have no local result surface.

Consequences:

- rejected profile switch optimistically moves then rolls back with no nearby
  explanation;
- rejected default-route selection snaps back with no nearby explanation;
- role action closes the role drawer immediately before the result arrives;
- role removal confirm closes immediately even when core rejects connected or
  default-route-owner removal;
- failed membership save rolls the optimistic cache back but does not explain
  the failure near the checkboxes;
- opening tunnel import after another command may display an unrelated stale
  global message/error.

**Fix:** make commands request-scoped in the UI client. Each mutating call needs
an id plus pending/success/error completion routed to the initiating view.
Controls stay pending until their response or authoritative snapshot confirms
the effect. Destructive dialogs close only on success. Keep a global
diagnostics history separately; do not use one global string as normal UX.

At minimum introduce typed completions for profile switch, bypass change,
role action, membership save, create/import and remove. A small generic
`CommandResult{id, method, ok, message}` model/toast can cover unexpected
errors, but field-level errors should remain attached to their form.

**Roadmap coverage / regression risk:** earlier audits correctly fixed
optimistic rollback for profile/membership state, but tests mostly assert the
final model and `lastCommandError` value. They do not assert that the operator
actually sees the reason at the control they used. New journey gates must
force one rejection for every mutation and verify visible, operation-local
feedback plus no stale message on another screen.

### JRN-17 P1 — Linux autostart toggle lies in the packaged installation

The `.deb` installs an all-users XDG entry:

~~~text
/etc/xdg/autostart/kikimora.desktop
~~~

but `DesktopIntegration` on Unix only reads/writes:

~~~text
~/.config/autostart/kikimora.desktop
~~~

`autostartEnabled()` returns only whether the per-user file exists. Disabling
autostart simply removes that per-user file.

Under XDG autostart semantics, removing the user file does **not** disable the
same system-wide entry. A per-user override with the same basename and
`Hidden=true` is required. Therefore a packaged install can show
“Автозапуск: выключен” while `/etc/xdg/autostart/kikimora.desktop` still
launches Kikimora on the next login.

This combines badly with JRN-09: the next login can autostart a UI whose core
was never initialized/enabled.

**Fix:** model effective XDG autostart, not merely ownership of one user file.
On Linux:

- detect the effective system entry plus user override;
- enable: write/remove the user override so effective entry is enabled;
- disable: write `~/.config/autostart/kikimora.desktop` with `Hidden=true`
  (or stop installing a system-wide autostart entry and make autostart purely
  per-user/first-run controlled);
- report effective state, not `QFile::exists(userPath)`.

For an integrated desktop app, the cleaner contract is probably: package does
**not** force all-users UI autostart; first-run asks/chooses per-user autostart.

**Roadmap coverage / regression risk:** `TrayMenuTest::autostartEntryIsCreatedAndRemoved`
uses a sandboxed user config directory and proves only user-file create/remove.
Packaging tests prove the system XDG entry exists. No gate composes those two
facts. Add an installed-package XDG test with both `/etc/xdg/autostart` and a
user override, including the `Hidden=true` disable case.

### JRN-04 implementation constraint — canonical AWG import must preserve the full Config, not down-convert to TunnelSpec

`profileimport.parseWireGuardINI` preserves Amnezia-specific fields
`Jc/Jmin/Jmax`, `S1-S4`, `H1-H4`, `I1-I5` and PersistentKeepalive.
`config.TunnelSpec` does not contain those fields.

Therefore the correct ImportTunnel core API must persist/activate the validated
canonical `config.Config` produced by `profileimport.Parse`, with explicit
name/profile overrides applied before validation. A design that parses native
AWG in Qt or in Go and then funnels it through the current TunnelSpec loses
provider semantics and is not an acceptable fix.

### JRN-01/JRN-09 coupling — fresh install suppresses even the documented group remediation

The generated Linux postinst prints the `usermod -aG kikimora $USER` hint only
inside:

~~~sh
if [ -d /run/kikimora ] && [ -S /run/kikimora/core.sock ]; then
    ... print remediation ...
fi
~~~

But JRN-09 confirms that a fresh install intentionally does not start the core.
Therefore the socket normally does not exist during fresh-install postinst and
the package does **not even print** the manual remediation that the earlier
install audit accepted as the partial fix for socket access.

This is a concrete cross-stage regression: the headless “fresh install stays
inert” invariant makes the later “postinst tells desktop user how to join the
group” mitigation unreachable on the exact fresh-desktop path it was supposed
to help.

**New gate:** build/install the exact integrated desktop artifact and capture
fresh postinst + first UI launch as one scenario. Never accept independent
source checks of the two branches as evidence for first-run usability.

### JRN-18 P1 — default-route combo offers choices the core guarantees to reject

`KDefaultRouteRow` receives only role labels from the active profile. It has no
role enabled/readiness information and offers every label as a selectable
default route.

The core invariant is stricter: `validateBypassRoleLocked` rejects a role when
`r.enabled == false` with:

~~~text
role <name> is disconnected; connect it before making it the global default route
~~~

So a perfectly normal UI state (two roles in profile, one currently off) shows
the off role as a valid default-route choice even though selecting it cannot
succeed. Combined with JRN-16 the combo simply returns to the previous value
without local explanation.

**Fix:** either filter/disable unavailable role choices with a reason, or make
“make role default” an atomic core operation that can safely connect/preflight
the target and then perform the protected handover. Do not expose a choice
whose precondition is invisible.

**Roadmap coverage / regression risk:** R8B explicitly added
`TestSetBypassModeRejectsDisconnectedRole`, so the backend rejection is well
protected. UI tests only assert that the combo contains profile roles and that
selection drives `setBypassMode`; they do not compose the backend precondition
with the presented options. Add a real-core UI row with one connected and one
disconnected role and verify the user cannot submit an impossible choice.

### JRN-19 P2 — ProfileDrawer empty-profile hint tests global role count, not profile role count

`ProfileDrawer.qml` renders a profile-filtered `KRoleList`, but its own empty
hint uses:

~~~qml
visible: root.roles ? root.roles.rowCount() === 0 : false
~~~

`root.roles` is the global role model. If the selected profile has zero roles
while another profile has roles, the drawer is empty but the text
“В этом профиле пока нет ролей.” is hidden.

The Home page has the correct local `roleList.roleCount` logic; the drawer
duplicates the concept incorrectly.

**Fix:** expose the filtered KRoleList count (or one shared profile-role model)
and base the hint on that. Remove duplicated filtering/count semantics between
Home and ProfileDrawer.

**Roadmap coverage / regression risk:** `profileSwitchThroughThePopup` tests
the Home empty-profile hint, not the ProfileDrawer variant. Add one fixture
with roles elsewhere and an empty target profile, then open ProfileDrawer.

### JRN-20 P1 — role removal UI ignores the preconditions that define real loaded-role removal

The role drawer enables “Удалить роль…” whenever `Core.safeToMutate` is true.
The confirmation text only says the TOML will be deleted and the role will
disappear after a core restart. It does not state or guide the actual core
preconditions:

- connected role: removal rejected until disconnected;
- global default-route owner: removal rejected until default route moves to direct/another role.

The confirm button sends `RemoveTunnel` and immediately closes the dialog, so
the rejection is then subject to the global/silent feedback problem in JRN-16.

**Fix:** once live remove from JRN-07/JRN-10 exists, make removal one
preflighted workflow. The UI should show dependencies before confirmation and
the core should either perform the safe atomic teardown/handover requested by
the operator or return a structured blocker that stays in the dialog. Never
close a destructive dialog before success.

**Roadmap coverage / regression risk:** `RealCoreMatrixTest` currently proves
`CreateTunnel -> RemoveTunnel` by creating a new **disk-only** tunnel and
removing it immediately before any restart loads it. That entirely bypasses
the loaded-role removal fence. Existing core tests protect the fences
separately, but there is no user-journey composition test. Replace/add a
matrix row with a loaded role that is (a) connected and (b) default owner, and
verify the desktop presents and resolves both blockers.

## Confirmed Linux authorization redesign — no logout/reboot

JRN-01 cannot be fixed robustly by improving the `usermod` message. The current
server authorization in `toad/internal/control/peer_linux.go` explicitly reads
the **connecting process' supplementary groups** from `/proc/<pid>/status` and
requires the core's effective GID (`kikimora`). A running GNOME/systemd-user
process cannot acquire a newly-added supplementary group.

Therefore the production desktop contract must stop making login-time group
credentials a prerequisite for UI control.

### Recommended design

Keep the privileged root core and `SO_PEERCRED`, but add a persistent,
root-owned **operator UID allowlist** plus a narrow polkit enrollment helper.

Suggested persistent state:

~~~text
/etc/kikimora/operators.d/<uid>     root:root, not user-writable
~~~

First launch when the UI is not authorized:

~~~text
UI (already running as uid N)
  -> “Initialize / grant control to this user”
  -> pkexec /usr/libexec/kikimora-authorize-current-user
  -> helper trusts PKEXEC_UID / peer identity, not a caller-supplied username
  -> persist uid N in operators.d
  -> enable/start kikimora-core.service if needed
  -> grant uid N immediate filesystem access to the control socket
  -> UI reconnect loop succeeds in the SAME desktop session
~~~

Core authorization becomes:

~~~text
root/core uid                       -> allow
legacy/admin kikimora process group -> optionally keep for CLI compatibility
SO_PEERCRED uid in root-owned operator allowlist -> allow
everything else                     -> reject
~~~

Do not replace authorization with a client-supplied UID/name. `SO_PEERCRED` is
the identity authority.

### Immediate socket access

Changing server authorization alone is insufficient because the kernel must let
the process reach/connect the socket before `SO_PEERCRED` can be checked.

Preferred implementation is a dedicated control-socket directory with POSIX
ACLs, e.g.:

~~~text
/run/kikimora/control/              root:kikimora, no broad read access
/run/kikimora/control/core.sock     root:kikimora
~~~

A root helper applies `u:<operator-uid>:x`/`rw` ACL entries and a suitable
default ACL to the **control directory only**, so socket recreation inherits
access. A startup helper reapplies ACLs from `operators.d` on every core start
and boot. Do not put operator default ACLs on `/run/kikimora/toads` or secret
paths.

Using the distro `setfacl` tool is acceptable if the package adds an explicit
`acl` dependency and the helper uses fixed argv (no shell interpolation). A
small Linux-specific ACL implementation is also possible; either way the
allowlist remains the authorization truth and ACL is only the kernel reachability
gate.

An alternative for a single-operator workstation is a per-UID socket owned
`0600` by that UID, with the core still checking `SO_PEERCRED`. That avoids
POSIX ACLs but requires a multi/per-user socket lifecycle if multiple desktop
operators are ever supported. The allowlist+ACL design scales better.

### What not to do

- do not require logout/reboot;
- do not chmod the control socket `0666` and rely on application rejection;
- do not make `/run/kikimora` or Toad control paths world-readable/writable;
- do not authorize by account-database group membership while filesystem access
  still depends on stale process groups;
- do not auto-enroll every UID >= 1000 or every logged-in user.

### First-run service activation

The same polkit action should resolve JRN-09: enabling/starting the **control
plane** is not the same as connecting VPN. A fresh core already treats missing
desired state as all-off. Thus first-run may safely:

1. authorize the current UID;
2. enable/start the core service;
3. wait for the socket and authoritative snapshot;
4. return success to the already-running UI;
5. leave every VPN role disconnected until explicit operator intent.

This preserves the old no-surprise network-cutover invariant without shipping
a desktop client that needs a terminal or reboot.

### Required gates

- fresh integrated `.deb`, core initially absent/stopped;
- launch UI as ordinary user N;
- one polkit approval;
- same UI PID/session connects without logout/reboot;
- unauthorized user M cannot connect even if it knows the socket path;
- core restart and host reboot reapply N's access from the persistent allowlist;
- removing N from the operator allowlist revokes new connections;
- ACL applies only to the desktop control socket, never Toad/private-key paths;
- group-based CLI compatibility (if retained) stays tested independently.

### JRN-21 P0 process/gate defect — “installed UI acceptance” never launches the installed UI or performs first-run

`desktop/tests/guest-run-suites.sh`, the U8e runner, does **not** execute
`/usr/bin/kikimora-ui`. It runs eight QtTest binaries from an external
`--build-dir` with:

~~~text
QT_QPA_PLATFORM=offscreen
QT_QUICK_BACKEND=software
KIKIMORA_UI_SOCKET=/run/kikimora/core.sock
~~~

against an already-ready installed core/socket.

The runner precondition is literally:

~~~text
readiness || fail "the installed core is not Ready"
~~~

so it cannot observe the installed product's first-run behavior when the core
is stopped, the desktop user lacks socket access, GNOME identity is wrong, or
the packaged XDG autostart interacts with per-user settings.

The adjacent G5 lifecycle gate closes the other half by manually starting the
service. Its own execution record says:

~~~text
the smoke starts the service (a fresh install intentionally leaves it inactive)
~~~

Therefore the green G5 + U8e combination structurally skipped the exact path
that failed on the real workstation:

~~~text
fresh .deb
-> ordinary logged-in desktop user
-> installed /usr/bin/kikimora-ui
-> package/service initial state
-> authorization
-> first role/profile setup
~~~

123/123 installed-suite tests were valuable component/integration evidence, but
they were mislabeled as end-to-end installed desktop acceptance.

**Fix the gates, not only the product:** introduce a separate mandatory
`InstalledDesktopJourney` gate that uses the real installed binary in a real
GNOME/Wayland user session (not offscreen) and starts from a clean package
state. It must not manually prepare core readiness before launching the UI.

Minimum journey:

1. clean VM and ordinary desktop user already logged in;
2. install exact candidate `.deb`;
3. launch exact `/usr/bin/kikimora-ui` through its `.desktop` entry;
4. complete first-run initialization/authorization in that same session;
5. create profile;
6. import real native AWG fixture, explicitly name role, bind to profile;
7. role appears live without service restart;
8. connect role, choose default, verify routed traffic;
9. switch to second profile/default in one user action;
10. disconnect/reconnect, restart UI, restart core, reboot host;
11. edit role, remove loaded role, remove/rename profile;
12. toggle autostart off/on and verify the next login behavior.

Keep U8e's offscreen suites as a lower-level installed-core compatibility lane;
do not let them satisfy the desktop journey gate.

**Roadmap mapping:** roadmap §20.1 says U8e runs against installed
`kikimora-ui`, but the implemented runner never executes it. G5 explicitly
prepares the service. K1-K8 is a network/core canary and likewise does not
exercise the desktop first-run. The release closure treated these orthogonal
gates as if their union proved the missing path.

### JRN-22 P0 process defect — FakeCore and RealCore encode contradictory create-role semantics

`UiBehaviorTest::createFlowsAddProfileAndTunnel` uses FakeCore and treats the
normal desktop contract as:

~~~text
click Add -> roles.rowCount increases immediately
~~~

The same test even comments “the import adds the role”.

`RealCoreMatrixTest::createTunnelWritesConfiguration`, however, explicitly
asserts that the real-core reply contains `restart` and only checks that the
TOML exists. It never requires the role to appear in the live model.

Thus two green suites certify contradictory product semantics:

~~~text
FakeCore/UI: Add is live
RealCore:     Add is disk-only, restart required
~~~

This is exactly why the declarative UI looked complete in behavior tests while
the installed product was not.

**Fix:** FakeCore is a contract test double, not a design prototype. For every
user-visible operation it must mirror the real backend's lifecycle/result
semantics, and the shared expected contract must live in backend-agnostic
tests. Once JRN-07/JRN-10 make real create/remove live, keep the existing
FakeCore immediate behavior and change RealCore to match it. Until then the
test suite must flag the divergence rather than accepting both.

Add a contract matrix run against both FakeCore and RealCore for:

- create/import role;
- remove loaded role;
- profile create/switch;
- membership edit;
- bypass/default route;
- connect/disconnect;
- command rejection/result visibility.

**Roadmap coverage / regression risk:** U-block behavior work relied heavily on
FakeCore, while Layer C real-core tests intentionally accepted restart-required
create. The coverage matrix counted both lanes but had no cross-backend
semantic-equivalence gate.

### JRN-08 roadmap correction — U3 “every call site gated” claim was too strong

The 2026-11-15 execution log says:

~~~text
Every gate in the roadmap's complete list is present ...
KRoleDetailDrawer role action: actions[0] == disconnect ? safeToDisconnect : safeToMutate
~~~

But that inventory covers only the first action button. The QML Repeater for
`root.actions.slice(1)` creates secondary action buttons without any `enabled`
binding. `UiBehaviorTest::drawerActionsAreCoreReported` verifies that a
`roleSecondaryActionButton` exists, but never puts the backend into a blocked
state and asserts the secondary action is disabled.

The RealCore backend still rejects unsafe programmatic commands, so this is not
a server-side authorization bypass. It is a concrete example of checklist
coverage being mistaken for exhaustive declarative-surface coverage.

**New gate:** enumerate every QML invocation of every Core mutation and map it
to an action class mechanically. A static test/script should fail when a new
button/callsite appears without the shared gate wrapper. Then run behavioral
blocked-state tests for all generated/repeated action variants, not only the
first child.

### JRN-23 P1/P0 UI wiring — role actions inside ProfileDrawer are visually present but disconnected from Core

`ProfileDrawer` uses `KRoleList { expandable: true }`. Clicking a role expands
an inline `KRoleDetailDrawer`.

That inline detail emits:

~~~qml
signal roleActionRequested(int roleIndex, string action)
~~~

but `KRoleList.qml` instantiates it with no `onRoleActionRequested` handler.
There is no forwarding signal on KRoleList for that action.

The working top-level `RoleDrawer` is different: Main.qml explicitly connects
its signal to `Core.roleActionByName(index, action)`.

So Connect/Disconnect/Retry/etc. buttons rendered inside the expandable role
panel in ProfileDrawer can be clicked but their role-action signal terminates
inside the component tree and never reaches the backend.

There are two more dead-looking seams in the same component:

- `KRoleList.activate()` does not emit `roleActivated` at all when
  `expandable=true`; it only toggles `expandedRole`, so ProfileDrawer's
  `onRoleActivated -> root.roleSelected` is unreachable in this mode;
- Main.qml installs an `onRoleMoveRequested` handler on ProfileDrawer, but the
  current ProfileDrawer never emits `roleMoveRequested`.

These are strong signs that the profile sheet interaction was rearranged
without re-auditing signal wiring.

**Fix:** do not duplicate role-detail behavior. Prefer one canonical RoleDrawer
opened from every role row, including ProfileDrawer. If inline expansion is
kept, KRoleList must explicitly forward every role action and dirty/result
contract to its owner, with the same safety/result semantics as RoleDrawer.
Delete dead signals/handlers rather than leaving false API surface.

**Roadmap coverage / regression risk:** repository search finds no UI test for
`inlineRolePanel`, `profileRoleRow`, `expandedRole` or role-action dispatch
from the ProfileDrawer. Existing `drawerActionsAreCoreReported` exercises the
top-level RoleDrawer only. Add a navigation matrix: every visible path to a
role detail must execute the same connect/disconnect/retry command against the
same role identity.

### JRN-24 P0/P1 — VLESS desktop import also loses canonical protocol semantics

The duplicate-importer drift is not limited to AWG.

Canonical `profileimport.parseVLESS` validates and preserves:

- `security=reality` (rejects other security);
- `encryption` empty/none only;
- explicit host + port;
- `sni`, `pbk`, `sid`, `flow`;
- `fp` fingerprint;
- `type` transport (default tcp);
- `spx` SpiderX.

The desktop `classifyVlessLink` preserves only endpoint, UUID, sni, pbk, sid
and flow. It defaults a missing port to 443, does not validate security or
encryption, and drops fingerprint/transport/SpiderX.

The current `config.TunnelSpec` also lacks fingerprint, transport and SpiderX,
so even moving the Qt-parsed values through CreateTunnel cannot represent the
canonical VLESS config.

**Fix:** same as JRN-04: raw import goes to `profileimport.Parse` in the Go
core and returns/persists the full validated `config.Config`. Qt may show a
preview returned by the core but must not independently define protocol
semantics. Remove protocol-specific Qt parsing once the core API exists.

**Roadmap coverage / regression risk:** UI import tests use a narrow fixture
with `sni` and `pbk`; real-core tests use hand-authored JSON TunnelSpec. Neither
compares desktop import output with canonical `profileimport` output. Add a
shared corpus where each provider URI/config is imported through the product
API and compared field-for-field with the canonical parser result.

### JRN-25 P1 — normal role settings hide the configured server until the tunnel is connected

`RealCoreClient::updateModelFromSnapshot` sets the user-facing `role.server` as:

~~~cpp
role.server = role.sessionEndpoint.isEmpty() ? "—" : role.sessionEndpoint;
~~~

`KRoleDetailDrawer` then renders “Сервер” from that field. Therefore an
ordinary disconnected role shows no server at all in normal Settings.

The core snapshot already carries `endpoint.configured`, and the UI maps it to
`configuredEndpoints`, but that value is shown only after expanding
“Техническая информация”.

This breaks the basic create-before-connect review step: the operator cannot
visually confirm the configured destination from the normal role settings
before enabling the VPN.

**Fix:** separate configured and live concepts in the UI model:

- `configuredServer` / configured endpoints from role config/policy;
- `sessionEndpoint` as the currently connected peer;
- normal Settings shows configured destination;
- live endpoint may be shown alongside it when connected, especially when
  provider/rediscovery selected a different concrete address.

Do not overload one `server` field with runtime session truth.

**Roadmap coverage / regression risk:** U5 verified that “server” is rendered
but did not assert its meaning in a disconnected role. Add a role-detail state
matrix for Stopped/Connecting/Ready with configured-vs-live endpoint values.

#### JRN-25 extension — configured identity and runtime observation must be separate models

The same issue affects more than the server. `snapshotRoleLocked` fills
`RoleSnapshot.Interface` from the **observed Toad runtime** only when a process
has published state. For a stopped role, normal Settings can therefore also
show no meaningful interface/runtime address, while the actual config already
contains interface/address/MTU.

The desktop needs an explicit non-secret **configured role summary** in the core
snapshot/API (name, protocol, configured endpoint(s), configured interface,
configured addresses, MTU, profiles and protocol-safe display fields), separate
from runtime interface/session observation. The normal Settings page should be
built from configured truth; diagnostics can show both configured and observed
truth and their divergence.

This also gives role-edit JRN-14 a proper read/modify/confirm contract instead of
trying to reconstruct configuration from runtime state.

### JRN-26 implementation regression guard — live role CRUD makes row-index commands unsafe

Several UI/backend methods identify a role by mutable model row:

~~~text
roleActionByName(int row, action)
setRoleIpv6(int row, enabled)
retryRole(int row)
validateRole(int row)
rediscoverEndpoints(int row)
~~~

The open RoleDrawer stores `roleIndex`; RealCoreClient later resolves the role
again via `m_roles.roleAt(row)` before sending the stable role id to core.

Today the real role set is effectively static until core restart, so ordinary
state snapshots do not reorder the rows. JRN-07/JRN-10 will deliberately make
roles appear/disappear live. At that point an open drawer can retain row N
while insertion/removal shifts row N to another role, causing the next
mutation to target the wrong identity.

**Required before/with live CRUD:** change the UI-facing mutation contract to
stable role identity (`QString roleId` / name). Drawer/list delegates may cache
the id, never the row for a delayed command. Row index remains presentation
only.

Add a regression: open role B, insert/remove role A before it, then invoke
Connect/IPv6/Validate from the still-open drawer and assert the request names B.

This is a direct example of the requested “fix one thing without reintroducing
another” rule: live create/remove must not land before identity-safe UI actions.

## Retained strong contracts — repairs must not regress these

The audit is finding serious desktop/product gaps, but several core safety
contracts are genuinely strong. The remediation plan must treat them as
non-negotiable regression gates rather than rewriting the product around the UI.

### S-01 — server-side command gating remains authoritative

`RealCoreClient` blocks commands by class before sending, and the Go core has
its own authorization/invariant checks. QML disabled state is not the only
safety boundary. Keep both layers.

The aggregate Home connect control and tray action are correctly gated by
`safeToMutate` vs `safeToDisconnect`; do not weaken them while repairing
secondary action wiring.

### S-02 — active-profile role fencing is correct

`ConnectAll`/profile composition operates on the active profile, and direct
`ConnectRole` is rejected for a role outside that active profile. Profile
switch/membership settlement is epoch-fenced so stale asynchronous work cannot
stop/start a role owned by newer intent.

Live Create/Remove and profile-default redesign must use this composition path,
not bypass it.

### S-03 — profile/membership persistence and restart recovery are real durable state

Active profile and per-role desired flags are persisted in `desired.json` and
validated/restored at startup. Unknown/stale persisted profile state fails
safely/falls back according to explicit tests; membership mutation already has
durable transaction/undo handling for loaded roles.

Do not turn the UI into the durable source of truth. New live CRUD must update
the same core-owned durable state before publishing success.

### S-04 — global bypass handover is strongly fail-closed

The existing selected-route/default-route implementation has explicit
preflight, drain/park, transition fencing, rollback and quarantine tests:
`bypass_handover_test.go` and `bypass_owner_test.go` cover target readiness,
concurrent Ensure fencing, rollback, disconnected/out-of-profile targets and
authority quarantine.

JRN-12 asks to change the *configured product model* to a per-profile default
preference / atomic profile switch. It must reuse this protected handover. Do
not replace it with QML issuing `SetBypassMode(direct)`, `SetActiveProfile`,
`SetBypassMode(role)` as three client commands.

### S-05 — secrets are not currently dumped into normal API debug logs

The Go `debugRequest` logs method/role/profile/timing/error, not the TunnelSpec
payload. The Qt client does not log outbound JSON command payloads. This is
important because future `ImportTunnel(raw)` will contain private keys and
possibly provider credentials.

Keep that property explicitly: raw import text, private/preshared keys,
password/token values and secret-file contents must be redacted from UI logs,
journald, diagnostics bundles and command-result messages. Add explicit tests
when the new import/secret API lands.

### S-06 — preserve the old no-surprise network cutover principle, not the old first-run mechanics

The historical package rule “fresh postinst does not silently start/cut over
VPN ownership” is good intent. The mistake was applying it as “desktop product
has no first-run activation”.

The replacement contract is: install may stay inert; explicit first-run
initialization starts the control plane **all-off**, authorizes the current
operator immediately, and no VPN role connects until explicit user intent.

This distinction must be written into package and desktop acceptance so a later
hardening pass cannot reintroduce either silent VPN activation or the current
dead first launch.

## Roadmap / historical-green regression matrix

| Findings | Earlier GREEN / contract | What it actually proved | Why the defect survived | Required replacement/additional gate |
|---|---|---|---|---|
| JRN-01, JRN-09 | 07f1, 08a, 08a1; G5 fresh install | package payload/cutover safety; fresh service deliberately inert | headless/staging invariant was carried into integrated desktop; G5 manually starts core | real logged-in first-run: install -> installed UI -> one polkit init -> same session connected, roles all-off |
| JRN-01 + JRN-09 coupling | independent install audit group hint | source contained a remediation message | hint is conditional on a socket that fresh install deliberately never creates | execute exact fresh postinst and assert reachable first-run action, not source text |
| JRN-02 | U1/U3 API compatibility + safety banner | fail-closed action policy and incompatible-handshake rendering | `!apiCompatible` was treated as a reason instead of a readiness bit | state-table test for Connecting / Unavailable(EACCES, missing, refused) / Incompatible / Ready |
| JRN-03 | P1 integrated packaging, U10 visual lane | PNG + desktop entry packaged; offscreen rendering/goldens | offscreen Qt cannot prove Wayland app-id -> desktop-file association | installed GNOME/Wayland app-id/taskbar identity test using real binary |
| JRN-04, JRN-24 | U3 Layer-B clipboard/import | narrow Qt classification fixtures | duplicate Qt parser became a separate protocol authority | canonical import corpus through core API; compare full `config.Config`, then UI journey |
| JRN-05, JRN-06 | create-flow QML test | an import textarea/button exists and FakeCore can add a role | no product-form completeness requirement; profile context not asserted | required explicit name + target profile; resulting live role identity/membership asserted |
| JRN-11 | parser classification + config validation separately | URL classified as OpenConnect; incomplete backend spec rejected | nobody composed the advertised URL input with required credentials | gateway paste -> auth fields -> secret API -> live connect end-to-end |
| JRN-07, JRN-10 | config API + RealCoreMatrix | CreateTunnel writes durable TOML; RemoveTunnel deletes it | test explicitly accepts `restart`; live lifecycle never required | live create/remove contract with authoritative snapshot and no service restart |
| JRN-22 | Layer B FakeCore + Layer C RealCore both GREEN | each backend passed its own expectations | FakeCore expected live Add while RealCore expected restart | one backend-semantic contract matrix executed against FakeCore and RealCore |
| JRN-26 | static role-set UI tests | row-index actions work while composition is static | live CRUD did not exist, so identity drift was impossible | stable-id mutation APIs + insert/remove-under-open-drawer regression |
| JRN-08 | U3 safety-gate checklist | first role action and named callsites were gated | repeated secondary buttons were not enumerated | mechanical mutation-callsite inventory + blocked-state behavior for every repeated action |
| JRN-23 | RoleDrawer action tests | top-level drawer dispatch works | ProfileDrawer introduced a second inline detail path with no action-forwarding test | navigation matrix: every visible role detail path dispatches identical stable-id actions |
| JRN-12 | PROD2/R8B | global bypass handover is fail-closed; incompatible profile switch rejected | rejection itself was accepted as product behavior | one-action A->B profile switch with different defaults, reusing protected handover |
| JRN-18 | R8B disconnected-default rejection | core correctly rejects unsafe owner | UI was never required to hide/disable impossible choices | real-core UI state with connected+disconnected roles; impossible option unavailable/explained |
| JRN-13, JRN-14 | U5 role-detail surface | fields/diagnostics are visible | “visible” was conflated with lifecycle-complete CRUD | entity lifecycle inventory: create/read/update/rename/delete for role/profile, or explicit immutable rationale |
| JRN-25 | U5 field rendering | a “server/interface” row exists | tests did not define configured-vs-runtime semantics | Stopped/Connecting/Ready role-detail matrix with configured and observed truth separately |
| JRN-15 | routing draft close guard tests | unsaved routing text is protected | second draft type (profile membership) added without composite dirty-state gate | table-driven close test for every editable draft and close path |
| JRN-16 | U1 command error lifetime / optimistic rollback fixes | backend cache eventually returns to authoritative state | final model correctness was treated as operator feedback | each mutation rejection must remain visible at the initiating control until acknowledged/retried |
| JRN-17 | U11 TrayMenu autostart unit + packaging payload test | per-user file toggles; system XDG file exists | two individually green facts compose into wrong XDG effective state | installed XDG precedence test with system entry + `Hidden=true` user override |
| JRN-19 | Home profile-switch behavior | Home empty-profile hint correct | ProfileDrawer duplicated count/filter semantics and had no equivalent test | one shared profile-role model/count; empty target profile tested in every view |
| JRN-20 | RealCoreMatrix Create->Remove + core removal fences | disk-only newly-created role can be removed; core rejects unsafe loaded removal | matrix removes before role is ever loaded | loaded role removal journey for connected/default-owner states |
| JRN-21 | U8e 123/123, G5, K1-K8 | component suites against ready installed core; package/network canary | no gate launched installed UI from real session/fresh service/auth state | dedicated InstalledDesktopJourney gate; old lanes remain lower-level evidence |

### Matrix rule for future roadmap work

A source-stage or component GREEN may not satisfy a right-hand user-journey
column by implication. A release claim must name the exact executable, user
identity/session, service initial state and end-user action sequence that was
actually exercised.

Where an old test protects a safety rejection (for example R8B or PROD2), keep
that test. Add the higher-level transaction/UX behavior above it. Do not delete
the rejection invariant merely because the UI should stop making the user hit it.

### Authorization implementation evidence — ACL inheritance verified on the target Linux

A temporary real Unix-socket experiment on the current Ubuntu host verified
the kernel/filesystem behavior needed by the proposed same-session design:

1. create a private directory;
2. add a named-user traverse ACL and a default named-user `rw-` ACL;
3. bind an AF_UNIX socket inside it;
4. run the same `chmod 0660` that `listenControl` performs;
5. inspect the socket ACL.

The named-user `rw-` ACL remained present after `chmod 0660`. Therefore an
authorized UID can be given immediate socket access without changing its
supplementary groups and without world permissions.

On this Ubuntu host `/usr/bin/pkexec` is owned by package `pkexec` (which
depends on `polkitd`), and `/usr/bin/setfacl` is owned by package `acl`.
If the release uses this design, the integrated Linux package must depend
explicitly on `pkexec` and `acl` rather than assuming desktop-image contents.

Do not apply a default operator ACL to the whole `/run/kikimora`: child Toad
runtime directories could inherit it. Use either:

- a dedicated `/run/kikimora/control/` directory carrying the operator
  default ACL and put the desktop control socket there; **preferred**, or
- exact ACLs on `/run/kikimora` (traverse only) and `core.sock`, reapplied
  after every socket recreation by a service helper.

The dedicated control subdirectory has the cleaner inheritance and least
privilege model.

### JRN-27 P1 — top-level RoleDrawer freezes a copied role snapshot when opened

`Main.qml` opens the role drawer by assigning:

~~~qml
roleDrawer.roleIndex = roleDelegate.sourceIndex
roleDrawer.roleData = Core.roles.get(roleDelegate.sourceIndex)
roleDrawer.open()
~~~

`Core.roles.get()` returns a QVariantMap copy. `RoleDrawer` has no connection
that re-resolves that role on later model/snapshot updates.

Therefore an open role drawer can keep stale:

- state/stateText;
- availableActions;
- live endpoint/handshake/traffic;
- validation/recovery fields;
- profile/runtime diagnostics.

This is especially visible across Connect/Disconnect: the drawer may close on
its own first action today, but it can remain open for secondary settings,
external/tray actions, recovery transitions and future operation-scoped
pending flows.

No desktop test found by repository search asserts that an already-open
`RoleDrawer` follows a later core snapshot.

**Fix:** combine with JRN-26. RoleDrawer owns only a stable `roleId`; its
roleData is resolved/bound from the current model by id on every revision.
Do not persist a row index or a QVariantMap snapshot as role identity/state.

Add a test: open role B, publish several snapshots changing state/actions and
model ordering, and assert the same open drawer tracks B without resetting
operator drafts. Draft form state and live snapshot state must be separate so
a refresh does not overwrite unsaved edits.

### JRN-28 P1 — UI-created roles put mutable Toad runtime state under `/etc/kikimora/state`

`config.CreateTunnel(dir, spec)` assigns:

~~~go
cfg.StateDir = filepath.Join(filepath.Dir(dir), "state", cfg.Name)
~~~

The packaged core runs with:

~~~text
--config-dir /etc/kikimora/toads
~~~

so a role created through the desktop API gets:

~~~text
state_dir = /etc/kikimora/state/<role>
~~~

That mixes mutable Toad runtime/process state with administrator configuration.
The package's actual persistent core state is under `/var/lib/kikimora/core`,
while historical/runtime Toad state is elsewhere; no audit/roadmap rationale
was found for making `/etc/kikimora/state` a writable runtime root.

**Fix:** stop deriving state paths from the config directory. Manager/runtime
paths must provide an explicit role-state root, production Linux under
`/var/lib/kikimora/toads/<role>` for persistent state that must survive reboot
(or `/run/kikimora/toads/<role>` only for state explicitly defined as
ephemeral). Windows uses its ProgramData state root. Import/Create/Edit all
use the same resolver.

The core should own this normalization; imported provider configs must not be
allowed to move product runtime state into arbitrary config/secrets paths by
default.

**Roadmap coverage / regression risk:** `TestFreshInstallCanCreateProfiles`
only asserts that `<config-dir>/lab.toml` exists and explicitly accepts
restart-required CreateTunnel. It never inspects the generated `state_dir`.
Add a production-path config test and package lifecycle test asserting the
state root is outside `/etc` and has the intended owner/mode/persistence.

## Executable remediation sequence

The order below is intentional. Do not implement live CRUD before stable role
identity, and do not declare any feature done before the installed journey gate
that observes it exists.

### W0 — repair the acceptance architecture first

**Files / entry points**

- keep `desktop/tests/guest-run-suites.sh`, but classify it explicitly as
  installed-core/component compatibility, not installed desktop E2E;
- add `linux/tests/toad/installed-desktop-journey-vm.sh` as the VM orchestrator;
- add `desktop/tests/installed_desktop_journey.py` using AT-SPI;
- add a shared FakeCore/RealCore semantic-contract suite under `desktop/tests/`.

**Linux journey runner**

- boot/login a normal GNOME Wayland user;
- install the exact candidate `.deb`; record package version + SHA;
- do not pre-start the core or add groups;
- launch the real desktop entry with `gtk-launch kikimora`;
- assert the process executable is `/usr/bin/kikimora-ui` and it is not using
  the offscreen platform;
- drive accessible controls through `python3-pyatspi` (`python3-pyatspi` is
  available on the target Ubuntu and depends on the AT-SPI stack);
- exercise first-run init, profile/role creation, connection, profile switch,
  edit/delete, UI/core restart, host reboot and autostart.

For CI, a narrow polkit rule may auto-authorize the designated test account so
the helper path is exercised without trying to automate a GNOME password
dialog. Keep one manual real-host canary for the actual graphical polkit prompt.

**Cross-backend contract**

Run the same expected operation semantics against FakeCore and RealCore:
create/import role is live; create/remove results are scoped; membership
changes are live; stable identity survives model changes; rejection semantics
match. A FakeCore result may never be accepted if RealCore has a different
user-visible lifecycle contract.

### W1 — Linux first-run, authorization, socket diagnostics, app identity and autostart

**Authorization authority**

Add a root-owned operator UID registry, for example:

~~~text
/etc/kikimora/operators.d/<decimal-uid>
~~~

Only root may create/remove entries. In
`toad/internal/control/peer_linux.go`, keep `SO_PEERCRED` as the identity
source and authorize:

1. root/core uid;
2. UID present in the validated root-owned operator registry;
3. optionally the existing `kikimora` effective/supplementary-group path for
   administrator/CLI compatibility.

Factor the operator-dir check into a testable helper; production uses the fixed
path, unit tests use a temp path. Validate registry entries with `Lstat`/owner
and reject symlink or writable-by-non-root entries.

**Same-session filesystem reachability**

Do not wait for supplementary groups. Install a root helper, preferably a small
Go binary under `/usr/libexec`, with two idempotent modes:

~~~text
kikimora-authorize-user --authorize-current-user
kikimora-authorize-user --apply-runtime-acl
~~~

`--authorize-current-user` is invoked only through pkexec. It obtains the
target from `PKEXEC_UID`, never from a caller-supplied username/uid, validates
the local account, durably writes the operator entry, enables/starts the core
and applies current runtime ACLs.

`--apply-runtime-acl` reads the root-owned registry and applies:

- traverse-only ACL for each operator UID on `/run/kikimora`;
- `rw-` ACL on `/run/kikimora/core.sock` after it exists.

Wire it into `linux/files/kikimora-core.service` with service-side apply hooks
so a core restart/reboot recreates access from the persistent registry. Prefer
exact directory/socket ACL application over a default ACL on all of
`/run/kikimora`, because Toad runtime paths must not inherit desktop access.

The target-host experiment confirmed a named-user socket ACL survives the
core's existing `chmod 0660`.

**Packaging** — `packaging/linux/stage-release.sh`

- integrated UI build depends on `pkexec` and `acl`;
- stage a custom polkit action for the helper;
- create `/etc/kikimora/operators.d` root-owned;
- remove the unreachable `usermod ... logout/login` remediation;
- fresh install may remain network-inert: no VPN role is connected;
- first-run helper performs `systemctl enable --now kikimora-core.service`;
- remove the system-wide `/etc/xdg/autostart/kikimora.desktop` default; let
  DesktopIntegration own effective per-user autostart.

**Desktop**

- `desktop/src/DesktopIntegration.{h,cpp}`: asynchronous
  `initializeKikimora()` that runs `pkexec /usr/libexec/kikimora-authorize-user
  --authorize-current-user`, exposes pending/result, never blocks UI thread;
- `desktop/qml/Main.qml`: Unavailable/access/service-not-initialized state gets
  an explicit Initialize/Grant access action; no API-incompatible text;
- `desktop/src/core/CoreSocket.cpp`: classify access / server missing / refused;
- `desktop/src/main.cpp`: `QGuiApplication::setDesktopFileName("kikimora")`
  before window creation;
- `DesktopIntegration` autostart reads effective state and, once the package
  stops shipping the system entry, owns only the user entry.

**W1 gate**: one polkit action, same existing GNOME session/UI process becomes
able to handshake. No logout, no reboot, no `sg`, no chmod 0666.

### W2 — stabilize UI identity, live binding and command-result semantics before dynamic roles

**Stable role identity**

Change `CoreBackend` and both backends so all role mutations take a stable
`roleId/name`, not a model row:

~~~text
roleAction(roleId, action)
setRoleIpv6(roleId, enabled)
validateRole(roleId)
retryRole(roleId)
rediscoverEndpoints(roleId)
~~~

`RoleDrawer` owns `roleId`. Add a model lookup by id/name and resolve current
roleData on each revision. Operator drafts (routes/profile checkboxes/edit
forms) are separate from the live snapshot so updates do not erase typing.

**One role-detail path**

Prefer removing the inline `KRoleDetailDrawer` from expandable `KRoleList` and
open the canonical `RoleDrawer` from both Home and ProfileDrawer. If inline is
kept, forward every action/result/dirty contract explicitly. Delete dead
`roleMoveRequested` / unreachable `roleSelected` wiring.

**Operation-scoped results**

Replace normal use of global `lastCommandMessage/Error` with request-scoped
completion. A practical Qt contract:

~~~text
commandStarted(id, method)
commandFinished(id, method, ok, message, fieldErrors)
~~~

or typed completion signals per mutation. Every form/dialog keeps its request
id, stays pending, and closes only on success. Keep the global strings only for
diagnostics/history.

Apply one shared action-class helper to **all** generated role action buttons,
including secondary actions.

Make drawer dirty-state composite (`routeDirty || profilesDirty || future
configDirty`) and test every close path.

### W3 — canonical role creation/import + dynamic role registry

Do this as one feature; do not first add another Qt parser.

**3A. Canonical import API**

Add a versioned core `ImportTunnel` request carrying:

~~~text
raw provider/share profile
explicit role name (required)
target profile (required from the current drawer)
optional structured overrides
~~~

For VLESS / Amnezia `vpn://` / WG / native `[Interface]`, invoke
`toad/internal/profileimport.Parse` with explicit product-owned Name/Interface/
Address/MTU/StateDir options. Apply profile membership to the resulting full
`config.Config`, validate, and persist the **full Config**. Do not down-convert
through `TunnelSpec` (it loses AWG obfuscation and VLESS fp/type/spx).

Remove protocol authority from `desktop/src/core/TunnelImport.cpp`; the UI may
show a preview returned by core but does not parse protocol semantics.

OpenConnect is not a `profileimport` format. Give it a structured wizard:
gateway, username, auth group and authentication method. A password/token value
is sent over the protected local IPC to a core secret-write path; core writes a
root-only file under `/etc/kikimora/secrets` and puts only that path in Config.
Never make the normal UI ask for a pre-existing root-owned password file.

**3B. Normalize product paths**

Add an explicit role-state root to Manager/RuntimePaths. On Linux production
use `/var/lib/kikimora/toads/<role>` (or another documented persistent state
root), not `/etc/kikimora/state/<role>`. Package creates it with the correct
owner/mode. Import/Create/Edit use the same resolver.

**3C. Dynamic core role registration**

Current composition is static in three places. Add explicit APIs:

- `toad/internal/core`: `Controller.AddRole(RoleSpec)` and
  `Controller.RemoveRole(id)` under controller lock;
- `toad/internal/control/selected_routes.go`: register/unregister role-zone
  mapping under its lock, rejecting ambiguous duplicate zones;
- `toad/internal/control/Manager`: register/unregister loaded role config and
  rebuild profile map.

A newly imported role is created **desired=false** even when assigned to the
active profile: adding configuration must not silently connect VPN. `Connect`
or `ConnectAll` remains explicit intent.

Managed-mode create must extend the schema2 marker before the role is admitted
to managed selected-route writes. Reuse/refactor
`extendManagedMarkerForCurrentRoles`; only then set `managedSelectedRoles[name]`
and regenerate/verify Leshy policy. Because marker extension is add-only, a
rolled-back create may leave an inert zone tombstone, which the existing marker
contract already permits.

Persist the config durably, register in-memory state, persist explicit desired
false, extend managed policy when applicable, regenerate/reload policy, then
publish one authoritative snapshot. If any pre-publish step fails, remove the
new config/secret and unregister memory state; success is not returned for a
disk-only role.

**3D. Live removal**

Loaded-role remove is a lifecycle transaction:

1. preflight blockers (current bypass owner/replacement, policy membership);
2. disable desired intent and stop/drain the role;
3. retire selected-route/publication/parking evidence and prove teardown;
4. durably delete config and core-owned secret material;
5. unregister selected-route zone, Controller role and Manager role;
6. rebuild profiles/policy and publish snapshot.

Crash linearization is after resources are safely drained and the durable role
config is removed: after a crash, startup must not resurrect a role whose delete
was acknowledged. Keep current fail-closed quarantine behavior if route cleanup
cannot be proved.

### W4 — make profile policy complete: per-profile default preference + profile CRUD

Do **not** replace the protected global bypass engine.

Add a durable inactive-profile preference map, e.g.
`/etc/kikimora/leshy/profile-defaults.json`:

~~~text
profile -> direct | role
~~~

`bypass.conf` remains the durable **active/effective** default owner so current
startup and selected-route recovery keep one authority.

- new profile preference defaults to `direct`;
- when current profile's default is changed, transactionally update its
  preference + protected `SetBypassMode` commit;
- on `SetActiveProfile(B)`, resolve B's preference and execute one Manager
  transaction that uses the existing bypass preflight/drain/handover machinery
  together with profile desired-state settlement;
- missing preference file migration: current active profile inherits existing
  `bypass.conf`; other profiles start `direct`.

Do not have QML issue direct->switch->role as three commands.

Add `RenameProfile` and `RemoveProfile` through the existing profile composition
transaction machinery. Rename rewrites declared registry, every role membership,
profile-default map and persisted active profile if needed. Remove either
switches active context explicitly or rejects with a structured preview; it
removes membership/default preference atomically.

Default-route UI shows only currently valid targets or explains/connects a
target through one explicit atomic action; never offer a guaranteed rejection
without visible precondition.

### W5 — role edit/rename and configured-vs-runtime model

Add a non-secret configured role projection to core snapshots/API:
name/id, protocol, configured endpoints, interface, addresses, MTU, profiles
and protocol-safe fields. Keep runtime interface/session separately.

Normal Settings renders configured truth; Diagnostics may render configured +
observed/live truth and mismatch.

Add transactional `UpdateRole` and deliberate rename semantics. Prefer stable
internal role ID plus editable display name if renaming on-disk identity would
make route/state recovery unsafe. If name remains identity, rename must migrate
config filename, state dir, profile memberships, routing list, default
preferences and desired state as one journaled operation.

Connection-affecting edits perform a per-role controlled restart/revalidation
inside core, preserving membership/routing policy and rolling back on failure.
Secrets use the W3 secret path/API.

### W6 — UI cleanup and release acceptance

- `ProfileDrawer`: required Name + import/config UI + explicit current profile;
- one role-detail implementation; no dead signals;
- local pending/error state for every mutation;
- composite dirty guard;
- effective autostart toggle;
- truthful socket/core states;
- configured server visible while Stopped;
- profile rename/delete and role edit/delete present;
- GNOME app-id/icon verified.

Then run, in order:

1. Go unit/race/vet and all old managed-routing/profile/bypass crash tests;
2. Qt Layer A/B/C including the new cross-backend semantic matrix;
3. existing G/G5/U8e/K lanes (reclassified, none removed);
4. new real installed GNOME/Wayland desktop journey from a clean VM;
5. real-host clean-install canary.

Production acceptance may be restored only when old safety gates **and** the
new journey gate are green on the same release artifact SHA.

### JRN-29 P1/P0 UX — core advertises `disconnect` for the current default-route owner even though it rejects that command

`snapshotRoleLocked` computes `available_actions` only from role enabled/runtime
state. A Ready/Recovering/etc. role gets `disconnect` (plus retry) regardless
of whether it is the current global bypass/default-route owner.

But `DisconnectRole` has an explicit safety invariant, covered by
`TestDisconnectRoleRejectsCurrentBypassOwner`: the current bypass owner cannot
be disconnected until the default route moves away.

Therefore the authoritative snapshot itself advertises an action the same core
will reject. The desktop renders that advertised action as its primary button,
so the normal per-role Disconnect path becomes a guaranteed error for the
default owner (and JRN-16 may hide the reason after closing the drawer).

**Fix:** available actions must include policy preconditions, not only state.
Either:

- do not advertise bare `disconnect` while the role owns the default route and
  publish a structured blocker/reason, or
- advertise a higher-level `disconnect_and_set_default_direct` action that the
  core executes through the protected bypass handover/drain plus role teardown.

The latter is better desktop UX if the operator explicitly asks to disconnect
the role. The aggregate DisconnectAll path can keep its separate all-off
semantics.

**Roadmap coverage / regression risk:** the core rejection test is correct and
must stay. U5/UI tests verify that core-reported actions are rendered, which
ironically propagates the inconsistency. Add an invariant test at snapshot
construction: every advertised available_action must be executable under the
same snapshot/policy state, or must carry an explicit precondition/action type.

### Matrix additions from the final connect/live-binding pass

| Findings | Earlier GREEN / contract | Gap | Required gate |
|---|---|---|---|
| JRN-27 | U5 RoleDrawer field/action tests | drawer tested at open time, not while snapshots mutate | keep drawer open across state/action/model changes and assert stable role binding |
| JRN-28 | fresh CreateTunnel config API test | only TOML existence asserted; generated product paths not inspected | production-path create/import asserts role state under `/var/lib`/documented runtime root, never `/etc` |
| JRN-29 | core default-owner disconnect rejection + UI “render core actions” | snapshot action computation not checked against command preconditions | for every snapshot, each advertised action must succeed or be a structured higher-level action with a visible precondition |

## End-to-end journey verdict

### What the current 1.4.0/master path actually does

**Install / first launch**

1. `.deb` installs the UI, desktop entry and a system-wide autostart entry.
2. Fresh postinst deliberately leaves the core inactive.
3. The desktop account is not given usable same-session control-socket access.
4. The `usermod` hint is normally suppressed because the stopped core has no
   socket yet.
5. UI can launch into Unavailable; transport failure may be labelled API
   incompatibility; GNOME may not associate the window with its packaged icon.

**Create profile / role**

6. Profile creation itself is live and reasonably transactional, but does not
   open the new profile and has no rename/delete lifecycle.
7. Add Tunnel has no role name field and does not pass the current profile.
8. Native AWG is rejected as JSON; VLESS is parsed by a drifting duplicate
   parser; the advertised bare OpenConnect gateway is incomplete by design.
9. Real core writes the role TOML but does not add the role to the running
   composition until service restart; generated role state is placed under
   `/etc/kikimora/state`.
10. Empty/unknown membership is displayed permissively, masking lost profile
    assignment.

**Operate roles / routing**

11. Aggregate Connect/Disconnect and server-side active-profile fencing are
    strong and fail-closed.
12. ProfileDrawer's inline role actions are visually present but unwired.
13. Top-level RoleDrawer freezes a copied role snapshot while open.
14. The UI offers some impossible actions: disconnected role as default
    target; disconnect on the current default owner.
15. Default-route presentation is per-profile, but persisted policy is one
    global owner. Switching profiles with different desired defaults is a
    manual direct -> switch -> role sequence, otherwise the switch is rejected.
16. Rejections/results are often silent or detached from the control that
    initiated them.

**Maintain / restart**

17. Active-profile + desired-state restart recovery and protected selected
    routing are among the strongest parts of the product and should be kept.
18. Role config/name/credentials are not normally editable; role removal is
    disk/restart based with hidden blockers; profile rename/delete is absent.
19. Linux autostart toggle does not control the system-wide entry installed by
    the package.

### Required production journey after W0-W6

~~~text
install exact .deb
  -> no VPN connects automatically
launch Kikimora from desktop entry
  -> one Initialize action / one polkit approval
  -> SAME UI session reaches core, no logout/reboot
create/open profile
add role:
  explicit name + current profile
  native provider/share input -> canonical core parser
  OpenConnect -> structured credential wizard
  -> role appears LIVE, Stopped, with configured endpoint visible
connect role
  -> pending/result local to role; valid actions only
choose profile default
  -> protected handover, no impossible choice
create second profile/role
switch profile once
  -> composition + per-profile default hand over atomically
restart UI
restart core
reboot host
  -> active profile/desired/default/operator access restore consistently
edit role / rotate credential / rename as supported
remove loaded role safely
rename/remove profile safely
toggle autostart off/on
  -> next login follows the displayed setting
~~~

Anything less is not a completed desktop VPN-client journey even if every
component/unit suite is green.

## Audit closure state

This source audit is **complete for the requested primary user journey**:
install -> first-run -> profile/role creation -> connect/disconnect -> default
routing/profile switching -> maintenance -> restart/recovery, plus the test/
roadmap contracts that were supposed to cover it.

Further work should now execute W0-W6 rather than add isolated UI patches.
Every implementation packet must cite the JRN findings it closes and the S-*
strong contracts it preserves.
