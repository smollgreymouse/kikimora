#!/usr/bin/env bash
set -Eeuo pipefail

readonly ROUTE_WATCH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/route-watch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p \
  "$tmp/bin" \
  "$tmp/runtime" \
  "$tmp/lifecycle" \
  "$tmp/watch" \
  "$tmp/endpoint-state" \
  "$tmp/endpoints"
touch "$tmp/lifecycle/devices"

cat > "$tmp/vpn.conf" <<'EOF_CONFIG'
PRIMARY_INTERFACE="amn0"
PRIMARY_DEVICE_FILE="/tmp/unused-primary.dev"
SECONDARY_INTERFACE="vpn0"
SECONDARY_DEVICE_FILE="/tmp/unused-secondary.dev"
EOF_CONFIG

cat > "$tmp/endpoints/primary.txt" <<'EOF_PRIMARY'
primary-vpn.example
EOF_PRIMARY
cat > "$tmp/endpoints/secondary.txt" <<'EOF_SECONDARY'
# This endpoint is intentionally a child of an explicit secondary domain.
ve.ad-tech.ru
EOF_SECONDARY

cat > "$tmp/bin/reconcile" <<'EOF_RECONCILE'
#!/usr/bin/env bash
exit 0
EOF_RECONCILE
chmod +x "$tmp/bin/reconcile"

cat > "$tmp/bin/lifecycle" <<'EOF_LIFECYCLE'
#!/usr/bin/env bash
exit 0
EOF_LIFECYCLE
chmod +x "$tmp/bin/lifecycle"

cat > "$tmp/bin/resolvectl" <<'EOF_RESOLVECTL'
#!/usr/bin/env bash
exit 0
EOF_RESOLVECTL
chmod +x "$tmp/bin/resolvectl"

cat > "$tmp/bin/getent" <<'EOF_GETENT'
#!/usr/bin/env bash
set -Eeuo pipefail

case "$*" in
  'ahostsv4 primary-vpn.example')
    printf '203.0.113.10 STREAM primary-vpn.example\n'
    ;;
  'ahostsv6 primary-vpn.example')
    printf '2001:db8::10 STREAM primary-vpn.example\n'
    ;;
  'ahostsv4 ve.ad-tech.ru')
    printf '46.243.227.103 STREAM ve.ad-tech.ru\n'
    ;;
  'ahostsv6 ve.ad-tech.ru')
    exit 2
    ;;
  *)
    exit 2
    ;;
esac
EOF_GETENT
chmod +x "$tmp/bin/getent"

cat > "$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail

printf '%s\n' "$*" >> "${MOCK_IP_LOG}"

case "$*" in
  '-4 route show table main default')
    cat <<'EOF_ROUTES4'
default proto static scope link dev vpn0 metric 50
default dev amn0 scope link metric 100
default via 192.0.2.1 dev wlp0s20f3 proto dhcp metric 600
EOF_ROUTES4
    ;;
  '-6 route show table main default')
    cat <<'EOF_ROUTES6'
default dev amn0 metric 100
default via fe80::1 dev wlp0s20f3 proto ra metric 600
EOF_ROUTES6
    ;;
  '-4 rule del priority 50 lookup 51890'|'-6 rule del priority 50 lookup 51890')
    exit 2
    ;;
  *)
    exit 0
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

export PATH="$tmp/bin:$PATH"
export MOCK_IP_LOG="$tmp/ip.log"
export KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime"
export KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/lifecycle"
export KIKIMORA_ROUTE_WATCH_STATE_DIR="$tmp/watch"
export KIKIMORA_ENDPOINT_STATE_DIR="$tmp/endpoint-state"
export KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_ROUTE_LIFECYCLE="$tmp/bin/lifecycle"
export KIKIMORA_RECONCILE="$tmp/bin/reconcile"
export KIKIMORA_ROUTE_WATCH_MAX_ITERATIONS=1
export KIKIMORA_ENDPOINT_RECONCILE_ITERATIONS=1
export KIKIMORA_ENDPOINT_ROUTE_TABLE=51890
export KIKIMORA_ENDPOINT_RULE_PRIORITY=50

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  printf '%s\n' '--- ip log ---' >&2
  cat "$tmp/ip.log" >&2 2>/dev/null || true
  exit 1
}

assert_log() {
  local expected="$1"
  grep -Fqx -- "$expected" "$tmp/ip.log" || fail "missing ip invocation: $expected"
}

"$ROUTE_WATCH" >/dev/null 2>"$tmp/route-watch.log"

# Both VPN interfaces have better metrics, but endpoint policy must explicitly
# skip them and use the physical Wi-Fi default route.
assert_log '-4 route replace 46.243.227.103/32 via 192.0.2.1 dev wlp0s20f3 onlink table 51890 metric 1'
assert_log '-4 route replace 203.0.113.10/32 via 192.0.2.1 dev wlp0s20f3 onlink table 51890 metric 1'
assert_log '-6 route replace 2001:db8::10/128 via fe80::1 dev wlp0s20f3 onlink table 51890 metric 1'

# Exact endpoint IP rules have higher priority than the normal main-table rule.
# Therefore ve.ad-tech.ru stays DIRECT even when ad-tech.ru is routed to vpn0.
assert_log '-4 rule add priority 50 to 46.243.227.103/32 lookup 51890'
assert_log '-4 rule add priority 50 to 203.0.113.10/32 lookup 51890'
assert_log '-6 rule add priority 50 to 2001:db8::10/128 lookup 51890'
assert_log '-4 route replace unreachable default table 51890 metric 32767'
assert_log '-6 route replace unreachable default table 51890 metric 32767'

# Re-running with an unchanged signature must not churn policy routes.
cp "$tmp/ip.log" "$tmp/ip.first.log"
: > "$tmp/ip.log"
"$ROUTE_WATCH" >/dev/null 2>>"$tmp/route-watch.log"
if grep -Fq 'route replace 46.243.227.103/32' "$tmp/ip.log"; then
  fail 'unchanged endpoint policy was rewritten'
fi

# Wildcards are deliberately invalid: endpoint matching is exact-only.
printf '*.ad-tech.ru\n' > "$tmp/endpoints/secondary.txt"
rm -f "$tmp/endpoint-state/signature"
: > "$tmp/ip.log"
"$ROUTE_WATCH" >/dev/null 2>>"$tmp/route-watch.log"
grep -Fq "wildcards are not supported" "$tmp/route-watch.log" ||
  fail 'wildcard endpoint was not rejected'
if grep -Fq 'rule add priority 50 to' "$tmp/ip.log"; then
  fail 'policy was installed for an invalid wildcard endpoint'
fi

printf 'VPN endpoint underlay tests: OK\n'
