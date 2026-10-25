#!/usr/bin/env bash
set -Eeuo pipefail

ns_pids() {
    local ns="$1"
    sudo ip netns pids "$ns" 2>/dev/null || true
}

kill_ns_processes() {
    local ns="$1" pid
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        sudo kill -TERM "$pid" 2>/dev/null || true
    done < <(ns_pids "$ns")
    sleep 0.1
    while read -r pid; do
        [[ -n "$pid" ]] || continue
        sudo kill -KILL "$pid" 2>/dev/null || true
    done < <(ns_pids "$ns")
}

remove_netns() {
    local ns="$1"
    kill_ns_processes "$ns"
    sudo ip netns del "$ns" 2>/dev/null || true
}

assert_host_interface_absent() {
    local iface="$1"
    if ip link show dev "$iface" >/dev/null 2>&1; then
        printf 'unsafe test leak: interface %s exists in root namespace\n' "$iface" >&2
        return 1
    fi
}

assert_no_default_route() {
    local ns="$1"
    if sudo ip netns exec "$ns" ip route show default | grep -q .; then
        printf 'unsafe test namespace: %s has a default route\n' "$ns" >&2
        return 1
    fi
    if sudo ip netns exec "$ns" ip -6 route show default | grep -q .; then
        printf 'unsafe test namespace: %s has an IPv6 default route\n' "$ns" >&2
        return 1
    fi
}

wait_for_file() {
    local path="$1" attempts="${2:-100}"
    local i
    for ((i=0; i<attempts; i++)); do
        [[ -s "$path" ]] && return 0
        sleep 0.05
    done
    printf 'timed out waiting for file: %s\n' "$path" >&2
    return 1
}

wait_for_ns_interface() {
    local ns="$1" iface="$2" attempts="${3:-100}"
    local i
    for ((i=0; i<attempts; i++)); do
        sudo ip netns exec "$ns" ip link show dev "$iface" >/dev/null 2>&1 && return 0
        sleep 0.05
    done
    printf 'timed out waiting for %s in namespace %s\n' "$iface" "$ns" >&2
    return 1
}
