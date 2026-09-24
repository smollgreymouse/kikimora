#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"
MODE="${1:-}"
BUILD_DIR="${KIKIMORA_TOAD_BUILD_DIR:-$REPO_ROOT/build/toad-rootless-smoke}"
UNSUPPORTED_EXIT=77

usage() {
    echo "usage: $0 <model|all|tun-owner|route-parking|awg2-attachment|awg2-interop|xray-lifecycle|xray-interop|openconnect-interop|multi-toad|orchestration-acceptance|core-isolated|core-ui-isolated>" >&2
    exit 2
}

[[ -n "$MODE" ]] || usage

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

if [[ "$MODE" == model ]]; then
    run_model
    exit 0
fi

case "$MODE" in
    all|tun-owner|route-parking|awg2-attachment|awg2-interop|xray-lifecycle|xray-interop|openconnect-interop|multi-toad|orchestration-acceptance|core-isolated|core-ui-isolated)
        ;;
    *)
        usage
        ;;
esac

if [[ "${KIKIMORA_ROOTLESS_TEST_NS:-0}" == 1 ]]; then
    exec bash "$SCRIPT_DIR/run-isolated.sh" "$MODE"
fi

for command in unshare ip readlink sort; do
    command -v "$command" >/dev/null 2>&1 || {
        echo "ERROR: required command not found: $command" >&2
        exit 1
    }
done

mkdir -p "$BUILD_DIR"
echo "==> preparing cached test binaries as uid $(id -u)"
KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR" bash "$SCRIPT_DIR/run-isolated.sh" build-only

set +e
KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR" TOAD_TUN_HELPER="$BUILD_DIR/toad-tun-test-helper"     bash "$SCRIPT_DIR/rootless-netns-probe.sh"
PROBE_STATUS=$?
set -e
if [[ "$PROBE_STATUS" -eq "$UNSUPPORTED_EXIT" ]]; then
    echo "rootless kernel integration: UNSUPPORTED BY EXECUTOR ENVIRONMENT" >&2
    echo "Run: bash $SCRIPT_DIR/run-rootless.sh model" >&2
    exit "$UNSUPPORTED_EXIT"
elif [[ "$PROBE_STATUS" -ne 0 ]]; then
    echo "ERROR: rootless capability probe failed (exit=$PROBE_STATUS)" >&2
    exit "$PROBE_STATUS"
fi

STATE_TMP="$(mktemp -d "${TMPDIR:-/tmp}/kikimora-rootless-state.XXXXXX")"
trap 'rm -rf -- "$STATE_TMP"' EXIT

snapshot_host() {
    local target="$1"
    {
        printf 'netns=%s\n' "$(readlink /proc/self/ns/net)"
        printf '%s\n' '-- links --'
        ip -o link show | awk -F': ' '{print $2}' | sed 's/@.*//' | sort
        printf '%s\n' '-- ipv4-default --'
        ip -4 route show default | sort
        printf '%s\n' '-- ipv6-default --'
        ip -6 route show default | sort
        printf '%s\n' '-- ipv4-rules --'
        ip -4 rule show | sort
        printf '%s\n' '-- ipv6-rules --'
        ip -6 rule show | sort
        printf '%s\n' '-- run-netns --'
        if [[ -d /run/netns && -r /run/netns ]]; then
            find /run/netns -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null | sort || true
        fi
    } >"$target"
}

snapshot_host "$STATE_TMP/before"

set +e
# shellcheck disable=SC2016
KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR" unshare --user --map-root-user --mount --net --pid --fork --mount-proc bash -c '
        set -euo pipefail
        runner="$1"
        mode="$2"
        mount --make-rprivate /
        mount -t tmpfs -o mode=755 tmpfs /run
        mkdir -p /run/netns /run/amneziawg /run/kikimora-rootless
        chmod 0700 /run/kikimora-rootless
        export XDG_RUNTIME_DIR=/run/kikimora-rootless
        export KIKIMORA_ROOTLESS_TEST_NS=1
        exec bash "$runner" "$mode"
    ' bash "$SCRIPT_DIR/run-isolated.sh" "$MODE"
TEST_STATUS=$?
set -e

snapshot_host "$STATE_TMP/after"
if ! cmp -s "$STATE_TMP/before" "$STATE_TMP/after"; then
    echo "ERROR: host network state changed across rootless test" >&2
    diff -u "$STATE_TMP/before" "$STATE_TMP/after" >&2 || true
    exit 1
fi
echo "host state unchanged: PASS"

exit "$TEST_STATUS"
