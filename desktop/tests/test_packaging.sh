#!/usr/bin/env bash
set -euo pipefail

DESKTOP_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
REPO_ROOT=$(cd -- "$DESKTOP_ROOT/.." && pwd -P)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
VERSION=9.9.9
ARCH=$(dpkg --print-architecture)
BUILD="$TMP/build"
DIST="$TMP/dist"

go -C "$REPO_ROOT/toad" build -o "$TMP/kikimora-core" ./cmd/kikimora-core
go -C "$REPO_ROOT/toad" build -o "$TMP/kikimora-toad" ./cmd/kikimora-toad
cmake -S "$DESKTOP_ROOT" -B "$BUILD" \
  -DCMAKE_BUILD_TYPE=Release \
  -DKIKIMORA_VERSION="$VERSION" \
  -DKIKIMORA_PACKAGE_OUTPUT_DIR="$DIST" \
  -DKIKIMORA_CORE_BINARY="$TMP/kikimora-core" \
  -DKIKIMORA_TOAD_BINARY="$TMP/kikimora-toad" >/dev/null
cmake --build "$BUILD" --target package-release >/dev/null

DEB="$DIST/kikimora_${VERSION}_${ARCH}.deb"
TGZ="$DIST/kikimora-${VERSION}-linux-${ARCH}.tar.gz"
[[ -f "$DEB" ]]
[[ -f "$TGZ" ]]

dpkg-deb --field "$DEB" Package | grep -qx 'kikimora'
dpkg-deb --field "$DEB" Version | grep -qx "$VERSION"
dpkg-deb --field "$DEB" Architecture | grep -qx "$ARCH"
dpkg-deb --field "$DEB" Depends | grep -q 'libqt6'
dpkg-deb -x "$DEB" "$TMP/root"
for path in \
  usr/bin/kikimora-ui \
  usr/bin/kikimora-core \
  usr/bin/kikimora-toad \
  usr/share/applications/kikimora.desktop \
  usr/share/icons/hicolor/256x256/apps/kikimora.png; do
  [[ -e "$TMP/root/$path" ]] || { echo "missing package path: $path" >&2; exit 1; }
done
grep -qx 'Exec=kikimora-ui' "$TMP/root/usr/share/applications/kikimora.desktop"

tar -tzf "$TGZ" > "$TMP/tar-list.txt"
PORTABLE="kikimora-${VERSION}-linux-${ARCH}"
for path in \
  "$PORTABLE/bin/kikimora-ui" \
  "$PORTABLE/bin/kikimora-core" \
  "$PORTABLE/bin/kikimora-toad" \
  "$PORTABLE/INSTALL.txt" \
  "$PORTABLE/share/applications/kikimora.desktop"; do
  grep -qx "$path" "$TMP/tar-list.txt"
done

printf 'Kikimora packaging tests passed: %s and %s\n' "$(basename "$DEB")" "$(basename "$TGZ")"
