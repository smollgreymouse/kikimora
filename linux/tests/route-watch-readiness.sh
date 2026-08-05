#!/usr/bin/env bash
set -Eeuo pipefail

readonly ROUTE_WATCH="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/route-watch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p \
  "$tmp/bin" \
  "$tmp/runtime" \
  "$tmp/lifecycle-state" \
  "$tmp/watch-state" \
  "$tmp/mock-state"

touch "$tmp/lifecycle-state/devices"

cat >"$tmp/bin/reconcile" <<'EOF_RECONCILE'
#!/usr/bin/env bash
set -Eeuo pipefail

count_file="${MOCK_STATE_DIR}/reconcile-count"
count=0
[[ ! -r "$count_file" ]] || IFS= read -r count <"$count_file"
count=$((count + 1))
printf '%s\n' "$count" >"$count_file"

if ((count >= 2)); then
  printf 'vpn0\n' >"${KIKIMORA_VPN_RUNTIME_DIR}/secondary.dev"
fi
EOF_RECONCILE
chmod +x "$tmp/bin/reconcile"

cat >"$tmp/bin/lifecycle" <<'EOF_LIFECYCLE'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/lifecycle.log"
EOF_LIFECYCLE
chmod +x "$tmp/bin/lifecycle"

cat >"$tmp/bin/systemctl" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/systemctl.log"
printf 'route-watch must not manage leshy.service: %s\n' "$*" >&2
exit 64
EOF_SYSTEMCTL
chmod +x "$tmp/bin/systemctl"

cat >"$tmp/bin/resolvectl" <<'EOF_RESOLVECTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/resolvectl.log"
EOF_RESOLVECTL
chmod +x "$tmp/bin/resolvectl"

export PATH="$tmp/bin:$PATH"
export MOCK_STATE_DIR="$tmp/mock-state"
export KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime"
export KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/lifecycle-state"
export KIKIMORA_ROUTE_WATCH_STATE_DIR="$tmp/watch-state"
export KIKIMORA_ROUTE_LIFECYCLE="$tmp/bin/lifecycle"
export KIKIMORA_RECONCILE="$tmp/bin/reconcile"
export KIKIMORA_ROUTE_WATCH_MAX_ITERATIONS=2
export LESHY_ROUTE_WATCH_INTERVAL=1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

line_count() {
  local file="$1"
  if [[ -r "$file" ]]; then
    wc -l <"$file"
  else
    printf '0\n'
  fi
}

"$ROUTE_WATCH" >/dev/null 2>&1

[[ "$(line_count "$tmp/mock-state/systemctl.log")" == 0 ]] ||
  fail 'route-watch managed leshy.service after vpn0 became ready'
[[ "$(line_count "$tmp/mock-state/resolvectl.log")" == 1 ]] ||
  fail 'expected one DNS cache flush after vpn0 became ready'
grep -Fqx 'vpn0' "$tmp/watch-state/active.devices" ||
  fail 'expected vpn0 in route-watch active state'

# Starting the watcher while vpn0 is already published must not produce another
# readiness transition or DNS cache flush.
cat >"$tmp/bin/reconcile" <<'EOF_RECONCILE_STEADY'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'vpn0\n' >"${KIKIMORA_VPN_RUNTIME_DIR}/secondary.dev"
EOF_RECONCILE_STEADY
chmod +x "$tmp/bin/reconcile"
export KIKIMORA_ROUTE_WATCH_MAX_ITERATIONS=1

"$ROUTE_WATCH" >/dev/null 2>&1

[[ "$(line_count "$tmp/mock-state/systemctl.log")" == 0 ]] ||
  fail 'watcher restart with an already-published device managed leshy.service'
[[ "$(line_count "$tmp/mock-state/resolvectl.log")" == 1 ]] ||
  fail 'watcher restart with an already-published device caused an extra flush'

printf 'Route-watch readiness transition tests: OK\n'
