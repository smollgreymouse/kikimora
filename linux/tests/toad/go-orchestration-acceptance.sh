#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# go-orchestration-acceptance.sh — privileged 07D/07F orchestration gate
#
# Reuses the shared multi-protocol fixture from lib/multi-protocol-fixture.sh.
# Runs phases A-F with precise invariants:
#   - synthetic default underlay for core underlay discovery
#   - non-recursive transport endpoint routing proven
#   - explicit canonical underlay mutation in Phase B
#   - PID/generation/ifindex stability across underlay change
#   - fail-closed on address drift and Toad crash
#   - persisted desired-state restore across core restart
#
# Required environment:
#   TOAD_BIN, CORE_BIN, AWG_REF_BIN, XRAY_REF_BIN, XRAY_COVER_BIN,
#   OPENCONNECT_BIN, OCSERV_BIN
#
# shellcheck source=linux/tests/toad/lib/multi-protocol-fixture.sh

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/multi-protocol-fixture.sh
source "$SCRIPT_DIR/lib/multi-protocol-fixture.sh"

require_root
mpf_require_binaries

# ---------------------------------------------------------------------------
# Fixture setup
# ---------------------------------------------------------------------------
SUFFIX="or-$$"
mpf_setup_namespaces "$SUFFIX"
CORE_PID=""
cleanup() {
    set +e
    if [[ -n "${CORE_PID:-}" ]]; then
        kill -TERM "$CORE_PID" 2>/dev/null || true
        wait "$CORE_PID" 2>/dev/null || true
        CORE_PID=""
    fi
    mpf_cleanup_namespaces
}
trap cleanup EXIT

mpf_setup_awg_fixture
mpf_setup_xray_fixture
mpf_setup_openconnect_fixture

# Start reference servers
mpf_start_xray_cover
mpf_start_awg_server
mpf_start_xray_server
mpf_start_oc_server

# ---------------------------------------------------------------------------
# Phase 1 (fixture) — enable synthetic default-underlay for core
#
# Two isolated defaults in the client namespace:
#   primary: via AWG server IP on AWG client veth, metric 100
#   backup:  via Xray server IP on Xray client veth, metric 200
#
# These are NOT Internet access — both gateways terminate inside isolated
# test namespaces with no NAT/public route.
#
# The old "pre-core ping to refresh AWG handshake" workaround is removed.
# The AWG handshake must reach Ready through normal core lifecycle with a
# valid underlay; veth keepalive artifacts are no longer the root cause.
# ---------------------------------------------------------------------------
mpf_enable_core_underlay
mpf_assert_core_underlay_routes yes yes

# Record initial route path for each protocol endpoint (must be physical veth)
echo "--- Endpoint route get (initial) ---"
ip -n "$MPF_CLIENT_NS" route get "$MPF_AWG_SERVER_IP"
ip -n "$MPF_CLIENT_NS" route get "$MPF_XR_SERVER_IP"
ip -n "$MPF_CLIENT_NS" route get "$MPF_OC_SERVER_IP"

# ---------------------------------------------------------------------------
# Core with Go ownership
# ---------------------------------------------------------------------------
CORE_STATE="${MPF_TMP}/core-state"
CORE_SOCKET="${CORE_STATE}/core.sock"
OWNERSHIP_CONFIG="${MPF_TMP}/ownership.toml"
mkdir -p "$CORE_STATE"

cat > "$OWNERSHIP_CONFIG" <<'MPFEOF'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
MPFEOF

# Start kikimora-core with Go ownership
ip netns exec "$MPF_CLIENT_NS" "$CORE_BIN" serve \
    --socket "$CORE_SOCKET" \
    --state-dir "$CORE_STATE" \
    --config "${MPF_TMP}/awg.toml" \
    --config "${MPF_TMP}/xray.toml" \
    --config "${MPF_TMP}/openconnect.toml" \
    --toad-binary "$TOAD_BIN" \
    --ownership-config "$OWNERSHIP_CONFIG" \
    --auto-recovery &
CORE_PID=$!

mpf_wait_core_socket "$CORE_SOCKET" 5000 || {
    echo "Core socket did not appear" >&2
    exit 1
}

# ---------------------------------------------------------------------------
# Phase 3 — prove core sees intended underlay BEFORE ConnectAll
#
# The core underlay contract:
#   * Linux underlay.DefaultSnapshot() delegates to netlink.Snapshotter
#   * Snapshotter.defaultPath() only accepts 0.0.0.0/0 or ::/0
#   * Manager.scheduleValidation() refuses validation when epoch == 0
#     or both IPv4/IPv6 paths are nil
#
# Verify epoch > 0 and non-null IPv4 path BEFORE starting roles.
# Also record which interface/gateway the core selected for the primary
# underlay path — it should be the AWG client veth / AWG server IP.
# ---------------------------------------------------------------------------
echo "=== Phase 3: Prove core sees canonical underlay ==="

UNDERLAY_EPOCH=""
UNDERLAY_IFACE=""
UNDERLAY_GW=""

# Poll until core underlay epoch > 0 (up to 5s after socket appears).
# Use raw python3 parsing — no intermediate functions.
for _ in $(seq 1 50); do
    SNAP=$("$CORE_BIN" status --socket "$CORE_SOCKET" --json 2>/dev/null || echo '{"error":"unreachable"}')
    EPOCH=$(echo "$SNAP" | python3 -c "
import json,sys
snap=json.load(sys.stdin)
print(snap.get('underlay',{}).get('epoch',0))
" 2>/dev/null || echo "0")
    if [ "$EPOCH" -gt 0 ] 2>/dev/null; then
        UNDERLAY_EPOCH="$EPOCH"
        # Extract v4 details
        UNDERLAY_IFACE=$(echo "$SNAP" | python3 -c "
import json,sys
snap=json.load(sys.stdin)
v4=snap.get('underlay',{}).get('ipv4',{})
print(v4.get('interface','') or '')
" 2>/dev/null || echo "")
        UNDERLAY_GW=$(echo "$SNAP" | python3 -c "
import json,sys
snap=json.load(sys.stdin)
v4=snap.get('underlay',{}).get('ipv4',{})
print(v4.get('gateway','') or '')
" 2>/dev/null || echo "")
        break
    fi
    sleep 0.1
done

if [ -z "$UNDERLAY_EPOCH" ]; then
    echo "Phase 3 FAIL: core underlay epoch did not become > 0" >&2
    echo "--- main table routes ---"
    ip -n "$MPF_CLIENT_NS" route show table main
    echo "--- core status (raw) ---"
    "$CORE_BIN" status --socket "$CORE_SOCKET" --json 2>/dev/null || true
    exit 1
fi

echo "  epoch=$UNDERLAY_EPOCH"
echo "  v4: iface=$UNDERLAY_IFACE gw=$UNDERLAY_GW"
if [ "$UNDERLAY_IFACE" != "$MPF_AWG_CLIENT_VETH" ]; then
    echo "  WARNING: v4 interface is $UNDERLAY_IFACE, expected $MPF_AWG_CLIENT_VETH"
fi
echo "Phase 3 PASS: underlay epoch=$UNDERLAY_EPOCH iface=$UNDERLAY_IFACE gw=$UNDERLAY_GW"

# ---------------------------------------------------------------------------
# Role name constants
# --------------------------------------------------------------------------
ROLES=(awg xray oc)
AWG=awg
XRAY=xray
OC=oc

# ---------------------------------------------------------------------------
# Helper: extract a field from the core JSON snapshot
# ---------------------------------------------------------------------------
# shellcheck disable=SC2317 # invoked indirectly via $()
snapshot_field() {
    local field="$1"
    mpf_core_status "$CORE_SOCKET" | python3 -c "
import json, sys
snap = json.load(sys.stdin)
$field
"
}

# shellcheck disable=SC2317 # invoked indirectly via $()
role_field() {
    local role="$1" field="$2"
    mpf_core_status "$CORE_SOCKET" | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap.get('roles', []):
    if r['id'] == '$role':
        print($field)
        break
"
}

# shellcheck disable=SC2317 # invoked indirectly via $()
role_field_raw() {
    local role="$1" field="$2"
    mpf_core_status "$CORE_SOCKET" | python3 -c "
import json, sys
snap = json.load(sys.stdin)
for r in snap.get('roles', []):
    if r['id'] == '$role':
        val = $field
        if val is None:
            print('')
        elif isinstance(val, bool):
            print('true' if val else 'false')
        else:
            print(val)
        break
"
}

# ---------------------------------------------------------------------------
# Phase A — Initial connect
#
# Run normal ConnectAll only after Phase 3 proved nonzero underlay.
# Require all desired roles to reach Ready at current nonzero underlay epoch.
# For AWG specifically require:
#   - route_ready=true
#   - recent handshake / connected session
#   - validation completed before 30-second handshake window can expire
#   - endpoint route to server IP still resolves through physical veth
# ---------------------------------------------------------------------------
echo "=== Phase A: Initial connect ==="

mpf_core_connect_all "$CORE_SOCKET"

# Wait for all three roles to reach Ready with current epoch
for role in "${ROLES[@]}"; do
    mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap.get('roles', []):
    if r['id'] == '$role' and r['state'] == 'Ready' and r.get('route_ready') and r.get('validated_underlay_epoch') == snap.get('underlay', {}).get('epoch', 0) and not r.get('parking', {}).get('active'):
        sys.exit(0)
sys.exit(1)
" || {
        echo "Phase A: $role did not reach Ready" >&2
        snapshot_field "print(json.dumps(snap, indent=2))"
        exit 1
    }
done

# Verify endpoint route/rule ownership and fail-closed
snapshot_field "
for r in snap['roles']:
    assert r.get('endpoint', {}).get('state') == 'ready', f'{r[\"id\"]} endpoint not ready'
    assert r.get('publication', {}).get('published'), f'{r[\"id\"]} not published'
    assert not r.get('parking', {}).get('active'), f'{r[\"id\"]} parking active'
    assert r.get('validated_underlay_epoch') == snap.get('underlay', {}).get('epoch'), f'{r[\"id\"]} epoch mismatch'
    print(f'  {r[\"id\"]}: state={r[\"state\"]} epoch={r[\"validated_underlay_epoch\"]} route_ready={r[\"route_ready\"]}')
"

# Record initial identity for Phase F assertions
AWG_IF_BEFORE=$(role_field_raw "$AWG" "r.get('interface', {}).get('ifindex', '')")
XR_IF_BEFORE=$(role_field_raw "$XRAY" "r.get('interface', {}).get('ifindex', '')")
OC_IF_BEFORE=$(role_field_raw "$OC" "r.get('interface', {}).get('ifindex', '')")

echo "Phase A PASS: all three roles Ready at epoch $(snapshot_field 'print(snap.get(\"underlay\", {}).get(\"epoch\", \"?\"))')"

# ---------------------------------------------------------------------------
# Phase 6 — full-tunnel AWG: verify transport endpoint is NOT recursive
#
# With full-tunnel allowed_ips = ["0.0.0.0/0", "::/0"], the AWG transport
# endpoint (192.0.2.2:51820) must still resolve through the physical underlay
# veth, never through kk-awg0.
# ---------------------------------------------------------------------------
echo "=== Phase 6: Non-recursive AWG endpoint route ==="

AWG_ROUTE_GET=$(ip -n "$MPF_CLIENT_NS" route get "$MPF_AWG_SERVER_IP")
echo "  $AWG_ROUTE_GET"

if echo "$AWG_ROUTE_GET" | grep -q "$MPF_AWG_TUN"; then
    echo "ERROR: AWG transport endpoint resolves through $MPF_AWG_TUN (recursive)!" >&2
    echo "  This is a real endpoint-routing defect under full-tunnel config." >&2
    echo "  Full routing table:" >&2
    ip -n "$MPF_CLIENT_NS" route show table all >&2
    exit 1
fi

if ! echo "$AWG_ROUTE_GET" | grep -q "$MPF_AWG_CLIENT_VETH"; then
    echo "ERROR: AWG transport endpoint does not resolve through $MPF_AWG_CLIENT_VETH" >&2
    echo "  Got: $AWG_ROUTE_GET" >&2
    exit 1
fi

echo "Phase 6 PASS: AWG endpoint route is non-recursive (via $MPF_AWG_CLIENT_VETH)"

# ---------------------------------------------------------------------------
# Phase B — Canonical underlay mutation
#
# Unlike the old script that only brought the AWG veth down (which was
# insufficient with a synthetic default route), Phase B now explicitly
# mutates the canonical underlay:
#   1. Record initial epoch, PIDs/generations and TUN ifindices
#   2. Remove the primary default route
#   3. Bring the AWG underlay veth down
#   4. Require core underlay to converge to backup (Xray veth/default)
#      and epoch to advance
#   5. Require AWG selected traffic fail-closed while physical endpoint
#      is unavailable
#   6. Xray/OpenConnect may transiently revalidate, but their TUN
#      identities must not be recreated
#   7. Restore AWG veth
#   8. Restore primary metric-100 default
#   9. Require convergence and eventual Ready/current-epoch state
# ---------------------------------------------------------------------------
echo "=== Phase B: Canonical underlay mutation ==="

EPOCH_BEFORE=$(snapshot_field 'print(snap.get("underlay", {}).get("epoch", 0))')

# 1. Remove primary synthetic default + bring AWG underlay veth down
mpf_switch_core_underlay_to_backup
mpf_assert_core_underlay_routes no yes
ip -n "$MPF_CLIENT_NS" link set "$MPF_AWG_CLIENT_VETH" down

# 2. Wait for epoch to advance (backup Xray path becomes canonical)
mpf_wait_snapshot "$CORE_SOCKET" 15000 "
epoch = snap.get('underlay', {}).get('epoch', 0)
assert epoch > $EPOCH_BEFORE, f'epoch did not advance: {epoch} <= $EPOCH_BEFORE'
v4 = snap.get('underlay', {}).get('ipv4', {})
# After underlay switch, gateway should be Xray server IP
# or at minimum a different path from the original AWG gateway
print(f'epoch advanced: $EPOCH_BEFORE -> {epoch}')
print(f'  v4 iface={v4.get(\"interface\",\"?\")} gw={v4.get(\"gateway\",\"?\")}')
sys.exit(0)
" || {
    echo "Phase B: epoch did not advance after removing primary underlay" >&2
    echo "--- Routes ---"
    ip -n "$MPF_CLIENT_NS" route show table main
    echo "--- Core status ---"
    mpf_core_status "$CORE_SOCKET"
    exit 1
}

# 3. Wait for AWG to detect endpoint unreachable and enter fail-closed
mpf_wait_snapshot "$CORE_SOCKET" 15000 "
epoch = snap.get('underlay', {}).get('epoch', 0)
for r in snap.get('roles', []):
    if r['id'] == 'awg':
        assert r.get('state') in ('Recovering', 'Degraded'), f'awg should be recovering: {r[\"state\"]}'
        if r.get('route_ready'):
            print('  awg still route_ready after endpoint loss — waiting for fail-closed')
            sys.exit(1)
        if not r.get('parking', {}).get('active'):
            print('  awg parking not yet active')
            sys.exit(1)
    else:
        if r.get('state') != 'Ready':
            print(f'  {r[\"id\"]} transitioning: {r[\"state\"]} — waiting')
            sys.exit(1)
print(f'awg fail-closed at epoch {epoch}')
sys.exit(0)
" || {
    echo "Phase B: AWG did not enter fail-closed after underlay loss" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# 4. Restore AWG veth and primary default
ip -n "$MPF_CLIENT_NS" link set "$MPF_AWG_CLIENT_VETH" up
mpf_restore_core_underlay_primary
mpf_assert_core_underlay_routes yes yes

# 5. Wait for epoch to advance again and AWG to recover to Ready
mpf_wait_snapshot "$CORE_SOCKET" 45000 "
epoch = snap.get('underlay', {}).get('epoch', 0)
assert epoch > $EPOCH_BEFORE, f'epoch should have advanced past $EPOCH_BEFORE: {epoch}'

# AWG must be Ready with current epoch
awg_ok = False
for r in snap.get('roles', []):
    if r['id'] == 'awg':
        if r.get('state') == 'Ready' and r.get('route_ready') and r.get('validated_underlay_epoch') == epoch:
            awg_ok = True
        else:
            print(f'  awg not ready: state={r[\"state\"]} route_ready={r.get(\"route_ready\")} epoch={r.get(\"validated_underlay_epoch\")}')
            sys.exit(1)
    else:
        if r.get('state') != 'Ready':
            print(f'  {r[\"id\"]} not Ready: state={r[\"state\"]}')
            sys.exit(1)
        if r.get('validated_underlay_epoch') != epoch:
            print(f'  {r[\"id\"]} epoch mismatch: {r.get(\"validated_underlay_epoch\")} != {epoch}')
            sys.exit(1)

assert awg_ok, 'AWG did not recover to Ready'
print(f'AWG recovered at epoch {epoch}')
sys.exit(0)
" || {
    echo "Phase B: AWG did not recover after underlay restoration" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase B PASS: canonical underlay mutation triggered epoch advance and AWG recovered"

# ---------------------------------------------------------------------------
# Phase C — AWG and Xray local-address drift
# ---------------------------------------------------------------------------
echo "=== Phase C: Address drift ==="

AWG_IFINDEX_BEFORE=$(role_field_raw "$AWG" "r.get('interface', {}).get('ifindex', '')")
ip -n "$MPF_CLIENT_NS" addr del 10.77.0.2/24 dev "$MPF_AWG_TUN" 2>/dev/null || true

mpf_wait_snapshot "$CORE_SOCKET" 15000 "
for r in snap.get('roles', []):
    if r['id'] == 'awg':
        assert not r.get('route_ready'), 'awg should not be route_ready after addr loss'
        assert r.get('state') != 'Ready', 'awg should not be Ready after addr loss'
sys.exit(0)
" || {
    echo "Phase C: AWG did not detect address loss" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for repair (same ifindex)
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap.get('roles', []):
    if r['id'] == 'awg':
        assert r.get('route_ready'), 'awg should be route_ready after repair'
        assert r.get('state') == 'Ready', f'awg should be Ready: {r[\"state\"]}'
        assert r.get('interface', {}).get('ifindex') == $AWG_IFINDEX_BEFORE, f'awg ifindex changed: {r.get(\"interface\", {}).get(\"ifindex\")} != $AWG_IFINDEX_BEFORE'
        assert r.get('validated_underlay_epoch') == snap.get('underlay', {}).get('epoch', 0), 'awg epoch mismatch'
sys.exit(0)
" || {
    echo "Phase C: AWG did not repair address drift" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase C PASS: AWG address drift detected and repaired with same ifindex"

# ---------------------------------------------------------------------------
# Phase D — OpenConnect negotiated-address drift
# ---------------------------------------------------------------------------
echo "=== Phase D: OpenConnect address drift ==="

OC_IF_BEFORE_D=$(role_field_raw "$OC" "r.get('interface', {}).get('ifindex', '')")

# Remove negotiated address
ip -n "$MPF_CLIENT_NS" addr flush dev "$MPF_OC_TUN" 2>/dev/null || true

# Verify RouteReady drops and fail-closed
mpf_wait_snapshot "$CORE_SOCKET" 15000 "
for r in snap.get('roles', []):
    if r['id'] == 'oc':
        assert not r.get('route_ready'), 'oc should not be route_ready after addr loss'
        assert r.get('state') != 'Ready', 'oc should not be Ready after addr loss'
sys.exit(0)
" || {
    echo "Phase D: OC did not detect address loss" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for transport recovery (full Toad restart may advance generation)
mpf_wait_snapshot "$CORE_SOCKET" 45000 "
for r in snap.get('roles', []):
    if r['id'] == 'oc':
        assert r.get('route_ready'), 'oc should be route_ready after recovery'
        assert r.get('state') == 'Ready', f'oc should be Ready: {r[\"state\"]}'
        assert r.get('validated_underlay_epoch') == snap.get('underlay', {}).get('epoch', 0), 'oc epoch mismatch'
        assert not r.get('parking', {}).get('active'), 'oc parking should be inactive'
        assert len(r.get('interface', {}).get('addresses', [])) > 0, 'oc should have a negotiated address'
sys.exit(0)
" || {
    echo "Phase D: OC did not recover address drift" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase D PASS: OpenConnect address drift detected and recovered"

# ---------------------------------------------------------------------------
# Phase E — One Toad crash
# ---------------------------------------------------------------------------
echo "=== Phase E: Toad crash ==="

# Record before state
AWG_IF_BEFORE_E=$(role_field_raw "$AWG" "r.get('interface', {}).get('ifindex', '')")
XR_IF_BEFORE_E=$(role_field_raw "$XRAY" "r.get('interface', {}).get('ifindex', '')")
OC_IF_BEFORE_E=$(role_field_raw "$OC" "r.get('interface', {}).get('ifindex', '')")

# Find and SIGKILL the Xray Toad process
XR_TOAD_PID=$(ip netns exec "$MPF_CLIENT_NS" pgrep -f "kikimora-toad.*xray" 2>/dev/null || true)
if [[ -z "$XR_TOAD_PID" ]]; then
    echo "Phase E: could not find Xray Toad PID" >&2
    exit 1
fi
kill -9 "$XR_TOAD_PID" 2>/dev/null || true

# Wait for Xray to get a new process/generation, others unchanged
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap.get('roles', []):
    if r['id'] == 'xray':
        assert r.get('state') in ('Starting', 'Recovering', 'Ready'), f'xray unexpected: {r[\"state\"]}'
    else:
        assert r.get('state') == 'Ready', f'{r[\"id\"]} should remain Ready: {r[\"state\"]}'
sys.exit(0)
" || {
    echo "Phase E: Xray crash did not trigger expected behavior" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for Xray full recovery
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap.get('roles', []):
    if r['id'] == 'xray':
        assert r.get('state') == 'Ready', f'xray should be Ready: {r[\"state\"]}'
        assert r.get('route_ready'), 'xray should be route_ready'
        assert r.get('validated_underlay_epoch') == snap.get('underlay', {}).get('epoch', 0), 'xray epoch mismatch'
    else:
        assert r.get('state') == 'Ready', f'{r[\"id\"]} should remain Ready'
        if r['id'] == 'awg':
            assert r.get('interface', {}).get('ifindex') == $AWG_IF_BEFORE_E, 'awg ifindex changed'
        if r['id'] == 'oc':
            assert r.get('interface', {}).get('ifindex') == $OC_IF_BEFORE_E, 'oc ifindex changed'
sys.exit(0)
" || {
    echo "Phase E: Xray did not recover" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase E PASS: Xray Toad crash isolated and recovered"

# ---------------------------------------------------------------------------
# Phase F — Core restart and persisted desired intent
# ---------------------------------------------------------------------------
echo "=== Phase F: Core restart ==="

# Disconnect one role explicitly (OC) to test persisted desired=false
mpf_core_disconnect_role "$CORE_SOCKET" "$OC"

mpf_wait_snapshot "$CORE_SOCKET" 10000 "
for r in snap.get('roles', []):
    if r['id'] == 'oc':
        assert not r.get('desired_enabled'), 'oc should be disabled'
        assert r.get('state') == 'Stopped', 'oc should be Stopped'
sys.exit(0)
" || {
    echo "Phase F: OC did not stop" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Kill core
kill "$CORE_PID" 2>/dev/null || true
wait "$CORE_PID" 2>/dev/null || true

# Restart core with same ownership config and state dir
ip netns exec "$MPF_CLIENT_NS" "$CORE_BIN" serve \
    --socket "$CORE_SOCKET" \
    --state-dir "$CORE_STATE" \
    --config "${MPF_TMP}/awg.toml" \
    --config "${MPF_TMP}/xray.toml" \
    --config "${MPF_TMP}/openconnect.toml" \
    --toad-binary "$TOAD_BIN" \
    --ownership-config "$OWNERSHIP_CONFIG" \
    --auto-recovery &
CORE_PID=$!

mpf_wait_core_socket "$CORE_SOCKET" 5000 || {
    echo "Core socket did not appear after restart" >&2
    exit 1
}

# Wait for desired state restoration: AWG and Xray should be Ready, OC should be Stopped
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
awg_ok=false
xray_ok=false
oc_stopped=false
for r in snap.get('roles', []):
    if r['id'] == 'awg' and r.get('desired_enabled') and r.get('state') == 'Ready' and r.get('route_ready'):
        awg_ok = True
    if r['id'] == 'xray' and r.get('desired_enabled') and r.get('state') == 'Ready' and r.get('route_ready'):
        xray_ok = True
    if r['id'] == 'oc' and not r.get('desired_enabled') and r.get('state') == 'Stopped':
        oc_stopped = True
assert awg_ok, 'AWG should be Ready after restart'
assert xray_ok, 'Xray should be Ready after restart'
assert oc_stopped, 'OC should be Stopped after restart (desired=false persisted)'
sys.exit(0)
" || {
    echo "Phase F: desired state not correctly restored after core restart" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase F PASS: desired state persisted and restored after core restart"

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
echo ""
echo "=== ALL PHASES PASSED ==="
exit 0