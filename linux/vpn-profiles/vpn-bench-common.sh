#!/usr/bin/env bash
set -Eeuo pipefail

# Standalone VPN benchmark helper for Kikimora development/tests.
# This file is intentionally not wired into Kikimora install/systemd/CLI.

RUNS="${RUNS:-12}"
DOWNLOAD_URL="${DOWNLOAD_URL:-https://speed.cloudflare.com/__down?bytes=10000000}"
HTTPS_URL="${HTTPS_URL:-https://www.google.com/generate_204}"
PING_HOST="${PING_HOST:-1.1.1.1}"
OUTPUT_DIR="${OUTPUT_DIR:-./vpn-bench-results}"

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        printf 'Missing required command: %s\n' "$1" >&2
        exit 127
    }
}

for cmd in curl ping ip date uname awk sed grep; do
    require_cmd "$cmd"
done

mkdir -p "$OUTPUT_DIR"

collect_snapshot() {
    local label="$1"
    local stamp outfile
    stamp="$(date +'%Y%m%d-%H%M%S')"
    outfile="${OUTPUT_DIR}/${stamp}-${label}.txt"

    {
        printf 'label=%s\n' "$label"
        printf 'timestamp=%s\n' "$(date --iso-8601=seconds)"
        printf 'hostname=%s\n' "$(hostname 2>/dev/null || true)"
        printf 'kernel=%s\n' "$(uname -srmo)"
        printf 'runs=%s\n' "$RUNS"
        printf 'https_url=%s\n' "$HTTPS_URL"
        printf 'download_url=%s\n' "$DOWNLOAD_URL"
        printf 'ping_host=%s\n' "$PING_HOST"

        printf '\n== ip link ==\n'
        ip -br link || true
        printf '\n== ip addr ==\n'
        ip -br addr || true

        printf '\n== tunnel-like interface candidates ==\n'
        for devpath in /sys/class/net/*; do
            [[ -e "$devpath" ]] || continue
            dev="${devpath##*/}"
            kind="$(ip -d link show dev "$dev" 2>/dev/null | sed -n '1,2p' | tr '\n' ' ')"
            case "$kind" in
                *tun*|*tap*|*wireguard*|*amneziawg*|*awg*|*wg*)
                    printf '%s\n' "$kind"
                    ;;
            esac
        done

        printf '\n== ip route ==\n'
        ip -4 route show table main || true
        printf '\n== ip rule ==\n'
        ip -4 rule show || true
        printf '\n== route to ping target ==\n'
        ip -4 route get "$PING_HOST" || true

        printf '\n== route-selected interface ==\n'
        ip -4 route get "$PING_HOST" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") {print $(i+1); exit}}' || true

        printf '\n== resolv.conf ==\n'
        cat /etc/resolv.conf 2>/dev/null || true
        if command -v resolvectl >/dev/null 2>&1; then
            printf '\n== resolvectl status ==\n'
            resolvectl status 2>/dev/null || true
        fi
        printf '\n== processes ==\n'
        ps -eo pid,comm,args 2>/dev/null | grep -Ei 'amnezia|xray|tun2socks|wireguard|awg' | grep -v grep || true

        printf '\n== ping ==\n'
        ping -4 -c 20 -i 0.2 -W 2 "$PING_HOST" || true

        printf '\n== HTTPS latency runs ==\n'
        printf 'run,dns,connect,tls,ttfb,total,http_code,remote_ip\n'
        local i
        for ((i=1; i<=RUNS; i++)); do
            curl -4 -L --max-time 20 -o /dev/null -sS \
                -w "${i},%{time_namelookup},%{time_connect},%{time_appconnect},%{time_starttransfer},%{time_total},%{http_code},%{remote_ip}\\n" \
                "$HTTPS_URL" || printf '%s,ERROR\n' "$i"
            sleep 0.25
        done

        printf '\n== download runs ==\n'
        printf 'run,connect,tls,ttfb,total,speed_download,size_download,http_code,remote_ip\n'
        for ((i=1; i<=5; i++)); do
            curl -4 -L --max-time 60 -o /dev/null -sS \
                -w "${i},%{time_connect},%{time_appconnect},%{time_starttransfer},%{time_total},%{speed_download},%{size_download},%{http_code},%{remote_ip}\\n" \
                "$DOWNLOAD_URL" || printf '%s,ERROR\n' "$i"
            sleep 0.5
        done
    } | tee "$outfile"

    printf '\nSaved: %s\n' "$outfile" >&2
}
