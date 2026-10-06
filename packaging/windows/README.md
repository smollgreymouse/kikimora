# Windows packaging

**Status: portable package implemented (08a4a phase 9); the privileged VM
acceptance pass is still pending** — see the PENDING marker in
`docs/roadmap.md` and the 08a4a packet.

The package ships the Go control plane and the per-role supervisor as a
portable zip with elevated install/uninstall scripts. A dedicated
`setup.exe` (WiX/NSIS) is deliberately deferred until after the Windows VM
lifecycle acceptance.

## Contents

- `stage.ps1` — builds (or stages provided) `kikimora-core.exe` /
  `kikimora-toad.exe`, adds `install.ps1` / `uninstall.ps1`, writes
  `manifest.json` + `checksums.txt` (SHA256) and produces
  `kikimora-<version>-windows-amd64.zip`.
- `install.ps1` — elevated installer: copies binaries into
  `%ProgramFiles%\Kikimora`, creates the crash-safe
  `%ProgramData%\Kikimora` layout (`state`, `toads`, `logs`, `leshy`) with a
  hardened state-directory ACL and registers the `KikimoraCore` service via
  the core's own `service install` verb (automatic start, staged restart
  recovery). Fresh installs never write VPN role desired state.
- `uninstall.ps1` — elevated removal: deletes the service and binaries;
  desired state, logs and Leshy publications are preserved unless `-Purge`
  is requested (explicit purge semantics; Wintun adapters of retired roles
  go with the driver-level uninstall owned by this package).
- `test_package.ps1` — unprivileged static contract test: stages the package
  and verifies layout, checksum coverage and the script contracts; run it
  locally or in CI with Go on `PATH`.

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
