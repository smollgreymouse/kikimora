#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

: "${TOAD_BIN:?TOAD_BIN must point to the repo-built kikimora-toad binary}"
: "${AWG_REF_BIN:?AWG_REF_BIN must point to the pinned official amneziawg-go reference binary}"

CLIENT_NS="toad-awg-client-$$"
SERVER_NS="toad-awg-server-$$"
TMPDIR="$(mktemp -d)"
CLIENT_LOG="$TMPDIR/client.log"
SERVER_LOG="$TMPDIR/server.log"
CLIENT_CONFIG="$TMPDIR/client.toml"
SERVER_CONFIG="$TMPDIR/server.uapi"
STATE_FILE="$TMPDIR/state.json"
SERVER_SOCKET="/var/run/amneziawg/awg-ref0.sock"
CLIENT_PID=""
SERVER_PID=""
CLIENT_IFINDEX=""
SERVER_RESTART_MS=""
UNDERLAY_RECOVERY_MS=""

cleanup() {
    set +e
    if [[ -n "$CLIENT_PID" ]] && kill -0 "$CLIENT_PID" 2>/dev/null; then
        kill -INT "$CLIENT_PID" 2>/dev/null
        wait "$CLIENT_PID" 2>/dev/null
    fi
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill -TERM "$SERVER_PID" 2>/dev/null
        wait "$SERVER_PID" 2>/dev/null
    fi
    rm -f "$SERVER_SOCKET"
    ip netns delete "$CLIENT_NS" 2>/dev/null
    ip netns delete "$SERVER_NS" 2>/dev/null
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

uapi_get() {
    python3 - "$SERVER_SOCKET" <<'PY'
import socket
import sys

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(2)
sock.connect(sys.argv[1])
sock.sendall(b"get=1\n\n")
data = bytearray()
while b"\n\n" not in data:
    chunk = sock.recv(65536)
    if not chunk:
        break
    data.extend(chunk)
sock.close()
sys.stdout.write(data.decode("utf-8", "replace"))
PY
}

uapi_set() {
    python3 - "$SERVER_SOCKET" "$SERVER_CONFIG" <<'PY'
import socket
import sys

with open(sys.argv[2], "rb") as fh:
    config = fh.read().rstrip(b"\n")
request = b"set=1\n" + config + b"\n\n"
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(2)
sock.connect(sys.argv[1])
sock.sendall(request)
data = bytearray()
while b"\n\n" not in data:
    chunk = sock.recv(65536)
    if not chunk:
        break
    data.extend(chunk)
sock.close()
if b"errno=0\n\n" not in data:
    raise SystemExit("official AWG UAPI set failed: " + data.decode("utf-8", "replace"))
PY
}

server_field() {
    local field="$1"
    uapi_get | python3 -c 'import sys
key=sys.argv[1]+"="
value=""
for line in sys.stdin:
    if line.startswith(key):
        value=line[len(key):].strip()
print(value)' "$field"
}

state_field() {
    local field="$1"
    python3 - "$STATE_FILE" "$field" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        value = json.load(fh)
    for part in sys.argv[2].split("."):
        value = value[part]
except (FileNotFoundError, KeyError, TypeError, json.JSONDecodeError):
    print("")
    raise SystemExit(0)
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("")
else:
    print(value)
PY
}

sanitize_uapi() {
    sed -E 's/^(private_key|preshared_key|public_key|header_protection_key)=.*/\1=<redacted>/'
}

dump_diagnostics() {
    set +e
    echo "=== sanitized AWG2 interop diagnostics ===" >&2
    if [[ -s "$CLIENT_LOG" ]]; then
        echo "--- Toad stdout/stderr ---" >&2
        cat "$CLIENT_LOG" >&2
    fi
    if [[ -s "$STATE_FILE" ]]; then
        echo "--- Toad state.json ---" >&2
        cat "$STATE_FILE" >&2
    fi
    if ip netns list | grep -q "^$CLIENT_NS\b"; then
        echo "--- client links ---" >&2
        ip -n "$CLIENT_NS" link show >&2
        echo "--- client addresses ---" >&2
        ip -n "$CLIENT_NS" addr show >&2
        echo "--- client routes ---" >&2
        ip -n "$CLIENT_NS" route show table all >&2
        echo "--- client UDP sockets ---" >&2
        ip netns exec "$CLIENT_NS" ss -lunp >&2 || true
        echo "--- client veth counters ---" >&2
        ip -n "$CLIENT_NS" -s link show dev veth-c >&2 || true
    fi
    if [[ -s "$SERVER_LOG" ]]; then
        echo "--- reference log ---" >&2
        cat "$SERVER_LOG" >&2
    fi
    if [[ -S "$SERVER_SOCKET" ]]; then
        echo "--- reference UAPI (redacted) ---" >&2
        uapi_get 2>/dev/null | sanitize_uapi >&2 || true
    fi
    if ip netns list | grep -q "^$SERVER_NS\b"; then
        echo "--- reference links ---" >&2
        ip -n "$SERVER_NS" link show >&2
        echo "--- reference addresses ---" >&2
        ip -n "$SERVER_NS" addr show >&2
        echo "--- reference routes ---" >&2
        ip -n "$SERVER_NS" route show table all >&2
        echo "--- reference UDP sockets ---" >&2
        ip netns exec "$SERVER_NS" ss -lunp >&2 || true
        echo "--- reference veth counters ---" >&2
        ip -n "$SERVER_NS" -s link show dev veth-s >&2 || true
    fi
    echo "==========================================" >&2
}

fail() {
    echo "ERROR: $*" >&2
    dump_diagnostics
    exit 1
}

assert_process_alive() {
    local pid="$1"
    local name="$2"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
        fail "$name process is not alive"
    fi
}

assert_same_client_ifindex() {
    local phase="$1"
    local line current
    line="$(ip -n "$CLIENT_NS" -o link show dev kk-awg0 2>/dev/null)" || fail "kk-awg0 missing during $phase"
    current="${line%%:*}"
    current="${current//[[:space:]]/}"
    if [[ "$current" != "$CLIENT_IFINDEX" ]]; then
        fail "kk-awg0 ifindex changed during $phase: got $current want $CLIENT_IFINDEX"
    fi
}

assert_isolated() {
    local ns="$1"
    if ip -n "$ns" -4 route show default | grep -q .; then
        fail "$ns unexpectedly has an IPv4 default route"
    fi
    if ip -n "$ns" -6 route show default | grep -q .; then
        fail "$ns unexpectedly has an IPv6 default route"
    fi
}

wait_for_socket_and_link() {
    for _ in $(seq 1 200); do
        if [[ -S "$SERVER_SOCKET" ]] && ip -n "$SERVER_NS" link show dev awg-ref0 >/dev/null 2>&1; then
            return 0
        fi
        assert_process_alive "$SERVER_PID" "reference AWG2"
        sleep 0.05
    done
    fail "timed out waiting for official reference UAPI/interface"
}

start_server() {
    rm -f "$SERVER_SOCKET"
    : >"$SERVER_LOG"
    ip netns exec "$SERVER_NS" env LOG_LEVEL=error "$AWG_REF_BIN" -f awg-ref0 >"$SERVER_LOG" 2>&1 &
    SERVER_PID=$!
    wait_for_socket_and_link
    uapi_set || fail "failed to configure official reference server"
    ip -n "$SERVER_NS" addr add 10.77.0.1/24 dev awg-ref0
    ip -n "$SERVER_NS" link set awg-ref0 up
    if ip link show dev awg-ref0 >/dev/null 2>&1; then
        fail "awg-ref0 leaked into root namespace"
    fi
}

stop_server() {
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill -TERM "$SERVER_PID"
        wait "$SERVER_PID" || fail "official reference failed during shutdown"
    fi
    SERVER_PID=""
    for _ in $(seq 1 100); do
        if ! ip -n "$SERVER_NS" link show dev awg-ref0 >/dev/null 2>&1; then
            rm -f "$SERVER_SOCKET"
            return 0
        fi
        sleep 0.05
    done
    fail "reference interface remained after reference shutdown"
}

wait_client_online() {
    for _ in $(seq 1 240); do
        assert_process_alive "$CLIENT_PID" "Toad"
        if [[ "$(state_field state)" == "online" ]] && [[ "$(state_field session.connected)" == "true" ]]; then
            return 0
        fi
        sleep 0.05
    done
    fail "Toad did not report a real AWG2 handshake"
}

wait_state_age_at_least() {
    local threshold="$1"
    for _ in $(seq 1 100); do
        local age
        age="$(state_field session.last_handshake_age_ms)"
        if [[ "$age" =~ ^[0-9]+$ ]] && (( age >= threshold )); then
            return 0
        fi
        sleep 0.05
    done
    fail "handshake age did not grow to ${threshold}ms during reference outage"
}

wait_fresh_handshake() {
    for _ in $(seq 1 240); do
        assert_process_alive "$CLIENT_PID" "Toad"
        local age
        age="$(state_field session.last_handshake_age_ms)"
        if [[ "$(state_field state)" == "online" ]] && [[ "$age" =~ ^[0-9]+$ ]] && (( age <= 750 )); then
            return 0
        fi
        ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 1 -W 1 10.77.0.1 >/dev/null 2>&1 || true
        sleep 0.05
    done
    fail "fresh handshake was not observed after reference restart"
}

wait_ping() {
    for _ in $(seq 1 120); do
        assert_process_alive "$CLIENT_PID" "Toad"
        if ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 1 -W 1 10.77.0.1 >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.05
    done
    fail "encrypted tunnel ping did not recover"
}

wait_client_counters_advance() {
    local before_rx="$1"
    local before_tx="$2"
    for _ in $(seq 1 120); do
        local rx tx
        rx="$(state_field session.rx_bytes)"
        tx="$(state_field session.tx_bytes)"
        rx="${rx:-0}"
        tx="${tx:-0}"
        if [[ "$rx" =~ ^[0-9]+$ ]] && [[ "$tx" =~ ^[0-9]+$ ]] && (( rx > before_rx && tx > before_tx )); then
            return 0
        fi
        sleep 0.05
    done
    fail "Toad RX/TX counters did not advance"
}

wait_server_counters_advance() {
    local before_rx="$1"
    local before_tx="$2"
    for _ in $(seq 1 120); do
        local rx tx
        rx="$(server_field rx_bytes)"
        tx="$(server_field tx_bytes)"
        rx="${rx:-0}"
        tx="${tx:-0}"
        if [[ "$rx" =~ ^[0-9]+$ ]] && [[ "$tx" =~ ^[0-9]+$ ]] && (( rx > before_rx && tx > before_tx )); then
            return 0
        fi
        sleep 0.05
    done
    fail "reference RX/TX counters did not advance"
}

mkdir -p /var/run/amneziawg
rm -f "$SERVER_SOCKET"

cat >"$CLIENT_CONFIG" <<EOF
name = "awg2-interop"
protocol = "amneziawg2"
interface = "kk-awg0"
address = ["10.77.0.2/24"]
mtu = 1380
state_dir = "$TMPDIR"

[awg2]
private_key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
peer_public_key = "zo060cy2M+x7cMF4FKXHbs0CloUFDTRHRboFhw5YfVk="
endpoint = "192.0.2.1:51820"
allowed_ips = ["10.77.0.1/32"]
persistent_keepalive = 1
jc = 4
jmin = 40
jmax = 80
s1 = 15
s2 = 16
s3 = 17
s4 = 18
h1 = "1001"
h2 = "1002"
h3 = "1003"
h4 = "1004"
i1 = "<r 8><t>"
i2 = "<rd 6>"
i3 = "<rc 6>"
i4 = "<b 0x01020304>"
i5 = "<r 4><rc 4>"
EOF
chmod 0600 "$CLIENT_CONFIG"

cat >"$SERVER_CONFIG" <<'EOF'
private_key=0202020202020202020202020202020202020202020202020202020202020202
listen_port=51820
replace_peers=true
jc=4
jmin=40
jmax=80
s1=15
s2=16
s3=17
s4=18
h1=1001
h2=1002
h3=1003
h4=1004
i1=<r 8><t>
i2=<rd 6>
i3=<rc 6>
i4=<b 0x01020304>
i5=<r 4><rc 4>
public_key=a4e09292b651c278b9772c569f5fa9bb13d906b46ab68c9df9dc2b4409f8a209
replace_allowed_ips=true
allowed_ip=10.77.0.2/32
persistent_keepalive_interval=1
EOF
chmod 0600 "$SERVER_CONFIG"

ip netns add "$CLIENT_NS"
ip netns add "$SERVER_NS"
ip link add veth-c type veth peer name veth-s
ip link set veth-c netns "$CLIENT_NS"
ip link set veth-s netns "$SERVER_NS"
ip -n "$CLIENT_NS" addr add 192.0.2.2/30 dev veth-c
ip -n "$SERVER_NS" addr add 192.0.2.1/30 dev veth-s
ip -n "$CLIENT_NS" link set lo up
ip -n "$SERVER_NS" link set lo up
ip -n "$CLIENT_NS" link set veth-c up
ip -n "$SERVER_NS" link set veth-s up
assert_isolated "$CLIENT_NS"
assert_isolated "$SERVER_NS"

if ip link show dev kk-awg0 >/dev/null 2>&1 || ip link show dev awg-ref0 >/dev/null 2>&1; then
    fail "AWG interface unexpectedly exists in root namespace before test"
fi

# Phase A: official reference plus real Toad client handshake.
start_server
ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$CLIENT_CONFIG" >"$CLIENT_LOG" 2>&1 &
CLIENT_PID=$!

for _ in $(seq 1 200); do
    if ip -n "$CLIENT_NS" link show dev kk-awg0 >/dev/null 2>&1; then
        break
    fi
    assert_process_alive "$CLIENT_PID" "Toad"
    sleep 0.05
done
if ! ip -n "$CLIENT_NS" link show dev kk-awg0 >/dev/null 2>&1; then
    fail "timed out waiting for kk-awg0"
fi
CLIENT_LINK="$(ip -n "$CLIENT_NS" -o link show dev kk-awg0)"
CLIENT_IFINDEX="${CLIENT_LINK%%:*}"
CLIENT_IFINDEX="${CLIENT_IFINDEX//[[:space:]]/}"
if [[ ! "$CLIENT_IFINDEX" =~ ^[1-9][0-9]*$ ]]; then
    fail "invalid client ifindex $CLIENT_IFINDEX"
fi
assert_isolated "$CLIENT_NS"
assert_isolated "$SERVER_NS"
wait_client_online

# Phase B: real encrypted data plane and counters on both official endpoints.
CLIENT_RX0="$(state_field session.rx_bytes)"; CLIENT_RX0="${CLIENT_RX0:-0}"
CLIENT_TX0="$(state_field session.tx_bytes)"; CLIENT_TX0="${CLIENT_TX0:-0}"
SERVER_RX0="$(server_field rx_bytes)"; SERVER_RX0="${SERVER_RX0:-0}"
SERVER_TX0="$(server_field tx_bytes)"; SERVER_TX0="${SERVER_TX0:-0}"
ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 3 -W 1 10.77.0.1 >/dev/null || fail "baseline encrypted ping failed"
wait_client_counters_advance "$CLIENT_RX0" "$CLIENT_TX0"
wait_server_counters_advance "$SERVER_RX0" "$SERVER_TX0"
assert_same_client_ifindex "baseline traffic"

# Phase C: reference restart. Toad and its route-target TUN must survive unchanged.
stop_server
assert_process_alive "$CLIENT_PID" "Toad"
assert_same_client_ifindex "reference outage"
wait_state_age_at_least 1000
SERVER_RECOVERY_START="$(date +%s%3N)"
start_server
wait_fresh_handshake
wait_ping
SERVER_RESTART_MS=$(( $(date +%s%3N) - SERVER_RECOVERY_START ))
assert_same_client_ifindex "reference restart recovery"
SERVER_RX1="$(server_field rx_bytes)"; SERVER_RX1="${SERVER_RX1:-0}"
SERVER_TX1="$(server_field tx_bytes)"; SERVER_TX1="${SERVER_TX1:-0}"
ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 2 -W 1 10.77.0.1 >/dev/null || fail "post-restart encrypted ping failed"
wait_server_counters_advance "$SERVER_RX1" "$SERVER_TX1"

# Phase D: private underlay loss/restore with the Toad process and TUN unchanged.
CLIENT_RX1="$(state_field session.rx_bytes)"; CLIENT_RX1="${CLIENT_RX1:-0}"
CLIENT_TX1="$(state_field session.tx_bytes)"; CLIENT_TX1="${CLIENT_TX1:-0}"
ip -n "$CLIENT_NS" link set veth-c down
assert_process_alive "$CLIENT_PID" "Toad"
assert_same_client_ifindex "underlay outage"
if ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 1 -W 1 10.77.0.1 >/dev/null 2>&1; then
    fail "tunnel traffic unexpectedly succeeded with client underlay down"
fi
UNDERLAY_RECOVERY_START="$(date +%s%3N)"
ip -n "$CLIENT_NS" link set veth-c up
wait_ping
UNDERLAY_RECOVERY_MS=$(( $(date +%s%3N) - UNDERLAY_RECOVERY_START ))
assert_same_client_ifindex "underlay recovery"
ip netns exec "$CLIENT_NS" ping -I kk-awg0 -c 2 -W 1 10.77.0.1 >/dev/null || fail "post-underlay encrypted ping failed"
wait_client_counters_advance "$CLIENT_RX1" "$CLIENT_TX1"
assert_isolated "$CLIENT_NS"
assert_isolated "$SERVER_NS"

# Phase E: clean owner-driven removal.
kill -INT "$CLIENT_PID"
if ! wait "$CLIENT_PID"; then
    CLIENT_PID=""
    fail "Toad failed during clean shutdown"
fi
CLIENT_PID=""
for _ in $(seq 1 100); do
    if ! ip -n "$CLIENT_NS" link show dev kk-awg0 >/dev/null 2>&1; then
        break
    fi
    sleep 0.05
done
if ip -n "$CLIENT_NS" link show dev kk-awg0 >/dev/null 2>&1; then
    fail "kk-awg0 remained after final Toad owner shutdown"
fi
stop_server
if ip link show dev kk-awg0 >/dev/null 2>&1 || ip link show dev awg-ref0 >/dev/null 2>&1; then
    fail "AWG interface leaked into root namespace after cleanup"
fi

echo "Toad AWG2 isolated interop passed: ifindex=$CLIENT_IFINDEX server_restart_ms=$SERVER_RESTART_MS underlay_recovery_ms=$UNDERLAY_RECOVERY_MS profile=J4/40-80,S15-18,H1001-1004,I1-I5"
