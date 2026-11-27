# 09G-W1 — Linux first-run authorization, truthful connection state, app identity and autostart

Status: **EXECUTION PLAN**

Closes: JRN-01, JRN-02, JRN-03, JRN-09, JRN-17 and the
JRN-01/JRN-09 coupling note.

Depends on: W0 harness exists and currently reproduces the broken first-run.

## Fixed architecture

The Linux desktop control path is:

```text
unprivileged UI
  -> pkexec /usr/libexec/kikimora/kikimora-authorize-user --authorize-current-user
  -> helper trusts PKEXEC_UID only
  -> root-owned /etc/kikimora/operators.d/<uid>
  -> systemctl enable --now kikimora-core.service
  -> service ExecStartPost reapplies per-UID ACL to /run/kikimora + core.sock
  -> already-running UI reconnects
  -> kernel admits socket connect
  -> core verifies SO_PEERCRED uid against the same root-owned registry
```

Keep the current socket path `/run/kikimora/core.sock`. Do not move it.

Do not chmod runtime paths world-accessible. Do not depend on newly-added Unix
supplementary groups for the desktop user.

## Commit W1.1 — shared operator registry and peer authorization

### New package

Create:

- `toad/internal/operatorauth/registry_linux.go`
- `toad/internal/operatorauth/registry_linux_test.go`

Export:

```go
const DefaultRegistryDir = "/etc/kikimora/operators.d"

type Registry struct {
    Dir string
}

func (r Registry) IsAuthorized(uid uint32) (bool, error)
func (r Registry) Authorize(uid uint32) error
func (r Registry) AuthorizedUIDs() ([]uint32, error)
```

### On-disk format

One file per uid:

```text
/etc/kikimora/operators.d/1000
content: 1000\n
owner: root:root
mode: 0644
```

Directory:

```text
owner root:root
mode 0755
```

`IsAuthorized` must:

1. reject uid 0 as a registry entry; root is authorized separately;
2. build the filename only from decimal uid;
3. `Lstat` it;
4. require a regular file, never symlink;
5. require owner uid 0;
6. reject group/other writable mode bits;
7. parse one decimal line and require it equals the filename uid;
8. return false, nil for missing entry;
9. return error for malformed/insecure entry.

`Authorize` must:

1. require effective uid 0;
2. mkdir registry dir 0755 if absent;
3. write exact `<uid>\n` via same-directory temp + fsync + rename;
4. chmod final file 0644;
5. fsync registry directory.

Use `toad/internal/fsutil.WriteAtomicFile` for the registry entry; it already fsyncs the payload and parent directory. Do not add a parallel atomic-write implementation.

### Peer authorization

Modify `toad/internal/control/peer_linux.go`.

Refactor:

```go
func authorizePeer(conn net.Conn) error {
    return authorizePeerWithRegistry(conn, operatorauth.Registry{Dir: operatorauth.DefaultRegistryDir})
}

func authorizePeerWithRegistry(conn net.Conn, registry operatorauth.Registry) error
```

Authorization order after `SO_PEERCRED`:

1. server uid / root uid: allow;
2. `registry.IsAuthorized(credential.Uid)`: allow on true, fail closed on
   malformed registry error;
3. retain current effective/supplementary server-GID check for CLI/admin
   compatibility;
4. otherwise reject.

Never use a uid supplied in the request body.

### Tests

Add/extend `toad/internal/control/peer_linux_test.go`:

- same uid allowed;
- root allowed;
- authorized uid through temp registry allowed even without kikimora group;
- missing uid rejected;
- malformed registry entry rejected;
- symlink rejected;
- non-root-owned entry rejected where test privileges permit; otherwise test
  the validation helper directly;
- old supplementary-group path remains green.

Run:

```bash
cd toad
gofmt -w internal/operatorauth/*.go internal/control/peer_linux*.go
go test -count=1 ./internal/operatorauth ./internal/control
go test -race -count=1 ./internal/control
go vet ./...
```

## Commit W1.2 — privileged helper and runtime ACL reapplication

### New binary

Create:

- `toad/cmd/kikimora-authorize-user/main_linux.go`
- `toad/cmd/kikimora-authorize-user/main_linux_test.go`

Install path is fixed:

```text
/usr/libexec/kikimora/kikimora-authorize-user
```

Supported modes only:

```text
--authorize-current-user
--apply-runtime-acl [--wait-ms N]
```

No arbitrary uid/name command-line argument is allowed.

### `--authorize-current-user`

Algorithm:

1. require euid 0;
2. read `PKEXEC_UID`;
3. parse decimal uid, require uid != 0;
4. `user.LookupId` and require lookup succeeds;
5. authorize uid in the registry;
6. execute exactly:
   `systemctl enable --now kikimora-core.service`;
7. invoke the same internal ACL apply function with a 10-second socket wait;
8. return 0 only if registry write, service activation and ACL application all
   succeed.

Use `exec.CommandContext` with fixed argv; no shell.

### `--apply-runtime-acl`

Algorithm:

1. require euid 0;
2. read `Registry.AuthorizedUIDs()`;
3. wait up to `--wait-ms` for both:
   - `/run/kikimora` directory;
   - `/run/kikimora/core.sock` Unix socket;
4. for every authorized uid run exact argv:
   - `setfacl -m u:<uid>:--x /run/kikimora`
   - `setfacl -m u:<uid>:rw- /run/kikimora/core.sock`
5. never apply a default ACL to all of `/run/kikimora`;
6. never touch `/run/kikimora/toads`, secrets or Leshy paths.

The registry remains the authorization authority. ACL is only the kernel
reachability gate.

### Helper tests

Abstract command execution behind a tiny interface/function variable local to
the command package so tests assert exact `systemctl` and `setfacl` argv.
Test:

- PKEXEC_UID missing;
- uid 0 rejected;
- malformed uid rejected;
- root requirement;
- authorize writes registry;
- apply emits exact ACL commands for multiple UIDs;
- no ACL command references `/run/kikimora/toads`;
- socket wait timeout is a hard error.

## Commit W1.3 — package helper, policy and service hooks

### `packaging/linux/stage-release.sh`

At stage-directory creation add:

```text
/usr/libexec/kikimora
/usr/share/polkit-1/actions
```

Build helper from `./cmd/kikimora-authorize-user` and install mode 0755 to the
fixed path.

For integrated UI artifacts only, append runtime dependencies:

```text
pkexec, acl
```

Do not add them to headless-only packaging unless that artifact also ships the
desktop authorization helper.

### Polkit policy

Add source file:

- `linux/files/io.github.smollgreymouse.kikimora.policy`

Install to:

- `/usr/share/polkit-1/actions/io.github.smollgreymouse.kikimora.policy`

Action id:

```text
io.github.smollgreymouse.kikimora.authorize-user
```

Use this exact policy body:

~~~xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC
 "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <vendor>Kikimora</vendor>
  <vendor_url>https://github.com/smollgreymouse/kikimora</vendor_url>

  <action id="io.github.smollgreymouse.kikimora.authorize-user">
    <description>Authorize this desktop user to control Kikimora</description>
    <message>Authentication is required to initialize Kikimora for this user</message>
    <defaults>
      <allow_any>no</allow_any>
      <allow_inactive>no</allow_inactive>
      <allow_active>auth_admin_keep</allow_active>
    </defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/libexec/kikimora/kikimora-authorize-user</annotate>
  </action>
</policyconfig>
~~~

Do not add `org.freedesktop.policykit.exec.allow_gui`: the helper itself needs
no GUI environment. The graphical authentication dialog is provided by the
desktop polkit agent.

The UI invokes the exact helper through `pkexec`; do not add sudo integration.

### Postinst

In generated `DEBIAN/postinst`:

1. create `/etc/kikimora/operators.d` root:root 0755;
2. keep fresh install network-inert: do not auto-start the core here;
3. remove the old conditional `usermod -aG kikimora` message entirely;
4. preserve upgrade restart behavior.

### Service

Modify `linux/files/kikimora-core.service`.

Add after `ExecStart`:

```ini
ExecStartPost=/usr/libexec/kikimora/kikimora-authorize-user --apply-runtime-acl --wait-ms 10000
```

Do not prefix with `-`: failure to restore operator reachability is a service
start failure and must be visible.

Keep:

- `User=root`;
- `Group=kikimora`;
- `RuntimeDirectory=kikimora`;
- mode 0750;
- socket chmod 0660.

### Packaging tests

Extend `linux/tests/test_integrated_release_packaging.sh`:

- helper exists and executable;
- policy exists;
- Depends includes `pkexec` and `acl`;
- postinst creates operators dir;
- postinst contains no `usermod` remediation;
- system-wide autostart expectation will be changed in W1.6;
- core service contains exact ExecStartPost.

Extend `linux/tests/toad/managed-package-lifecycle-vm.sh --mode fresh`:

- after install service remains inactive;
- helper/policy/registry dir exist;
- running the helper under a test PKEXEC_UID or direct test mode in the VM
  starts/enables core and creates working ACL;
- current desktop uid can connect without new supplementary groups.

## Commit W1.4 — typed transport problem state

### CoreSocket

Modify:

- `desktop/src/core/CoreSocket.h`
- `desktop/src/core/CoreSocket.cpp`

Add enum:

```cpp
enum class CoreSocketProblem {
    None,
    AccessDenied,
    ServerMissing,
    ConnectionRefused,
    Other
};
Q_ENUM(CoreSocketProblem)
CoreSocketProblem lastProblem() const;
```

Unix mapping from `QLocalSocket::LocalSocketError`:

- `SocketAccessError -> AccessDenied`
- `ServerNotFoundError -> ServerMissing`
- `ConnectionRefusedError -> ConnectionRefused`
- all else -> Other.

Windows mapping stays equivalent to its Winsock errors.

Remove Linux text instructing `usermod`.

### CoreBackend / RealCoreClient

Add read-only property:

```cpp
Q_PROPERTY(QString connectionProblem READ connectionProblem NOTIFY snapshotChanged)
```

Values are exactly:

```text
none
access-denied
server-missing
connection-refused
other
```

`RealCoreClient::setDisconnected` and socket error handling map
`CoreSocket::lastProblem()`. A compatible handshake clears it to `none`.

Do not overload `apiCompatible`. It remains true only after compatible
handshake and false otherwise.

### QML

In `desktop/qml/Main.qml`:

- show "Ядро несовместимо по API" only when
  `Core.coreState === "Incompatible"`;
- for `Unavailable`:
  - access-denied: explain that desktop authorization is required and expose
    Initialize action;
  - server-missing/refused: explain core is not running/reachable and expose
    Initialize action on Linux;
  - other: generic transport error;
- never call an Unavailable state incompatible.

Add stable object names:

- `coreUnavailableBanner`
- `initializeKikimoraButton`
- `coreIncompatibleBanner`.

### Tests

Extend `desktop/tests/RealCoreClientTest.cpp` and
`desktop/tests/UiBehaviorTest.cpp` with all four states.

## Commit W1.5 — async desktop initialization and GNOME identity

### DesktopIntegration API

Modify:

- `desktop/src/DesktopIntegration.h`
- `desktop/src/DesktopIntegration.cpp`

Add properties:

```cpp
Q_PROPERTY(bool initializationPending READ initializationPending NOTIFY initializationChanged)
Q_PROPERTY(QString initializationError READ initializationError NOTIFY initializationChanged)
Q_INVOKABLE void initializeKikimora();
```

Add private members in DesktopIntegration.h:

~~~cpp
QProcess *m_initializationProcess = nullptr;
bool m_initializationPending = false;
QString m_initializationError;
~~~

Forward-declare `class QProcess;`.

Implementation on Linux:

1. if `m_initializationPending`, return;
2. clear initializationError, set pending=true, emit initializationChanged;
3. allocate `m_initializationProcess = new QProcess(this)`;
4. set program exactly `/usr/bin/pkexec`;
5. set arguments exactly:
   `{"/usr/libexec/kikimora/kikimora-authorize-user",
      "--authorize-current-user"}`;
6. set process channel mode `QProcess::SeparateChannels`;
7. connect `errorOccurred`:
   - if the process fails to start, store
     `"Не удалось запустить системную авторизацию: " + process.errorString()`;
   - finish the operation through the common cleanup below;
8. connect `finished(int,QProcess::ExitStatus)`:
   - read stderr once;
   - cap to the last 8192 bytes before converting to QString;
   - exit status NormalExit + code 0: clear error and call
     `m_core->reconnectNow()`;
   - otherwise store
     `"Авторизация Kikimora не выполнена"` plus one trimmed stderr line when
     present; never expose more than 8 KiB;
9. common cleanup for start-error/finished:
   - set pending=false;
   - `deleteLater()` the exact process;
   - set pointer=nullptr;
   - emit initializationChanged exactly once;
10. destructor does not synchronously wait. If the process still exists,
    `kill()` it and let QObject ownership reclaim it.
11. do not merge stdout/stderr, do not invoke a shell, and do not invoke
    `systemctl` from QML/C++ directly.

On non-Linux builds, `initializeKikimora()` completes through queued
`initializationChanged` with error `"Инициализация через polkit доступна только в Linux"`;
it never starts a process.

Add to `desktop/src/core/CoreBackend.h`:

~~~cpp
Q_INVOKABLE virtual void reconnectNow() = 0;
~~~

Implement in RealCoreClient: stop the reconnect timer, reset the reconnect
attempt counter, abort only a non-connected stale socket, then invoke
`connectToCore()`.

Implement FakeCoreBackend::reconnectNow() as an explicit no-op.

DesktopIntegration holds `CoreBackend *m_core`, so after the pkexec helper exits
0 it calls `m_core->reconnectNow()` directly. Do not dynamic_cast to
RealCoreClient, do not add a second socket client, and do not wait for the
exponential backoff after successful initialization.

Wire `initializeKikimoraButton` to `Desktop.initializeKikimora()`.

### App identity

In `desktop/src/main.cpp`, before creating windows:

```cpp
QGuiApplication::setDesktopFileName(QStringLiteral("kikimora"));
```

Do not change desktop basename or Icon field.

## Commit W1.6 — make Linux autostart purely per-user

### Packaging

In `packaging/linux/stage-release.sh`:

- stop staging `/etc/xdg/autostart/kikimora.desktop`;
- do not create the stage autostart directory solely for this file;
- keep `/usr/share/applications/kikimora.desktop`.

### DesktopIntegration

Linux remains per-user only at:

```text
~/.config/autostart/kikimora.desktop
```

`setAutostartEnabled(true)` writes a full entry with:

```ini
[Desktop Entry]
Type=Application
Name=Kikimora
Exec=<absolute current executable> --background
Icon=kikimora
Terminal=false
X-GNOME-Autostart-enabled=true
```

`false` removes the user file.

Because the package no longer creates a system entry, no Hidden=true override
is needed.

Update `TrayMenuTest::autostartEntryIsCreatedAndRemoved` and integrated
packaging tests accordingly.

## W1 acceptance

Run old safety/package gates plus W0 journey.

The W0 installed journey must now require:

1. fresh package installed;
2. core initially inactive;
3. real UI launched from desktop entry;
4. UI shows Initialize rather than API incompatibility;
5. one polkit authorization;
6. **same already-running UI process/session** reaches Ready;
7. no VPN role is desired/connected;
8. user process has not acquired the kikimora supplementary group as a
   prerequisite;
9. UI app-id associates with `kikimora.desktop`;
10. autostart off -> next login no UI; on -> next login UI.

W1 is not done if logout/reboot is required for authorization.
