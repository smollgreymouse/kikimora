# 08A Gate 0 evidence — 2026-09-25

Accepted staging commit:

`846335d18a71e9da81211b319f6831acf3ffba24`

This is the exact commit that passed all four authoritative privileged gates in one operator run:

- route-parking PASS;
- multi-toad PASS;
- orchestration-acceptance phases A-F PASS;
- `=== ALL PHASES PASSED ===`;
- hermetic Xray interop PASS;
- final fixture cleanup complete;
- host state unchanged PASS.

## Exact Linux artifact history

The canonical release artifact was built from clean privileged-accepted commit `846335d` and passed the release package contract:

`dist/08a-846335d/kikimora_1.0.0_amd64.deb`

SHA-256:

`b23f98df3a5e5b47065f5d7c7d9ac663ac650be63640a72652b9f235c4f622a1`

However, installed-host inspection found that this host already has an unmanaged legacy Kikimora under `/usr/local`, and the canonical package contains the same `kk`, `kikimora` and CLI-lib paths. Therefore this canonical artifact is **accepted as test/release evidence but revoked as an 08A installation artifact on this host**.

The safe installed-host artifact is the side-by-side package introduced by `57e97c8`:

`dist/08a-next-57e97c8/kikimora-next_1.0.0_amd64.deb`

SHA-256:

`1fb8e3b27a315c4162484a88846ad0b6427f69dd4c4a5da0ab5da703689cc451`

It is built from a clean `git archive 57e97c8`. The `toad/` source tree is unchanged from `846335d`; the new commit adds only isolated staging package/test assets.

The side-by-side package collision test passes and explicitly proves absence of:

- `/usr/local/bin/kk`;
- `/usr/local/sbin/kikimora`;
- `/usr/local/libexec/kikimora`;
- `/etc/kikimora`;
- `/usr/lib/systemd/system/kikimora-core.service`.

See `08a1-side-by-side-installed-staging.md` for the complete installed-host safety contract.

## Read-only installed-host baseline

No live mutation has been performed.

Observed before package installation:

- Debian package `kikimora`: not installed;
- `kikimora-core.service`: not installed/inactive;
- legacy `leshy.service`: active and enabled;
- legacy `leshy-route-watch.service`: active and enabled;
- legacy `leshy-health-watch.service`: active;
- `/etc/kikimora/leshy/orchestration-ownership.conf`: absent;
- `/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf`: absent;
- legacy Leshy configuration and rollback assets are present;
- existing route table 51890 and legacy endpoint rules are active;
- current host has live `vpn0`, `amn0`, and `leshy-dns0` interfaces.

The host therefore remains under legacy ownership. Phase 0.5 package installation is the next mutation boundary and requires explicit operator authorization.

## Read-only install simulation and rollback readiness

The exact `.deb` also passes both read-only install simulations:

- `dpkg --no-act --install ...` succeeds;
- `apt-get -s install ./...deb` succeeds and requires no additional package changes beyond installing `kikimora`.

Package inspection confirms:

- ownership defaults remain `routing_owner="legacy"`, `tunnel_owner="external"`, `endpoint_owner="legacy"`;
- the package installs the `kk-*` NetworkManager unmanaged rule;
- `kikimora-core.service` points at `/etc/kikimora/toads`, the legacy ownership file and `/var/lib/kikimora/core`;
- postinst creates directories/default ownership and reloads systemd only;
- postinst does not enable/start Go VPN ownership or perform cutover.

Legacy rollback assets are currently present: `leshy`, `reconcile`, `route-lifecycle`, `route-watch`, `health-watch`, and all three legacy systemd units.

## Real-profile preparation

Read-only host inspection confirms the intended 08A protocol scope exists today:

- primary legacy interface: `amn0` (Amnezia/AWG);
- secondary legacy interface: `vpn0` (NetworkManager OpenConnect).

The exact-package `kikimora-toad` binary was used to prepare and validate the replacement profiles under the untracked, mode-0700 `build/08a-private/profiles` directory.

### AWG

The current Amnezia application QSettings contains five configured servers with `defaultServerIndex=4`. That active default entry contains one AWG `last_config`; its embedded WireGuard/AWG INI resolves to the same currently selected primary endpoint `72.56.119.116:31912` and client IPv4 `10.8.1.1`.

The staged Go-owned profile uses:

- role `awg`;
- interface `kk-awg0` rather than the externally owned live `amn0`;
- observed live addresses `10.8.1.1/32` and `fd58:baa6:dead::1/64`;
- MTU 1376 from the active Amnezia profile;
- Leshy zone `primary`;
- static endpoint policy `/etc/kikimora/leshy/endpoints/primary.txt`;
- rule priority 50.

Private key, PSK and AWG obfuscation material remain only in the mode-0600 untracked TOML. The exact-package validator reports PASS.

### OpenConnect

The active NetworkManager connection is `SberAds`, protocol AnyConnect, gateway `ve.ad-tech.ru`, with TOTP enabled. NetworkManager stores the username/auth group and TOTP seed but does **not** store the password.

The staged Go-owned profile uses:

- role `oc`;
- interface `kk-oc0` rather than the externally owned live `vpn0`;
- Leshy zone `secondary`;
- static endpoint policy `/etc/kikimora/leshy/endpoints/secondary.txt`, which contains `ve.ad-tech.ru`;
- rule priority 51;
- root-only future secret paths for password and TOTP.

The TOTP seed has been copied only into an untracked mode-0600 staging secret. The OpenConnect password remains an explicit operator credential input and has not been recovered, printed or stored by the executor. The exact-package validator reports PASS.

The two profiles also pass combined endpoint-policy validation: zones/priorities are unique and preserve the existing primary/secondary routing contract while allowing NetworkManager's `kk-*` unmanaged rule to protect the new Go-owned interfaces.

## Prepared Phase 0.5 operator script

The obsolete canonical installer `build/08a-private/08a-phase05-install.sh` is now fail-closed and exits before sudo because that package would overwrite the legacy control plane.

The safe installer is:

`build/08a-private/08a-phase05-install-next.sh`

It passes `bash -n` and full ShellCheck and performs only:

1. exact `kikimora-next` SHA verification;
2. package payload forbidden-path scan;
3. interactive sudo authentication;
4. pre-install hashing of legacy `kk`, `kikimora` and the full legacy libexec tree;
5. side-by-side installation of `kikimora-next`;
6. assertion that the candidate service stayed inactive and disabled;
7. assertion that all legacy hashes, symlink target and service states are unchanged;
8. root-only installation of candidate AWG/OC configs and secrets under `/etc/kikimora-next`;
9. candidate-config validation;
10. candidate-only `kk-next orchestration preflight`;
11. post-install evidence capture under `build/08a-private`.

It cannot perform cutover because `kk-next` deliberately rejects cutover/rollback/retirement at this stage.

## NetworkManager observation note

On this installed NetworkManager version the read-only field is `GENERAL.NM-MANAGED`, not `GENERAL.MANAGED`. The 08A packet has been updated accordingly.

## Operator boundary

The operator has authorized continuing into Phase 0.5, but only through the side-by-side staging path.

The prepared next command is:

`bash build/08a-private/08a-phase05-install-next.sh`

It will prompt interactively for:

1. the host sudo password;
2. the OpenConnect password that NetworkManager does not persist.

After that script succeeds, only candidate install/preflight evidence is complete. Real ownership cutover remains blocked because the currently installed legacy `route-watch`, `route-lifecycle` and `reconcile` scripts predate the ownership-file mechanism used by the generic repository cutover tests.

Still do **not** run:

- the obsolete canonical Phase 0.5 installer;
- `sudo kk orchestration cutover --go`;
- any candidate cutover command;
- real suspend/resume;
- `retire-legacy --confirm`.

A host-specific cutover/rollback packet is required after side-by-side install evidence is reviewed.
