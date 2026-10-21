#!/usr/bin/env bash
# Zone device resolution after the Go core took over route ownership.
#
# The core publishes one device file per role (named after the role's
# leshy_zone, holding its interface). The legacy primary.dev/secondary.dev pair
# is no longer created, and without a fallback every primary-zone domain lost
# its tunnel path after a Leshy restart ("Device file ... not found").
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
GENERATOR="${REPO_ROOT}/linux/files/build-config"
failures=0

check() {
  local label="$1" expected="$2" file="$3"
  if grep -Fq "route_target = \"${expected}\"" "$file"; then
    printf 'ok   %s\n' "$label"
  else
    printf 'FAIL %s: expected route_target %s in %s\n' "$label" "$expected" "$file" >&2
    grep -F 'route_target' "$file" >&2 || true
    failures=$((failures + 1))
  fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/domains" "$work/routes" "$work/run"
printf 'chatgpt.com\nopenai.com\n' >"$work/domains/primary.txt"
: >"$work/domains/secondary.txt"
: >"$work/domains/bypass.txt"
: >"$work/routes/primary.txt"
: >"$work/routes/secondary.txt"
printf 'DEFAULT_ZONE="direct"\nUPSTREAM_ZONE="direct"\n' >"$work/routing.conf"

# The core publishes the role devices; there is no primary.dev.
printf 'kk-awg0\n' >"$work/run/awg.dev"
printf 'kk-xray0\n' >"$work/run/xray.dev"

VPN_RUNTIME_DIR="$work/run" bash "$GENERATOR" \
  "$work/domains" "$work/config.toml" "$work/routing.conf" "$work/routes" >/dev/null

check "primary zone follows the published role device" \
  "$work/run/awg.dev" "$work/config.toml"

# An explicit PRIMARY_INTERFACE picks its own role device.
printf 'PRIMARY_INTERFACE="kk-xray0"\n' >"$work/domains/../vpn.conf"
VPN_RUNTIME_DIR="$work/run" bash "$GENERATOR" \
  "$work/domains" "$work/config-pinned.toml" "$work/routing.conf" "$work/routes" >/dev/null
check "PRIMARY_INTERFACE pins the role device" \
  "$work/run/xray.dev" "$work/config-pinned.toml"
rm -f "$work/domains/../vpn.conf"

# The legacy file still wins when it exists.
printf 'kk-legacy0\n' >"$work/run/primary.dev"
VPN_RUNTIME_DIR="$work/run" bash "$GENERATOR" \
  "$work/domains" "$work/config-legacy.toml" "$work/routing.conf" "$work/routes" >/dev/null
check "legacy device file keeps priority" \
  "$work/run/primary.dev" "$work/config-legacy.toml"

if ((failures)); then
  printf '%d zone-device checks failed\n' "$failures" >&2
  exit 1
fi
printf 'zone-device fallback verified\n'
