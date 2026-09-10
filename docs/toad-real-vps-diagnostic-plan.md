# Real VPS Toad diagnostic test plan

## Goal

Real VPS tests must be self-contained. The test script starts Toad, collects diagnostics before and after VPN activation, and leaves one diagnostic archive for analysis.

No manual `kk diag` command is involved.

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
- detected errors.

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
collect final state
      |
package diagnostic archive
```

## Expected use

The real VPS test must not add or replace any host route. Toad and the routes to
Google and ChatGPT stay inside a disposable network namespace. The test validates:

- successful application connectivity through the client;
- TUN creation;
- traffic through managed interface;
- exact HTTP status and minimum downloaded response sizes;
- target addresses on TUN, encrypted endpoint on uplink, and no direct leak;
- increasing interface traffic counters;
- diagnostic completeness.
- cleanup without changing an existing VPN or pre-Toad Kikimora instance.
