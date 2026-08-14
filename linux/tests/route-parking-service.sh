#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_SH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/kikimora-cli/service.sh}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
readonly LOG="$tmp/log"

cat > "$tmp/endpoint-ctl" <<'EOF_ENDPOINT'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'endpoint %s\n' "$*" >> "${MOCK_SERVICE_LOG}"
EOF_ENDPOINT

cat > "$tmp/lifecycle-ctl" <<'EOF_LIFECYCLE'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'lifecycle %s\n' "$*" >> "${MOCK_SERVICE_LOG}"
EOF_LIFECYCLE
chmod +x "$tmp/endpoint-ctl" "$tmp/lifecycle-ctl"

export KIKIMORA_ENDPOINT_UNDERLAY_CTL="$tmp/endpoint-ctl"
export KIKIMORA_ROUTE_LIFECYCLE_CTL="$tmp/lifecycle-ctl"
export MOCK_SERVICE_LOG="$LOG"

# Globals consumed by the sourced production fragment.
# shellcheck disable=SC2034
MANAGED_UNITS=(leshy.service leshy-route-watch.service leshy-health-watch.service)
# shellcheck disable=SC2034
PRIMARY_ROUTES="$tmp/unused-primary"
# shellcheck disable=SC2034
SECONDARY_ROUTES="$tmp/unused-secondary"
# shellcheck disable=SC2034
ROUTING_CONFIG="$tmp/unused-routing"
# shellcheck disable=SC2034
ROUTES_DIR="$tmp/unused-routes"

require_root() { :; }
die() { printf 'FAIL: %s\n' "$*" >&2; return 1; }
cmd_dns_ensure_enabled() { printf 'dns ensure\n' >> "$LOG"; }
cmd_dns_suspend_if_available() { printf 'dns suspend\n' >> "$LOG"; }
seed_route_file_from_config() { :; }
systemctl() { printf 'systemctl %s\n' "$*" >> "$LOG"; }

# shellcheck source=../files/kikimora-cli/service.sh
source "$SERVICE_SH"

# Avoid filesystem/config work; this regression is only about lifecycle order.
rebuild_service_config() { printf 'config rebuild\n' >> "$LOG"; }

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  cat "$LOG" >&2 2>/dev/null || true
  exit 1
}
assert_log() {
  local expected="$1"
  diff -u <(printf '%s\n' "$expected") "$LOG" >/dev/null || \
    fail 'unexpected service lifecycle order'
}

# A managed restart must mark route parking for preservation before any service
# is stopped. The next route-lifecycle snapshot clears that marker.
: > "$LOG"
cmd_service restart
assert_log $'config rebuild\nendpoint endpoint apply\nlifecycle prepare-restart\ndns suspend\nsystemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service\nsystemctl start leshy.service leshy-route-watch.service leshy-health-watch.service\ndns ensure'

# Ordinary stop ends Kikimora ownership and explicitly clears any parking left
# behind by an interrupted internal restart.
: > "$LOG"
cmd_service stop
assert_log $'endpoint endpoint check-clear\ndns suspend\nsystemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service\nlifecycle clear-parking\nendpoint endpoint clear'

# disable --now has the same shutdown boundary.
: > "$LOG"
cmd_service disable --now
assert_log $'endpoint endpoint check-clear\ndns suspend\nsystemctl disable --now leshy.service leshy-route-watch.service leshy-health-watch.service\nlifecycle clear-parking\nendpoint endpoint clear'

printf 'Route parking CLI lifecycle regression: OK\n'
