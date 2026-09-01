#!/usr/bin/env bash
set -Eeuo pipefail

[[ $# -eq 1 ]] || { echo "usage: $0 /path/to/kikimora-vpn" >&2; exit 2; }
VPN_BIN="$(readlink -f -- "$1")"
readonly VPN_BIN
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck source=linux/tests/vpn-client/netns-lib.sh
source "${SCRIPT_DIR}/netns-lib.sh"

readonly NS="kkvpn-ci-${RANDOM}-$$"
readonly IFACE="kk-ci0"
WORK="$(mktemp -d)"
readonly WORK
readonly STATE_DIR="${WORK}/state"
readonly CONFIG="${WORK}/client.toml"
readonly LOG="${WORK}/client.log"

cleanup() {
    local status=$?
    remove_netns "$NS"
    assert_host_interface_absent "$IFACE" || status=$?
    if ((status != 0)); then
        printf '\n--- client log ---\n' >&2
        cat "$LOG" >&2 2>/dev/null || true
    fi
    rm -rf -- "$WORK"
    exit "$status"
}
trap cleanup EXIT

[[ -x "$VPN_BIN" ]] || { echo "binary not executable: $VPN_BIN" >&2; exit 1; }
[[ -c /dev/net/tun ]] || { echo "/dev/net/tun is unavailable" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

assert_host_interface_absent "$IFACE"

cat >"$CONFIG" <<EOF
name = "ci-stub"
protocol = "stub"
interface = "$IFACE"
address = ["10.77.0.2/32"]
mtu = 1380
state_dir = "$STATE_DIR"
queue_packets = 32
queue_bytes = 65536

[stub]
mode = "reconnect-once"
reconnect_after_ms = 150
EOF

"$VPN_BIN" --config "$CONFIG" --check

sudo ip netns add "$NS"
sudo ip netns exec "$NS" ip link set lo up
assert_no_default_route "$NS"

# There is deliberately no veth and no default route in this smoke test. The
# process can create/configure its TUN but cannot reach any external network.
sudo ip netns exec "$NS" "$VPN_BIN" --config "$CONFIG" 2>&1 | tee "$LOG" >/dev/null &
LAUNCHER_PID=$!
readonly LAUNCHER_PID

wait_for_file "${STATE_DIR}/state.json"
wait_for_ns_interface "$NS" "$IFACE"
assert_host_interface_absent "$IFACE"
assert_no_default_route "$NS"

if sudo ip netns exec "$NS" ip route get 1.1.1.1 >/dev/null 2>&1; then
    echo "unsafe test namespace unexpectedly has public IPv4 reachability" >&2
    exit 1
fi

before_ifindex="$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE}/ifindex")"
[[ "$before_ifindex" =~ ^[0-9]+$ ]]

# Wait through the deterministic reconnect-once cycle.
sleep 0.6

after_ifindex="$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE}/ifindex")"
[[ "$after_ifindex" == "$before_ifindex" ]] || {
    printf 'TUN was recreated across reconnect: before=%s after=%s\n' "$before_ifindex" "$after_ifindex" >&2
    exit 1
}

jq -e --arg iface "$IFACE" --argjson idx "$before_ifindex" '
    .schema == 1 and
    .name == "ci-stub" and
    .protocol == "stub" and
    .route_ready == true and
    .interface.name == $iface and
    .interface.ifindex == $idx and
    .counters.reconnects >= 1
' "${STATE_DIR}/state.json" >/dev/null

assert_host_interface_absent "$IFACE"

# Stop only processes living in the disposable namespace. Closing the two TUN
# fd halves must remove the interface without an explicit `ip link del`.
kill_ns_processes "$NS"
wait "$LAUNCHER_PID" 2>/dev/null || true

for _ in {1..50}; do
    if ! sudo ip netns exec "$NS" ip link show dev "$IFACE" >/dev/null 2>&1; then
        break
    fi
    sleep 0.05
done
if sudo ip netns exec "$NS" ip link show dev "$IFACE" >/dev/null 2>&1; then
    echo "TUN remained after client process exit" >&2
    exit 1
fi

assert_host_interface_absent "$IFACE"
echo "isolated TUN smoke test: OK"
