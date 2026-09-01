#!/usr/bin/env bash
set -Eeuo pipefail

[[ $# -eq 2 ]] || { echo "usage: $0 /path/to/kikimora-vpn /path/to/xray" >&2; exit 2; }
VPN_BIN="$(readlink -f -- "$1")"
XRAY_BIN="$(readlink -f -- "$2")"
readonly VPN_BIN XRAY_BIN
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck source=linux/tests/vpn-client/netns-lib.sh
source "${SCRIPT_DIR}/netns-lib.sh"

readonly CLIENT_NS="kkxr-c-${RANDOM}-$$"
readonly SERVER_NS="kkxr-s-${RANDOM}-$$"
readonly CLIENT_VETH="kkxr-cv"
readonly SERVER_VETH="kkxr-sv"
readonly CLIENT_IFACE="kk-xray0"
readonly UUID="00010203-0405-0607-0809-0a0b0c0d0e0f"
readonly REALITY_PRIVATE="AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
readonly REALITY_PUBLIC="pOCSkrZRwni5dyxWn1-puxPZBrRqtoyd-dwrRAn4ogk"
readonly SHORT_ID="0123456789abcdef"
readonly SERVER_NAME="stage0.invalid"
WORK="$(mktemp -d)"
readonly WORK
readonly STATE_DIR="${WORK}/state"
readonly CLIENT_CONFIG="${WORK}/client.toml"
readonly SERVER_CONFIG="${WORK}/server.json"
readonly CLIENT_LOG="${WORK}/client.log"
readonly SERVER_LOG="${WORK}/xray-server.log"
readonly TARGET_LOG="${WORK}/target.log"
readonly HTTP_LOG="${WORK}/http.log"
readonly CERT="${WORK}/target.crt"
readonly KEY="${WORK}/target.key"
readonly WWW="${WORK}/www"

cleanup() {
    local status=$?
    remove_netns "$CLIENT_NS"
    remove_netns "$SERVER_NS"
    assert_host_interface_absent "$CLIENT_IFACE" || status=$?
    if ((status != 0)); then
        printf '\n--- client state ---\n' >&2
        cat "${STATE_DIR}/state.json" >&2 2>/dev/null || true
        printf '\n--- client log ---\n' >&2
        cat "$CLIENT_LOG" >&2 2>/dev/null || true
        printf '\n--- Xray server log ---\n' >&2
        cat "$SERVER_LOG" >&2 2>/dev/null || true
        printf '\n--- REALITY target log ---\n' >&2
        cat "$TARGET_LOG" >&2 2>/dev/null || true
        printf '\n--- HTTP target log ---\n' >&2
        cat "$HTTP_LOG" >&2 2>/dev/null || true
    fi
    sudo rm -rf -- "$WORK"
    exit "$status"
}
trap cleanup EXIT

for binary in "$VPN_BIN" "$XRAY_BIN"; do
    [[ -x "$binary" ]] || { echo "binary not executable: $binary" >&2; exit 1; }
done
[[ -c /dev/net/tun ]] || { echo "/dev/net/tun is unavailable" >&2; exit 1; }
for command in curl jq openssl python3 ss; do
    command -v "$command" >/dev/null || { echo "$command is required" >&2; exit 1; }
done

wait_state() {
    local expected="$1" attempts="${2:-120}" i
    for ((i=0; i<attempts; i++)); do
        if [[ -s "${STATE_DIR}/state.json" ]] && jq -e --arg state "$expected" '.state == $state' "${STATE_DIR}/state.json" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.25
    done
    printf 'timed out waiting for client state=%s\n' "$expected" >&2
    return 1
}

wait_listen() {
    local ns="$1" port="$2" attempts="${3:-120}" i
    for ((i=0; i<attempts; i++)); do
        if sudo ip netns exec "$ns" ss -ltnH | awk '{print $4}' | grep -Eq "(^|:)$port$"; then
            return 0
        fi
        sleep 0.1
    done
    printf 'timed out waiting for TCP port %s in %s\n' "$port" "$ns" >&2
    return 1
}

wait_http() {
    local attempts="${1:-100}" i body
    for ((i=0; i<attempts; i++)); do
        body="$(sudo ip netns exec "$CLIENT_NS" curl -fsS --connect-timeout 1 --max-time 2 --interface "$CLIENT_IFACE" http://10.77.0.1:8080/ 2>/dev/null || true)"
        if [[ "$body" == *"stage0-vless-ok"* ]]; then
            return 0
        fi
        sleep 0.2
    done
    echo "timed out waiting for VLESS/REALITY tunnel HTTP traffic" >&2
    return 1
}

kill_matching_in_ns() {
    local ns="$1" needle="$2" pid cmdline
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        cmdline="$(tr '\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
        if [[ "$cmdline" == *"$needle"* ]]; then
            sudo kill -TERM "$pid" 2>/dev/null || true
        fi
    done < <(ns_pids "$ns")
    for _ in {1..80}; do
        local found=0
        while read -r pid; do
            [[ -n "$pid" ]] || continue
            cmdline="$(tr '\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
            [[ "$cmdline" == *"$needle"* ]] && found=1
        done < <(ns_pids "$ns")
        ((found == 0)) && return 0
        sleep 0.05
    done
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        cmdline="$(tr '\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
        [[ "$cmdline" == *"$needle"* ]] && sudo kill -KILL "$pid" 2>/dev/null || true
    done < <(ns_pids "$ns")
}

start_reference() {
    sudo ip netns exec "$SERVER_NS" "$XRAY_BIN" run -config "$SERVER_CONFIG" 2>&1 | tee -a "$SERVER_LOG" >/dev/null &
    wait_listen "$SERVER_NS" 443 200
}

stop_reference() {
    kill_matching_in_ns "$SERVER_NS" "$XRAY_BIN"
}

assert_host_interface_absent "$CLIENT_IFACE"
mkdir -p "$WWW"
printf 'stage0-vless-ok\n' >"${WWW}/index.html"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -keyout "$KEY" -out "$CERT" -subj "/CN=${SERVER_NAME}" \
    -addext "subjectAltName=DNS:${SERVER_NAME}" >/dev/null 2>&1
chmod 600 "$KEY"

cat >"$SERVER_CONFIG" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "10.250.0.1",
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
        "target": "127.0.0.1:8443",
        "xver": 0,
        "serverNames": ["$SERVER_NAME"],
        "privateKey": "$REALITY_PRIVATE",
        "minClientVer": "0.0.0",
        "maxTimeDiff": 0,
        "shortIds": ["$SHORT_ID"]
      }
    }
  }],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}
EOF

cat >"$CLIENT_CONFIG" <<EOF
name = "ci-xray"
protocol = "vless-reality"
interface = "$CLIENT_IFACE"
address = ["10.77.0.2/24"]
mtu = 1380
state_dir = "$STATE_DIR"
queue_packets = 128
queue_bytes = 1048576

[vless_reality]
endpoint = "10.250.0.1:443"
uuid = "$UUID"
server_name = "$SERVER_NAME"
public_key = "$REALITY_PUBLIC"
short_id = "$SHORT_ID"
flow = "xtls-rprx-vision"
fingerprint = "chrome"
spider_x = "/"
transport = "raw"
EOF

"$VPN_BIN" --config "$CLIENT_CONFIG" --check
"$XRAY_BIN" run -test -config "$SERVER_CONFIG"

sudo ip netns add "$CLIENT_NS"
sudo ip netns add "$SERVER_NS"
sudo ip link add "$CLIENT_VETH" type veth peer name "$SERVER_VETH"
sudo ip link set "$CLIENT_VETH" netns "$CLIENT_NS"
sudo ip link set "$SERVER_VETH" netns "$SERVER_NS"
sudo ip netns exec "$CLIENT_NS" ip link set lo up
sudo ip netns exec "$SERVER_NS" ip link set lo up
sudo ip netns exec "$CLIENT_NS" ip address add 10.250.0.2/24 dev "$CLIENT_VETH"
sudo ip netns exec "$SERVER_NS" ip address add 10.250.0.1/24 dev "$SERVER_VETH"
sudo ip netns exec "$SERVER_NS" ip address add 10.77.0.1/32 dev lo
sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" up
sudo ip netns exec "$SERVER_NS" ip link set "$SERVER_VETH" up
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"

sudo ip netns exec "$SERVER_NS" openssl s_server -quiet -accept 127.0.0.1:8443 -cert "$CERT" -key "$KEY" -www 2>&1 | tee "$TARGET_LOG" >/dev/null &
wait_listen "$SERVER_NS" 8443 120
sudo ip netns exec "$SERVER_NS" python3 -m http.server 8080 --bind 10.77.0.1 --directory "$WWW" 2>&1 | tee "$HTTP_LOG" >/dev/null &
wait_listen "$SERVER_NS" 8080 120
start_reference

sudo ip netns exec "$CLIENT_NS" "$VPN_BIN" --config "$CLIENT_CONFIG" 2>&1 | tee "$CLIENT_LOG" >/dev/null &
CLIENT_LAUNCHER=$!
readonly CLIENT_LAUNCHER
wait_for_file "${STATE_DIR}/state.json" 200
wait_for_ns_interface "$CLIENT_NS" "$CLIENT_IFACE" 200
before_ifindex="$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")"
[[ "$before_ifindex" =~ ^[0-9]+$ ]]

# REALITY is demand-dialed: the first successful flow is what proves transport
# establishment and permits the client to publish state=online.
wait_http 120
wait_state online 80
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]]

# No namespace has a default route, so the only possible transport path is the
# private veth to the pinned reference server.
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
if sudo ip netns exec "$CLIENT_NS" ip route get 1.1.1.1 >/dev/null 2>&1; then
    echo "unsafe client namespace unexpectedly has public IPv4 reachability" >&2
    exit 1
fi

# Restart only the reference Xray server. A failed new flow must become
# reconnecting; restoring the server must recover without replacing the TUN.
stop_reference
for _ in {1..3}; do
    sudo ip netns exec "$CLIENT_NS" curl -fsS --connect-timeout 1 --max-time 2 --interface "$CLIENT_IFACE" http://10.77.0.1:8080/ >/dev/null 2>&1 || true
done
wait_state reconnecting 100
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]]
start_reference
wait_http 160
wait_state online 100
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]] || {
    echo "Kikimora TUN was recreated across Xray peer restart" >&2
    exit 1
}

# Underlay disappearance is also transport-local. Traffic fails closed and the
# same TUN returns online once the private link is restored.
sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" down
for _ in {1..3}; do
    sudo ip netns exec "$CLIENT_NS" curl -fsS --connect-timeout 1 --max-time 2 --interface "$CLIENT_IFACE" http://10.77.0.1:8080/ >/dev/null 2>&1 || true
done
wait_state reconnecting 100
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]]
sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" up
wait_http 160
wait_state online 100
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]] || {
    echo "Kikimora TUN was recreated across VLESS underlay loss" >&2
    exit 1
}

jq -e --arg iface "$CLIENT_IFACE" --argjson idx "$before_ifindex" '
    .schema == 1 and
    .protocol == "vless-reality" and
    .state == "online" and
    .route_ready == true and
    .interface.name == $iface and
    .interface.ifindex == $idx and
    .session.connected == true and
    .counters.reconnects >= 2
' "${STATE_DIR}/state.json" >/dev/null

assert_host_interface_absent "$CLIENT_IFACE"
kill_ns_processes "$CLIENT_NS"
wait "$CLIENT_LAUNCHER" 2>/dev/null || true

echo "isolated VLESS/REALITY reference interop: OK"
