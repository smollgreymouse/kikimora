#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-mode-cutover-vm.sh — Phase E VM acceptance (08D1 post-Phase-D audit
# E0/E3): the INSTALLED package performs the one-writer managed cutover on a
# real VM, against the real systemd services, the real writer lock, the real
# Leshy runtime and a real connected role.
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
#   F  managed Leshy starts on the generated managed configuration and owns
#      the writer fence while it runs;
#   G  the canary domain queried through the installed DNS path makes the
#      selected registry NON-EMPTY, Disable is refused, and only the
#      DisconnectAll empty proof (registered == 0, Parking empty) lets the
#      Disable succeed;
#   H  local Leshy starts again on the regenerated local configuration.
#
# The script establishes its own known source mode: an install found in
# managed mode (a previous acceptance run) is rolled back to local through
# the real transaction before row C. The script leaves the install in local
# mode by design and restores the role's routing list.
#
# Usage (on the VM, as root) — both arguments are MANDATORY:
#   sudo bash linux/tests/toad/managed-mode-cutover-vm.sh <role> <canary-domain>
# The canary domain must be publicly resolvable (the row proves the real
# resolution -> Ensure -> registry flow through the connected tunnel).
# A role that cannot reach Ready is a RED result, never a skip.
set -u

CORE=/usr/local/bin/kikimora-core
CORE_SERVICE=kikimora-core.service
LESHY_SERVICE=leshy.service
CORE_SOCKET=/run/kikimora/core.sock
STATE_DIR=/var/lib/kikimora/core
AUTH_KEY=$STATE_DIR/control-route-auth.key
TOADS_DIR=/etc/kikimora/toads
ROUTING_DIR=/etc/kikimora/leshy/domains
MARKER=$TOADS_DIR/leshy-managed.json
LESHY_CONFIG=/etc/kikimora/leshy/config.toml
WRITER_LOCK=/run/kikimora/leshy-writer.lock
JOURNAL=$STATE_DIR/leshy-mode-transition.json
LISTEN_ADDRESS=127.0.0.1:53053

ROLE=${1:-}
CANARY=${2:-}
if [[ -z "$ROLE" || -z "$CANARY" ]]; then
    echo "usage: $0 <role> <canary-domain> — both arguments are mandatory" >&2
    exit 2
fi
LIST_FILE=$ROUTING_DIR/$ROLE.txt

PASS=0
FAIL=0

note() { echo "== $*"; }
pass() { echo "PASS: $*"; PASS=$((PASS+1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL+1)); }

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

mode_field() { # mode_field mode|generation|activation_id_fingerprint
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
        --auth-file "$AUTH_KEY" --control-socket "$CORE_SOCKET"
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

role_field() { # role_field <role> <json field>
    "$CORE" status --socket "$CORE_SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
try:
    roles = json.load(sys.stdin).get('roles', [])
except Exception:
    print('')
    raise SystemExit
role = next((r for r in roles if r['id'] == '$1'), None)
if role is None:
    print('')
    raise SystemExit
value = role
for part in '$2'.split('.'):
    value = value.get(part, '') if isinstance(value, dict) else ''
print(value)"
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

dns_query() { # dns_query <name> — returns the DNS rcode via $? (0 = NOERROR)
    python3 - "$LISTEN_ADDRESS" "$1" <<'PY'
import random
import socket
import struct
import sys

server, name = sys.argv[1], sys.argv[2]
host, port = server.rsplit(":", 1)
tid = random.randint(0, 65535)
header = struct.pack(">HHHHHH", tid, 0x0100, 1, 0, 0, 0)
question = b"".join(bytes([len(p)]) + p.encode() for p in name.split(".")) + b"\x00"
packet = header + question + struct.pack(">HH", 1, 1)
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.settimeout(8)
try:
    sock.sendto(packet, (host, int(port)))
    data, _ = sock.recvfrom(2048)
    raise SystemExit(data[3] & 0xF)
except socket.timeout:
    raise SystemExit(9)
finally:
    sock.close()
PY
}

# ---------------------------------------------------------------------------
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
if [ "$(mode_field mode)" = managed ]; then
    note "the install is committed managed (a previous acceptance state); rolling back to local through the real transaction"
    "$CORE" disconnect-all --socket "$CORE_SOCKET" >/dev/null 2>&1 || true
    for _ in $(seq 1 30); do
        roles_settled && break
        sleep 1
    done
    # The 11.5 rollback order: stop every Leshy instance first - the running
    # Leshy holds the writer lock for its process lifetime in BOTH modes.
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
if [ "$(mode_field mode)" != local ]; then
    fail "baseline: the install is neither local nor rollable: $(mode_field mode)"
    exit 1
fi
pass "baseline: the install is local at generation $(mode_field generation)"
systemctl stop "$LESHY_SERVICE" >/dev/null 2>&1 || true
systemctl start "$LESHY_SERVICE"
for _ in $(seq 1 30); do
    [ "$(systemctl is-active "$LESHY_SERVICE")" = active ] && break
    sleep 0.5
done
check "baseline: local Leshy is active" systemctl is-active --quiet "$LESHY_SERVICE"
GEN_BEFORE=$(mode_field generation)
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
    && [ "$(mode_field generation)" = "$GEN_BEFORE" ] \
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
GEN_MANAGED=$(mode_field generation)
FP_MANAGED=$(mode_field activation_id_fingerprint)
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
note "row F: managed Leshy starts and owns the writer fence"
systemctl start "$LESHY_SERVICE"
for _ in $(seq 1 60); do
    [ "$(systemctl is-active "$LESHY_SERVICE")" = active ] && break
    sleep 0.5
done
check "F: managed Leshy is active" systemctl is-active --quiet "$LESHY_SERVICE"
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
note "row G: the canary query fills the selected registry; Disable refuses until it drains"
if [ ! -e "$TOADS_DIR/$ROLE.toml" ]; then
    fail "G: role config $TOADS_DIR/$ROLE.toml does not exist (the row is mandatory)"
fi
LIST_BEFORE=$(cat "$LIST_FILE" 2>/dev/null)
if printf '%s\n' "$CANARY" >>"$LIST_FILE" && ! grep -qx "$CANARY" "$LIST_FILE"; then
    fail "G: could not add the canary domain to $LIST_FILE"
fi
# The core owns the generated configuration: rewriting the list through the
# API regenerates the managed config and reloads the managed Leshy.
python3 - "$CORE_SOCKET" "$ROLE" "$LIST_FILE" <<'PY' >/dev/null 2>&1
import json
import socket
import struct
import sys

path, role, list_file = sys.argv[1], sys.argv[2], sys.argv[3]


def call(request):
    payload = json.dumps(request).encode()
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(15)
    try:
        sock.connect(path)
        sock.sendall(struct.pack(">I", len(payload)) + payload)
        header = b""
        while len(header) < 4:
            chunk = sock.recv(4 - len(header))
            if not chunk:
                raise RuntimeError("closed before header")
            header += chunk
        size = struct.unpack(">I", header)[0]
        data = b""
        while len(data) < size:
            chunk = sock.recv(size - len(data))
            if not chunk:
                raise RuntimeError("closed mid-frame")
            data += chunk
        return json.loads(data)
    finally:
        sock.close()


content = open(list_file).read()
response = call({"version": 3, "id": "cutover", "method": "SetRoutingList",
                 "list": role, "content": content})
raise SystemExit(0 if response.get("ok") else 1)
PY
setlist_rc=$?
if [ "$setlist_rc" -ne 0 ]; then
    fail "G: SetRoutingList through the API failed"
fi
"$CORE" connect --socket "$CORE_SOCKET" --role "$ROLE" >/dev/null 2>&1 || true
role_ready=0
for _ in $(seq 1 120); do
    [ "$(role_field "$ROLE" state)" = "Ready" ] && role_ready=1 && break
    sleep 1
done
if [ "$role_ready" -ne 1 ]; then
    fail "G: role $ROLE never reached Ready — the mandatory registry row is RED"
fi
# The canary query through the installed DNS path: the managed Leshy must
# turn the resolution into a selected route owned by the core.
rcode=9
for _ in $(seq 1 10); do
    dns_query "$CANARY" && rcode=0 || rcode=$?
    registered=$(role_field "$ROLE" selected_routes.registered)
    [ -n "$registered" ] && [ "$registered" -gt 0 ] && break
    sleep 1
done
if [ -n "$registered" ] && [ "$registered" -gt 0 ]; then
    pass "G: the canary query filled the selected registry ($registered record(s), DNS rcode=$rcode)"
    # The refusal sub-check is only meaningful with a non-empty registry;
    # the drain and success sub-steps below always run so the install ends
    # in local mode for row H.
    systemctl stop "$LESHY_SERVICE"
    for _ in $(seq 1 30); do
        writer_lock_free && break
        sleep 0.5
    done
    if disable_managed >/dev/null 2>&1; then
        fail "G: Disable must be refused while the selected registry is non-empty"
    else
        pass "G: Disable refused while the selected registry is non-empty"
    fi
    "$CORE" disconnect --socket "$CORE_SOCKET" --role "$ROLE" >/dev/null 2>&1 || true
else
    fail "G: the selected registry stayed empty (registered='${registered:-none}', DNS rcode=$rcode)"
fi
"$CORE" disconnect-all --socket "$CORE_SOCKET" >/dev/null 2>&1 || true
drained=0
for _ in $(seq 1 60); do
    registered=$(role_field "$ROLE" selected_routes.registered)
    parked=$(role_field "$ROLE" parking.count)
    active=$(role_field "$ROLE" parking.active)
    if [ "$registered" = "0" ] && { [ "$parked" = "0" ] || [ "$parked" = "" ]; } \
        && { [ "$active" = "False" ] || [ "$active" = "" ]; }; then
        drained=1
        break
    fi
    sleep 1
done
if [ "$drained" -eq 1 ]; then
    pass "G: the DisconnectAll empty proof (registered=0, Parking empty)"
else
    fail "G: the registry never drained (registered=${registered:-?} parked=${parked:-?} active=${active:-?})"
fi
for _ in $(seq 1 30); do
    writer_lock_free && break
    sleep 0.5
done
if disable_managed >/dev/null 2>&1; then
    pass "G: Disable succeeds after the empty proof"
else
    fail "G: Disable failed after the empty proof"
fi
GEN_LOCAL=$(mode_field generation)
if [ -n "$GEN_LOCAL" ] && [ "$GEN_LOCAL" != "$GEN_MANAGED" ] && [ ! -e "$MARKER" ] \
    && ! grep -q 'routing_backend' "$LESHY_CONFIG"; then
    pass "G: the rollback converged to local/$GEN_LOCAL with the marker gone"
else
    fail "G: the rollback did not converge (gen=$GEN_LOCAL marker=$( [ -e "$MARKER" ] && echo present || echo absent))"
fi
# Restore the role's list and reconnect nothing: the script leaves the
# roles stopped and the routing list as it found it.
if printf '%s\n' "$LIST_BEFORE" >"$LIST_FILE"; then
    python3 - "$CORE_SOCKET" "$ROLE" "$LIST_FILE" <<'PY' >/dev/null 2>&1
import json
import socket
import struct
import sys

path, role, list_file = sys.argv[1], sys.argv[2], sys.argv[3]


def call(request):
    payload = json.dumps(request).encode()
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(15)
    try:
        sock.connect(path)
        sock.sendall(struct.pack(">I", len(payload)) + payload)
        header = b""
        while len(header) < 4:
            chunk = sock.recv(4 - len(header))
            if not chunk:
                raise RuntimeError("closed before header")
            header += chunk
        size = struct.unpack(">I", header)[0]
        data = b""
        while len(data) < size:
            chunk = sock.recv(size - len(data))
            if not chunk:
                raise RuntimeError("closed mid-frame")
            data += chunk
        return json.loads(data)
    finally:
        sock.close()


content = open(list_file).read()
response = call({"version": 3, "id": "cutover", "method": "SetRoutingList",
                 "list": role, "content": content})
raise SystemExit(0 if response.get("ok") else 1)
PY
    pass "G: the role's routing list restored"
else
    fail "G: the role's routing list could not be restored"
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

echo "== cutover acceptance complete: PASS=$PASS FAIL=$FAIL"
if [ "$FAIL" -eq 0 ]; then
    echo "RESULT: GREEN"
else
    echo "RESULT: RED"
    exit 1
fi
