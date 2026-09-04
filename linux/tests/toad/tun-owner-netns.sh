#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

: "${TOAD_TUN_HELPER:?TOAD_TUN_HELPER must point to the repo-built Linux TUN test helper}"
if [[ ! -x "$TOAD_TUN_HELPER" ]]; then
    echo "ERROR: TOAD_TUN_HELPER is not executable: $TOAD_TUN_HELPER" >&2
    exit 1
fi

NSNAME="toad-tun-owner-$$"
TMPDIR="$(mktemp -d)"
READY_FILE="$TMPDIR/ready"
RELEASE_FILE="$TMPDIR/release"
HELPER_LOG="$TMPDIR/helper.log"
HELPER_PID=""

cleanup() {
    if [[ -n "$HELPER_PID" ]] && kill -0 "$HELPER_PID" 2>/dev/null; then
        kill "$HELPER_PID" 2>/dev/null || true
        wait "$HELPER_PID" 2>/dev/null || true
    fi
    ip netns delete "$NSNAME" 2>/dev/null || true
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

fail() {
    echo "ERROR: $*" >&2
    if [[ -s "$HELPER_LOG" ]]; then
        echo "--- helper log ---" >&2
        cat "$HELPER_LOG" >&2
        echo "------------------" >&2
    fi
    exit 1
}

ip netns add "$NSNAME"

if ip -n "$NSNAME" -4 route show default | grep -q .; then
    fail "test namespace unexpectedly has an IPv4 default route"
fi
if ip -n "$NSNAME" -6 route show default | grep -q .; then
    fail "test namespace unexpectedly has an IPv6 default route"
fi
if ip link show dev kk-toad0 >/dev/null 2>&1; then
    fail "kk-toad0 unexpectedly exists in the root namespace before the test"
fi

ip netns exec "$NSNAME" "$TOAD_TUN_HELPER" "$READY_FILE" "$RELEASE_FILE" >"$HELPER_LOG" 2>&1 &
HELPER_PID=$!

for _ in $(seq 1 200); do
    if [[ -s "$READY_FILE" ]]; then
        break
    fi
    if ! kill -0 "$HELPER_PID" 2>/dev/null; then
        wait "$HELPER_PID" || true
        HELPER_PID=""
        fail "TUN helper exited before publishing READY"
    fi
    sleep 0.05
done

if [[ ! -s "$READY_FILE" ]]; then
    fail "timed out waiting for TUN helper READY"
fi

EXPECTED_IFINDEX="$(tr -d '[:space:]' <"$READY_FILE")"
if [[ ! "$EXPECTED_IFINDEX" =~ ^[1-9][0-9]*$ ]]; then
    fail "helper published invalid ifindex: $EXPECTED_IFINDEX"
fi

LINK_LINE="$(ip -n "$NSNAME" -o link show dev kk-toad0)"
ACTUAL_IFINDEX="${LINK_LINE%%:*}"
ACTUAL_IFINDEX="${ACTUAL_IFINDEX//[[:space:]]/}"
if [[ "$ACTUAL_IFINDEX" != "$EXPECTED_IFINDEX" ]]; then
    fail "ifindex mismatch after duplicate close: got $ACTUAL_IFINDEX want $EXPECTED_IFINDEX"
fi
if [[ "$LINK_LINE" != *"mtu 1380"* ]]; then
    fail "kk-toad0 MTU is not 1380: $LINK_LINE"
fi
if [[ "$LINK_LINE" != *"UP"* ]]; then
    fail "kk-toad0 is not UP: $LINK_LINE"
fi
if ! ip -n "$NSNAME" -o -4 addr show dev kk-toad0 | grep -Fq "10.77.0.2/30"; then
    fail "kk-toad0 is missing 10.77.0.2/30"
fi
if ip link show dev kk-toad0 >/dev/null 2>&1; then
    fail "kk-toad0 leaked into the root namespace while owner fd is alive"
fi
if ip -n "$NSNAME" -4 route show default | grep -q .; then
    fail "test created an IPv4 default route"
fi
if ip -n "$NSNAME" -6 route show default | grep -q .; then
    fail "test created an IPv6 default route"
fi

touch "$RELEASE_FILE"
if ! wait "$HELPER_PID"; then
    HELPER_PID=""
    fail "TUN helper failed after RELEASE"
fi
HELPER_PID=""

if ip -n "$NSNAME" link show dev kk-toad0 >/dev/null 2>&1; then
    fail "kk-toad0 still exists after closing the final owner fd"
fi
if ip link show dev kk-toad0 >/dev/null 2>&1; then
    fail "kk-toad0 exists in the root namespace after helper exit"
fi

echo "Toad Linux TUN ownership test passed: ifindex=$EXPECTED_IFINDEX mtu=1380 address=10.77.0.2/30"
