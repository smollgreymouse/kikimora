#!/usr/bin/env bash
set -Eeuo pipefail

[[ $# -eq 1 ]] || { echo "usage: $0 /path/to/kikimora-vpn" >&2; exit 2; }
VPN_BIN="$(readlink -f -- "$1")"
readonly VPN_BIN
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck source=linux/tests/vpn-client/netns-lib.sh
source "${SCRIPT_DIR}/netns-lib.sh"

readonly NS="kkvpn-soak-${RANDOM}-$$"
readonly IFACE_A="kk-soak0"
readonly IFACE_B="kk-soak1"
WORK="$(mktemp -d)"
readonly WORK
readonly CONFIG_A="${WORK}/a.toml"
readonly CONFIG_B="${WORK}/b.toml"
readonly STATE_A="${WORK}/state-a"
readonly STATE_B="${WORK}/state-b"
readonly LOG_A="${WORK}/a.log"
readonly LOG_B="${WORK}/b.log"

cleanup() {
    local status=$?
    remove_netns "$NS"
    assert_host_interface_absent "$IFACE_A" || status=$?
    assert_host_interface_absent "$IFACE_B" || status=$?
    if ((status != 0)); then
        printf '\n--- A state/log ---\n' >&2
        cat "${STATE_A}/state.json" >&2 2>/dev/null || true
        cat "$LOG_A" >&2 2>/dev/null || true
        printf '\n--- B state/log ---\n' >&2
        cat "${STATE_B}/state.json" >&2 2>/dev/null || true
        cat "$LOG_B" >&2 2>/dev/null || true
    fi
    sudo rm -rf -- "$WORK"
    exit "$status"
}
trap cleanup EXIT

wait_reconnects() {
    local state_file="$1" minimum="$2" attempts="${3:-200}" i
    for ((i=0; i<attempts; i++)); do
        if [[ -s "$state_file" ]] && jq -e --argjson n "$minimum" '.counters.reconnects >= $n' "$state_file" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.05
    done
    printf 'timed out waiting for reconnects >= %s in %s\n' "$minimum" "$state_file" >&2
    return 1
}

kill_config_process() {
    local config="$1" pid cmdline
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        cmdline="$(tr '\0' ' ' <"/proc/${pid}/cmdline" 2>/dev/null || true)"
        if [[ "$cmdline" == *"$config"* ]]; then
            sudo kill -KILL "$pid"
            return 0
        fi
    done < <(ns_pids "$NS")
    printf 'could not find process for config %s\n' "$config" >&2
    return 1
}

for pair in \
    "a:$IFACE_A:$STATE_A:$CONFIG_A" \
    "b:$IFACE_B:$STATE_B:$CONFIG_B"; do
    IFS=: read -r name iface state config <<<"$pair"
    cat >"$config" <<EOF
name = "soak-$name"
protocol = "stub"
interface = "$iface"
address = ["10.77.${name/a/10}.2/32"]
mtu = 1380
state_dir = "$state"
queue_packets = 8
queue_bytes = 8192

[stub]
mode = "reconnect-loop"
reconnect_after_ms = 20
EOF
    if [[ "$name" == "b" ]]; then
        sed -i 's/10\.77\.b\.2/10.77.11.2/' "$config"
    fi
    "$VPN_BIN" --config "$config" --check
done

assert_host_interface_absent "$IFACE_A"
assert_host_interface_absent "$IFACE_B"
sudo ip netns add "$NS"
sudo ip netns exec "$NS" ip link set lo up
assert_no_default_route "$NS"

sudo ip netns exec "$NS" "$VPN_BIN" --config "$CONFIG_A" 2>&1 | tee "$LOG_A" >/dev/null &
sudo ip netns exec "$NS" "$VPN_BIN" --config "$CONFIG_B" 2>&1 | tee "$LOG_B" >/dev/null &

wait_for_file "${STATE_A}/state.json" 200
wait_for_file "${STATE_B}/state.json" 200
wait_for_ns_interface "$NS" "$IFACE_A" 200
wait_for_ns_interface "$NS" "$IFACE_B" 200

idx_a="$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE_A}/ifindex")"
idx_b="$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE_B}/ifindex")"
[[ "$idx_a" =~ ^[0-9]+$ && "$idx_b" =~ ^[0-9]+$ && "$idx_a" != "$idx_b" ]]

wait_reconnects "${STATE_A}/state.json" 25
wait_reconnects "${STATE_B}/state.json" 25

[[ "$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE_A}/ifindex")" == "$idx_a" ]]
[[ "$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE_B}/ifindex")" == "$idx_b" ]]
assert_no_default_route "$NS"
assert_host_interface_absent "$IFACE_A"
assert_host_interface_absent "$IFACE_B"

jq -e --argjson idx "$idx_a" '.route_ready == true and .interface.ifindex == $idx and .counters.reconnects >= 25' "${STATE_A}/state.json" >/dev/null
jq -e --argjson idx "$idx_b" '.route_ready == true and .interface.ifindex == $idx and .counters.reconnects >= 25' "${STATE_B}/state.json" >/dev/null

kill_config_process "$CONFIG_A"
for _ in {1..100}; do
    if ! sudo ip netns exec "$NS" ip link show dev "$IFACE_A" >/dev/null 2>&1; then
        break
    fi
    sleep 0.05
done
if sudo ip netns exec "$NS" ip link show dev "$IFACE_A" >/dev/null 2>&1; then
    echo "crashed instance left its TUN behind" >&2
    exit 1
fi
sudo ip netns exec "$NS" ip link show dev "$IFACE_B" >/dev/null
[[ "$(sudo ip netns exec "$NS" cat "/sys/class/net/${IFACE_B}/ifindex")" == "$idx_b" ]]

kill_ns_processes "$NS"
for _ in {1..100}; do
    if ! sudo ip netns exec "$NS" ip link show dev "$IFACE_B" >/dev/null 2>&1; then
        break
    fi
    sleep 0.05
done
if sudo ip netns exec "$NS" ip link show dev "$IFACE_B" >/dev/null 2>&1; then
    echo "second instance left its TUN behind after shutdown" >&2
    exit 1
fi

echo "multi-instance reconnect/crash soak: OK"
