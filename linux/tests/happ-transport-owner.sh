#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
PROVIDER="${HAPP_PROVIDER_UNDER_TEST:-${ROOT}/linux/files/endpoint-providers/happ}"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin"
printf '0\n' >"$tmp/other-active"

cat >"$tmp/vpn.conf" <<'EOF_VPN'
PRIMARY_INTERFACE=tun0
SECONDARY_INTERFACE=vpn0
EOF_VPN

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
case "$*" in
  '-n -x xray') printf '111\n' ;;
  '-n -x sing-box')
    [[ "${MOCK_SINGBOX_DOWN:-0}" == 1 ]] && exit 1
    printf '222\n'
    ;;
  *) exit 1 ;;
esac
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
    KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
    "$@" bash "$PROVIDER" primary
}

# Real-world layout: sing-box owns the TUN-side connection, while Xray owns the
# physical Happ transport. The probe delta on Xray must win over sing-box noise.
cat >"$tmp/bin/ss-xray-owner" <<EOF_SS_XRAY
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "\$*" == '-H -4 -tinp' ]] || exit 0
count_file="$tmp/xray-owner.count"
count=0
[[ -r "\$count_file" ]] && count="\$(cat "\$count_file")"
count=\$((count + 1))
printf '%s\n' "\$count" >"\$count_file"
case "\$count" in
  1|3|5)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.240.219.226%wlp0s20f3:4000 31.76.97.79:443 users:(("xray",pid=111,fd=1))
 cubic bytes_sent:1000 bytes_received:1000
ESTAB 0 0 172.18.0.1:5000 213.180.204.179:443 users:(("sing-box",pid=222,fd=2))
 cubic bytes_sent:5000 bytes_received:5000
EOF_SOCKETS
    ;;
  2|4|6)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.240.219.226%wlp0s20f3:4000 31.76.97.79:443 users:(("xray",pid=111,fd=1))
 cubic bytes_sent:101000 bytes_received:601000
ESTAB 0 0 172.18.0.1:5000 213.180.204.179:443 users:(("sing-box",pid=222,fd=2))
 cubic bytes_sent:25000 bytes_received:65000
EOF_SOCKETS
    ;;
esac
EOF_SS_XRAY
chmod +x "$tmp/bin/ss-xray-owner"

mkdir -p "$tmp/xray-state"
xray_output="$(run_happ "$tmp/xray-state" "$tmp/bin/ss-xray-owner" env)"
grep -Fxq '31.76.97.79' <<<"$xray_output" || die 'Happ did not autodetect Xray as transport owner'
if grep -Fxq '213.180.204.179' <<<"$xray_output"; then
    die 'Happ emitted weaker sing-box data-plane activity as a transport endpoint'
fi
grep -Fq 'owner=xray' "$tmp/xray-state/happ-primary.cache" || die 'Happ cache did not record Xray as transport owner'
grep -Fq 'process=xray:111' "$tmp/xray-state/happ-primary.cache" || die 'Happ cache missed Xray process identity'
grep -Fq 'process=sing-box:222' "$tmp/xray-state/happ-primary.cache" || die 'Happ cache missed sing-box process identity'

# Xray-only Happ builds must remain supported even when sing-box is absent.
rm -f "$tmp/xray-owner.count"
mkdir -p "$tmp/xray-only-state"
xray_only_output="$(
    run_happ "$tmp/xray-only-state" "$tmp/bin/ss-xray-owner" \
        env MOCK_SINGBOX_DOWN=1
)"
grep -Fxq '31.76.97.79' <<<"$xray_only_output" || die 'Happ failed Xray-only transport discovery'
grep -Fq 'process=xray:111' "$tmp/xray-only-state/happ-primary.cache" || die 'Xray-only cache missed Xray identity'
if grep -Fq 'process=sing-box:' "$tmp/xray-only-state/happ-primary.cache"; then
    die 'Xray-only cache recorded a non-running sing-box process'
fi

# The inverse layout must still work: process names are candidates, not roles
# hard-coded into the detector.
cat >"$tmp/bin/ss-singbox-owner" <<EOF_SS_SINGBOX
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "\$*" == '-H -4 -tinp' ]] || exit 0
count_file="$tmp/singbox-owner.count"
count=0
[[ -r "\$count_file" ]] && count="\$(cat "\$count_file")"
count=\$((count + 1))
printf '%s\n' "\$count" >"\$count_file"
case "\$count" in
  1|3|5)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.240.219.226%wlp0s20f3:4100 31.76.19.84:443 users:(("sing-box",pid=222,fd=3))
 cubic bytes_sent:1000 bytes_received:1000
ESTAB 0 0 172.18.0.1:5100 213.180.204.179:443 users:(("xray",pid=111,fd=4))
 cubic bytes_sent:5000 bytes_received:5000
EOF_SOCKETS
    ;;
  2|4|6)
    cat <<'EOF_SOCKETS'
ESTAB 0 0 10.240.219.226%wlp0s20f3:4100 31.76.19.84:443 users:(("sing-box",pid=222,fd=3))
 cubic bytes_sent:101000 bytes_received:601000
ESTAB 0 0 172.18.0.1:5100 213.180.204.179:443 users:(("xray",pid=111,fd=4))
 cubic bytes_sent:25000 bytes_received:65000
EOF_SOCKETS
    ;;
esac
EOF_SS_SINGBOX
chmod +x "$tmp/bin/ss-singbox-owner"

mkdir -p "$tmp/singbox-state"
singbox_output="$(run_happ "$tmp/singbox-state" "$tmp/bin/ss-singbox-owner" env)"
grep -Fxq '31.76.19.84' <<<"$singbox_output" || die 'Happ did not autodetect sing-box as alternate transport owner'
grep -Fq 'owner=sing-box' "$tmp/singbox-state/happ-primary.cache" || die 'Happ cache did not record sing-box owner'

# Topology change invalidates a healthy cache. If the new correlation is
# inconclusive but the cached endpoint is still active on the same Xray owner,
# degraded fallback must keep the physical pin and establish cooldown.
printf '0\n' >"$tmp/other-active"
rm -f "$tmp/xray-owner.count"
mkdir -p "$tmp/fallback-state"
run_happ "$tmp/fallback-state" "$tmp/bin/ss-xray-owner" \
    env KIKIMORA_HAPP_CACHE_TTL=300 >/dev/null

cat >"$tmp/bin/ss-xray-quiet" <<'EOF_SS_QUIET'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  '-H -4 -tnp')
    printf '%s\n' 'ESTAB 0 0 10.240.219.226%wlp0s20f3:4000 31.76.97.79:443 users:(("xray",pid=111,fd=1))'
    ;;
  '-H -4 -tinp'|'-H -4 -unp') exit 0 ;;
esac
EOF_SS_QUIET
chmod +x "$tmp/bin/ss-xray-quiet"

printf '1\n' >"$tmp/other-active"
fallback_output="$(
    run_happ "$tmp/fallback-state" "$tmp/bin/ss-xray-quiet" \
        env KIKIMORA_HAPP_CACHE_TTL=300 \
        2>"$tmp/fallback.err"
)"
grep -Fxq '31.76.97.79' <<<"$fallback_output" || die 'Xray-owner degraded fallback lost the cached transport'
grep -Fq 'degraded=1' "$tmp/fallback-state/happ-primary.cache" || die 'Xray-owner fallback cache was not marked degraded'
grep -Fq 'other_active=1' "$tmp/fallback-state/happ-primary.cache" || die 'Xray-owner fallback cache did not adopt current topology'
grep -Fq 'reconnect Happ manually' "$tmp/fallback.err" || die 'Xray-owner fallback did not preserve manual reconnect guidance'

# UDP sockets are valid liveness evidence for an endpoint already proven by a
# prior correlation. They are deliberately not used as fresh discovery proof,
# because ss does not expose reliable cumulative UDP byte counters.
mkdir -p "$tmp/udp-state"
now="$(date +%s)"
cat >"$tmp/udp-state/happ-primary.cache" <<EOF_UDP_CACHE
timestamp=$now
other_interface=vpn0
other_active=1
degraded=1
process=sing-box:222
process=xray:111
owner=xray
endpoint=198.51.100.44
EOF_UDP_CACHE

cat >"$tmp/bin/ss-udp-live" <<'EOF_SS_UDP'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  '-H -4 -tnp') exit 0 ;;
  '-H -4 -unp')
    printf '%s\n' 'ESTAB 0 0 10.240.219.226%wlp0s20f3:4500 198.51.100.44:443 users:(("xray",pid=111,fd=9))'
    ;;
  '-H -4 -tinp')
    printf 'unexpected fresh correlation during UDP cache liveness check\n' >&2
    exit 70
    ;;
esac
EOF_SS_UDP
chmod +x "$tmp/bin/ss-udp-live"

udp_output="$(
    run_happ "$tmp/udp-state" "$tmp/bin/ss-udp-live" \
        env KIKIMORA_HAPP_CACHE_TTL=300
)"
grep -Fxq '198.51.100.44' <<<"$udp_output" || die 'Happ cache did not accept UDP liveness for a previously proven endpoint'

printf 'Happ transport-owner tests: OK\n'
