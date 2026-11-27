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

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
OUT_DIR="${KIKIMORA_OUT_DIR:-$ROOT/dist}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$OUT_DIR"

# 09G-W0.2: artifact provenance. The integrated release must carry the exact
# source SHA and the pinned Leshy SHA in build-info.json (the installed
# desktop journey validates them before installing the candidate).
KIKIMORA_SOURCE_SHA="$(git -C "$ROOT" rev-parse HEAD)"
KIKIMORA_LESHY_SRC="${KIKIMORA_LESHY_SRC:-$ROOT/../leshy}"
# A Leshy git checkout is authoritative: HEAD is the revision the staged
# binary must be built from. Without one, the caller supplies the pinned
# revision the staged binary (or the non-git cargo build in KIKIMORA_LESHY_SRC)
# was built from — a prebuilt binary carries no git metadata.
KIKIMORA_LESHY_SHA="${KIKIMORA_LESHY_SHA:-}"
if [[ -d "$KIKIMORA_LESHY_SRC/.git" ]]; then
    KIKIMORA_LESHY_SHA="$(git -C "$KIKIMORA_LESHY_SRC" rev-parse HEAD)"
fi
export KIKIMORA_SOURCE_SHA KIKIMORA_LESHY_SHA
for sha in "$KIKIMORA_SOURCE_SHA" "$KIKIMORA_LESHY_SHA"; do
    if [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
        echo "FAIL: integrated release provenance requires a 40-hex SHA (got '$sha');" >&2
        echo "      set KIKIMORA_LESHY_SRC to a git checkout or KIKIMORA_LESHY_SHA to the pinned revision" >&2
        exit 1
    fi
done

echo "==> Kikimora Linux release build"
echo "    Version: $KIKIMORA_VERSION"
echo "    Architecture: $ARCH"
echo "    Output: $OUT_DIR"

# ---- Stage all files ----
"$ROOT/packaging/linux/stage-release.sh" "$STAGE" "$ARCH"

# Chapter 08 ships the console/runtime product only.  The optional Qt UI is
# packaged separately in chapter 09 and must not affect this artifact.

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