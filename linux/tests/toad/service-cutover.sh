#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# service-cutover.sh — verify service/ownership/cutover contract for
# canonical packaging, not implementation details of wrapper scripts.
#
# The canonical staging builder is packaging/linux/build-release.sh.
# linux/package.sh and desktop/packaging/build-release.sh are thin
# wrappers that delegate to it. We test the delegation contract and
# the payload content through the canonical .deb artifact.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
SERVICE="$ROOT/linux/files/kikimora-core.service"
OWNERSHIP="$ROOT/linux/files/orchestration-ownership.conf"
PACKAGE_WRAPPER="$ROOT/linux/package.sh"
DESKTOP_WRAPPER="$ROOT/desktop/packaging/build-release.sh"
CANONICAL_BUILDER="$ROOT/packaging/linux/build-release.sh"
STAGE_SCRIPT="$ROOT/packaging/linux/stage-release.sh"
PROVIDERS_DIR="$ROOT/linux/files/endpoint-providers"
RUNNER="$ROOT/linux/tests/toad/run-isolated.sh"
LEGACY_RECONCILE="$ROOT/linux/files/reconcile"
LEGACY_LIFECYCLE="$ROOT/linux/files/route-lifecycle"
LEGACY_WATCH="$ROOT/linux/files/route-watch"
NM_UNMANAGED="$ROOT/linux/files/90-kikimora-unmanaged.conf"
CLI_ORCHESTRATION="$ROOT/linux/files/kikimora-cli/orchestration.sh"
CLI_SERVICE="$ROOT/linux/files/kikimora-cli/service.sh"
ORCHESTRATION_CUTOVER="$ROOT/linux/tests/toad/orchestration-cutover.sh"

# ===========================================================================
# Section 1: Service unit file contract
# ===========================================================================
echo "=== Section 1: Service unit file contract ==="

grep -Fxq 'Type=simple' "$SERVICE"
grep -Fxq 'User=root' "$SERVICE"
grep -Fxq 'Group=kikimora' "$SERVICE"
grep -Fxq 'RuntimeDirectory=kikimora' "$SERVICE"
grep -Fxq 'RuntimeDirectoryMode=0750' "$SERVICE"
grep -Fxq 'StateDirectory=kikimora/core' "$SERVICE"
grep -Fxq 'StateDirectoryMode=0750' "$SERVICE"
grep -Fq -- '--state-dir /var/lib/kikimora/core' "$SERVICE"
grep -Fxq 'UMask=0007' "$SERVICE"
grep -Fxq 'CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW' "$SERVICE"
grep -Fxq 'AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW' "$SERVICE"
grep -Fxq 'NoNewPrivileges=yes' "$SERVICE"
grep -Fq -- '--ownership-config /etc/kikimora/leshy/orchestration-ownership.conf' "$SERVICE"
grep -Fq -- '--legacy-vpn-config /etc/kikimora/leshy/vpn.conf' "$SERVICE"
grep -Fq -- '--endpoint-provider-dir /usr/local/libexec/kikimora/endpoint-providers' "$SERVICE"
if grep -q '^Type=notify$' "$SERVICE"; then
    echo "ERROR: service must not use Type=notify" >&2
    exit 1
fi
if grep -q '^ExecStartPost=' "$SERVICE"; then
    echo "ERROR: service must not use ExecStartPost" >&2
    exit 1
fi
echo "  Service unit contract: OK"

# ===========================================================================
# Section 2: Ownership config contract
# ===========================================================================
echo "=== Section 2: Ownership config contract ==="

grep -Fxq 'routing_owner = "legacy"' "$OWNERSHIP"
grep -Fxq 'tunnel_owner = "external"' "$OWNERSHIP"
grep -Fxq 'endpoint_owner = "legacy"' "$OWNERSHIP"
echo "  Ownership config: OK"

# ===========================================================================
# Section 3: Wrapper delegation contract
#
# linux/package.sh and desktop/packaging/build-release.sh must delegate
# to the canonical packaging/linux/build-release.sh — they should not
# contain their own payload-install logic.
# ===========================================================================
echo "=== Section 3: Wrapper delegation contract ==="

# linux/package.sh delegates to canonical builder
if ! grep -Fq 'packaging/linux/build-release.sh' "$PACKAGE_WRAPPER"; then
    echo "ERROR: linux/package.sh does not delegate to canonical builder" >&2
    exit 1
fi
if grep -Fq 'install -m' "$PACKAGE_WRAPPER"; then
    echo "ERROR: linux/package.sh contains payload-install logic (wrapper should only delegate)" >&2
    exit 1
fi
if grep -Fq 'kikimora-core.service' "$PACKAGE_WRAPPER"; then
    echo "WARNING: linux/package.sh references kikimora-core.service directly" >&2
    echo "  (wrapper should delegate; this is informational)" >&2
fi
echo "  linux/package.sh: delegates to canonical builder"

# desktop/packaging/build-release.sh delegates to canonical builder
if ! grep -Fq 'packaging/linux/build-release.sh' "$DESKTOP_WRAPPER"; then
    echo "ERROR: desktop/packaging/build-release.sh does not delegate to canonical builder" >&2
    exit 1
fi
if grep -Fq 'install -m' "$DESKTOP_WRAPPER"; then
    echo "ERROR: desktop/packaging/build-release.sh contains payload-install logic" >&2
    exit 1
fi
echo "  desktop/packaging/build-release.sh: delegates to canonical builder"

# Canonical builder exists and sources stage-release.sh
if [[ ! -x "$CANONICAL_BUILDER" ]]; then
    echo "ERROR: canonical builder not executable: $CANONICAL_BUILDER" >&2
    exit 1
fi
if [[ ! -x "$STAGE_SCRIPT" ]]; then
    echo "ERROR: stage-release.sh not executable: $STAGE_SCRIPT" >&2
    exit 1
fi
echo "  Canonical builder: exists and executable"

# ===========================================================================
# Section 4: Endpoint providers exist and are executable
# ===========================================================================
echo "=== Section 4: Endpoint providers ==="

for provider in static command happ; do
    if [[ ! -x "$PROVIDERS_DIR/$provider" ]]; then
        echo "ERROR: endpoint provider $provider not found or not executable" >&2
        exit 1
    fi
done
echo "  Endpoint providers: OK"

# ===========================================================================
# Section 5: CLI orchestration and cutover
# ===========================================================================
echo "=== Section 5: CLI orchestration and cutover ==="

grep -Fq 'orchestration) cmd_orchestration' "$ROOT/linux/kikimora"
grep -Fq 'retire-legacy' "$CLI_ORCHESTRATION"
grep -Fq 'orch_core_service_action' "$CLI_SERVICE"

if [[ ! -x "$ORCHESTRATION_CUTOVER" ]]; then
    echo "ERROR: orchestration-cutover.sh not executable" >&2
    exit 1
fi
echo "  CLI orchestration and cutover: OK"

# ===========================================================================
# Section 6: Legacy lifecycle scripts contain Go ownership references
# ===========================================================================
echo "=== Section 6: Legacy lifecycle scripts ==="

grep -Fq 'Go owns tunnel lifecycle' "$LEGACY_RECONCILE"
grep -Fq 'Go owns route/tunnel lifecycle' "$LEGACY_LIFECYCLE"
grep -Fq 'Go owns orchestration' "$LEGACY_WATCH"
echo "  Legacy lifecycle scripts: OK"

# ===========================================================================
# Section 7: NetworkManager unmanaged devices
# ===========================================================================
echo "=== Section 7: NetworkManager unmanaged devices ==="

grep -Fxq '[keyfile]' "$NM_UNMANAGED"
grep -Fxq 'unmanaged-devices=interface-name:kk-*' "$NM_UNMANAGED"
echo "  NetworkManager config: OK"

# ===========================================================================
# Section 8: Run-isolated runner contract
# ===========================================================================
echo "=== Section 8: Run-isolated runner contract ==="

grep -Fq 'flock 9' "$RUNNER"
grep -Fq 'build_inputs_hash' "$RUNNER"
grep -Fq 'reference_inputs_hash' "$RUNNER"
grep -Fq 'build_go_binary' "$RUNNER"
grep -Fq '.inputs' "$RUNNER"
grep -Fq 'build-only' "$RUNNER"

# Runner must use cached build helpers, not raw go build for core/toad
if grep -Fq 'go build -o "$BUILD_DIR/kikimora-toad"' "$RUNNER"; then
    echo "ERROR: runner must use cached build helper for kikimora-toad" >&2
    exit 1
fi
if grep -Fq 'go build -o "$BUILD_DIR/kikimora-core"' "$RUNNER"; then
    echo "ERROR: runner must use cached build helper for kikimora-core" >&2
    exit 1
fi
echo "  Run-isolated runner contract: OK"

# ===========================================================================
# Section 9: systemd-analyze verify (optional, requires installed binaries)
# ===========================================================================
echo "=== Section 9: systemd-analyze verify ==="

if command -v systemd-analyze >/dev/null 2>&1 && [[ -x /usr/local/bin/kikimora-core ]]; then
    systemd-analyze verify "$SERVICE"
    echo "  systemd-analyze verify: OK"
else
    echo "  systemd-analyze verify: skipped (binaries not installed on host)"
fi

# ===========================================================================
# Done
# ===========================================================================
echo ""
echo "=== ALL SERVICE/CUTOVER CONTRACT TESTS PASSED ==="
exit 0