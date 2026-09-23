# Toad step 08A — Linux installed-host staging and reversible cutover

Status: **FUTURE / OPERATOR-GATED**.

Execute only after 07E local acceptance is complete, 06A/07A-07D statuses are closed from recorded local evidence, and **07F-Linux packaging is complete for the exact staging HEAD**, including the privileged hermetic orchestration acceptance gate.

This packet is for a real installed Linux host. It is not an executor-autonomous task.

08A must use the exact Linux release artifact produced by 07F. Installing directly from a source checkout or ad-hoc locally built core/Toad binaries does not satisfy this packet.

The executor may prepare commands, collect read-only evidence and analyze results. It must not perform a real cutover, suspend the workstation, or retire legacy writers without explicit operator authorization in the live session.

---

## Goal

Prove that the already-tested Go-owned control plane can take ownership on the actual host while preserving an immediate rollback path.

This packet ends with:

- Go ownership running on the installed host;
- desired state surviving a real core restart;
- real suspend/resume evidence if explicitly authorized;
- legacy writers still installed and recoverable;
- a diagnostic archive suitable for 08B review.

It does **not** retire legacy components.

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

# Phase 0.5 — install the verified 07F artifact

This phase changes installed files and therefore requires the same explicit operator authorization as installed-host staging.

Record first:

```bash
sha256sum ./kikimora_<VERSION>_<ARCH>.deb
dpkg-deb --info ./kikimora_<VERSION>_<ARCH>.deb
```

The checksum must exactly match the locally recorded 07F artifact manifest/checksum for the same HEAD.

Install the package artifact itself:

```bash
sudo dpkg -i ./kikimora_<VERSION>_<ARCH>.deb
```

If dependency resolution is required, use the documented supported package-manager command and preserve the exact .deb as the Kikimora payload. Do not rebuild from checkout on the target host.

Immediately verify the installed payload before any ownership cutover:

```bash
sudo kk verify
sudo kk orchestration preflight
kikimora-core --help >/dev/null
kikimora-toad --help >/dev/null
```

Also record installed paths and versions:

```bash
command -v kikimora-core
command -v kikimora-toad
command -v kikimora
command -v kk
dpkg-query -W kikimora
systemctl cat kikimora-core.service
```

Package installation must **not** itself perform Go ownership cutover.

If package install or verify fails, stop before later mutation and collect diagnostics.

---
# Phase 1 — capture read-only baseline

Run:

```bash
sudo kk orchestration preflight
sudo kk orchestration status
sudo kk debuglog -o ./kikimora-pre-cutover.log
```

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

nmcli -t -f GENERAL.DEVICE,GENERAL.MANAGED device show 2>/dev/null || true
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
- ownership file is writable atomically;
- Go core and Toad binaries exist and are executable;
- all Toad TOMLs validate;
- NetworkManager persistent unmanaged rule is installed;
- desired-state directory path matches systemd `StateDirectory`;
- `kk orchestration rollback` preconditions pass in a read-only check if such mode exists.

If rollback prerequisites are missing, stop.

Do not “continue carefully” without a rollback path.

---

# Phase 3 — operator-authorized cutover

Only after an explicit operator instruction in the current session:

```bash
sudo kk orchestration cutover --go
```

The command itself must enforce the readiness predicate. Do not manually bypass its failure.

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

This phase requires a separate explicit operator authorization because it suspends the real workstation.

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
- AWG/Xray stable TUN ifindex where protocol contract allows repair;
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

08A can be marked complete only when the operator-reviewed report contains:

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
