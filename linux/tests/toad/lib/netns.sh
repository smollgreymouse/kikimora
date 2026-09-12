#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

# Shared helpers for hermetic Toad protocol tests. Source this file from a test
# script; it intentionally performs no setup on its own.

TOAD_NETNS_POLL_INTERVAL="${TOAD_NETNS_POLL_INTERVAL:-0.05}"

require_root() {
    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        echo "ERROR: isolated network tests require root (run through sudo)" >&2
        return 1
    fi
}

require_commands() {
    local missing=0 cmd
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            echo "ERROR: required command not found: $cmd" >&2
            missing=1
        fi
    done
    return "$missing"
}

netns_exists() {
    local ns="$1"
    ip netns list | grep -q "^${ns}\b"
}

netns_create_pair() {
    local client_ns="$1" server_ns="$2" client_if="$3" server_if="$4"
    local client_addr="$5" server_addr="$6"

    ip netns add "$client_ns"
    ip netns add "$server_ns"
    ip link add "$client_if" type veth peer name "$server_if"
    ip link set "$client_if" netns "$client_ns"
    ip link set "$server_if" netns "$server_ns"
    ip -n "$client_ns" addr add "$client_addr" dev "$client_if"
    ip -n "$server_ns" addr add "$server_addr" dev "$server_if"
    ip -n "$client_ns" link set lo up
    ip -n "$server_ns" link set lo up
    ip -n "$client_ns" link set "$client_if" up
    ip -n "$server_ns" link set "$server_if" up
}

netns_delete_if_present() {
    local ns="$1"
    ip netns delete "$ns" 2>/dev/null || true
}

assert_no_default_route() {
    local ns="$1"
    if ip -n "$ns" -4 route show default | grep -q .; then
        echo "ERROR: $ns unexpectedly has an IPv4 default route" >&2
        return 1
    fi
    if ip -n "$ns" -6 route show default | grep -q .; then
        echo "ERROR: $ns unexpectedly has an IPv6 default route" >&2
        return 1
    fi
}

assert_no_root_interface() {
    local iface="$1"
    if ip link show dev "$iface" >/dev/null 2>&1; then
        echo "ERROR: $iface leaked into the root namespace" >&2
        return 1
    fi
}

interface_ifindex() {
    local ns="$1" iface="$2" line index
    line="$(ip -n "$ns" -o link show dev "$iface")" || return 1
    index="${line%%:*}"
    index="${index//[[:space:]]/}"
    [[ "$index" =~ ^[1-9][0-9]*$ ]] || return 1
    printf '%s\n' "$index"
}

assert_ifindex() {
    local ns="$1" iface="$2" expected="$3" phase="${4:-check}" actual
    actual="$(interface_ifindex "$ns" "$iface")" || {
        echo "ERROR: $iface missing in $ns during $phase" >&2
        return 1
    }
    if [[ "$actual" != "$expected" ]]; then
        echo "ERROR: $iface ifindex changed during $phase: got $actual want $expected" >&2
        return 1
    fi
}

assert_process_alive() {
    local pid="$1" name="$2"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
        echo "ERROR: $name process is not alive" >&2
        return 1
    fi
}

wait_until() {
    local timeout_ms="$1"
    shift
    local elapsed=0 step_ms=50
    while (( elapsed <= timeout_ms )); do
        if "$@"; then
            return 0
        fi
        sleep "$TOAD_NETNS_POLL_INTERVAL"
        elapsed=$((elapsed + step_ms))
    done
    return 1
}

dump_namespace() {
    local ns="$1"
    if ! netns_exists "$ns"; then
        return 0
    fi
    echo "--- namespace: $ns / links ---" >&2
    ip -n "$ns" -s link show >&2 || true
    echo "--- namespace: $ns / addresses ---" >&2
    ip -n "$ns" addr show >&2 || true
    echo "--- namespace: $ns / routes ---" >&2
    ip -n "$ns" route show table all >&2 || true
    echo "--- namespace: $ns / sockets ---" >&2
    ip netns exec "$ns" ss -lntup >&2 || true
}

ensure_tun_device() {
    if [[ ! -c /dev/net/tun ]]; then
        mkdir -p /dev/net
        mknod /dev/net/tun c 10 200
    fi
}
