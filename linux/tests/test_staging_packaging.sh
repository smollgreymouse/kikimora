#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

KIKIMORA_OUT_DIR="$TMP/dist" bash "$ROOT/packaging/linux/build-staging-release.sh" >/dev/null
DEB="$TMP/dist/kikimora-next_$(tr -d '[:space:]' < "$ROOT/VERSION")_$(dpkg --print-architecture).deb"

[[ -f "$DEB" ]] || { echo "FAIL: staging deb missing" >&2; exit 1; }
[[ "$(dpkg-deb --field "$DEB" Package)" == "kikimora-next" ]] || {
  echo "FAIL: package name is not kikimora-next" >&2; exit 1; }

dpkg-deb -x "$DEB" "$TMP/root"
dpkg-deb -e "$DEB" "$TMP/control"

required=(
  opt/kikimora-next/bin/kikimora-core
  opt/kikimora-next/bin/kikimora-toad
  opt/kikimora-next/bin/kk-next
  opt/kikimora-next/libexec/cli/common.sh
  opt/kikimora-next/libexec/cli/orchestration.sh
  usr/local/bin/kk-next
  usr/lib/systemd/system/kikimora-core-next.service
  usr/share/kikimora-next/VERSION
  usr/share/kikimora-next/orchestration-ownership.conf
  etc/NetworkManager/conf.d/90-kikimora-next-unmanaged.conf
)
for path in "${required[@]}"; do
  [[ -e "$TMP/root/$path" || -L "$TMP/root/$path" ]] || {
    echo "FAIL: missing staging path $path" >&2; exit 1; }
done

forbidden=(
  usr/local/bin/kk
  usr/local/sbin/kikimora
  usr/local/libexec/kikimora
  etc/kikimora
  usr/lib/systemd/system/kikimora-core.service
)
for path in "${forbidden[@]}"; do
  if [[ -e "$TMP/root/$path" || -L "$TMP/root/$path" ]]; then
    echo "FAIL: staging package collides with legacy path $path" >&2
    exit 1
  fi
done

grep -Fq '/opt/kikimora-next/bin/kikimora-core' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"
grep -Fq '/etc/kikimora-next/toads' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"
grep -Fq '/run/kikimora-next/core.sock' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"
grep -Fq '/etc/kikimora-next/orchestration-ownership.conf' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"
grep -Fq -- '--leshy-publication-dir /run/kikimora-next/leshy/vpn' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"
if grep -Fq -- '--legacy-vpn-config /etc/kikimora/leshy/vpn.conf' "$TMP/root/usr/lib/systemd/system/kikimora-core-next.service"; then
  echo "FAIL: side-by-side service must not depend on or mutate the legacy vpn.conf namespace" >&2
  exit 1
fi
grep -Fq 'side-by-side staging deliberately disables cutover/rollback/retirement' "$TMP/root/opt/kikimora-next/bin/kk-next"
for command in 'status [--json]' 'watch [--json]' 'connect [--role NAME]' 'disconnect [--role NAME]' 'retry --role NAME' 'restart [--role NAME|--all]'; do
  grep -Fq "$command" "$TMP/root/opt/kikimora-next/bin/kk-next" || {
    echo "FAIL: staging console missing runtime command: $command" >&2
    exit 1
  }
done
"$TMP/root/opt/kikimora-next/bin/kikimora-core" help 2>&1 | grep -Fq 'watch [--socket PATH] [--json]'
if grep -Fq '/etc/kikimora/leshy/orchestration-ownership.conf' "$TMP/root/opt/kikimora-next/bin/kk-next"; then
  echo "FAIL: staging CLI must not use shared ownership state before cutover is armed" >&2
  exit 1
fi

if grep -Eq 'systemctl[[:space:]]+(start|enable)|orchestration[[:space:]]+cutover' "$TMP/control/postinst"; then
  echo "FAIL: staging postinst must not activate candidate or cut over ownership" >&2
  exit 1
fi
grep -Fq 'chown root:kikimora /etc/kikimora-next /etc/kikimora-next/toads' "$TMP/control/postinst" || {
  echo "FAIL: staging postinst must grant the kikimora operator group access to non-secret candidate config" >&2
  exit 1
}
grep -Fq 'chown root:root /etc/kikimora-next/secrets' "$TMP/control/postinst" || {
  echo "FAIL: staging secrets directory must remain root-owned" >&2
  exit 1
}

"$TMP/root/opt/kikimora-next/bin/kikimora-core" version | grep -Fq "$(tr -d '[:space:]' < "$ROOT/VERSION")"
"$TMP/root/opt/kikimora-next/bin/kikimora-toad" version | grep -Fq "$(tr -d '[:space:]' < "$ROOT/VERSION")"

echo "side-by-side staging package: PASS"
