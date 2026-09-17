#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${KIKIMORA_PACKAGE_VERSION:-0.1.0}"
OUT_DIR="${KIKIMORA_PACKAGE_OUT:-$ROOT/dist}"
STAGE="$ROOT/build/package-stage"

rm -rf -- "$STAGE"
install -d "$OUT_DIR" "$STAGE/DEBIAN" "$STAGE/usr/local/bin" "$STAGE/usr/local/sbin" "$STAGE/usr/local/libexec/kikimora/endpoint-providers" "$STAGE/usr/local/libexec/kikimora/cli" "$STAGE/usr/lib/systemd/system" "$STAGE/usr/lib/tmpfiles.d" "$STAGE/usr/lib/sysusers.d" "$STAGE/usr/share/kikimora"

(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$VERSION" -o "$STAGE/usr/local/bin/kikimora-toad" ./cmd/kikimora-toad)
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$VERSION" -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)
install -m 0755 "$ROOT/linux/kikimora" "$STAGE/usr/local/sbin/kikimora"
for cli_file in common.sh help.sh dns.sh service.sh status.sh domains.sh config.sh maintenance.sh orchestration.sh; do
    install -m 0644 "$ROOT/linux/files/kikimora-cli/$cli_file" "$STAGE/usr/local/libexec/kikimora/cli/$cli_file"
done
for provider in static command happ; do
    install -m 0755 "$ROOT/linux/files/endpoint-providers/$provider" "$STAGE/usr/local/libexec/kikimora/endpoint-providers/$provider"
done
install -m 0644 "$ROOT/linux/files/kikimora-core.service" "$STAGE/usr/lib/systemd/system/kikimora-core.service"
install -m 0644 "$ROOT/linux/files/kikimora-core.tmpfiles.conf" "$STAGE/usr/lib/tmpfiles.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/kikimora-core.sysusers.conf" "$STAGE/usr/lib/sysusers.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/orchestration-ownership.conf" "$STAGE/usr/share/kikimora/orchestration-ownership.conf"

cat >"$STAGE/DEBIAN/control" <<EOF
Package: kikimora
Version: $VERSION
Section: net
Priority: optional
Architecture: $(dpkg --print-architecture 2>/dev/null || printf '%s' amd64)
Maintainer: Kikimora maintainers
Description: Kikimora VPN control plane and desktop integration
EOF

tar -C "$STAGE" --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner -czf "$OUT_DIR/kikimora-$VERSION.tar.gz" .
if command -v dpkg-deb >/dev/null 2>&1; then dpkg-deb --build --root-owner-group "$STAGE" "$OUT_DIR/kikimora-$VERSION.deb" >/dev/null; fi
printf 'Packages written to %s\n' "$OUT_DIR"
