
# 09e — Linux installed desktop defects found on the real host (2026-11-27)

Status: **OPEN / implementation packet**

Scope: production Linux 1.4.0 integrated .deb installed on the real Ubuntu
GNOME/Wayland desktop after removing the old unmanaged Bash Kikimora and all
previous 1.4.0 residue. This note records defects observed after a genuinely
clean install. It is not an audit of the old migration path.

Release under test:

- package: kikimora 1.4.0;
- .deb SHA-256:
  65904655fd8099bbb0dbc4abe31f9d40b17db5eed7f9cfdeaf00895d5363e079;
- tag 1.4.0: 847917b96728c9b98d848f09627453f74676bbf1.

The clean reinstall gate explicitly proved that no old
/etc/systemd/system/leshy* overrides, old Bash libexec tree, old package, or
old running product process remained before installing the release. After
install the live services resolve to package units under
/usr/lib/systemd/system.

## Summary

Five user-visible Linux desktop defects remain in 1.4.0:

| ID | Severity | Defect |
|---|---:|---|
| LUI-1 | P0 | Fresh desktop install does not provision the desktop user for the kikimora socket group; a persistent `systemd --user` can keep stale groups even across a plain logout/login. |
| LUI-2 | P1 | Any pre-handshake socket failure is rendered as “Ядро несовместимо по API”, even when no API handshake occurred. |
| LUI-3 | P1 | GNOME/Wayland taskbar/overview shows a generic/no Kikimora icon because the Qt desktop app identity is not bound to kikimora.desktop. |
| LUI-4 | P0 | The desktop tunnel importer rejects native AmneziaWG/WireGuard `[Interface]/[Peer]` input even though the canonical Go importer already supports it. |
| LUI-5 | P0 | Tunnel creation has no explicit role-name field and drops the current profile context instead of creating the role in the opened profile. |

These are release-blocking as a set for a normal Linux desktop install. LUI-1
breaks first-run control-plane access, LUI-4 blocks the expected AmneziaWG
migration path, and LUI-5 makes role identity/profile membership incomplete.

---

# LUI-1 — fresh install does not grant the desktop user access to the core socket

## Observed live state

After the clean 1.4.0 install, the control socket contract itself was correct:

~~~text
/run/kikimora            0750 root:kikimora
/run/kikimora/core.sock  0660 root:kikimora
~~~

The core, Leshy and DNS integration were healthy and package-owned:

~~~text
kikimora-core.service       active
leshy.service               active
kikimora-leshy-dns.service  active

kikimora-core FragmentPath=/usr/lib/systemd/system/kikimora-core.service
leshy FragmentPath=/usr/lib/systemd/system/leshy.service
~~~

The install initially left the desktop account outside kikimora. After
manually adding it, /etc/group contained:

~~~text
kikimora:x:981:nmkartsev
~~~

but the already-running GNOME session and the UI process still had no group
981 in /proc/<ui-pid>/status.

A normal GNOME logout/login was then tried and **did not fix it**. Live
inspection showed why:

~~~text
loginctl show-user nmkartsev:
Linger=yes

/usr/lib/systemd/systemd --user:
PID 3608, started 14:55, survived logout/login
Groups: ... 1001              # no 981

new gnome-shell after relogin:
Groups: ... 1001              # no 981

new kikimora-ui launched by GNOME:
Groups: ... 1001              # no 981
~~~

The account database was already correct (`id nmkartsev` and `getent group
kikimora` included GID 981), but the persistent per-user systemd manager kept
the pre-membership credential set and spawned the replacement graphical session
with those stale supplementary groups.

A direct AF_UNIX probe reproduced the exact failure:

~~~text
current GNOME session:
PermissionError [Errno 13] Permission denied

same account executed with kikimora supplementary group:
CONNECT_OK
~~~

Therefore this is a local credential/session problem, not a core/API failure.

## Existing code

packaging/linux/stage-release.sh generates DEBIAN/postinst. The current
postinst deliberately does not enroll any desktop user. It only prints:

~~~text
sudo usermod -aG kikimora $USER   (then log out and back in)
~~~

The comment says the deb cannot know which account runs the desktop session.
That is safe for unattended/server installs, but insufficient for an integrated
desktop product: terminal postinst output is not a usable first-run flow, and
GUI/software-center installs may never expose it.

The earlier independent install audit already recorded this as only partially
fixed; the real installed-host run proves that the residual item is still
product-visible in 1.4.0.

## Required fix

Keep root:kikimora plus 0750/0660 as the security boundary. Do not weaken the
socket or runtime directory to world-accessible permissions.

Implement two layers.

### A. Packaging: enroll an unambiguous interactive installer account

In the generated Linux postinst in packaging/linux/stage-release.sh:

1. Resolve an interactive target account only when it is unambiguous.
2. Preferred sources:
   - explicit packaging/test override, e.g. KIKIMORA_DESKTOP_USER;
   - valid SUDO_USER when it is not root;
   - valid PKEXEC_UID mapped with getent passwd.
3. Validate that the account exists and is a normal local login account
   (non-zero uid, normally uid >= 1000, usable home/shell).
4. Run usermod -aG kikimora "$user" idempotently.
5. Do **not** promise that plain “log out and back in” is sufficient. On hosts
   with `loginctl show-user <user> -p Linger` reporting `yes`, the user manager
   can survive logout and retain the old supplementary groups. The installer/UI
   must say that a **new user manager** is required: reboot, or explicitly
   terminate/restart the user's systemd manager after all sessions are closed.
6. On an unattended/root-only install with no unambiguous desktop account, do
   not guess and do not enroll arbitrary users. Leave explicit remediation.

Do not derive the target from “first UID >= 1000” and do not enumerate every
logged-in account. That would silently widen control-plane access on multi-user
hosts.

Longer-term desktop UX may use a first-run polkit helper instead of relying on
maintainer-script environment. Such a helper must preserve the kikimora group
authorization model; changing only filesystem ACLs is insufficient because
core peer authorization also checks identity/group.

### B. UI: distinguish “membership recorded but this session is stale”

On Linux, when socket access fails with EACCES:

- check the account database membership in group kikimora;
- check the process's actual supplementary groups;
- if the user is listed in the group database but the current process does not
  have that GID, show:
  “Доступ к ядру добавлен, но текущий сеанс ещё не получил группу kikimora.
  Выйдите из системы и войдите снова.”
- if the user is not listed, show the exact usermod remediation or a future
  “Grant access” polkit action.

Do not suggest reboot as a repair when membership was never added.

## Tests

Add packaging/install tests, not only source-tree tests.

1. Fresh VM install through sudo apt install ./kikimora_*.deb from a normal
   desktop user.
2. Assert the selected user appears in getent group kikimora.
3. Start a fresh login/session, or an isolated process with the newly resolved
   supplementary group, and assert it can traverse /run/kikimora and connect
   to core.sock.
4. Assert an existing pre-membership process still gets EACCES, proving the
   stale-session diagnostic.
5. Add a `Linger=yes` regression: keep `systemd --user` alive across a graphical
   logout/login, prove the replacement GNOME/UI process still lacks the newly
   added group, and verify the diagnostic says the user manager/session
   credentials are stale rather than promising that a plain relogin fixes it.
6. Verify a reboot or explicit user-manager restart after all sessions are
   closed gives the UI process GID `kikimora` and restores socket access.
   Real-host evidence: `systemctl restart user@1000.service` terminated the
   active graphical session back to GDM, replaced user-manager PID 3608 with
   PID 67351, and the new manager had supplementary GID 981. A direct AF_UNIX
   probe from the refreshed user context then returned `CONNECT_OK`.
7. Unattended/root-only install: assert no arbitrary desktop account is added.
8. Reinstall/upgrade: enrollment is idempotent.

Candidate gate location: extend the installed-package VM lifecycle gate under
linux/tests/toad/managed-package-lifecycle-vm.sh, or add a focused installed
desktop package gate if graphical/session checks would make that file unclear.

Acceptance:

- a normal interactive install has a deterministic path to socket access;
- no world-writable/readable socket workaround;
- no arbitrary user enrollment;
- UI explains stale-session vs missing-membership correctly.

---

# LUI-2 — transport failure is falsely labeled “API incompatible”

## Observed UI

With the core running but the UI denied traversal of /run/kikimora, the Home
banner displayed:

~~~text
Ядро несовместимо по API: команды заблокированы —
core socket: QLocalSocket::connectToServer: Socket access error
~~~

No handshake happened. Calling this “API incompatible” is false.

## Root cause

The transport/client state is already mostly correct:

- desktop/src/core/CoreSocket.cpp stores the QLocalSocket error;
- desktop/src/core/RealCoreClient.cpp handles errorOccurred by setting
  m_coreState = "Unavailable" and putting the socket reason in
  m_lastCommandError;
- actual API mismatch is set only after parsing the handshake response:
  m_coreState = "Incompatible".

The bug is in desktop/qml/Main.qml.

The banner currently uses !Core.apiCompatible as the highest-priority
condition:

~~~qml
visible: !Core.apiCompatible
         || Core.modeTransition !== "none"
         || (Core.managedMode && !Core.routeAuthorityReady)

if (!Core.apiCompatible)
    return "Ядро несовместимо по API: команды заблокированы" ...
~~~

But apiCompatible is intentionally false before a successful handshake and is
cleared on disconnect. Therefore the expression conflates:

- Connecting;
- socket missing/refused;
- EACCES;
- disconnected/reconnecting;
- real handshake incompatibility.

RealCoreClient.h already documents the correct meaning: apiCompatible means
this connection completed a compatible Handshake. It is not a classification
of why a connection is absent.

## Required fix

Make coreState authoritative for the banner class.

Minimum shape in desktop/qml/Main.qml:

~~~qml
visible: Core.coreState === "Incompatible"
         || Core.coreState === "Unavailable"
         || Core.modeTransition !== "none"
         || (Core.managedMode && !Core.routeAuthorityReady)

text: {
    if (Core.coreState === "Incompatible")
        return "Ядро несовместимо по API: команды заблокированы"
               + errorSuffix
    if (Core.coreState === "Unavailable")
        return "Ядро недоступно"
               + errorSuffix
    ...
}
~~~

Do not infer incompatibility from !apiCompatible.

Also improve the Unix branch of desktop/src/core/CoreSocket.cpp. Instead of
surfacing Qt's generic:

~~~text
QLocalSocket::connectToServer: Socket access error
~~~

map at least:

- QLocalSocket::SocketAccessError -> actionable Linux group/session message;
- QLocalSocket::ServerNotFoundError -> core socket/service not present;
- QLocalSocket::ConnectionRefusedError -> socket exists but service is not
  accepting connections;
- fallback -> Qt error string.

The Windows branch already performs explicit error mapping; Unix should provide
equivalent operator quality.

Safety semantics remain fail-closed: mutations stay disabled until a compatible
handshake. This change only makes the reason truthful.

## Tests

### Client/transport

Add a Linux test for an inaccessible AF_UNIX path. Use a temporary parent
directory without traversal permission; skip only if the test runs as root.
Assert:

~~~text
coreState == "Unavailable"
apiCompatible == false
lastCommandError contains actionable access/group text
~~~

A missing/refused socket must produce the corresponding unavailable reason.

Keep or add a handshake incompatibility test:

~~~text
coreState == "Incompatible"
apiCompatible == false
lastCommandError contains offered API range
~~~

### QML/UI

Drive at least these states:

| State | Expected banner |
|---|---|
| Unavailable, apiCompatible=false, EACCES reason | “Ядро недоступно …” and not “несовместимо по API” |
| Incompatible, apiCompatible=false | “Ядро несовместимо по API …” |
| Connecting, apiCompatible=false | no false incompatibility banner |
| compatible + route authority lost | existing route-authority warning |

Acceptance: a transport failure can never be rendered as API incompatibility
unless an actual handshake response proved an incompatible API range.

---

# LUI-3 — GNOME/Wayland taskbar shows no Kikimora application icon

## Observed live UI

On the installed Ubuntu GNOME/Wayland desktop, the Kikimora window appears in
the taskbar/overview with a generic/blank application icon even though the main
window renders and the packaged icon exists.

## Packaging is present

The integrated package correctly contains:

~~~text
/usr/share/applications/kikimora.desktop
/usr/share/icons/hicolor/256x256/apps/kikimora.png
/usr/bin/kikimora-ui
~~~

The desktop entry contains:

~~~ini
Exec=/usr/bin/kikimora-ui
Icon=kikimora
~~~

The icon file is physically installed. hicolor-icon-theme package triggers ran
during package installation, so “missing PNG” is not the observed failure.

desktop/src/main.cpp additionally calls:

~~~cpp
app.setWindowIcon(QIcon(QStringLiteral(":/resources/kikimora.png")));
~~~

That is not sufficient for GNOME/Wayland application matching.

## Root cause / primary fix

desktop/src/main.cpp sets:

~~~cpp
app.setApplicationName(QStringLiteral("Kikimora"));
app.setOrganizationName(QStringLiteral("Kikimora"));
~~~

but never sets the Qt desktop file name.

On Wayland, the compositor/shell associates the top-level window with a desktop
entry through the application/desktop identity (xdg-shell app id), not by
reading the window pixmap set by setWindowIcon().

Before creating/showing any window, bind Qt explicitly to the packaged desktop
entry:

~~~cpp
QGuiApplication::setDesktopFileName(QStringLiteral("kikimora"));
~~~

or the equivalent instance call supported by the Qt version in use.

The value must match the basename of:

~~~text
/usr/share/applications/kikimora.desktop
~~~

For X11 compatibility, also consider adding:

~~~ini
StartupWMClass=Kikimora
~~~

to the desktop launch metadata, but do not treat that as a substitute for
setDesktopFileName() on Wayland.

Do not rename the PNG or desktop file unless there is a deliberate product-wide
application-ID migration.

## Tests

### Static/package test

Extend desktop/tests/test_packaging.sh or the Linux package gate to assert:

- kikimora.desktop exists;
- Icon=kikimora;
- kikimora.png is installed in the hicolor application icon path;
- the desktop app ID used by the binary is kikimora.

A small Qt metadata test can assert after app initialization:

~~~cpp
QGuiApplication::desktopFileName() == QStringLiteral("kikimora")
~~~

### Installed GNOME/Wayland acceptance

The real installed gate must include an actual Wayland identity check. One
practical diagnostic is to launch the app with WAYLAND_DEBUG=client in an
isolated test session and assert the xdg-toplevel app-id sent to the compositor
is kikimora, then launch through gtk-launch kikimora and verify that the window
groups under the Kikimora desktop entry rather than a generic application.

The gate must run with no already-running instance because the single-instance
lock otherwise prevents a second process from creating a top-level window.

Acceptance:

- GNOME taskbar/overview uses the packaged Kikimora icon;
- launching from the desktop entry and session autostart groups the same
  application identity;
- tray icon and window/taskbar icon remain consistent.

---

# LUI-4 — desktop “Добавить туннель” rejects native AmneziaWG/WireGuard config

## Observed UI

Pasting a native AmneziaWG/WireGuard profile beginning with [Interface] / [Peer]
into “Добавить туннель” produces:

~~~text
похоже на JSON, но разобрать не удалось: illegal number
~~~

This is not malformed user input. The installed 1.4.0 desktop importer does
not support the native AWG/WireGuard format.

## Exact root cause

There are two different import implementations in the same release.

The canonical Go importer under:

~~~text
toad/internal/profileimport/import.go
toad/internal/profileimport/wireguard.go
~~~

already accepts raw native WireGuard/AWG INI. Its dispatch explicitly accepts
input beginning with [interface], and parseWireGuardINI maps [Interface] /
[Peer] fields into the amneziawg2 Toad configuration, including the
Amnezia-specific Jc/Jmin/Jmax, S1-S4, H1-H4 and I1-I5 parameters.

The desktop UI does not use that importer. It has a separate reduced
implementation in desktop/src/core/TunnelImport.cpp whose supported inputs in
1.4.0/master are only:

- vless://...
- http://... / https://... as an OpenConnect gateway;
- JSON object containing at least name and protocol.

Worse, ParseTunnelImport currently routes any input beginning with either '{'
or '[' to classifyJson(). Therefore a valid [Interface] profile is
deterministically sent to QJsonDocument::fromJson(), producing the misleading
illegal number JSON error.

This is an architectural drift bug: the command-line/Go import path supports
the native format while the primary desktop import surface does not.

## Required fix

### Preferred fix — one canonical importer in the core

Do not implement a third independent parser and do not keep duplicating the Go
import rules in Qt.

Add a core API operation such as ImportTunnel that accepts the raw pasted
profile plus target profile/name options. The core should invoke the existing
toad/internal/profileimport package and persist the resulting validated
configuration using the same durable config-creation path as other create
operations.

Desktop flow:

~~~text
paste raw text
    -> optional lightweight UI classification/preview
    -> ImportTunnel(raw text, target profile/options)
    -> core canonical profileimport
    -> validated Toad config
~~~

The authoritative accepted formats then become exactly those supported by
toad/internal/profileimport, including native [Interface]/[Peer], wg://,
wireguard://, amneziawg://, provider Amnezia exports and VLESS.

Keep secrets out of logs and API error echoes.

### Minimal release fix if the API change is deferred

At minimum, desktop/src/core/TunnelImport.cpp must detect [Interface] before
the generic JSON-array branch and support the native AWG fields with behavior
equivalent to profileimport.parseWireGuardINI.

Do not merely remove '[' from the JSON condition: that would replace the
current false JSON error with “unsupported input” while leaving the product
feature broken.

The UI hint in desktop/qml/Drawers/ProfileDrawer.qml must also list the native
supported format once it really works.

## Current workaround for 1.4.0

The desktop import field cannot consume the native AmneziaWG export directly.
For 1.4.0 the UI accepts a JSON TunnelSpec for AWG2 with the core fields:

~~~json
{
  "name": "my-awg",
  "protocol": "amneziawg2",
  "endpoint": "host:port",
  "address": ["10.x.x.x/32"],
  "private_key": "...",
  "peer_public_key": "...",
  "preshared_key": "...",
  "allowed_ips": ["0.0.0.0/0"]
}
~~~

This JSON path is only a workaround. It does not expose all Amnezia-specific
obfuscation fields carried by the canonical Go importer, so it must not be
presented as equivalent to importing a native AmneziaWG profile.

A safer local workaround is to use the corresponding kikimora-toad import path
to convert the native profile into the canonical Toad config rather than
manually transcribing secrets.

## Tests

1. Desktop import parser: raw [Interface] / [Peer] input must never enter the
   JSON parser.
2. Native WireGuard fixture -> successful AWG2 import.
3. Native AmneziaWG fixture with Jc/Jmin/Jmax, S*, H* and I* -> values survive
   import.
4. wg://, wireguard:// and amneziawg:// fixtures accepted through the same
   product path.
5. Invalid native profile returns a WireGuard/AWG-specific diagnostic, never
   “похоже на JSON”.
6. End-to-end UI/core test: paste native config, create tunnel, verify the
   resulting role/config and connect path.
7. Secret hygiene: private key/preshared key must not appear in UI logs,
   journal, diagnostic errors or test failure output.

Acceptance: every import format advertised by the product must be backed by
the same canonical importer, and a native AmneziaWG export from the provider
can be pasted into the desktop UI without manual conversion.

---

# LUI-5 — tunnel creation UI has no role name and drops current profile context

## Observed UX

The “Добавить туннель” section inside ProfileDrawer contains only:

- one multiline import TextArea;
- “Вставить из буфера”;
- “Добавить”.

There is no role/tunnel name field at all.

Current implicit naming behavior is format-dependent:

- VLESS: URL fragment, otherwise `vless-<host>`;
- OpenConnect gateway: `oc-<host>`;
- JSON: caller must manually provide `"name"`;
- native AWG/WireGuard: no desktop support at all in 1.4.0, so no usable
  naming path exists.

This makes the primary creation flow non-deterministic and prevents the
operator from choosing a stable semantic role name such as `primary`,
`work`, `nl`, or `awg-home`.

## Second defect in the same flow: selected profile is not propagated

`desktop/qml/Drawers/ProfileDrawer.qml` has:

~~~qml
property string profileName: ""
~~~

but the Add button calls only:

~~~qml
Core.importTunnel(importField.text)
~~~

`profileName` is not passed to the backend.

`TunnelSpec` already has a `profile` field, and config creation persists it
when supplied. However the VLESS and OpenConnect desktop classifiers do not
set it, and the ProfileDrawer does not inject its current profile. Therefore
creating a tunnel from inside a profile drawer can create a disk tunnel with
no membership in the profile the operator was looking at.

## Required fix

Make tunnel creation an explicit form rather than deriving product identity
from transport text.

At minimum ProfileDrawer must provide:

1. **Role name** field (required, validated against the same instance-name
   contract as core config creation).
2. **Target profile** initialized to `root.profileName`; normally hidden or
   shown read-only because the operator opened this profile drawer.
3. Import/config field for the actual transport material.

Preferred API shape together with LUI-4:

~~~text
ImportTunnel {
    raw: <provider/native config>,
    name: <explicit role name>,
    profile: <current profile>
}
~~~

The core canonical importer parses protocol material; the explicit UI fields
override provider-derived labels for product identity and membership.

Rules:

- explicit user-entered role name wins over provider/share-link labels;
- if the role-name field is empty, Add is disabled; do not silently invent a
  production role name;
- current ProfileDrawer.profileName is passed atomically with creation;
- creation must not require a follow-up SetTunnelProfiles race;
- if a generic/global Add flow is introduced later, it may present an
  explicit profile selector instead.

## Tests

1. ProfileDrawer exposes a required role-name field.
2. Add is disabled for empty/invalid names.
3. VLESS import with explicit name uses the explicit name, not URL fragment.
4. OpenConnect import with explicit name uses the explicit name, not `oc-host`.
5. Native AWG import with explicit name creates that exact role.
6. Creating from profile `work` persists membership `work` in the new Toad
   config and the role appears in that profile after reload/restart.
7. Creating from profile A must never accidentally attach to active profile B.
8. Duplicate role name is surfaced as a clear create error without mutating
   profile membership.

Acceptance: the operator can choose the role name before creation, and a
tunnel created from a profile drawer belongs to that profile by construction.

---

# Implementation order

1. **LUI-2 first**: truthful state classification is small and makes every
   subsequent installed-host failure diagnosable.
2. **LUI-1 second**: deterministic desktop-user enrollment plus stale-session
   diagnosis while preserving the group security model.
3. **LUI-4 + LUI-5 together**: route raw imports through the canonical core
   importer and make explicit `name + profile` part of the creation contract.
4. **LUI-3**: desktop identity via setDesktopFileName plus package/Wayland
   acceptance.
5. Build a new integrated .deb.
6. Test on a clean disposable Ubuntu GNOME/Wayland VM.
7. Repeat on the real installed host from a state with no old unmanaged
   Kikimora files.

# Required regression evidence for closure

The executor must record all of the following before marking this packet done:

1. fresh installed package version and SHA;
2. systemctl FragmentPath for core and Leshy;
3. socket directory/socket owner, group and mode;
4. group database entry and actual supplementary groups of the launched UI
   process;
5. a successful user-level socket/API handshake;
6. an intentional EACCES test showing Unavailable/access rather than API
   incompatible;
7. an intentional incompatible-handshake test showing the API warning;
8. GNOME/Wayland app-id/icon evidence;
9. native AmneziaWG `[Interface]/[Peer]` paste -> canonical AWG2 role creation;
10. explicit role name and current-profile membership persisted atomically;
11. desktop/package unit tests plus relevant CTest suites;
12. git diff --check.

Do not close LUI-1 merely because postinst prints a command. Do not close LUI-2
merely because lastCommandError contains EACCES. Do not close LUI-3 merely
because the PNG exists in the package: the real shell/window identity must match
the desktop entry. Do not close LUI-4 with a second ad-hoc Qt parser if the
canonical importer still diverges. Do not close LUI-5 until name and target
profile are explicit creation inputs.
