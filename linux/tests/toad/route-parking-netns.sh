#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -Eeuo pipefail

: "${PARKING_HELPER:?PARKING_HELPER must point to the checked-out Go parking helper}"

if [[ "${EUID}" -ne 0 ]]; then
    echo "ERROR: Go route parking netns gate requires root" >&2
    exit 1
fi
[[ -x "$PARKING_HELPER" ]] || {
    echo "ERROR: parking helper is not executable: $PARKING_HELPER" >&2
    exit 1
}
command -v ip >/dev/null 2>&1 || {
    echo "ERROR: ip command is required" >&2
    exit 1
}

NS="toad-go-parking-$$"
cleanup() {
    ip netns del "$NS" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add uplink0 type dummy
ip -n "$NS" link add vpn0 type dummy
ip -n "$NS" link set uplink0 up
ip -n "$NS" link set vpn0 up

# A physical default exists for both families. If the host park disappears,
# route-get will visibly fall through to uplink0.
ip -n "$NS" addr add 192.0.2.2/24 dev uplink0
ip -n "$NS" route add default dev uplink0 metric 600
ip -n "$NS" -6 addr add 2001:db8:ffff::2/64 dev uplink0
ip -n "$NS" -6 route add default dev uplink0 metric 600

ip -n "$NS" addr add 10.77.0.2/24 dev vpn0
ip -n "$NS" -6 addr add fd00:77::2/64 dev vpn0

VPN_IFINDEX="$(ip netns exec "$NS" cat /sys/class/net/vpn0/ifindex)"
[[ "$VPN_IFINDEX" =~ ^[0-9]+$ ]] || {
    echo "ERROR: cannot read vpn0 ifindex" >&2
    exit 1
}

V4="203.0.113.10/32"
V6="2001:db8:100::10/128"
ip -n "$NS" route add "$V4" dev vpn0 proto static metric 10
ip -n "$NS" -6 route add "$V6" dev vpn0 proto static metric 10

ip -n "$NS" -4 route get 203.0.113.10 | grep -Fq 'dev vpn0' || {
    echo "ERROR: IPv4 selected route did not initially use vpn0" >&2
    exit 1
}
ip -n "$NS" -6 route get 2001:db8:100::10 | grep -Fq 'dev vpn0' || {
    echo "ERROR: IPv6 selected route did not initially use vpn0" >&2
    exit 1
}

ip netns exec "$NS" "$PARKING_HELPER" -role primary -prefix "$V4" -ifindex "$VPN_IFINDEX"
ip netns exec "$NS" "$PARKING_HELPER" -role secondary -prefix "$V6" -ifindex "$VPN_IFINDEX"

ip -n "$NS" -4 route get 203.0.113.10 | grep -Fq 'dev vpn0' || {
    echo "ERROR: IPv4 route was not restored after parking cycle" >&2
    exit 1
}
ip -n "$NS" -6 route get 2001:db8:100::10 | grep -Fq 'dev vpn0' || {
    echo "ERROR: IPv6 route was not restored after parking cycle" >&2
    exit 1
}

if ip -n "$NS" -4 route show "$V4" | grep -q '^unreachable '; then
    echo "ERROR: IPv4 park remained after restoration" >&2
    exit 1
fi
if ip -n "$NS" -6 route show "$V6" | grep -q '^unreachable '; then
    echo "ERROR: IPv6 park remained after restoration" >&2
    exit 1
fi

echo "Go route parking netns gate passed: IPv4 /32 and IPv6 /128 fail closed and restore"
