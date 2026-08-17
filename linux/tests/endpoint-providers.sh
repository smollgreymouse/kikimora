#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly PROVIDERS="${ROOT}/linux/files/endpoint-providers"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/endpoints" "$tmp/bin"
printf '0\n' >"$tmp/other-active"

cat >"$tmp/endpoints/secondary.txt" <<'EOF_STATIC'
# comment
ve.ad-tech.ru
46.243.227.103
EOF_STATIC
static_output="$(KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints" bash "$PROVIDERS/static" secondary)"
grep -Fq '# kikimora-endpoint-provider-mode: static' <<<"$static_output" || die 'static provider mode header missing'
grep -Fq 've.ad-tech.ru' <<<"$static_output" || die 'static provider did not return role endpoint file'

cat >"$tmp/custom-provider" <<'EOF_COMMAND'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${KIKIMORA_ENDPOINT_ROLE:-}" == secondary ]] || exit 70
[[ "${1:-}" == secondary ]] || exit 71
printf '# kikimora-endpoint-provider-mode: dynamic-additive\n203.0.113.77\n'
EOF_COMMAND
chmod +x "$tmp/custom-provider"
command_output="$(bash "$PROVIDERS/command" secondary "$tmp/custom-provider")"
grep -Fq 'dynamic-additive' <<<"$command_output" || die 'command provider did not pass through provider mode'
grep -Fq '203.0.113.77' <<<"$command_output" || die 'command provider did not pass through endpoints'

cat >"$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  '-o link show dev tun0')
    printf '7: tun0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1500\n'
    ;;
  '-4 -o address show dev tun0')
    printf '7: tun0    inet 172.18.0.1/30 scope global tun0\n'
    ;;
  '-o link show dev vpn0')
    if [[ -r "${MOCK_OTHER_ACTIVE_FILE:-}" && "$(<"$MOCK_OTHER_ACTIVE_FILE")" == 1 ]]; then
        printf '8: vpn0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1500\n'
    fi
    ;;
  '-4 -o address show dev vpn0')
    if [[ -r "${MOCK_OTHER_ACTIVE_FILE:-}" && "$(<"$MOCK_OTHER_ACTIVE_FILE")" == 1 ]]; then
        printf '8: vpn0    inet 10.0.0.81/24 scope global vpn0\n'
    fi
    ;;
  '-4 route get 140.82.121.3')
    printf '140.82.121.3 via 172.18.0.2 dev tun0 table 2022 src 172.18.0.1\n'
    ;;
  *) exit 0 ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

cat >"$tmp/bin/getent" <<'EOF_GETENT'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$*" == 'ahostsv4 github.com' ]] || exit 1
printf '140.82.121.3 STREAM github.com\n'
EOF_GETENT
chmod +x "$tmp/bin/getent"

cat >"$tmp/bin/pgrep" <<'EOF_PGREP'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$*" == '-n -x sing-box' ]] || exit 1
printf '3441088\n'
EOF_PGREP
chmod +x "$tmp/bin/pgrep"

cat >"$tmp/bin/curl" <<'EOF_CURL'
#!/usr/bin/env bash
set -Eeuo pipefail
exit 0
EOF_CURL
chmod +x "$tmp/bin/curl"

cat >"$tmp/bin/sleep" <<'EOF_SLEEP'
#!/usr/bin/env bash
set -Eeuo pipefail
exit 0
EOF_SLEEP
chmod +x "$tmp/bin/sleep"

run_happ() {
    local state_dir="$1" ss_cmd="$2"
    shift 2
    KIKIMORA_ENDPOINT_INTERFACE=tun0 \
    MOCK_OTHER_ACTIVE_FILE="$tmp/other-active" \
    KIKIMORA_IP="$tmp/bin/ip" \
    KIKIMORA_SS="$ss_cmd" \
    KIKIMORA_CURL="$tmp/bin/curl" \
    KIKIMORA_GETENT="$tmp/bin/getent" \
    KIKIMORA_SLEEP="$tmp/bin/sleep" \
    KIKIMORA_PGREP="$tmp/bin/pgrep" \
    KIKIMORA_HAPP_STATE_DIR="$state_dir" \
    KIKIMORA_HAPP_CACHE_TTL=0 \
    KIKIMORA_VPN_CONFIG="$tmp/no-vpn.conf" \
    "$@" bash "$PROVIDERS/happ" primary
}

# Stable correlation: every synthetic request creates a dominant flow to the
# same Happ transport. Smaller unrelated activity must not be emitted.
mkdir -p "$tmp/stable-state"
cat >"$tmp/bin/ss-stable" <<EOF_SS_STABLE
#!/usr/bin/env bash
set -Eeuo pipefail
count_file="$tmp/ss-stable.count"
count=0
[[ -r "\$count_file" ]] && count="\$(cat "\$count_file")"
count=\$((count + 1))
printf '%s\n' "\$count" >"\$count_file"
case "\$count" in
  1|3|5) exit 0 ;;
  2|4|6)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.240.219.226%wlp0s20f3:41000 31.76.97.79:443 users:(("sing-box",pid=3441088,fd=10))
 cubic bytes_sent:100000 bytes_received:600000
ESTAB 0 0 10.240.219.226%wlp0s20f3:41001 153.80.241.175:443 users:(("sing-box",pid=3441088,fd=11))
 cubic bytes_sent:100 bytes_received:100
EOF_SOCKETS
    ;;
esac
EOF_SS_STABLE
chmod +x "$tmp/bin/ss-stable"

stable_output="$(run_happ "$tmp/stable-state" "$tmp/bin/ss-stable" env)"
grep -Fq '# kikimora-endpoint-provider-mode: dynamic-additive' <<<"$stable_output" || die 'Happ provider mode header missing'
grep -Fxq '31.76.97.79' <<<"$stable_output" || die 'Happ correlated provider missed stable transport endpoint'
if grep -Fxq '153.80.241.175' <<<"$stable_output"; then
    die 'Happ correlated provider emitted weak background activity'
fi

# Happ may rotate transport servers between requests. The provider must return
# the union of dominant per-round transports so dynamic-additive core can pin all
# transports observed during the correlation window.
mkdir -p "$tmp/switch-state"
cat >"$tmp/bin/ss-switch" <<EOF_SS_SWITCH
#!/usr/bin/env bash
set -Eeuo pipefail
count_file="$tmp/ss-switch.count"
count=0
[[ -r "\$count_file" ]] && count="\$(cat "\$count_file")"
count=\$((count + 1))
printf '%s\n' "\$count" >"\$count_file"
case "\$count" in
  1|3|5) exit 0 ;;
  2|4)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.0.0.81%vpn0:42000 153.80.241.175:443 users:(("sing-box",pid=3441088,fd=20))
 cubic bytes_sent:100000 bytes_received:600000
ESTAB 0 0 10.0.0.81%vpn0:42001 31.76.19.84:443 users:(("sing-box",pid=3441088,fd=21))
 cubic bytes_sent:10000 bytes_received:10000
EOF_SOCKETS
    ;;
  6)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.0.0.81%vpn0:43000 31.76.19.84:443 users:(("sing-box",pid=3441088,fd=30))
 cubic bytes_sent:100000 bytes_received:600000
EOF_SOCKETS
    ;;
esac
EOF_SS_SWITCH
chmod +x "$tmp/bin/ss-switch"

switch_output="$(run_happ "$tmp/switch-state" "$tmp/bin/ss-switch" env)"
grep -Fxq '153.80.241.175' <<<"$switch_output" || die 'Happ provider missed first rotated transport'
grep -Fxq '31.76.19.84' <<<"$switch_output" || die 'Happ provider missed second rotated transport'

# A cache created while the other managed VPN is down must not suppress fresh
# discovery after that VPN comes up. The old transport may still be active, but
# newly selected Happ transport can otherwise escape through the other VPN.
cat >"$tmp/cache-vpn.conf" <<'EOF_CACHE_VPN'
PRIMARY_INTERFACE=tun0
SECONDARY_INTERFACE=vpn0
EOF_CACHE_VPN

mkdir -p "$tmp/cache-state"
printf 'old\n' >"$tmp/cache-mode"
printf '0\n' >"$tmp/other-active"

cat >"$tmp/bin/ss-cache" <<EOF_SS_CACHE
#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "\$*" == '-H -4 -tnp' ]]; then
    printf '%s\n' 'ESTAB 0 0 10.240.219.226%wlp0s20f3:44000 31.76.19.84:443 users:(("sing-box",pid=3441088,fd=40))'
    exit 0
fi

count_file="$tmp/ss-cache.count"
count=0
[[ -r "\$count_file" ]] && count="\$(cat "\$count_file")"
count=\$((count + 1))
printf '%s\n' "\$count" >"\$count_file"

case "\$count" in
  1|3|5)
    exit 0
    ;;
  2|4|6)
    mode="\$(cat "$tmp/cache-mode")"
    if [[ "\$mode" == old ]]; then
        endpoint=31.76.19.84
    else
        endpoint=153.80.241.175
    fi
    printf 'ESTAB 0 0 10.240.219.226%%wlp0s20f3:44001 %s:443 users:(("sing-box",pid=3441088,fd=41))\n' "\$endpoint"
    printf ' cubic bytes_sent:100000 bytes_received:600000\n'
    ;;
esac
EOF_SS_CACHE
chmod +x "$tmp/bin/ss-cache"

cache_first="$(
    run_happ "$tmp/cache-state" "$tmp/bin/ss-cache" \
        env \
        KIKIMORA_HAPP_CACHE_TTL=300 \
        KIKIMORA_VPN_CONFIG="$tmp/cache-vpn.conf"
)"
grep -Fxq '31.76.19.84' <<<"$cache_first" ||
    die 'Happ cache setup did not discover initial transport'
grep -Fq 'other_interface=vpn0' "$tmp/cache-state/happ-primary.cache" ||
    die 'Happ cache did not persist other managed interface'
grep -Fq 'other_active=0' "$tmp/cache-state/happ-primary.cache" ||
    die 'Happ cache did not persist inactive other-VPN state'

printf '1\n' >"$tmp/other-active"
printf 'new\n' >"$tmp/cache-mode"
rm -f "$tmp/ss-cache.count"

cache_second="$(
    run_happ "$tmp/cache-state" "$tmp/bin/ss-cache" \
        env \
        KIKIMORA_HAPP_CACHE_TTL=300 \
        KIKIMORA_VPN_CONFIG="$tmp/cache-vpn.conf"
)"
grep -Fxq '153.80.241.175' <<<"$cache_second" ||
    die 'Happ reused stale cache after other managed VPN became active'
if grep -Fxq '31.76.19.84' <<<"$cache_second"; then
    die 'Happ returned stale cached endpoint instead of refreshing transport proof'
fi
grep -Fq 'other_active=1' "$tmp/cache-state/happ-primary.cache" ||
    die 'Happ cache did not refresh other-VPN state'

# Correlation must fail closed when no round has enough signal. Core then keeps
# the previous endpoint policy instead of pinning an arbitrary destination.
rm -f "$tmp/ss-stable.count"
mkdir -p "$tmp/weak-state"
if weak_output="$(run_happ "$tmp/weak-state" "$tmp/bin/ss-stable" env KIKIMORA_HAPP_PROBE_MIN_BYTES=1000000 2>"$tmp/weak.err")"; then
    die "Happ provider accepted weak correlation: $weak_output"
fi
grep -Fq 'correlated endpoint discovery was inconclusive' "$tmp/weak.err" || die 'Happ provider did not explain inconclusive discovery'

# If another managed VPN already owns Happ's current outer socket, discovery is
# still valid, but the provider must tell the operator that existing connections
# need a manual reconnect after Kikimora installs the physical pin.
cat >"$tmp/vpn.conf" <<'EOF_VPN'
PRIMARY_INTERFACE=tun0
SECONDARY_INTERFACE=vpn0
EOF_VPN
rm -f "$tmp/ss-stable.count"
mkdir -p "$tmp/nested-state"
KIKIMORA_ENDPOINT_INTERFACE=tun0 \
MOCK_OTHER_ACTIVE_FILE="$tmp/other-active" \
KIKIMORA_IP="$tmp/bin/ip" \
KIKIMORA_SS="$tmp/bin/ss-stable" \
KIKIMORA_CURL="$tmp/bin/curl" \
KIKIMORA_GETENT="$tmp/bin/getent" \
KIKIMORA_SLEEP="$tmp/bin/sleep" \
KIKIMORA_PGREP="$tmp/bin/pgrep" \
KIKIMORA_HAPP_STATE_DIR="$tmp/nested-state" \
KIKIMORA_HAPP_CACHE_TTL=0 \
KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
bash "$PROVIDERS/happ" primary >"$tmp/nested.out" 2>"$tmp/nested.err"
grep -Fxq '31.76.97.79' "$tmp/nested.out" || die 'nested Happ discovery lost the correlated endpoint'
# ss-stable models a physical source, so no nested warning is expected here.
if grep -Fq 'reconnect Happ manually' "$tmp/nested.err"; then
    die 'Happ provider warned about nesting when the observed source was physical'
fi

printf 'Endpoint provider plugin tests: OK\n'
