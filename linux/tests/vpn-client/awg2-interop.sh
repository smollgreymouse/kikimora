#!/usr/bin/env bash
set -Eeuo pipefail

[[ $# -eq 3 ]] || { echo "usage: $0 /path/to/kikimora-vpn /path/to/amneziawg-go /path/to/awg" >&2; exit 2; }
VPN_BIN="$(readlink -f -- "$1")"
AWG_GO="$(readlink -f -- "$2")"
AWG_BIN="$(readlink -f -- "$3")"
readonly VPN_BIN AWG_GO AWG_BIN
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck source=linux/tests/vpn-client/netns-lib.sh
source "${SCRIPT_DIR}/netns-lib.sh"

readonly CLIENT_NS="kkawg-c-${RANDOM}-$$"
readonly SERVER_NS="kkawg-s-${RANDOM}-$$"
readonly CLIENT_VETH="kkawg-cv"
readonly SERVER_VETH="kkawg-sv"
readonly CLIENT_IFACE="kk-awg0"
readonly SERVER_IFACE="kk-awg-ref0"
readonly CONTROL_SOCKET="/var/run/amneziawg/${SERVER_IFACE}.sock"
WORK="$(mktemp -d)"
readonly WORK
readonly STATE_DIR="${WORK}/state"
readonly CONFIG="${WORK}/client.toml"
readonly CLIENT_LOG="${WORK}/client.log"
readonly SERVER_LOG="${WORK}/server.log"
readonly CLIENT_KEY="${WORK}/client.key"
readonly SERVER_KEY="${WORK}/server.key"

cleanup() {
    local status=$?
    remove_netns "$CLIENT_NS"
    remove_netns "$SERVER_NS"
    sudo rm -f -- "$CONTROL_SOCKET" 2>/dev/null || true
    assert_host_interface_absent "$CLIENT_IFACE" || status=$?
    assert_host_interface_absent "$SERVER_IFACE" || status=$?
    if ((status != 0)); then
        printf '\n--- client state ---\n' >&2
        cat "${STATE_DIR}/state.json" >&2 2>/dev/null || true
        printf '\n--- client log ---\n' >&2
        cat "$CLIENT_LOG" >&2 2>/dev/null || true
        printf '\n--- reference server log ---\n' >&2
        cat "$SERVER_LOG" >&2 2>/dev/null || true
        printf '\n--- client routes ---\n' >&2
        sudo ip netns exec "$CLIENT_NS" ip route show >&2 2>/dev/null || true
        printf '\n--- server routes ---\n' >&2
        sudo ip netns exec "$SERVER_NS" ip route show >&2 2>/dev/null || true
    fi
    sudo rm -rf -- "$WORK"
    exit "$status"
}
trap cleanup EXIT

for binary in "$VPN_BIN" "$AWG_GO" "$AWG_BIN"; do
    [[ -x "$binary" ]] || { echo "binary not executable: $binary" >&2; exit 1; }
done
[[ -c /dev/net/tun ]] || { echo "/dev/net/tun is unavailable" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
command -v ping >/dev/null || { echo "ping is required" >&2; exit 1; }

wait_state() {
    local expected="$1" attempts="${2:-160}" i
    for ((i=0; i<attempts; i++)); do
        if [[ -s "${STATE_DIR}/state.json" ]] && jq -e --arg state "$expected" '.state == $state' "${STATE_DIR}/state.json" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.25
    done
    printf 'timed out waiting for client state=%s\n' "$expected" >&2
    return 1
}

wait_ping() {
    local attempts="${1:-120}" i
    for ((i=0; i<attempts; i++)); do
        if sudo ip netns exec "$CLIENT_NS" ping -n -c 1 -W 1 10.77.0.1 >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.25
    done
    echo "timed out waiting for AWG2 tunnel traffic" >&2
    return 1
}

stop_reference() {
    local pid
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        sudo kill -TERM "$pid" 2>/dev/null || true
    done < <(ns_pids "$SERVER_NS")
    for _ in {1..80}; do
        [[ -z "$(ns_pids "$SERVER_NS")" ]] && break
        sleep 0.05
    done
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        sudo kill -KILL "$pid" 2>/dev/null || true
    done < <(ns_pids "$SERVER_NS")
    sudo rm -f -- "$CONTROL_SOCKET" 2>/dev/null || true
}

start_reference() {
    sudo rm -f -- "$CONTROL_SOCKET" 2>/dev/null || true
    sudo ip netns exec "$SERVER_NS" "$AWG_GO" -f "$SERVER_IFACE" >>"$SERVER_LOG" 2>&1 &
    wait_for_ns_interface "$SERVER_NS" "$SERVER_IFACE" 200
    sudo ip netns exec "$SERVER_NS" ip address add 10.77.0.1/24 dev "$SERVER_IFACE"
    sudo ip netns exec "$SERVER_NS" ip link set dev "$SERVER_IFACE" mtu 1380 up
    sudo ip netns exec "$SERVER_NS" "$AWG_BIN" set "$SERVER_IFACE" \
        private-key "$SERVER_KEY" listen-port 51820 \
        s1 15 s2 15 s3 15 s4 15 \
        h1 1001 h2 1002 h3 1003 h4 1004 \
        peer "$CLIENT_PUBLIC" allowed-ips 10.77.0.2/32
}

assert_host_interface_absent "$CLIENT_IFACE"
assert_host_interface_absent "$SERVER_IFACE"

"$AWG_BIN" genkey >"$CLIENT_KEY"
"$AWG_BIN" genkey >"$SERVER_KEY"
chmod 600 "$CLIENT_KEY" "$SERVER_KEY"
CLIENT_PRIVATE="$(cat "$CLIENT_KEY")"
CLIENT_PUBLIC="$("$AWG_BIN" pubkey <"$CLIENT_KEY")"
SERVER_PUBLIC="$("$AWG_BIN" pubkey <"$SERVER_KEY")"
readonly CLIENT_PRIVATE CLIENT_PUBLIC SERVER_PUBLIC

cat >"$CONFIG" <<EOF
name = "ci-awg2"
protocol = "amneziawg2"
interface = "$CLIENT_IFACE"
address = ["10.77.0.2/24"]
mtu = 1380
state_dir = "$STATE_DIR"
queue_packets = 128
queue_bytes = 1048576

[awg2]
private_key = "$CLIENT_PRIVATE"
peer_public_key = "$SERVER_PUBLIC"
endpoint = "10.250.0.1:51820"
allowed_ips = ["10.77.0.0/24"]
persistent_keepalive = 1
jc = 4
jmin = 40
jmax = 80
s1 = 15
s2 = 15
s3 = 15
s4 = 15
h1 = "1001"
h2 = "1002"
h3 = "1003"
h4 = "1004"
i1 = "<r 8>"
EOF

"$VPN_BIN" --config "$CONFIG" --check

sudo ip netns add "$CLIENT_NS"
sudo ip netns add "$SERVER_NS"
sudo ip link add "$CLIENT_VETH" type veth peer name "$SERVER_VETH"
sudo ip link set "$CLIENT_VETH" netns "$CLIENT_NS"
sudo ip link set "$SERVER_VETH" netns "$SERVER_NS"
sudo ip netns exec "$CLIENT_NS" ip link set lo up
sudo ip netns exec "$SERVER_NS" ip link set lo up
sudo ip netns exec "$CLIENT_NS" ip address add 10.250.0.2/24 dev "$CLIENT_VETH"
sudo ip netns exec "$SERVER_NS" ip address add 10.250.0.1/24 dev "$SERVER_VETH"
# Deliberately leave the underlay down for the first connection attempt.
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"

sudo ip netns exec "$CLIENT_NS" "$VPN_BIN" --config "$CONFIG" 2>&1 | tee "$CLIENT_LOG" >/dev/null &
CLIENT_LAUNCHER=$!
readonly CLIENT_LAUNCHER
wait_for_file "${STATE_DIR}/state.json" 200
wait_for_ns_interface "$CLIENT_NS" "$CLIENT_IFACE" 200
before_ifindex="$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")"
[[ "$before_ifindex" =~ ^[0-9]+$ ]]

# Initial endpoint loss is recoverable and must not tear down the TUN.
wait_state reconnecting 100
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]]

sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" up
sudo ip netns exec "$SERVER_NS" ip link set "$SERVER_VETH" up
start_reference
wait_ping 160
wait_state online 80

# The only transport path is the private veth. There is no default route or NAT.
assert_no_default_route "$CLIENT_NS"
assert_no_default_route "$SERVER_NS"
if sudo ip netns exec "$CLIENT_NS" ip route get 1.1.1.1 >/dev/null 2>&1; then
    echo "unsafe client namespace unexpectedly has public IPv4 reachability" >&2
    exit 1
fi
if sudo ip netns exec "$SERVER_NS" ip route get 1.1.1.1 >/dev/null 2>&1; then
    echo "unsafe server namespace unexpectedly has public IPv4 reachability" >&2
    exit 1
fi

veth_tx_before="$(sudo ip -n "$CLIENT_NS" -j -s link show dev "$CLIENT_VETH" | jq '.[0].stats64.tx.bytes // .[0].stats.tx.bytes')"
sudo ip netns exec "$CLIENT_NS" ping -n -c 3 -W 1 10.77.0.1 >/dev/null
veth_tx_after="$(sudo ip -n "$CLIENT_NS" -j -s link show dev "$CLIENT_VETH" | jq '.[0].stats64.tx.bytes // .[0].stats.tx.bytes')"
((veth_tx_after > veth_tx_before)) || { echo "private veth counters did not advance" >&2; exit 1; }

# Reference peer restart: same static identity, fresh userspace session state.
stop_reference
if sudo ip netns exec "$CLIENT_NS" ping -n -c 1 -W 1 10.77.0.1 >/dev/null 2>&1; then
    echo "tunnel unexpectedly remained usable with reference peer stopped" >&2
    exit 1
fi
start_reference
wait_ping 200
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]] || {
    echo "Kikimora TUN was recreated across AWG2 peer restart" >&2
    exit 1
}

# Underlay loss after an established session: traffic fails closed, interface survives,
# and restoring only the private veth is sufficient for protocol recovery.
sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" down
sleep 2
if sudo ip netns exec "$CLIENT_NS" ping -n -c 1 -W 1 10.77.0.1 >/dev/null 2>&1; then
    echo "tunnel unexpectedly passed traffic with underlay down" >&2
    exit 1
fi
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]]
sudo ip netns exec "$CLIENT_NS" ip link set "$CLIENT_VETH" up
wait_ping 200
wait_state online 80
[[ "$(sudo ip netns exec "$CLIENT_NS" cat "/sys/class/net/${CLIENT_IFACE}/ifindex")" == "$before_ifindex" ]] || {
    echo "Kikimora TUN was recreated across AWG2 underlay loss" >&2
    exit 1
}

jq -e --arg iface "$CLIENT_IFACE" --argjson idx "$before_ifindex" '
    .schema == 1 and
    .protocol == "amneziawg2" and
    .state == "online" and
    .route_ready == true and
    .interface.name == $iface and
    .interface.ifindex == $idx and
    .session.connected == true
' "${STATE_DIR}/state.json" >/dev/null

assert_host_interface_absent "$CLIENT_IFACE"
assert_host_interface_absent "$SERVER_IFACE"
stop_reference
kill_ns_processes "$CLIENT_NS"
wait "$CLIENT_LAUNCHER" 2>/dev/null || true

echo "isolated AWG2 reference interop: OK"
