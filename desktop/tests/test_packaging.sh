#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# test_packaging.sh — verify the complete Linux release artifact.
#
# Tests the canonical .deb produced by packaging/linux/build-release.sh.

set -euo pipefail

DESKTOP_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd -- "$DESKTOP_ROOT/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"

# ---- Build the canonical release artifact ----
echo "==> Building release artifact..."
KIKIMORA_OUT_DIR="$TMP/dist" \
KIKIMORA_BUILD_DIR="$TMP/build-ui" \
bash "$REPO_ROOT/packaging/linux/build-release.sh" 2>&1 | tail -10

# ---- Locate artifacts ----
DEB=$(ls "$TMP/dist"/kikimora_*.deb 2>/dev/null | head -1)
TGZ=$(ls "$TMP/dist"/kikimora-*-linux-*.tar.gz 2>/dev/null | head -1)
SUMS="$TMP/dist/SHA256SUMS"

if [[ -z "$DEB" ]]; then
    echo "FAIL: no .deb found in $TMP/dist" >&2
    exit 1
fi

echo "==> Testing: $(basename "$DEB")"

# ---- dpkg-deb metadata ----
dpkg-deb --field "$DEB" Package | grep -qx 'kikimora' || {
    echo "FAIL: Package field mismatch" >&2; exit 1; }
dpkg-deb --field "$DEB" Version | grep -q '.' || {
    echo "FAIL: Version field empty" >&2; exit 1; }
dpkg-deb --field "$DEB" Architecture | grep -qx "$ARCH" || {
    echo "FAIL: Architecture field mismatch" >&2; exit 1; }
dpkg-deb --field "$DEB" Depends | grep -q 'libqt6' || {
    echo "FAIL: Depends missing libqt6" >&2; exit 1; }
dpkg-deb --field "$DEB" Depends | grep -q 'systemd' || {
    echo "FAIL: Depends missing systemd" >&2; exit 1; }
echo "  metadata: OK"

# ---- dpkg-deb contents ----
dpkg-deb -x "$DEB" "$TMP/root"

REQUIRED_PATHS=(
    usr/local/bin/kikimora-core
    usr/local/bin/kikimora-toad
    usr/local/sbin/kikimora
    usr/local/bin/kk
    usr/bin/kikimora-ui
    usr/lib/systemd/system/kikimora-core.service
    usr/lib/tmpfiles.d/kikimora-core.conf
    usr/lib/sysusers.d/kikimora-core.conf
    usr/share/kikimora/orchestration-ownership.conf
    etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf
    usr/share/applications/kikimora.desktop
    usr/share/icons/hicolor/256x256/apps/kikimora.png
)

for path in "${REQUIRED_PATHS[@]}"; do
    if [[ ! -e "$TMP/root/$path" ]]; then
        echo "FAIL: missing package path: $path" >&2
        exit 1
    fi
done
echo "  contents: all ${#REQUIRED_PATHS[@]} required paths present"

# ---- Executable modes ----
for bin in usr/local/bin/kikimora-core usr/local/bin/kikimora-toad usr/local/sbin/kikimora usr/bin/kikimora-ui; do
    mode=$(stat -c '%a' "$TMP/root/$bin")
    if [[ "$mode" != "755" ]]; then
        echo "FAIL: $bin has mode $mode (expected 755)" >&2
        exit 1
    fi
done
echo "  modes: executable modes correct"

# ---- Service ExecStart points to packaged binary ----
SERVICE_FILE="$TMP/root/usr/lib/systemd/system/kikimora-core.service"
if [[ -f "$SERVICE_FILE" ]]; then
    grep -q '/usr/local/bin/kikimora-core' "$SERVICE_FILE" || {
        echo "FAIL: service ExecStart does not point to packaged binary" >&2
        exit 1
    }
    echo "  service: ExecStart points to packaged binary"
fi

# ---- Desktop entry Exec ----
DESKTOP_FILE="$TMP/root/usr/share/applications/kikimora.desktop"
if [[ -f "$DESKTOP_FILE" ]]; then
    grep -qx 'Exec=kikimora-ui' "$DESKTOP_FILE" || {
        echo "FAIL: desktop entry Exec mismatch" >&2
        exit 1
    }
    echo "  desktop: Exec=kikimora-ui"
fi

# ---- SHA256SUMS ----
if [[ -f "$SUMS" ]]; then
    (cd "$TMP/dist" && sha256sum -c SHA256SUMS >/dev/null 2>&1) || {
        echo "FAIL: SHA256SUMS verification failed" >&2
        exit 1
    }
    echo "  checksums: SHA256SUMS verified"
fi

# ---- No secrets/test fixtures ----
# Only flag actual credential values, not code that references or redacts them.
if grep -rn 'PRIVATE_KEY\|password\|secret\|testkey' "$TMP/root" 2>/dev/null \
    | grep -v '^Binary' \
    | grep -v 'REDACTED\|redact\|#.*password\|#.*secret\|s/\(password\)' \
    | grep -v 'maintenance.sh.*password\|maintenance.sh.*secret'; then
    echo "FAIL: secrets found in package" >&2
    exit 1
fi
echo "  secrets: none detected"

# ---- .tar.gz ----
if [[ -f "$TGZ" ]]; then
    tar -tzf "$TGZ" > "$TMP/tar-list.txt"
    for path in ./usr/local/bin/kikimora-core ./usr/local/bin/kikimora-toad ./usr/local/sbin/kikimora ./usr/bin/kikimora-ui; do
        grep -qx "$path" "$TMP/tar-list.txt" || {
            echo "FAIL: tarball missing $path" >&2
            exit 1
        }
    done
    echo "  tarball: contents verified"
fi

echo ""
echo "=== ALL PACKAGING TESTS PASSED ==="
echo "  $(basename "$DEB")"
echo "  $(basename "$TGZ")"
