# Toad step 08A — Linux installed-host staging and reversible cutover

Status: **VM PROTOCOL PRECHECK COMPLETE; 08A.2 CONSOLE/LIFECYCLE ACCEPTANCE IS CURRENT; WORKSTATION CUTOVER DEFERRED**.

Current Gate 0 evidence: `08a-gate0-evidence.md`.
Side-by-side workstation packet: `08a1-side-by-side-installed-staging.md`.
Current VM console/lifecycle packet: `08a2-vm-console-lifecycle-acceptance.md`.
Final Linux console package packet: `08a3-linux-console-package.md`.
Prepared local operator script: `build/08a-private/08a-phase05-install-next.sh` (untracked; contains no password/TOTP values).

**Do not install the canonical `kikimora_1.0.0_amd64.deb` on this host.** The existing legacy Kikimora is an unmanaged `/usr/local` installation and the canonical package collides with its `kk`, `kikimora` and CLI-lib paths. 08A Phase 0.5 now uses only the isolated `kikimora-next` package.

Current read-only preflight also confirms that legacy rollback assets are present and the live host currently uses `amn0` plus NetworkManager OpenConnect `vpn0`. Real AWG/OpenConnect Toad TOMLs are prepared and validated locally under `build/08a-private/profiles`, but are not installed yet. They must be installed and revalidated before any `cutover --go`.

## Current disposable-VM pre-staging lane

Before mutating the developer workstation, the disposable Ubuntu VM at `192.168.1.236` is used to close the installed **console/runtime product** end to end. Privileged and standalone real-VPN prechecks are already complete; the current work is core-orchestrated lifecycle and package acceptance.

Recorded VM state is current through the real-VPN application acceptance run at `6c01dcb7502e962a5ea5a212d098761435db6649`:

- Ubuntu 26.04.1 LTS, kernel `7.0.0-38-generic`;
- `enp0s3` at `192.168.1.236/24`, default gateway `192.168.1.1`;
- Go `1.26.0` plus Git/build tools, OpenConnect, ocserv/ocpasswd, slirp4netns, tcpdump, curl and OpenSSL installed;
- Toad/core and pinned AWG/Xray reference binaries build successfully;
- `real-vps-awg-link.secret`, `real-vps-vless-link.secret` and `real-vps-openconnect.secret` are present under `linux/tests/toad/`, user-owned and mode `0600`;
- clean `run-rootless.sh model` passes on the accepted runtime line;
- the flaky `TestCoreIPCUsesFakeToadBinaryWithoutNetwork` was fixed at `55d4782` by replacing accidental live host-network observation with injected deterministic dependencies; on the VM the fixed test passes `-race -count=20`;
- the VM privileged gate set is green 4/4;
- at `6c01dcb`, AWG passes Google/Telegram/ChatGPT/OpenAI application probes and OpenConnect reaches the internal GitLab endpoint;
- Xray carries real Google + Telegram traffic but the operator's provider network then exhibits the already-known Xray DPI block; that external limitation is non-blocking for the AWG + OpenConnect product scope.

The VM lane is now stronger than a protocol precheck. Execute `08a2-vm-console-lifecycle-acceptance.md` before returning to the workstation. Required order is:

1. build and install an exact package candidate in the VM;
2. make the installed `kk`/candidate console capable of real orchestration, not only status/preflight;
3. run real AWG + OpenConnect as core-managed Toad roles;
4. exercise process failure, NetworkManager restart, virtual NIC loss/restoration, OS suspend/resume, VM pause/save-state/reset, reboot, shutdown/start and cold boot;
5. prove the revisioned state channel survives/reconnects and exposes everything required by the future UI;
6. prove package install/upgrade/remove/reinstall behavior;
7. produce the final Linux console/runtime `.deb` and SHA-256;
8. only after 08A.2 PASS return to workstation side-by-side install/preflight and separate cutover authorization.

Execute only after 07E local acceptance is complete, 06A/07A-07D statuses are closed from recorded local evidence, and **07F-Linux packaging is complete for the exact staging HEAD**, including the privileged hermetic orchestration acceptance gate.

This packet is for a real installed Linux host. It is not an executor-autonomous task.

08A must use the exact Linux release artifact produced by 07F. Installing directly from a source checkout or ad-hoc locally built core/Toad binaries does not satisfy this packet.

The executor may prepare commands, collect read-only evidence and analyze results. It must not perform a real cutover, suspend the workstation, or retire legacy writers without explicit operator authorization in the live session.

---

## Goal

Prove that the already-tested Go-owned control plane can take ownership on the actual host while preserving an immediate rollback path.

This chapter ends with:

- a working installed console tool controlling the Go orchestrator and Toads;
- one canonical revisioned state/control channel usable by both CLI and the next UI chapter;
- real VM lifecycle recovery evidence before workstation cutover;
- a tested Linux console/runtime `.deb` built from the accepted HEAD;
- Go ownership running on the installed workstation after separate authorization;
- desired state surviving a real core restart;
- real workstation suspend/resume evidence if explicitly authorized;
- legacy writers still installed and recoverable until 08B;
- diagnostic archives suitable for 08B review.

It does **not** retire legacy components.

---

## Installed-host protocol scope

The minimum real-host staging scope for 08A is:

- AmneziaWG against the operator's real AmneziaWG endpoint;
- OpenConnect against the operator's real OpenConnect endpoint.

Xray does **not** block 08A when no real external Xray endpoint is available in the executor environment.

Xray policy:

- the mandatory automated Xray proof is the existing hermetic local fixture using pinned official `xray-core`, isolated client/server netns, generated ephemeral REALITY credentials, hermetic TLS cover, and a local payload server;
- do not replace that fixture with a protocol mock;
- on the installed host, keep an unvalidated Xray role disabled (`desired=false`) unless the operator explicitly supplies a real Xray profile/endpoint for this run;
- real external Xray/VPS validation is deferred to `08c-external-xray-validation.md` and is not a prerequisite for AmneziaWG + OpenConnect 08A completion.

Therefore 08A may be accepted for the AmneziaWG + OpenConnect production scope while 08C remains pending.

---
# Gate 0 — prerequisites

Before any live mutation require all of:

1. 07E status says automated proof complete and 06A/07A-07D are closed from current evidence;
2. 07F status says Linux release packaging is complete;
3. exact Linux artifact filename is recorded;
4. artifact SHA-256 is recorded;
5. artifact build commit/HEAD exactly matches the HEAD accepted for staging;
6. 07F Linux package contract/install-smoke commands are recorded green locally;
7. privileged hermetic commands are recorded green locally:
   - route-parking;
   - multi-toad;
   - orchestration-acceptance;
8. no unresolved STOP/DESIGN packet;
9. operator explicitly authorizes installed-host staging.

If any item is missing, stop after read-only preflight.

---

# Phase 0.5 — install the verified side-by-side staging artifact

This host has an unmanaged legacy Kikimora in `/usr/local`. Therefore the canonical `kikimora` package is not a valid Phase 0.5 artifact here.

Use only the side-by-side candidate recorded by 08A.1:

```bash
sha256sum ./kikimora-next_<VERSION>_<ARCH>.deb
dpkg-deb --info ./kikimora-next_<VERSION>_<ARCH>.deb
```

The checksum must match the recorded `kikimora-next` checksum exactly.

The preferred operator path is the prepared fail-closed installer:

```bash
bash build/08a-private/08a-phase05-install-next.sh
```

The script verifies that the candidate payload contains none of the legacy control-plane paths, hashes the old `kk`/Kikimora/libexec tree before installation, installs `kikimora-next`, then requires those hashes and legacy service states to remain unchanged.

Immediately after installation the only allowed candidate checks are:

```bash
/opt/kikimora-next/bin/kikimora-core --help >/dev/null
/opt/kikimora-next/bin/kikimora-toad --help >/dev/null
sudo kk-next orchestration preflight
kk-next orchestration status
```

Also record:

```bash
command -v kk
command -v kikimora
command -v kk-next
dpkg-query -W kikimora-next
systemctl cat kikimora-core-next.service
systemctl is-active kikimora-core-next.service || true
systemctl is-enabled kikimora-core-next.service || true
```

The candidate service must remain inactive and disabled after package installation.

If any legacy hash/path/service state changes, stop immediately. Do not proceed to cutover.

---
# Phase 1 — capture read-only baseline

For the candidate side use:

```bash
sudo kk-next orchestration preflight
kk-next orchestration status
```

Continue collecting the legacy baseline with the existing old `kk`/system commands; do not replace the legacy CLI during this phase.

Also capture, without changing state:

```bash
date -Is
uname -a

ip -4 rule show
ip -6 rule show
ip -4 route show table all
ip -6 route show table all
ip -4 route show table 51890
ip -6 route show table 51890

systemctl is-active kikimora-core.service || true
systemctl is-enabled kikimora-core.service || true
systemctl is-active leshy.service || true
systemctl is-active leshy-route-watch.service || true
systemctl is-active leshy-health-watch.service || true

systemctl cat kikimora-core.service
systemctl cat leshy.service

cat /etc/kikimora/leshy/orchestration-ownership.conf

nmcli -t -f GENERAL.DEVICE,GENERAL.NM-MANAGED device show 2>/dev/null || true
ip -br link show
ip -br addr show

ps -eo pid,ppid,etimes,cmd | grep -E 'kikimora-(core|toad)|leshy|route-watch' | grep -v grep || true
```

If core API is already reachable, also capture:

```bash
sudo kikimora-core status --socket /run/kikimora/core.sock --json
```

Do not print protocol secrets.

Store all outputs in one timestamped staging directory.

---

# Phase 2 — validate rollback before cutover

Before ownership mutation, prove the rollback assets still exist.

Require:

- legacy route writer executable/files exist;
- legacy service unit(s) exist;
- installed legacy writers are audited for ownership-awareness or a host-specific full-stop rollback contract exists;
- ownership state used by the candidate is writable atomically;
- Go core and Toad binaries exist and are executable;
- all Toad TOMLs validate;
- NetworkManager persistent unmanaged rule is installed;
- desired-state directory path matches systemd `StateDirectory`;
- candidate rollback/cutover commands remain disabled until the installed-host writer audit is closed.

Current host result: installed `route-watch`, `route-lifecycle` and `reconcile` predate the ownership-file mechanism used by the generic repository cutover tests. Therefore Phase 2 is **not closed yet** even though the side-by-side package/rollback binaries are preserved.

If rollback prerequisites are missing or installed-host writer semantics differ from the tested contract, stop and create a host-specific cutover packet.

Do not “continue carefully” without a rollback path.

---

# Phase 3 — operator-authorized cutover

**Currently blocked on this host.** The generic `kk orchestration cutover --go` command was tested against ownership-aware legacy writers, but the installed legacy writer stack on this workstation predates that mechanism. `kk-next` deliberately refuses cutover/rollback until a host-specific migration/rollback packet is implemented and reviewed.

Do not invoke either the old `kk orchestration cutover --go` or any ad-hoc equivalent.

When Phase 2 is eventually closed, the replacement cutover command must itself enforce the same readiness predicate and automatic rollback contract. Do not manually bypass its failure.

Record:

- exact command exit code;
- elapsed time;
- ownership file after cutover;
- core JSON snapshot;
- service state;
- Toad PID/generation/interface identity;
- endpoint policy;
- parking;
- publication;
- routes/rules/table 51890;
- NM managed state.

## Success predicate

For every desired role:

- product state Ready;
- route_ready true;
- validated_underlay_epoch == current underlay epoch;
- endpoint applied for current epoch;
- publication present;
- parking inactive.

Also require:

- at least one physical underlay family exists;
- no role is Failed;
- no selected-route physical fallback is observed.

If the cutover command fails, run the documented rollback path and stop.

---

# Phase 4 — real core restart

After successful Go cutover:

```bash
sudo systemctl restart kikimora-core.service
```

Require:

- desired roles automatically return;
- intentionally disabled roles remain disabled;
- new Toad generations cannot be overwritten by stale prior-generation state;
- endpoint/routing reconciliation converges without duplicate routes/rules;
- parking/checkpoint restoration is safe;
- selected traffic remains fail-closed while recovery is incomplete.

Capture before/after:

- core revision;
- underlay epoch;
- role desired bits;
- role generations;
- PIDs;
- ifindices;
- endpoint applied epoch;
- parking/publication.

---

# Phase 5 — deliberate per-role intent persistence

Choose one non-critical test role only with operator approval.

Sequence:

```text
disconnect role
verify desired=false persisted
restart core
verify role stays stopped
reconnect role
verify desired=true persisted
verify Ready/current epoch
```

Do not infer success only from process existence.

---

# Phase 6 — real suspend/resume gate

The equivalent suspend/resume behavior must already be automated and green on the disposable VM under 08A.2. This phase is only the final real-workstation confirmation and requires separate explicit operator authorization because it suspends the real workstation.

Before suspend capture:

- current underlay epoch/interface/gateway/source;
- every Toad PID/generation;
- every managed TUN ifindex/addresses/MTU;
- endpoint state;
- parking;
- publication;
- selected-route state.

Suspend using the operator-approved method.

After resume require:

- one canonical post-resume current underlay identity;
- desired state unchanged;
- every enabled/validated role keeps the expected stable TUN identity; in the default 08A scope this means AmneziaWG, while Xray is checked only if the operator explicitly enabled a real Xray role;
- local configured addresses restored;
- OpenConnect transport recovered by negotiated session, not static address injection;
- current epoch is the only epoch accepted as validated;
- no selected IPv4/IPv6 traffic leaks physically while recovery is incomplete;
- eventual Ready/current epoch for desired roles.

If any condition fails, collect diagnostics before rollback/retry.

Do not run suspend automatically as part of a generic executor script.

---

# Phase 7 — post-cutover diagnostics

Create a second archive:

```bash
sudo kk debuglog -o ./kikimora-post-cutover.log
```

Capture the same route/rule/service/core state as Phase 1.

Compare:

- ownership before/after;
- route/rule deltas;
- Toad identities;
- underlay epochs;
- desired state;
- parking/publication;
- NetworkManager ownership;
- logs for repeated restart/recovery loops.

The archive must contain no protocol secrets.

---

# Phase 8 — rollback gate

Legacy remains installed after 08A.

If any required acceptance fails, execute:

```bash
sudo kk orchestration rollback
```

Then require:

- Go roles stopped before legacy routing ownership is restored;
- core stopped/disabled as defined by rollback contract;
- ownership returned to legacy/external/legacy;
- legacy writer active;
- route/rule behavior matches pre-cutover baseline;
- persisted Go desired state was not erased.

Capture a rollback diagnostic archive.

If rollback itself fails, stop and report exact host state. Do not attempt legacy retirement.

---

# Completion state

Before workstation completion is even considered, require `08a2-vm-console-lifecycle-acceptance.md` and `08a3-linux-console-package.md` to be green: the installed console/runtime product must survive the VM lifecycle matrix and the exact final `.deb` must pass package lifecycle acceptance.

08A can then be marked complete only when the operator-reviewed report contains:

1. preflight evidence;
2. rollback-prerequisite evidence;
3. successful cutover command output;
4. Ready/current-epoch snapshot;
5. real core restart restoration;
6. deliberate desired-state persistence check;
7. real suspend/resume result, if explicitly authorized;
8. pre/post diagnostics;
9. no selected-route leak;
10. legacy rollback path still available.

If real suspend/resume is not authorized, mark:

`08A cutover/restart complete; suspend/resume manual gate pending`

and do not advance to retirement.

---

# Executor report

```text
HEAD:
Installed package/version:

Automated prerequisite evidence:
- 07E:
- 07F packaging:
- reviewer CI status (optional, not an executor gate):
- orchestration-acceptance:
- Linux artifact:
- artifact SHA256:
- artifact build HEAD:
- package install smoke:

Installed artifact:
- dpkg install:
- kk verify:
- installed version:
- installed paths:

Read-only baseline:
- preflight:
- ownership:
- services:
- core snapshot:
- routes/rules:
- NetworkManager:
- diagnostic archive:

Operator-authorized mutations:
- cutover: NOT RUN / exact result
- core restart: NOT RUN / exact result
- per-role persistence: NOT RUN / exact result
- suspend/resume: NOT RUN / exact result

Rollback path:
- prerequisites:
- actual rollback performed: yes/no
- result:

Leaks/fail-closed:
- IPv4:
- IPv6:

Legacy retirement:
- NOT PERFORMED

Unresolved:
- ...
```
