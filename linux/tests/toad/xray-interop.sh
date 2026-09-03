#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/netns.sh
source "$SCRIPT_DIR/lib/netns.sh"

TOAD_BIN="${TOAD_BIN:?TOAD_BIN must point to the checked-out kikimora-toad binary}"
XRAY_REF_BIN="${XRAY_REF_BIN:?XRAY_REF_BIN must point to the pinned official Xray binary}"
XRAY_COVER_BIN="${XRAY_COVER_BIN:?XRAY_COVER_BIN must point to the hermetic TLS cover helper}"

CLIENT_NS="toad-xray-client-$$"
SERVER_NS="toad-xray-server-$$"
CLIENT_VETH="txc$$"
SERVER_VETH="txs$$"
XRAY_IF="kk-xray0"
UNDERLAY_CLIENT="192.0.2.1/30"
UNDERLAY_SERVER="192.0.2.2/30"
SERVER_ENDPOINT="192.0.2.2:443"
PAYLOAD_IP="10.66.0.1"
PAYLOAD_PORT="8080"
COVER_NAME="cover.test"
COVER_ADDR="127.0.0.1:8443"
TMP_DIR="$(mktemp -d)"
STATE_DIR="$TMP_DIR/state"
CLIENT_CONFIG="$TMP_DIR/client.toml"
SERVER_CONFIG="$TMP_DIR/server.json"
CLIENT_LOG="$TMP_DIR/client.log"
SERVER_LOG="$TMP_DIR/server.log"
COVER_LOG="$TMP_DIR/cover.log"
PAYLOAD_LOG="$TMP_DIR/payload.log"
PAYLOAD_DIR="$TMP_DIR/payload"
TOAD_PID=""
SERVER_PID=""
COVER_PID=""
PAYLOAD_PID=""
PRIVATE_KEY=""

cleanup() {
    local rc=$?
    set +e
    if [[ -n "$TOAD_PID" ]] && kill -0 "$TOAD_PID" 2>/dev/null; then
        kill -INT "$TOAD_PID" 2>/dev/null || true
        wait "$TOAD_PID" 2>/dev/null || true
    fi
    for pid in "$SERVER_PID" "$COVER_PID" "$PAYLOAD_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if (( rc != 0 )); then
        echo "--- Xray interop diagnostics ---" >&2
        dump_namespace "$CLIENT_NS"
        dump_namespace "$SERVER_NS"
        echo "--- Toad log ---" >&2
        tail -n 120 "$CLIENT_LOG" >&2 2>/dev/null || true
        echo "--- reference Xray log ---" >&2
        if [[ -n "$PRIVATE_KEY" ]]; then
            tail -n 120 "$SERVER_LOG" 2>/dev/null | sed "s/${PRIVATE_KEY}/[REDACTED]/g" >&2 || true
        else
            tail -n 120 "$SERVER_LOG" >&2 2>/dev/null || true
        fi
        echo "--- cover log ---" >&2
        tail -n 80 "$COVER_LOG" >&2 2>/dev/null || true
        echo "--- payload log ---" >&2
        tail -n 80 "$PAYLOAD_LOG" >&2 2>/dev/null || true
    fi
    netns_delete_if_present "$CLIENT_NS"
    netns_delete_if_present "$SERVER_NS"
    rm -rf "$TMP_DIR"
    exit "$rc"
}
trap cleanup EXIT

require_root
require_commands ip python3 ss sed awk grep
ensure_tun_device
mkdir -p "$STATE_DIR" "$PAYLOAD_DIR"
printf '%s\n' 'toad-xray-interop-ok' > "$PAYLOAD_DIR/probe"

assert_no_root_interface "$XRAY_IF"
netns_create_pair "$CLIENT_NS" "$SERVER_NS" "$CLIENT_VETH" "$SERVER_VETH" "$UNDERLAY_CLIENT" "$UNDERLAY_SERVER"
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
ip -n "$SERVER_NS" addr add "$PAYLOAD_IP/32" dev lo

KEY_OUTPUT="$($XRAY_REF_BIN x25519)"
PRIVATE_KEY="$(printf '%s\n' "$KEY_OUTPUT" | awk -F': ' '$1 == "PrivateKey" {print $2}')"
PUBLIC_KEY="$(printf '%s\n' "$KEY_OUTPUT" | awk -F': ' '$1 == "Password (PublicKey)" {print $2}')"
UUID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
SHORT_ID="$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
if [[ -z "$PRIVATE_KEY" || -z "$PUBLIC_KEY" || -z "$UUID" || ! "$SHORT_ID" =~ ^[0-9a-f]{16}$ ]]; then
    echo "ERROR: failed to generate ephemeral REALITY test credentials" >&2
    exit 1
fi

cat > "$SERVER_CONFIG" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "192.0.2.2",
    "port": 443,
    "protocol": "vless",
    "settings": {
      "clients": [{"id": "$UUID", "flow": "xtls-rprx-vision"}],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "raw",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "target": "$COVER_ADDR",
        "xver": 0,
        "serverNames": ["$COVER_NAME"],
        "privateKey": "$PRIVATE_KEY",
        "shortIds": ["$SHORT_ID"]
      }
    }
  }],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}]
}
EOF
chmod 0600 "$SERVER_CONFIG"

cat > "$CLIENT_CONFIG" <<EOF
name = "xray-interop"
protocol = "vless-reality"
interface = "$XRAY_IF"
address = ["10.41.0.2/30"]
mtu = 1380
state_dir = "$STATE_DIR"

[vless_reality]
endpoint = "$SERVER_ENDPOINT"
uuid = "$UUID"
server_name = "$COVER_NAME"
public_key = "$PUBLIC_KEY"
short_id = "$SHORT_ID"
flow = "xtls-rprx-vision"
fingerprint = "chrome"
transport = "raw"
spider_x = "/"
EOF
chmod 0600 "$CLIENT_CONFIG"

listener_ready() {
    local ns="$1" port="$2"
    ip netns exec "$ns" sh -c "ss -ltn | grep -Eq '[:.]${port}[[:space:]]'"
}

xray_interface_ready() {
    ip -n "$CLIENT_NS" link show dev "$XRAY_IF" >/dev/null 2>&1
}

payload_probe() {
    ip netns exec "$CLIENT_NS" python3 - "$PAYLOAD_IP" "$PAYLOAD_PORT" <<'PY' >/dev/null 2>&1
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
try:
    with socket.create_connection((host, port), timeout=2.0) as sock:
        sock.settimeout(2.0)
        sock.sendall(b"GET /probe HTTP/1.0\r\nHost: payload.test\r\nConnection: close\r\n\r\n")
        chunks = []
        while True:
            data = sock.recv(4096)
            if not data:
                break
            chunks.append(data)
except OSError:
    raise SystemExit(1)
response = b"".join(chunks)
if b"200 OK" not in response or b"toad-xray-interop-ok" not in response:
    raise SystemExit(1)
PY
}

start_reference_server() {
    : > "$SERVER_LOG"
    ip netns exec "$SERVER_NS" "$XRAY_REF_BIN" run -config "$SERVER_CONFIG" >"$SERVER_LOG" 2>&1 &
    SERVER_PID=$!
    if ! wait_until 10000 listener_ready "$SERVER_NS" 443; then
        echo "ERROR: reference Xray server did not listen on port 443" >&2
        return 1
    fi
    assert_process_alive "$SERVER_PID" "reference Xray"
}

ip netns exec "$SERVER_NS" "$XRAY_COVER_BIN" -listen "$COVER_ADDR" -server-name "$COVER_NAME" >"$COVER_LOG" 2>&1 &
COVER_PID=$!
wait_until 5000 listener_ready "$SERVER_NS" 8443 || {
    echo "ERROR: hermetic REALITY cover did not start" >&2
    exit 1
}
assert_process_alive "$COVER_PID" "REALITY cover"

ip netns exec "$SERVER_NS" python3 -m http.server "$PAYLOAD_PORT" --bind "$PAYLOAD_IP" --directory "$PAYLOAD_DIR" >"$PAYLOAD_LOG" 2>&1 &
PAYLOAD_PID=$!
wait_until 5000 listener_ready "$SERVER_NS" "$PAYLOAD_PORT" || {
    echo "ERROR: private payload endpoint did not start" >&2
    exit 1
}
assert_process_alive "$PAYLOAD_PID" "payload endpoint"

start_reference_server

ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$CLIENT_CONFIG" >"$CLIENT_LOG" 2>&1 &
TOAD_PID=$!
if ! wait_until 10000 xray_interface_ready; then
    echo "ERROR: production Toad did not create $XRAY_IF" >&2
    exit 1
fi
assert_process_alive "$TOAD_PID" "production Toad"
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
assert_no_root_interface "$XRAY_IF"

XRAY_IFINDEX="$(interface_ifindex "$CLIENT_NS" "$XRAY_IF")"
ip -n "$CLIENT_NS" route add "$PAYLOAD_IP/32" dev "$XRAY_IF"

if ! wait_until 45000 payload_probe; then
    echo "ERROR: REALITY + VLESS + Vision payload did not pass through production Toad" >&2
    exit 1
fi
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "initial REALITY/Vision payload"

kill "$SERVER_PID"
wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""
wait_until 5000 sh -c "! ip netns exec '$SERVER_NS' ss -ltn | grep -Eq '[:.]443[[:space:]]'" || true
assert_process_alive "$TOAD_PID" "production Toad after server stop"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "reference server outage"
if payload_probe; then
    echo "ERROR: new payload unexpectedly succeeded while reference Xray was stopped" >&2
    exit 1
fi

start_reference_server
if ! wait_until 45000 payload_probe; then
    echo "ERROR: payload did not recover after reference Xray restart" >&2
    exit 1
fi
assert_process_alive "$TOAD_PID" "production Toad after server restart"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "reference server restart recovery"

ip -n "$CLIENT_NS" link set "$CLIENT_VETH" down
assert_process_alive "$TOAD_PID" "production Toad during underlay outage"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "underlay outage"
if payload_probe; then
    echo "ERROR: new payload unexpectedly succeeded while client underlay was down" >&2
    exit 1
fi
ip -n "$CLIENT_NS" link set "$CLIENT_VETH" up
if ! wait_until 45000 payload_probe; then
    echo "ERROR: payload did not recover after private underlay restore" >&2
    exit 1
fi
assert_process_alive "$TOAD_PID" "production Toad after underlay recovery"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "underlay recovery"
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
assert_no_root_interface "$XRAY_IF"

kill -INT "$TOAD_PID"
wait "$TOAD_PID"
TOAD_PID=""
if ip -n "$CLIENT_NS" link show dev "$XRAY_IF" >/dev/null 2>&1; then
    echo "ERROR: Xray-owned TUN survived deliberate Toad shutdown" >&2
    exit 1
fi

echo "Toad Xray interop passed: REALITY+VLESS+Vision payload, server restart and underlay recovery kept ifindex=$XRAY_IFINDEX"
