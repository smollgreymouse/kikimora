# Toad step 08A.3 — final Linux console/runtime package

Status: **COMPLETE / FINAL CHAPTER-08 LINUX CONSOLE PACKAGE ACCEPTED**.

Purpose: turn the accepted chapter-08 runtime into the actual Linux installation artifact.

This packet supersedes the older 07F assumption that the Qt desktop UI must be inside the same release-blocking `.deb`. Chapter 08 is console/runtime only. UI packaging moves to chapter 09.

## Final chapter-08 artifact

Required output:

```text
dist/kikimora_<VERSION>_<ARCH>.deb
dist/kikimora-<VERSION>-linux-<ARCH>.tar.gz   # optional/support artifact if retained
dist/SHA256SUMS
```

The `.deb` is the canonical Linux install artifact.

Required payload:

- `kikimora-core`;
- `kikimora-toad`;
- `kk` / canonical console launcher;
- CLI support libraries needed by the accepted console surface;
- endpoint providers;
- systemd core unit;
- tmpfiles/sysusers integration;
- orchestration ownership template;
- NetworkManager unmanaged rule;
- configuration directory/bootstrap contract;
- version/release metadata;
- diagnostics support.

Not part of the chapter-08 package:

- Qt libraries;
- `kikimora-ui`;
- desktop entry/icon;
- UI-only resources;
- test fixtures;
- real VPN secrets;
- `ocserv`.

Preferred later split:

```text
kikimora        -> this packet
kikimora-ui     -> chapter 09 optional UI package
```

## Packaging implementation direction

Use `packaging/linux/stage-release.sh` and `packaging/linux/build-release.sh` as the canonical payload/build path.

Required changes from the current staging payload:

1. remove desktop entry/icon from the console package;
2. remove Qt runtime dependencies from DEBIAN control;
3. describe the package as the Kikimora console control plane/runtime;
4. ensure the complete accepted `kk` console is installed;
5. keep source/developer installers as wrappers/bootstrap only; they must not define a competing payload;
6. ensure systemd starts only package-owned binaries;
7. preserve config, secrets and desired state across upgrade;
8. never perform orchestration cutover merely because a package was installed/upgraded.

The side-by-side `kikimora-next` artifact remains a workstation migration tool. It is not the final product package name.

## Console/API package contract

After installation, without a source checkout, require:

```bash
kk version
kk status
kk status --json
kk watch --json
kk connect ...
kk disconnect ...
kk retry ...
kk restart ...
kk diagnostics   # or the final accepted equivalent
```

The package must expose the same versioned local API consumed by those commands:

- Handshake/capabilities;
- Snapshot;
- Subscribe;
- structured commands/errors;
- monotonic revision.

Future UI compatibility is therefore a package/API invariant, not a Qt build requirement.

## VM package acceptance

Run on the disposable VM:

### Fresh install

- install with the supported apt/dpkg path;
- verify package ownership of every installed product path;
- verify service unit ExecStart points to package-owned core;
- verify service policy (enabled/disabled) matches the documented install contract;
- install local test configs/secrets separately;
- start and operate the product only through installed paths.

### Upgrade

Build a second candidate version/HEAD and upgrade.

Require:

- configs preserved;
- secrets preserved;
- desired state preserved;
- service recovers;
- state API schema/version remains compatible according to its declared contract;
- desired AWG/OpenConnect roles reconverge.

### Remove / purge

Define and test the distinction:

- remove: package binaries/units removed while documented persistent admin/user state is preserved;
- purge: remove package-owned config/state according to explicit policy, never unrelated legacy/user files.

### Reinstall

Reinstall the package and prove a deterministic supported state without manual cleanup of stale package-owned files.

## Package tests

Required static assertions:

- package VERSION == root `VERSION`;
- no Qt dependency in console package;
- no desktop/UI payload;
- no test secret/profile included;
- core/toad/kk executable modes correct;
- service/config modes correct;
- service ExecStart matches packaged core path;
- console socket/config paths match the installed service;
- required endpoint providers present;
- package does not own legacy rollback paths unexpectedly;
- no duplicate core/toad binaries in competing prefixes.

Required dynamic assertions are the relevant 08A.2 lifecycle gates run against the installed package.

## Completion

08A.3 is complete when:

- exact `.deb` filename and SHA-256 are recorded;
- package static contract is green;
- fresh VM install is green;
- installed console controls real core-managed AWG/OpenConnect;
- 08A.2 lifecycle gates are green from installed binaries;
- upgrade is green;
- remove/purge/reinstall policy is green;
- no Qt/UI dependency is required;
- the state/control API needed by chapter 09 remains available.

The accepted `.deb` then becomes the only artifact eligible for workstation staging/cutover.
## Recorded final artifact — 2026-10-04

Accepted source HEAD:

`66a9a93`

Canonical package:

`dist/08a-final-66a9a93/kikimora_1.0.0_amd64.deb`

SHA-256:

`758156aff047c55392575298ce2c08fa55f9fb02d3661ecc0e3a24c45712d75c`

Support tarball SHA-256:

`98ceb69623d01f36ceea382b9790df69f1ae847723aeca8bca82f8396000389b`

Recorded acceptance:

- static chapter-08 package contract: PASS;
- fresh install semantics: PASS;
- package install does not silently connect VPN roles: PASS;
- real package upgrade with running core: PASS;
- remove/reinstall with preserved admin config/secrets/desired state: PASS;
- purge/reinstall with package-owned desired-state reset: PASS;
- final canonical package installed on disposable VM as
  `kikimora 1.0.0 amd64`: PASS;
- `kikimora-core.service` active/enabled: PASS;
- installed `kk` reports `Kikimora 1.0.0`: PASS;
- AWG + OpenConnect Ready/current epoch: PASS;
- no-accumulation assertion: PASS;
- Telegram / ChatGPT trace / unauthenticated OpenAI API / internal GitLab real
  probes: PASS;
- no Qt/UI dependency required.

The temporary system-sleep evidence hook used for VM acceptance was removed
after the final package smoke. The accepted package is now the only chapter-08
Linux artifact eligible for workstation staging/cutover.
