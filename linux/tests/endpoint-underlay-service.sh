#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_SH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/kikimora-cli/service.sh}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
readonly LOG="$tmp/log"
readonly ALLOW_CLEAR="$tmp/allow-clear"

cat > "$tmp/endpoint-ctl" <<'EOF_CTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'endpoint %s\n' "$*" >> "${MOCK_SERVICE_LOG}"
if [[ "$*" == 'endpoint check-clear' && ! -e ${MOCK_ALLOW_CLEAR} ]]; then
  exit 75
fi
EOF_CTL
chmod +x "$tmp/endpoint-ctl"

export KIKIMORA_ENDPOINT_UNDERLAY_CTL="$tmp/endpoint-ctl"
export MOCK_SERVICE_LOG="$LOG"
export MOCK_ALLOW_CLEAR="$ALLOW_CLEAR"
MANAGED_UNITS=(leshy.service leshy-route-watch.service leshy-health-watch.service)
PRIMARY_ROUTES="$tmp/unused-primary"
SECONDARY_ROUTES="$tmp/unused-secondary"
ROUTING_CONFIG="$tmp/unused-routing"
ROUTES_DIR="$tmp/unused-routes"

require_root() { :; }
die() { printf 'FAIL: %s\n' "$*" >&2; return 1; }
cmd_dns_ensure_enabled() { printf 'dns ensure\n' >> "$LOG"; }
cmd_dns_suspend_if_available() { printf 'dns suspend\n' >> "$LOG"; }
seed_route_file_from_config() { :; }
systemctl() { printf 'systemctl %s\n' "$*" >> "$LOG"; }

# shellcheck source=../files/kikimora-cli/service.sh
source "$SERVICE_SH"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  cat "$LOG" >&2 2>/dev/null || true
  exit 1
}
assert_log() {
  local expected="$1"
  diff -u <(printf '%s\n' "$expected") "$LOG" >/dev/null || fail "unexpected lifecycle order"
}

: > "$LOG"
cmd_service start
assert_log $'endpoint endpoint apply\nsystemctl start leshy.service leshy-route-watch.service leshy-health-watch.service\ndns ensure'

# A normal stop must fail before DNS or systemd is touched when policy cannot
# safely be removed below a live managed VPN.
: > "$LOG"
rm -f "$ALLOW_CLEAR"
if cmd_service stop >/dev/null 2>&1; then
  fail 'stop unexpectedly succeeded while clear preflight failed'
fi
assert_log 'endpoint endpoint check-clear'

# Once the VPN is disconnected, stop is ordered: preflight, DNS suspend,
# services stop, then endpoint policy clear.
: > "$LOG"
touch "$ALLOW_CLEAR"
cmd_service stop
assert_log $'endpoint endpoint check-clear\ndns suspend\nsystemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service\nendpoint endpoint clear'

# Forced stop is explicit and skips the preflight, but still clears only after
# services are stopped.
: > "$LOG"
rm -f "$ALLOW_CLEAR"
cmd_service stop --force
assert_log $'dns suspend\nsystemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service\nendpoint endpoint clear --force'

# enable --now must protect endpoints before systemd starts services.
: > "$LOG"
cmd_service enable --now
assert_log $'endpoint endpoint apply\nsystemctl enable --now leshy.service leshy-route-watch.service leshy-health-watch.service\ndns ensure'

printf 'VPN endpoint CLI lifecycle regression: OK\n'
