#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# build-release.sh — canonical Linux release builder.
#
# Produces .deb and .tar.gz artifacts under dist/.
# Thin wrappers (linux/package.sh, desktop/packaging/build-release.sh)
# delegate to this script.
#
# Environment:
#   KIKIMORA_ARCH     target architecture (default: host arch)
#   KIKIMORA_OUT_DIR  output directory (default: ROOT/dist)
#   KIKIMORA_BUILD_DIR  CMake build directory (default: ROOT/build/packaging-ui)
#   KIKIMORA_SKIP_UI  if set, skip Qt UI build

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
OUT_DIR="${KIKIMORA_OUT_DIR:-$ROOT/dist}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$OUT_DIR"

echo "==> Kikimora Linux release build"
echo "    Version: $KIKIMORA_VERSION"
echo "    Architecture: $ARCH"
echo "    Output: $OUT_DIR"

# ---- Stage all files ----
"$ROOT/packaging/linux/stage-release.sh" "$STAGE" "$ARCH"

# ---- Build Qt UI ----
if [[ -z "${KIKIMORA_SKIP_UI:-}" ]]; then
    BUILD_DIR="${KIKIMORA_BUILD_DIR:-$ROOT/build/packaging-ui}"
    echo "  Building kikimora-ui..."
    cmake -S "$ROOT/desktop" -B "$BUILD_DIR" -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DKIKIMORA_VERSION="$KIKIMORA_VERSION" \
        -DKIKIMORA_CORE_BINARY="$STAGE/usr/local/bin/kikimora-core" \
        -DKIKIMORA_TOAD_BINARY="$STAGE/usr/local/bin/kikimora-toad" 2>&1 | tail -3
    cmake --build "$BUILD_DIR" --target kikimora-ui 2>&1 | tail -5
    install -m 0755 "$BUILD_DIR/kikimora-ui" "$STAGE/usr/bin/kikimora-ui"
    echo "  kikimora-ui built and staged"
else
    echo "  Skipping kikimora-ui build (KIKIMORA_SKIP_UI set)"
fi

# ---- Build .deb ----
DEB_PATH="$OUT_DIR/kikimora_${KIKIMORA_VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$STAGE" "$DEB_PATH" >/dev/null
echo "  Created: $DEB_PATH"

# ---- Build .tar.gz (portable) ----
TAR_PATH="$OUT_DIR/kikimora-${KIKIMORA_VERSION}-linux-${ARCH}.tar.gz"
tar -C "$STAGE" --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
    -czf "$TAR_PATH" \
    --exclude=DEBIAN \
    .
echo "  Created: $TAR_PATH"

# ---- SHA-256 checksums ----
(cd "$OUT_DIR" && sha256sum "kikimora_${KIKIMORA_VERSION}_${ARCH}.deb" \
    "kikimora-${KIKIMORA_VERSION}-linux-${ARCH}.tar.gz" > SHA256SUMS)
echo "  Created: $OUT_DIR/SHA256SUMS"

echo "==> Build complete"