# Toad step 08A.1 — side-by-side installed-host staging

Status: **DEFERRED LINUX DEPLOYMENT BRANCH / READ-ONLY PREFLIGHT COMPLETE**. Resume only on explicit operator request to deploy/cut over the developer workstation.

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

`kk-next` is currently deliberately restricted to:

- `version`;
- `orchestration status`;
- `orchestration preflight`.

That restriction remains appropriate for the **developer workstation staging package**, because read-only host audit found the installed legacy `route-watch`, `route-lifecycle` and `reconcile` scripts predate the ownership-file mechanism. Generic workstation cutover is not authorized.

However, this restricted surface is **not** the chapter-08 product CLI. Under `08a2-vm-console-lifecycle-acceptance.md`, the disposable VM must receive a package-built candidate exposing the full safe console orchestration surface against its candidate core socket. The final chapter-08 `kk` CLI must control Toad desired state, expose JSON status/watch, retries/restarts and diagnostics without source-tree scripts. Workstation mutation commands may remain separately guarded until the host-specific cutover contract is closed.

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

The side-by-side package remains the only authorized workstation mutation, but it is intentionally paused while current-HEAD acceptance is repeated on the disposable Ubuntu VM.

VM-first order:

1. privileged and standalone real-protocol prechecks — **complete**;
2. execute `08a2-vm-console-lifecycle-acceptance.md` using an installed package-built candidate;
3. close the full console/state-channel contract and OS lifecycle recovery on the VM;
4. close package install/upgrade/remove/reinstall and produce the final console/runtime `.deb`;
5. review VM evidence;
6. only then return to the prepared workstation side-by-side install:

`bash build/08a-private/08a-phase05-install-next.sh`

That workstation install requires interactive sudo and the OpenConnect password.

Do **not** run:

- the obsolete canonical Phase 0.5 installer;
- `kk orchestration cutover --go`;
- `kk-next orchestration cutover --go` (the staging CLI refuses it anyway);
- workstation suspend/resume;
- legacy retirement.

A separate installed-host cutover packet is required after VM acceptance and side-by-side install/preflight evidence are reviewed.

## Refreshed current-runtime staging candidate — 2026-10-04

The original `57e97c8` side-by-side artifact above is historical evidence only.
Do not install that old package on the workstation.

After 08A.2/08A.3 closure, the staging package was rebuilt from current accepted
source `36e0366` (runtime code includes the accepted suspend/underlay fix from
`a4055e6`; later commits are acceptance/docs/harness changes).

Current side-by-side artifact:

`dist/08a-next-36e0366/kikimora-next_1.0.0_amd64.deb`

SHA-256:

`a4b364de54b9f0962726371b33746e96aa021466fa7de080f06ad4b91f4d7119`

The current `kk-next` is no longer status/preflight-only. It exposes the safe
accepted runtime console surface against the isolated candidate socket:

- status / status --json;
- watch / watch --json;
- connect / disconnect;
- retry;
- restart;
- start / stop;
- interfaces;
- profiles.

It still deliberately refuses workstation ownership cutover, rollback,
retirement and legacy maintenance.

Read-only workstation preflight on 2026-10-04:

- staging package contract test: PASS;
- exact SHA verification: PASS;
- forbidden legacy-path payload scan: PASS;
- legacy `/usr/local/sbin/kikimora` SHA unchanged:
  `e9336f483fca9a0bb3780798e12e454fbc83a83bead4bc0aa592c28362ec73a5`;
- legacy `/usr/local/bin/kk` still resolves to
  `/usr/local/sbin/kikimora`;
- current legacy libexec manifest hash recorded as
  `23357aeb3007c9f478d049fd68fe8e62b9920414019fc048c0112f1bb415c2ea`;
- all candidate install paths absent before installation: PASS;
- AWG candidate profile validates with the extracted current Toad: PASS;
- OpenConnect candidate profile validates with the extracted current Toad: PASS;
- private installer bash syntax + ShellCheck: PASS;
- `dpkg --no-act`: PASS;
- `apt-get -s install`: only `kikimora-next 1.0.0` would be newly installed;
- staging postinst activation scan: no service start/enable and no cutover.

The untracked operator installer
`build/08a-private/08a-phase05-install-next.sh` has been updated locally to
this exact artifact and SHA. It is intentionally not committed because it
references private local profiles/secrets.

### 2026-10-08 legacy-host network regression input

A real read-only analysis of the still-installed legacy Kikimora stack found a
healthy physical/IPv4 network coexisting with a dead IPv6 path: legacy routing
had captured IPv6 with `::/1` + `8000::/1` through a VPN interface while DNS
continued to return AAAA answers. The same observation also showed competing
route/DNS scopes and continuous host-route churn (about 70 route additions per
minute in the sample).

This is not evidence against the accepted `kikimora-next` package because that
candidate has not taken workstation ownership. It is now the regression input
for `08-dual-stack-dns-route-stability.md`.

Before any later workstation **ownership cutover**, require that packet against
the real host. In particular, prove that the new owner removes the dead-family
capture/AAAA timeout condition, has deterministic DNS ownership and converges to
zero route/DNS mutation for unchanged desired state. Side-by-side installation
while the candidate remains inactive must itself leave the legacy route/DNS
state untouched.

### Current operator boundary

The next action is the first workstation mutation:

`bash build/08a-private/08a-phase05-install-next.sh`

That action installs only the isolated `kikimora-next` package plus candidate
configs/secrets. It must leave `kikimora-core-next.service` inactive/disabled
and must not change legacy Kikimora/Leshy ownership.

Do not run it without explicit operator authorization. Ownership cutover,
workstation suspend/resume and legacy retirement remain later, separate
operator-gated boundaries.

## Disposable-VM side-by-side coexistence proof — 2026-10-04

Before touching the developer workstation, the refreshed staging package was
installed beside the accepted canonical package on the disposable Ubuntu VM.

Installed together:

- `kikimora 1.0.0`;
- `kikimora-next 1.0.0`.

Result:

- canonical `kikimora-core.service`: active/enabled;
- staging `kikimora-core-next.service`: inactive/disabled;
- canonical `kk version`: `Kikimora 1.0.0`;
- staging `kk-next version`: `Kikimora next 1.0.0`;
- canonical AWG + OpenConnect remained Ready/current epoch;
- no-accumulation assertion: PASS;
- canonical Telegram / ChatGPT trace / OpenAI API / internal GitLab probes:
  PASS;
- `kk-next status --json` failed closed against
  `/run/kikimora-next/core.sock` because next-core was intentionally not
  started; it did not attach to the canonical core socket.

This proves package/path/socket coexistence before the workstation install
boundary. It does not replace the real workstation staging/cutover acceptance.
