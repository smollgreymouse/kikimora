# Toad real-VPS client test plan

## Scope

The harness checks one thing: the local kikimora-toad client can import the
supplied profile and carry HTTPS traffic through the real VPS. It never restarts
or changes the remote server. Toad, its TUN, DNS and all test routes live in a
new per-run network namespace; the root namespace is used only as a
slirp4netns underlay.

## Before the test

Do not stop the currently working VPN, old Kikimora, SSH, the primary network
interface, the default route or the management firewall. AWG uses kk-awg0 and
VLESS/REALITY uses kk-xray0 only inside the isolated namespace, so interfaces
with those names may remain active on the host. The harness never adds host
routes, NAT rules, policy rules or firewall rules.

Required commands: go, sudo, ip, slirp4netns, curl, tcpdump, getent, python3, ss and tar.
Run sudo -v first for a non-interactive test. Set TOAD_BIN to use a prebuilt
binary; otherwise the checked-out client is built in a private temporary
directory.

## Run

    ./linux/tests/toad/real-vps-awg-diag-test.sh 'vpn://...'
    ./linux/tests/toad/real-vps-vless-diag-test.sh 'vless://...'

For a temporary local AWG credential, put one link in
`linux/tests/toad/real-vps-awg-link.secret`, set mode `0600`, and run the AWG
script without an argument. Files ending in `.secret` are ignored by Git.

To keep the link out of interactive shell history:

    read -rsp 'Share link: ' TOAD_SHARE_LINK; echo
    ./linux/tests/toad/real-vps-awg-diag-test.sh "$TOAD_SHARE_LINK"
    unset TOAD_SHARE_LINK

The link remains visible in the harness argv while it runs, but archived process
data omits argv. Import uses stdin, and all archived text is redacted. The
ChatGPT target is chatgpt.com by default; TOAD_TEST_HOST may override it.

## Stages and result

The stages are: collect BEFORE, build/select the client, import and validate,
create an isolated namespace and user-space uplink, start the client inside it,
wait for its interface and state.json, add namespace-local /32 routes, probe
Google and ChatGPT, verify packet addresses, the process and counters, collect
AFTER, clean up, redact and
package. There is no local or remote restart stage.

PASS requires successful import and validation, a live client, the expected
interface, HTTP 200 plus at least 10000 response bytes from the Google page, and
an HTTP 2xx plus at least 1024 response bytes from chatgpt.com. A 403 from
ChatGPT is FAIL. Header-only success is insufficient: the size thresholds make
each probe transfer multiple packets. tcpdump must observe both destination
addresses on the Toad TUN and the encrypted VPN endpoint on the isolated uplink,
with no direct Google or ChatGPT destination packet on that uplink. PASS also
requires increasing RX and TX counters, an AWG handshake when applicable, and
complete namespace cleanup. Any pre-existing root interface with the same name
must keep the same ifindex. Failure returns a non-zero status but still runs
cleanup and packages diagnostics. The size thresholds can be raised with
TOAD_GOOGLE_MIN_BYTES and TOAD_CHATGPT_MIN_BYTES.

## Diagnostic archive

The current directory receives toad-real-vps-diag-YYYY-MM-DD-HHMMSS.tar.gz with
one diag directory containing:

    metadata.txt
    command.log
    config-summary.json
    state-before.json
    state-after.json
    network-before.txt
    network-after.txt
    routes-before.txt
    routes-after.txt
    dns-before.txt
    dns-after.txt
    interfaces-before.txt
    interfaces-after.txt
    sockets.txt
    processes.txt
    kernel.txt
    journal.txt
    toad.log
    traffic-test.txt
    address-trace.txt
    errors.txt

The archive mode is 0600. Share links, private keys, preshared keys, VLESS UUIDs
and REALITY private keys are redacted. Endpoint, protocol, interface, ifindex,
MTU, counters, errors and timing remain available. The credential-bearing TOML
is never archived and is removed after packaging.

## Where to look

| Symptom | Evidence |
|---|---|
| Link rejected | command.log, errors.txt |
| Client exited | toad.log, errors.txt, kernel.txt |
| Interface missing | toad.log, interfaces-after.txt, processes.txt |
| Namespace/uplink problem | command.log, network-after.txt, routes-after.txt |
| Route problem | routes-after.txt, traffic-test.txt |
| HTTP code or truncated response | traffic-test.txt, toad.log, state-after.json, sockets.txt |
| Wrong path or direct leak | address-trace.txt, traffic-test.txt, routes-after.txt |
| Counters unchanged | interfaces-after.txt, state-after.json |
| DNS or underlay failure | dns-before.txt, dns-after.txt, network-before.txt |
