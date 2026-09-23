#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# stage-release.sh — canonical Linux staging builder.
#
# Stages all product files into a directory tree suitable for .deb and .tar.gz.
# Called by build-release.sh; not meant to be executed directly.
#
# Usage: stage-release.sh <stage-dir> [arch]

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

STAGE="${1:?usage: stage-release.sh <stage-dir> [arch]}"
ARCH="${2:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"

rm -rf "$STAGE"
install -d "$STAGE/DEBIAN" \
    "$STAGE/usr/local/bin" \
    "$STAGE/usr/local/sbin" \
    "$STAGE/usr/local/libexec/kikimora/endpoint-providers" \
    "$STAGE/usr/local/libexec/kikimora/cli" \
    "$STAGE/usr/lib/systemd/system" \
    "$STAGE/usr/lib/tmpfiles.d" \
    "$STAGE/usr/lib/sysusers.d" \
    "$STAGE/usr/share/kikimora" \
    "$STAGE/usr/share/applications" \
    "$STAGE/usr/share/icons/hicolor/256x256/apps" \
    "$STAGE/etc/NetworkManager/conf.d" \
    "$STAGE/usr/bin"

# ---- Go binaries ----
echo "  Building kikimora-core..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)
chmod 755 "$STAGE/usr/local/bin/kikimora-core"

echo "  Building kikimora-toad..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-toad" ./cmd/kikimora-toad)
chmod 755 "$STAGE/usr/local/bin/kikimora-toad"

# ---- CLI ----
install -m 0755 "$ROOT/linux/kikimora" "$STAGE/usr/local/sbin/kikimora"
ln -sfn /usr/local/sbin/kikimora "$STAGE/usr/local/bin/kk"

# ---- CLI library files ----
for cli_file in common.sh help.sh dns.sh service.sh status.sh domains.sh config.sh maintenance.sh orchestration.sh; do
    install -m 0644 "$ROOT/linux/files/kikimora-cli/$cli_file" \
        "$STAGE/usr/local/libexec/kikimora/cli/$cli_file"
done

# ---- Endpoint providers ----
for provider in static command happ; do
    if [[ -f "$ROOT/linux/files/endpoint-providers/$provider" ]]; then
        install -m 0755 "$ROOT/linux/files/endpoint-providers/$provider" \
            "$STAGE/usr/local/libexec/kikimora/endpoint-providers/$provider"
    fi
done

# ---- System integration ----
install -m 0644 "$ROOT/linux/files/kikimora-core.service" \
    "$STAGE/usr/lib/systemd/system/kikimora-core.service"
install -m 0644 "$ROOT/linux/files/kikimora-core.tmpfiles.conf" \
    "$STAGE/usr/lib/tmpfiles.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/kikimora-core.sysusers.conf" \
    "$STAGE/usr/lib/sysusers.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/orchestration-ownership.conf" \
    "$STAGE/usr/share/kikimora/orchestration-ownership.conf"
install -m 0644 "$ROOT/linux/files/90-kikimora-unmanaged.conf" \
    "$STAGE/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf"

# ---- Desktop entry and icon ----
install -m 0644 "$ROOT/desktop/packaging/kikimora.desktop.in" \
    "$STAGE/usr/share/applications/kikimora.desktop"
if [[ -f "$ROOT/desktop/resources/kikimora.png" ]]; then
    install -m 0644 "$ROOT/desktop/resources/kikimora.png" \
        "$STAGE/usr/share/icons/hicolor/256x256/apps/kikimora.png"
fi

# ---- DEBIAN control file ----
cat > "$STAGE/DEBIAN/control" <<EOF
Package: kikimora
Version: $KIKIMORA_VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: Kikimora maintainers
Depends: bash, iproute2, systemd, libqt6core6t64 | libqt6core6, libqt6gui6t64 | libqt6gui6, libqt6network6t64 | libqt6network6, libqt6qml6 | libqt6qml6t64, libqt6quick6 | libqt6quick6t64, libqt6widgets6t64 | libqt6widgets6
Description: Kikimora VPN control plane and desktop integration
 Kikimora is a multi-protocol managed VPN client with a Qt 6 desktop UI.
 It supports AmneziaWG, Xray (VLESS/REALITY), and OpenConnect protocols
 through independently supervised Toad processes.
EOF

echo "  Staged: $STAGE"
