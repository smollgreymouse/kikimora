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
#      while upstream UDP is blocked; the operator's config is restored
#      afterwards;
#   G8 external system-wide VPN handoff is recorded as SKIP (needs a second
#      VPN client provisioned on the VM).
#
# Env: ROLES (two role names, default "awg oc"), RECONNECT_TIMEOUT (seconds,
# default 180), SKIP_DNS_FIXTURE=1 to skip G7.

set -Eeuo pipefail

CORE_BIN="${KIKIMORA_CORE_BIN:-/usr/local/bin/kikimora-core}"
CORE_SOCK="${KIKIMORA_CORE_SOCK:-/run/kikimora/core.sock}"
PUB_DIR="${KIKIMORA_PUB_DIR:-/run/kikimora/leshy/vpn}"
LESHY_CONFIG="${KIKIMORA_LESHY_CONFIG:-/etc/kikimora/leshy/config.toml}"
OUT_DIR="${VM_DISCONNECT_ACCEPTANCE_OUT:-/tmp/vm-disconnect-acceptance-$$}"
MARKER="/run/kikimora/core-restart.pending"
ROLES=${ROLES:-"awg oc"}
RECONNECT_TIMEOUT=${RECONNECT_TIMEOUT:-180}

mkdir -p "$OUT_DIR"
fail() { echo "ERROR: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
SKIPPED=""
command -v python3 >/dev/null || fail "python3 required"

CONFIG_BACKUP=""
cleanup() {
    # The fixture and the marker must never outlive the script, whatever way
    # it ends; every step is idempotent so INT/TERM re-entry is safe.
    iptables -D OUTPUT -p udp --dport 53 ! -o lo -j REJECT 2>/dev/null || true
    if [[ -n "$CONFIG_BACKUP" && -f "$CONFIG_BACKUP" ]]; then
        cp "$CONFIG_BACKUP" "$LESHY_CONFIG"
        systemctl restart leshy.service 2>/dev/null || true
    fi
    rm -f "$MARKER"
}
trap cleanup EXIT INT TERM

core() { "$CORE_BIN" "$@" --socket "$CORE_SOCK"; }
# generate-leshy is a local file operation: its flag set has no --socket.
core_local() { "$CORE_BIN" "$@"; }
role_field() { # role_field <role> <json-field>
    python3 -c "
import json, sys
try:
    data = json.load(open('$OUT_DIR/status.json'))
except Exception:
    sys.exit(0)
for r in data.get('roles') or []:
    if r.get('id') == '$1':
        print(json.dumps(r.get('$2')))
        break
" 2>/dev/null
}
wait_roles_state() { # wait_roles_state <state> <timeout-seconds> <role...>
    local want="$1"
    local deadline=$((SECONDS + $2)); shift 2
    while (( SECONDS <= deadline )); do
        core status --json >"$OUT_DIR/status.json" 2>/dev/null || true
        local ok=1 r
        for r in "$@"; do
            [[ "$(role_field "$r" state | tr -d '"')" == "$want" ]] || ok=0
        done
        ((ok)) && return 0
        sleep 2
    done
    return 1
}
dig_leshy() { # dig_leshy <outfile>: retry while the Leshy listener settles
    local out="$1"
    for _ in 1 2 3 4 5; do
        if dig @127.0.0.1 -p 53053 example.com A +time=3 +tries=1 +short >"$out" 2>&1 && [[ -s "$out" ]]; then
            return 0
        fi
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
    [[ -n "$(role_field "$r" id | tr -d '"')" ]] ||
        fail "role $r not configured (have: $(python3 -c "import json;print(' '.join(x['id'] for x in json.load(open('$OUT_DIR/status.json'))['roles']))"))"
done
[[ -e "$MARKER" ]] && fail "stale restart marker $MARKER present; remove it before the run"
read -r -a ROLE_ARR <<<"$ROLES"
(( ${#ROLE_ARR[@]} == 2 )) || fail "ROLES must name exactly two roles"
pass "preflight: two roles configured (${ROLE_ARR[*]}), core reachable, no restart marker"

# Roles must start connected: the gates exercise release from a live state.
if ! wait_roles_state Ready 5 "${ROLE_ARR[@]}"; then
    core connect-all >/dev/null
    wait_roles_state Ready "$RECONNECT_TIMEOUT" "${ROLE_ARR[@]}" || fail "roles did not reach Ready"
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
pass "baseline captured"

# The core withdraws publications asynchronously on the disable event, so a
# just-finished DisconnectAll may be observed mid-settle: the assertion waits
# a bounded window for the owned state to drain before declaring residue.
debug_owned_state() {
    echo "DEBUG residue: t4=[$(ip -4 route show table 51890 2>/dev/null)]" \
        "t6=[$(ip -6 route show table 51890 2>/dev/null)]" \
        "r4=[$(ip -4 rule show | grep 'lookup 51890' || true)]" \
        "r6=[$(ip -6 rule show | grep 'lookup 51890' || true)]" \
        "p4=[$(ip -4 route show table 254 | grep 'unreachable.*metric 42760' || true)]" \
        "p6=[$(ip -6 route show table 254 | grep 'unreachable.*metric 42760' || true)]" \
        "pub=[$(find "$PUB_DIR" -maxdepth 1 -type f -printf '%f ' 2>/dev/null)]" >&2
}

wait_owned_state_gone() { # wait_owned_state_gone <timeout-seconds>
    local deadline=$((SECONDS + $1))
    while (( SECONDS <= deadline )); do
        owned_state_present || return 0
        sleep 1
    done
    debug_owned_state
    return 1
}

owned_state_present() { # zero when no owned routes/rules/parks/publications
    local owned=0 c1 c2 c3 c4 c5 c6 c7
    c1="$(ip -4 route show table 51890 2>/dev/null)"
    c2="$(ip -6 route show table 51890 2>/dev/null)"
    c3="$(ip -4 rule show | grep 'lookup 51890' || true)"
    c4="$(ip -6 rule show | grep 'lookup 51890' || true)"
    c5="$(ip -4 route show table 254 | grep 'unreachable.*metric 42760' || true)"
    c6="$(ip -6 route show table 254 | grep 'unreachable.*metric 42760' || true)"
    c7="$(find "$PUB_DIR" -maxdepth 1 -name '*.dev' 2>/dev/null || true)"
    [[ -n "$c1" ]] && owned=1
    [[ -n "$c2" ]] && owned=1
    [[ -n "$c3" ]] && owned=1
    [[ -n "$c4" ]] && owned=1
    [[ -n "$c5" ]] && owned=1
    [[ -n "$c6" ]] && owned=1
    [[ -n "$c7" ]] && owned=1
    if [[ "${KIKIMORA_RESIDUE_DEBUG:-0}" == "1" && "$owned" == "1" ]]; then
        echo "residue: t4=[$c1] t6=[$c2] r4=[$c3] r6=[$c4] p4=[$c5] p6=[$c6] dev=[$c7]" >&2
    fi
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
wait_roles_state Ready "$RECONNECT_TIMEOUT" "${ROLE_ARR[0]}" || fail "${ROLE_ARR[0]} did not reconnect"
pass "${ROLE_ARR[0]} reconnected to Ready"

# ---------------------------------------------------------------------------
# G3. DisconnectAll: zero owned state, then idempotent
# ---------------------------------------------------------------------------
echo "--- G3. DisconnectAll zero-residual ---"
core disconnect-all >/dev/null
wait_roles_state Stopped 30 "${ROLE_ARR[@]}" || fail "roles did not stop"
if ! wait_owned_state_gone 10; then
    capture_net "$OUT_DIR/net-after-disconnectall.txt"
    fail "Kikimora-owned network state survived DisconnectAll (see net-after-disconnectall.txt)"
fi
systemctl is-active --quiet kikimora-core || fail "core must stay alive after DisconnectAll"
pass "DisconnectAll left zero owned routes/rules/parks/publications; core still alive"

echo "--- G3b. Idempotent DisconnectAll ---"
wait_owned_state_gone 10 || true
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
wait_roles_state Ready "$RECONNECT_TIMEOUT" "${ROLE_ARR[@]}" || fail "roles did not reconnect after DisconnectAll"
pass "idle core reconnected all roles to Ready"

# ---------------------------------------------------------------------------
# G5. Graceful stop releases the same state; DNS owner goes with the core
# ---------------------------------------------------------------------------
echo "--- G5. Graceful stop equivalence ---"
[[ -e "$MARKER" ]] && fail "restart marker must not exist before the plain stop"
DNS_WAS_ACTIVE=0
if systemctl is-active --quiet kikimora-leshy-dns.service; then
    DNS_WAS_ACTIVE=1
fi
systemctl stop kikimora-core.service
if ! wait_owned_state_gone 10; then
    capture_net "$OUT_DIR/net-after-stop.txt"
    fail "graceful stop left owned network state (see net-after-stop.txt)"
fi
systemctl is-active --quiet kikimora-core && fail "core still active after stop"
if (( DNS_WAS_ACTIVE )); then
    systemctl is-active --quiet kikimora-leshy-dns.service && fail "DNS owner still active after the core stopped"
    pass "DNS owner stopped with the core (PartOf)"
else
    SKIPPED+="G5-PartOf(DNS-owner-not-enabled) "
    pass "DNS owner was not enabled; skip the PartOf observation"
fi
pass "graceful stop released the network exactly like DisconnectAll"
systemctl start kikimora-core.service
wait_roles_state Ready "$RECONNECT_TIMEOUT" "${ROLE_ARR[@]}" || fail "roles did not reconnect after the graceful stop cycle"
pass "roles reconnected after start"

# ---------------------------------------------------------------------------
# G6. Restart with the marker keeps the transient fail-closed contract
# ---------------------------------------------------------------------------
echo "--- G6. Marker restart ---"
touch "$MARKER"
systemctl restart kikimora-core.service
wait_roles_state Ready "$RECONNECT_TIMEOUT" "${ROLE_ARR[@]}" || fail "roles did not recover after the marker restart"
[[ -e "$MARKER" ]] && fail "ExecStopPost must remove the restart marker after the stop"
pass "marker restart recovered to Ready and the marker was cleaned up"

# ---------------------------------------------------------------------------
# G7. UDP/53-drop TCP/53-pass DNS fixture
# ---------------------------------------------------------------------------
echo "--- G7. DNS transport fixture ---"
if [[ "${SKIP_DNS_FIXTURE:-0}" == "1" ]]; then
    SKIPPED+="G7(SKIP_DNS_FIXTURE) "
    echo "SKIP: DNS transport fixture disabled by SKIP_DNS_FIXTURE"
else
    # The cleanup restores this snapshot, so the operator's config survives
    # every exit path — including failures inside the gate.
    CONFIG_BACKUP="$OUT_DIR/config.toml.bak"
    cp "$LESHY_CONFIG" "$CONFIG_BACKUP"
    iptables -A OUTPUT -p udp --dport 53 ! -o lo -j REJECT
    core_local generate-leshy --dns-protocol tcp >/dev/null
    grep -q 'dns_protocol = "tcp"' "$LESHY_CONFIG" || fail "dns_protocol=tcp did not survive regeneration"
    systemctl restart leshy.service
    if dig_leshy "$OUT_DIR/dig-tcp.txt"; then
        pass "Leshy resolves over TCP while upstream UDP/53 is blocked"
    else
        fail "Leshy failed to resolve with dns_protocol=tcp under UDP block (see dig-tcp.txt)"
    fi
    # Revert: restore the operator's original config and prove resolution.
    cp "$CONFIG_BACKUP" "$LESHY_CONFIG"
    CONFIG_BACKUP=""
    systemctl restart leshy.service
    if dig_leshy "$OUT_DIR/dig-revert.txt"; then
        pass "fixture reverted, Leshy resolves on the restored config"
    else
        fail "Leshy stopped resolving after the fixture revert"
    fi
fi

# ---------------------------------------------------------------------------
# G8. External VPN handoff — recorded, not executed here
# ---------------------------------------------------------------------------
echo "--- G8. External VPN handoff ---"
SKIPPED+="G8(external-VPN-client-not-provisioned) "
echo "SKIP: external system-wide VPN handoff needs a second VPN client provisioned"
echo "      on the VM (issue #31 gate 6); run it separately before workstation cutover."

echo "=== ALL GATES PASSED${SKIPPED:+ (skipped: $SKIPPED)} ==="
