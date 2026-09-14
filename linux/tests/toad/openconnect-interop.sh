#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/netns.sh
source "$ROOT/lib/netns.sh"

require_root
require_commands ip ss curl python3 openssl ocpasswd ocserv openconnect awk grep sed
ensure_tun_device

TOAD_BIN="${TOAD_BIN:-$ROOT/../../../toad/kikimora-toad}"
OPENCONNECT_BIN="${OPENCONNECT_BIN:-$(command -v openconnect)}"
OCSERV_BIN="${OCSERV_BIN:-$(command -v ocserv)}"
[[ -x "$TOAD_BIN" ]] || { echo "ERROR: TOAD_BIN is not executable: $TOAD_BIN" >&2; exit 1; }
[[ -x "$OPENCONNECT_BIN" ]] || { echo "ERROR: OPENCONNECT_BIN is not executable: $OPENCONNECT_BIN" >&2; exit 1; }
[[ -x "$OCSERV_BIN" ]] || { echo "ERROR: OCSERV_BIN is not executable: $OCSERV_BIN" >&2; exit 1; }

CLIENT_NS="toad-oc-client-$$"
SERVER_NS="toad-oc-server-$$"
CLIENT_IF="oc-client0"
SERVER_IF="oc-server0"
TUN_IF="kk-oc0"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-interop.XXXXXX")"
STATE_DIR="$TMP/state"
OCSERV_PID=""
TOAD_PID=""
PAYLOAD_PID=""

cleanup() {
    local status=$?
    set +e
    if [[ -n "$TOAD_PID" ]]; then kill -INT "$TOAD_PID" 2>/dev/null; wait "$TOAD_PID" 2>/dev/null; fi
    if [[ -n "$OCSERV_PID" ]]; then kill -TERM "$OCSERV_PID" 2>/dev/null; wait "$OCSERV_PID" 2>/dev/null; fi
    if [[ -n "$PAYLOAD_PID" ]]; then kill -TERM "$PAYLOAD_PID" 2>/dev/null; wait "$PAYLOAD_PID" 2>/dev/null; fi
    if (( status != 0 )); then
        echo "--- Toad log ---" >&2
        sed -n '1,240p' "$TMP/toad.log" >&2 2>/dev/null || true
        echo "--- ocserv log ---" >&2
        sed -n '1,240p' "$TMP/ocserv.log" >&2 2>/dev/null || true
        dump_namespace "$CLIENT_NS"
        dump_namespace "$SERVER_NS"
    fi
    netns_delete_if_present "$CLIENT_NS"
    netns_delete_if_present "$SERVER_NS"
    rm -rf -- "$TMP"
    exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$STATE_DIR"
chmod 0700 "$TMP" "$STATE_DIR"

netns_create_pair "$CLIENT_NS" "$SERVER_NS" "$CLIENT_IF" "$SERVER_IF" "192.0.2.1/30" "192.0.2.2/30"
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
assert_no_root_interface "$TUN_IF"

# Private application endpoint. There is no NAT and no public route.
ip -n "$SERVER_NS" addr add 10.66.0.1/32 dev lo
ip netns exec "$SERVER_NS" python3 -u -m http.server 8080 --bind 10.66.0.1 >"$TMP/payload.log" 2>&1 &
PAYLOAD_PID=$!

# Runtime-only self-signed server certificate. The client verifies its SPKI pin.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj '/CN=ocserv.test' \
    -keyout "$TMP/server.key" -out "$TMP/server.crt" >/dev/null 2>&1
chmod 0600 "$TMP/server.key"
PIN="pin-sha256:$(openssl x509 -in "$TMP/server.crt" -pubkey -noout | \
    openssl pkey -pubin -outform DER 2>/dev/null | openssl dgst -sha256 -binary | openssl base64 -A)"

TEST_USER="toad-test"
TEST_PASSWORD="synthetic-openconnect-password"
printf '%s\n%s\n' "$TEST_PASSWORD" "$TEST_PASSWORD" | \
    ocpasswd -c "$TMP/ocpasswd" "$TEST_USER" >/dev/null 2>&1
chmod 0600 "$TMP/ocpasswd"
printf '%s\n' "$TEST_PASSWORD" >"$TMP/client-password"
chmod 0600 "$TMP/client-password"

cat >"$TMP/ocserv.conf" <<EOF
foreground = true
pid-file = $TMP/ocserv.pid
socket-file = $TMP/ocserv.sock
auth = "plain[passwd=$TMP/ocpasswd]"
tcp-port = 4443
udp-port = 4443
listen-host = 192.0.2.2
run-as-user = nobody
run-as-group = nogroup
server-cert = $TMP/server.crt
server-key = $TMP/server.key
device = ocserv
ipv4-network = 10.77.0.0/24
route = 10.66.0.1/255.255.255.255
max-clients = 4
max-same-clients = 2
keepalive = 5
dpd = 10
mobile-dpd = 10
try-mtu-discovery = true
EOF

ip netns exec "$SERVER_NS" "$OCSERV_BIN" -f -d 1 -c "$TMP/ocserv.conf" >"$TMP/ocserv.log" 2>&1 &
OCSERV_PID=$!
wait_until 5000 ip netns exec "$SERVER_NS" ss -lnt | grep -q ':4443 ' || {
    echo "ERROR: ocserv did not listen on 4443" >&2
    exit 1
}

cat >"$TMP/toad.toml" <<EOF
name = "openconnect-interop"
protocol = "openconnect"
interface = "$TUN_IF"
mtu = 1380
state_dir = "$STATE_DIR"

[openconnect]
gateway = "https://192.0.2.2:4443"
vpn_protocol = "anyconnect"
username = "$TEST_USER"
password_file = "$TMP/client-password"
token_mode = "none"
server_cert = "$PIN"
disable_udp = false
disable_ipv6 = true
reconnect_timeout = 20
openconnect_binary = "$OPENCONNECT_BIN"
EOF
chmod 0600 "$TMP/toad.toml"

ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$TMP/toad.toml" >"$TMP/toad.log" 2>&1 &
TOAD_PID=$!

wait_until 15000 ip -n "$CLIENT_NS" link show dev "$TUN_IF" >/dev/null 2>&1 || {
    echo "ERROR: $TUN_IF did not appear" >&2
    exit 1
}
assert_process_alive "$TOAD_PID" kikimora-toad
IFINDEX="$(interface_ifindex "$CLIENT_NS" "$TUN_IF")"

wait_until 5000 grep -q '"state": "online"' "$STATE_DIR/state.json" || {
    echo "ERROR: Toad never published online OpenConnect state" >&2
    exit 1
}

# Kikimora/Leshy own routing. OpenConnect's replacement script must not install
# server-pushed routes, so install only the explicit payload route for the gate.
if ip -n "$CLIENT_NS" route show 10.66.0.1/32 | grep -q .; then
    echo "ERROR: OpenConnect backend installed payload route itself" >&2
    exit 1
fi
ip -n "$CLIENT_NS" route add 10.66.0.1/32 dev "$TUN_IF"

probe_payload() {
    ip netns exec "$CLIENT_NS" curl -4fsS --interface "$TUN_IF" --connect-timeout 3 --max-time 8 \
        http://10.66.0.1:8080/ | grep -q 'Directory listing'
}
wait_until 8000 probe_payload || {
    echo "ERROR: real OpenConnect payload probe failed" >&2
    exit 1
}

RX_BEFORE="$(ip netns exec "$CLIENT_NS" cat "/sys/class/net/$TUN_IF/statistics/rx_bytes")"
TX_BEFORE="$(ip netns exec "$CLIENT_NS" cat "/sys/class/net/$TUN_IF/statistics/tx_bytes")"
[[ "$RX_BEFORE" =~ ^[0-9]+$ && "$TX_BEFORE" =~ ^[0-9]+$ ]] || exit 1
assert_ifindex "$CLIENT_NS" "$TUN_IF" "$IFINDEX" "initial payload"

# Ordinary underlay loss must be recovered by the same OpenConnect child and
# must not recreate the route-target TUN.
ip -n "$CLIENT_NS" link set "$CLIENT_IF" down
sleep 1
assert_ifindex "$CLIENT_NS" "$TUN_IF" "$IFINDEX" "underlay down"
ip -n "$CLIENT_NS" link set "$CLIENT_IF" up
wait_until 15000 probe_payload || {
    echo "ERROR: OpenConnect did not recover after underlay down/up" >&2
    exit 1
}
assert_ifindex "$CLIENT_NS" "$TUN_IF" "$IFINDEX" "underlay recovery"

RX_AFTER="$(ip netns exec "$CLIENT_NS" cat "/sys/class/net/$TUN_IF/statistics/rx_bytes")"
TX_AFTER="$(ip netns exec "$CLIENT_NS" cat "/sys/class/net/$TUN_IF/statistics/tx_bytes")"
(( RX_AFTER > RX_BEFORE )) || { echo "ERROR: RX counter did not advance" >&2; exit 1; }
(( TX_AFTER > TX_BEFORE )) || { echo "ERROR: TX counter did not advance" >&2; exit 1; }

kill -INT "$TOAD_PID"
wait "$TOAD_PID"
TOAD_PID=""
wait_until 5000 bash -c "! ip -n '$CLIENT_NS' link show dev '$TUN_IF' >/dev/null 2>&1" || {
    echo "ERROR: $TUN_IF survived deliberate Toad shutdown" >&2
    exit 1
}

echo "Toad OpenConnect interop passed: real ocserv payload and underlay recovery kept ifindex=$IFINDEX"
