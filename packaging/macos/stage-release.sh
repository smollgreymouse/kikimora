#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# stage-release.sh — canonical macOS staging builder.
#
# Stages all product files for .pkg creation.
# Called by build-release.sh; not meant to be executed directly.
#
# Usage: stage-release.sh <stage-dir> [arch]

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

STAGE="${1:?usage: stage-release.sh <stage-dir> [arch]}"
RAW_ARCH="${2:-$(uname -m)}"

# Normalize Darwin architecture names (Phase 4.3)
case "$RAW_ARCH" in
    x86_64) GOARCH="amd64";;
    arm64)  GOARCH="arm64";;
    amd64)  GOARCH="amd64";;
    *)      GOARCH="$RAW_ARCH";;
esac

rm -rf "$STAGE"
install -d "$STAGE/usr/local/bin" \
    "$STAGE/usr/local/libexec/kikimora" \
    "$STAGE/Library/LaunchDaemons" \
    "$STAGE/Applications"

# ---- Go binaries ----
echo "  Building kikimora-core (darwin/$GOARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$GOARCH" go build -trimpath \
    -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)

echo "  Building kikimora-toad (darwin/$GOARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$GOARCH" go build -trimpath \
    -ldflags "-s -w -X main.toadVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-toad" ./cmd/kikimora-toad)

# ---- Version file ----
install -m 0644 "$ROOT/VERSION" "$STAGE/usr/local/libexec/kikimora/VERSION"

# ---- macOS CLI wrapper ----
install -m 0755 "$ROOT/macos/kikimora" "$STAGE/usr/local/bin/kikimora"
ln -sfn /usr/local/bin/kikimora "$STAGE/usr/local/bin/kk"

# ---- macOS support scripts ----
if [[ -f "$ROOT/macos/lib.sh" ]]; then
    install -m 0644 "$ROOT/macos/lib.sh" "$STAGE/usr/local/libexec/kikimora/lib.sh"
fi

# ---- Launchd plist ----
if [[ -f "$ROOT/macos/files/com.kikimora.core.plist" ]]; then
    install -m 0644 "$ROOT/macos/files/com.kikimora.core.plist" \
        "$STAGE/Library/LaunchDaemons/com.kikimora.core.plist"
fi

echo "  Staged: $STAGE"