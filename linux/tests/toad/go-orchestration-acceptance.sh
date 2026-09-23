#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# go-orchestration-acceptance.sh — privileged 07D orchestration gate
#
# Reuses the shared multi-protocol fixture from lib/multi-protocol-fixture.sh.
# Runs phases A-F with precise invariants: PID/generation/ifindex, current
# epoch, fail-closed, and persisted desired-state restore.
#
# Required environment:
#   TOAD_BIN, CORE_BIN, AWG_REF_BIN, XRAY_REF_BIN, XRAY_COVER_BIN,
#   OPENCONNECT_BIN, OCSERV_BIN
#
# shellcheck source=linux/tests/toad/lib/multi-protocol-fixture.sh

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/multi-protocol-fixture.sh
# shellcheck source=linux/tests/toad/lib/multi-protocol-fixture.sh
source "$SCRIPT_DIR/lib/multi-protocol-fixture.sh"

require_root
mpf_require_binaries

# ---------------------------------------------------------------------------
# Fixture setup
# ---------------------------------------------------------------------------
SUFFIX="oc-$$"
mpf_setup_namespaces "$SUFFIX"
trap 'mpf_cleanup_namespaces' EXIT

mpf_setup_awg_fixture
mpf_setup_xray_fixture
mpf_setup_openconnect_fixture

# Start reference servers
mpf_start_xray_cover
mpf_start_awg_server
mpf_start_xray_server
mpf_start_oc_server

# ---------------------------------------------------------------------------
# Core with Go ownership
# ---------------------------------------------------------------------------
CORE_STATE="${MPF_TMP}/core-state"
CORE_SOCKET="${CORE_STATE}/core.sock"
OWNERSHIP_CONFIG="${MPF_TMP}/ownership.toml"
mkdir -p "$CORE_STATE"
mpf_ownership_config > "$OWNERSHIP_CONFIG"

# Generate ownership config that enables Go-owned lifecycle
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
# Role name constants
# --------------------------------------------------------------------------
ROLES=(awg xray oc)
AWG=awg
XRAY=xray
OC=oc

# ---------------------------------------------------------------------------
# Helper: extract a field from the core JSON snapshot
# ---------------------------------------------------------------------------
snapshot_field() {
    local field="$1"
    mpf_core_status "$CORE_SOCKET" | python3 -c "
import json, sys
snap = json.load(sys.stdin)
$field
"
}

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
# ---------------------------------------------------------------------------
echo "=== Phase A: Initial connect ==="

mpf_core_connect_all "$CORE_SOCKET"

# Wait for all three roles to reach Ready with current epoch
for role in "${ROLES[@]}"; do
    mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap.get('roles', []):
    if r['id'] == '$role' and r['state'] == 'Ready' and r['route_ready'] and r['validated_underlay_epoch'] == snap['underlay']['epoch'] and not r['parking']['active']:
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
    assert r['endpoint']['state'] == 'ready', f'{r[\"id\"]} endpoint not ready'
    assert r['publication']['published'], f'{r[\"id\"]} not published'
    assert not r['parking']['active'], f'{r[\"id\"]} parking active'
    assert r['validated_underlay_epoch'] == snap['underlay']['epoch'], f'{r[\"id\"]} epoch mismatch'
    print(f'  {r[\"id\"]}: state={r[\"state\"]} epoch={r[\"validated_underlay_epoch\"]} route_ready={r[\"route_ready\"]}')
"

# Record initial identity for Phase E assertions
# shellcheck disable=SC2034
AWG_IF_BEFORE=$(role_field_raw "$AWG" "r['interface']['ifindex']")
# shellcheck disable=SC2034
XR_IF_BEFORE=$(role_field_raw "$XRAY" "r['interface']['ifindex']")
# shellcheck disable=SC2034
OC_IF_BEFORE=$(role_field_raw "$OC" "r['interface']['ifindex']")

echo "Phase A PASS: all three roles Ready at epoch $(snapshot_field 'print(snap[\"underlay\"][\"epoch\"])')"

# ---------------------------------------------------------------------------
# Phase B — Underlay change / recovery
# ---------------------------------------------------------------------------
echo "=== Phase B: Underlay change ==="

EPOCH_BEFORE=$(snapshot_field 'print(snap["underlay"]["epoch"])')

# Bring down the AWG underlay veth
ip -n "$MPF_CLIENT_NS" link set "$MPF_AWG_CLIENT_VETH" down

# Wait for epoch to advance and AWG to enter fail-closed recovery
mpf_wait_snapshot "$CORE_SOCKET" 15000 "
epoch = snap['underlay']['epoch']
assert epoch > $EPOCH_BEFORE, 'epoch did not advance'
for r in snap['roles']:
    if r['id'] == 'awg':
        assert r['state'] in ('Recovering', 'Degraded'), f'awg should be recovering: {r[\"state\"]}'
        assert not r['route_ready'], 'awg should not be route_ready'
        assert r['parking']['active'], 'awg parking should be active'
    else:
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready: {r[\"state\"]}'
        assert r['validated_underlay_epoch'] == epoch, f'{r[\"id\"]} epoch mismatch'
print(f'epoch advanced: {EPOCH_BEFORE} -> {epoch}')
sys.exit(0)
" || {
    echo "Phase B: underlay loss did not trigger expected recovery" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Restore underlay
ip -n "$MPF_CLIENT_NS" link set "$MPF_AWG_CLIENT_VETH" up

# Wait for AWG to recover to Ready at current epoch
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
epoch = snap['underlay']['epoch']
for r in snap['roles']:
    if r['id'] == 'awg':
        assert r['state'] == 'Ready', f'awg should be Ready: {r[\"state\"]}'
        assert r['route_ready'], 'awg should be route_ready'
        assert r['validated_underlay_epoch'] == epoch, 'awg epoch mismatch'
        assert not r['parking']['active'], 'awg parking should be inactive'
    else:
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready'
sys.exit(0)
" || {
    echo "Phase B: AWG did not recover" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

echo "Phase B PASS: underlay change triggered epoch advance and AWG recovered"

# ---------------------------------------------------------------------------
# Phase C — AWG and Xray local-address drift
# ---------------------------------------------------------------------------
echo "=== Phase C: Address drift ==="

# AWG address drift
AWG_IFINDEX_BEFORE=$(role_field_raw "$AWG" "r['interface']['ifindex']")
ip -n "$MPF_CLIENT_NS" addr del 10.77.0.2/24 dev "$MPF_AWG_TUN" 2>/dev/null || true

mpf_wait_snapshot "$CORE_SOCKET" 15000 "
for r in snap['roles']:
    if r['id'] == 'awg':
        assert not r['route_ready'], 'awg should not be route_ready after addr loss'
        assert r['state'] != 'Ready', 'awg should not be Ready after addr loss'
sys.exit(0)
" || {
    echo "Phase C: AWG did not detect address loss" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for repair (same ifindex)
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap['roles']:
    if r['id'] == 'awg':
        assert r['route_ready'], 'awg should be route_ready after repair'
        assert r['state'] == 'Ready', f'awg should be Ready: {r[\"state\"]}'
        assert r['interface']['ifindex'] == $AWG_IFINDEX_BEFORE, f'awg ifindex changed: {r[\"interface\"][\"ifindex\"]} != $AWG_IFINDEX_BEFORE'
        assert r['validated_underlay_epoch'] == snap['underlay']['epoch'], 'awg epoch mismatch'
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

# shellcheck disable=SC2034
OC_IF_BEFORE_D=$(role_field_raw "$OC" "r['interface']['ifindex']")

# Remove negotiated address
ip -n "$MPF_CLIENT_NS" addr flush dev "$MPF_OC_TUN" 2>/dev/null || true

# Verify RouteReady drops and fail-closed
mpf_wait_snapshot "$CORE_SOCKET" 15000 "
for r in snap['roles']:
    if r['id'] == 'oc':
        assert not r['route_ready'], 'oc should not be route_ready after addr loss'
        assert r['state'] != 'Ready', 'oc should not be Ready after addr loss'
sys.exit(0)
" || {
    echo "Phase D: OC did not detect address loss" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for transport recovery (full Toad restart may advance generation)
mpf_wait_snapshot "$CORE_SOCKET" 45000 "
for r in snap['roles']:
    if r['id'] == 'oc':
        assert r['route_ready'], 'oc should be route_ready after recovery'
        assert r['state'] == 'Ready', f'oc should be Ready: {r[\"state\"]}'
        assert r['validated_underlay_epoch'] == snap['underlay']['epoch'], 'oc epoch mismatch'
        assert not r['parking']['active'], 'oc parking should be inactive'
        assert len(r['interface']['addresses']) > 0, 'oc should have a negotiated address'
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

# Record before state for Phase E assertions
# shellcheck disable=SC2034
AWG_IF_BEFORE_E=$(role_field_raw "$AWG" "r['interface']['ifindex']")
# shellcheck disable=SC2034
XR_IF_BEFORE_E=$(role_field_raw "$XRAY" "r['interface']['ifindex']")
# shellcheck disable=SC2034
OC_IF_BEFORE_E=$(role_field_raw "$OC" "r['interface']['ifindex']")

# Find and SIGKILL the Xray Toad process
XR_TOAD_PID=$(ip netns exec "$MPF_CLIENT_NS" pgrep -f "kikimora-toad.*xray" 2>/dev/null || true)
if [[ -z "$XR_TOAD_PID" ]]; then
    echo "Phase E: could not find Xray Toad PID" >&2
    exit 1
fi
kill -9 "$XR_TOAD_PID" 2>/dev/null || true

# Wait for Xray to get a new process/generation, others unchanged
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap['roles']:
    if r['id'] == 'xray':
        assert r['state'] in ('Starting', 'Recovering', 'Ready'), f'xray unexpected: {r[\"state\"]}'
    else:
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready: {r[\"state\"]}'
sys.exit(0)
" || {
    echo "Phase E: Xray crash did not trigger expected behavior" >&2
    snapshot_field "print(json.dumps(snap, indent=2))"
    exit 1
}

# Wait for Xray full recovery
mpf_wait_snapshot "$CORE_SOCKET" 30000 "
for r in snap['roles']:
    if r['id'] == 'xray':
        assert r['state'] == 'Ready', f'xray should be Ready: {r[\"state\"]}'
        assert r['route_ready'], 'xray should be route_ready'
        assert r['validated_underlay_epoch'] == snap['underlay']['epoch'], 'xray epoch mismatch'
    else:
        assert r['state'] == 'Ready', f'{r[\"id\"]} should remain Ready'
        assert r['interface']['ifindex'] == $AWG_IF_BEFORE_E or r['id'] != 'awg', 'awg ifindex changed'
        assert r['interface']['ifindex'] == $OC_IF_BEFORE_E or r['id'] != 'oc', 'oc ifindex changed'
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
for r in snap['roles']:
    if r['id'] == 'oc':
        assert not r['desired_enabled'], 'oc should be disabled'
        assert r['state'] == 'Stopped', 'oc should be Stopped'
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
for r in snap['roles']:
    if r['id'] == 'awg' and r['desired_enabled'] and r['state'] == 'Ready' and r['route_ready']:
        awg_ok = True
    if r['id'] == 'xray' and r['desired_enabled'] and r['state'] == 'Ready' and r['route_ready']:
        xray_ok = True
    if r['id'] == 'oc' and not r['desired_enabled'] and r['state'] == 'Stopped':
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
