#!/usr/bin/env bash
# Required desktop UI suites (U6, docs/roadmap.md §13).
#
# CTest alone cannot enforce the release policy: a QtTest binary "passes"
# CTest while its cases QSKIP. This wrapper runs every required suite
# directly and fails the whole run when
#   - a binary exits nonzero,
#   - a mandatory SKIP appears,
#   - an unexpected QML warning pattern appears,
#   - the Layer-C binaries were not provided (harness failure, never a skip).
#
# Usage:
#   desktop/tests/run-required-suites.sh <build-dir> [suite ...]
#
# Env:
#   KIKIMORA_SKIP_GO_FIXTURES=1  do not build the core/fake-toad helpers
#                                (only for Layer A/B-only runs)
#
# Every suite log lands in "$build-dir/required-suite-logs/".

set -u

BUILD_DIR="${1:-}"
if [ -z "$BUILD_DIR" ] || [ ! -d "$BUILD_DIR" ]; then
    echo "usage: $0 <build-dir> [suite ...]" >&2
    exit 2
fi
# Absolute from here on: the fixture build cd's into toad/, which would
# otherwise resolve a relative build dir against the wrong root.
BUILD_DIR="$(cd "$BUILD_DIR" && pwd)"
shift || true

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ARTIFACT_DIR="$BUILD_DIR/required-suite-logs"
mkdir -p "$ARTIFACT_DIR"

# Layer C binaries: build them from source so a missing helper is a harness
# failure here rather than a skip inside a QtTest case.
CORE_BIN="$BUILD_DIR/fixtures/kikimora-core"
TOAD_BIN="$BUILD_DIR/fixtures/kikimora-fake-toad"
if [ "${KIKIMORA_SKIP_GO_FIXTURES:-0}" != "1" ]; then
    echo "== building Go fixtures (core + fake toad)"
    mkdir -p "$BUILD_DIR/fixtures"
    if ! (cd "$REPO_ROOT/toad" && go build -o "$CORE_BIN" ./cmd/kikimora-core \
        && go build -o "$TOAD_BIN" ./internal/control/testhelper); then
        echo "HARNESS FAILURE: cannot build the Layer-C fixtures" >&2
        exit 2
    fi
    export KIKIMORA_CORE_BINARY="$CORE_BIN"
    export KIKIMORA_FAKE_TOAD_BINARY="$TOAD_BIN"
fi

run_required() {
    local name="$1"
    shift
    local binary="$BUILD_DIR/$name"
    local log="$ARTIFACT_DIR/$name.log"

    if [ ! -x "$binary" ]; then
        echo "HARNESS FAILURE: required suite binary is missing: $binary" >&2
        return 2
    fi

    echo "== $name"
    "$@" "$binary" >"$log" 2>&1
    local rc=$?
    cat "$log"

    if [ "$rc" -ne 0 ]; then
        echo "$name: exit $rc" >&2
        return "$rc"
    fi

    # Evidence contract (08d1-current-state-audit-2026-11-12): a passing exit
    # code proves nothing on its own — the run must produce real QtTest
    # output with a positive executed count.
    if ! grep -E '^Totals: [0-9]+ passed, [0-9]+ failed, [0-9]+ skipped' "$log" >/dev/null 2>&1; then
        echo "HARNESS FAILURE: $name produced no QtTest totals; refusing to count it as evidence" >&2
        return 3
    fi
    local executed
    executed="$(sed -nE 's/^Totals: ([0-9]+) passed, ([0-9]+) failed.*/\1 \2/p' "$log" | awk '{print $1 + $2}')"
    if [ "${executed:-0}" -le 0 ]; then
        echo "HARNESS FAILURE: $name executed no tests" >&2
        return 3
    fi

    # A mandatory suite may not pass via QSKIP.
    if grep -E '^.*SKIP[[:space:]]+:' "$log" >/dev/null 2>&1; then
        echo "$name: mandatory QtTest skip" >&2
        grep -E '^.*SKIP[[:space:]]+:' "$log" >&2
        return 1
    fi

    # Unexpected QML/runtime warnings are failures, not log decoration.
    if grep -E 'ReferenceError|TypeError|Binding loop|QQmlApplicationEngine failed|QQuickStyle::setStyle\(\) must be called|Unable to assign|Cannot assign|required property' "$log" >/dev/null 2>&1; then
        echo "$name: unexpected QML warning" >&2
        grep -E 'ReferenceError|TypeError|Binding loop|QQmlApplicationEngine failed|QQuickStyle::setStyle\(\) must be called|Unable to assign|Cannot assign|required property' "$log" >&2
        return 1
    fi
    return 0
}

OFFSCREEN_ENV=(env QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software)
FAILURES=0

LAYER_A=(real-core-client-test role-list-model-test system-theme-test)
LAYER_B=(ui-behavior-test tray-menu-test)
LAYER_C=(real-core-e2e-test real-core-ui-test real-core-matrix-test tray-live-test)

SUITES=("${LAYER_A[@]}" "${LAYER_B[@]}" "${LAYER_C[@]}")
if [ "$#" -gt 0 ]; then
    SUITES=("$@")
fi

for suite in "${SUITES[@]}"; do
    case "$suite" in
    real-core-client-test | real-core-e2e-test | real-core-ui-test | real-core-matrix-test | tray-live-test)
        # No Quick backend needed: these never load QML except the two ui suites.
        run_required "$suite" "${OFFSCREEN_ENV[@]}" && continue
        ;;
    ui-behavior-test | tray-menu-test | system-theme-test | role-list-model-test)
        run_required "$suite" "${OFFSCREEN_ENV[@]}" && continue
        ;;
    *)
        echo "unknown required suite: $suite" >&2
        FAILURES=$((FAILURES + 1))
        continue
        ;;
    esac
    FAILURES=$((FAILURES + 1))
done

echo
echo "== required-suite summary"
for suite in "${SUITES[@]}"; do
    log="$ARTIFACT_DIR/$suite.log"
    if [ -f "$log" ]; then
        totals=$(grep -E '^Totals:' "$log" | tail -1)
        printf '%-26s %s\n' "$suite" "${totals:-<no totals>}"
    else
        printf '%-26s %s\n' "$suite" "<no log>"
    fi
done

if [ "$FAILURES" -gt 0 ]; then
    echo "REQUIRED SUITES FAILED: $FAILURES" >&2
    exit 1
fi
echo "required suites: all green, zero mandatory skips"
