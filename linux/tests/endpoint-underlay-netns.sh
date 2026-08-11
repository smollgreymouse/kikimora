#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID} -eq 0 ]] || {
  printf 'SKIP: endpoint-underlay-netns requires root\n'
  exit 0
}

readonly ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROUTE_WATCH="${1:-${ROOT}/linux/files/route-watch}"
readonly RECONCILE="${2:-${ROOT}/linux/files/reconcile}"
readonly NS="kikimora-endpoint-$$"
tmp="$(mktemp -d)"
cleanup() {
  ip netns del "$NS" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

mkdir -p "$tmp/runtime" "$tmp/state" "$tmp/endpoints"
cat > "$tmp/vpn.conf" <<EOF_CONFIG
PRIMARY_INTERFACE="amn0"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
SECONDARY_INTERFACE="vpn0"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
VPN_LINK_READY_SUCCESSES=3
EOF_CONFIG
printf '203.0.113.10\n' > "$tmp/endpoints/primary.txt"
printf '46.243.227.103\n' > "$tmp/endpoints/secondary.txt"

ip netns add "$NS"
ip -n "$NS" link add wlan0 type dummy
ip -n "$NS" link add amn0 type dummy
ip -n "$NS" link add vpn0 type dummy
ip -n "$NS" link set lo up
ip -n "$NS" link set wlan0 up
ip -n "$NS" addr add 192.0.2.2/24 dev wlan0
ip -n "$NS" addr add 10.8.1.1/32 dev amn0
ip -n "$NS" addr add 10.0.0.81/24 dev vpn0
ip -n "$NS" route add default via 192.0.2.1 dev wlan0 onlink metric 600

run_endpoint() {
  ip netns exec "$NS" env \
    PATH="/usr/sbin:/usr/bin:/sbin:/bin" \
    KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints" \
    KIKIMORA_ENDPOINT_STATE_DIR="$tmp/state" \
    KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
    KIKIMORA_ENDPOINT_ROUTE_TABLE=51890 \
    KIKIMORA_PRIMARY_ENDPOINT_RULE_PRIORITY=50 \
    KIKIMORA_SECONDARY_ENDPOINT_RULE_PRIORITY=51 \
    "$ROUTE_WATCH" endpoint "$@"
}
run_reconcile() {
  ip netns exec "$NS" env \
    PATH="/usr/sbin:/usr/bin:/sbin:/bin" \
    KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
    KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime" \
    KIKIMORA_ENDPOINT_STATE_DIR="$tmp/state" \
    "$RECONCILE"
}

# Correct startup: policy exists before either VPN comes up.
run_endpoint apply >/dev/null
ip -n "$NS" link set amn0 up
ip -n "$NS" link set vpn0 up
ip -n "$NS" route add default dev amn0 metric 100
ip -n "$NS" route add default dev vpn0 metric 50
ip -n "$NS" route add 46.243.227.103/32 dev amn0 proto static

endpoint_route="$(ip -n "$NS" route get 46.243.227.103)"
[[ "$endpoint_route" == *'via 192.0.2.1 dev wlan0'* ]] || {
  printf 'FAIL: protected endpoint did not use physical underlay: %s\n' "$endpoint_route" >&2
  exit 1
}
ip -n "$NS" rule show | grep -Eq '^51:.*to 46\.243\.227\.103.*lookup 51890$' || {
  printf 'FAIL: secondary exact endpoint rule missing\n' >&2
  exit 1
}

# Normal stop must refuse to remove policy below a live managed VPN.
if run_endpoint check-clear >/dev/null 2>&1; then
  printf 'FAIL: clear preflight accepted live VPN interfaces\n' >&2
  exit 1
fi
run_endpoint clear --force >/dev/null
if ip -n "$NS" rule show | grep -q 'lookup 51890'; then
  printf 'FAIL: forced clear left endpoint rules behind\n' >&2
  exit 1
fi

# Unsafe first adoption: secondary is already UP and its endpoint currently
# routes through primary. Applying Kikimora must not rewrite that live path.
ip -n "$NS" route replace 46.243.227.103/32 dev amn0 proto static
run_endpoint apply >/dev/null
[[ -e "$tmp/state/secondary.pending" ]] || {
  printf 'FAIL: live conflicting secondary endpoint was not marked pending\n' >&2
  exit 1
}
if ip -n "$NS" rule show | grep -Eq '^51:.*to 46\.243\.227\.103.*lookup 51890$'; then
  printf 'FAIL: pending secondary path was changed under a live VPN\n' >&2
  exit 1
fi

# Pending means not ready even though vpn0 itself is UP+IPv4.
run_reconcile >/dev/null
[[ ! -e "$tmp/runtime/secondary.dev" ]] || {
  printf 'FAIL: secondary.dev published while endpoint underlay was pending\n' >&2
  exit 1
}

# One disconnect gives the watcher a safe transition point. The next connect
# starts with DIRECT endpoint policy already present and normal readiness resumes.
ip -n "$NS" link set vpn0 down
run_endpoint reconcile >/dev/null
[[ ! -e "$tmp/state/secondary.pending" ]] || {
  printf 'FAIL: pending state survived safe VPN-down reconcile\n' >&2
  exit 1
}
ip -n "$NS" rule show | grep -Eq '^51:.*to 46\.243\.227\.103.*lookup 51890$' || {
  printf 'FAIL: secondary direct rule not installed while VPN was down\n' >&2
  exit 1
}
ip -n "$NS" link set vpn0 up
run_reconcile >/dev/null
run_reconcile >/dev/null
run_reconcile >/dev/null
[[ "$(cat "$tmp/runtime/secondary.dev")" == vpn0 ]] || {
  printf 'FAIL: secondary readiness did not recover after safe reconnect\n' >&2
  exit 1
}

printf 'VPN endpoint underlay lifecycle integration: OK\n'
