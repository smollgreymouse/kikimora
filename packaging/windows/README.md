# Windows packaging

**Status: MSI installer implemented (08a4a phase 9); the privileged VM
acceptance pass is still pending** — see the PENDING marker in
`docs/roadmap.md` and the 08a4a packet.

The primary installation contract is a real **Windows Installer package
(MSI)** built with WiX v3: it installs the binaries into
`%ProgramFiles%\Kikimora`, creates the crash-safe `%ProgramData%\Kikimora`
layout (`state`, `toads`, `logs`, `leshy`) with a hardened state-directory
ACL, registers the `KikimoraCore` service declaratively
(`ServiceInstall`/`ServiceControl`, automatic start, removed on uninstall)
and carries a stable UpgradeCode so MajorUpgrade handles upgrades and
downgrade rejection through Add/Remove Programs. Fresh installs start the
service with all VPN roles disabled — no desired state is written. The
service binary self-applies the staged restart recovery policy (5s/30s/60s,
one-hour reset) on its first SCM start. Desired state, logs and Leshy
publications survive upgrade and uninstall; a purge is a manual explicit
step. Wintun adapters of retired roles are keyed by the deterministic role
GUID and go with the driver-level uninstall.

## Contents

- `kikimora.wxs` — the WiX v3 authoring (product, directories, permissions,
  service, upgrade contract).
- `stage.ps1` — builds (or stages provided) `kikimora-core.exe` /
  `kikimora-toad.exe`, writes `manifest.json` + `checksums.txt` (SHA256),
  produces `kikimora-<version>-windows-amd64.msi` (primary artifact) and the
  portable `kikimora-<version>-windows-amd64.zip` (binaries + scripts for
  portable/dev use). WiX binaries are expected at `%USERPROFILE%\Tools\wix311`
  or `$Env:WIX_BIN`.
- `install.ps1` / `uninstall.ps1` — elevated install/remove scripts for the
  portable layout (the MSI path needs no scripts).
- `test_package.ps1` — unprivileged contract test: stages the package, builds
  the MSI and verifies the payload, checksums and the MSI database (product
  identity, UpgradeCode, File rows, ServiceInstall/ServiceControl
  registration, ProgramData directories); run locally or in CI with Go and
  WiX available.

## Verification status

- The package contract test passes on windows/amd64 (`test_package.ps1`,
  19 assertions).
- The privileged steps — `install.ps1` (service registration, ProgramData
  ACLs), the Wintun lifecycle test
  (`go test -tags privileged ./internal/platform/`) and SCM
  start-before-desktop-login — are staged for the disposable Windows VM
  pass required by `docs/toad-steps/08a4-windows-vm-console-lifecycle-acceptance.md`.
  Until that gate passes with real AWG + OpenConnect, lifecycle,
  suspend/reboot/hypervisor and installer evidence, the package stays a
  staging artifact and no release publishing is enabled.
