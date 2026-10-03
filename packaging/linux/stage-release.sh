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
    "$STAGE/usr/share/bash-completion/completions" \
    "$STAGE/usr/local/share/zsh/site-functions" \
    "$STAGE/usr/share/fish/vendor_completions.d" \
    "$STAGE/etc/NetworkManager/conf.d"

# ---- Go binaries ----
echo "  Building kikimora-core..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)
chmod 755 "$STAGE/usr/local/bin/kikimora-core"

echo "  Building kikimora-toad..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.toadVersion=$KIKIMORA_VERSION" \
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
install -m 0644 "$ROOT/VERSION" \
    "$STAGE/usr/share/kikimora/VERSION"
install -m 0644 "$ROOT/linux/files/90-kikimora-unmanaged.conf" \
    "$STAGE/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf"

# ---- Shell completion ----
install -m 0644 "$ROOT/linux/completions/kikimora.bash" \
    "$STAGE/usr/share/bash-completion/completions/kikimora"
install -m 0644 "$ROOT/linux/completions/_kikimora" \
    "$STAGE/usr/local/share/zsh/site-functions/_kikimora"
install -m 0644 "$ROOT/linux/completions/kikimora.fish" \
    "$STAGE/usr/share/fish/vendor_completions.d/kikimora.fish"

# ---- DEBIAN control file ----
cat > "$STAGE/DEBIAN/control" <<EOF
Package: kikimora
Version: $KIKIMORA_VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: Kikimora maintainers
Depends: bash, iproute2, systemd, openconnect, network-manager
Description: Kikimora console VPN control plane and managed Toad runtime
 Kikimora provides a versioned local control API, the kk console client,
 and independently supervised AmneziaWG, Xray (VLESS/REALITY), and
 OpenConnect Toad processes. The optional Qt UI is packaged separately.
EOF

# ---- DEBIAN postinst ----
cat > "$STAGE/DEBIAN/postinst" <<'POSTINST'
#!/bin/sh
set -e

# Create the service group before assigning group-owned paths.
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers /usr/lib/sysusers.d/kikimora-core.conf 2>/dev/null || true
fi

# Create required directories without making secrets world-readable.
install -d -o root -g root -m 0755 /etc/kikimora
install -d -o root -g root -m 0755 /etc/kikimora/leshy
install -d -o root -g kikimora -m 0750 /etc/kikimora/toads
install -d -o root -g root -m 0700 /etc/kikimora/secrets

# Install default ownership config only when missing (preserve admin edits)
if [ ! -f /etc/kikimora/leshy/orchestration-ownership.conf ]; then
    cp /usr/share/kikimora/orchestration-ownership.conf \
       /etc/kikimora/leshy/orchestration-ownership.conf
    chmod 0644 /etc/kikimora/leshy/orchestration-ownership.conf
fi

# systemd integration (best-effort)
if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
fi

exit 0
POSTINST
chmod 0755 "$STAGE/DEBIAN/postinst"

# ---- DEBIAN postrm ----
cat > "$STAGE/DEBIAN/postrm" <<'POSTRM'
#!/bin/sh
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
fi

exit 0
POSTRM
chmod 0755 "$STAGE/DEBIAN/postrm"

echo "  Staged: $STAGE"
