#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# selected-routes-netns.sh — 08D §16/§17: privileged disposable-namespace
# acceptance for the managed selected-route path.
#
# Exercises the Go pipeline (explicit registry -> netlink reconciler ->
# registered parking with exact restoration proof -> checkpoint) against
# the real kernel inside a namespace this script creates and cleans up.
#
# Usage:
#   sudo bash linux/tests/toad/selected-routes-netns.sh
#   # or with a prebuilt test binary (cross-compiled on the station):
#   KK_NETNS_TEST_BIN=/path/to/netlink.test sudo bash linux/tests/toad/selected-routes-netns.sh
set -Eeuo pipefail

NS="kk-selected-routes-$$"
DEV="kk-sel-dummy"

if [[ "$(id -u)" -ne 0 ]]; then
    echo "must run as root: sudo $0" >&2
    exit 1
fi
command -v ip >/dev/null 2>&1 || { echo "iproute2 is required" >&2; exit 1; }

cleanup() {
    ip netns del "$NS" >/dev/null 2>&1 || true
}
trap cleanup EXIT

ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add "$DEV" type dummy
ip -n "$NS" address add 203.0.113.254/24 dev "$DEV"
ip -n "$NS" -6 address add 2001:db8:ffff::254/64 dev "$DEV"
ip -n "$NS" link set "$DEV" up

echo "namespace $NS ready (device $DEV)"

cd "$(dirname "$0")/../../.."

BIN="${KK_NETNS_TEST_BIN:-}"
if [[ -z "$BIN" ]]; then
    echo "building the acceptance test binary..."
    if [[ -n "${SUDO_USER:-}" ]]; then
        sudo -u "$SUDO_USER" env \
            HOME="$(getent passwd "$SUDO_USER" | cut -d: -f6)" \
            CARGO_NET_OFFLINE=true GOFLAGS="${GOFLAGS:-}" \
            go test -c ./internal/platform/linux/netlink -o /tmp/kk-selected-netns.test
    else
        go test -c ./internal/platform/linux/netlink -o /tmp/kk-selected-netns.test
    fi
    BIN=/tmp/kk-selected-netns.test
fi
if [[ ! -x "$BIN" ]]; then
    echo "test binary $BIN is not executable" >&2
    exit 1
fi

ip netns exec "$NS" env KK_NETNS_DEV="$DEV" "$BIN" \
    -test.run TestManagedSelectedRoutesNetns -test.v
