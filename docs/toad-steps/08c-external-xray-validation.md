# Toad step 08C — external Xray endpoint validation

Status: **DEFERRED / OPERATOR-GATED / NON-BLOCKING FOR 08A AWG+OPENCONNECT**.

Purpose: validate the production Xray/VLESS/REALITY client against the operator's actual remote Xray server when that environment is available.

This packet is intentionally separate from hermetic acceptance.

---

## What already counts as automated Xray acceptance

The mandatory automated protocol gate is local and hermetic:

- pinned official `github.com/xtls/xray-core/main` built as `xray-ref`;
- client and server in isolated Linux network namespaces;
- no default route and no public Internet path;
- generated ephemeral x25519 REALITY keys, UUID and short-id;
- hermetic TLS cover helper;
- private local HTTP payload endpoint;
- real VLESS + REALITY + Vision traffic;
- server restart and underlay recovery.

Entry points:

- `linux/tests/toad/run-isolated.sh xray-interop`;
- `linux/tests/toad/xray-interop.sh`.

This is **not a mock protocol server**. It uses the official Xray implementation locally and is the required automated gate.

Do not replace it with a simplified fake/mock Xray server.

---

## Why 08C is deferred

The executor environment may not have access to the operator's real remote Xray/VPS endpoint or its bearer credentials.

That limitation must not block:

- 07E/07F local acceptance;
- Linux package creation;
- 08A real-host staging for AmneziaWG + OpenConnect.

08C becomes actionable only when the operator explicitly provides a usable real Xray profile/endpoint in the live environment.

---

# Gate 0 — operator-provided secret profile only

Never commit real VLESS links, UUIDs, REALITY private/public credential material, endpoint IPs that the operator considers private, or tokens into the repository.

Use one of:

- a local secret file outside the repository;
- stdin;
- an environment variable supplied only for the command invocation.

Preferred path:

```bash
chmod 600 /path/outside/repo/xray-real.txt
kikimora-toad import -file /path/outside/repo/xray-real.txt \
  -name xray-real \
  -interface kk-xray-real0 \
  > /tmp/xray-real.toml
chmod 600 /tmp/xray-real.toml
```

Inspect generated config without copying secrets into reports.

---

# Phase 1 — standalone real-server connectivity

Run the production Toad against the real profile without giving it routing ownership beyond its own TUN.

Require:

- process stays alive;
- TUN appears;
- TUN has expected configured address/MTU;
- RouteReady becomes true only after real session proof;
- data-plane probe through the Xray TUN reaches an operator-approved target;
- no ambient default/split-default route is installed by Xray/Toad;
- no unrelated interface/routing state changes.

Record only redacted state:

- generation;
- interface name/ifindex;
- RouteReady;
- session health;
- endpoint hostname/port if safe to report;
- probe success/failure.

Do not record UUID/public key/short-id/full share link.

---

# Phase 2 — real-server failure and recovery

With operator approval, induce only reversible failures that are possible in the current environment.

Preferred non-destructive cases:

1. temporarily block the remote endpoint route locally;
2. temporarily change underlay interface/gateway by an operator-controlled network transition;
3. stop/restart only the local Toad process.

Require:

- Xray does not report online solely because TUN remains up;
- RouteReady becomes false when real session proof is lost;
- selected traffic is fail-closed when the role is selected;
- ordinary transport recovery does not recreate the TUN if the protocol contract allows stable TUN;
- recovery returns to Ready only after fresh real session proof.

Do not deliberately attack or restart the remote server unless the operator owns it and explicitly authorizes that action.

---

# Phase 3 — core-managed real Xray role

Only after standalone proof:

- add the real Xray role to the installed Go core config;
- keep other validated roles (especially AmneziaWG/OpenConnect) active;
- enable Xray desired state explicitly;
- verify independent PID/generation/ifindex;
- verify one Xray failure does not restart/recreate unrelated Toads;
- verify Xray selected traffic remains fail-closed during loss;
- verify final Ready uses current underlay epoch.

If 08A was already completed for AmneziaWG + OpenConnect, this phase must not disturb their accepted state.

---

# Phase 4 — optional suspend/resume with real Xray

This phase is manual/operator-gated and only runs if the operator explicitly requests it.

Before suspend capture redacted:

- underlay epoch/interface;
- Xray PID/generation;
- Xray TUN ifindex;
- RouteReady;
- desired state.

After resume require:

- canonical new/current underlay state;
- desired state unchanged;
- no stale pre-resume validation accepted;
- Xray recovers to Ready with fresh session proof;
- stable TUN identity if current Xray runtime contract guarantees it.

---

# Completion

08C is complete only after the real remote endpoint has been tested with production binaries and redacted evidence is recorded.

Until then roadmap status remains:

`external Xray validation pending; hermetic official-Xray acceptance green`.

08C does not block 08A completion for AmneziaWG + OpenConnect.

---

# Executor report

Keep the report redacted:

```text
HEAD:
real Xray profile source: local secret / stdin (not committed)

standalone:
- TUN:
- RouteReady:
- payload probe:

failure/recovery:
- failure method:
- fail-closed:
- recovered:
- ifindex stable:

core-managed:
- independent role:
- unrelated roles stable:
- final epoch:

manual suspend/resume:
- NOT RUN / PASS

secrets committed/logged: NO
```
