#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"
MODE="${1:-all}"
BUILD_DIR="$(mktemp -d)"

cleanup() {
    rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

require() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "ERROR: required command not found: $1" >&2
        exit 1
    }
}

require go
require sudo
require ip
require python3
require ping
require ss

build_common() {
    echo "==> building checked-out Toad binaries"
    (
        cd "$TOAD_DIR"
        go build -o "$BUILD_DIR/kikimora-toad" ./cmd/kikimora-toad
        go build -o "$BUILD_DIR/toad-tun-test-helper" ./internal/platform/testhelper
        go build -o "$BUILD_DIR/toad-awg2-test-helper" ./internal/backend/awg2/testhelper
    )
    chmod 0755 "$BUILD_DIR/kikimora-toad" "$BUILD_DIR/toad-tun-test-helper" "$BUILD_DIR/toad-awg2-test-helper"
}

build_awg_reference() {
    echo "==> building pinned official AmneziaWG reference"
    (
        cd "$TOAD_DIR"
        go build -o "$BUILD_DIR/amneziawg-go-ref" github.com/amnezia-vpn/amneziawg-go/v3
    )
    chmod 0755 "$BUILD_DIR/amneziawg-go-ref"
    go version -m "$BUILD_DIR/amneziawg-go-ref" \
        | grep -F 'github.com/amnezia-vpn/amneziawg-go/v3' \
        | grep -F 'v3.1.20260828' >/dev/null
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

build_common

case "$MODE" in
    all)
        run_tun_owner
        run_awg_attachment
        run_awg_interop
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
    *)
        echo "usage: $0 [all|tun-owner|awg2-attachment|awg2-interop]" >&2
        exit 2
        ;;
esac

echo "All requested isolated Toad tests passed (mode=$MODE)"
