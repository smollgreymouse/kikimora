# Real VPS Xray system-wide diagnostic test

## Purpose and safety boundary

This is the host-routing follow-up to the isolated VLESS + REALITY test. It starts the checked-out `kikimora-toad` Xray client in the root network namespace, temporarily makes `kk-xray0` the host IPv4 default route, runs strict Google and ChatGPT probes, keeps the VPN active for a short manual browser window, and then restores host routing and DNS.

Unlike the isolated test, this script **does change the current host network**. Do not run it over a remote-only SSH connection without an out-of-band recovery path. It never changes or restarts the VPS.

The script never stops or modifies legacy Kikimora/Leshy. Stop it yourself first. For the systemd installation this normally means:

```bash
sudo systemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service
```

Preflight fails before starting Toad if one of those units or the Leshy process is active, or if the endpoint/default route actually uses a VPN-like interface. A disconnected but still existing `vpn0`, `amn0`, `tun*` or `wg*` interface does not block the test. This makes “old Kikimora is off” an enforced prerequisite without relying on interface presence and without granting the test permission to alter it.

## Prerequisite and link

First pass the isolated test:

```bash
./linux/tests/toad/real-vps-vless-diag-test.sh
```

The system-wide runner reads the same ignored `0600` secret file:

```text
linux/tests/toad/real-vps-vless-link.secret
```

It accepts a direct `vless://` link or an Amnezia `vpn://` qCompress export containing Xray VLESS + REALITY. The imported protocol must be exactly `vless-reality`; an AWG `vpn://` export is rejected after import.

## Run

From the repository root:

```bash
chmod 0600 linux/tests/toad/real-vps-vless-link.secret
./linux/tests/toad/real-vps-vless-system-wide-diag-test.sh
```

The runner uses the common `linux/tests/toad/lib/system-wide-diag.sh` lifecycle and `system-wide-policy.sh` bounded-archive policy. It:

1. captures the host baseline and verifies that the legacy stack/other VPNs are absent;
2. imports and validates the link without archiving the credential-bearing TOML;
3. resolves and pins the Xray VPS endpoint through the original physical uplink;
4. starts Toad and waits for `kk-xray0` plus `state.json`;
5. installs a lower-metric host IPv4 default route and per-link systemd-resolved DNS on `kk-xray0` when available;
6. resolves Google and ChatGPT with the active system resolver and curls their exact IPv4 addresses without an interface override;
7. requires Google HTTP 200 with at least 10000 bytes and ChatGPT HTTP 2xx with at least 1024 bytes;
8. requires both destination addresses in the `kk-xray0` trace and the Xray endpoint on the physical uplink trace, while rejecting a direct Google/ChatGPT packet on that uplink;
9. requires advancing TUN RX/TX counters and a live Toad process;
10. opens the bounded manual browser window, then removes the temporary route, reverts only `kk-xray0` resolver state, removes the endpoint pin, and stops Toad.

Xray does not expose the AWG handshake state, so its acceptance is based on the HTTPS application traffic, exact route/packet traces, counters and process lifecycle. The AWG-only online/handshake gate is intentionally skipped.

## Result and diagnostics

HTTP 403 from ChatGPT is an overall FAIL. If Google and the packet/counter gates passed first, the archive still records `data_plane_result=PASS` and `chatgpt_application_result=FAIL_HTTP_403`, which distinguishes a working tunnel from a wrong VPS egress region.

Default output:

```text
toad-system-wide-xray-diag-YYYY-MM-DD-HHMMSS.tar.gz
```

It contains sanitized metadata/config summary, before/active/after host snapshots, Toad states/logs, strict probe results, TUN and outbound-underlay packet-header traces, route/counter samples, journal excerpts, errors and warnings. VLESS UUID, share link, private keys, preshared keys and WireGuard key material are redacted; the imported profile is deleted before packaging.

The manual window defaults to 120 seconds and rolls back automatically. Override only when needed:

```bash
TOAD_SYSTEM_WIDE_HOLD_SECONDS=180 ./linux/tests/toad/real-vps-vless-system-wide-diag-test.sh
```

Response thresholds can be raised with `TOAD_GOOGLE_MIN_BYTES` and `TOAD_CHATGPT_MIN_BYTES`. The default compressed archive target is about 4 MiB and can be changed with `TOAD_SYSTEM_WIDE_DIAG_TARGET_BYTES`.

## Persistent system-wide on/off controller

For ordinary manual use after diagnostics, use the separate controller rather than leaving the diagnostic test running:

```bash
./linux/tests/toad/real-vps-vless-system-wide-vpn.sh up
./linux/tests/toad/real-vps-vless-system-wide-vpn.sh status
./linux/tests/toad/real-vps-vless-system-wide-vpn.sh down
```

For a controller session that must leave one redacted diagnostic archive, add
`--diag` to `up` and call `down` normally when finished:

```bash
./linux/tests/toad/real-vps-vless-system-wide-vpn.sh up --diag
# ... use the system-wide Xray connection ...
./linux/tests/toad/real-vps-vless-system-wide-vpn.sh down
```

The archive is written only by `down`, after it has captured the final active
state and the post-cleanup state. Its default name is
`toad-system-wide-xray-controller-diag-YYYY-MM-DD-HHMMSS.tar.gz` in the
current directory. Choose an explicit destination with
`up --diag-output /safe/path/xray-session.tar.gz`; this option also enables
recording. A failed `up` packages the evidence collected before rollback.
The archive contains sanitized config metadata, before/active/after network,
route, DNS and runtime snapshots, Toad state, Google probe result and the
controller/kernel journal. It never contains the share link, VLESS UUID,
private keys or PSK.

Run it as the desktop user, without prefixing the whole command with `sudo`; the script requests sudo only for its transient root systemd unit, route and resolver operations. `up` reads `linux/tests/toad/real-vps-vless-link.secret` unless a link is explicitly supplied.

Before changing anything, `up` rejects active legacy Leshy units/processes, active Toad instance units, active NetworkManager VPN connections and VPN-like interfaces that occur in effective non-link routes. A persistent but disconnected `vpn0` with no VPN routes is ignored. The controller also refuses an existing `kk-xray0` or an IPv6 default route; the latter prevents unintentional IPv6 bypass while this controller manages only IPv4.

The endpoint receives an owned `/32` route through the original physical uplink before `kk-xray0` becomes the lower-metric IPv4 default. DNS is assigned only to `kk-xray0` through systemd-resolved. Enabling succeeds only after Google returns HTTP 200 with at least 10000 bytes through the new default route. ChatGPT is not an `up` gate because its HTTP 403 may come from regional/IP policy or automated-client protection even when packet tracing proves the tunnel path.

The Toad process runs as the transient `kikimora-toad-system-wide-xray-<uid>.service`. Its secret normalized config, ownership record and state live in a root-only `/run/kikimora-toad-system-wide-xray-<uid>` directory. The root-owned executable copy is kept in a unique `/tmp/kikimora-toad-system-wide-xray-<uid>.XXXXXX` directory because hardened hosts may mount `/run` with `noexec`; `down` removes both directories.

`down` is idempotent and restores connectivity before stopping Toad. It removes only the controller-owned IPv4 default route, per-link DNS, endpoint pin, transient unit, `kk-xray0`, normalized secret config, binary and runtime state. It does not remove the unrelated persistent `vpn0`. If ownership validation or cleanup fails, it returns an error and preserves its `/run` state for inspection and another `down` attempt.
