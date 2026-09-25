# Real VPS AWG system-wide diagnostic test

## Purpose

This is the destructive host-routing follow-up to the isolated real-VPS preflight.

The script starts the real `kikimora-toad` AWG client in the root network namespace, temporarily makes `kk-awg0` the host IPv4 default route, optionally installs per-link systemd-resolved DNS on `kk-awg0`, runs automatic Google/ChatGPT probes, leaves the VPN active for a manual browser window, then automatically restores host routing/DNS and stops Toad.

The script **does not start, stop, restart, configure, or otherwise modify legacy Kikimora/Leshy**. Stop the old VPN yourself before starting this test. The preflight fails if `leshy.service`, `leshy-route-watch.service` or `leshy-health-watch.service` is active, if the Leshy process is still running, or if the actual default/endpoint route still goes through a VPN-like interface.

## Prerequisite

First pass the isolated test:

```bash
./linux/tests/toad/real-vps-awg-diag-test.sh
```

Then stop legacy Kikimora yourself and verify that ordinary non-VPN Internet access still exists. For a systemd installation this normally means:

```bash
sudo systemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service
```

The test only checks that the old stack is stopped; it never performs this operation itself. It rejects an active Leshy unit/process and refuses to start when the current default route or route to the AWG VPS still uses a VPN-like interface. Merely existing stale interfaces such as `vpn0` are ignored because clients may keep them after disconnecting.

## Secret link

The runner uses the same local ignored secret file as the isolated AWG test:

```text
linux/tests/toad/real-vps-awg-link.secret
```

Keep it mode `0600`. The file must contain one supported `vpn://`, `wg://`, `wireguard://`, or `amneziawg://` share link.

You may also pass the link as argv, but using the secret file avoids putting credentials in shell history/process arguments.

## Run

From the repository root:

```bash
chmod 0600 linux/tests/toad/real-vps-awg-link.secret
./linux/tests/toad/real-vps-awg-system-wide-diag-test.sh
```

The script will:

1. collect a full host baseline;
2. build the checked-out `kikimora-toad` unless `TOAD_BIN` is supplied;
3. import/validate the AWG share link;
4. resolve the real VPS endpoint and record the current physical underlay route;
5. refuse to continue if a legacy service/process is active or an actual default/endpoint route still uses the old VPN;
6. add an explicit `/32` route for the VPS endpoint through the original underlay;
7. start Toad in the root namespace and wait for `kk-awg0`;
8. add a lower-metric IPv4 default route through `kk-awg0`;
9. start packet-header traces on `kk-awg0` and the original physical uplink;
10. when systemd-resolved is available, configure `kk-awg0` as the default DNS route using `1.1.1.1` and `1.0.0.1` by default;
11. resolve Google/ChatGPT using the active system resolver and run ordinary curl requests **without `--interface`**, pinned to those exact resolved addresses;
12. require Google HTTP 200 with at least 10000 response bytes and ChatGPT HTTP 2xx with at least 1024 bytes;
13. require both destination addresses in the TUN trace, encrypted VPS traffic on the underlay, and no direct Google/ChatGPT destination packet on the underlay;
14. leave the VPN active for manual browser testing;
15. continuously sample routes, Toad state and TUN counters during that manual phase;
16. on Enter or safety timeout, remove the Toad default route, revert Toad DNS, remove the endpoint pin, stop Toad, collect post-state, compact noisy diagnostics and create one bounded archive.

## Manual browser window

When automatic probes pass, the terminal prints:

```text
SYSTEM-WIDE TOAD VPN IS ACTIVE.
Use the browser now.
Press Enter to finish and restore the original route ...
```

Use Chrome/Firefox normally during this period. Do not add proxy or interface-specific browser settings: the point is to exercise the host-wide default route.

The default safety timeout is **120 seconds**. That gives roughly two minutes for manual browser checks and then automatically rolls back the host route/DNS even if Enter is not pressed.

Override it when needed:

```bash
TOAD_SYSTEM_WIDE_HOLD_SECONDS=180 ./linux/tests/toad/real-vps-awg-system-wide-diag-test.sh
```

If stdin is not interactive, the script keeps the VPN active until the timeout and then rolls back automatically.

## DNS

If `systemd-resolved`/`resolvectl` is available, the test configures DNS only on the newly-created `kk-awg0` interface and uses it as `~.` default DNS route. Default servers:

```text
1.1.1.1 1.0.0.1
```

Override for a test run with:

```bash
TOAD_SYSTEM_WIDE_DNS='9.9.9.9 149.112.112.112' ./linux/tests/toad/real-vps-awg-system-wide-diag-test.sh
```

If systemd-resolved is unavailable, the script leaves existing DNS untouched and records a warning. It never rewrites `/etc/resolv.conf` directly.

## Automatic rollback order

Cleanup intentionally restores host connectivity before stopping Toad:

```text
stop active sampler
    -> remove kk-awg0 default route
    -> revert kk-awg0 resolved state
    -> remove explicit VPS endpoint /32 route
    -> stop packet traces
    -> stop kikimora-toad
    -> collect final host snapshot
    -> compact + sanitize + archive
```

The same rollback runs on normal completion, command failure, Ctrl-C, SIGTERM, or manual-window timeout.

## Output archive and size control

The default archive is:

```text
toad-system-wide-awg-diag-YYYY-MM-DD-HHMMSS.tar.gz
```

It contains, without the imported secret profile:

- `metadata.txt` with overall/data-plane/ChatGPT results, endpoint underlay and counters;
- `command.log`, `errors.txt`, `warnings.txt`, `toad.log`;
- sanitized `config-summary.json`;
- Toad state snapshots before cutover, active, active-final and after shutdown;
- `before/`, `toad-up/`, `active/`, `active-final/`, `after/` snapshots containing interfaces, all routing tables/rules, DNS, sockets, processes, NetworkManager/networkctl state, firewall rules and selected sysctls;
- `active-sampler.txt` sampled every two seconds during the manual browser window;
- `tun-trace.txt` packet headers for traffic crossing `kk-awg0` during the active phase;
- `underlay-trace.txt` outbound packet headers for the encrypted AWG endpoint transport plus Google/ChatGPT leak detection on the original physical uplink;
- system and kernel journal excerpts since the test started;
- automatic HTTP probe details and public-IP observations;
- `size-report.txt` showing which text files were compacted and their before/after sizes.

Packet traces use a short snap length (`96`) and are textual address/header traces, not full packet payload captures.

Before creating the archive the harness post-processes noisy text. Small/structured files are preserved verbatim. Large traces, journals, runtime/firewall snapshots and logs keep a bounded **head + tail**, with an explicit marker describing the omitted middle. This preserves startup context and the final failure/cleanup context without uploading minutes of repetitive browser packets.

The default compressed archive target is about **4 MiB**. If the first tar.gz is still larger, the harness automatically applies a second tighter text-only compaction pass and rebuilds the archive. Structured JSON state/metadata are not truncated.

Override the target only if necessary:

```bash
TOAD_SYSTEM_WIDE_DIAG_TARGET_BYTES=6291456 ./linux/tests/toad/real-vps-awg-system-wide-diag-test.sh
```

## Acceptance

The test is a system-wide PASS when:

- the VPS endpoint remains routed through the original underlay after cutover;
- ordinary host default traffic resolves to `kk-awg0`;
- Google returns HTTP 200 with a sufficiently large body through the system default path;
- ChatGPT returns HTTP 2xx with a sufficiently large body; HTTP 403 is FAIL;
- packet traces prove both destinations crossed `kk-awg0`, only the VPS endpoint crossed the physical uplink, and no direct destination packet leaked there;
- AWG reports an online/recent handshake;
- TUN RX and TX counters advance;
- Toad remains alive throughout the automatic and manual active window;
- cleanup removes the temporary default route and stops/removes `kk-awg0`.

Google success independently records `data_plane_result=PASS`. A later ChatGPT 403, short body, wrong remote address or transport failure still makes the overall test fail while preserving the route, trace and counter evidence in the archive.

After the run, send the resulting `.tar.gz` archive for analysis.

## Persistent system-wide on/off controller

For normal manual use after the diagnostics, use the separate AWG controller:

```bash
./linux/tests/toad/real-vps-awg-system-wide-vpn.sh up
./linux/tests/toad/real-vps-awg-system-wide-vpn.sh status
./linux/tests/toad/real-vps-awg-system-wide-vpn.sh down
```

To keep a redacted archive for a persistent controller session, enable it
when bringing the connection up:

```bash
./linux/tests/toad/real-vps-awg-system-wide-vpn.sh up --diag
# ... use the system-wide AWG connection ...
./linux/tests/toad/real-vps-awg-system-wide-vpn.sh down
```

`down` writes the archive after recording the last active state and the
post-cleanup state. By default it is named
`toad-system-wide-awg-controller-diag-YYYY-MM-DD-HHMMSS.tar.gz` in the
current directory. `up --diag-output /safe/path/awg-session.tar.gz` selects a
destination and enables recording. If `up` fails after recording begins, the
rollback produces the archive too. It contains sanitized config metadata,
before/active/after network, route, DNS and runtime snapshots, Toad state,
Google probe evidence and the controller/kernel journal; share links,
private keys and PSK are excluded.

Run it as the desktop user without placing `sudo` before the whole command. It
uses the same ignored `linux/tests/toad/real-vps-awg-link.secret` file, rejects
active Leshy/Toad/NetworkManager VPNs and effective VPN routes, but ignores a
stale disconnected `vpn0` that has no VPN route.

The controller owns a transient `kikimora-toad-system-wide-awg-<uid>.service`,
`kk-awg0`, a lower-metric IPv4 default route, per-link systemd-resolved DNS and
an endpoint pin through the original physical uplink. `up` requires Google
HTTP 200 with at least 10000 bytes and an AWG online/connected handshake.
`down` restores the normal route and DNS before stopping Toad, then removes
only its owned interface, route, unit, normalized secret config, binary and
runtime state. The secret config/state are root-only under `/run`; the
root-owned executable copy uses a unique `/tmp/kikimora-toad-system-wide-awg-<uid>.XXXXXX`
directory so a host that mounts `/run` with `noexec` can still start Toad.
`down` removes both. It does not delete a pre-existing `vpn0`.
