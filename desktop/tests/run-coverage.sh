#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# run-coverage.sh — native C++ source coverage for the desktop UI (U11).
#
# Contract (docs/roadmap.md §18.4, docs/desktop-ui-executor-coverage-plan-2026-11-11.md §14):
#   - coverage build uses -DKIKIMORA_ENABLE_COVERAGE=ON;
#   - the runner invokes the same required-suite shell wrapper, so coverage is
#     never collected from a reduced test set;
#   - scope is desktop/src/**, excluding generated Qt code, tests and build dirs;
#   - thresholds are enforced here and must not be lowered to absorb a red build.
#
# Usage:
#   bash desktop/tests/run-coverage.sh [BUILD_DIR]
#
# The build directory defaults to build/desktop-ui-coverage and is configured
# from scratch unless it already carries COVERAGE=ON.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_DIR="$ROOT/desktop"
BUILD_DIR="${1:-$ROOT/build/desktop-ui-coverage}"
# Absolute, because the collection step chdirs into the build tree.
mkdir -p "$BUILD_DIR"
BUILD_DIR="$(cd "$BUILD_DIR" && pwd)"
REPORT_DIR="$BUILD_DIR/coverage"

# Thresholds fixed by the UI executor packet §14. Do not lower these.
SRC_LINE_MIN=75
SRC_BRANCH_MIN=60
FILECOV_LINE_MIN=80
FILECOV_FILES=(RealCoreClient.cpp RoleListModel.cpp DesktopIntegration.cpp)

die() { echo "FAIL: $*" >&2; exit 1; }
note() { echo "== $*"; }

command -v gcovr >/dev/null 2>&1 || die "gcovr is required for U11 coverage"
command -v gcov >/dev/null 2>&1 || die "gcov is required for U11 coverage"

note "configuring the coverage build ($BUILD_DIR)"
if [[ ! -f "$BUILD_DIR/CMakeCache.txt" ]] ||
   ! grep -q 'KIKIMORA_ENABLE_COVERAGE:BOOL=ON' "$BUILD_DIR/CMakeCache.txt"; then
    rm -rf "$BUILD_DIR"
    cmake -S "$SRC_DIR" -B "$BUILD_DIR" \
        -DCMAKE_BUILD_TYPE=Debug \
        -DKIKIMORA_ENABLE_COVERAGE=ON >/dev/null
fi

note "building"
cmake --build "$BUILD_DIR" -j"$(nproc)" >/dev/null

# Coverage must come from the full required set: reuse the same wrapper the
# release gate runs, so a reduced invocation cannot inflate the numbers.
note "running the required suites (full set, no reduction)"
bash "$SRC_DIR/tests/run-required-suites.sh" "$BUILD_DIR" >/dev/null

mkdir -p "$REPORT_DIR"
note "collecting coverage for desktop/src"
# Scope: ${SRC_DIR}/src only, expressed as a filter so generated Qt sources from
# the build tree, tests/ and build output cannot leak in.
#
# --gcov-ignore-errors=no_working_dir_found is required in practice: gcov emits
# notes for system headers (qstring.h, new, ...) whose paths cannot be resolved
# from the build tree, and gcovr 7 aborts the whole report on them by default.
# Ignoring ONLY that error class keeps every real parse problem fatal.
# gcov/gcovr resolve notes and data files relative to the build tree: run from
# there, otherwise every .gcno is looked up relative to the invocation directory
# and the report comes out empty (0 out of 0) instead of failing loudly.
cd "$BUILD_DIR"

gcovr \
    --filter ".*desktop/src/.*" \
    --gcov-executable gcov \
    --gcov-ignore-errors=no_working_dir_found \
    --exclude-unreachable-branches \
    --exclude-throw-branches \
    --txt "$REPORT_DIR/coverage.txt" \
    --txt-summary \
    --json-summary "$REPORT_DIR/coverage.json" \
    --html-details "$REPORT_DIR/index.html" \
    >/dev/null

python3 - "$REPORT_DIR/coverage.json" "$SRC_LINE_MIN" "$SRC_BRANCH_MIN" "$FILECOV_LINE_MIN" \
    "${FILECOV_FILES[@]}" <<'PY'
import json
import os
import sys

report_path = sys.argv[1]
src_line_min = float(sys.argv[2])
src_branch_min = float(sys.argv[3])
file_line_min = float(sys.argv[4])
file_required = sys.argv[5:]

with open(report_path, encoding="utf-8") as handle:
    report = json.load(handle)

summary = report.get("line_percent")
# gcovr 7 emits float fields directly; older layouts nest under "line_covered".
if summary is None:
    totals = report.get("line_total")
    covered = report.get("line_covered")
    summary = (100.0 * covered / totals) if totals else 0.0

branch_percent = 0.0
raw_branch = report.get("branch_percent")
if raw_branch is not None:
    branch_percent = float(raw_branch)
else:
    btotal = report.get("branch_total") or 0
    bcovered = report.get("branch_covered") or 0
    branch_percent = (100.0 * bcovered / btotal) if btotal else 0.0

failures = []
print(f"desktop/src line coverage:   {float(summary):.2f}%  (min {src_line_min:.0f}%)")
print(f"desktop/src branch coverage: {branch_percent:.2f}%  (min {src_branch_min:.0f}%)")
if float(summary) < src_line_min:
    failures.append(f"desktop/src line coverage {float(summary):.2f}% < {src_line_min:.0f}%")
if branch_percent < src_branch_min:
    failures.append(f"desktop/src branch coverage {branch_percent:.2f}% < {src_branch_min:.0f}%")

# Per-file contract for the three files the packet names explicitly.
files = {os.path.basename(entry.get("filename", "")): entry
         for entry in report.get("files", [])}
for name in file_required:
    entry = files.get(name)
    if entry is None:
        failures.append(f"{name}: no coverage data (was it compiled into a suite?)")
        continue
    percent = entry.get("line_percent")
    if percent is None:
        total = entry.get("line_total") or 0
        covered = entry.get("line_covered") or 0
        percent = (100.0 * covered / total) if total else 0.0
    percent = float(percent)
    status = "ok" if percent >= file_line_min else "BELOW"
    print(f"  {name:28} {percent:6.2f}%  (min {file_line_min:.0f}%)  {status}")
    if percent < file_line_min:
        failures.append(f"{name} line coverage {percent:.2f}% < {file_line_min:.0f}%")

if failures:
    print("\nCOVERAGE GATE FAILED:", file=sys.stderr)
    for item in failures:
        print(f"  - {item}", file=sys.stderr)
    sys.exit(1)
print("\ncoverage gate: all thresholds met")
PY

note "report: $REPORT_DIR/coverage.txt and $REPORT_DIR/index.html"
