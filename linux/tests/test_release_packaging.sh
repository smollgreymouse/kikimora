#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Static contract for the chapter-08 canonical Linux console/runtime package.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ARCH="${KIKIMORA_ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"

echo "==> building chapter-08 console package"
KIKIMORA_OUT_DIR="$TMP/dist" bash "$ROOT/packaging/linux/build-release.sh" >/dev/null
DEB="$TMP/dist/kikimora_${VERSION}_${ARCH}.deb"
TGZ="$TMP/dist/kikimora-${VERSION}-linux-${ARCH}.tar.gz"
[[ -f "$DEB" && -f "$TGZ" && -f "$TMP/dist/SHA256SUMS" ]]

[[ "$(dpkg-deb --field "$DEB" Package)" == kikimora ]]
[[ "$(dpkg-deb --field "$DEB" Version)" == "$VERSION" ]]
[[ "$(dpkg-deb --field "$DEB" Architecture)" == "$ARCH" ]]
deps="$(dpkg-deb --field "$DEB" Depends)"
for dep in bash iproute2 systemd openconnect network-manager; do
  grep -Eq "(^|, )[[:space:]]*$dep([[:space:]]|,|$)" <<<"$deps" || {
    echo "FAIL: missing package dependency $dep: $deps" >&2
    exit 1
  }
done
if grep -Eqi '(^|[,[:space:]])libqt|qt6' <<<"$deps"; then
  echo "FAIL: chapter-08 console package must not depend on Qt: $deps" >&2
  exit 1
fi

dpkg-deb -x "$DEB" "$TMP/root"
dpkg-deb -e "$DEB" "$TMP/control"

required=(
  usr/local/bin/kikimora-core
  usr/local/bin/kikimora-toad
  usr/local/sbin/kikimora
  usr/local/bin/kk
  usr/local/libexec/kikimora/cli/common.sh
  usr/local/libexec/kikimora/cli/orchestration.sh
  usr/local/libexec/kikimora/endpoint-providers/static
  usr/local/libexec/kikimora/endpoint-providers/command
  usr/local/libexec/kikimora/endpoint-providers/happ
  usr/lib/systemd/system/kikimora-core.service
  usr/lib/tmpfiles.d/kikimora-core.conf
  usr/lib/sysusers.d/kikimora-core.conf
  usr/share/kikimora/VERSION
  usr/share/kikimora/orchestration-ownership.conf
  etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf
  usr/share/bash-completion/completions/kikimora
  usr/local/share/zsh/site-functions/_kikimora
  usr/share/fish/vendor_completions.d/kikimora.fish
)
for path in "${required[@]}"; do
  [[ -e "$TMP/root/$path" || -L "$TMP/root/$path" ]] || {
    echo "FAIL: missing package path $path" >&2
    exit 1
  }
done

for forbidden in   usr/bin/kikimora-ui   usr/share/applications/kikimora.desktop   usr/share/icons/hicolor/256x256/apps/kikimora.png; do
  if [[ -e "$TMP/root/$forbidden" || -L "$TMP/root/$forbidden" ]]; then
    echo "FAIL: UI payload leaked into console package: $forbidden" >&2
    exit 1
  fi
done

service="$TMP/root/usr/lib/systemd/system/kikimora-core.service"
grep -Fq 'ExecStart=/usr/local/bin/kikimora-core serve' "$service"
grep -Fq -- '--config-dir /etc/kikimora/toads' "$service"
grep -Fq -- '--state-dir /var/lib/kikimora/core' "$service"
grep -Fq -- '--leshy-publication-dir /run/kikimora/leshy/vpn' "$service"
if grep -Fq -- '--legacy-vpn-config' "$service"; then
  echo "FAIL: canonical service must not require legacy vpn.conf" >&2
  exit 1
fi

for bin in usr/local/bin/kikimora-core usr/local/bin/kikimora-toad usr/local/sbin/kikimora; do
  [[ "$(stat -c '%a' "$TMP/root/$bin")" == 755 ]] || {
    echo "FAIL: executable mode for $bin" >&2
    exit 1
  }
done
[[ "$(readlink "$TMP/root/usr/local/bin/kk")" == /usr/local/sbin/kikimora ]]

"$TMP/root/usr/local/bin/kikimora-core" version | grep -Fq "$VERSION"
"$TMP/root/usr/local/bin/kikimora-toad" version | grep -Fq "$VERSION"
"$TMP/root/usr/local/bin/kikimora-core" help 2>&1 | grep -Fq 'watch [--socket PATH] [--json]'
grep -Fq 'watch [--json]' "$TMP/root/usr/local/libexec/kikimora/cli/help.sh"

# Package install is inert with respect to ownership/cutover and VPN desired
# state. Upgrade/remove/purge have explicit lifecycle semantics.
for script in prerm postinst postrm; do
  [[ -x "$TMP/control/$script" ]]
done
if grep -Eq 'systemctl[[:space:]]+(start|enable)|orchestration[[:space:]]+cutover|ConnectAll' "$TMP/control/postinst"; then
  echo "FAIL: fresh postinst must not start/cut over VPN ownership" >&2
  exit 1
fi
grep -Fq '0750 /etc/kikimora/toads' "$TMP/control/postinst"
grep -Fq '0700 /etc/kikimora/secrets' "$TMP/control/postinst"
grep -Fq 'restart-after-upgrade' "$TMP/control/prerm"
grep -Fq 'systemctl stop kikimora-core.service' "$TMP/control/prerm"
grep -Fq 'systemctl disable kikimora-core.service' "$TMP/control/prerm"
grep -Fq 'restart-after-upgrade' "$TMP/control/postinst"
grep -Fq 'systemctl restart kikimora-core.service' "$TMP/control/postinst"
grep -Fq 'purge)' "$TMP/control/postrm"
grep -Fq 'rm -rf /var/lib/kikimora/core' "$TMP/control/postrm"
upgrade_postrm="$(awk '/^[[:space:]]*upgrade\)/,/^[[:space:]]*failed-upgrade\|abort-upgrade\)/' "$TMP/control/postrm")"
grep -Fq 'Keep the marker' <<<"$upgrade_postrm"
if grep -Fq 'rm -f /run/kikimora-package/restart-after-upgrade' <<<"$upgrade_postrm"; then
  echo "FAIL: old-package postrm upgrade must preserve the restart marker for new postinst" >&2
  exit 1
fi
grep -Fq 'systemctl restart kikimora-core.service' "$TMP/control/postrm"
if grep -Eq 'rm -rf[[:space:]]+/etc/kikimora([/[:space:]]|$)' "$TMP/control/postrm"; then
  echo "FAIL: purge must preserve admin-created /etc/kikimora config/secrets" >&2
  exit 1
fi

# No real profiles, bearer links, or credential files may be in the artifact.
if find "$TMP/root" -type f \( -name '*.secret' -o -name 'real-vps-*' -o -name '*password.secret' -o -name '*token.secret' \) | grep -q .; then
  echo "FAIL: credential/test profile leaked into package" >&2
  exit 1
fi

core_count="$(find "$TMP/root" -type f -name kikimora-core | wc -l)"
toad_count="$(find "$TMP/root" -type f -name kikimora-toad | wc -l)"
[[ "$core_count" == 1 && "$toad_count" == 1 ]]

(cd "$TMP/dist" && sha256sum -c SHA256SUMS >/dev/null)
tar -tzf "$TGZ" >"$TMP/tar.list"
for path in ./usr/local/bin/kikimora-core ./usr/local/bin/kikimora-toad ./usr/local/sbin/kikimora; do
  grep -qx "$path" "$TMP/tar.list"
done
if grep -Eq 'kikimora-ui|usr/share/applications/kikimora.desktop' "$TMP/tar.list"; then
  echo "FAIL: UI payload leaked into console tarball" >&2
  exit 1
fi

echo "chapter-08 Linux console package: PASS"
echo "deb=$DEB"
sha256sum "$DEB"
