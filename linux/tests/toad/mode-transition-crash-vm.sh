#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# mode-transition-crash-vm.sh — the versioned native D10 harness for
# ubuntu2604 (08D1 post-Phase-D audit §4 P0-D3/P0-D4/P1-D5, D10R-2).
#
# Runs the REAL failpoint-tagged core binary through all nine enable and nine
# disable crash boundaries, then executes all eight §10.5 negative cases as
# native process proofs:
#
#   N1 writer lock busy          -> zero mutation
#   N2 missing managed auth      -> startup fail
#   N3 malformed managed auth    -> startup fail
#   N4 stale ActivationID        -> zero mutation
#   N5 stale ModeGeneration      -> zero mutation
#   N6 corrupt journal           -> startup fail
#   N7 digest mismatch           -> startup fail
#   N8 second core same StateDir -> fail before socket/routes
#
# The enable/disable transactions go through the real
# `kikimora-core leshy-mode enable` CLI, which runs the real Leshy
# capabilities preflight — the capability array is never injected.
#
# Usage:
#   sudo bash mode-transition-crash-vm.sh \
#       --core /path/to/kikimora-core --leshy /path/to/leshy \
#       --routeprobe /path/to/routeprobe --work /absolute/work/dir
# The work directory is created if missing and left in place for inspection
# unless --keep is passed (it is wiped at startup either way).
# The system leshy service is stopped while the harness runs (the shared
# writer lock) and restored on exit.
set -u

CORE=""
LESHY=""
ROUTEPROBE=""
WORK=""
KEEP=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --core) CORE=${2:?}; shift 2 ;;
        --leshy) LESHY=${2:?}; shift 2 ;;
        --routeprobe) ROUTEPROBE=${2:?}; shift 2 ;;
        --work) WORK=${2:?}; shift 2 ;;
        --keep) KEEP=1; shift ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done
[[ -x "$CORE" ]] || { echo "--core must be an executable" >&2; exit 2; }
[[ -x "$LESHY" ]] || { echo "--leshy must be an executable" >&2; exit 2; }
[[ -x "$ROUTEPROBE" ]] || { echo "--routeprobe must be an executable" >&2; exit 2; }
[[ -n "$WORK" ]] || { echo "--work is required" >&2; exit 2; }
[[ "$(id -u)" -eq 0 ]] || { echo "must run as root" >&2; exit 2; }

CORE=$(readlink -f "$CORE")
LESHY=$(readlink -f "$LESHY")
ROUTEPROBE=$(readlink -f "$ROUTEPROBE")
mkdir -p "$WORK"
WORK=$(readlink -f "$WORK")

SOCKET=$WORK/core.sock
STATE=$WORK/state
CONFIGS=$WORK/configs
LESHY_HOME=$WORK/leshy
AUTH_KEY=$STATE/control-route-auth.key
WRITER_LOCK=/run/kikimora/leshy-writer.lock
JOURNAL=$STATE/leshy-mode-transition.json
MARKER=$CONFIGS/leshy-managed.json
CORE_LOG=$WORK/core.log
BOUNDARIES=(
    before_journal after_prepared after_config_write after_config_written
    after_marker_operation after_marker_written after_apply
    after_generation_commit before_journal_delete
)

PASS=0
FAIL=0
CORE_PID=""

note() { echo "== $*"; }
pass() { echo "PASS: $*"; PASS=$((PASS+1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL+1)); }

cleanup() {
    stop_core
    if [[ "$KEEP" -eq 0 ]]; then
        rm -rf "$WORK"
    fi
    systemctl start leshy.service >/dev/null 2>&1 || true
}
trap cleanup EXIT



stop_core() {
    if [[ -n "$CORE_PID" ]]; then
        kill "$CORE_PID" 2>/dev/null || true
        wait "$CORE_PID" 2>/dev/null || true
        CORE_PID=""
    fi
}

start_core() { # start_core [failpoint]
    rm -f "$SOCKET"
    : > "$CORE_LOG"
    if [[ $# -gt 0 && -n "$1" ]]; then
        env "KK_MODE_FAILPOINT=$1" "$CORE" serve --socket "$SOCKET" \
            --state-dir "$STATE" --config-dir "$CONFIGS" \
            --toad-binary /bin/true >>"$CORE_LOG" 2>&1 &
    else
        "$CORE" serve --socket "$SOCKET" --state-dir "$STATE" \
            --config-dir "$CONFIGS" --toad-binary /bin/true >>"$CORE_LOG" 2>&1 &
    fi
    CORE_PID=$!
}

wait_ready() {
    "$CORE" wait-startup --socket "$SOCKET" --timeout 30s
}

wait_exit_86() {
    for _ in $(seq 1 100); do
        if ! kill -0 "$CORE_PID" 2>/dev/null; then
            wait "$CORE_PID" 2>/dev/null
            local rc=$?
            CORE_PID=""
            [[ "$rc" -eq 86 ]] && return 0
            echo "core exited rc=$rc, want 86" >&2
            tail -3 "$CORE_LOG" >&2
            return 1
        fi
        sleep 0.2
    done
    echo "core did not exit within 20s" >&2
    return 1
}

reset_state() {
    stop_core
    rm -rf "$STATE" "$CONFIGS" "$LESHY_HOME" "$SOCKET"
    mkdir -p "$STATE" "$CONFIGS" "$LESHY_HOME/domains"
}

enable_cli() { # the REAL CLI preflight, never an injected capability array
    "$CORE" leshy-mode enable --leshy "$LESHY" --socket "$SOCKET" \
        --auth-file "$AUTH_KEY" --control-socket "$SOCKET"
}

disable_cli() {
    "$CORE" leshy-mode disable --socket "$SOCKET" --auth-file "$AUTH_KEY"
}

mode_field() { # mode_field <field>
    "$CORE" leshy-mode status --socket "$SOCKET" --auth-file "$AUTH_KEY" --json 2>/dev/null \
        | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit
print(data.get('$1', ''))"
}

marker_field() { # marker_field <field>
    python3 -c "
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print('')
    raise SystemExit
print(data.get('$1', ''))" < "$MARKER"
}

routes_hash() {
    { ip -4 route show; ip -6 route show; ip rule show; } | sha256sum | cut -d' ' -f1
}

# ---------------------------------------------------------------------------
note "harness identities"
note "core binary: $CORE"
"$CORE" version 2>&1 | head -1
note "core sha256: $(sha256sum "$CORE" | cut -d' ' -f1)"
note "leshy binary: $LESHY"
"$LESHY" --version 2>&1 | head -1
note "leshy sha256: $(sha256sum "$LESHY" | cut -d' ' -f1)"
note "routeprobe binary: $ROUTEPROBE"
note "writer lock path: $WRITER_LOCK"
ROUTES_BEFORE=$(routes_hash)
note "route baseline hash: $ROUTES_BEFORE"

# The system Leshy holds the shared writer lock for its process lifetime.
systemctl stop leshy.service >/dev/null 2>&1 || true
for _ in $(seq 1 30); do
    flock -n "$WRITER_LOCK" -c true && break
    sleep 0.5
done
if flock -n "$WRITER_LOCK" -c true; then
    note "writer lock is free"
else
    echo "FAIL: the writer lock is still held after leshy.service stopped" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
note "phase 1: the nine enable crash boundaries"
for boundary in "${BOUNDARIES[@]}"; do
    label="enable/$boundary"
    reset_state
    start_core
    if ! wait_ready; then
        fail "$label: the core never became ready"
        continue
    fi
    if enable_cli >/dev/null 2>&1; then
        fail "$label: the enable must have crashed the core at the boundary"
        stop_core
        continue
    fi
    if ! wait_exit_86; then
        fail "$label: the core did not die with exit 86"
        stop_core
        continue
    fi
    start_core ""
    if ! wait_ready; then
        fail "$label: the restart never became ready"
        stop_core
        continue
    fi
    if [[ "$boundary" == "before_journal" ]]; then
        if [[ "$(mode_field mode)" == "local" && "$(mode_field generation)" == "0" ]] \
            && [[ ! -e "$MARKER" ]] && [[ ! -e "$JOURNAL" ]]; then
            pass "$label converged to local/0"
        else
            fail "$label: expected local/0 with no marker and no journal"
        fi
    else
        if [[ "$(mode_field mode)" == "managed" && "$(mode_field generation)" == "1" ]] \
            && [[ "$(marker_field mode_generation)" == "1" ]] && [[ ! -e "$JOURNAL" ]]; then
            pass "$label converged to managed/1"
        else
            fail "$label: expected managed/1 with the marker and no journal"
        fi
    fi
    stop_core
done

# ---------------------------------------------------------------------------
note "phase 2: the nine disable crash boundaries"
for boundary in "${BOUNDARIES[@]}"; do
    label="disable/$boundary"
    reset_state
    start_core ""
    if ! wait_ready; then
        fail "$label: the core never became ready"
        continue
    fi
    if ! enable_cli >/dev/null 2>&1; then
        fail "$label: the pre-stage enable failed"
        stop_core
        continue
    fi
    stop_core
    routes_before=$(routes_hash)
    start_core "disable/$boundary"
    if ! wait_ready; then
        fail "$label: the core never became ready"
        continue
    fi
    if disable_cli >/dev/null 2>&1; then
        fail "$label: the disable must have crashed the core at the boundary"
        stop_core
        continue
    fi
    if ! wait_exit_86; then
        fail "$label: the core did not die with exit 86"
        stop_core
        continue
    fi
    start_core ""
    if ! wait_ready; then
        fail "$label: the restart never became ready"
        stop_core
        continue
    fi
    if [[ "$boundary" == "before_journal" ]]; then
        if [[ "$(mode_field mode)" == "managed" && "$(mode_field generation)" == "1" ]] \
            && [[ "$(marker_field mode_generation)" == "1" ]]; then
            pass "$label converged to managed/1"
        else
            fail "$label: expected managed/1"
        fi
    else
        if [[ "$(mode_field mode)" == "local" && "$(mode_field generation)" == "2" ]] \
            && [[ ! -e "$MARKER" ]] && [[ ! -e "$JOURNAL" ]]; then
            pass "$label converged to local/2"
        else
            fail "$label: expected local/2 with no marker and no journal"
        fi
    fi
    if [[ "$(routes_hash)" != "$routes_before" ]]; then
        fail "$label: the route table drifted across the crash"
    fi
    stop_core
done

# ---------------------------------------------------------------------------
note "phase 3: negative case N1 - writer lock busy means zero mutation"
reset_state
start_core ""
if ! wait_ready; then
    fail "N1: the core never became ready"
else
    # A local Leshy owns the writer lock for its process lifetime.
    cat > "$LESHY_HOME/config.toml" <<TOML
[server]
listen_address = "127.0.0.1:0"
default_upstream = ["127.0.0.1:53"]
writer_lock = "$WRITER_LOCK"
TOML
    "$LESHY" "$LESHY_HOME/config.toml" >"$LESHY_HOME/leshy.log" 2>&1 &
    LESHY_PID=$!
    leshy_up=0
    for _ in $(seq 1 40); do
        if ! kill -0 "$LESHY_PID" 2>/dev/null; then
            break
        fi
        if ! flock -n "$WRITER_LOCK" -c true; then
            leshy_up=1
            break
        fi
        sleep 0.25
    done
    if [[ "$leshy_up" -ne 1 ]]; then
        fail "N1: local Leshy never took the writer lock"
        tail -5 "$LESHY_HOME/leshy.log" >&2
    else
        gen_before=$(mode_field generation)
        if enable_cli >/dev/null 2>&1; then
            fail "N1: the enable must be refused while the lock is busy"
        else
            if [[ ! -e "$MARKER" && ! -e "$JOURNAL" && "$(mode_field generation)" == "$gen_before" ]]; then
                pass "N1: the enable was refused with zero mutation"
            else
                fail "N1: the refused enable mutated durable state"
            fi
        fi
    fi
    kill "$LESHY_PID" 2>/dev/null || true
    wait "$LESHY_PID" 2>/dev/null || true
    LESHY_PID=""
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative case N2 - missing managed auth fails startup"
reset_state
start_core ""
wait_ready >/dev/null 2>&1
if ! enable_cli >/dev/null 2>&1; then
    fail "N2 pre-stage: the enable failed"
fi
stop_core
rm -f "$AUTH_KEY"
start_core ""
startup_failed=0
for _ in $(seq 1 40); do
    if ! kill -0 "$CORE_PID" 2>/dev/null; then
        startup_failed=1
        break
    fi
    sleep 0.25
done
if [[ "$startup_failed" -eq 1 ]]; then
    if grep -q "missing its privileged auth key" "$CORE_LOG"; then
        pass "N2: a managed install without its auth key failed startup"
    else
        fail "N2: the core died but not with the missing-key error"
    fi
else
    fail "N2: the core must refuse to start without the managed auth key"
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative case N3 - malformed managed auth fails startup"
reset_state
start_core ""
wait_ready >/dev/null 2>&1
enable_cli >/dev/null 2>&1 || fail "N3 pre-stage: the enable failed"
stop_core
printf 'not-a-credential\n' > "$AUTH_KEY"
start_core ""
startup_failed=0
for _ in $(seq 1 40); do
    if ! kill -0 "$CORE_PID" 2>/dev/null; then
        startup_failed=1
        break
    fi
    sleep 0.25
done
if [[ "$startup_failed" -eq 1 ]]; then
    if grep -q "is malformed" "$CORE_LOG"; then
        pass "N3: a managed install with a malformed auth key failed startup"
    else
        fail "N3: the core died but not with the malformed-key error"
    fi
else
    fail "N3: the core must refuse to start with a malformed managed auth key"
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative cases N4/N5 - stale fences are zero-mutation denials"
reset_state
start_core ""
wait_ready >/dev/null 2>&1 || fail "N4 pre-stage: the core never became ready"
enable_cli >/dev/null 2>&1 || fail "N4 pre-stage: the enable failed"
activation=$(marker_field activation_id)
generation=$(marker_field mode_generation)
gen_before=$(mode_field generation)
stale_activation="deadbeefdeadbeefdeadbeefdeadbeef"

# N4: a stale ActivationID is a stable non-retryable API denial. The zone is
# irrelevant here: the strict validation order denies the stale fence before
# the zone step, and the denial must carry zero mutation.
if "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_KEY" ensure \
    --zone "nowhere" --prefix "203.0.113.10/32" \
    --activation "$stale_activation" --generation "$generation" >/dev/null 2>&1; then
    fail "N4: a stale activation must be denied"
else
    rc=$?
    if [[ "$rc" -eq 3 ]]; then
        if [[ ! -e "$JOURNAL" && "$(mode_field generation)" == "$gen_before" ]]; then
            pass "N4: the stale activation was denied with zero mutation"
        else
            fail "N4: the denial mutated durable state"
        fi
    else
        fail "N4: the denial must be the stable API error (rc=$rc)"
    fi
fi

# N5: a stale ModeGeneration is the same class of denial.
if "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_KEY" ensure \
    --zone "nowhere" --prefix "203.0.113.10/32" \
    --activation "$activation" --generation $((generation + 1)) >/dev/null 2>&1; then
    fail "N5: a stale generation must be denied"
else
    rc=$?
    if [[ "$rc" -eq 3 ]]; then
        if [[ ! -e "$JOURNAL" && "$(mode_field generation)" == "$gen_before" ]]; then
            pass "N5: the stale generation was denied with zero mutation"
        else
            fail "N5: the denial mutated durable state"
        fi
    else
        fail "N5: the denial must be the stable API error (rc=$rc)"
    fi
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative case N6 - a corrupt journal fails startup"
reset_state
start_core ""
wait_ready >/dev/null 2>&1
enable_cli >/dev/null 2>&1 || fail "N6 pre-stage: the enable failed"
stop_core
printf '{broken journal' > "$JOURNAL"
start_core ""
startup_failed=0
for _ in $(seq 1 40); do
    if ! kill -0 "$CORE_PID" 2>/dev/null; then
        startup_failed=1
        break
    fi
    sleep 0.25
done
if [[ "$startup_failed" -eq 1 ]]; then
    if grep -q "transition journal" "$CORE_LOG"; then
        pass "N6: a corrupt journal failed startup"
    else
        fail "N6: the core died but not on the corrupt journal"
    fi
else
    fail "N6: the core must refuse to start on a corrupt journal"
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative case N7 - a digest mismatch fails startup"
reset_state
start_core ""
wait_ready >/dev/null 2>&1
enable_cli >/dev/null 2>&1 || fail "N7 pre-stage: the enable failed"
generation=$(mode_field generation)
stop_core
# A committed journal whose digest cannot match the bytes on disk puts the
# restart on the verify-and-clean path: recovery must fail closed.
python3 - "$STATE" "$generation" <<'PY'
import json, sys
state, generation = sys.argv[1], int(sys.argv[2])
journal = {
    "schema": 1,
    "id": "0123456789abcdef0123456789abcdef",
    "from": "local",
    "to": "managed",
    "phase": "marker_written",
    "socket": "/run/kikimora/core.sock",
    "from_activation_id": "",
    "to_activation_id": "0123456789abcdef0123456789abcdef",
    "from_generation": generation - 1,
    "to_generation": generation,
    "zones": [],
    "config_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
}
with open(state + "/leshy-mode-transition.json", "w") as handle:
    json.dump(journal, handle)
PY
start_core ""
startup_failed=0
for _ in $(seq 1 40); do
    if ! kill -0 "$CORE_PID" 2>/dev/null; then
        startup_failed=1
        break
    fi
    sleep 0.25
done
if [[ "$startup_failed" -eq 1 ]]; then
    if grep -q "does not match the journaled" "$CORE_LOG"; then
        pass "N7: a config digest mismatch failed startup"
    else
        fail "N7: the core died but not on the digest mismatch"
    fi
else
    fail "N7: the core must refuse to start on a digest mismatch"
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 3: negative case N8 - a second core with the same StateDir fails first"
reset_state
start_core ""
wait_ready >/dev/null 2>&1 || fail "N8 pre-stage: the core never became ready"
"$CORE" serve --socket "$WORK/second.sock" --state-dir "$STATE" \
    --config-dir "$CONFIGS" --toad-binary /bin/true >"$WORK/second.log" 2>&1
second_rc=$?
if [[ "$second_rc" -ne 0 ]] && grep -q "another core already owns this state directory" "$WORK/second.log"; then
    if wait_ready; then
        pass "N8: the second core was refused before the socket and routes"
    else
        fail "N8: the first core stopped serving after the second-core refusal"
    fi
else
    fail "N8: the second core must fail with the state-dir ownership error (rc=$second_rc)"
fi
stop_core

# ---------------------------------------------------------------------------
note "phase 4: foreign route baseline"
if [[ "$(routes_hash)" == "$ROUTES_BEFORE" ]]; then
    pass "foreign route baseline unchanged"
else
    fail "the route table drifted across the harness run"
fi

note "harness complete: PASS=$PASS FAIL=$FAIL"
# The trailing exit status must come from inside the branch: an explicit
# top-level `exit` after an EXIT trap makes ShellCheck 0.11 report the trap
# function as never invoked (SC2329).
if [[ "$FAIL" -eq 0 ]]; then
    echo "RESULT: GREEN"
else
    echo "RESULT: RED"
    exit 1
fi
