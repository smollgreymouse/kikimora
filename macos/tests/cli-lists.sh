#!/bin/bash
set -Eeu -o pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d /tmp/kikimora-macos-cli-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/config/domains" "$tmp/config/routes" "$tmp/state" "$tmp/runtime" "$tmp/bin"
for zone in primary secondary bypass; do : > "$tmp/config/domains/$zone.txt"; done
for zone in primary secondary; do : > "$tmp/config/routes/$zone.txt"; done
cat > "$tmp/config/routing.conf" <<'EOF'
DEFAULT_ZONE=direct
DEFAULT_UPSTREAMS="1.1.1.1:53"
UPSTREAM_ZONE=direct
EOF
cat > "$tmp/config/vpn.conf" <<EOF
PRIMARY_INTERFACE="utun4"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
SECONDARY_INTERFACE="utun5"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
VPN_LINK_READY_SUCCESSES=3
EOF
printf 'DNS_SERVICES=("Wi-Fi")\n' > "$tmp/config/macos.conf"
cat > "$tmp/bin/leshy" <<'EOF'
#!/bin/bash
case "${1:-}" in
  --help) printf '%s\n' '--check-config' ;;
  --check-config) exit 0 ;;
  --version) printf 'leshy 0.4.0\n' ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$tmp/bin/leshy"
"$ROOT/macos/build-config" "$tmp/config/domains" "$tmp/config/config.toml" "$tmp/config/routing.conf" "$tmp/config/routes" >/dev/null

run_cli() {
  KIKIMORA_SKIP_PLATFORM_CHECK=1 KIKIMORA_SKIP_ROOT_CHECK=1 \
  KIKIMORA_CONFIG_DIR="$tmp/config" KIKIMORA_STATE_DIR="$tmp/state" \
  KIKIMORA_RUNTIME_ROOT="$tmp/runtime" LESHY_BIN="$tmp/bin/leshy" \
    "$ROOT/macos/kikimora" "$@" >/dev/null
}

run_cli domains add example.com primary
grep -Fqx example.com "$tmp/config/domains/primary.txt"
run_cli routes add 10.20.0.0/16 secondary
grep -Fqx 10.20.0.0/16 "$tmp/config/routes/secondary.txt"
run_cli domains default secondary
grep -Fqx DEFAULT_ZONE=secondary "$tmp/config/routing.conf"
grep -Fqx 'name = "default-secondary"' "$tmp/config/config.toml"
run_cli domains remove example.com primary
[[ ! -s $tmp/config/domains/primary.txt ]]
printf 'macOS CLI list tests: OK\n'
