# Real VPS Toad diagnostic test plan

## Goal

Real VPS tests must be self-contained. The test script starts Toad, collects diagnostics before and after VPN activation, and leaves one diagnostic archive for analysis.

No manual `kk diag` command is involved.

## Output

Each run produces:

```
toad-real-vps-diag-YYYYMMDD-HHMMSS.tar.gz
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
start kikimora-toad
      |
wait for managed TUN
      |
collect active state
      |
run traffic checks
      |
run recovery checks
      |
collect final state
      |
package diagnostic archive
```

## Expected use

The first real VPS test must not replace the default route. It validates:

- protocol handshake;
- TUN creation;
- traffic through managed interface;
- reconnect behaviour;
- diagnostic completeness.

Only after successful review should routing policy be changed.
