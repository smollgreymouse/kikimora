# Real VPS Toad diagnostic test plan

## Goal

Real VPS tests must be self-contained. The test script starts Toad, collects diagnostics before and after VPN activation, and leaves one diagnostic archive for analysis.

No manual `kk diag` command is involved.

These scripts are an **isolated real-VPS preflight**. They prove the real provider profile and VPS work with the Toad client without modifying the host default route. Full-system cutover is a separate acceptance step.

## Output

Each run produces:

```
toad-real-vps-diag-YYYY-MM-DD-HHMMSS.tar.gz
```

The archive contains:

- metadata and test parameters (without secrets);
- command execution log;
- Toad state snapshots;
- network state before/after;
- interfaces and routes before/after;
- DNS state;
- sockets/processes;
- kernel/network diagnostics;
- Toad logs;
- traffic test results;
- address-only packet trace for TUN destinations and VPS transport;
- fatal errors in `errors.txt`;
- non-fatal application observations in `warnings.txt`.

`metadata.txt` separates:

- `result` — overall test result;
- `data_plane_result` — whether the VPN/TUN data plane was proven;
- `chatgpt_application_result` — result of the ChatGPT HTTP application probe.

An HTTP response such as `403` from ChatGPT is **not** a VPN protocol failure. If the response came from the expected destination through the Toad TUN, it is recorded as a warning such as `WARN_HTTP_403`; protocol acceptance continues using the independent strict Google data-plane probe, packet trace, Toad health and interface counters.

## Secret handling

Never store:

- private keys;
- VLESS UUID;
- REALITY private keys;
- preshared keys.

Store only fingerprints and operational state.

## Test lifecycle

```
collect baseline
      |
import share link
      |
create isolated namespace + slirp uplink
      |
start kikimora-toad
      |
wait for managed TUN
      |
collect active state
      |
run traffic checks
      |
verify client connection and traffic counters
      |
collect final state and counters even on failure
      |
package diagnostic archive
```

## Protocol/data-plane PASS criteria

The real-VPS preflight passes the VPN data plane only when all mandatory checks succeed:

- Toad creates the expected managed TUN;
- the VPN endpoint remains routed over the isolated underlay rather than recursively through the TUN;
- the strict Google HTTPS probe returns HTTP 200 with the configured minimum body size;
- packet trace observes Google traffic on the managed TUN;
- packet trace observes encrypted/provider transport on the isolated uplink;
- no direct Google/ChatGPT destination traffic leaks onto the underlay;
- Toad remains alive;
- interface RX/TX counters advance;
- AWG additionally reaches the existing `online`/connected handshake state.

The ChatGPT probe is deliberately **application diagnostic**, not the protocol oracle. `2xx` is recorded as application success; a valid non-2xx HTTP response is a warning because TCP/TLS/HTTP transport already succeeded; transport failure is also reported separately for diagnosis and does not override a successful independent VPN data-plane proof.

## Failure completeness

The finalizer attempts to record `traffic_probe_ms`, `rx_after`, `tx_after`, `state-after.json` and runtime/network state before tearing down the Toad and namespace, even when a probe fails midway. Fields can remain `unavailable` only when the test failed before the relevant interface/probe existed.

## Expected use

The real VPS test must not add or replace any host route. Toad and the routes to Google and ChatGPT stay inside a disposable network namespace. The test validates:

- successful application connectivity through the client;
- TUN creation;
- traffic through managed interface;
- a strict independent data-plane HTTP result;
- ChatGPT application behavior as a separately classified observation;
- target addresses on TUN, encrypted endpoint on uplink, and no direct leak;
- increasing interface traffic counters;
- diagnostic completeness;
- cleanup without changing an existing VPN or pre-Toad Kikimora instance.

A successful isolated preflight proves that the real VPS/provider profile works through Toad. It does **not** prove host-wide default-route or DNS cutover; that must be tested separately with rollback protection.
