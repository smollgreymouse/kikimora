#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID} -eq 0 ]] || {
  printf 'SKIP: endpoint-underlay-netns requires root\n'
  exit 0
}

readonly ROUTE_WATCH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/route-watch}"
readonly NS="kikimora-endpoint-$$"
tmp="$(mktemp -d)"
cleanup() {
  ip netns del "$NS" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

mkdir -p "$tmp/bin" "$tmp/runtime" "$tmp/lifecycle" "$tmp/watch" "$tmp/endpoint-state" "$tmp/endpoints"
touch "$tmp/lifecycle/devices"

cat > "$tmp/vpn.conf" <<'EOF_CONFIG'
PRIMARY_INTERFACE="amn0"
SECONDARY_INTERFACE="vpn0"
EOF_CONFIG
printf '# exact endpoint IP for the integration test\n' > "$tmp/endpoints/primary.txt"
printf '46.243.227.103\n' > "$tmp/endpoints/secondary.txt"

cat > "$tmp/bin/reconcile" <<'EOF_RECONCILE'
#!/usr/bin/env bash
exit 0
EOF_RECONCILE
cat > "$tmp/bin/lifecycle" <<'EOF_LIFECYCLE'
#!/usr/bin/env bash
exit 0
EOF_LIFECYCLE
chmod +x "$tmp/bin/reconcile" "$tmp/bin/lifecycle"

ip netns add "$NS"
ip -n "$NS" link add wlan0 type dummy
ip -n "$NS" link add amn0 type dummy
ip -n "$NS" link add vpn0 type dummy
ip -n "$NS" link set lo up
ip -n "$NS" link set wlan0 up
ip -n "$NS" link set amn0 up
ip -n "$NS" link set vpn0 up
ip -n "$NS" addr add 192.0.2.2/24 dev wlan0
ip -n "$NS" addr add 10.8.1.1/32 dev amn0
ip -n "$NS" addr add 10.0.0.81/24 dev vpn0

# Reproduce the problematic shape: VPN defaults beat Wi-Fi, and Leshy/main has
# an explicit route for the secondary VPN endpoint through vpn0.
ip -n "$NS" route add default via 192.0.2.1 dev wlan0 onlink metric 600
ip -n "$NS" route add default dev amn0 metric 100
ip -n "$NS" route add default dev vpn0 metric 50
ip -n "$NS" route add 46.243.227.103/32 dev vpn0 proto static

ip netns exec "$NS" env \
  PATH="/usr/sbin:/usr/bin:/sbin:/bin" \
  KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime" \
  KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/lifecycle" \
  KIKIMORA_ROUTE_WATCH_STATE_DIR="$tmp/watch" \
  KIKIMORA_ENDPOINT_STATE_DIR="$tmp/endpoint-state" \
  KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints" \
  KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
  KIKIMORA_ROUTE_LIFECYCLE="$tmp/bin/lifecycle" \
  KIKIMORA_RECONCILE="$tmp/bin/reconcile" \
  KIKIMORA_ROUTE_WATCH_MAX_ITERATIONS=1 \
  KIKIMORA_ENDPOINT_RECONCILE_ITERATIONS=1 \
  KIKIMORA_ENDPOINT_ROUTE_TABLE=51890 \
  KIKIMORA_ENDPOINT_RULE_PRIORITY=50 \
  "$ROUTE_WATCH" >/dev/null

endpoint_route="$(ip -n "$NS" route get 46.243.227.103)"
normal_route="$(ip -n "$NS" route get 198.51.100.10)"

[[ "$endpoint_route" == *'via 192.0.2.1 dev wlan0'* ]] || {
  printf 'FAIL: endpoint did not use physical underlay: %s\n' "$endpoint_route" >&2
  ip -n "$NS" rule show >&2
  ip -n "$NS" route show table 51890 >&2
  exit 1
}
[[ "$normal_route" == *'dev vpn0'* ]] || {
  printf 'FAIL: ordinary routing was unexpectedly changed: %s\n' "$normal_route" >&2
  exit 1
}
ip -n "$NS" rule show | grep -Eq '^50:.*to 46\.243\.227\.103.*lookup 51890$' || {
  printf 'FAIL: exact endpoint rule missing\n' >&2
  ip -n "$NS" rule show >&2
  exit 1
}

printf 'VPN endpoint underlay netns integration: OK\n'
