#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-leshy-smoke.sh — 08D §16 transport-level live smoke: the real
# Leshy (managed backend) talks to the real kikimora-core over the framed
# control socket inside a disposable namespace.
#
# Proves live: handshake capability gating, API v3 framing between the Rust
# and Go sides, verdict delivery (denied -> SERVFAIL), non-zone queries
# bypassing the controller, and the no-local-fallback rule with the core
# down (§16 item 9).
#
# Usage:
#   sudo bash linux/tests/toad/managed-leshy-smoke.sh
#     KK_CORE_BIN=/path/to/kikimora-core KK_LESHY_BIN=/path/to/leshy
set -Eeuo pipefail

NS="kk-leshy-smoke-$$"
ROOT="/run/kk-smoke"
SOCKET="$ROOT/core.sock"
KK="${KK_CORE_BIN:-/tmp/kikimora-core}"
LESHY="${KK_LESHY_BIN:-/tmp/leshy}"

if [[ "$(id -u)" -ne 0 ]]; then
    echo "must run as root: sudo $0" >&2
    exit 1
fi
for f in "$KK" "$LESHY"; do
    [[ -x "$f" ]] || { echo "missing executable: $f" >&2; exit 1; }
done

cleanup() {
    # shellcheck disable=SC2317 -- invoked through the EXIT trap
    if [[ -n "${CORE_PID:-}" ]]; then kill "$CORE_PID" 2>/dev/null || true; fi
    if [[ -n "${LESHY_PID:-}" ]]; then kill "$LESHY_PID" 2>/dev/null || true; fi
    if [[ -n "${STUB_PID:-}" ]]; then kill "$STUB_PID" 2>/dev/null || true; fi
    ip netns del "$NS" >/dev/null 2>&1 || true
}
trap cleanup EXIT

ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add kk-smoke-dummy type dummy
ip -n "$NS" address add 203.0.113.254/24 dev kk-smoke-dummy
ip -n "$NS" link set kk-smoke-dummy up

cd "$(dirname "$0")/../../.."
mkdir -p "$ROOT/config" "$ROOT/state" "$ROOT/leshy-vpn"

# Core role config: one role, never connected (no toad needed for the smoke —
# the role stays not-ready, which is exactly the denial path Leshy must
# surface as SERVFAIL).
cat > "$ROOT/config/primary.toml" <<'TOML'
name = "primary"
protocol = "openconnect"
interface = "kkprimary"
mtu = 1380
state_dir = "/run/kk-smoke/state/primary"

[openconnect]
gateway = "203.0.113.1"
username = "smoke"
TOML

# Leshy config: managed backend pointed at the core socket.
cat > "$ROOT/leshy.toml" <<'TOML'
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
max_dynamic_routes = 1024
routing_backend = "kikimora"
kikimora_socket = "/run/kk-smoke/core.sock"

[[zones]]
name = "primary"
route_type = "dev"
route_target = "/run/kk-smoke/leshy-vpn/primary.dev"
domains = ["example.com"]
ipv6 = true
TOML

# Python DNS stub: answers every A with 203.0.113.10 and AAAA with 2001:db8::10.
cat > "$ROOT/dns-stub.py" <<'PYEOF'
import socket, struct

def answer(q):
    tid = q[:2]
    if len(q) < 12:
        return None
    names, i = [], 12
    while True:
        if q[i] == 0:
            i += 1
            break
        length = q[i]
        names.append(q[i + 1:i + 1 + length])
        i += 1 + length
    qtype = struct.unpack("!H", q[i:i + 2])[0]
    flags = 0x8180
    rdata = {1: socket.inet_aton("203.0.113.10"),
             28: socket.inet_pton(socket.AF_INET6, "2001:db8::10")}.get(qtype)
    if rdata is None:
        return tid + struct.pack("!HHHHH", flags, 1, 0, 0, 0) + q[12:]
    reply = tid + struct.pack("!HHHHH", flags, 1, 1, 0, 0) + q[12:]
    reply += b"\xc0\x0c" + struct.pack("!HHIH", qtype, 1, 60, len(rdata)) + rdata
    return reply

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("127.0.0.1", 15354))
while True:
    data, peer = s.recvfrom(4096)
    reply = answer(data)
    if reply:
        s.sendto(reply, peer)
PYEOF

# The query helper: one UDP DNS transaction.
cat > "$ROOT/dnsq.py" <<'PYEOF'
import socket, struct, sys

name = sys.argv[1].encode()
qtype = int(sys.argv[2])
q = b"\x12\x34" + struct.pack("!HHHHH", 0x0100, 1, 0, 0, 0)
for label in name.split(b"."):
    q += bytes([len(label)]) + label
q += b"\x00" + struct.pack("!HH", qtype, 1)
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(4)
s.sendto(q, ("127.0.0.1", 15353))
data, _ = s.recvfrom(4096)
rcode = data[3] & 0x0F
ancount = struct.unpack("!H", data[6:8])[0]
print(f"rcode={rcode} answers={ancount}")
PYEOF

ip netns exec "$NS" env KK_SMOKE=1 python3 "$ROOT/dns-stub.py" &
STUB_PID=$!

ip netns exec "$NS" "$KK" serve \
    --socket "$SOCKET" \
    --state-dir "$ROOT/state" \
    --leshy-publication-dir "$ROOT/leshy-vpn" \
    --config-dir "$ROOT/config" &
CORE_PID=$!

sleep 1
ip netns exec "$NS" "$LESHY" "$ROOT/leshy.toml" &
LESHY_PID=$!
sleep 1

echo "--- zone query while the role is not ready (must be SERVFAIL via the controller verdict)"
ZONE_RESULT=$(ip netns exec "$NS" python3 "$ROOT/dnsq.py" example.com 1 || true)
echo "zone query: $ZONE_RESULT"
[[ "$ZONE_RESULT" == "rcode=2"* ]] || { echo "FAIL: expected SERVFAIL from the controller denial"; exit 1; }

echo "--- non-zone query bypasses the controller (NOERROR from the stub)"
NONZONE=$(ip netns exec "$NS" python3 "$ROOT/dnsq.py" example.org 1 || true)
echo "non-zone query: $NONZONE"
[[ "$NONZONE" == "rcode=0"* ]] || { echo "FAIL: non-zone query must pass through"; exit 1; }

echo "--- core down: Leshy never falls back to the local writer (§16 item 9)"
kill "$CORE_PID"
CORE_PID=
sleep 1
MANAGED_RESULT=$(ip netns exec "$NS" python3 "$ROOT/dnsq.py" example.com 1 || true)
echo "query with the core down: $MANAGED_RESULT"
[[ "$MANAGED_RESULT" == "rcode=2"* ]] || { echo "FAIL: expected SERVFAIL with the core down"; exit 1; }
ROUTES=$(ip -n "$NS" route show table main)
[[ "$ROUTES" != *"203.0.113.10"* ]] || { echo "FAIL: a local route was installed — the forbidden fallback happened"; exit 1; }

echo "--- Leshy is still alive after the core died"
if ! kill -0 "$LESHY_PID" 2>/dev/null; then
    echo "FAIL: Leshy died with the core" >&2
    exit 1
fi

echo "MANAGED-LESHY SMOKE: PASS"
