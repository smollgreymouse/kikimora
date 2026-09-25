# Toad step 08A.1 — side-by-side installed-host staging

Status: **IMPLEMENTED; SIDE-BY-SIDE PACKAGE READY; LIVE INSTALL STILL REQUIRES INTERACTIVE SUDO + OC PASSWORD**.

This packet supersedes direct installation of the canonical `kikimora_1.0.0_amd64.deb` on the current workstation.

## Why the canonical package is unsafe on this host

Read-only installed-host inspection found an existing unmanaged legacy Kikimora installation:

- `/usr/local/bin/kk -> /usr/local/sbin/kikimora`;
- `/usr/local/sbin/kikimora` is the active legacy shell CLI;
- `/usr/local/libexec/kikimora/cli/*` contains the legacy CLI implementation;
- those paths are not owned by dpkg.

Current legacy CLI content SHA-256:

`e9336f483fca9a0bb3780798e12e454fbc83a83bead4bc0aa592c28362ec73a5`

The canonical 1.0.0 package contains exactly those paths:

- `/usr/local/bin/kk`;
- `/usr/local/sbin/kikimora`;
- `/usr/local/libexec/kikimora/cli/*`.

Therefore `dpkg -i kikimora_1.0.0_amd64.deb` would overwrite the operator's rollback/control CLI even before any orchestration cutover. That is unacceptable for 08A.

The previously prepared canonical Phase 0.5 installer is now fail-closed and exits before sudo.

## Side-by-side package

Implementation commit:

`57e97c8` — `packaging(toad): add side-by-side 08A staging package`.

Artifact built from a clean `git archive 57e97c8`:

`dist/08a-next-57e97c8/kikimora-next_1.0.0_amd64.deb`

SHA-256:

`1fb8e3b27a315c4162484a88846ad0b6427f69dd4c4a5da0ab5da703689cc451`

Package name:

`kikimora-next`

The Go `toad/` source tree is unchanged from privileged-accepted commit `846335d`; `git diff --name-only 846335d..57e97c8 -- toad` is empty. The staging commit changes only side-by-side packaging/test assets.

## Filesystem isolation

The staging package installs only into new paths:

- `/opt/kikimora-next/bin/kikimora-core`;
- `/opt/kikimora-next/bin/kikimora-toad`;
- `/opt/kikimora-next/bin/kk-next`;
- `/opt/kikimora-next/libexec/*`;
- `/usr/local/bin/kk-next`;
- `/usr/lib/systemd/system/kikimora-core-next.service`;
- `/usr/share/kikimora-next/*`;
- `/etc/NetworkManager/conf.d/90-kikimora-next-unmanaged.conf`;
- postinst-created `/etc/kikimora-next/*`.

The package does **not** contain:

- `/usr/local/bin/kk`;
- `/usr/local/sbin/kikimora`;
- `/usr/local/libexec/kikimora`;
- `/etc/kikimora`;
- `/usr/lib/systemd/system/kikimora-core.service`.

Live-host collision scan found no pre-existing candidate path.

## Candidate runtime isolation

`kikimora-core-next.service` uses:

- candidate core/toad under `/opt/kikimora-next`;
- config dir `/etc/kikimora-next/toads`;
- ownership config `/etc/kikimora-next/orchestration-ownership.conf`;
- socket `/run/kikimora-next/core.sock`;
- state dir `/var/lib/kikimora-next/core`;
- candidate endpoint-provider dir under `/opt/kikimora-next`.

The service is not enabled or started by package postinst.

## kk-next safety boundary

`kk-next` is deliberately restricted to:

- `version`;
- `orchestration status`;
- `orchestration preflight`.

It refuses cutover, rollback, retirement, install/uninstall and maintenance commands.

This is intentional because read-only host audit found the currently installed legacy `route-watch`, `route-lifecycle` and `reconcile` scripts predate the ownership-file mechanism. A real cutover must therefore receive a separate host-specific migration/rollback design; generic repository cutover is not yet authorized on this host.

## Package verification

PASS:

- full ShellCheck;
- bash syntax;
- side-by-side package collision test;
- canonical package regression test;
- rootless model suite;
- exact candidate profile validation.

The collision test asserts both required candidate paths and absence of all critical legacy paths.

## Prepared real profiles

Private untracked staging lives under:

`build/08a-private/`

Never use `.gigacode` for these files.

Candidate profiles are now isolated too:

- AWG state under `/run/kikimora-next/toads/awg`;
- OpenConnect state under `/run/kikimora-next/toads/oc`;
- OpenConnect secrets under `/etc/kikimora-next/secrets`.

Both profiles validate with the exact `kikimora-next` Toad binary.

## Safe Phase 0.5 installer

Prepared untracked script:

`build/08a-private/08a-phase05-install-next.sh`

It performs:

1. exact candidate SHA verification;
2. package payload forbidden-path check;
3. snapshots legacy `kk`, `kikimora` and full legacy libexec hashes;
4. snapshots legacy service states;
5. installs only `kikimora-next`;
6. requires candidate service to remain inactive and disabled;
7. requires all legacy hashes and service states to remain unchanged;
8. installs only candidate configs/secrets under `/etc/kikimora-next`;
9. validates candidate configs;
10. runs candidate-only orchestration preflight;
11. writes evidence under `build/08a-private`.

It performs no ownership cutover.

## Next operator boundary

The only currently authorized live mutation is side-by-side package installation:

`bash build/08a-private/08a-phase05-install-next.sh`

This requires interactive sudo and the OpenConnect password.

Do **not** run:

- the obsolete canonical Phase 0.5 installer;
- `kk orchestration cutover --go`;
- `kk-next orchestration cutover --go` (the staging CLI refuses it anyway);
- suspend/resume;
- legacy retirement.

A separate installed-host cutover packet is required after side-by-side install/preflight evidence is reviewed.
