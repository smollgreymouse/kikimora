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

stop_process() {
    local pid="$1" signal_name="${2:-TERM}" i
    [[ -n "$pid" ]] || return 0
    if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        return 0
    fi
    kill "-$signal_name" "$pid" 2>/dev/null || true
    for ((i = 0; i < 100; i++)); do
        if ! kill -0 "$pid" 2>/dev/null; then
            wait "$pid" 2>/dev/null || true
            return 0
        fi
        sleep 0.05
    done
    kill -KILL "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
}

cleanup() {
    local rc=$?
    set +e
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
    stop_process "$TOAD_PID" INT
    stop_process "$SERVER_PID" TERM
    stop_process "$COVER_PID" TERM
    stop_process "$PAYLOAD_PID" TERM
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
  "outbounds": [{
    "tag": "direct",
    "protocol": "freedom",
    "settings": {"finalRules": [{"action": "allow"}]}
  }]
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

listener_gone() {
    local ns="$1" port="$2"
    ! listener_ready "$ns" "$port"
}

xray_interface_ready() {
    ip -n "$CLIENT_NS" link show dev "$XRAY_IF" >/dev/null 2>&1
}

http_probe_in_ns() {
    local ns="$1"
    ip netns exec "$ns" python3 - "$PAYLOAD_IP" "$PAYLOAD_PORT" <<'PY' >/dev/null 2>&1
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
response = bytearray()
try:
    with socket.create_connection((host, port), timeout=1.0) as sock:
        sock.settimeout(1.0)
        sock.sendall(b"GET /probe HTTP/1.0\r\nHost: payload.test\r\nConnection: close\r\n\r\n")
        while len(response) < 65536:
            data = sock.recv(4096)
            if not data:
                break
            response.extend(data)
            if b"200 OK" in response and b"toad-xray-interop-ok" in response:
                raise SystemExit(0)
except OSError:
    pass
raise SystemExit(1)
PY
}

payload_probe() {
    http_probe_in_ns "$CLIENT_NS"
}

server_payload_probe() {
    http_probe_in_ns "$SERVER_NS"
}

wait_for_payload() {
    local attempts="${1:-60}" i
    for ((i = 0; i < attempts; i++)); do
        if payload_probe; then
            return 0
        fi
        sleep 0.25
    done
    return 1
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

stop_reference_server() {
    stop_process "$SERVER_PID" TERM
    SERVER_PID=""
    if ! wait_until 5000 listener_gone "$SERVER_NS" 443; then
        echo "ERROR: reference Xray listener survived server stop" >&2
        return 1
    fi
}

ip netns exec "$SERVER_NS" "$XRAY_COVER_BIN" -listen "$COVER_ADDR" -server-name "$COVER_NAME" >"$COVER_LOG" 2>&1 &
COVER_PID=$!
wait_until 5000 listener_ready "$SERVER_NS" 8443 || {
    echo "ERROR: hermetic REALITY cover did not start" >&2
    exit 1
}
assert_process_alive "$COVER_PID" "REALITY cover"

ip netns exec "$SERVER_NS" python3 -u -m http.server "$PAYLOAD_PORT" --bind "$PAYLOAD_IP" --directory "$PAYLOAD_DIR" >"$PAYLOAD_LOG" 2>&1 &
PAYLOAD_PID=$!
wait_until 5000 listener_ready "$SERVER_NS" "$PAYLOAD_PORT" || {
    echo "ERROR: private payload endpoint did not start" >&2
    exit 1
}
assert_process_alive "$PAYLOAD_PID" "payload endpoint"
if ! server_payload_probe; then
    echo "ERROR: private payload endpoint failed direct server namespace self-check" >&2
    exit 1
fi

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

if ! wait_for_payload 60; then
    echo "ERROR: REALITY + VLESS + Vision payload did not pass through production Toad" >&2
    exit 1
fi
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "initial REALITY/Vision payload"

stop_reference_server
assert_process_alive "$TOAD_PID" "production Toad after server stop"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "reference server outage"
if payload_probe; then
    echo "ERROR: new payload unexpectedly succeeded while reference Xray was stopped" >&2
    exit 1
fi

start_reference_server
if ! wait_for_payload 60; then
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
if ! wait_for_payload 60; then
    echo "ERROR: payload did not recover after private underlay restore" >&2
    exit 1
fi
assert_process_alive "$TOAD_PID" "production Toad after underlay recovery"
assert_ifindex "$CLIENT_NS" "$XRAY_IF" "$XRAY_IFINDEX" "underlay recovery"
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
assert_no_root_interface "$XRAY_IF"

stop_process "$TOAD_PID" INT
TOAD_PID=""
if ip -n "$CLIENT_NS" link show dev "$XRAY_IF" >/dev/null 2>&1; then
    echo "ERROR: Xray-owned TUN survived deliberate Toad shutdown" >&2
    exit 1
fi

echo "Toad Xray interop passed: REALITY+VLESS+Vision payload, server restart and underlay recovery kept ifindex=$XRAY_IFINDEX"
