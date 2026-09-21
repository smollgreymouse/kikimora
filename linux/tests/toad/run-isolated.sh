#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"
MODE="${1:-all}"
BUILD_DIR="${KIKIMORA_TOAD_BUILD_DIR:-$REPO_ROOT/build/toad-smoke}"
mkdir -p "$BUILD_DIR"

require() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "ERROR: required command not found: $1" >&2
        exit 1
    }
}

require go
require flock
if [[ "$MODE" != build-only ]]; then
    require sudo
    require ip
    require python3
    require ping
    require ss
fi

# Keep one stable output directory for all isolated modes. Go's build cache
# recompiles only stale packages, while stable output paths let subsequent
# tests reuse the same checked-out binaries and pinned reference tools.
exec 9>"$BUILD_DIR/.build.lock"
flock 9

build_inputs_hash() {
    {
        go version
        find "$TOAD_DIR" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -print0 |
            sort -z |
            xargs -0 sha256sum
    } | sha256sum | awk '{print $1}'
}

reference_inputs_hash() {
    {
        go version
        sha256sum "$TOAD_DIR/go.mod" "$TOAD_DIR/go.sum"
    } | sha256sum | awk '{print $1}'
}

build_go_binary() {
    local output="$1" input_hash="$2"
    shift 2
    local marker="${output}.inputs"
    if [[ -x "$output" && -r "$marker" ]] && [[ "$(<"$marker")" == "$input_hash" ]]; then
        echo "==> reusing $output (inputs unchanged)"
        return 0
    fi
    go build -o "$output" "$@"
    printf '%s\n' "$input_hash" >"$marker"
}

build_common() {
    echo "==> building checked-out Toad binaries (incremental cache: $BUILD_DIR)"
    local input_hash
    input_hash="$(build_inputs_hash)"
    (
        cd "$TOAD_DIR"
        build_go_binary "$BUILD_DIR/kikimora-toad" "$input_hash" ./cmd/kikimora-toad
        build_go_binary "$BUILD_DIR/kikimora-core" "$input_hash" ./cmd/kikimora-core
        build_go_binary "$BUILD_DIR/toad-tun-test-helper" "$input_hash" ./internal/platform/testhelper
        build_go_binary "$BUILD_DIR/toad-awg2-test-helper" "$input_hash" ./internal/backend/awg2/testhelper
    )
    chmod 0755 "$BUILD_DIR/kikimora-toad" "$BUILD_DIR/kikimora-core" "$BUILD_DIR/toad-tun-test-helper" "$BUILD_DIR/toad-awg2-test-helper"
    go version -m "$BUILD_DIR/kikimora-toad" \
        | grep -F 'github.com/xtls/xray-core' \
        | grep -F 'v1.260327.1-0.20260728075948-5ca6f4b7d4dc' >/dev/null
}

build_awg_reference() {
    echo "==> building pinned official AmneziaWG reference (incremental cache)"
    local input_hash
    input_hash="$(reference_inputs_hash)"
    (
        cd "$TOAD_DIR"
        build_go_binary "$BUILD_DIR/amneziawg-go-ref" "$input_hash" github.com/amnezia-vpn/amneziawg-go/v3
    )
    chmod 0755 "$BUILD_DIR/amneziawg-go-ref"
    go version -m "$BUILD_DIR/amneziawg-go-ref" \
        | grep -F 'github.com/amnezia-vpn/amneziawg-go/v3' \
        | grep -F 'v3.1.20260828' >/dev/null
}

build_xray_reference() {
    echo "==> building pinned official Xray reference and hermetic cover (incremental cache)"
    local reference_hash source_hash
    reference_hash="$(reference_inputs_hash)"
    source_hash="$(build_inputs_hash)"
    (
        cd "$TOAD_DIR"
        build_go_binary "$BUILD_DIR/xray-ref" "$reference_hash" github.com/xtls/xray-core/main
        build_go_binary "$BUILD_DIR/xray-test-cover" "$source_hash" ./internal/backend/xray/testcover
    )
    chmod 0755 "$BUILD_DIR/xray-ref" "$BUILD_DIR/xray-test-cover"
    go version -m "$BUILD_DIR/xray-ref" \
        | grep -F 'github.com/xtls/xray-core' \
        | grep -F 'v1.260327.1-0.20260728075948-5ca6f4b7d4dc' >/dev/null
}

run_tun_owner() {
    echo "==> linux TUN owner gate"
    sudo env TOAD_TUN_HELPER="$BUILD_DIR/toad-tun-test-helper" \
        bash "$SCRIPT_DIR/tun-owner-netns.sh"
}

run_awg_attachment() {
    echo "==> AWG2 attachment gate"
    sudo env \
        TOAD_AWG2_HELPER="$BUILD_DIR/toad-awg2-test-helper" \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        bash "$SCRIPT_DIR/awg2-attachment-netns.sh"
}

run_awg_interop() {
    echo "==> AWG2 isolated client/server interop gate"
    build_awg_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        AWG_REF_BIN="$BUILD_DIR/amneziawg-go-ref" \
        bash "$SCRIPT_DIR/awg2-interop.sh"
}

run_xray_lifecycle() {
    echo "==> Xray isolated lifecycle gate"
    sudo env TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        bash "$SCRIPT_DIR/xray-lifecycle-netns.sh"
}

run_xray_interop() {
    echo "==> Xray isolated REALITY + VLESS + Vision interop gate"
    build_xray_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        XRAY_REF_BIN="$BUILD_DIR/xray-ref" \
        XRAY_COVER_BIN="$BUILD_DIR/xray-test-cover" \
        bash "$SCRIPT_DIR/xray-interop.sh"
}

run_openconnect_interop() {
    echo "==> OpenConnect isolated client/server interop gate"
    for command in openconnect ocserv ocpasswd openssl curl; do
        require "$command"
    done
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        OPENCONNECT_BIN="$(command -v openconnect)" \
        OCSERV_BIN="$(command -v ocserv)" \
        bash "$SCRIPT_DIR/openconnect-interop.sh"
}

run_multi_toad_interop() {
    echo "==> simultaneous AWG2 + Xray + OpenConnect isolation gate"
    for command in openconnect ocserv ocpasswd openssl curl; do
        require "$command"
    done
    build_awg_reference
    build_xray_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        AWG_REF_BIN="$BUILD_DIR/amneziawg-go-ref" \
        XRAY_REF_BIN="$BUILD_DIR/xray-ref" \
        XRAY_COVER_BIN="$BUILD_DIR/xray-test-cover" \
        OPENCONNECT_BIN="$(command -v openconnect)" \
        OCSERV_BIN="$(command -v ocserv)" \
        bash "$SCRIPT_DIR/multi-toad-interop.sh"
}

run_core_isolated() {
    echo "==> Kikimora core + isolated Toad smoke (no UI)"
    build_awg_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        AWG_REF_BIN="$BUILD_DIR/amneziawg-go-ref" \
        bash "$SCRIPT_DIR/awg2-interop.sh"

    build_xray_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        XRAY_REF_BIN="$BUILD_DIR/xray-ref" \
        XRAY_COVER_BIN="$BUILD_DIR/xray-test-cover" \
        bash "$SCRIPT_DIR/xray-interop.sh"

    for command in openconnect ocserv ocpasswd openssl curl; do
        require "$command"
    done
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        OPENCONNECT_BIN="$(command -v openconnect)" \
        OCSERV_BIN="$(command -v ocserv)" \
        bash "$SCRIPT_DIR/openconnect-interop.sh"
}

run_core_ui_isolated() {
    local ui_test_bin="${KIKIMORA_UI_TEST_BINARY:-$REPO_ROOT/build/desktop/real-core-ui-test}"
    [[ -x "$ui_test_bin" ]] || {
        echo "ERROR: headless UI test is not executable: $ui_test_bin" >&2
        echo "Build it with: cmake --build build/desktop --target real-core-ui-test" >&2
        exit 1
    }
    echo "==> Kikimora core + real isolated Toad + headless UI smoke"
    build_awg_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        UI_TEST_BIN="$ui_test_bin" \
        AWG_REF_BIN="$BUILD_DIR/amneziawg-go-ref" \
        bash "$SCRIPT_DIR/awg2-interop.sh"

    build_xray_reference
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        UI_TEST_BIN="$ui_test_bin" \
        XRAY_REF_BIN="$BUILD_DIR/xray-ref" \
        XRAY_COVER_BIN="$BUILD_DIR/xray-test-cover" \
        bash "$SCRIPT_DIR/xray-interop.sh"

    for command in openconnect ocserv ocpasswd openssl curl; do
        require "$command"
    done
    sudo env \
        TOAD_BIN="$BUILD_DIR/kikimora-toad" \
        CORE_BIN="$BUILD_DIR/kikimora-core" \
        UI_TEST_BIN="$ui_test_bin" \
        OPENCONNECT_BIN="$(command -v openconnect)" \
        OCSERV_BIN="$(command -v ocserv)" \
        bash "$SCRIPT_DIR/openconnect-interop.sh"
}

require_real_vps_secret() {
    local path="$1"
    local owner mode
    [[ -f "$path" && -r "$path" ]] || {
        echo "ERROR: real-VPS secret file is missing or unreadable: $path" >&2
        exit 1
    }
    owner="$(stat -c '%u' -- "$path")"
    mode="$(stat -c '%a' -- "$path")"
    [[ "$owner" == "$(id -u)" ]] || {
        echo "ERROR: real-VPS secret file must be owned by the current user: $path" >&2
        exit 1
    }
    if (( 8#$mode & 077 )); then
        echo "ERROR: real-VPS secret file permissions are too broad ($mode): $path" >&2
        exit 1
    fi
}

run_real_vps_awg() {
    echo "==> real-VPS isolated AWG2 smoke"
    require_real_vps_secret "$SCRIPT_DIR/real-vps-awg-link.secret"
    TOAD_BIN="$BUILD_DIR/kikimora-toad" bash "$SCRIPT_DIR/real-vps-awg-diag-test.sh"
}

run_real_vps_vless() {
    [[ "${KIKIMORA_ALLOW_REAL_VPS_VLESS:-}" == "1" ]] || {
        echo "ERROR: real-VPS VLESS smoke is opt-in; set KIKIMORA_ALLOW_REAL_VPS_VLESS=1 explicitly" >&2
        exit 2
    }
    echo "==> real-VPS isolated VLESS/REALITY smoke"
    require_real_vps_secret "$SCRIPT_DIR/real-vps-vless-link.secret"
    TOAD_BIN="$BUILD_DIR/kikimora-toad" bash "$SCRIPT_DIR/real-vps-vless-diag-test.sh"
}

run_real_vps_openconnect() {
    echo "==> real-VPS isolated OpenConnect smoke"
    require_real_vps_secret "$SCRIPT_DIR/real-vps-openconnect.secret"
    TOAD_BIN="$BUILD_DIR/kikimora-toad" bash "$SCRIPT_DIR/real-vps-openconnect-diag-test.sh"
}

run_real_vps() {
    run_real_vps_awg
    run_real_vps_vless
    run_real_vps_openconnect
}

build_common

case "$MODE" in
    all)
        run_tun_owner
        run_awg_attachment
        run_awg_interop
        run_xray_lifecycle
        run_xray_interop
        run_openconnect_interop
        ;;
    tun-owner)
        run_tun_owner
        ;;
    awg2-attachment)
        run_awg_attachment
        ;;
    awg2-interop)
        run_awg_interop
        ;;
    xray-lifecycle)
        run_xray_lifecycle
        ;;
    xray-interop)
        run_xray_interop
        ;;
    openconnect-interop)
        run_openconnect_interop
        ;;
    multi-toad)
        run_multi_toad_interop
        ;;
    core-isolated)
        run_core_isolated
        ;;
    core-ui-isolated)
        run_core_ui_isolated
        ;;
    build-only)
        build_awg_reference
        build_xray_reference
        echo "Prepared shared incremental Toad smoke binaries in $BUILD_DIR"
        ;;
    real-vps)
        run_real_vps
        ;;
    real-vps-awg)
        run_real_vps_awg
        ;;
    real-vps-vless)
        run_real_vps_vless
        ;;
    real-vps-openconnect)
        run_real_vps_openconnect
        ;;
    *)
        echo "usage: $0 [all|build-only|tun-owner|awg2-attachment|awg2-interop|xray-lifecycle|xray-interop|openconnect-interop|multi-toad|core-isolated|core-ui-isolated|real-vps|real-vps-awg|real-vps-vless|real-vps-openconnect]" >&2
        exit 2
        ;;
esac

echo "All requested isolated Toad tests passed (mode=$MODE)"
