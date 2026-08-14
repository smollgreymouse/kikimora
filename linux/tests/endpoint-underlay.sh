#!/usr/bin/env bash
set -Eeuo pipefail

readonly ROUTE_WATCH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/route-watch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin" "$tmp/endpoints" "$tmp/state"
cat > "$tmp/vpn.conf" <<'EOF_CONFIG'
PRIMARY_INTERFACE="amn0"
SECONDARY_INTERFACE="vpn0"
EOF_CONFIG
cat > "$tmp/endpoints/primary.txt" <<'EOF_PRIMARY'
primary-vpn.example
EOF_PRIMARY
cat > "$tmp/endpoints/secondary.txt" <<'EOF_SECONDARY'
ve.ad-tech.ru
EOF_SECONDARY

cat > "$tmp/bin/getent" <<'EOF_GETENT'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  'ahostsv4 primary-vpn.example') printf '203.0.113.10 STREAM primary-vpn.example\n' ;;
  'ahostsv6 primary-vpn.example') printf '2001:db8::10 STREAM primary-vpn.example\n' ;;
  'ahostsv4 ve.ad-tech.ru') printf '46.243.227.103 STREAM ve.ad-tech.ru\n' ;;
  'ahostsv6 ve.ad-tech.ru') printf '::ffff:46.243.227.103 STREAM ve.ad-tech.ru\n' ;;
  *) exit 2 ;;
esac
EOF_GETENT

cat > "$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >> "${MOCK_IP_LOG}"
case "$*" in
  '-4 route show table main default')
    printf '%s\n' \
      'default proto static scope link dev vpn0 metric 50' \
      'default dev amn0 scope link metric 100' \
      'default via 192.0.2.1 dev wlp0s20f3 proto dhcp metric 600'
    ;;
  '-6 route show table main default')
    printf '%s\n' \
      'default dev amn0 metric 100' \
      'default via fe80::1 dev wlp0s20f3 proto ra metric 600'
    ;;
  'link show dev amn0'|'link show dev vpn0')
    exit 1
    ;;
  '-4 rule show'|'-6 rule show')
    exit 0
    ;;
  '-4 rule del '*|'-6 rule del '*)
    exit 2
    ;;
  *)
    exit 0
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/getent" "$tmp/bin/ip"

export PATH="$tmp/bin:$PATH"
export MOCK_IP_LOG="$tmp/ip.log"
export KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints"
export KIKIMORA_ENDPOINT_STATE_DIR="$tmp/state"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_ENDPOINT_ROUTE_TABLE=51890
export KIKIMORA_PRIMARY_ENDPOINT_RULE_PRIORITY=50
export KIKIMORA_SECONDARY_ENDPOINT_RULE_PRIORITY=51

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  cat "$tmp/ip.log" >&2 2>/dev/null || true
  exit 1
}
assert_log() {
  grep -Fqx -- "$1" "$tmp/ip.log" || fail "missing ip invocation: $1"
}

"$ROUTE_WATCH" endpoint apply >/dev/null 2>"$tmp/apply.log"
assert_log '-4 route replace 203.0.113.10/32 via 192.0.2.1 dev wlp0s20f3 onlink table 51890 metric 1'
assert_log '-4 route replace 46.243.227.103/32 via 192.0.2.1 dev wlp0s20f3 onlink table 51890 metric 1'
assert_log '-6 route replace 2001:db8::10/128 via fe80::1 dev wlp0s20f3 onlink table 51890 metric 1'
assert_log '-4 rule add priority 50 to 203.0.113.10/32 lookup 51890'
assert_log '-6 rule add priority 50 to 2001:db8::10/128 lookup 51890'
assert_log '-4 rule add priority 51 to 46.243.227.103/32 lookup 51890'
if grep -Fq -- '::ffff:46.243.227.103' "$tmp/ip.log"; then
  fail 'IPv4-mapped ahostsv6 answer created IPv6 endpoint policy'
fi
[[ -s "$tmp/state/primary.signature" ]] || fail 'primary signature missing'
[[ -s "$tmp/state/secondary.signature" ]] || fail 'secondary signature missing'

# Exact endpoint syntax only: wildcard must be rejected and existing policy kept.
printf '*.ad-tech.ru\n' > "$tmp/endpoints/secondary.txt"
: > "$tmp/ip.log"
"$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/wildcard.log" || true
grep -Fq 'wildcards are not supported' "$tmp/wildcard.log" || fail 'wildcard endpoint was not rejected'
if grep -Fq 'rule add priority 51' "$tmp/ip.log"; then
  fail 'invalid wildcard changed secondary endpoint rules'
fi

printf 'VPN endpoint underlay unit regression: OK\n'
