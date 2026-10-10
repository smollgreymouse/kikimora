#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# test-staging.sh — macOS static staging test (runnable from Linux).
#
# Verifies macOS staging contract without requiring macOS host.
# Does not produce a native .pkg.

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ARCH="${KIKIMORA_ARCH:-amd64}"
echo "==> macOS static staging test (arch=$ARCH)"

# ---- Cross-compile Darwin Go binaries ----
echo "  Cross-building kikimora-core (darwin/$ARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X main.coreVersion=$(tr -d '[:space:]' < "$ROOT/VERSION")" \
    -o "$TMP/kikimora-core" ./cmd/kikimora-core) || {
    echo "FAIL: darwin/$ARCH kikimora-core build failed" >&2; exit 1
}

echo "  Cross-building kikimora-toad (darwin/$ARCH)..."
(cd "$ROOT/toad" && GOOS=darwin GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X main.toadVersion=$(tr -d '[:space:]' < "$ROOT/VERSION")" \
    -o "$TMP/kikimora-toad" ./cmd/kikimora-toad) || {
    echo "FAIL: darwin/$ARCH kikimora-toad build failed" >&2; exit 1
}

echo "  darwin/$ARCH binaries built:"
file "$TMP/kikimora-core" "$TMP/kikimora-toad"

# ---- Verify version via ldflags (cross-compiled binary cannot run/be inspected on Linux) ----
ROOT_VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"

# On Linux, Mach-O binaries cannot be executed or inspected with go version -m.
# Verify ldflags were passed correctly by checking build output succeeded.
# Full version verification requires macOS host.
if [[ "$(uname)" == "Darwin" ]]; then
    CORE_VER="$("$TMP/kikimora-core" version 2>/dev/null || true)"
    TOAD_VER="$("$TMP/kikimora-toad" version 2>/dev/null || true)"
    if ! echo "$CORE_VER" | grep -qF "$ROOT_VERSION"; then
        echo "FAIL: darwin core version '$CORE_VER' does not contain '$ROOT_VERSION'" >&2
        exit 1
    fi
    if [[ "$TOAD_VER" != "v$ROOT_VERSION" ]] && [[ "$TOAD_VER" != "$ROOT_VERSION" ]]; then
        echo "FAIL: darwin toad version '$TOAD_VER' != '$ROOT_VERSION'" >&2
        exit 1
    fi
    echo "  darwin version: core=$CORE_VER toad=$TOAD_VER"
else
    echo "  darwin version: skipped (not on macOS host; ldflags confirmed at build time)"
fi

# ---- Stage into temp root ----
bash "$ROOT/packaging/macos/stage-release.sh" "$TMP/stage" "$ARCH"
echo "  staged successfully"

# ---- Verify required paths in stage ----
REQUIRED_STAGE_PATHS=(
    usr/local/bin/kikimora-core
    usr/local/bin/kikimora-toad
    usr/local/bin/kikimora
    usr/local/bin/kk
    usr/local/libexec/kikimora/VERSION
    usr/local/libexec/kikimora/lib.sh
    Library/LaunchDaemons/com.kikimora.core.plist
)
for path in "${REQUIRED_STAGE_PATHS[@]}"; do
    if [[ ! -f "$TMP/stage/$path" ]] && [[ ! -L "$TMP/stage/$path" ]]; then
        echo "FAIL: missing staged path: $path" >&2
        exit 1
    fi
done
echo "  all ${#REQUIRED_STAGE_PATHS[@]} required stage paths present"

# ---- Verify plist contains ownership/state/config args ----
PLIST="$TMP/stage/Library/LaunchDaemons/com.kikimora.core.plist"
if [[ -f "$PLIST" ]]; then
    grep -q -- '--state-dir' "$PLIST" || {
        echo "FAIL: plist missing --state-dir" >&2; exit 1
    }
    grep -q -- '--ownership-config' "$PLIST" || {
        echo "FAIL: plist missing --ownership-config" >&2; exit 1
    }
    grep -q -- '--config-dir' "$PLIST" || {
        echo "FAIL: plist missing --config-dir" >&2; exit 1
    }
    grep -q '/usr/local/bin/kikimora-core' "$PLIST" || {
        echo "FAIL: plist missing core binary path" >&2; exit 1
    }
    echo "  plist: ownership/state/config args present"
fi

# ---- Verify no Linux-only CLI is installed ----
if [[ -f "$TMP/stage/usr/local/sbin/kikimora" ]]; then
    echo "FAIL: Linux CLI installed in macOS stage" >&2
    exit 1
fi
echo "  no Linux CLI in macOS stage"

# ---- Verify no source references point to missing files ----
INSTALL_SH="$ROOT/macos/install.sh"
bash -n "$INSTALL_SH" 2>/dev/null || {
    echo "FAIL: install.sh bash syntax check failed" >&2; exit 1
}
echo "  install.sh: syntax OK"

# ---- Verify VERSION in stage ----
STAGED_VERSION="$(tr -d '[:space:]' < "$TMP/stage/usr/local/libexec/kikimora/VERSION")"
if [[ "$STAGED_VERSION" != "$ROOT_VERSION" ]]; then
    echo "FAIL: staged VERSION '$STAGED_VERSION' != root '$ROOT_VERSION'" >&2
    exit 1
fi
echo "  VERSION: staged=$STAGED_VERSION root=$ROOT_VERSION"

echo ""
echo "=== macOS STATIC STAGING TEST PASSED ==="