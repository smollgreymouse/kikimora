# Toad real-VPS smoke — AWG link

Use the self-contained harness from the repository root:

```bash
./linux/tests/toad/real-vps-awg-diag-test.sh 'wg://...'
```

Accepted schemes are `vpn://`, `wg://`, `wireguard://` and `amneziawg://`.
`vpn://` supports Amnezia's URL-safe Base64 + qCompress JSON export and extracts
the embedded AWG/AWG2 native config. The other schemes contain a
WireGuard/AmneziaWG profile or equivalent supported query fields. Treat the
link as a bearer secret.

The test builds or selects `kikimora-toad`, imports and validates the link, then
creates a unique network namespace. `slirp4netns`, `kk-awg0`, namespace DNS and
the `/32` routes to Google and ChatGPT exist only inside that namespace. Existing VPN
clients, old Kikimora interfaces, host routes, policy rules, NAT and firewall
rules are not changed.

PASS requires a real AWG handshake, a Google response with exact HTTP 200 and
at least 10000 bytes, a ChatGPT HTTP 2xx response with at least 1024 bytes, and
increasing RX/TX counters on the namespace-local `kk-awg0`. A packet trace must
also prove that both targets crossed `kk-awg0`, while only the encrypted VPS
endpoint crossed `toad-uplink0`. The harness does not restart or modify the VPS.

Prerequisites and archive contents are documented in
`docs/toad-real-vps-test-plan.md`. Use `TOAD_BIN=/path/to/kikimora-toad` for a
prebuilt client or `TOAD_TEST_HOST=example.com` to override the ChatGPT target.
