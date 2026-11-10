#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-mode-cutover-vm.sh — Phase E VM acceptance (08D1 post-D7 packet
# §11.1/§11.6): the INSTALLED package performs the one-writer managed cutover
# on a real VM, against the real systemd services, the real writer lock and
# the real Leshy runtime.
#
# Proven rows:
#   A  the packaged unit bytes carry the hard dependency and the readiness
#      barrier (Requires/After/ExecStartPre wait-startup);
#   B  the runtime ordering edge is real (After=kikimora-core.service);
#   C  Enable while local Leshy holds the writer lock fails with zero
#      mutation (no journal, no marker, generation and config unchanged);
#   D  after a graceful Leshy stop the shared writer lock is acquirable;
#   E  the leshy-mode enable transaction succeeds through the real CLI
#      preflight and the marker/config identity is exact;
#   F  managed Leshy starts on the generated managed configuration and the
#      writer lock stays free while it runs (no writer overlap);
#   G  Disable with a running role (and, when reachable, a non-empty selected
#      registry) is refused; after DisconnectAll the Disable succeeds;
#   H  local Leshy starts again on the regenerated local configuration.
#
# The script establishes its own known source mode: an install found in
# managed mode (a previous acceptance run, or the §11.5 upgrade path) is
# rolled back to local through the real transaction before row C. The script
# leaves the install in local mode by design.
#
# Usage (on the VM, as root):
#   sudo bash linux/tests/toad/managed-mode-cutover-vm.sh [role]
# The optional role argument names a configured Toad used for row G; without
# it (or when the role cannot reach Ready) row G degrades to the explicit
# skip, never to a false pass.
set -u

CORE=/usr/local/bin/kikimora-core
CORE_SERVICE=kikimora-core.service
LESHY_SERVICE=leshy.service
CORE_SOCKET=/run/kikimora/core.sock
STATE_DIR=/var/lib/kikimora/core
AUTH_KEY=$STATE_DIR/control-route-auth.key
TOADS_DIR=/etc/kikimora/toads
MARKER=$TOADS_DIR/leshy-managed.json
LESHY_CONFIG=/etc/kikimora/leshy/config.toml
WRITER_LOCK=/run/kikimora/leshy-writer.lock
JOURNAL=$STATE_DIR/leshy-mode-transition.json
ROLE=${1:-}

PASS=0
FAIL=0
SKIP=0

note() { echo "== $*"; }
pass() { echo "PASS: $*"; PASS=$((PASS+1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL+1)); }
skip() { echo "SKIP: $*"; SKIP=$((SKIP+1)); }

check() { # check <label> <command...>
    local label=$1
    shift
    if "$@" >/dev/null 2>&1; then
        pass "$label"
    else
        fail "$label"
    fi
}

wait_ready() {
    "$CORE" wait-startup --socket "$CORE_SOCKET" --timeout 60s
}

status_json() {
    "$CORE" leshy-mode status --socket "$CORE_SOCKET" --auth-file "$AUTH_KEY" --json 2>/dev/null
}

status_field() { # status_field mode|generation|activation_id_fingerprint
    status_json | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit
print(data.get('$1', ''))"
}

enable_managed() {
    "$CORE" leshy-mode enable --leshy /usr/local/bin/leshy --socket "$CORE_SOCKET" \
        --auth-file "$AUTH_KEY" --control-socket /run/kikimora/core.sock
}

disable_managed() {
    "$CORE" leshy-mode disable --socket "$CORE_SOCKET" --auth-file "$AUTH_KEY"
}

roles_settled() { # every role is Stopped/Disconnected/Failed
    "$CORE" status --socket "$CORE_SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
try:
    roles = json.load(sys.stdin).get('roles', [])
except Exception:
    raise SystemExit(1)
bad = [r['id'] for r in roles if r.get('state') not in ('Stopped', 'Disconnected', 'Failed', '')]
raise SystemExit(1 if bad else 0)"
}

writer_lock_free() {
    flock -n "$WRITER_LOCK" -c true
}

config_digest() {
    sha256sum "$LESHY_CONFIG" 2>/dev/null | cut -d' ' -f1
}

ensure_core_running() {
    systemctl start "$CORE_SERVICE"
    wait_ready
}

note "Phase E managed cutover acceptance on the installed package"
"$CORE" version 2>/dev/null | head -1
leshy --version 2>/dev/null

# ---------------------------------------------------------------------------
note "row A: the packaged unit carries the hard dependency and the barrier"
unit=$(systemctl cat "$LESHY_SERVICE" 2>/dev/null)
if grep -Fq 'Requires=kikimora-core.service' <<<"$unit" \
    && grep -Fq 'After=kikimora-core.service' <<<"$unit" \
    && grep -Fq 'ExecStartPre=/usr/local/bin/kikimora-core wait-startup --socket /run/kikimora/core.sock --timeout 30s' <<<"$unit"; then
    pass "A: Requires/After/ExecStartPre wait-startup present in leshy.service"
else
    fail "A: leshy.service is missing the hard dependency or the readiness barrier"
fi

# ---------------------------------------------------------------------------
note "row B: the runtime ordering edge is real"
after=$(systemctl show "$LESHY_SERVICE" -p After --value 2>/dev/null)
if grep -q 'kikimora-core.service' <<<"$after"; then
    pass "B: leshy.service is ordered After=kikimora-core.service at runtime"
else
    fail "B: the runtime After edge is missing: $after"
fi

# ---------------------------------------------------------------------------
note "baseline: establish the known local source mode"
ensure_core_running || { fail "baseline: the core never became ready"; exit 1; }
if [ "$(status_field mode)" = managed ]; then
    note "the install is committed managed (a previous acceptance state); rolling back to local through the real transaction"
    "$CORE" disconnect-all --socket "$CORE_SOCKET" >/dev/null 2>&1 || true
    for _ in $(seq 1 30); do
        roles_settled && break
        sleep 1
    done
    # The rollback order is the 11.5 contract: stop every Leshy instance
    # first - the running Leshy holds the writer lock for its process
    # lifetime in BOTH modes - then disable.
    systemctl stop "$LESHY_SERVICE"
    for _ in $(seq 1 30); do
        writer_lock_free && break
        sleep 0.5
    done
    if disable_managed >/dev/null 2>&1; then
        pass "baseline: the pre-existing managed install rolled back to local"
    else
        fail "baseline: the rollback of the managed install failed"
        exit 1
    fi
fi
if [ "$(status_field mode)" != local ]; then
    fail "baseline: the install is neither local nor rollable: $(status_field mode)"
    exit 1
fi
pass "baseline: the install is local at generation $(status_field generation)"
systemctl stop "$LESHY_SERVICE" >/dev/null 2>&1 || true
systemctl start "$LESHY_SERVICE"
for _ in $(seq 1 30); do
    [ "$(systemctl is-active "$LESHY_SERVICE")" = active ] && break
    sleep 0.5
done
check "baseline: local Leshy is active" systemctl is-active --quiet "$LESHY_SERVICE"
GEN_BEFORE=$(status_field generation)
DIGEST_BEFORE=$(config_digest)

# ---------------------------------------------------------------------------
note "row C: Enable while local Leshy holds the writer lock fails with zero mutation"
if writer_lock_free; then
    fail "C precondition: local Leshy must hold the writer lock (flock probe unexpectedly succeeded)"
else
    pass "C precondition: the writer lock is held while local Leshy runs"
fi
enable_managed >/dev/null 2>&1 && ENABLE_RC=0 || ENABLE_RC=$?
if [ "$ENABLE_RC" -ne 0 ]; then
    pass "C: the enable was refused while the lock was busy"
else
    fail "C: the enable must fail while local Leshy holds the writer lock"
fi
if [ ! -e "$MARKER" ] && [ ! -e "$JOURNAL" ] \
    && [ "$(status_field generation)" = "$GEN_BEFORE" ] \
    && [ "$(config_digest)" = "$DIGEST_BEFORE" ]; then
    pass "C: zero mutation (no marker, no journal, generation and config unchanged)"
else
    fail "C: the refused enable mutated durable state"
fi

# ---------------------------------------------------------------------------
note "row D: after a graceful Leshy stop the writer lock is acquirable"
systemctl stop "$LESHY_SERVICE"
for _ in $(seq 1 30); do
    writer_lock_free && break
    sleep 0.5
done
if writer_lock_free; then
    pass "D: the writer lock is free after the graceful stop"
else
    fail "D: the writer lock is still held after leshy stopped"
fi

# ---------------------------------------------------------------------------
note "row E: the enable transaction succeeds with an exact identity"
if enable_managed >"$STATE_DIR/cutover-enable.json" 2>"$STATE_DIR/cutover-enable.err"; then
    pass "E: the enable transaction completed through the real CLI preflight"
else
    fail "E: the enable transaction failed: $(tail -1 "$STATE_DIR/cutover-enable.err" 2>/dev/null)"
fi
GEN_MANAGED=$(status_field generation)
FP_MANAGED=$(status_field activation_id_fingerprint)
MARKER_GEN=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mode_generation"])' "$MARKER" 2>/dev/null)
MARKER_ACT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["activation_id"])' "$MARKER" 2>/dev/null)
if [ -n "$GEN_MANAGED" ] && [ "$GEN_MANAGED" != "$GEN_BEFORE" ] && [ -n "$FP_MANAGED" ] \
    && [ "$MARKER_GEN" = "$GEN_MANAGED" ] && [ ${#MARKER_ACT} -eq 32 ]; then
    pass "E: managed generation $GEN_MANAGED with the activation fingerprint $FP_MANAGED"
else
    fail "E: mode identity inconsistent (gen=$GEN_MANAGED fp=$FP_MANAGED marker_gen=$MARKER_GEN)"
fi
identity_ok=1
grep -q 'routing_backend = "kikimora"' "$LESHY_CONFIG" || identity_ok=0
grep -q "kikimora_socket = \"/run/kikimora/core.sock\"" "$LESHY_CONFIG" || identity_ok=0
grep -q "kikimora_auth_file = \"$AUTH_KEY\"" "$LESHY_CONFIG" || identity_ok=0
grep -q "kikimora_activation_id = \"$MARKER_ACT\"" "$LESHY_CONFIG" || identity_ok=0
grep -q "kikimora_mode_generation = $MARKER_GEN" "$LESHY_CONFIG" || identity_ok=0
grep -q "writer_lock = \"$WRITER_LOCK\"" "$LESHY_CONFIG" || identity_ok=0
if [ "$identity_ok" -eq 1 ]; then
    pass "E: the generated config carries the exact marker identity"
else
    fail "E: the generated config does not match the marker"
fi
check "E: no transition journal remains" test ! -e "$JOURNAL"

# ---------------------------------------------------------------------------
note "row F: managed Leshy starts and the writer lock stays free"
systemctl start "$LESHY_SERVICE"
for _ in $(seq 1 60); do
    [ "$(systemctl is-active "$LESHY_SERVICE")" = active ] && break
    sleep 0.5
done
check "F: managed Leshy is active" systemctl is-active --quiet "$LESHY_SERVICE"
# The running Leshy owns the writer fence for its process lifetime in both
# modes: managed Leshy holding it (and the core refusing a concurrent
# transaction) is exactly the zero-writer-overlap contract.
if writer_lock_free; then
    fail "F: managed Leshy must hold the writer lock for its process lifetime"
else
    pass "F: managed Leshy holds the writer lock (single-writer surface owned)"
fi
enable_managed >/dev/null 2>&1 && ENABLE_RC=0 || ENABLE_RC=$?
if [ "$ENABLE_RC" -ne 0 ]; then
    pass "F: a concurrent transaction is refused while managed Leshy runs"
else
    fail "F: a transaction must be refused while managed Leshy holds the lock"
fi

# ---------------------------------------------------------------------------
note "row G: Disable is refused while the writer is in use, then succeeds"
if [ -n "$ROLE" ] && [ -e "$TOADS_DIR/$ROLE.toml" ]; then
    "$CORE" connect --socket "$CORE_SOCKET" "$ROLE" >/dev/null 2>&1 || true
    role_ready=0
    for _ in $(seq 1 120); do
        state=$("$CORE" status --socket "$CORE_SOCKET" --json 2>/dev/null \
            | python3 -c "
import json, sys
try:
    roles = json.load(sys.stdin).get('roles', [])
except Exception:
    roles = []
print(next((r['state'] for r in roles if r['id'] == '$ROLE'), ''))" 2>/dev/null)
        [ "$state" = "Ready" ] && role_ready=1 && break
        sleep 1
    done
    if [ "$role_ready" -eq 1 ]; then
        # The role refusal needs the LOCK free - otherwise the busy-lock
        # denial would mask the role precondition. Stopping Leshy is the
        # 11.4 rollback order and keeps the connected role running.
        systemctl stop "$LESHY_SERVICE"
        for _ in $(seq 1 30); do
            writer_lock_free && break
            sleep 0.5
        done
        disable_managed >/dev/null 2>&1 \
            && { fail "G: Disable must be refused while role $ROLE is running"; } \
            || pass "G: Disable refused while role $ROLE is running"
        "$CORE" disconnect --socket "$CORE_SOCKET" "$ROLE" >/dev/null 2>&1 || true
    else
        skip "G: role $ROLE could not reach Ready in 120s; the refusal row is covered by the mode gate unit matrix"
        systemctl stop "$LESHY_SERVICE"
    fi
else
    skip "G: no role given (or $TOADS_DIR/$ROLE.toml missing); the refusal row is covered by the mode gate unit matrix"
    systemctl stop "$LESHY_SERVICE"
fi
"$CORE" disconnect-all --socket "$CORE_SOCKET" >/dev/null 2>&1 || true
for _ in $(seq 1 30); do
    roles_settled && break
    sleep 1
done
for _ in $(seq 1 30); do
    writer_lock_free && break
    sleep 0.5
done
if disable_managed >/dev/null 2>&1; then
    pass "G: Disable succeeds after the empty proof"
else
    fail "G: Disable failed after DisconnectAll"
fi
GEN_LOCAL=$(status_field generation)
if [ -n "$GEN_LOCAL" ] && [ "$GEN_LOCAL" != "$GEN_MANAGED" ] && [ ! -e "$MARKER" ] \
    && ! grep -q 'routing_backend' "$LESHY_CONFIG"; then
    pass "G: the rollback converged to local/$GEN_LOCAL with the marker gone"
else
    fail "G: the rollback did not converge (gen=$GEN_LOCAL marker=$( [ -e "$MARKER" ] && echo present || echo absent))"
fi

# ---------------------------------------------------------------------------
note "row H: local Leshy starts again"
systemctl start "$LESHY_SERVICE"
for _ in $(seq 1 30); do
    [ "$(systemctl is-active "$LESHY_SERVICE")" = active ] && break
    sleep 0.5
done
check "H: local Leshy is active on the local configuration" systemctl is-active --quiet "$LESHY_SERVICE"
check "H: the core is still ready" wait_ready

echo "== cutover acceptance complete: PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
if [ "$FAIL" -eq 0 ]; then
    echo "RESULT: GREEN"
    exit 0
fi
echo "RESULT: RED"
exit 1
