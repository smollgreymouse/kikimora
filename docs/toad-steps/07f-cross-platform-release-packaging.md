# Toad step 07F — cross-platform release packaging

Status: **NEXT AFTER 07E**.

Purpose: produce installable release artifacts before any real installed-host staging.

Supported release platforms in this packet:

- Linux: **required and release-blocking**;
- macOS: **required packaging/install contract**; privileged networking acceptance remains a separate macOS runtime gate;
- Windows: **scaffold only**; do not enable release publishing or networking acceptance yet.

08A must consume an artifact produced and verified by this packet. It must not install directly from a source checkout.

---

## 0. Audit of current packaging

Current repository has overlapping/incomplete packaging paths:

### Linux path A — `linux/package.sh`

Already builds:

- `kikimora-core`;
- `kikimora-toad`;
- Linux CLI;
- systemd core unit;
- tmpfiles/sysusers files;
- ownership config;
- NetworkManager unmanaged rule;
- endpoint providers;
- `.deb` and rootfs-style `.tar.gz`.

But it does **not** package the Qt desktop UI.

### Linux path B — `desktop/packaging/build-release.sh`

Already builds/packages:

- `kikimora-ui`;
- `kikimora-core`;
- `kikimora-toad`;
- desktop entry/icon;
- `.deb` and portable `.tar.gz`.

But it does **not** include the Linux system integration needed by the production Go control plane:

- `kikimora`/`kk` CLI;
- core systemd unit;
- tmpfiles/sysusers;
- ownership config;
- NetworkManager unmanaged rule;
- endpoint providers;
- orchestration/diagnostic CLI libraries.

Therefore neither current Linux artifact is the complete 08A product artifact.

### macOS

`macos/install.sh` exists, but it is primarily the older Leshy/service adapter path. There is no equivalent release package that contains the current Go `kikimora-core`, `kikimora-toad` and Qt UI as one tested artifact.

Audit also found `macos/install.sh` references `kikimora-macos`, while no `macos/kikimora-macos` file exists at the reviewed HEAD. Treat this as a concrete packaging defect to resolve, not as documentation noise.

### Windows

There is no `windows/` packaging tree. Desktop CI builds on Windows, but Windows networking remains out of the current production acceptance path.

---

# Phase 1 — define one canonical release payload contract

Create a machine-readable or shell/CMake-readable release manifest under a platform-neutral location, for example:

`packaging/release-manifest.txt`

or an equivalent implementation that has one source of truth.

The manifest must define logical components, not hard-coded staging commands.

Required common product components:

- `kikimora-ui`;
- `kikimora-core`;
- `kikimora-toad`;
- product version from repository `VERSION`;
- license/readme/release metadata.

Required Linux integration components:

- `/usr/local/sbin/kikimora` or the chosen canonical CLI path;
- `kk` alias/symlink contract;
- CLI library files under `/usr/local/libexec/kikimora/cli`;
- endpoint providers;
- `kikimora-core.service`;
- tmpfiles config;
- sysusers config;
- orchestration ownership template;
- `/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf`;
- desktop entry/icon;
- any configuration templates required to start the Go-owned stack.

Required macOS integration components:

- `Kikimora.app` or the existing Qt bundle output;
- `kikimora-core`;
- `kikimora-toad`;
- launchd plist for core;
- CLI/alias contract appropriate to macOS;
- required support/config directories;
- uninstall/upgrade-safe ownership metadata.

Windows scaffold components:

- `kikimora-ui.exe`;
- placeholders for `kikimora-core.exe` and `kikimora-toad.exe` build outputs;
- packaging manifest only;
- no service installation;
- no driver/TUN installation;
- no enabled release publishing.

Do not duplicate file lists independently in three unrelated scripts if one shared manifest can express them.

---

# Phase 2 — unify Linux release packaging

Choose one canonical Linux release builder.

Preferred direction:

- keep CMake/CPack for UI packaging;
- teach the Linux release target to include the complete system integration payload currently owned by `linux/package.sh`;
- retire `linux/package.sh` as a second independent release artifact builder, or turn it into a thin wrapper around the canonical builder.

Do not leave two different `.deb` formats named Kikimora.

## 2.1 Required Linux artifacts

Produce at minimum:

```text
dist/kikimora_<VERSION>_<ARCH>.deb
dist/kikimora-<VERSION>-linux-<ARCH>.tar.gz
dist/SHA256SUMS
```

The `.deb` is the canonical installed-host artifact for 08A.

The tarball may be portable/staging-oriented but must have an explicit installation contract. Do not call a rootfs tarball 'portable' if it silently requires extraction into `/`.

## 2.2 Linux package metadata

Ensure package metadata has:

- exact VERSION;
- architecture;
- required Qt runtime dependencies;
- `openconnect` dependency or documented optional/runtime requirement according to current OpenConnect execution model;
- systemd/network tools dependencies required by installed-host operation;
- package description matching actual product contents.

Do not package test-only `ocserv`.

## 2.3 Linux package install lifecycle

Add package maintainer scripts only where needed and keep them conservative.

Required post-install behavior:

- `systemctl daemon-reload`;
- tmpfiles/sysusers creation as appropriate;
- install files with correct modes/owners;
- do **not** automatically perform Go cutover;
- do **not** enable/start production VPN ownership unless current ownership policy already says Go and the package contract explicitly allows preserving that state.

Upgrade must preserve:

- user Toad configs;
- desired-state file;
- ownership config;
- legacy rollback assets until 08B retirement;
- secrets.

Uninstall without purge must preserve user config/state according to existing product policy.

## 2.4 Eliminate source-installer/package divergence

`linux/install.sh` currently follows the older source-tree installer contract and does not itself install the built Go binaries from a release artifact.

Do one of:

1. make release package installation the canonical supported install path and clearly mark `install.sh` as source/developer bootstrap; or
2. make `install.sh` consume the same staged payload/manifest as the release package.

Do not maintain a third independent list of product files.

---

# Phase 3 — Linux package tests

Extend/replace `desktop/tests/test_packaging.sh` so it tests the **complete** product artifact.

Required `.deb` content assertions:

```text
kikimora-ui
kikimora-core
kikimora-toad
kikimora / kk CLI
CLI libraries
endpoint providers
kikimora-core.service
tmpfiles config
sysusers config
orchestration ownership config
90-kikimora-unmanaged.conf
desktop entry
icon
```

Required assertions:

- package VERSION equals repository VERSION;
- executable modes correct;
- config/service file modes correct;
- no test fixtures/secrets included;
- no duplicate conflicting core/toad binaries in multiple prefixes;
- service ExecStart points to the packaged binary path;
- CLI points to packaged core/socket paths;
- `dpkg-deb --info` and extraction succeed.

Add install smoke in a disposable Linux environment/container where systemd mutation is not required:

- install `.deb` into an isolated root or disposable runner/VM;
- verify filesystem layout;
- run `kikimora-core --help` / version;
- run `kikimora-toad --help` / version;
- run CLI help/version;
- validate systemd unit with `systemd-analyze verify` using staged paths where feasible.

Do not run real cutover in the packaging test.

---

# Phase 4 — macOS release artifact

macOS is a supported product platform, so packaging must be explicit even though Linux remains the first privileged networking acceptance platform.

## 4.1 Build outputs

On macOS build:

- Qt `Kikimora.app` for the runner architecture;
- `kikimora-core`;
- `kikimora-toad`.

Support both intended macOS architectures:

- arm64;
- x86_64;

either as separate artifacts or a deliberate universal-binary strategy.

Do not claim universal binaries unless both slices are actually present.

## 4.2 Package format

Create one canonical installable artifact, preferably:

```text
kikimora-<VERSION>-macos-<ARCH>.pkg
```

Optionally also produce a `.tar.gz`/`.dmg` for manual distribution, but `.pkg` is the installed-host contract.

The package must contain:

- `Kikimora.app`;
- core/toad binaries;
- launchd plist for core;
- CLI entry point/alias;
- required support/config directories.

Do not bundle protocol secrets.

## 4.3 Fix current macOS installer drift

Audit and fix `macos/install.sh`:

- resolve the missing `kikimora-macos` reference;
- ensure it installs the current Go core/toad path rather than only legacy orchestration;
- ensure launchd plist points to packaged binary paths;
- ensure install/upgrade is idempotent;
- do not automatically take production routing ownership.

If `macos/install.sh` becomes obsolete after `.pkg` creation, convert it to a thin developer/source wrapper rather than leaving a conflicting installer.

## 4.4 macOS package smoke

In macOS CI:

- build package;
- inspect package payload;
- verify plist syntax with `plutil -lint`;
- verify app bundle exists and Qt binary links resolve;
- run core/toad help/version commands;
- verify launchd plist ProgramArguments point to packaged paths;
- verify package contains no secrets/test credentials.

Do not require privileged live VPN networking for 07F. That belongs to a later macOS privileged acceptance packet.

---

# Phase 5 — Windows packaging scaffold only

Create a non-release-blocking `windows/packaging/` scaffold.

Allowed contents:

- package manifest/layout description;
- build script that stages `kikimora-ui.exe`, and if buildable, `kikimora-core.exe`/`kikimora-toad.exe`;
- WiX/MSIX/ZIP design skeleton;
- CI syntax/build check behind an explicit disabled/experimental flag.

Do **not**:

- install a Windows service;
- install TAP/Wintun drivers;
- claim networking support;
- publish Windows artifact as supported release;
- make Windows packaging block Linux/macOS release.

Add roadmap text:

`Windows packaging scaffold present; activation deferred until Windows networking ownership is designed and accepted.`

---

# Phase 6 — release CI

Create a dedicated workflow, e.g.:

`.github/workflows/release-packaging.yml`

Required PR jobs:

### Linux

- build canonical release artifact;
- run package contract tests;
- upload `.deb`, tarball and checksums as CI artifacts.

### macOS

- matrix arm64/x86_64 if runner/tooling permits;
- build app/core/toad;
- build/inspect package;
- upload package artifact.

### Windows

- optional experimental staging/build job only;
- `continue-on-error` or workflow-dispatch-only until Windows support is activated;
- do not publish as supported release.

Release/tag publishing is a separate action from PR artifact creation.

Do not automatically create a GitHub Release from every PR.

---

# Phase 7 — reproducibility and version contract

All release artifacts must derive version from root `VERSION`.

Remove conflicting hard-coded versions such as installer-local product version constants where they represent Kikimora release version.

Require:

- core reports release version;
- toad reports release version where supported;
- UI package version matches;
- package metadata matches;
- filenames match;
- upgrade logic compares the same product version source.

Generate SHA-256 checksums for distributable artifacts.

If signing/notarization credentials are unavailable:

- keep signing/notarization as explicit pending release gate;
- do not fabricate signatures;
- package build/test still must be deterministic.

---

# Phase 8 — gate 08A on a concrete artifact

Update `08a-linux-installed-host-staging.md` Gate 0.

Add prerequisites:

- 07F Linux packaging green;
- exact package artifact filename recorded;
- SHA-256 recorded;
- artifact came from the same commit/HEAD being staged;
- package contract test green.

08A installation must start from the `.deb` artifact, not from source checkout.

Required installed-host sequence before cutover:

```bash
sudo dpkg -i ./kikimora_<VERSION>_<ARCH>.deb
sudo kk verify
sudo kk orchestration preflight
```

If package dependencies require `apt`, document the exact supported invocation without silently replacing the artifact.

Do not perform Go cutover as part of package installation.

---

# Phase 9 — acceptance commands

Linux:

```bash
bash desktop/tests/test_packaging.sh
bash linux/package.sh   # only if retained as wrapper/compatibility test
test -f dist/kikimora_*.deb
dpkg-deb --info dist/kikimora_*.deb
dpkg-deb --contents dist/kikimora_*.deb
```

macOS CI:

```text
build Qt app
build Go core/toad
build pkg
pkgutil --check-signature <pkg>   # informational until signing enabled
pkgutil --payload-files <pkg>
plutil -lint packaged launchd plist
```

Cross-platform Go compile boundary:

```bash
cd toad
GOOS=linux GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
GOOS=darwin GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
GOOS=darwin GOARCH=arm64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
GOOS=windows GOARCH=amd64 go build ./cmd/kikimora-core ./cmd/kikimora-toad
```

If a Windows backend intentionally cannot build yet, record exact symbol/build-tag evidence and keep Windows scaffold disabled. Do not weaken Linux/macOS acceptance.

---

# Completion criteria

07F is complete only when:

1. there is one canonical Linux package payload;
2. Linux `.deb` contains UI + core + toad + production integration files;
3. Linux artifact contract test is green in CI;
4. macOS produces an installable package containing UI + core + toad + launchd integration;
5. macOS package inspection/smoke is green;
6. all artifact versions come from root `VERSION`;
7. checksums are generated;
8. Windows packaging scaffold exists but remains explicitly disabled/experimental;
9. 08A is updated to require the exact Linux artifact and checksum.

Signing/notarization may remain a separately recorded release blocker if credentials are not available, but unsigned local package smoke must still pass.

---

# STOP/DESIGN conditions

Stop and write a focused sub-plan if:

1. Linux package ownership paths cannot be unified without changing installed CLI/service paths;
2. desktop and system package dependency sets conflict materially;
3. macOS Go Toad/core cannot be packaged because a required platform adapter is missing;
4. macOS launchd lifecycle requires a privilege/helper architecture not yet designed;
5. Windows cross-build requires networking code that is not build-tag isolated;
6. package upgrade would overwrite user configs/secrets/desired state.

Do not solve these by deleting preservation/rollback semantics.

---

# Executor report

Return exactly:

```text
HEAD before:
HEAD after:

Canonical packaging architecture:
- shared manifest:
- retired/thin-wrapper old builders:

Linux:
- artifact names:
- UI included:
- core/toad included:
- systemd/CLI/NM/ownership included:
- package tests:
- install smoke:
- SHA256:

macOS:
- artifact names:
- architectures:
- app/core/toad included:
- launchd/CLI included:
- package tests:
- signing/notarization status:

Windows scaffold:
- files added:
- cross-build result:
- release publishing enabled: NO

CI:
- workflow:
- Linux job:
- macOS job:
- Windows experimental job:

08A integration:
- exact artifact prerequisite added:
- source-checkout install prohibited:

Unresolved:
- none OR exact file/function/package contract + evidence
```
