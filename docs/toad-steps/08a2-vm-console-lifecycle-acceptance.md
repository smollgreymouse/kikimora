# Toad step 08A.2 — disposable-VM console orchestrator and OS lifecycle acceptance

Status: **CURRENT / IMPLEMENT NEXT ON THE DISPOSABLE UBUNTU VM**.

This packet is the bridge between the completed hermetic Go orchestration gates and any workstation cutover.

The target is no longer “prove that each protocol can make a TUN”. The target is a usable **console Kikimora product**:

- one installed `kikimora-core` orchestration daemon;
- independently supervised `kikimora-toad` processes;
- a stable `kk` console control surface;
- persistent desired state;
- automatic convergence after real OS/network/process lifecycle events;
- a versioned machine-readable state channel that a later UI can consume;
- a final installable Linux `.deb`.

There is deliberately **no UI acceptance in this packet**. Desktop/UI work is the next roadmap chapter and must consume the same state/control API rather than inventing a second product state model.

The disposable VM is `ubuntu-desktop-vm`. Host-side lifecycle control is available through VirtualBox:

```bash
VBoxManage startvm ubuntu-desktop-vm
VBoxManage controlvm ubuntu-desktop-vm pause
VBoxManage controlvm ubuntu-desktop-vm resume
VBoxManage controlvm ubuntu-desktop-vm reset
VBoxManage controlvm ubuntu-desktop-vm acpipowerbutton
VBoxManage controlvm ubuntu-desktop-vm poweroff
VBoxManage controlvm ubuntu-desktop-vm savestate
VBoxManage controlvm ubuntu-desktop-vm setlinkstate1 off
VBoxManage controlvm ubuntu-desktop-vm setlinkstate1 on
VBoxManage guestproperty wait ubuntu-desktop-vm '<pattern>' --timeout=<msec>
```

Guest-side commands may also be executed through the existing SSH/Guest Additions test harness. Do not use `.gigacode` for VM acceptance assets.

---

## Evidence already established before this packet

Current real-VPN evidence at `6c01dcb`:

- AmneziaWG real system-wide path: PASS;
- AWG application probes: Google 200, Telegram 200, ChatGPT trace 200, unauthenticated OpenAI API 401;
- OpenConnect real system-wide path: PASS;
- internal `gitlab.sca.ad-tech.ru`: real HTTPS response 302 through `kk-oc0`;
- Xray/VLESS integration: TUN/routing and real Google + Telegram traffic work, then the provider network blocks further Xray use at the ChatGPT DNS/application step; this is a known ISP-DPI property and is not an 08A blocker;
- every real-VPN run cleaned owned routes/DNS/interfaces and restored the original IPv4/IPv6 host state.

The authoritative hermetic Xray gate remains required and green independently of public-network DPI.

---

## Product boundary for the end of chapter 08

Chapter 08 is complete only when the Linux console product can be installed from a `.deb` and operated without source-tree scripts.

The installed console product must provide:

```text
kikimora-core
kikimora-toad
kk
systemd service/unit integration
per-Toad config loading
persistent desired state
diagnostics
versioned local control/state API
```

The user-facing console must support, directly or through a thin stable wrapper over the core API:

```text
kk status
kk status --json
kk watch --json
kk connect [--role NAME]
kk disconnect [--role NAME]
kk retry --role NAME
kk restart [--role NAME|--all]
kk profiles
kk diagnostics / debuglog
kk version
```

Exact spelling may reuse existing commands, but there must be one coherent installed CLI. Operators must not need to invoke private test scripts or manually speak the core socket protocol.

### State channel reserved for the future UI

The existing local core API is the product state channel, not an implementation detail to throw away after CLI work.

Required API contract for chapter 08:

- local authenticated/permissioned socket;
- explicit protocol/API version negotiation;
- `Handshake` capabilities;
- `GetSnapshot` / `Snapshot`;
- revisioned `Subscribe`;
- monotonically increasing revision;
- one canonical snapshot schema for CLI and future UI;
- structured role state/reason codes;
- desired state, observed state, underlay epoch, generation, interface identity, route readiness, endpoint/parking/publication state;
- no secrets in snapshots or diagnostics;
- bounded reconnect/resubscribe semantics after core restart.

`kk status --json` is a snapshot view. `kk watch --json` must expose the revisioned subscription stream in a scriptable form. The later UI chapter must use this API directly; it must not scrape human CLI text.

---

# Phase 0 — install the candidate product in the VM

Do not run lifecycle acceptance from a source checkout.

Build an exact candidate `.deb` from the tested HEAD, record SHA-256, then install it in the disposable VM.

For the VM, it is acceptable to use the isolated `kikimora-next` staging namespace until the canonical-package collision/migration contract is closed, but the tested candidate must contain the **full console orchestration surface**, not the current status/preflight-only `kk-next` restriction.

Require after install:

- package-owned binaries and units only;
- no source-tree binary is used by systemd;
- service starts from the installed path;
- Toad configs and secrets are outside the package payload and mode-protected;
- package install does not silently enable VPN desired state unless explicitly requested;
- `kk version`, `kk status --json`, API handshake and subscription all work from installed files.

Record package filename, SHA-256, package version, git HEAD and installed file list.

---

# Phase 1 — console orchestration baseline

Use real AWG and OpenConnect roles. Xray may be configured but must not block this packet on the known provider DPI path.

Establish desired state only through the installed console/API.

Require:

- core service active;
- AWG and OpenConnect desired=true;
- roles converge to Ready/current underlay epoch;
- real application traffic passes through each accepted role;
- each role has independent PID/generation/interface identity;
- `kk status` and `kk status --json` agree semantically;
- `kk watch --json` receives a newer revision for every meaningful lifecycle transition;
- snapshots contain no secret material.

Capture a baseline tuple for every desired role:

```text
desired
state
reason
generation
pid
interface/name/ifindex
route_ready
validated_underlay_epoch
endpoint applied epoch/state
parking
publication
rx/tx/session health
```

---

# Phase 2 — process and service recovery

Run separately and return to a clean Ready baseline after every case.

## 2.1 Core restart

```bash
sudo systemctl restart kikimora-core.service
```

Require:

- persisted desired state restored;
- intentionally disabled roles stay disabled;
- desired roles return automatically;
- no duplicate Toads;
- no duplicate routes/rules/publications;
- stale generation snapshots are rejected;
- API clients can reconnect and resubscribe.

## 2.2 Kill one Toad

Kill only one managed Toad process.

Require:

- unrelated Toad PID/generation/interface remains unchanged;
- failed role becomes non-ready/fail-closed;
- core starts bounded recovery;
- desired role returns to Ready;
- restart generation increases exactly as required by the recovery contract;
- selected traffic never falls through the physical default while the role is unavailable.

## 2.3 Core hard death

Terminate the core without a graceful application shutdown and let systemd restart it.

Require the same persisted-state/reconciliation properties as 2.1, plus no orphan Toad/process ownership ambiguity.

## 2.4 NetworkManager restart

Restart NetworkManager inside the VM.

Require:

- `kk-*` interfaces remain excluded from NM ownership;
- observer degradation/recovery is visible in the state channel;
- no false Ready from stale underlay state;
- roles converge to current epoch.

---

# Phase 3 — physical-link lifecycle

Drive the virtual NIC from the host, not by mutating Toad internals.

## 3.1 Link loss

```bash
VBoxManage controlvm ubuntu-desktop-vm setlinkstate1 off
```

Require:

- underlay loss becomes visible;
- selected routes fail closed;
- desired state does not change;
- no recovery storm/unbounded process churn;
- state subscription reports the transition.

## 3.2 Link restoration

```bash
VBoxManage controlvm ubuntu-desktop-vm setlinkstate1 on
```

Require:

- one canonical current underlay is selected;
- epoch advances appropriately;
- endpoint policy is reconciled to that epoch;
- AWG/OpenConnect recover automatically;
- no duplicate routes/rules;
- application probes pass again.

Repeat link off/on several times to catch accumulation bugs.

---

# Phase 4 — guest suspend/resume

This is safe to automate on the disposable VM and is intentionally stronger than the workstation manual gate.

Before suspend capture:

- core revision;
- underlay epoch;
- desired bits;
- Toad PIDs/generations;
- TUN names/ifindices;
- route/DNS state.

Suspend the guest with an OS-level path so the Linux logind sleep observer is exercised. Resume it from the VirtualBox host.


### Recorded VM result — 2026-10-03

The installed-console lifecycle run reached this phase after the following gates were already green on the disposable VM:

- core restart + `kk watch --json` reconnect;
- per-role AWG kill/recovery;
- per-role OpenConnect kill/recovery;
- hard core SIGKILL/systemd recovery;
- NetworkManager restart;
- real AWG/ChatGPT/OpenAI probes;
- real OpenConnect internal-GitLab probe;
- physical `enp0s3` link down/up with new DHCP address and full AWG/OpenConnect recovery.

The first real OS suspend attempt was corrected after a shell-argument bug. The corrected run executed `systemctl suspend`; the operator then woke the guest from VirtualBox. After wake, however, the guest virtual NIC did not reappear on the LAN:

- previous IPv4 addresses `192.168.1.236` and `192.168.1.51` were absent;
- the previous link-local IPv6 address was unreachable;
- the previous VirtualBox MAC/SSH host keys were not observable anywhere in the current LAN;
- restarting NetworkManager inside the resumed guest did not restore an IPv4 address.

Classify this as **VM/hypervisor NIC resume failure before Kikimora underlay recovery can be evaluated**. It is not evidence that Kikimora failed recovery, because no usable physical underlay returned to the guest.

Do not weaken the Kikimora suspend/resume acceptance requirement. Restore/reboot the VM testbed, capture the post-resume NIC/kernel/NetworkManager evidence locally, and rerun this phase only after the hypervisor can reliably return the guest NIC. The already-green explicit physical-link loss/recovery test remains valid independent evidence for Kikimora underlay convergence.

Require after resume:

- desired state unchanged;
- resume is visible through the sleep observer/state channel;
- stale pre-suspend validation is not accepted;
- AWG keeps stable route-target identity where its contract requires it;
- OpenConnect recovers via its real negotiated transport/session path;
- current epoch becomes authoritative;
- fail-closed remains active until fresh validation;
- eventual Ready/application traffic without manual reconnect.

Run more than one suspend/resume cycle.

---

# Phase 5 — hypervisor lifecycle

These cases test boundaries that an in-guest sleep event does not.

## 5.1 VM pause/resume

```bash
VBoxManage controlvm ubuntu-desktop-vm pause
VBoxManage controlvm ubuntu-desktop-vm resume
```

The guest does not receive a normal suspend sequence. Require recovery from elapsed transport/network time without corrupting desired/product state.

## 5.2 Saved-state resume

Save VM state, then start/resume it.

Require the same convergence invariants as pause/resume and no stale session authority.

## 5.3 Hard reset

```bash
VBoxManage controlvm ubuntu-desktop-vm reset
```

Require after boot:

- systemd starts the installed core according to package policy;
- persisted desired state is restored;
- Toads are recreated once;
- no stale runtime ownership survives the boot boundary;
- application traffic converges automatically.

A hard reset is expected to lose volatile runtime state; it must not lose desired state.

---

# Phase 6 — normal reboot, shutdown and cold boot

## 6.1 Guest reboot

Use the guest OS reboot path.

Require clean service stop/start and automatic desired-state convergence after boot.

## 6.2 ACPI shutdown

Request clean shutdown from the VirtualBox host:

```bash
VBoxManage controlvm ubuntu-desktop-vm acpipowerbutton
```

Wait until the VM is fully powered off, then:

```bash
VBoxManage startvm ubuntu-desktop-vm
```

Require installed service startup and desired-state restore with no manual source-tree command.

## 6.3 Cold boot after power-off

Power the VM down only after collecting evidence, start it fresh, and require the same installed-product behavior.

Do not use hard power-off as the normal shutdown test; keep it as a separate crash-recovery case.

---

# Phase 7 — repetition / soak without UI

Run a bounded lifecycle sequence, for example:

```text
link off/on
core restart
Toad kill/recovery
suspend/resume
link off/on
reboot
pause/resume
```

Repeat enough cycles to detect:

- route/rule accumulation;
- orphaned Toads;
- stale sockets/runtime dirs;
- ever-growing generations without cause;
- retry storms;
- stale parking/publication;
- API revision regressions;
- lost desired state;
- NetworkManager ownership drift.

This is not an indefinite soak test. Keep it bounded and evidence-driven.

---

# Phase 8 — package upgrade and removal contract

The final chapter-08 artifact is a Linux console/runtime `.deb`.

On the VM prove:

1. fresh install;
2. start/configure via installed console;
3. desired state persistence;
4. upgrade to a rebuilt package from a newer test version/HEAD without deleting configs/secrets/desired state;
5. service restart/recovery after upgrade;
6. uninstall without purge preserves documented state/config;
7. purge removes only package-owned product state according to explicit policy;
8. reinstall returns to a known state.

The chapter-08 console package must not depend on Qt merely to satisfy an old packaging contract.

Preferred package split for the following UI chapter:

```text
kikimora        -> core + Toads + kk + Linux integration
kikimora-ui     -> optional Qt UI, depends on compatible kikimora
```

The exact UI package name can be finalized in chapter 09, but UI dependencies/assets must not be release-blocking for the chapter-08 console package.

---

# Completion gate

08A.2 is complete only when all of the following are true on the disposable VM:

- installed console controls real AWG/OpenConnect Toads;
- real traffic acceptance is green for the supported VM network;
- desired state survives core restart, reboot and cold boot;
- process crashes recover without disturbing unrelated roles;
- physical link loss/restoration converges;
- OS suspend/resume converges;
- VM pause/saved-state/resume converges;
- state API remains revisioned, reconnectable and secret-free;
- `kk status --json` and `kk watch --json` expose sufficient state for the future UI;
- repeated lifecycle cycles leave no stale routes/rules/processes/runtime ownership;
- a package-built, package-installed candidate passes the same gates;
- final Linux console/runtime `.deb` and SHA-256 are produced.

Only after this gate should the project return to workstation side-by-side/cutover acceptance.

---

# Executor report

```text
HEAD:
package:
sha256:

installed console:
- version:
- status:
- status --json:
- watch --json/API subscribe:
- secrets absent:

roles:
- AWG real application probe:
- OpenConnect internal GitLab probe:
- Xray external: EXPECTED_DPI_BLOCK / optional result

lifecycle:
- core restart:
- one-Toad kill:
- core hard death:
- NetworkManager restart:
- NIC off/on:
- OS suspend/resume:
- VM pause/resume:
- saved-state resume:
- hard reset:
- normal reboot:
- ACPI shutdown/start:
- cold boot:
- bounded repeated sequence:

invariants:
- desired state:
- fail-closed:
- unrelated role isolation:
- routes/rules clean:
- NM ownership:
- API revisions:
- state-channel reconnect:
- final application probes:

package lifecycle:
- fresh install:
- upgrade:
- remove:
- purge/reinstall:

final result:
- PASS / FAIL
- blockers:
```
