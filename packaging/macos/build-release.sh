#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# build-release.sh — canonical macOS release builder.
#
# Produces .pkg and .tar.gz artifacts under dist/.
#
# Environment:
#   KIKIMORA_ARCH     target architecture (default: host arch)
#   KIKIMORA_OUT_DIR  output directory (default: ROOT/dist)

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

ARCH="${KIKIMORA_ARCH:-$(uname -m)}"
OUT_DIR="${KIKIMORA_OUT_DIR:-$ROOT/dist}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$OUT_DIR"

echo "==> Kikimora macOS release build"
echo "    Version: $KIKIMORA_VERSION"
echo "    Architecture: $ARCH"
echo "    Output: $OUT_DIR"

# ---- Stage all files ----
"$ROOT/packaging/macos/stage-release.sh" "$STAGE" "$ARCH"

# ---- Build Qt app bundle (macOS host only) ----
if [[ "$(uname)" == "Darwin" ]]; then
    BUILD_DIR="${KIKIMORA_BUILD_DIR:-$ROOT/build/packaging-ui-macos}"
    echo "  Building Kikimora.app..."
    cmake -S "$ROOT/desktop" -B "$BUILD_DIR" -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DKIKIMORA_VERSION="$KIKIMORA_VERSION" \
        -DKIKIMORA_CORE_BINARY="$STAGE/usr/local/bin/kikimora-core" \
        -DKIKIMORA_TOAD_BINARY="$STAGE/usr/local/bin/kikimora-toad" 2>&1 | tail -3
    cmake --build "$BUILD_DIR" --target kikimora-ui 2>&1 | tail -5
    cp -R "$BUILD_DIR/Kikimora.app" "$STAGE/Applications/Kikimora.app"
    echo "  Kikimora.app staged"
else
    echo "  Skipping Qt app bundle build (not on macOS host)"
fi

# ---- Build .pkg (macOS host only) ----
if [[ "$(uname)" == "Darwin" ]] && command -v pkgbuild >/dev/null 2>&1; then
    PKG_PATH="$OUT_DIR/kikimora-${KIKIMORA_VERSION}-macos-${ARCH}.pkg"
    pkgbuild --root "$STAGE" \
        --identifier com.kikimora.core \
        --version "$KIKIMORA_VERSION" \
        --install-location / \
        "$PKG_PATH" 2>&1 | tail -3
    echo "  Created: $PKG_PATH"
fi

# ---- Build .tar.gz (portable) ----
TAR_PATH="$OUT_DIR/kikimora-${KIKIMORA_VERSION}-macos-${ARCH}.tar.gz"
tar -C "$STAGE" --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
    -czf "$TAR_PATH" .
echo "  Created: $TAR_PATH"

# ---- SHA-256 checksums ----
(cd "$OUT_DIR" && sha256sum "kikimora-${KIKIMORA_VERSION}-macos-${ARCH}.tar.gz" > SHA256SUMS-macos)
echo "  Created: $OUT_DIR/SHA256SUMS-macos"

echo "==> Build complete"