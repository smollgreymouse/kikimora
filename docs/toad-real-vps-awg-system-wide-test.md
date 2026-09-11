# Real VPS AWG system-wide diagnostic test

## Purpose

This is the destructive host-routing follow-up to the isolated real-VPS preflight.

The script starts the real `kikimora-toad` AWG client in the root network namespace, temporarily makes `kk-awg0` the host IPv4 default route, optionally installs per-link systemd-resolved DNS on `kk-awg0`, runs automatic Google/ChatGPT probes, leaves the VPN active for a manual browser window, then automatically restores host routing/DNS and stops Toad.

The script **does not start, stop, restart, configure, or otherwise modify legacy Kikimora/Leshy**. Stop the old VPN yourself before starting this test.

## Prerequisite

First pass the isolated test:

```bash
./linux/tests/toad/real-vps-awg-diag-test.sh
```

Then stop legacy Kikimora yourself and verify that ordinary non-VPN Internet access still exists. The system-wide test deliberately refuses to start if the current default route or the route to the AWG VPS still uses a known VPN-like interface such as `vpn0`, `amn0`, `kk-*`, `tun*`, or `wg*`.

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
5. refuse to continue if the endpoint/default route appears to use an old VPN interface;
6. add an explicit `/32` route for the VPS endpoint through the original underlay;
7. start Toad in the root namespace and wait for `kk-awg0`;
8. start packet-header traces on `kk-awg0` and the physical endpoint transport;
9. add a lower-metric IPv4 default route through `kk-awg0`;
10. when systemd-resolved is available, configure `kk-awg0` as the default DNS route using `1.1.1.1` and `1.0.0.1` by default;
11. resolve Google/ChatGPT using the active system resolver and run ordinary curl requests **without `--interface`**, proving the system default path;
12. require Google HTTP 200 and advancing TUN RX/TX counters;
13. record ChatGPT 2xx as PASS and non-2xx HTTP responses such as 403 as application WARN, not VPN failure;
14. leave the VPN active for manual browser testing;
15. continuously sample routes, Toad state and TUN counters during that manual phase;
16. on Enter or safety timeout, remove the Toad default route, revert Toad DNS, remove the endpoint pin, stop Toad, collect post-state and create one diagnostic archive.

## Manual browser window

When automatic probes pass, the terminal prints:

```text
SYSTEM-WIDE TOAD VPN IS ACTIVE.
Use the browser now.
Press Enter to finish and restore the original route ...
```

Use Chrome/Firefox normally during this period. Do not add proxy or interface-specific browser settings: the point is to exercise the host-wide default route.

The default safety timeout is 600 seconds. Override it before the run if needed:

```bash
TOAD_SYSTEM_WIDE_HOLD_SECONDS=900 ./linux/tests/toad/real-vps-awg-system-wide-diag-test.sh
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
    -> sanitize + archive
```

The same rollback runs on normal completion, command failure, Ctrl-C, SIGTERM, or manual-window timeout.

## Output archive

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
- `tun-trace.txt` packet headers for all traffic crossing `kk-awg0` during the active phase;
- `underlay-trace.txt` packet headers for only the encrypted AWG endpoint transport on the original physical uplink;
- system and kernel journal excerpts since the test started;
- automatic HTTP probe details and public-IP observations.

Packet traces use a short snap length (`96`) and are textual address/header traces, not full packet payload captures.

## Acceptance

The test is a system-wide data-plane PASS when:

- the VPS endpoint remains routed through the original underlay after cutover;
- ordinary host default traffic resolves to `kk-awg0`;
- Google returns HTTP 200 through the system default path;
- AWG reports an online/recent handshake;
- TUN RX and TX counters advance;
- Toad remains alive throughout the automatic and manual active window;
- cleanup removes the temporary default route and stops/removes `kk-awg0`.

A ChatGPT HTTP response such as 403 is recorded separately as an application warning. A transport failure is also recorded, but Google remains the independent VPN data-plane gate.

After the run, send the resulting `.tar.gz` archive for analysis.
