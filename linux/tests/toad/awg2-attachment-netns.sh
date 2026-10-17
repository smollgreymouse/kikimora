#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

: "${TOAD_AWG2_HELPER:?TOAD_AWG2_HELPER must point to the repo-built AWG2 helper}"
: "${TOAD_BIN:?TOAD_BIN must point to the repo-built kikimora-toad binary}"

NSNAME="toad-awg2-$$"
TMPDIR="$(mktemp -d)"
HELPER_LOG="$TMPDIR/helper.log"
RUN_LOG="$TMPDIR/run.log"
CONFIG="$TMPDIR/awg2.toml"
RUN_PID=""

cleanup() {
    if [[ -n "$RUN_PID" ]] && kill -0 "$RUN_PID" 2>/dev/null; then
        kill -INT "$RUN_PID" 2>/dev/null || true
        wait "$RUN_PID" 2>/dev/null || true
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
    fi
    if [[ -s "$RUN_LOG" ]]; then
        echo "--- run log ---" >&2
        cat "$RUN_LOG" >&2
    fi
    exit 1
}

cat >"$CONFIG" <<EOF
name = "awg2-smoke"
protocol = "amneziawg2"
interface = "kk-awg0"
address = ["10.77.0.2/30"]
mtu = 1380
state_dir = "$TMPDIR"

[awg2]
private_key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
peer_public_key = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
endpoint = "192.0.2.1:51820"
allowed_ips = ["10.78.0.0/24"]
persistent_keepalive = 1
EOF

ip netns add "$NSNAME"

if ip -n "$NSNAME" -4 route show default | grep -q .; then
    fail "test namespace unexpectedly has an IPv4 default route"
fi
if ip -n "$NSNAME" -6 route show default | grep -q .; then
    fail "test namespace unexpectedly has an IPv6 default route"
fi
if ip link show dev kk-awg0 >/dev/null 2>&1; then
    fail "kk-awg0 unexpectedly exists in the root namespace"
fi

if ! ip netns exec "$NSNAME" "$TOAD_AWG2_HELPER" >"$HELPER_LOG" 2>&1; then
    fail "official AWG2 lifecycle helper failed"
fi
if ip -n "$NSNAME" link show dev kk-awg0 >/dev/null 2>&1; then
    fail "helper leaked kk-awg0 after final owner close"
fi

ip netns exec "$NSNAME" "$TOAD_BIN" run -config "$CONFIG" >"$RUN_LOG" 2>&1 &
RUN_PID=$!

for _ in $(seq 1 200); do
    if ip -n "$NSNAME" link show dev kk-awg0 >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$RUN_PID" 2>/dev/null; then
        wait "$RUN_PID" || true
        RUN_PID=""
        fail "kikimora-toad run exited before creating kk-awg0"
    fi
    sleep 0.05
done

if ! ip -n "$NSNAME" link show dev kk-awg0 >/dev/null 2>&1; then
    fail "timed out waiting for kk-awg0"
fi

LINK_LINE="$(ip -n "$NSNAME" -o link show dev kk-awg0)"
EXPECTED_IFINDEX="${LINK_LINE%%:*}"
EXPECTED_IFINDEX="${EXPECTED_IFINDEX//[[:space:]]/}"
if [[ ! "$EXPECTED_IFINDEX" =~ ^[1-9][0-9]*$ ]]; then
    fail "invalid kk-awg0 ifindex: $EXPECTED_IFINDEX"
fi
if [[ "$LINK_LINE" != *"mtu 1380"* ]] || [[ "$LINK_LINE" != *"UP"* ]]; then
    fail "unexpected kk-awg0 link state: $LINK_LINE"
fi
if ! ip -n "$NSNAME" -o -4 addr show dev kk-awg0 | grep -Fq "10.77.0.2/30"; then
    fail "kk-awg0 is missing 10.77.0.2/30"
fi
if ip link show dev kk-awg0 >/dev/null 2>&1; then
    fail "kk-awg0 leaked into the root namespace"
fi
if ip -n "$NSNAME" -4 route show default | grep -q . || ip -n "$NSNAME" -6 route show default | grep -q .; then
    fail "AWG2 smoke created a default route"
fi

sleep 1
if ! kill -0 "$RUN_PID" 2>/dev/null; then
    wait "$RUN_PID" || true
    RUN_PID=""
    fail "kikimora-toad run died solely because no peer answered"
fi
CURRENT_LINE="$(ip -n "$NSNAME" -o link show dev kk-awg0)"
CURRENT_IFINDEX="${CURRENT_LINE%%:*}"
CURRENT_IFINDEX="${CURRENT_IFINDEX//[[:space:]]/}"
if [[ "$CURRENT_IFINDEX" != "$EXPECTED_IFINDEX" ]]; then
    fail "kk-awg0 ifindex changed without owner shutdown: got $CURRENT_IFINDEX want $EXPECTED_IFINDEX"
fi

kill -INT "$RUN_PID"
if ! wait "$RUN_PID"; then
    RUN_PID=""
    fail "kikimora-toad run failed during clean interrupt shutdown"
fi
RUN_PID=""

if ip -n "$NSNAME" link show dev kk-awg0 >/dev/null 2>&1; then
    fail "kk-awg0 remains after final Toad owner shutdown"
fi

echo "Toad AWG2 attachment smoke passed: ifindex=$EXPECTED_IFINDEX no-peer process stayed alive"
