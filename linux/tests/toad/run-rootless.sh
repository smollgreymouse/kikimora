#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"
MODE="${1:-}"
BUILD_DIR="${KIKIMORA_TOAD_BUILD_DIR:-$REPO_ROOT/build/toad-rootless-smoke}"

usage() {
    echo "usage: $0 <model|probe>" >&2
    echo "kernel/network integration is operator-privileged; use $REPO_ROOT/run-privileged-gates.sh" >&2
    exit 2
}

run_model() {
    echo "==> rootless model fallback: deterministic kernel-independent Go suite"
    (
        cd "$TOAD_DIR"
        go test             ./internal/routing             ./internal/parking             ./internal/endpoint             ./internal/core             ./internal/control             ./internal/netstate             ./internal/toadruntime
        go test -race             ./internal/control             ./internal/core             ./internal/netstate             ./internal/toadruntime
    )

    echo "==> rootless model fallback: service/package static contracts"
    bash "$SCRIPT_DIR/service-cutover.sh"

    echo "==> rootless model fallback: orchestration fake contract"
    bash "$SCRIPT_DIR/orchestration-cutover.sh"

    echo "model suite: PASS"
}

run_probe() {
    mkdir -p "$BUILD_DIR"
    echo "==> preparing current test helper as uid $(id -u)"
    KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR" bash "$SCRIPT_DIR/run-isolated.sh" build-only
    echo "==> probing optional unprivileged userns/netns/TUN capability"
    KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR"     TOAD_TUN_HELPER="$BUILD_DIR/toad-tun-test-helper"         bash "$SCRIPT_DIR/rootless-netns-probe.sh"
}

case "$MODE" in
    model)
        run_model
        ;;
    probe)
        run_probe
        ;;
    *)
        usage
        ;;
esac
