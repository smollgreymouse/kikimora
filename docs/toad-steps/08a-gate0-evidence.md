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

## Exact Linux artifact

The staging artifact was built from a clean `git archive 846335d`, not from the later proof/documentation working tree.

Artifact:

`dist/08a-846335d/kikimora_1.0.0_amd64.deb`

SHA-256:

`b23f98df3a5e5b47065f5d7c7d9ac663ac650be63640a72652b9f235c4f622a1`

Portable tarball:

`dist/08a-846335d/kikimora-1.0.0-linux-amd64.tar.gz`

SHA-256:

`7a67fa69b0866cd609ab278b10fa9657651fdb65e789b2053187b4f42af02ce2`

`sha256sum -c SHA256SUMS` passes for both artifacts.

The exact-tree packaging test also passes completely, including:

- package metadata/version/architecture;
- complete system-integration payload;
- systemd unit path;
- CLI/kk contract;
- NetworkManager unmanaged config;
- ownership template;
- maintainer scripts without automatic cutover;
- installed binary version commands;
- OpenConnect dependency;
- secret scan;
- tarball contents.

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

`build/08a-private/08a-phase05-install.sh` is prepared and passes `bash -n` and full ShellCheck.

It performs only:

1. exact artifact SHA verification;
2. interactive sudo authentication;
3. installation of the verified `.deb`;
4. root-only installation of the prepared AWG/OC TOMLs and TOTP secret;
5. hidden interactive capture of the otherwise-unsaved OpenConnect password into a root-only secret file;
6. installed-config validation;
7. `kk verify` and `kk orchestration preflight`;
8. post-install evidence capture.

It does **not** perform `cutover --go`, suspend/resume or legacy retirement.

## NetworkManager observation note

On this installed NetworkManager version the read-only field is `GENERAL.NM-MANAGED`, not `GENERAL.MANAGED`. The 08A packet has been updated accordingly.

## Operator boundary

The operator has authorized continuing into Phase 0.5, but the executor cannot satisfy interactive sudo authentication through CTUN.

The prepared next command is therefore:

`bash build/08a-private/08a-phase05-install.sh`

It will prompt interactively for:

1. the host sudo password;
2. the OpenConnect password that NetworkManager does not persist.

After that script succeeds, the executor can continue with installed-host verification and Phase 1/2 evidence.

Still do **not** run without a separate explicit authorization:

- `sudo kk orchestration cutover --go`;
- real suspend/resume;
- `retire-legacy --confirm`.
