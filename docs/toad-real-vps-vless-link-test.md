# Toad real-VPS smoke — VLESS/REALITY/Vision link

Use the self-contained harness from the repository root:

```bash
./linux/tests/toad/real-vps-vless-diag-test.sh 'vless://...'
```

The current client accepts VLESS + REALITY over TCP/raw, including
`xtls-rprx-vision` when present in the share link. Treat the URI and its UUID as
bearer secrets.

The test builds or selects `kikimora-toad`, imports and validates the link, then
creates a unique network namespace. `slirp4netns`, `kk-xray0`, namespace DNS and
the `/32` routes to Google and ChatGPT exist only inside that namespace. Existing VPN
clients, old Kikimora interfaces, host routes, policy rules, NAT and firewall
rules are not changed.

PASS requires a Google response with exact HTTP 200 and at least 10000 bytes, a
ChatGPT HTTP 2xx response with at least 1024 bytes, increasing RX/TX counters,
and a packet trace proving both destinations crossed the namespace-local
`kk-xray0` while only the encrypted VPS endpoint crossed the isolated uplink.
Successful application traffic is the VLESS connection proof; the
current Xray backend deliberately keeps its conservative lifecycle health at
`connecting`. The harness does not restart or modify the VPS.

Prerequisites and archive contents are documented in
`docs/toad-real-vps-test-plan.md`. Use `TOAD_BIN=/path/to/kikimora-toad` for a
prebuilt client or `TOAD_TEST_HOST=example.com` to override the ChatGPT target.
