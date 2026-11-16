#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# guest-run-suites.sh — installed-Linux desktop acceptance runner (U8i / UIR2).
#
# It runs the desktop test binaries against the installed core and fails
# closed: a missing binary, an unready core, a suite failure, a mandatory
# QSKIP or an unexpected QML warning all exit nonzero. It never builds the
# product and never treats a skip as a pass.
#
# Usage:
#   desktop/tests/guest-run-suites.sh \
#       --build-dir /opt/kikimora-tests \
#       --socket /run/kikimora/core.sock \
#       --expected-kikimora-version 1.3.1 \
#       --expected-source-sha <40-hex>
#
# Env overrides:
#   KIKIMORA_CORE_BINARY       core used by the hermetic live fixtures
#                              (default: <build-dir>/fixtures/kikimora-core,
#                               then /usr/bin/kikimora-core)
#   KIKIMORA_FAKE_TOAD_BINARY  ready-toad fixture
#                              (default: <build-dir>/fixtures/ready-toad,
#                               then <build-dir>/fixtures/kikimora-fake-toad)
#   KIKIMORA_CORE_CLI          core CLI for readiness/status
#                              (default: /usr/bin/kikimora-core)
set -u

BUILD_DIR=""
SOCKET="/run/kikimora/core.sock"
EXPECTED_VERSION=""
EXPECTED_SHA=""
SELECTED=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --build-dir) BUILD_DIR="$2"; shift 2 ;;
        --socket) SOCKET="$2"; shift 2 ;;
        --expected-kikimora-version) EXPECTED_VERSION="$2"; shift 2 ;;
        --expected-source-sha) EXPECTED_SHA="$2"; shift 2 ;;
        --suite) SELECTED+=("$2"); shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

fail() { echo "INSTALLED RUNNER FAILURE: $*" >&2; exit 1; }

[[ -n "$BUILD_DIR" && -d "$BUILD_DIR" ]] || fail "--build-dir is required and must exist"
[[ -n "$EXPECTED_VERSION" ]] || fail "--expected-kikimora-version is required"
[[ "$EXPECTED_SHA" =~ ^[0-9a-f]{40}$ ]] || fail "--expected-source-sha must be 40 lowercase hex characters"
BUILD_DIR="$(cd "$BUILD_DIR" && pwd)"
ARTIFACT_DIR="$BUILD_DIR/installed-suite-logs"
mkdir -p "$ARTIFACT_DIR"
# Stale logs from an earlier invocation must never appear in this run summary.
rm -f "$ARTIFACT_DIR"/*.log
RUN_LOGS=()

CORE_CLI="${KIKIMORA_CORE_CLI:-/usr/bin/kikimora-core}"
CORE_BIN="${KIKIMORA_CORE_BINARY:-$BUILD_DIR/fixtures/kikimora-core}"
TOAD_BIN="${KIKIMORA_FAKE_TOAD_BINARY:-$BUILD_DIR/fixtures/ready-toad}"
[[ -x "$CORE_BIN" ]] || CORE_BIN="/usr/bin/kikimora-core"
if [[ ! -x "$TOAD_BIN" ]]; then
    TOAD_BIN="$BUILD_DIR/fixtures/kikimora-fake-toad"
fi
[[ -x "$CORE_CLI" ]] || fail "installed core CLI is missing: $CORE_CLI"
[[ -x "$CORE_BIN" ]] || fail "core fixture is missing: $CORE_BIN"
[[ -x "$TOAD_BIN" ]] || fail "ready-toad fixture is missing: $TOAD_BIN (harness failure, never a skip)"
export KIKIMORA_CORE_BINARY="$CORE_BIN"
export KIKIMORA_FAKE_TOAD_BINARY="$TOAD_BIN"
export KIKIMORA_UI_SOCKET="$SOCKET"
export QT_QPA_PLATFORM=offscreen
export QT_QUICK_BACKEND=software
export QTEST_FUNCTION_TIMEOUT=600000

# --- evidence ---------------------------------------------------------------
echo "== installed acceptance identities"
if command -v dpkg-query >/dev/null 2>&1; then
    dpkg-query -W -f='${Package} ${Version}\n' kikimora kikimora-ui 2>/dev/null || true
fi
echo "expected kikimora version: $EXPECTED_VERSION"
echo "expected source sha:       $EXPECTED_SHA"
echo "core cli:                  $CORE_CLI"
echo "core fixture:              $CORE_BIN"
echo "ready-toad fixture:        $TOAD_BIN"
echo "control socket:            $SOCKET"
if [[ -x "$CORE_CLI" ]]; then
    echo "core version: $("$CORE_CLI" version 2>/dev/null | head -1 || true)"
fi

readiness() {
    local deadline=240
    while [[ $deadline -gt 0 ]]; do
        local json
        json="$("$CORE_CLI" status --socket "$SOCKET" --json 2>/dev/null || true)"
        if printf '%s' "$json" | grep -q '"startup_ready":true' &&
            printf '%s' "$json" | grep -q '"core_state":"Ready"'; then
            return 0
        fi
        deadline=$((deadline - 5))
        sleep 5
    done
    return 1
}

# The live suites move roles inside their own temporary cores; the installed
# composition is only observed. Record its desired set and restore it if a
# suite somehow changes it through the shared socket.
snapshot_desired() {
    "$CORE_CLI" status --socket "$SOCKET" --json 2>/dev/null |
        python3 -c '
import json, sys
snap = json.load(sys.stdin)
for role in snap.get("roles", []):
    print(role.get("id", "") + "=" + ("1" if role.get("desired_enabled") else "0"))
' | sort
}

restore_desired() {
    local before="$1" after
    after="$(snapshot_desired)"
    [[ "$before" == "$after" ]] && return 0
    echo "== restoring the installed role desired state" >&2
    while IFS='=' read -r role want; do
        [[ -n "$role" ]] || continue
        if [[ "$want" == "1" ]]; then
            "$CORE_CLI" connect --role "$role" --socket "$SOCKET" >/dev/null 2>&1 || true
        else
            "$CORE_CLI" disconnect --role "$role" --socket "$SOCKET" >/dev/null 2>&1 || true
        fi
    done <<<"$before"
}

run_suite() {
    local name="$1" mode="${2:-}" live="${3:-0}"
    local binary="$BUILD_DIR/$name" log="$ARTIFACT_DIR/$name.log"
    RUN_LOGS+=("$log")
    if [[ ! -x "$binary" ]]; then
        fail "required suite binary is missing: $binary"
    fi
    if [[ "$live" == "1" ]]; then
        readiness || fail "the installed core never became Ready before $name"
    fi
    echo "== $name${mode:+ ($mode)}"
    local env_args=(env QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software
        KIKIMORA_UI_SOCKET="$SOCKET" KIKIMORA_CORE_BINARY="$CORE_BIN"
        KIKIMORA_FAKE_TOAD_BINARY="$TOAD_BIN" QTEST_FUNCTION_TIMEOUT=600000)
    [[ -n "$mode" ]] && env_args+=("KIKIMORA_UI_TEST_MODE=$mode")
    timeout "${KIKIMORA_SUITE_TIMEOUT:-900}" "${env_args[@]}" "$binary" >"$log" 2>&1
    local rc=$?
    cat "$log"

    if [[ $rc -eq 124 ]]; then echo "$name: timed out after ${KIKIMORA_SUITE_TIMEOUT:-900}s" >&2; return 1; fi
    [[ $rc -eq 0 ]] || { echo "$name: exit $rc" >&2; return 1; }
    grep -qE '^Totals: [0-9]+ passed, [0-9]+ failed, [0-9]+ skipped' "$log" ||
        { echo "$name: no QtTest totals; not evidence" >&2; return 1; }
    local executed
    executed="$(sed -nE 's/^Totals: ([0-9]+) passed, ([0-9]+) failed.*/\1 \2/p' "$log" | awk '{print $1 + $2}')"
    [[ "${executed:-0}" -gt 0 ]] || { echo "$name: executed no tests" >&2; return 1; }
    if grep -E '^.*SKIP[[:space:]]+:' "$log" >/dev/null 2>&1; then
        echo "$name: mandatory QtTest skip" >&2
        grep -E '^.*SKIP[[:space:]]+:' "$log" >&2
        return 1
    fi
    if grep -E 'ReferenceError|TypeError|Binding loop|QQmlApplicationEngine failed|Unable to assign|Cannot assign|required property' "$log" >/dev/null 2>&1; then
        echo "$name: unexpected QML warning" >&2
        return 1
    fi
    return 0
}

FAILURES=0
DESIRED_BEFORE="$(snapshot_desired || true)"

readiness || fail "the installed core is not Ready; refusing to run the live suites"
echo "== installed core is Ready"

if [[ ${#SELECTED[@]} -gt 0 ]]; then
    for chosen in "${SELECTED[@]}"; do
        case "$chosen" in
            ui-behavior-test | system-theme-test | role-list-model-test | tray-menu-test | real-core-client-test)
                run_suite "$chosen" || FAILURES=$((FAILURES + 1)) ;;
            real-core-ui-test) run_suite "$chosen" observe-ready 1 || FAILURES=$((FAILURES + 1)) ;;
            real-core-matrix-test | tray-live-test) run_suite "$chosen" "" 1 || FAILURES=$((FAILURES + 1)) ;;
            *) echo "unknown --suite $chosen" >&2; FAILURES=$((FAILURES + 1)) ;;
        esac
    done
else
    run_suite ui-behavior-test || FAILURES=$((FAILURES + 1))
    run_suite system-theme-test || FAILURES=$((FAILURES + 1))
    run_suite role-list-model-test || FAILURES=$((FAILURES + 1))
    run_suite tray-menu-test || FAILURES=$((FAILURES + 1))
    run_suite real-core-client-test || FAILURES=$((FAILURES + 1))
    run_suite real-core-ui-test observe-ready 1 || FAILURES=$((FAILURES + 1))
    run_suite real-core-matrix-test "" 1 || FAILURES=$((FAILURES + 1))
    run_suite tray-live-test "" 1 || FAILURES=$((FAILURES + 1))
fi

restore_desired "$DESIRED_BEFORE"

echo
echo "== installed-suite summary"
for log in "${RUN_LOGS[@]}"; do
    [[ -f "$log" ]] || continue
    totals="$(grep -E '^Totals:' "$log" | tail -1)"
    printf '%-32s %s\n' "$(basename "$log" .log)" "${totals:-<no totals>}"
done

[[ $FAILURES -eq 0 ]] || fail "$FAILURES installed suite(s) failed"
echo "installed desktop suites: all green, zero mandatory skips"
