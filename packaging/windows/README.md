# Windows packaging scaffold

**Status: experimental / disabled for release.**

Windows networking support is not yet designed or accepted. This directory
contains packaging scaffold only — no service installation, no TAP/Wintun
drivers, no enabled release publishing.

## Current contents

- `stage.ps1` — build staging script (stub)

## Cross-build status

Windows Go cross-build (`GOOS=windows GOARCH=amd64`) is verified during
07F packaging checks. The resulting binaries are not packaged as a supported
release artifact.

## Activation

Windows packaging will be activated when:

1. Windows networking ownership is designed and accepted.
2. A Windows privileged networking gate exists.
3. Windows CI runners produce verified artifacts.
4. The disposable-VM parity packet `docs/toad-steps/08a4-windows-vm-console-lifecycle-acceptance.md` passes with real AWG + OpenConnect, lifecycle, suspend/reboot/hypervisor and installer evidence.

Passing the Windows UI/FakeCore suite is not networking acceptance. Until the native substrate exists and 08A.4 passes, this directory remains scaffold-only.