#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly ROUTE_WATCH="${ROOT}/linux/files/route-watch"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    if [[ -f "${tmp:-}/ip.log" ]]; then
        cat "$tmp/ip.log" >&2
    fi
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/endpoints" "$tmp/providers" "$tmp/state"
: >"$tmp/endpoints/primary.txt"
: >"$tmp/endpoints/secondary.txt"
: >"$tmp/rules4"
printf '192.0.2.1\n' >"$tmp/gateway"
printf '1\n' >"$tmp/tun-up"
printf '198.51.100.10\n' >"$tmp/dynamic-endpoints"

cat >"$tmp/vpn.conf" <<'EOF_VPN'
PRIMARY_INTERFACE="amn0"
PRIMARY_ENDPOINT_PROVIDER="static"
PRIMARY_ENDPOINT_PROVIDER_ARGS=""
SECONDARY_INTERFACE="tun0"
SECONDARY_ENDPOINT_PROVIDER="dynamic-test"
SECONDARY_ENDPOINT_PROVIDER_ARGS=""
EOF_VPN

cp "$ROOT/linux/files/endpoint-providers/static" "$tmp/providers/static"
chmod +x "$tmp/providers/static"
cat >"$tmp/providers/dynamic-test" <<'EOF_PROVIDER'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '# kikimora-endpoint-provider-mode: dynamic-additive\n'
cat "$DYNAMIC_ENDPOINT_FILE"
EOF_PROVIDER
chmod +x "$tmp/providers/dynamic-test"

cat >"$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$MOCK_IP_LOG"

rule_show4() {
    local address
    while IFS= read -r address; do
        [[ -n "$address" ]] || continue
        printf '51: from all to %s/32 lookup 51890\n' "$address"
    done <"$MOCK_RULES4"
}

rule_add4() {
    local previous='' token address=''
    for token in "$@"; do
        if [[ "$previous" == to ]]; then
            address="${token%/32}"
            break
        fi
        previous="$token"
    done
    [[ -n "$address" ]] || exit 64
    grep -Fxq -- "$address" "$MOCK_RULES4" 2>/dev/null || printf '%s\n' "$address" >>"$MOCK_RULES4"
}

rule_del_priority51() {
    [[ -s "$MOCK_RULES4" ]] || exit 2
    tail -n +2 "$MOCK_RULES4" >"${MOCK_RULES4}.new"
    mv -f "${MOCK_RULES4}.new" "$MOCK_RULES4"
}

case "$*" in
  '-4 route show table main default')
    gateway="$(cat "$MOCK_GATEWAY_FILE")"
    printf 'default via %s dev wlp0s20f3 proto dhcp metric 600\n' "$gateway"
    ;;
  '-6 route show table main default')
    ;;
  '-o link show dev tun0')
    [[ "$(cat "$MOCK_TUN_UP_FILE")" == 1 ]] || exit 1
    printf '7: tun0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1500 state UNKNOWN mode DEFAULT\n'
    ;;
  '-4 -o address show dev tun0')
    [[ "$(cat "$MOCK_TUN_UP_FILE")" == 1 ]] || exit 1
    printf '7: tun0    inet 172.18.0.1/30 scope global tun0\n'
    ;;
  '-o link show dev amn0'|'-4 -o address show dev amn0')
    exit 1
    ;;
  '-4 rule show')
    rule_show4
    ;;
  '-6 rule show')
    ;;
  '-4 rule add priority 51 to '*'/32 lookup 51890')
    rule_add4 "$@"
    ;;
  '-6 rule add priority 51 to '*'/128 lookup 51890')
    exit 0
    ;;
  '-4 rule del priority 51 lookup 51890')
    rule_del_priority51
    ;;
  '-4 rule del priority 50 lookup 51890'|'-6 rule del priority 50 lookup 51890'|'-6 rule del priority 51 lookup 51890')
    exit 2
    ;;
  '-4 route get '*)
    address="${*: -1}"
    gateway="$(cat "$MOCK_GATEWAY_FILE")"
    printf '%s via %s dev wlp0s20f3 table 51890 src 192.0.2.20\n' "$address" "$gateway"
    ;;
  '-6 route get '*)
    exit 2
    ;;
  '-4 route replace '*|'-6 route replace '*|'-4 route flush table 51890'|'-6 route flush table 51890')
    ;;
  *)
    exit 0
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

export PATH="$tmp/bin:$PATH"
export MOCK_IP_LOG="$tmp/ip.log"
export MOCK_RULES4="$tmp/rules4"
export MOCK_GATEWAY_FILE="$tmp/gateway"
export MOCK_TUN_UP_FILE="$tmp/tun-up"
export DYNAMIC_ENDPOINT_FILE="$tmp/dynamic-endpoints"
export KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints"
export KIKIMORA_ENDPOINT_PROVIDERS_DIR="$tmp/providers"
export KIKIMORA_ENDPOINT_STATE_DIR="$tmp/state"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_ENDPOINT_ROUTE_TABLE=51890
export KIKIMORA_PRIMARY_ENDPOINT_RULE_PRIORITY=50
export KIKIMORA_SECONDARY_ENDPOINT_RULE_PRIORITY=51

# First live observation may be added because the dynamic provider promises the
# endpoint was already observed on the physical underlay.
: >"$tmp/ip.log"
bash "$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/first.log"
grep -Fxq '198.51.100.10' "$tmp/rules4" || die 'first dynamic endpoint was not installed live'
[[ ! -e "$tmp/state/secondary.pending" ]] || die 'first safe dynamic endpoint incorrectly became pending'

# Provider switches to a new transport while tun0 stays live. The new endpoint
# is added, but the old rule is deliberately retained until a safe down boundary.
printf '198.51.100.20\n' >"$tmp/dynamic-endpoints"
: >"$tmp/ip.log"
bash "$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/second.log"
grep -Fxq '198.51.100.10' "$tmp/rules4" || die 'old live dynamic endpoint was removed'
grep -Fxq '198.51.100.20' "$tmp/rules4" || die 'new live dynamic endpoint was not added'
if grep -Fq -- '-4 rule del priority 51 lookup 51890' "$tmp/ip.log"; then
    die 'dynamic live update cleared role rules instead of adding monotonically'
fi

# A physical underlay change is not an additive endpoint change. It must defer
# rather than moving existing live transport routes underneath tun0.
printf '192.0.2.254\n' >"$tmp/gateway"
printf '198.51.100.30\n' >"$tmp/dynamic-endpoints"
: >"$tmp/ip.log"
bash "$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/underlay-change.log"
[[ -e "$tmp/state/secondary.pending" ]] || die 'physical underlay change did not create pending state'
if grep -Fxq '198.51.100.30' "$tmp/rules4"; then
    die 'dynamic endpoint was added while physical underlay change was pending'
fi
grep -Fxq '198.51.100.10' "$tmp/rules4" || die 'old endpoint disappeared during pending underlay transition'
grep -Fxq '198.51.100.20' "$tmp/rules4" || die 'second endpoint disappeared during pending underlay transition'

# Once the managed VPN is down the provider can reconcile exactly. Happ emits
# no live transports while down, so accumulated rules are safely removed.
printf '0\n' >"$tmp/tun-up"
: >"$tmp/dynamic-endpoints"
: >"$tmp/ip.log"
bash "$ROUTE_WATCH" endpoint reconcile >/dev/null 2>"$tmp/down.log"
[[ ! -s "$tmp/rules4" ]] || die 'dynamic endpoints were not cleaned at the safe down boundary'
[[ ! -e "$tmp/state/secondary.pending" ]] || die 'pending state survived safe exact reconcile'

printf 'Dynamic endpoint provider safety tests: OK\n'
