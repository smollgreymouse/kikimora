# Toad step 07F.2A — rootless hermetic network test runner

Status: **IMPLEMENTED; KERNEL INTEGRATION UNVERIFIED IN CURRENT EXECUTOR**.

Purpose: remove `sudo` from the normal executor test contract while preserving real Linux network-namespace, TUN, routing and protocol integration coverage.

This packet comes before continuing `07f2-orchestration-underlay-fixture.md`.

Executor rule: all ordinary local acceptance must run as the current unprivileged user. Do not ask for sudo and do not wait for GitHub Actions.

## Pre-implementation audited state at HEAD `d7f2cce8ce27a5fbc5c2f87d23cdee932e845592`

The bullets below are the historical baseline that motivated this packet. The rootless harness is now implemented; current execution evidence belongs in `test-report.txt`.

Verified repository state:

- `linux/tests/toad/run-rootless.sh` does not exist;
- `linux/tests/toad/rootless-netns-probe.sh` does not exist;
- `linux/tests/toad/run-isolated.sh` still requires `sudo` and wraps network modes in `sudo env ...`;
- the executor nevertheless implemented the next 07F.2 synthetic-underlay changes;
- latest `test-report.txt` is stale for this packet: it still names commit `d4f7810` and contains no rootless-runner evidence;
- the `d7f2cce...` commit reports `orchestration-acceptance` and `xray-interop` pending because of a sudo TTY problem.

Therefore:

- do **not** continue debugging AWG/core orchestration behavior yet;
- do **not** revert the already-landed 07F.2 fixture changes merely because they are unverified;
- first implement this packet, then validate the landed 07F.2 changes through the new rootless runner.

---
---

## Design decision

Do not replace AWG/Xray/OpenConnect with protocol mocks.

Use Linux unprivileged user namespaces:

```text
host user
  -> CLONE_NEWUSER, uid/gid mapped to root inside
     -> private mount namespace
     -> private outer network namespace
     -> existing client/server network namespaces
     -> CAP_NET_ADMIN only inside the created user/network namespaces
```

Inside the user namespace, the tests may create:

- veth pairs;
- nested network namespaces;
- TUN devices in those namespaces;
- routes/rules;
- local AWG reference server;
- pinned official Xray reference server;
- local ocserv;
- Kikimora core/Toads.

This must not grant the executor privileges over the host network namespace.

---

# Binding implementation recipe

Use this design first. Do not invent another sandbox architecture unless the probe proves it impossible.

## Outer rootless namespace

`run-rootless.sh` builds/reuses binaries as the invoking user, then enters:

```bash
unshare --user --map-root-user --mount --net --pid --fork --mount-proc ...
```

Inside that namespace:

```bash
mount --make-rprivate /
mount -t tmpfs -o mode=755 tmpfs /run
mkdir -p /run/netns /run/amneziawg
export KIKIMORA_ROOTLESS_TEST_NS=1
exec bash linux/tests/toad/run-isolated.sh "$MODE"
```

Why:

- the outer `--net` means veth/default-route changes cannot touch the host network namespace;
- uid 0 exists only inside the mapped user namespace;
- private tmpfs `/run` prevents `ip netns` handles and AWG sockets from touching host `/run`;
- existing named `ip netns` tests can remain mostly unchanged.

Do not implement the PID-backed netns backend unless this exact design fails **after** the capability probe demonstrates why.

## Rootless ocserv rule

In a simple `--map-root-user` user namespace, uid/gid `nobody` may be unmapped. For the hermetic rootless fixture only, when `KIKIMORA_ROOTLESS_TEST_NS=1`, configure ocserv to stay as uid/gid 0 **inside the user namespace** instead of dropping to unmapped `nobody/nogroup`.

This is not host root and must never alter the production/default ocserv fixture outside the rootless harness.

---
# Phase 0 — capability probe

Create:

`linux/tests/toad/rootless-netns-probe.sh`

It must run without sudo and return:

- exit 0 + concise PASS when rootless user/net namespaces and TUN are usable;
- a distinct nonzero 'unsupported environment' result when the kernel/runtime forbids them;
- ordinary failure for a real regression.

Probe all capabilities actually needed by the fixture, not only `unshare true`.

Minimum probe sequence:

1. `unshare` exists;
2. `nsenter` and `ip` exist;
3. user namespace can be created with current uid/gid mapped to uid 0 inside;
4. private mount namespace works;
5. nested/new network namespace works;
6. `CAP_NET_ADMIN` inside that network namespace is effective;
7. a veth pair can be created/moved/configured;
8. `/dev/net/tun` exists and a TUN can be created in the owned network namespace using the existing TUN test helper;
9. routes/rules can be added/deleted inside the owned namespace.

Suggested outer invocation shape:

```bash
unshare --user --map-root-user --mount --net --pid --fork --mount-proc \
  bash <rootless-inner-script>
```

Exact flags may be adjusted for the local util-linux version, but do not require host root.

Do not treat `kernel.unprivileged_userns_clone` sysctl text alone as proof; perform the actual operations.

---

# Phase 1 — create a rootless wrapper around `run-isolated.sh`

Add:

`linux/tests/toad/run-rootless.sh`

Contract:

```bash
bash linux/tests/toad/run-rootless.sh <mode>
```

Examples:

```bash
bash linux/tests/toad/run-rootless.sh route-parking
bash linux/tests/toad/run-rootless.sh xray-interop
bash linux/tests/toad/run-rootless.sh multi-toad
bash linux/tests/toad/run-rootless.sh orchestration-acceptance
```

The wrapper must:

1. build ordinary binaries as the invoking user or reuse the existing build cache;
2. run the capability probe;
3. enter one outer mapped user+mount+network namespace;
4. make mount propagation private;
5. provide a private writable runtime area for test namespace handles/sockets;
6. invoke the existing isolated test mode without sudo;
7. preserve the invoking user's ownership of generated files/build cache;
8. propagate the test exit code.

Use an environment marker such as:

`KIKIMORA_ROOTLESS_TEST_NS=1`

to prevent recursive re-entry.

---

# Phase 2 — remove direct sudo from `run-isolated.sh`

File:

`linux/tests/toad/run-isolated.sh`

Current problem: it requires `sudo` globally and every privileged mode is wrapped in `sudo env ...`.

Refactor to two execution layers:

### Layer A — mode/build dispatcher

Never calls sudo.

It checks whether the process currently has sufficient capabilities in its **current namespace**.

### Layer B — namespace preparation

Owned by `run-rootless.sh` for normal executor use.

The existing test scripts then run directly.

After refactor:

```bash
bash linux/tests/toad/run-isolated.sh build-only
```

must work normally.

For netadmin modes, direct `run-isolated.sh <mode>` may either:

- detect capabilities and run directly if already inside the rootless harness/root; or
- print a concise instruction to use `run-rootless.sh <mode>` and exit with an environment-status code.

It must not invoke `sudo` itself.

Delete `require sudo` from the ordinary runner.

---

# Phase 3 — make `netns.sh` compatible with rootless outer userns

Keep the existing helper API as much as possible.

Preferred path: retain `ip netns` inside the outer user namespace if a private `/run/netns` can be mounted safely.

Inside the outer private mount namespace:

- `mount --make-rprivate /`;
- create/mount a private writable netns runtime directory;
- ensure `ip netns add/exec/delete` affects only this outer namespace.

If private tmpfs `/run` + named `ip netns` fails, record the exact failing command/error in `test-report.txt`.

Do **not** immediately rewrite the suite around PID-backed namespaces. For this executor:
- if userns itself is unavailable, use the model fallback;
- if userns works but named netns fails because of a specific iproute2/mount restriction, STOP with that exact evidence and a focused follow-up design.

Do not rewrite every test script independently.

---

# Phase 4 — private runtime paths

Tests currently use host-style paths such as `/var/run/amneziawg` and `/run/netns`.

Under the rootless wrapper these must not require writes to host-owned directories.

Use the private mount namespace to mount writable tmpfs/bind mounts for runtime-only paths, or change test fixtures to use `$MPF_TMP`/`$XDG_RUNTIME_DIR` where supported.

Rules:

- never chmod/chown host `/run`, `/var/run` or `/etc`;
- never leave namespace handles or sockets after test cleanup;
- generated test secrets stay in the private temp directory;
- cleanup must work on success, failure and signal.

---

# Phase 5 — TUN handling without host root

Do not create a host device node.

Use the existing `/dev/net/tun` device when present.

The actual TUN interface must be created inside a network namespace owned by the mapped user namespace, where the test process has namespace-scoped `CAP_NET_ADMIN`.

Add a rootless smoke gate before the full suite:

```bash
bash linux/tests/toad/run-rootless.sh tun-owner
```

Assert:

- TUN appears only in the owned test namespace;
- it does not appear in the host namespace;
- cleanup removes it.

If `/dev/net/tun` is absent entirely, report environment unsupported; do not mknod on the host.

---

# Phase 6 — rootless protocol matrix

Once the harness works, these modes must be runnable without sudo:

```text
tun-owner
route-parking
awg2-attachment
awg2-interop
xray-lifecycle
xray-interop
openconnect-interop
multi-toad
orchestration-acceptance
```

For Xray keep the pinned official local Xray reference server. Do not replace it with a mock.

For OpenConnect keep local `ocserv`; it must run only inside the owned namespace.

If `ocserv` itself has a hard requirement that cannot be satisfied in an unprivileged user namespace, isolate that single gate as `ROOTLESS_UNSUPPORTED_OCSERV` and add a lower-level production OpenConnect backend test with a controlled fake process/server. Do not make the whole suite privileged because of one daemon.

---

# Phase 7 — deterministic no-kernel fallback for restricted executor sandboxes

Some sandboxes disable `CLONE_NEWUSER` entirely.

The executor must still be able to work productively.

Add a command:

```bash
bash linux/tests/toad/run-rootless.sh model
```

that never needs user namespaces and runs the deterministic kernel-independent suite covering:

- route transaction planning/idempotence using `routing.Fake`;
- IPv4/IPv6 parking ownership/baseline-delta;
- endpoint desired/current reconciliation;
- underlay epoch/coalescer/recovery;
- stale process/generation rejection;
- fail-closed state machine;
- route-target drift recovery;
- async restart retry identity;
- NetworkManager/sleep observer fakes;
- packaging/service static contracts.

Prefer invoking existing Go tests rather than duplicating logic in shell.

Example:

```bash
cd toad
go test ./internal/routing ./internal/parking ./internal/endpoint ./internal/core ./internal/control ./internal/netstate ./internal/toadruntime
go test -race ./internal/control ./internal/core ./internal/netstate ./internal/toadruntime
```

plus relevant shell/package static tests.

If rootless userns is unavailable, report:

```text
rootless kernel integration: UNSUPPORTED BY EXECUTOR ENVIRONMENT
model suite: PASS/FAIL
```

and continue code work. Do not ask the executor for sudo.

Kernel integration then remains an operator/reviewer gate, not an executor blocker.

---

# Phase 8 — update 07F.2 commands

After this packet, `07f2-orchestration-underlay-fixture.md` must use:

```bash
bash linux/tests/toad/run-rootless.sh route-parking
bash linux/tests/toad/run-rootless.sh multi-toad
bash linux/tests/toad/run-rootless.sh xray-interop
bash linux/tests/toad/run-rootless.sh orchestration-acceptance
```

Never `sudo bash ...` in executor instructions.

Keep the root/privileged form only as an optional operator/reviewer compatibility path if useful.

---

# Phase 9 — self-safety test

Add a script/test that captures host state before and after rootless tests:

- host network namespace inode;
- host link names/count;
- host default routes;
- host policy rules;
- optionally hashes of relevant `/run` directory listings accessible to the user.

After rootless suite, assert the invoking host namespace routing/link state is unchanged.

Do not require reading privileged host state unavailable to the ordinary user.

---

# Local acceptance

Run as the normal user, no sudo:

```bash
bash linux/tests/toad/rootless-netns-probe.sh
bash linux/tests/toad/run-rootless.sh tun-owner
bash linux/tests/toad/run-rootless.sh route-parking
bash linux/tests/toad/run-rootless.sh xray-interop
bash linux/tests/toad/run-rootless.sh multi-toad
bash linux/tests/toad/run-rootless.sh orchestration-acceptance
```

Also:

```bash
bash linux/tests/toad/run-rootless.sh model
```

If rootless namespaces are unsupported, only the probe may be UNSUPPORTED; the model suite must still run and pass.

Write exact results to `test-report.txt`.

The report must be rewritten for the actual final HEAD:
- top-level `Commit:` must equal `git rev-parse HEAD`;
- add a dedicated `07F.2A rootless harness` section;
- do not present historical sudo results from `d4f7810` as current evidence;
- historical results may remain only under an explicitly labelled history section.

---

# Completion

07F.2A is complete when:

1. normal executor commands contain no sudo;
2. `run-isolated.sh` no longer self-elevates;
3. a real capability probe distinguishes unsupported environment from test regression;
4. on a Linux host with unprivileged user namespaces + `/dev/net/tun`, the netns/TUN/protocol suite runs rootlessly;
5. restricted sandboxes still have a meaningful model suite;
6. host network state remains untouched by rootless tests;
7. 07F.2 is rewritten to consume the rootless runner.

Then resume 07F.2 orchestration-underlay fixture closure.

---

# STOP/DESIGN conditions

Stop with exact evidence only if:

1. the kernel forbids unprivileged user namespaces;
2. TUN creation is forbidden even inside an owned user/net namespace;
3. installed iproute2 cannot support named netns in userns and PID-backed namespace helpers also fail;
4. ocserv has an unavoidable initial-user-namespace privilege requirement.

These conditions must not trigger a request for sudo from the executor. Use the model fallback and defer only the affected kernel/daemon integration gate.

---

# Executor report

Keep chat short; full evidence goes in `test-report.txt`:

```text
HEAD before:
HEAD after:

rootless probe:
- userns:
- nested netns:
- veth:
- TUN:
- routes/rules:

rootless gates:
- tun-owner:
- route-parking:
- xray-interop:
- multi-toad:
- orchestration-acceptance:

model fallback:
- PASS/FAIL:

host state unchanged:
- PASS/FAIL:

unsupported environment features:
- none OR exact feature

Full evidence: test-report.txt
```
