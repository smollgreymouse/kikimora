#!/bin/bash
set -Eeu -o pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d /tmp/kikimora-macos-route-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/runtime/vpn"
printf 'utun4\n' > "$tmp/runtime/vpn/primary.dev"

cat > "$tmp/bin/netstat" <<'EOF'
#!/bin/bash
cat "$MOCK_ROUTES"
EOF
cat > "$tmp/bin/route" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "$MOCK_ROUTE_LOG"
EOF
chmod +x "$tmp/bin/netstat" "$tmp/bin/route"

cat > "$tmp/routes" <<'EOF'
Routing tables
Internet:
Destination Gateway Flags Netif Expire
10.0.0.1 link#1 UH utun4
EOF

run_lifecycle() {
  KIKIMORA_SKIP_PLATFORM_CHECK=1 KIKIMORA_RUNTIME_ROOT="$tmp/runtime" \
  KIKIMORA_RUNTIME_DIR="$tmp/runtime/vpn" \
  KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/runtime/route-lifecycle" \
  NETSTAT_BIN="$tmp/bin/netstat" ROUTE_BIN="$tmp/bin/route" \
  MOCK_ROUTES="$tmp/routes" MOCK_ROUTE_LOG="$tmp/route.log" \
    "$ROOT/macos/route-lifecycle" "$@" >/dev/null
}

run_lifecycle snapshot
cat >> "$tmp/routes" <<'EOF'
10.0.0.2 link#1 UH utun4
EOF
run_lifecycle cleanup-device utun4
grep -Fq -- '-host 10.0.0.2 -interface utun4' "$tmp/route.log"
if grep -Fq -- '-host 10.0.0.1 ' "$tmp/route.log"; then
  printf 'pre-existing route was removed\n' >&2
  exit 1
fi
printf 'macOS route lifecycle tests: OK\n'
