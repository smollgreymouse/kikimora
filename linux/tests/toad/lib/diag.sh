#!/usr/bin/env bash
set -euo pipefail

# Shared collector for real VPS tests.
# Collects operational state only. Callers must provide output directory.

collect_diag() {
    local out="$1"
    mkdir -p "$out"

    {
        echo "time=$(date -Is)"
        echo "host=$(hostname)"
        echo "kernel=$(uname -a)"
    } > "$out/metadata.txt"

    ip addr > "$out/interfaces.txt" 2>&1 || true
    ip route > "$out/routes.txt" 2>&1 || true
    ip rule > "$out/rules.txt" 2>&1 || true
    ip -6 route > "$out/routes-ipv6.txt" 2>&1 || true
    ss -tunap > "$out/sockets.txt" 2>&1 || true
    ps aux > "$out/processes.txt" 2>&1 || true
    resolvectl status > "$out/dns.txt" 2>&1 || true
    journalctl -n 300 > "$out/journal.txt" 2>&1 || true
}
