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
