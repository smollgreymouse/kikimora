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
ARCH="${2:-$(uname -m)}"

rm -rf "$STAGE"
install -d "$STAGE/usr/local/bin" \
    "$STAGE/usr/local/libexec/kikimora" \
    "$STAGE/Library/LaunchDaemons" \
    "$STAGE/Applications"

# ---- Go binaries ----
echo "  Building kikimora-core (darwin/$ARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)

echo "  Building kikimora-toad (darwin/$ARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-toad" ./cmd/kikimora-toad)

# ---- CLI entry point ----
install -m 0755 "$ROOT/linux/kikimora" "$STAGE/usr/local/bin/kikimora"
ln -sfn /usr/local/bin/kikimora "$STAGE/usr/local/bin/kk"

# ---- Launchd plist ----
if [[ -f "$ROOT/macos/files/com.kikimora.core.plist" ]]; then
    install -m 0644 "$ROOT/macos/files/com.kikimora.core.plist" \
        "$STAGE/Library/LaunchDaemons/com.kikimora.core.plist"
fi

echo "  Staged: $STAGE"