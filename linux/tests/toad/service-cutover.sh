#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
SERVICE="$ROOT/linux/files/kikimora-core.service"
OWNERSHIP="$ROOT/linux/files/orchestration-ownership.conf"
INSTALLER="$ROOT/linux/install.sh"
PACKAGE="$ROOT/linux/package.sh"
PROVIDERS="$ROOT/linux/files/endpoint-providers"
RUNNER="$ROOT/linux/tests/toad/run-isolated.sh"
LEGACY_RECONCILE="$ROOT/linux/files/reconcile"
LEGACY_LIFECYCLE="$ROOT/linux/files/route-lifecycle"
LEGACY_WATCH="$ROOT/linux/files/route-watch"

grep -Fxq 'Type=simple' "$SERVICE"
grep -Fxq 'User=root' "$SERVICE"
grep -Fxq 'Group=kikimora' "$SERVICE"
grep -Fxq 'RuntimeDirectory=kikimora' "$SERVICE"
grep -Fxq 'RuntimeDirectoryMode=0750' "$SERVICE"
grep -Fxq 'UMask=0007' "$SERVICE"
grep -Fxq 'CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW' "$SERVICE"
grep -Fxq 'AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW' "$SERVICE"
grep -Fxq 'NoNewPrivileges=yes' "$SERVICE"
grep -Fq -- '--ownership-config /etc/kikimora/leshy/orchestration-ownership.conf' "$SERVICE"
grep -Fq -- '--legacy-vpn-config /etc/kikimora/leshy/vpn.conf' "$SERVICE"
grep -Fq -- '--endpoint-provider-dir /usr/local/libexec/kikimora/endpoint-providers' "$SERVICE"
if grep -q '^Type=notify

grep -Fxq 'routing_owner = "legacy"' "$OWNERSHIP"
grep -Fxq 'tunnel_owner = "external"' "$OWNERSHIP"
grep -Fxq 'endpoint_owner = "legacy"' "$OWNERSHIP"
grep -Fq 'kikimora-core.service' "$INSTALLER"
grep -Fq 'orchestration-ownership.conf' "$INSTALLER"
grep -Fq 'kikimora-core.service' "$PACKAGE"
grep -Fq 'orchestration-ownership.conf' "$PACKAGE"
grep -Fq 'orchestration.sh' "$INSTALLER"
grep -Fq 'orchestration.sh' "$PACKAGE"
grep -Fq 'detect_legacy_runtime_mode' "$INSTALLER"
grep -Fq 'legacy_runtime_enabled' "$INSTALLER"
grep -Fq "rm -f -- \"\$RECONCILE\" \"\$ROUTE_LIFECYCLE\" \"\$ROUTE_WATCH\" \"\$ROUTE_WATCH_UNIT\" \"\$ROUTE_CLEANUP_DROPIN\"" "$INSTALLER"
grep -Fq 'orchestration) cmd_orchestration' "$ROOT/linux/kikimora"
grep -Fq 'retire-legacy' "$ROOT/linux/files/kikimora-cli/orchestration.sh"
grep -Fq 'orch_core_service_action' "$ROOT/linux/files/kikimora-cli/service.sh"
[[ -x "$ROOT/linux/tests/toad/orchestration-cutover.sh" ]]
for provider in static command happ; do
    [[ -x "$PROVIDERS/$provider" ]]
done
grep -Fq "endpoint-providers/$provider" "$PACKAGE"
grep -Fq 'flock 9' "$RUNNER"
grep -Fq 'build_inputs_hash' "$RUNNER"
grep -Fq 'reference_inputs_hash' "$RUNNER"
grep -Fq 'build_go_binary' "$RUNNER"
grep -Fq '.inputs' "$RUNNER"
grep -Fq 'build-only' "$RUNNER"
if grep -Fq "go build -o \"\$BUILD_DIR/kikimora-toad\"" "$RUNNER"; then
    echo "ERROR: runner must use cached build helper for kikimora-toad" >&2
    exit 1
fi
if grep -Fq "go build -o \"\$BUILD_DIR/kikimora-core\"" "$RUNNER"; then
    echo "ERROR: runner must use cached build helper for kikimora-core" >&2
    exit 1
fi
grep -Fq 'Go owns tunnel lifecycle' "$LEGACY_RECONCILE"
grep -Fq 'Go owns route/tunnel lifecycle' "$LEGACY_LIFECYCLE"
grep -Fq 'Go owns orchestration' "$LEGACY_WATCH"

if command -v systemd-analyze >/dev/null 2>&1 && [[ -x /usr/local/bin/kikimora-core ]]; then
    systemd-analyze verify "$SERVICE"
fi

printf 'Kikimora service/cutover contract: OK\n'
 "$SERVICE"; then
    echo "ERROR: service must not use Type=notify" >&2
    exit 1
fi
if grep -q '^ExecStartPost=' "$SERVICE"; then
    echo "ERROR: service must not use ExecStartPost" >&2
    exit 1
fi

grep -Fxq 'routing_owner = "legacy"' "$OWNERSHIP"
grep -Fxq 'tunnel_owner = "external"' "$OWNERSHIP"
grep -Fxq 'endpoint_owner = "legacy"' "$OWNERSHIP"
grep -Fq 'kikimora-core.service' "$INSTALLER"
grep -Fq 'orchestration-ownership.conf' "$INSTALLER"
grep -Fq 'kikimora-core.service' "$PACKAGE"
grep -Fq 'orchestration-ownership.conf' "$PACKAGE"
grep -Fq 'orchestration.sh' "$INSTALLER"
grep -Fq 'orchestration.sh' "$PACKAGE"
grep -Fq 'detect_legacy_runtime_mode' "$INSTALLER"
grep -Fq 'legacy_runtime_enabled' "$INSTALLER"
grep -Fq 'rm -f -- "$RECONCILE" "$ROUTE_LIFECYCLE" "$ROUTE_WATCH" "$ROUTE_WATCH_UNIT" "$ROUTE_CLEANUP_DROPIN"' "$INSTALLER"
grep -Fq 'orchestration) cmd_orchestration' "$ROOT/linux/kikimora"
grep -Fq 'retire-legacy' "$ROOT/linux/files/kikimora-cli/orchestration.sh"
grep -Fq 'orch_core_service_action' "$ROOT/linux/files/kikimora-cli/service.sh"
[[ -x "$ROOT/linux/tests/toad/orchestration-cutover.sh" ]]
for provider in static command happ; do
    [[ -x "$PROVIDERS/$provider" ]]
done
grep -Fq 'endpoint-providers/$provider' "$PACKAGE"
grep -Fq 'flock 9' "$RUNNER"
grep -Fq 'build_inputs_hash' "$RUNNER"
grep -Fq 'reference_inputs_hash' "$RUNNER"
grep -Fq 'build_go_binary' "$RUNNER"
grep -Fq '.inputs' "$RUNNER"
grep -Fq 'build-only' "$RUNNER"
! grep -Fq 'go build -o "$BUILD_DIR/kikimora-toad"' "$RUNNER"
! grep -Fq 'go build -o "$BUILD_DIR/kikimora-core"' "$RUNNER"
grep -Fq 'Go owns tunnel lifecycle' "$LEGACY_RECONCILE"
grep -Fq 'Go owns route/tunnel lifecycle' "$LEGACY_LIFECYCLE"
grep -Fq 'Go owns orchestration' "$LEGACY_WATCH"

if command -v systemd-analyze >/dev/null 2>&1 && [[ -x /usr/local/bin/kikimora-core ]]; then
    systemd-analyze verify "$SERVICE"
fi

printf 'Kikimora service/cutover contract: OK\n'
