#!/usr/bin/env bash
set -euo pipefail

DESKTOP_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
REPO_ROOT=$(cd -- "$DESKTOP_ROOT/.." && pwd -P)
VERSION=$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")
ARCH=$(dpkg --print-architecture)
BUILD_DIR="${KIKIMORA_BUILD_DIR:-$DESKTOP_ROOT/build-release}"
DIST_DIR="${KIKIMORA_DIST_DIR:-$DESKTOP_ROOT/dist}"

mkdir -p "$DIST_DIR"
go -C "$REPO_ROOT/toad" build -o "$BUILD_DIR/kikimora-core" ./cmd/kikimora-core
go -C "$REPO_ROOT/toad" build -o "$BUILD_DIR/kikimora-toad" ./cmd/kikimora-toad

cmake -S "$DESKTOP_ROOT" -B "$BUILD_DIR" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DKIKIMORA_VERSION="$VERSION" \
  -DKIKIMORA_PACKAGE_OUTPUT_DIR="$DIST_DIR" \
  -DKIKIMORA_CORE_BINARY="$BUILD_DIR/kikimora-core" \
  -DKIKIMORA_TOAD_BINARY="$BUILD_DIR/kikimora-toad"
cmake --build "$BUILD_DIR" --target package-release

printf 'Built Kikimora packages for %s: %s and %s\n' \
  "$ARCH" \
  "$DIST_DIR/kikimora_${VERSION}_${ARCH}.deb" \
  "$DIST_DIR/kikimora-${VERSION}-linux-${ARCH}.tar.gz"
