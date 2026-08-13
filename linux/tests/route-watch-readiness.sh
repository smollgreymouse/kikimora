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
  "$tmp/endpoint-state" \
  "$tmp/endpoints"
touch "$tmp/lifecycle-state/devices"
: > "$tmp/endpoints/primary.txt"
: > "$tmp/endpoints/secondary.txt"
cat > "$tmp/vpn.conf" <<EOF_CONFIG
PRIMARY_INTERFACE="amn0"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
SECONDARY_INTERFACE="vpn0"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
EOF_CONFIG

cat > "$tmp/bin/reconcile" <<'EOF_RECONCILE'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${MOCK_RECONCILE_MODE}" in
  ready)
    printf 'amn0\n' > "${MOCK_RUNTIME_DIR}/primary.dev"
    printf 'primary   amn0  ready\n'
    ;;
  down)
    rm -f -- "${MOCK_RUNTIME_DIR}/primary.dev"
    printf 'primary   amn0  down\n'
    ;;
  *) exit 64 ;;
esac
EOF_RECONCILE

cat > "$tmp/bin/lifecycle" <<'EOF_LIFECYCLE'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'lifecycle %s\n' "$*" >> "${MOCK_EVENT_LOG}"
EOF_LIFECYCLE

cat > "$tmp/bin/systemctl" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'systemctl %s\n' "$*" >> "${MOCK_EVENT_LOG}"
case "$*" in
  'is-active --quiet leshy.service'|'try-restart leshy.service') exit 0 ;;
  *) exit 64 ;;
esac
EOF_SYSTEMCTL

cat > "$tmp/bin/resolvectl" <<'EOF_RESOLVECTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'resolvectl %s\n' "$*" >> "${MOCK_EVENT_LOG}"
[[ "$*" == 'flush-caches' ]]
EOF_RESOLVECTL

cat > "$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  '-4 route show table main default')
    printf 'default via 192.0.2.1 dev wlan0 metric 600\n'
    ;;
  '-6 route show table main default'|'-4 rule show'|'-6 rule show')
    exit 0
    ;;
  '-4 rule del '*|'-6 rule del '*)
    exit 2
    ;;
  '-o link show dev '*|'-4 -o address show dev '*)
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/"*

export PATH="$tmp/bin:/usr/bin:/bin"
export MOCK_RUNTIME_DIR="$tmp/runtime"
export MOCK_EVENT_LOG="$tmp/events.log"
export KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime"
export KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/lifecycle-state"
export KIKIMORA_ROUTE_WATCH_STATE_DIR="$tmp/watch-state"
export KIKIMORA_ENDPOINT_STATE_DIR="$tmp/endpoint-state"
export KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_ROUTE_LIFECYCLE="$tmp/bin/lifecycle"
export KIKIMORA_RECONCILE="$tmp/bin/reconcile"
export KIKIMORA_SYSTEMCTL="$tmp/bin/systemctl"
export KIKIMORA_ROUTE_WATCH_MAX_ITERATIONS=1
export KIKIMORA_ENDPOINT_RECONCILE_ITERATIONS=5
export LESHY_ROUTE_WATCH_INTERVAL=1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  cat "$tmp/events.log" >&2 2>/dev/null || true
  exit 1
}

# A VPN becoming ready must invalidate Leshy's internal DNS cache. Otherwise a
# hostname resolved while the VPN was down can stay cached without the route
# side effect that previously failed because the runtime .dev file was absent.
: > "$tmp/events.log"
export MOCK_RECONCILE_MODE=ready
"$ROUTE_WATCH" >/dev/null 2>"$tmp/ready.log"
expected_ready=$'lifecycle begin-device amn0\nsystemctl is-active --quiet leshy.service\nsystemctl try-restart leshy.service\nresolvectl flush-caches'
diff -u <(printf '%s\n' "$expected_ready") "$tmp/events.log" >/dev/null || \
  fail 'ready transition did not restart Leshy before flushing resolved cache'
grep -Fq 'cached route-add failures are retried' "$tmp/ready.log" || \
  fail 'ready transition did not report Leshy cache refresh'

# A previously ready VPN is observed before reconcile can withdraw its device
# file. That observation is what lets route-lifecycle park destinations even if
# the kernel removes interface routes together with the disappearing link.
: > "$tmp/events.log"
export MOCK_RECONCILE_MODE=down
"$ROUTE_WATCH" >/dev/null 2>"$tmp/down.log"
expected_down=$'lifecycle observe-device amn0\nlifecycle cleanup-device amn0\nresolvectl flush-caches'
diff -u <(printf '%s\n' "$expected_down") "$tmp/events.log" >/dev/null || \
  fail 'down transition did not observe then clean up the disappearing VPN'

printf 'Route-watch VPN readiness cache refresh regression: OK\n'
