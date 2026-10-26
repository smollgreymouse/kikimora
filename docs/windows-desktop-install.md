# Windows desktop UI: install and known issues

## Install

```
powershell -File packaging\windows\deploy-ui.ps1 ^
    -UiExe .\build-win\kikimora-ui.exe ^
    -QtDir C:\Qt\6.10.2\mingw_64 ^
    -MingwRuntimeDir C:\uitests ^
    -Shortcuts
```

The UI goes to `C:\Program Files\Kikimora\ui\`, next to it its own Qt payload, the whole
`qml` tree, the `styles` plugins and the offscreen/minimal platform plugins, plus Start Menu
and Desktop shortcuts. It must not share `C:\Program Files\Kikimora` with the core: the
core's GnuTLS/curl/ffi DLLs break the UI with `0xC0000139`.

`packaging/windows/kikimora.wxs` now installs the `UiExe` component into the same `ui`
subdirectory, but the MSI still does not carry the Qt payload - run `deploy-ui.ps1` after an
MSI install (or harvest the payload with `heat.exe` when the MSI is extended).

## Identity

- Window icon, taskbar icon and tray icon come from `:/resources/kikimora.png`.
- The executable icon and version block come from `desktop/resources/app.rc`, compiled when
  `WIN32` is set; `kikimora.ico` is generated from the same PNG.
- The default style is `Basic`: the native Windows style refuses the Control background
  customisation that the K* controls rely on (263 warnings per run without it).
  `KIKIMORA_QT_STYLE` overrides.

## Verify an install

```
$env:KIKIMORA_UI_PRINT_BACKEND = 'C:\uitests\ui-backend.txt'
& 'C:\Program Files\Kikimora\ui\kikimora-ui.exe'      # exits immediately
Get-Content C:\uitests\ui-backend.txt
# backend=real-core socket=C:/ProgramData/Kikimora/core.sock available=true
```

For a headless check set `QT_QPA_PLATFORM=offscreen` and `QT_QUICK_BACKEND=software`; the
window survives and stderr stays at two lines (a font hint).

## Default-route row, verified on the guest (2026-10-26)

The default-route choice moved out of Settings into the last row of every profile's
role list. The rebuilt UI was installed on the `win10` guest and checked live:

- the installed `kikimora-ui.exe` hash matched the workstation build, and the
  headless probe printed `backend=real-core socket=C:/ProgramData/Kikimora/core.sock
  available=true`;
- the row showed `Напрямую` while `C:\ProgramData\Kikimora\leshy\bypass.conf` was
  absent (the core then reports `direct`);
- with `bypass.conf` set to `xray-win`, a restarted UI showed `xray-win` in the row,
  so the client reads the rule through `GetRoutingLists` instead of assuming
  `direct` (the snapshot does not carry `bypass_mode`);
- the file was removed afterwards and the row returned to `Напрямую`.

## Known issue: openconnect role can stick in Recovering

`toad/internal/control/control.go:1497` handles `core.ErrToadRestartPending` by marking
`restartHandoffPending` and relying on the replacement toad to publish its generation. That
wait has **no deadline**: if the replacement process stays alive without coming up, the role
remains `Recovering` forever and the aggregate never returns to Ready.

Observed in the lab on 2026-10-18: `oc-main` with `pid 7224`, `recovery.step=start-transport`,
`last_error="Toad restart started; awaiting replacement generation"` for over ten minutes.

Remedies, in order:

1. `kikimora-core disconnect --role oc-main` then `kikimora-core connect --role oc-main` -
   moved the role to `Stopped` and back, but it landed in the same handoff state.
2. `Restart-Service KikimoraCore`, then `kikimora-core start` - the only reliable way today;
   `retry --role` does **not** clear the pending handoff.

This is a product defect, not a lab artefact: the UI shows a permanent "Recovering" and the
readiness gate in `desktop/tests/guest-run-suites.ps1` cannot pass. The fix belongs in the
core: a watchdog on `restartHandoffPending` that kills the replacement and re-drives recovery
after a bounded wait, with a test next to `control_test.go`'s existing handoff cases.

## Fixed: restart handoff now has a deadline

The defect above is fixed in the core. `Manager.watchRestartHandoff`
(`toad/internal/control/control.go`) is armed whenever `ErrToadRestartPending` records
`restartHandoffPending`, and after `restartHandoffDeadline` (90 s) it drops the handoff and
re-drives recovery through `scheduleRecoveryRetry`, provided the role, its operation and the
underlay epoch are still the ones that entered the handoff. The variable exists so tests can
shorten it.

`TestRestartHandoffDeadlineReDrivesRecovery` covers it; the whole `toad` module is green
(`go test ./...`, every package `ok`).

Verified in the guest after installing the rebuilt core: the service came back `Running`,
`kikimora-core start` reached `aggregate_state=Ready` with all three roles in five seconds,
and the full desktop suite pass is green:

| Suite | Window guest |
|-------|--------------|
| ui-behavior | 15 passed, 0 failed |
| tray-menu | 5 passed, 0 failed |
| real-core-ui | 3 passed, 0 failed |
| real-core-matrix | 4 passed, 0 failed, 1 skipped (single profile) |
| tray-live | 4 passed, 0 failed |
| role-list-model | 7 passed, 0 failed |

## Fixed: the socket was unreachable for an ordinary user

The UI showed no profile and no tunnel. It was not a configuration problem and not the client:
the control socket carried the service token's default DACL, where `BUILTIN\Users` holds only
`(RX)`. AF_UNIX `connect()` needs **write** access, so a UI started by an ordinary user could
enumerate the socket but never connect, and every list stayed empty. Every probe in this lab ran
elevated, which is why the tests were green while the installed app was blank.

`listenControl` in `toad/internal/control/transport_windows.go` now merges
`BUILTIN\Users:(R,W)` into the socket DACL right after `bind`, keeping the existing SYSTEM and
Administrators entries:

```
icacls C:\ProgramData\Kikimora\core.sock
  BUILTIN\Users:(R,W)                    <- added
  BUILTIN\Administrators:(I)(F)
  NT AUTHORITY\SYSTEM:(I)(F)
```

Verified with a non-elevated token: `kikimora-core status` as `greymouse` returns
`core state: Ready` and the live state instead of an access error.

## Adding a tunnel or a profile

There is **no UI flow for it today**: neither `desktop/qml` nor `desktop/src` contains an
add/create path (the only `addRole` is the internal model mutator). Tunnels and profiles are
declared in the core configuration:

- a tunnel is a toad config (`toads\*.toml`, or the file list passed to `kikimora-core serve
  --config`);
- profiles are the `profiles = [...]` entries of that configuration, selected with
  `kikimora-core profiles use NAME` or the profile switch in the UI.

So today: edit the configuration, restart the core, and the UI lists the new role and profile.
A first-class flow would need two pieces that do not exist yet - a config-write API in the core
(`profile create`, `toad add`) and a wizard in the UI on top of it.

## Creating tunnels and profiles (new)

The core now writes configuration through its control socket:

- `CreateTunnel` with a `tunnel` object - name, protocol, profile, endpoint/address and the
  protocol's credentials (awg2: private_key/peer_public_key; vless: uuid/server_name/
  public_key; openconnect: username). It derives the interface name (`kk-<name>`), MTU 1380
  and `state_dir`, refuses duplicates and loads the file back through `config.Load` so an
  underspecified tunnel never lands on disk.
- `CreateProfile` with a `profile` name - adds it to every toad configuration and reports how
  many files changed.

Both are covered by `internal/config/create_test.go` and `internal/control/config_api_test.go`.
The composition is fixed at construction, so the snapshot shows the new role only after the
core restarts; the response carries a message saying exactly that.

In the UI the Settings page has the wizard: profile name, tunnel name, server, user and a
protocol selector, plus the core's reply. `ui-behavior` covers the flow against the fake
backend; `CoreBackend::createTunnel/createProfile` send the same calls to the real core.

Still to wire: a live end-to-end case in `real-core-matrix` (call the API, assert the file,
restart, assert the role) and the `QML` cleanup of the plain controls used by the wizard.

## Create flows: follow-up done

- `real-core-matrix::createTunnelWritesConfiguration` covers the whole socket path: it builds a
  spec, calls `createTunnel`, waits for the reply, requires the "restart" hint in it, parses
  the created path out of the message, asserts the file exists and removes it again. Green in
  the Windows guest (matrix now 5 passed) and on Ubuntu.
- The wizard runs on the design-system controls (`KTextField`, `KComboBox`, `KButton`).
  `KTextField` now keeps `value` in sync with typing, which is what the wizard reads.
- Below the wizard a hint appears as soon as the core answers: the raw message plus
  "Перезапустите службу ядра: состав фиксируется при старте."

## Profile creation sheet (2026-10-26)

The create-profile form is no longer a stock `Dialog` with standard buttons. It is a product
modal (`Popup`) built from the design-system controls, and it says what actually happened:

- the name is checked before the request with the core's own rule
  (`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`) and against the live profile list; the field turns red
  and the message stays in the sheet instead of the sheet closing silently;
- the sheet stays open until the core answers through `CoreBackend::profileCreationFinished`.
  `RealCoreClient` matches the `CreateProfile` reply by request id; the fake backend validates
  and answers at once;
- a profile the running core already loaded (simulated backend) closes the sheet and becomes
  the active profile; the real core only writes the configuration, so the sheet says the
  profile appears after a core restart instead of pretending it exists;
- Enter submits, Escape closes, the field takes focus on open.

Home owns both create entry points now: the `+` next to the profile selector opens this sheet,
and the `+` under the role list opens the profile sheet with the tunnel import.

Verified on the `win10` guest: the sheet renders on the product palette, an invalid name
(`test profile`) keeps it open with a red field and the format hint, and no configuration is
written.

## What the packages now deliver themselves (2026-10-20)

Hand-tuning individual installs was wrong; the behaviour belongs to the packages.

Linux (`packaging/linux/stage-release.sh`, built with `KIKIMORA_UI_BIN=build-ui/kikimora-ui`):

- `/usr/bin/kikimora-ui`, `/usr/share/applications/kikimora.desktop`,
  `/etc/xdg/autostart/kikimora.desktop` (`Exec=/usr/bin/kikimora-ui --background`), the
  hicolor icon, and `Recommends: gnome-shell-extension-appindicator` plus the Qt6 QML modules.

Verified by building the deb and installing it on the lab VM: the four UI payload entries are
inside the package and `dpkg -i` puts them on disk with the autostart `Exec` intact.

Windows (`packaging/windows/kikimora.wxs`): the `UiExe` component now carries a
`KikimoraStartupShortcut` (`StartupFolder`, `--background`), so an MSI install starts the tray
application at logon. Still open: the MSI does not carry the Qt payload (windeployqt output);
that needs `heat.exe` harvesting of the deployed `ui` directory and is the last packaging gap.

Build note for anyone repeating this from a Windows checkout: `git archive` produces CRLF, and
the Linux packaging scripts die with `set: pipefail: invalid option name`. Strip the CRs first
(`find packaging linux -name '*.sh' -exec sed -i 's/\r$//' {} +`).
