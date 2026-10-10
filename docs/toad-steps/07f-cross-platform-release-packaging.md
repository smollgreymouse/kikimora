# Toad step 07F — cross-platform release packaging

Status: **BASE RELEASE/PACKAGING INFRASTRUCTURE COMPLETE; CHAPTER-08 CONSOLE PACKAGE REFINEMENT MOVED TO 08A.3**.

Purpose: provide the cross-platform packaging foundation. For the current Linux product boundary, `08a3-linux-console-package.md` supersedes the older UI-inclusive Linux payload assumptions in this document: chapter 08 ships console/runtime first; Qt/UI packaging is chapter 09.

Implementation of the canonical builders landed at `5e9ce117eb36353193ec8d67652538a6ffc185e6`. Follow-up audit found install-contract and macOS portability gaps; execute `07f1-release-artifact-hardening.md` before 08A.

Supported release platforms in this packet:

- Linux: **required and release-blocking**;
- macOS: **required packaging/install contract**; privileged networking acceptance remains a separate macOS runtime gate;
- Windows: **scaffold only**; do not enable release publishing or networking acceptance yet.

08A must consume a package-built artifact, never an ad-hoc source checkout. The final Linux artifact accepted by 08A is now defined by `08a3-linux-console-package.md`; the canonical console/runtime `.deb` remains release-blocking while UI is not.

## Executor evidence policy

Do not use GitHub Actions as an execution gate. Run all available acceptance locally.

- On Linux: build and inspect the real Linux artifacts locally; Linux packaging proof is required before 08A.
- On a non-macOS executor host: implement the macOS package path, run static/plist/config checks and Go Darwin cross-builds locally, and report `macOS native package smoke pending on macOS host`.
- Do not wait for a GitHub macOS runner to continue Linux packaging or 08A.
- Windows remains scaffold-only and non-blocking.

A release workflow may be added/updated so the repository has automation, but the executor does not need to trigger, watch, or report its run.

---
---

## 0. Audit of current packaging

Current repository has overlapping/incomplete packaging paths:

### Linux path A — `linux/package.sh`

Already builds most of the chapter-08 console/runtime payload:

- `kikimora-core`;
- `kikimora-toad`;
- Linux CLI;
- systemd core unit;
- tmpfiles/sysusers files;
- ownership config;
- NetworkManager unmanaged rule;
- endpoint providers;
- `.deb` and rootfs-style `.tar.gz`.

The fact that it does not package the Qt desktop UI is **no longer a chapter-08 defect**. UI is deferred to chapter 09.

### Linux path B — `desktop/packaging/build-release.sh`

This remains useful for the future UI package/prototype, but it is not the chapter-08 release path because it lacks the full Linux console/system integration contract.

Therefore the chapter-08 canonical artifact must be derived from the console/runtime payload and finalized under `08a3-linux-console-package.md`; desktop packaging is no longer allowed to pull Qt dependencies into the release-blocking console package.

### macOS

`macos/install.sh` exists, but it is primarily the older Leshy/service adapter path. There is no equivalent release package that contains the current Go `kikimora-core`, `kikimora-toad` and Qt UI as one tested artifact.

Audit also found `macos/install.sh` references `kikimora-macos`, while no `macos/kikimora-macos` file exists at the reviewed HEAD. Treat this as a concrete packaging defect to resolve, not as documentation noise.

### Windows

There is no `windows/` packaging tree. Desktop CI builds on Windows, but Windows networking remains out of the current production acceptance path.

---

## Binding packaging decisions for the executor

For Linux chapter 08 these decisions supersede the older UI-inclusive payload direction:

- create one canonical console/runtime staging builder under `packaging/linux/`;
- preserve current production Linux Go/CLI paths unless 08A.3 records an explicit migration:
  - `/usr/local/bin/kikimora-core`;
  - `/usr/local/bin/kikimora-toad`;
  - `/usr/local/sbin/kikimora`;
  - `/usr/local/bin/kk`;
  - `/usr/local/libexec/kikimora/...`;
- preserve Linux service `ExecStart=/usr/local/bin/kikimora-core ...`;
- make `linux/package.sh` a thin wrapper/compatibility entry point over the canonical builder if retained;
- the release-blocking Linux `.deb` contains core + Toads + full console + system integration and **does not depend on Qt**;
- desktop/UI payload and desktop entry/icon belong to chapter 09 and should become an optional `kikimora-ui` package or equivalent;
- there must be one chapter-08 console/runtime `.deb` payload definition;
- Linux `.deb` is the artifact consumed by 08A;
- macOS/Windows packaging work remains separate and must not block Linux chapter-08 console acceptance.

Suggested concrete layout:

```text
packaging/
  linux/build-release.sh
  linux/stage-release.sh
  macos/build-release.sh
  macos/stage-release.sh
  common/version.sh
windows/
  packaging/README.md
  packaging/stage.ps1
```

`stage-release` scripts own the payload contents. Wrapper scripts may select/build prerequisites but must not duplicate the payload list.

---
# Phase 1 — define one canonical release payload contract

Create a machine-readable or shell/CMake-readable release manifest under a platform-neutral location, for example:

`packaging/release-manifest.txt`

or an equivalent implementation that has one source of truth.

The manifest must define logical components, not hard-coded staging commands.

Required chapter-08 Linux product components:

- `kikimora-core`;
- `kikimora-toad`;
- full `kk` / `kikimora` console;
- product version from repository `VERSION`;
- license/readme/release metadata;
- CLI library files under `/usr/local/libexec/kikimora/cli`;
- endpoint providers;
- `kikimora-core.service`;
- tmpfiles config;
- sysusers config;
- orchestration ownership template;
- `/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf`;
- any configuration templates required to start the Go-owned stack.

The chapter-08 Linux payload explicitly excludes `kikimora-ui`, desktop entry/icon and Qt runtime dependencies. Those become chapter-09 components.

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

Preferred direction for chapter 08:

- use `packaging/linux/stage-release.sh` + `packaging/linux/build-release.sh` as the canonical console/runtime builder;
- keep the complete system integration payload currently owned by the Linux packaging path;
- retire `linux/package.sh` as a second independent payload definition, or turn it into a thin wrapper around the canonical builder;
- keep CMake/CPack/desktop packaging for chapter 09 UI work rather than pulling UI/Qt into the chapter-08 artifact.

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
- **no Qt runtime dependency for the chapter-08 console/runtime package**;
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

Extend/replace the Linux package contract test so it tests the **complete chapter-08 console/runtime artifact**. The test may live outside `desktop/`; desktop packaging is no longer authoritative for this package.

Required `.deb` content assertions:

```text
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
```

Required absence assertions for chapter 08:

```text
kikimora-ui
desktop entry/icon
Qt-only runtime payload
real VPN secrets/test fixtures
```

Required assertions:

- package VERSION equals repository VERSION;
- package metadata has no Qt dependency for chapter 08;
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

# Phase 6 — release automation (executor remains local-test driven)

Create/update a dedicated workflow for repository automation, e.g.:

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

**Executor rule:** create/test the workflow syntax/config as practical, but do not query or wait for GitHub Actions. Local artifact builds/tests below are the execution gate.

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

07F has two completion dimensions:

### 07F-Linux — required before 08A

Linux is complete only when:

1. there is one canonical Linux package payload;
2. Linux chapter-08 `.deb` contains core + Toads + full console + production integration files and excludes UI/Qt payload;
3. Linux package contract and install-smoke commands are green **locally**;
4. all Linux artifact versions come from root `VERSION`;
5. SHA-256 checksums are generated;
6. 08A requires the exact Linux artifact and checksum.

### 07F-macOS — supported packaging target

macOS packaging implementation is complete when:

1. package builder/staging logic exists for UI + core + toad + launchd/CLI integration;
2. Darwin amd64/arm64 Go builds pass locally;
3. plist/package layout can be statically validated from the current host;
4. no source-tree-only/missing-file references remain.

Native macOS package build/install smoke must be run on a macOS host before calling the macOS release artifact validated. If the current executor is on Linux, report this explicitly; it does **not** block Linux 08A.

### Windows scaffold

Windows is complete for this packet when the packaging scaffold exists, cross-build status is recorded, and release publishing remains disabled/experimental.

Signing/notarization may remain a separately recorded release blocker if credentials are not available, but unsigned local package smoke must still pass.

---

# STOP/DESIGN conditions

Stop and write a focused sub-plan if:

1. Linux package ownership paths cannot be unified without changing installed CLI/service paths;
2. the console/runtime package cannot be made independent of desktop/Qt dependencies without changing the accepted control/runtime contract;
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
