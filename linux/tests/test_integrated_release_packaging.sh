#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Integrated artifact contract (roadmap P1.2): the UI-present Linux release.
#
# This test consumes already-built artifacts and never rebuilds:
#
#   linux/tests/test_integrated_release_packaging.sh [dist-dir]
#
# dist-dir must contain kikimora_<version>_<arch>.deb,
# kikimora-<version>-linux-<arch>.tar.gz and SHA256SUMS, produced by the
# canonical builder with KIKIMORA_REQUIRE_UI=1 and KIKIMORA_UI_BIN set.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="${1:-$ROOT/dist}"
ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"

DEB="$DIST/kikimora_${VERSION}_${ARCH}.deb"
TGZ="$DIST/kikimora-${KIKIMORA_VERSION:-$VERSION}-linux-${ARCH}.tar.gz"
SUMS="$DIST/SHA256SUMS"

for artifact in "$DEB" "$TGZ" "$SUMS"; do
  [[ -f "$artifact" ]] || {
    echo "FAIL: integrated contract needs prebuilt artifacts; missing $artifact" >&2
    echo "      build first: KIKIMORA_REQUIRE_UI=1 KIKIMORA_UI_BIN=... packaging/linux/build-release.sh" >&2
    exit 1
  }
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> verifying integrated (UI-present) artifacts in $DIST"

# ---- Checksums match the manifest exactly ----
( cd "$DIST" && sha256sum -c SHA256SUMS ) >/dev/null
echo "  checksums: SHA256SUMS matches"

# ---- Both archives carry the full integrated payload ----
payload=(
  usr/bin/kikimora-ui
  usr/local/bin/kikimora-core
  usr/local/bin/kikimora-toad
  usr/local/bin/leshy
  usr/share/applications/kikimora.desktop
  etc/xdg/autostart/kikimora.desktop
  usr/share/icons/hicolor/256x256/apps/kikimora.png
)

mkdir -p "$TMP/deb-root" "$TMP/tgz-root"
dpkg-deb -x "$DEB" "$TMP/deb-root"
for path in "${payload[@]}"; do
  [[ -e "$TMP/deb-root/$path" || -L "$TMP/deb-root/$path" ]] || {
    echo "FAIL: deb missing integrated payload path $path" >&2
    exit 1
  }
done
echo "  deb: integrated payload complete"

tar -xzf "$TGZ" -C "$TMP/tgz-root"
for path in "${payload[@]}"; do
  [[ -e "$TMP/tgz-root/$path" || -L "$TMP/tgz-root/$path" ]] || {
    echo "FAIL: tarball missing integrated payload path $path" >&2
    exit 1
  }
done
echo "  tarball: integrated payload complete"

# ---- The console/runtime payload is still there ----
for path in usr/local/sbin/kikimora usr/local/bin/kk \
            usr/lib/systemd/system/kikimora-core.service \
            usr/lib/systemd/system/leshy.service; do
  [[ -e "$TMP/deb-root/$path" || -L "$TMP/deb-root/$path" ]] || {
    echo "FAIL: integrated deb lost console payload $path" >&2
    exit 1
  }
done

# ---- Exact Qt Depends set (roadmap P1.2) ----
deps="$(dpkg-deb --field "$DEB" Depends)"
qt_deps=(
  "libqt6core6t64 | libqt6core6"
  "libqt6gui6t64 | libqt6gui6"
  "libqt6network6t64 | libqt6network6"
  "libqt6qml6 | libqt6qml6t64"
  "libqt6quick6 | libqt6quick6t64"
  "libqt6quickcontrols2-6 | libqt6quickcontrols2-6t64"
  "libqt6widgets6t64 | libqt6widgets6"
)
for dep in "${qt_deps[@]}"; do
  grep -Fq "$dep" <<<"$deps" || {
    echo "FAIL: integrated deb Depends missing '$dep': $deps" >&2
    exit 1
  }
done
# No stray Qt relationship outside the exact set: every libqt6 occurrence in
# Depends must be one of the pairs above.
stray="$(grep -oE 'libqt6[a-z0-9-]*(6t64)?' <<<"$deps" | sort -u |
  grep -Ev '^libqt6(core|gui|network|qml|quick|quickcontrols2-|widgets)6(t64)?$' || true)"
if [[ -n "$stray" ]]; then
  echo "FAIL: unexpected Qt dependency outside the P1.2 set: $stray" >&2
  exit 1
fi
echo "  deb: Qt Depends set exact"

recommends="$(dpkg-deb --field "$DEB" Recommends)"
grep -Fq "gnome-shell-extension-appindicator" <<<"$recommends" || {
  echo "FAIL: integrated deb must keep gnome-shell-extension-appindicator in Recommends: $recommends" >&2
  exit 1
}

description="$(dpkg-deb --field "$DEB" Description)"
grep -qi "desktop client" <<<"$description" || {
  echo "FAIL: integrated deb description must state the desktop client is included: $description" >&2
  exit 1
}

# The UI-present package must not regress the headless contract's
# runtime dependencies.
for dep in bash iproute2 systemd openconnect network-manager bind9-dnsutils python3; do
  grep -Eq "(^|, )[[:space:]]*$dep([[:space:]]|,|$)" <<<"$deps" || {
    echo "FAIL: integrated deb lost runtime dependency $dep: $deps" >&2
    exit 1
  }
done

echo "==> integrated artifact contract GREEN"
