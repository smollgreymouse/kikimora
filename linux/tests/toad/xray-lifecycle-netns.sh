#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

: "${TOAD_BIN:?TOAD_BIN must point to the repo-built kikimora-toad binary}"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/netns.sh
source "$SCRIPT_DIR/lib/netns.sh"

require_root
require_commands ip python3
ensure_tun_device

NS="toad-xray-$$"
TMPDIR="$(mktemp -d)"
CONFIG="$TMPDIR/xray.toml"
STATE_FILE="$TMPDIR/state.json"
RUN_LOG="$TMPDIR/run.log"
RUN_PID=""
IFINDEX=""

cleanup() {
    set +e
    if [[ -n "$RUN_PID" ]] && kill -0 "$RUN_PID" 2>/dev/null; then
        kill -INT "$RUN_PID" 2>/dev/null
        wait "$RUN_PID" 2>/dev/null
    fi
    netns_delete_if_present "$NS"
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

fail() {
    echo "ERROR: $*" >&2
    if [[ -s "$RUN_LOG" ]]; then
        echo "--- Toad/Xray log ---" >&2
        cat "$RUN_LOG" >&2
    fi
    if [[ -s "$STATE_FILE" ]]; then
        echo "--- state.json ---" >&2
        cat "$STATE_FILE" >&2
    fi
    dump_namespace "$NS"
    exit 1
}

state_field() {
    local field="$1"
    python3 - "$STATE_FILE" "$field" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        value = json.load(fh)
    for part in sys.argv[2].split("."):
        value = value[part]
except (FileNotFoundError, KeyError, TypeError, json.JSONDecodeError):
    print("")
    raise SystemExit(0)
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("")
else:
    print(value)
PY
}

cat >"$CONFIG" <<EOF
name = "xray-lifecycle"
protocol = "vless-reality"
interface = "kk-xray0"
address = ["10.88.0.2/30"]
mtu = 1380
state_dir = "$TMPDIR"

[vless_reality]
endpoint = "192.0.2.1:443"
uuid = "11111111-1111-4111-8111-111111111111"
server_name = "www.example.org"
public_key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
short_id = "abcd"
flow = "xtls-rprx-vision"
fingerprint = "chrome"
transport = "tcp"
spider_x = "/"
EOF
chmod 0600 "$CONFIG"

ip netns add "$NS"
ip -n "$NS" link set lo up
assert_no_default_route "$NS" || fail "test namespace is not isolated"
assert_no_root_interface kk-xray0 || fail "kk-xray0 exists in root namespace before test"

ip netns exec "$NS" "$TOAD_BIN" run -config "$CONFIG" >"$RUN_LOG" 2>&1 &
RUN_PID=$!

for _ in $(seq 1 100); do
    if ip -n "$NS" link show dev kk-xray0 >/dev/null 2>&1; then
        break
    fi
    assert_process_alive "$RUN_PID" "Toad/Xray" || fail "Toad exited before creating kk-xray0"
    sleep 0.05
done

if ! ip -n "$NS" link show dev kk-xray0 >/dev/null 2>&1; then
    fail "timed out waiting for official Xray TUN"
fi
IFINDEX="$(interface_ifindex "$NS" kk-xray0)" || fail "invalid kk-xray0 ifindex"

LINK_LINE="$(ip -n "$NS" -o link show dev kk-xray0)"
if [[ "$LINK_LINE" != *"mtu 1380"* ]] || [[ "$LINK_LINE" != *"UP"* ]]; then
    fail "unexpected kk-xray0 state: $LINK_LINE"
fi
if ! ip -n "$NS" -o -4 addr show dev kk-xray0 | grep -Fq "10.88.0.2/30"; then
    fail "official Xray TUN is missing configured gateway/address"
fi
assert_no_default_route "$NS" || fail "Xray created a default route"
assert_no_root_interface kk-xray0 || fail "Xray TUN leaked into root namespace"

for _ in $(seq 1 100); do
    if [[ -s "$STATE_FILE" ]]; then
        break
    fi
    assert_process_alive "$RUN_PID" "Toad/Xray" || fail "Toad died before publishing state"
    sleep 0.05
done
[[ -s "$STATE_FILE" ]] || fail "state.json was not published"

if [[ "$(state_field state)" != "connecting" ]]; then
    fail "Xray lifecycle smoke must remain connecting without a real server"
fi
if [[ "$(state_field session.connected)" == "true" ]]; then
    fail "Xray lifecycle smoke falsely reported an online session"
fi
if [[ "$(state_field route_ready)" != "true" ]]; then
    fail "state did not report managed Xray TUN ready"
fi

sleep 1
assert_process_alive "$RUN_PID" "Toad/Xray" || fail "unreachable private endpoint killed Toad"
assert_ifindex "$NS" kk-xray0 "$IFINDEX" "unreachable endpoint" || fail "Xray TUN identity changed"

kill -INT "$RUN_PID"
if ! wait "$RUN_PID"; then
    RUN_PID=""
    fail "Toad/Xray failed during clean shutdown"
fi
RUN_PID=""

for _ in $(seq 1 100); do
    if ! ip -n "$NS" link show dev kk-xray0 >/dev/null 2>&1; then
        break
    fi
    sleep 0.05
done
if ip -n "$NS" link show dev kk-xray0 >/dev/null 2>&1; then
    fail "official Xray TUN remained after final shutdown"
fi
assert_no_root_interface kk-xray0 || fail "Xray TUN leaked to root namespace after shutdown"

echo "Toad Xray lifecycle smoke passed: ifindex=$IFINDEX unreachable endpoint stayed non-fatal"
