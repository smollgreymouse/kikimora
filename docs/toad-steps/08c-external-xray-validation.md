# Toad step 08C — external Xray endpoint validation (Linux VM + Windows VM)

Status: **PLANNED — DUAL-PLATFORM EXECUTION AWAITING OPERATOR-PROVIDED WORKING SECRET**.

Purpose: validate the production Xray/VLESS/REALITY client on both the disposable Ubuntu VM and the disposable Windows 10 VM against the operator's real remote Xray server. The operator has located a working VLESS/REALITY profile that is expected to pass ISP DPI constraints that blocked the previous run at `6c01dcb`.

This packet covers both platforms because the xray-core tun inbound natively supports Windows (pinned `v1.260327.1`, `proxy/tun/tun_windows.go`): `wintun.CreateAdapter` + gVisor netstack + `winipcfg` for addresses/MTU/routes. No new Toad backend code is needed for Windows — the same `xraybackend.New(cfg)` path runs on both platforms.

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

This is **not a mock protocol server**. It uses the official Xray implementation locally and is the required automated gate. Do not replace it with a simplified fake/mock Xray server.

---

## Prior evidence

At `6c01dcb`, the standalone Linux VM Xray run created the real route target and carried real Google and Telegram HTTPS traffic, then failed resolving/reaching the ChatGPT path in the operator's provider network. This matched the already-established ISP DPI behavior that disrupts Xray protocols. That result was classified as external-network limitation, not a Kikimora orchestration failure.

The operator has since located a working VLESS/REALITY profile expected to pass ISP DPI. Execution of this plan will validate that profile end-to-end on both VMs.

---

## Platform summary

| Aspect | Linux VM | Windows VM |
|---|---|---|
| VM identity | Disposable Ubuntu (DHCP, enp0s3) | Disposable Windows 10 (`win10`, 192.168.1.246) |
| Installed product | `kikimora 1.0.0` .deb | KikimoraCore MSI (service) |
| Config dir | `/etc/kikimora/toads/` | `C:\ProgramData\Kikimora\toads\` |
| Ownership | `/etc/kikimora/leshy/orchestration-ownership.conf` | `C:\ProgramData\Kikimora\ownership.conf` |
| Socket | `/run/kikimora/core.sock` | `C:\ProgramData\Kikimora\core.sock` |
| State dir | `/run/kikimora/toads/<role>` | `C:\ProgramData\Kikimora\state\<role>` |
| CLI | `kk status --json` | `kikimora-core.exe status --socket C:\ProgramData\Kikimora\core.sock --json` |
| TUN backend | xray-core tun inbound (`/dev/net/tun`) | xray-core tun inbound (wintun, native) |
| Interface naming | `kk-xray0` (or custom) | `kk-xray-win0` (or custom) |

---

## Gate 0 — operator-provided secret profile only

Never commit real VLESS links, UUIDs, REALITY private/public credential material, endpoint IPs that the operator considers private, or tokens into the repository.

The secret must be delivered to each VM through one of:

- an ignored mode-`0600` file outside the repository;
- stdin;
- an environment variable supplied only for the command invocation.

The in-repository-path secret (`linux/tests/toad/real-vps-vless-link.secret`) is acceptable only while it remains ignored/untracked and never enters commits, diffs or logs.

### Import command

Cross-platform (via `profileimport.Parse`):

```bash
# Linux
chmod 600 /path/outside/repo/xray-real.txt
kikimora-toad import -file /path/outside/repo/xray-real.txt \
  -name xray-real \
  -interface kk-xray0 \
  -address 10.255.0.2/30 \
  -mtu 1380 \
  -state-dir /run/kikimora/toads/xray-real \
  > /tmp/xray-real.toml
chmod 600 /tmp/xray-real.toml
```

```powershell
# Windows
kikimora-toad.exe import -file C:\secrets\xray-real.txt `
  -name xray-win `
  -interface kk-xray-win0 `
  -address 10.255.0.6/30 `
  -mtu 1380 `
  -state-dir C:\ProgramData\Kikimora\state\xray-win `
  > C:\ProgramData\Kikimora\toads\xray-win.toml
```

### Import defaults (for reference)

When flags are omitted: interface `kk-xray0`, address `10.255.0.2/30`, MTU 1380, state_dir `/run/kikimora/toads/<name>` (Linux default — **Windows requires explicit `-state-dir`**).

### Lessons from 08A.4b carried forward

- **Custom role names** (anything other than `primary`/`secondary`) need explicit `leshy_zone` and `rule_priority` in the `[endpoint_policy]` section of the TOML; auto-fill only covers the standard names.
- **Per-role `state_dir`** is mandatory; sharing a state directory between roles causes `parking.json` role mismatch.
- **Secret file line endings**: Windows-created files may have `\r\n` — ensure LF-only or the import parser may mangle base32/credential content (observed with OC TOTP; VLESS URIs may tolerate CRLF, but LF is safer).

---

## Phase 1 — standalone real-server connectivity (both VMs)

Run the production Toad against the real profile without giving it routing ownership beyond its own TUN.

**On each VM:**

1. Import the secret → TOML.
2. Validate: `kikimora-toad validate -config <toml>`.
3. Run standalone (not under core): `kikimora-toad run -config <toml>` (Linux: `sudo`; Windows: elevated console).
4. Observe:
   - process stays alive;
   - TUN appears (`kk-xray0` / `kk-xray-win0`);
   - TUN has expected configured address/MTU;
   - RouteReady becomes true only after real session proof;
   - data-plane probe through the Xray TUN reaches operator-approved targets (Google, Telegram, ChatGPT);
   - no ambient default/split-default route is installed by Xray/Toad;
   - no unrelated interface/routing state changes.

**Probes (same on both platforms):**

- `curl -4 https://www.google.com` — HTTP 200, ≥10000 bytes
- `curl -4 https://api.telegram.org` — HTTP 2xx
- `curl -4 https://chat.openai.com` — HTTP 2xx (this is the DPI gate)

Record only redacted state:

- generation; interface name/ifindex; RouteReady; session health;
- endpoint hostname/port if safe to report; probe success/failure.

Do not record UUID/public key/short-id/full share link.

### Windows-specific observations

- xray-core's `tun_windows.go` creates the wintun adapter via `wintun.CreateAdapter(name, desc, md5-guid)` internally; Toad does not own the adapter handle.
- The adapter type is `wintun.TunnelType` set by xray-core (not `Kikimora Toad`).
- `net.InterfaceByName(cfg.Interface)` must find the adapter after xray starts.

---

## Phase 2 — real-server failure and recovery (both VMs)

With operator approval, induce only reversible failures possible in the current environment.

Preferred non-destructive cases:

1. temporarily block the remote endpoint route locally (e.g., `ip route add unreachable <endpoint-ip>`);
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

## Phase 3 — core-managed real Xray role (both VMs)

Only after standalone proof on each VM:

1. Add the real Xray role TOML to the installed core config:
   - **Linux**: copy TOML to `/etc/kikimora/toads/xray-real.toml`, add `leshy_zone` and `[endpoint_policy] rule_priority` if using a custom role name.
   - **Windows**: copy TOML to `C:\ProgramData\Kikimora\toads\xray-win.toml`, same annotations.

2. Enable ownership cutover (go+go):
   - **Linux**: edit `/etc/kikimora/leshy/orchestration-ownership.conf` → `routing_owner = "go"`, `tunnel_owner = "go"`.
   - **Windows**: edit `C:\ProgramData\Kikimora\ownership.conf` → same.

3. Restart core:
   - **Linux**: `sudo systemctl restart kikimora-core`.
   - **Windows**: `net stop KikimoraCore && net start KikimoraCore` (elevated).

4. Enable the role: `kk connect --role xray-real` (Linux) / `kikimora-core.exe connect --socket ... --role xray-win` (Windows).

5. Verify:
   - independent PID/generation/ifindex;
   - one Xray failure does not restart/recreate unrelated Toads;
   - Xray selected traffic remains fail-closed during loss;
   - final Ready uses current underlay epoch.

If 08A was already completed for AmneziaWG + OpenConnect on the same VM, this phase must not disturb their accepted state.

---

## Phase 4 — optional suspend/resume with real Xray

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

### Windows-specific note

The known wintun stale-adapter limitation (observed with OpenConnect during 08A.4b) may also affect xray-core's wintun adapter after VM savestate/resume. If Xray fails to recreate its adapter after resume, this should be documented as a known limitation (matching the OC behavior) and addressed as a follow-up. No pre-clear fix is included in this packet; the VM round will determine whether the issue exists for Xray.

---

## Completion

08C is complete after the real remote endpoint has been tested with production binaries on **both** Linux and Windows VMs and redacted evidence is recorded.

If the operator-provided secret passes ISP DPI:

- `external Xray full: real Google/Telegram/ChatGPT traffic proven on both platforms`.
- 08C becomes a complete external-Xray acceptance packet.

If the ISP still blocks the ChatGPT path:

- `external Xray partial: real Google/Telegram traffic proven; operator ISP DPI blocks further acceptance; hermetic official-Xray acceptance green`.
- 08C remains non-blocking for the AWG + OpenConnect chapter-08 product.

08C does not block 08A completion for AmneziaWG + OpenConnect.

---

## Executor report template

Keep the report redacted. One report per VM.

### Linux VM report

```text
HEAD: feat/native-core-vpn-clients (post-xhttp transport support)
real Xray profile source: local secret / stdin (not committed)
profile transport: VLESS/REALITY over xhttp (stream-one)
toad backend: xray-core v1.260327.1 tun inbound

standalone:
- TUN: kk-xray0, 10.255.0.2/30, MTU 1380
- RouteReady: true after real session proof
- Google probe: 200 (IPv4)
- Telegram probe: 302 (IPv4)
- ChatGPT probe: 308 (IPv4)

notes:
- Default route installed manually through kk-xray0 (gateway 10.255.0.1, metric 1)
- DNS set to 1.1.1.1
- IPv6 must be disabled/avoided for this provider profile; curl -4 required.
  Otherwise DNS resolution fails because the profile does not tunnel IPv6.
- Stale kk-awg0 static routes (1.1.1.1/32, 8.8.8.8/32, 140.82.121.4/32)
  interfered with DNS until removed.

failure/recovery:
- failure method: NOT RUN
- fail-closed: N/A
- recovered: N/A
- ifindex stable: N/A

core-managed:
- independent role: yes, xray-nl started/stopped independently of awg/oc
- unrelated roles stable: awg/oc remained Stopped while xray-nl was Ready
- final epoch: 1 (fresh core start)
- notes: default route and DNS were added manually through kk-xray0 for
  probing; Kikimora core itself does not replace default route.

manual suspend/resume:
- NOT RUN

secrets committed/logged: NO
```

### Windows VM report

```text
HEAD: feat/native-core-vpn-clients (post-xhttp transport support)
real Xray profile source: local secret / stdin (not committed)
profile transport: VLESS/REALITY over xhttp (stream-one)
toad backend: xray-core v1.260327.1 tun inbound (wintun)

standalone:
- TUN: kk-xray2, address from TOML
- RouteReady: true after real session proof
- Google probe: 200
- Telegram probe: 302
- ChatGPT probe: 308
- wintun adapter created by xray-core: yes

notes:
- A stale adapter from a previous run (kk-xray-win0) was automatically
  cleaned up by xray-core on the next start.
- Temporary default route added through TUN for probing; DNS set to 1.1.1.1.

failure/recovery:
- failure method: NOT RUN
- fail-closed: N/A
- recovered: N/A
- ifindex stable: N/A

core-managed:
- independent role: N/A (standalone run only)
- unrelated roles stable: N/A
- final epoch: N/A

manual suspend/resume:
- NOT RUN (known wintun stale-adapter limitation documented in 08A.4b)

secrets committed/logged: NO
```

### Code changes made to support this run

- `toad/internal/backend/xray/config.go`: added `xrayXHTTPSettings`,
  `xrayXHTTPEExtra`, and `xrayXHTTPXmux`; wired `xhttp` transport into
  `buildCoreJSON` so VLESS/REALITY over xhttp (stream-one) produces a config
  accepted by pinned xray-core.
- `toad/internal/config/config.go`: added `VLESSXHTTPConfig` and allowed
  `transport = "xhttp"` in `VLESSRealityConfig.validate()`.
- `toad/internal/backend/xray/config_test.go`: added
  `TestBuildCoreJSONXHTTPTransport` to prove the generated JSON loads in
  pinned xray-core and preserves host/path/mode/padding/xmux settings.
