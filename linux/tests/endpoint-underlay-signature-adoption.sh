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
: > "$tmp/endpoints/primary.txt"
printf 've.ad-tech.ru\n' > "$tmp/endpoints/secondary.txt"
printf 'stale-secondary-signature\n' > "$tmp/state/secondary.signature"

cat > "$tmp/bin/getent" <<'EOF_GETENT'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  'ahostsv4 ve.ad-tech.ru') printf '46.243.227.103 STREAM ve.ad-tech.ru\n' ;;
  'ahostsv6 ve.ad-tech.ru') exit 2 ;;
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
      'default dev vpn0 proto static scope link metric 50' \
      'default via 192.0.2.1 dev wlan0 proto dhcp metric 600'
    ;;
  '-6 route show table main default')
    printf '%s\n' 'default via fe80::1 dev wlan0 proto ra metric 600'
    ;;
  '-o link show dev vpn0')
    printf '%s\n' '8: vpn0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1434 state UNKNOWN'
    ;;
  '-4 -o address show dev vpn0')
    printf '%s\n' '8: vpn0 inet 10.0.0.81/24 scope global vpn0'
    ;;
  '-4 rule show')
    printf '%s\n' \
      '0: from all lookup local' \
      '51: from all to 46.243.227.103 lookup 51890' \
      '32766: from all lookup main'
    ;;
  '-6 rule show')
    printf '%s\n' \
      '0: from all lookup local' \
      '32766: from all lookup main'
    ;;
  '-4 route get 46.243.227.103')
    printf '%s\n' '46.243.227.103 via 192.0.2.1 dev wlan0 table 51890 src 192.0.2.2'
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
  printf '%s\n' '--- route-watch log ---' >&2
  cat "$tmp/reconcile.log" >&2 2>/dev/null || true
  printf '%s\n' '--- ip log ---' >&2
  cat "$tmp/ip.log" >&2 2>/dev/null || true
  exit 1
}

"$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/reconcile.log"

[[ ! -e "$tmp/state/secondary.pending" ]] || fail 'matching live policy was incorrectly marked pending'
[[ "$(cat "$tmp/state/secondary.signature")" != 'stale-secondary-signature' ]] || fail 'stale signature was not refreshed'
grep -Fq 'Secondary endpoint underlay signature refreshed' "$tmp/reconcile.log" || fail 'signature refresh was not logged'

if grep -Eq 'rule (add|del) priority 51|route replace 46\.243\.227\.103' "$tmp/ip.log"; then
  fail 'signature adoption mutated live secondary endpoint policy'
fi

printf 'VPN endpoint live signature adoption regression: OK\n'
