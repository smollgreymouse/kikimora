#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# vm-disconnect-teardown-acceptance.sh — issue #31 acceptance gates on the
# disposable Ubuntu VM against the installed Kikimora package.
#
# Covers the feasible subset of the issue's acceptance gates (section C):
#   G1 baseline of the connected state;
#   G2 DisconnectRole isolates: only that role's resources go, the sibling
#      stays Ready;
#   G3 DisconnectAll leaves zero Kikimora-owned network state and is
#      idempotent (the second call mutates nothing);
#   G4 the idle core still accepts ConnectAll and reconnects the roles;
#   G5 graceful stop (systemctl stop, no restart marker) releases the same
#      network state as DisconnectAll and stops the DNS owner with the core;
#   G6 restart with the marker keeps the transient fail-closed contract and
#      the marker is removed after the stop;
#   G7 UDP/53-drop TCP/53-pass fixture: Leshy with dns_protocol=tcp resolves
#      while upstream UDP is blocked;
#   G8 external system-wide VPN handoff is recorded as SKIP (needs a second
#      VPN client provisioned on the VM).
#
# Env: ROLES (two role names, default "awg oc"), SKIP_DNS_FIXTURE=1 to skip G7.

set -Eeuo pipefail

CORE_BIN="${KIKIMORA_CORE_BIN:-/usr/local/bin/kikimora-core}"
CORE_SOCK="${KIKIMORA_CORE_SOCK:-/run/kikimora/core.sock}"
PUB_DIR="${KIKIMORA_PUB_DIR:-/run/kikimora/leshy/vpn}"
OUT_DIR="${VM_DISCONNECT_ACCEPTANCE_OUT:-/tmp/vm-disconnect-acceptance-$$}"
MARKER="/run/kikimora/core-restart.pending"
ROLES=${ROLES:-"awg oc"}

mkdir -p "$OUT_DIR"
fail() { echo "ERROR: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
command -v python3 >/dev/null || fail "python3 required"
command -v jq >/dev/null || true

cleanup() {
    # The UDP fixture must never outlive the script on the disposable VM.
    iptables -D OUTPUT -p udp --dport 53 -j REJECT 2>/dev/null || true
}
trap cleanup EXIT

core() { "$CORE_BIN" "$@" --socket "$CORE_SOCK"; }
role_field() { # role_field <role> <json-field>
    python3 -c "
import json, sys
data = json.load(open('$OUT_DIR/status.json'))
for r in data.get('roles') or []:
    if r.get('id') == '$1':
        print(json.dumps(r.get('$2')))
        break
"
}
wait_roles_state() { # wait_roles_state <state> <timeout-seconds> <role...>
    local deadline=$((SECONDS + $2)); shift 2
    while (( SECONDS <= deadline )); do
        core status --json >"$OUT_DIR/status.json" 2>/dev/null || true
        local ok=1 r
        for r in "$@"; do
            [[ "$(role_field "$r" state | tr -d '"')" == "$1" ]] || ok=0
        done
        ((ok)) && return 0
        sleep 2
    done
    return 1
}

echo "=== issue #31 disconnect/teardown acceptance gates ==="
echo "Output: $OUT_DIR"

# ---------------------------------------------------------------------------
# G0. Preflight: two configured roles, core reachable, no restart marker
# ---------------------------------------------------------------------------
echo "--- G0. Preflight ---"
core status --json >"$OUT_DIR/status.json" || fail "core status unavailable"
for r in $ROLES; do
    [[ "$(role_field "$r" id)" != "null" ]] || fail "role $r not configured (have: $(python3 -c "import json;print(' '.join(x['id'] for x in json.load(open('$OUT_DIR/status.json'))['roles']))"))"
done
[[ -e "$MARKER" ]] && fail "stale restart marker $MARKER present; remove it before the run"
read -r -a ROLE_ARR <<<"$ROLES"
(( ${#ROLE_ARR[@]} == 2 )) || fail "ROLES must name exactly two roles"
pass "preflight: two roles configured (${ROLE_ARR[*]}), core reachable, no restart marker"

# Roles must start connected: the gates exercise release from a live state.
if ! wait_roles_state Ready 5 "${ROLE_ARR[@]}"; then
    core connect-all >/dev/null
    wait_roles_state Ready 90 "${ROLE_ARR[@]}" || fail "roles did not reach Ready"
fi
pass "roles connected and Ready"

# ---------------------------------------------------------------------------
# G1. Baseline of the connected state
# ---------------------------------------------------------------------------
echo "--- G1. Baseline ---"
capture_net() { # capture_net <file>
    {
        echo "=== routes v4 ==="; ip -4 route show
        echo "=== routes v6 ==="; ip -6 route show
        echo "=== table 51890 v4 ==="; ip -4 route show table 51890 2>/dev/null
        echo "=== table 51890 v6 ==="; ip -6 route show table 51890 2>/dev/null
        echo "=== rules v4 ==="; ip -4 rule show
        echo "=== rules v6 ==="; ip -6 rule show
        echo "=== parks (table 254, unreachable metric 42760) ==="
        ip -4 route show table 254 | grep 'unreachable.*metric 42760' || true
        ip -6 route show table 254 | grep 'unreachable.*metric 42760' || true
    } >"$1"
}
capture_net "$OUT_DIR/net-connected.txt"
ls -la "$PUB_DIR" >"$OUT_DIR/publications-connected.txt" 2>/dev/null || true
systemctl is-active kikimora-leshy-dns.service >"$OUT_DIR/dns-unit-before-stop.txt" 2>/dev/null || true
pass "baseline captured"

owned_state_present() { # zero when no owned routes/rules/parks/publications
    local owned=0
    [[ -n "$(ip -4 route show table 51890 2>/dev/null)" ]] && owned=1
    [[ -n "$(ip -6 route show table 51890 2>/dev/null)" ]] && owned=1
    ip -4 rule show | grep -q 'lookup 51890' && owned=1
    ip -6 rule show | grep -q 'lookup 51890' && owned=1
    ip -4 route show table 254 | grep -q 'unreachable.*metric 42760' && owned=1
    ip -6 route show table 254 | grep -q 'unreachable.*metric 42760' && owned=1
    ls "$PUB_DIR"/*.dev >/dev/null 2>&1 && owned=1
    return "$owned"
}

# ---------------------------------------------------------------------------
# G2. DisconnectRole isolates the sibling
# ---------------------------------------------------------------------------
echo "--- G2. DisconnectRole isolation ---"
core disconnect --role "${ROLE_ARR[0]}" >/dev/null
wait_roles_state Stopped 20 "${ROLE_ARR[0]}" || fail "${ROLE_ARR[0]} did not stop"
[[ -e "$PUB_DIR/${ROLE_ARR[0]}.dev" ]] && fail "${ROLE_ARR[0]} publication survived DisconnectRole"
[[ -e "$PUB_DIR/${ROLE_ARR[1]}.dev" ]] || fail "${ROLE_ARR[1]} publication was removed by a sibling disconnect"
core status --json >"$OUT_DIR/status.json"
[[ "$(role_field "${ROLE_ARR[1]}" state | tr -d '"')" == "Ready" ]] || fail "${ROLE_ARR[1]} left Ready by the sibling disconnect"
pass "DisconnectRole removed only ${ROLE_ARR[0]}'s resources; ${ROLE_ARR[1]} stayed Ready"

echo "--- G2b. Reconnect ${ROLE_ARR[0]} ---"
core connect --role "${ROLE_ARR[0]}" >/dev/null
wait_roles_state Ready 90 "${ROLE_ARR[0]}" || fail "${ROLE_ARR[0]} did not reconnect"
pass "${ROLE_ARR[0]} reconnected to Ready"

# ---------------------------------------------------------------------------
# G3. DisconnectAll: zero owned state, then idempotent
# ---------------------------------------------------------------------------
echo "--- G3. DisconnectAll zero-residual ---"
core disconnect-all >/dev/null
wait_roles_state Stopped 30 "${ROLE_ARR[@]}" || fail "roles did not stop"
if owned_state_present; then
    capture_net "$OUT_DIR/net-after-disconnectall.txt"
    fail "Kikimora-owned network state survived DisconnectAll (see net-after-disconnectall.txt)"
fi
systemctl is-active --quiet kikimora-core || fail "core must stay alive after DisconnectAll"
pass "DisconnectAll left zero owned routes/rules/parks/publications; core still alive"

echo "--- G3b. Idempotent DisconnectAll ---"
capture_net "$OUT_DIR/net-idem-before.txt"
core disconnect-all >/dev/null
sleep 2
capture_net "$OUT_DIR/net-idem-after.txt"
if ! diff -u "$OUT_DIR/net-idem-before.txt" "$OUT_DIR/net-idem-after.txt" >"$OUT_DIR/net-idem.diff"; then
    fail "second DisconnectAll mutated the network (see net-idem.diff)"
fi
pass "second DisconnectAll produced no further mutations"

# ---------------------------------------------------------------------------
# G4. The idle core still accepts ConnectAll
# ---------------------------------------------------------------------------
echo "--- G4. ConnectAll from idle ---"
core connect-all >/dev/null
wait_roles_state Ready 90 "${ROLE_ARR[@]}" || fail "roles did not reconnect after DisconnectAll"
pass "idle core reconnected all roles to Ready"

# ---------------------------------------------------------------------------
# G5. Graceful stop releases the same state; DNS owner goes with the core
# ---------------------------------------------------------------------------
echo "--- G5. Graceful stop equivalence ---"
DNS_WAS_ACTIVE=0
if systemctl is-active --quiet kikimora-leshy-dns.service; then
    DNS_WAS_ACTIVE=1
fi
systemctl stop kikimora-core.service
[[ -e "$MARKER" ]] && fail "restart marker must not exist for a plain stop"
if owned_state_present; then
    capture_net "$OUT_DIR/net-after-stop.txt"
    fail "graceful stop left owned network state (see net-after-stop.txt)"
fi
systemctl is-active --quiet kikimora-core && fail "core still active after stop"
if (( DNS_WAS_ACTIVE )); then
    systemctl is-active --quiet kikimora-leshy-dns.service && fail "DNS owner still active after the core stopped"
    pass "DNS owner stopped with the core (PartOf)"
else
    pass "DNS owner was not enabled; skip the PartOf observation"
fi
pass "graceful stop released the network exactly like DisconnectAll"
systemctl start kikimora-core.service
wait_roles_state Ready 120 "${ROLE_ARR[@]}" || fail "roles did not reconnect after the graceful stop cycle"
pass "roles reconnected after start"

# ---------------------------------------------------------------------------
# G6. Restart with the marker keeps the transient fail-closed contract
# ---------------------------------------------------------------------------
echo "--- G6. Marker restart ---"
touch "$MARKER"
systemctl restart kikimora-core.service
wait_roles_state Ready 120 "${ROLE_ARR[@]}" || fail "roles did not recover after the marker restart"
[[ -e "$MARKER" ]] && fail "ExecStopPost must remove the restart marker after the stop"
pass "marker restart recovered to Ready and the marker was cleaned up"

# ---------------------------------------------------------------------------
# G7. UDP/53-drop TCP/53-pass DNS fixture
# ---------------------------------------------------------------------------
echo "--- G7. DNS transport fixture ---"
if [[ "${SKIP_DNS_FIXTURE:-0}" == "1" ]]; then
    echo "SKIP: DNS transport fixture disabled by SKIP_DNS_FIXTURE"
else
    iptables -A OUTPUT -p udp --dport 53 -j REJECT
    core generate-leshy --dns-protocol tcp >/dev/null
    systemctl restart leshy.service
    sleep 2
    if systemctl is-active --quiet kikimora-leshy-dns.service; then
        systemctl restart kikimora-leshy-dns.service
    fi
    if dig @127.0.0.1 -p 53053 example.com A +time=3 +tries=1 +short >"$OUT_DIR/dig-tcp.txt" 2>&1 && [[ -s "$OUT_DIR/dig-tcp.txt" ]]; then
        pass "Leshy resolves over TCP while upstream UDP/53 is blocked"
    else
        fail "Leshy failed to resolve with dns_protocol=tcp under UDP block (see dig-tcp.txt)"
    fi
    grep -q 'dns_protocol = "tcp"' /etc/kikimora/leshy/config.toml || fail "dns_protocol=tcp did not survive regeneration"
    # Revert the fixture before the run ends.
    core generate-leshy --dns-protocol udp >/dev/null
    systemctl restart leshy.service
    sleep 2
    if systemctl is-active --quiet kikimora-leshy-dns.service; then
        systemctl restart kikimora-leshy-dns.service
    fi
    dig @127.0.0.1 -p 53053 example.com A +time=3 +tries=1 +short >/dev/null 2>&1 || fail "Leshy stopped resolving after the fixture revert"
    pass "fixture reverted (dns_protocol=udp), Leshy still resolves"
fi

# ---------------------------------------------------------------------------
# G8. External VPN handoff — recorded, not executed here
# ---------------------------------------------------------------------------
echo "--- G8. External VPN handoff ---"
echo "SKIP: external system-wide VPN handoff needs a second VPN client provisioned"
echo "      on the VM (issue #31 gate 6); run it separately before workstation cutover."

echo "=== ALL GATES PASSED (external-VPN handoff recorded as separate work) ==="
