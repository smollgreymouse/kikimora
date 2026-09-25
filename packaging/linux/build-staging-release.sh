#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Build a side-by-side Linux staging package for installed-host 08A acceptance.
# It must not overwrite the legacy Kikimora CLI or libexec tree.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
# shellcheck disable=SC1091 # dynamic repository-root path
source "$ROOT/packaging/common/version.sh"

ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
OUT_DIR="${KIKIMORA_OUT_DIR:-$ROOT/dist}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$OUT_DIR"
install -d   "$STAGE/DEBIAN"   "$STAGE/opt/kikimora-next/bin"   "$STAGE/opt/kikimora-next/libexec/cli"   "$STAGE/opt/kikimora-next/libexec/endpoint-providers"   "$STAGE/usr/local/bin"   "$STAGE/usr/lib/systemd/system"   "$STAGE/usr/lib/sysusers.d"   "$STAGE/usr/share/kikimora-next"   "$STAGE/etc/NetworkManager/conf.d"

echo "==> Kikimora side-by-side staging package"
echo "    Version: $KIKIMORA_VERSION"
echo "    Architecture: $ARCH"

(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION"   -o "$STAGE/opt/kikimora-next/bin/kikimora-core" ./cmd/kikimora-core)
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.toadVersion=$KIKIMORA_VERSION"   -o "$STAGE/opt/kikimora-next/bin/kikimora-toad" ./cmd/kikimora-toad)
chmod 0755 "$STAGE/opt/kikimora-next/bin/kikimora-core" "$STAGE/opt/kikimora-next/bin/kikimora-toad"

install -m 0755 "$ROOT/linux/kikimora-next" "$STAGE/opt/kikimora-next/bin/kk-next"
ln -s /opt/kikimora-next/bin/kk-next "$STAGE/usr/local/bin/kk-next"
install -m 0644 "$ROOT/linux/files/kikimora-cli/common.sh" "$STAGE/opt/kikimora-next/libexec/cli/common.sh"
install -m 0644 "$ROOT/linux/files/kikimora-cli/orchestration.sh" "$STAGE/opt/kikimora-next/libexec/cli/orchestration.sh"

for provider in static command happ; do
  install -m 0755 "$ROOT/linux/files/endpoint-providers/$provider"     "$STAGE/opt/kikimora-next/libexec/endpoint-providers/$provider"
done

install -m 0644 "$ROOT/linux/files/kikimora-core-next.service"   "$STAGE/usr/lib/systemd/system/kikimora-core-next.service"
install -m 0644 "$ROOT/VERSION" "$STAGE/usr/share/kikimora-next/VERSION"
install -m 0644 "$ROOT/linux/files/orchestration-ownership.conf"   "$STAGE/usr/share/kikimora-next/orchestration-ownership.conf"
install -m 0644 "$ROOT/linux/files/90-kikimora-unmanaged.conf"   "$STAGE/etc/NetworkManager/conf.d/90-kikimora-next-unmanaged.conf"

cat >"$STAGE/usr/lib/sysusers.d/kikimora-next.conf" <<'EOF'
g kikimora -
EOF

cat >"$STAGE/DEBIAN/control" <<EOF
Package: kikimora-next
Version: $KIKIMORA_VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: Kikimora maintainers
Depends: bash, iproute2, systemd, openconnect
Description: Kikimora side-by-side installed-host staging candidate
 Installs the Go core/Toad candidate under /opt/kikimora-next with kk-next and
 a separate systemd unit. It deliberately leaves the legacy Kikimora CLI and
 /usr/local/libexec/kikimora tree untouched for rollback.
EOF

cat >"$STAGE/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e

mkdir -p /etc/kikimora-next/toads /etc/kikimora-next/secrets
chmod 0750 /etc/kikimora-next /etc/kikimora-next/toads
chmod 0700 /etc/kikimora-next/secrets

if [ ! -f /etc/kikimora-next/orchestration-ownership.conf ]; then
  cp /usr/share/kikimora-next/orchestration-ownership.conf \
    /etc/kikimora-next/orchestration-ownership.conf
  chmod 0644 /etc/kikimora-next/orchestration-ownership.conf
fi

if command -v systemd-sysusers >/dev/null 2>&1; then
  systemd-sysusers /usr/lib/sysusers.d/kikimora-next.conf 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
fi

exit 0
EOF
chmod 0755 "$STAGE/DEBIAN/postinst"

cat >"$STAGE/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
fi
exit 0
EOF
chmod 0755 "$STAGE/DEBIAN/postrm"

DEB="$OUT_DIR/kikimora-next_${KIKIMORA_VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$STAGE" "$DEB" >/dev/null
sha256sum "$DEB" >"$OUT_DIR/kikimora-next_SHA256SUMS"

echo "  Created: $DEB"
echo "  Created: $OUT_DIR/kikimora-next_SHA256SUMS"
