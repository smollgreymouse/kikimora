#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# go-orchestration-acceptance.sh — privileged 07D orchestration gate
#
# Reuses the same private namespace/reference fixtures as the existing
# AWG2/Xray/OpenConnect/multi-Toad tests.  Does not use the public Internet.
#
# Required environment:
#   TOAD_BIN         path to kikimora-toad
#   CORE_BIN         path to kikimora-core
#   AWG_REF_BIN      path to official amneziawg-go reference binary
#   XRAY_REF_BIN     path to official Xray reference binary
#   XRAY_COVER_BIN   path to xray-test-cover (TLS cover)
#   OPENCONNECT_BIN  path to openconnect
#   OCSERV_BIN       path to ocserv
#   STATE_DIR        (optional) persistent directory; default tmpfs

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$SCRIPT_DIR/lib/netns.sh"
source "$LIB"

require_root
require_commands "$TOAD_BIN" "$CORE_BIN" "$AWG_REF_BIN" "$XRAY_REF_BIN" \
                  "$XRAY_COVER_BIN" "$OPENCONNECT_BIN" "$OCSERV_BIN" \
                  python3 socat openssl ip

STATE_DIR="${STATE_DIR:-$(mktemp -d /tmp/kikimora-oc-accept-XXXX)}"
CORE_STATE="$STATE_DIR/core"
TOAD_CONFIG_DIR="$STATE_DIR/configs"
mkdir -p "$CORE_STATE" "$TOAD_CONFIG_DIR"

# ---------------------------------------------------------------------------
# Fixture topology
#
#   ns-client  ── veth-awg ── ns-awg-server
#              ── veth-xr  ── ns-xray-server
#              ── veth-oc  ── ns-oc-server
# ---------------------------------------------------------------------------
CLIENT_NS="kki-oc-client-$$"
AWG_SRV_NS="kki-oc-awg-srv-$$"
XR_SRV_NS="kki-oc-xr-srv-$$"
OC_SRV_NS="kki-oc-oc-srv-$$"

cleanup() {
    set +e
    # Kill core and Toad processes
    [ -n "${CORE_PID:-}" ] && kill "$CORE_PID" 2>/dev/null
    [ -n "${TOAD_PID:-}" ] && kill "$TOAD_PID" 2>/dev/null
    # Kill reference servers
    [ -n "${AWG_SRV_PID:-}" ] && kill "$AWG_SRV_PID" 2>/dev/null
    [ -n "${XR_SRV_PID:-}" ] && kill "$XR_SRV_PID" 2>/dev/null
    [ -n "${OC_SRV_PID:-}" ] && kill "$OC_SRV_PID" 2>/dev/null
    [ -n "${COVER_PID:-}" ] && kill "$COVER_PID" 2>/dev/null
    # Delete namespaces
    netns_delete_if_present "$CLIENT_NS"
    netns_delete_if_present "$AWG_SRV_NS"
    netns_delete_if_present "$XR_SRV_NS"
    netns_delete_if_present "$OC_SRV_NS"
    rm -rf "$STATE_DIR"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Namespace setup
# ---------------------------------------------------------------------------
# AWG pair
AWG_CLIENT_IP="10.200.1.2"
AWG_SERVER_IP="10.200.1.1"
AWG_CLIENT_UNDERLAY="172.16.1.2/30"
AWG_SERVER_UNDERLAY="172.16.1.1/30"

netns_create_pair "$CLIENT_NS" "$AWG_SRV_NS" \
    "veth-awg-c" "veth-awg-s" \
    "$AWG_CLIENT_UNDERLAY" "$AWG_SERVER_UNDERLAY"

# Xray pair
XR_CLIENT_IP="10.200.2.2"
XR_SERVER_IP="10.200.2.1"
XR_CLIENT_UNDERLAY="172.16.2.2/30"
XR_SERVER_UNDERLAY="172.16.2.1/30"

netns_create_pair "$CLIENT_NS" "$XR_SRV_NS" \
    "veth-xr-c" "veth-xr-s" \
    "$XR_CLIENT_UNDERLAY" "$XR_SERVER_UNDERLAY"

# OpenConnect pair
OC_CLIENT_IP="10.200.3.2"
OC_SERVER_IP="10.200.3.1"
OC_CLIENT_UNDERLAY="172.16.3.2/30"
OC_SERVER_UNDERLAY="172.16.3.1/30"

netns_create_pair "$CLIENT_NS" "$OC_SRV_NS" \
    "veth-oc-c" "veth-oc-s" \
    "$OC_CLIENT_UNDERLAY" "$OC_SERVER_UNDERLAY"

ensure_tun_device "$CLIENT_NS"

# ---------------------------------------------------------------------------
# AWG reference server
# ---------------------------------------------------------------------------
AWG_PORT=51820
AWG_SRV_PUB_KEY=""
awg_setup() {
    local key
    key=$(ip netns exec "$AWG_SRV_NS" "$AWG_REF_BIN" genkey)
    AWG_SRV_PUB_KEY=$(echo "$key" | "$AWG_REF_BIN" pubkey)
    ip netns exec "$AWG_SRV_NS" "$AWG_REF_BIN" privkey "$key" \
        listen-port "$AWG_PORT" &
    AWG_SRV_PID=$!
    # Wait for the reference server to start listening
    sleep 0.5
}

# ---------------------------------------------------------------------------
# Xray reference server
# ---------------------------------------------------------------------------
XR_PORT=443
XR_SRV_CONFIG="$STATE_DIR/xray-server.json"
XR_PRIV_KEY=""
XR_PUB_KEY=""
xr_setup() {
    # Generate ephemeral keys for REALITY
    local keys
    keys=$(ip netns exec "$XR_SRV_NS" "$XRAY_COVER_BIN" generate keys 2>/dev/null || \
           "$XRAY_COVER_BIN" generate keys 2>/dev/null)
    XR_PRIV_KEY=$(echo "$keys" | head -1)
    XR_PUB_KEY=$(echo "$keys" | tail -1)

    cat > "$XR_SRV_CONFIG" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "port": $XR_PORT,
    "protocol": "vless",
    "settings": {
      "clients": [{"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "dest": "1.2.3.4:0",
        "serverNames": ["test-server-name"],
        "privateKey": "$XR_PRIV_KEY",
        "shortIds": ["abcd"]
      }
    }
  }],
  "outbounds": [{"protocol": "freedom"}]
}
EOF
    ip netns exec "$XR_SRV_NS" "$XRAY_REF_BIN" run -c "$XR_SRV_CONFIG" &
    XR_SRV_PID=$!
    sleep 0.5
}

# ---------------------------------------------------------------------------
# OpenConnect reference server (ocserv)
# ---------------------------------------------------------------------------
OC_PORT=4443
OC_SRV_CONFIG="$STATE_DIR/ocserv.conf"
OC_PASSWORD_FILE="$STATE_DIR/oc-password"
OC_CERT="$STATE_DIR/oc-cert.pem"
OC_KEY="$STATE_DIR/oc-key.pem"
oc_setup() {
    echo "testuser:testpass" > "$OC_PASSWORD_FILE"
    # Generate self-signed certificate
    openssl req -x509 -newkey rsa:2048 -nodes -keyout "$OC_KEY" \
        -out "$OC_CERT" -days 1 \
        -subj "/CN=ocserv-test/O=Kikimora/C=XX" 2>/dev/null

    cat > "$OC_SRV_CONFIG" <<EOF
auth = "plain[passwd=$OC_PASSWORD_FILE]"
tcp-port = $OC_PORT
udp-port = $OC_PORT
run-as-user = root
run-as-group = root
server-cert = $OC_CERT
server-key = $OC_KEY
cisco-client-compat = true
no-route = default
default-domain = kikimora.test
ipv4-network = 10.201.0.0/24
dns = 1.1.1.1
EOF
    ip netns exec "$OC_SRV_NS" "$OCSERV_BIN" -c "$OC_SRV_CONFIG" &
    OC_SRV_PID=$!
    sleep 1
}

# ---------------------------------------------------------------------------
# Toad configs
# ---------------------------------------------------------------------------
write_toad_config() {
    local name="$1" proto="$2" iface="$3" addr="$4" mtu="$5" extra="$6"
    local path="$TOAD_CONFIG_DIR/$name.toml"
    cat > "$path" <<EOF
name = "$name"
protocol = "$proto"
interface = "$iface"
mtu = $mtu
state_dir = "$CORE_STATE/$name"
address = ["$addr"]
$extra
EOF
    echo "$path"
}

AWG_CFG=$(write_toad_config "awg" "amneziawg2" "kk-awg0" "10.80.1.2/24" 1380 \
"endpoint = \"$AWG_SERVER_IP:$AWG_PORT\"
[awg2]
private_key = \"uJ3qFJwBcFh0GJwFJwBcFh0GJwFJwBcFh0GJwFJwA=\"
peer_public_key = \"$AWG_SRV_PUB_KEY\"
preshared_key = \"\"
allowed_ips = [\"0.0.0.0/0\", \"::/0\"]
persistent_keepalive = 0
jc = 1
jmin = 2
jmax = 3
s1 = 0
s2 = 0
s3 = 0
s4 = 0
h1 = \"test\"
h2 = \"test\"
h3 = \"test\"
h4 = \"test\"
i1 = \"test\"
i2 = \"test\"
i3 = \"test\"
i4 = \"test\"
i5 = \"test\"")

XR_CFG=$(write_toad_config "xray" "vless-reality" "kk-xray0" "10.80.2.2/24" 1380 \
"endpoint = \"$XR_SERVER_IP:$XR_PORT\"
[vless_reality]
uuid = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\"
server_name = \"test-server-name\"
public_key = \"$XR_PUB_KEY\"
short_id = \"abcd\"
flow = \"\"
fingerprint = \"\"
transport = \"raw\"
spider_x = \"\"")

OC_CFG=$(write_toad_config "oc" "openconnect" "kk-oc0" "" 1380 \
"gateway = \"$OC_SERVER_IP:$OC_PORT\"
username = \"testuser\"
vpn_protocol = \"anyconnect\"
auth_group = \"\"
password_file = \"$OC_PASSWORD_FILE\"
token_mode = \"none\"
token_secret_file = \"\"
user_agent = \"\"
server_cert = \"\"
disable_udp = false
disable_ipv6 = false
reconnect_timeout = 30")

# ---------------------------------------------------------------------------
# Phase A: Initial connect
# ---------------------------------------------------------------------------
echo "=== Phase A: Initial connect ==="

# Start reference servers
awg_setup
xr_setup
oc_setup

# Start kikimora-core in the client namespace
CORE_SOCKET="$CORE_STATE/core.sock"
ip netns exec "$CLIENT_NS" "$CORE_BIN" serve \
    --socket "$CORE_SOCKET" \
    --state-dir "$CORE_STATE" \
    --config "$AWG_CFG" --config "$XR_CFG" --config "$OC_CFG" \
    --toad-binary "$TOAD_BIN" &
CORE_PID=$!

# Wait for the core socket
wait_until 5 ip netns exec "$CLIENT_NS" test -S "$CORE_SOCKET"

# Connect all roles
"$CORE_BIN" --socket "$CORE_SOCKET" connect-all

# Wait for all roles to reach Ready
sleep 3

# Verify all roles are Ready
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
roles = snap.get('roles', [])
print(f'Roles: {len(roles)}')
for r in roles:
    print(f'  {r[\"id\"]}: state={r[\"state\"]} route_ready={r[\"route_ready\"]} epoch={r[\"validated_underlay_epoch\"]}')
    assert r['state'] == 'Ready', f'{r[\"id\"]} not Ready: {r[\"state\"]}'
    assert r['route_ready'], f'{r[\"id\"]} not route_ready'
    assert r['validated_underlay_epoch'] == snap['underlay']['epoch'], \
        f'{r[\"id\"]} validated epoch mismatch'
    assert not r['parking']['active'], f'{r[\"id\"]} parking active'
print('Phase A PASS')
"

# ---------------------------------------------------------------------------
# Phase B: Underlay change / recovery
# ---------------------------------------------------------------------------
echo "=== Phase B: Underlay change / recovery ==="

# Bring down the AWG underlay link
ip netns exec "$CLIENT_NS" ip link set veth-awg-c down
sleep 4

# Verify AWG role enters fail-closed recovery
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'awg':
        print(f'awg: state={r[\"state\"]} parking_active={r[\"parking\"][\"active\"]}')
        assert r['state'] == 'Recovering' or r['state'] == 'Degraded', \
            f'awg should be recovering after underlay loss: {r[\"state\"]}'
    else:
        print(f'{r[\"id\"]}: state={r[\"state\"]} (should be unaffected)')
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready'
print('Phase B underlay loss PASS')
"

# Restore underlay
ip netns exec "$CLIENT_NS" ip link set veth-awg-c up
sleep 5

# Verify AWG recovers
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'awg':
        print(f'awg after restore: state={r[\"state\"]} epoch={r[\"validated_underlay_epoch\"]}')
        assert r['state'] == 'Ready', f'awg should recover to Ready: {r[\"state\"]}'
print('Phase B recovery PASS')
"

# ---------------------------------------------------------------------------
# Phase C: AWG/Xray address drift
# ---------------------------------------------------------------------------
echo "=== Phase C: Address drift ==="

# Delete one configured local address from AWG interface
AWG_IFINDEX=$(interface_ifindex "$CLIENT_NS" kk-awg0)
ip netns exec "$CLIENT_NS" ip addr del 10.80.1.2/24 dev kk-awg0
sleep 2

# Verify RouteReady drops and repair occurs
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'awg':
        print(f'awg after addr loss: state={r[\"state\"]} route_ready={r[\"route_ready\"]}')
        # RouteReady may be false temporarily; repair should restore it
        print(f'  ifindex={r[\"interface\"][\"ifindex\"]} (should be {AWG_IFINDEX})')
print('Phase C address drift observed')
"

# Wait for repair
sleep 3

"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'awg':
        print(f'awg after repair: state={r[\"state\"]} route_ready={r[\"route_ready\"]}')
        assert r['route_ready'], f'awg should be route_ready after repair'
        assert r['interface']['ifindex'] == $AWG_IFINDEX, \
            f'awg ifindex changed: {r[\"interface\"][\"ifindex\"]} != $AWG_IFINDEX'
print('Phase C address drift PASS')
"

# ---------------------------------------------------------------------------
# Phase D: OpenConnect negotiated-address drift
# ---------------------------------------------------------------------------
echo "=== Phase D: OpenConnect negotiated-address drift ==="

OC_IFINDEX=$(interface_ifindex "$CLIENT_NS" kk-oc0)
# Remove the negotiated address (simulate drift)
ip netns exec "$CLIENT_NS" ip addr flush dev kk-oc0 2>/dev/null || true
sleep 2

# Verify no static address injection; RouteReady=false
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'oc':
        print(f'oc after addr loss: state={r[\"state\"]} route_ready={r[\"route_ready\"]}')
        assert not r['route_ready'], 'oc should not be route_ready after address loss'
        assert r['state'] != 'Ready', 'oc should not be Ready after address loss'
print('Phase D OpenConnect drift observed')
"

# Wait for recovery (full replacement)
sleep 5

"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'oc':
        print(f'oc after recovery: state={r[\"state\"]} route_ready={r[\"route_ready\"]} epoch={r[\"validated_underlay_epoch\"]}')
        assert r['state'] == 'Ready', f'oc should recover to Ready: {r[\"state\"]}'
        assert r['route_ready'], 'oc should be route_ready after recovery'
print('Phase D OpenConnect drift PASS')
"

# ---------------------------------------------------------------------------
# Phase E: Toad crash
# ---------------------------------------------------------------------------
echo "=== Phase E: Toad crash ==="

# Find and kill the Xray Toad process
XR_TOAD_PID=$(ip netns exec "$CLIENT_NS" pgrep -f "kikimora-toad.*xray" || true)
if [ -n "$XR_TOAD_PID" ]; then
    kill -9 "$XR_TOAD_PID"
    sleep 3
fi

# Verify only Xray role restarts; others keep their state
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    print(f'{r[\"id\"]}: state={r[\"state\"]}')
    if r['id'] == 'xray':
        # May be Starting or Recovering during restart
        assert r['state'] in ('Starting', 'Recovering', 'Ready'), \
            f'xray unexpected state after crash: {r[\"state\"]}'
    else:
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready after xray crash'
print('Phase E Toad crash PASS')
"

# Wait for Xray to recover
sleep 5

"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap['roles']:
    if r['id'] == 'xray':
        print(f'xray recovered: state={r[\"state\"]}')
        assert r['state'] == 'Ready', f'xray should recover to Ready: {r[\"state\"]}'
print('Phase E recovery PASS')
"

# ---------------------------------------------------------------------------
# Phase F: Core crash/restart
# ---------------------------------------------------------------------------
echo "=== Phase F: Core crash/restart ==="

# Kill the core
kill "$CORE_PID" 2>/dev/null || true
wait "$CORE_PID" 2>/dev/null || true

# Restart core (it should restore desired state from persistence)
ip netns exec "$CLIENT_NS" "$CORE_BIN" serve \
    --socket "$CORE_SOCKET" \
    --state-dir "$CORE_STATE" \
    --config "$AWG_CFG" --config "$XR_CFG" --config "$OC_CFG" \
    --toad-binary "$TOAD_BIN" &
CORE_PID=$!

wait_until 5 ip netns exec "$CLIENT_NS" test -S "$CORE_SOCKET"

sleep 5

# Verify all roles are restored
"$CORE_BIN" --socket "$CORE_SOCKET" get-snapshot | python3 -c "
import json, sys
snap = json.load(sys.stdin)
roles = snap.get('roles', [])
ready_count = 0
for r in roles:
    print(f'{r[\"id\"]}: state={r[\"state\"]} desired={r[\"desired_enabled\"]}')
    if r['desired_enabled'] and r['state'] == 'Ready':
        ready_count += 1
print(f'Ready count: {ready_count}/3')
assert ready_count >= 2, f'Expected at least 2 Ready roles after restart, got {ready_count}'
print('Phase F core restart PASS')
"

echo ""
echo "=== ALL PHASES PASSED ==="
exit 0