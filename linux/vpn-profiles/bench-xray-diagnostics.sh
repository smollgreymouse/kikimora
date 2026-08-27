#!/usr/bin/env bash
set -Eeuo pipefail

OUTPUT_DIR="${OUTPUT_DIR:-./vpn-bench-results}"
mkdir -p "$OUTPUT_DIR"
stamp="$(date +'%Y%m%d-%H%M%S')"
outfile="${OUTPUT_DIR}/${stamp}-xray-diagnostics.txt"

section() { printf '\n== %s ==\n' "$1"; }
run() {
    printf '\n$ %s\n' "$*"
    "$@" 2>&1 || true
}

{
    printf 'label=xray-diagnostics\n'
    printf 'timestamp=%s\n' "$(date --iso-8601=seconds)"
    printf 'hostname=%s\n' "$(hostname 2>/dev/null || true)"
    printf 'kernel=%s\n' "$(uname -srmo)"

    section 'interfaces summary'
    run ip -br link
    run ip -br addr

    section 'detailed tunnel-like interfaces'
    while IFS=: read -r _ ifname _; do
        ifname="${ifname// /}"
        [[ -n "$ifname" ]] || continue
        if ip -d link show dev "$ifname" 2>/dev/null | grep -Eq 'tun|POINTOPOINT|wireguard'; then
            run ip -d link show dev "$ifname"
            run ip -s link show dev "$ifname"
            run ip addr show dev "$ifname"
        fi
    done < <(ip -o link show)

    section 'routing all tables'
    run ip -4 route show table all
    run ip -6 route show table all
    run ip -4 rule show
    run ip -6 rule show

    section 'route probes'
    for target in 1.1.1.1 8.8.8.8 9.9.9.9; do
        run ip -4 route get "$target"
    done

    section 'resolver state'
    run cat /etc/resolv.conf
    if command -v resolvectl >/dev/null 2>&1; then
        run resolvectl status
        run resolvectl query www.google.com
        run resolvectl query cloudflare.com
    fi

    section 'vpn processes'
    ps -eo pid,ppid,user,stat,etime,comm,args 2>/dev/null | grep -Ei 'amnezia|xray|tun2socks|wireguard|awg' | grep -v grep || true

    section 'all TCP and UDP sockets'
    if command -v ss >/dev/null 2>&1; then
        run ss -lntup
        run ss -ntup
    fi

    section 'TCP transport diagnostics (DPI/retransmit clues)'
    if command -v ss >/dev/null 2>&1; then
        run ss -ti
    fi
    if command -v nstat >/dev/null 2>&1; then
        run nstat -az
    fi

    section 'local SOCKS listeners'
    if command -v ss >/dev/null 2>&1; then
        ss -lntp 2>/dev/null | grep -Ei '127\.0\.0\.1|amnezia|xray|tun2socks' || true
    fi

    section 'ICMP probes (note: tun2socks may synthesize replies)'
    run ping -4 -c 5 -W 2 1.1.1.1
    run ping -4 -c 5 -W 2 8.8.8.8

    section 'hostname HTTPS verbose'
    run curl -4 -v --http1.1 --connect-timeout 5 --max-time 15 https://www.google.com/generate_204 -o /dev/null

    section 'direct-IP HTTPS verbose'
    run curl -4 -vk --http1.1 --connect-timeout 5 --max-time 15 https://1.1.1.1/ -o /dev/null

    section 'repeated HTTPS probes for intermittent DPI/drop behavior'
    printf 'run,dns,connect,tls,ttfb,total,http_code,remote_ip,exit\n'
    for i in $(seq 1 15); do
        tmp="$(mktemp)"
        if curl -4 --http1.1 --connect-timeout 5 --max-time 15 -o /dev/null -sS \
            -w "${i},%{time_namelookup},%{time_connect},%{time_appconnect},%{time_starttransfer},%{time_total},%{http_code},%{remote_ip}" \
            https://www.google.com/generate_204 >"$tmp" 2>&1; then
            printf '%s,0\n' "$(cat "$tmp")"
        else
            rc=$?
            printf '%s,%s\n' "$(tr '\n' ' ' < "$tmp")" "$rc"
        fi
        rm -f "$tmp"
        sleep 0.5
    done

    section 'path MTU and route diagnostics'
    if command -v tracepath >/dev/null 2>&1; then
        run tracepath -4 -n 1.1.1.1
    fi
    if command -v traceroute >/dev/null 2>&1; then
        run traceroute -4 -n -w 1 -q 1 1.1.1.1
    fi

    section 'network counters'
    run cat /proc/net/dev
    run ip -s link

    section 'relevant sysctls'
    for key in \
        net.ipv4.ip_forward \
        net.ipv4.conf.all.rp_filter \
        net.ipv4.conf.default.rp_filter \
        net.ipv6.conf.all.disable_ipv6 \
        net.ipv6.conf.default.disable_ipv6; do
        run sysctl "$key"
    done

    section 'firewall nftables'
    if command -v nft >/dev/null 2>&1; then
        run sudo -n nft list ruleset
    fi

    section 'iptables compatibility view'
    if command -v iptables >/dev/null 2>&1; then
        run sudo -n iptables -S
        run sudo -n iptables -t nat -S
        run sudo -n iptables -t mangle -S
    fi

    section 'recent Amnezia/XRay/tun2socks journals'
    if command -v journalctl >/dev/null 2>&1; then
        journalctl --since '-15 min' --no-pager 2>&1 | grep -Ei 'amnezia|xray|tun2socks|vless|reality|socks|tun2' || true
    fi

    section 'kernel network warnings'
    if command -v journalctl >/dev/null 2>&1; then
        journalctl -k --since '-15 min' --no-pager 2>&1 | grep -Ei 'tun|net|route|tcp|drop|martian|mtu' || true
    fi

    section 'final sockets after probes'
    if command -v ss >/dev/null 2>&1; then
        run ss -ntup
        run ss -ti
    fi
    if command -v nstat >/dev/null 2>&1; then
        run nstat -az
    fi
} | tee "$outfile"

printf '\nSaved: %s\n' "$outfile" >&2
