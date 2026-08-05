#!/bin/bash
set -Eeu -o pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d /tmp/kikimora-macos-dns-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/config" "$tmp/state" "$tmp/runtime"
printf 'PRIMARY_INTERFACE="utun4"\nSECONDARY_INTERFACE="utun5"\n' > "$tmp/config/vpn.conf"
printf 'DNS_SERVICES=("Wi-Fi")\n' > "$tmp/config/macos.conf"
printf '9.9.9.9\n' > "$tmp/dns-current"

cat > "$tmp/bin/networksetup" <<'EOF'
#!/bin/bash
case "$1" in
  -getdnsservers) cat "$MOCK_DNS_CURRENT" ;;
  -setdnsservers)
    shift 2
    if [[ ${1:-} == Empty ]]; then printf "There aren't any DNS Servers set on Wi-Fi.\n" > "$MOCK_DNS_CURRENT"
    else printf '%s\n' "$@" > "$MOCK_DNS_CURRENT"
    fi ;;
  *) exit 64 ;;
esac
EOF
cat > "$tmp/bin/dig" <<'EOF'
#!/bin/bash
printf ';; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n'
EOF
cat > "$tmp/bin/noop" <<'EOF'
#!/bin/bash
exit 0
EOF
chmod +x "$tmp/bin/networksetup" "$tmp/bin/dig" "$tmp/bin/noop"

run_dns() {
  PATH="$tmp/bin:$PATH" MOCK_DNS_CURRENT="$tmp/dns-current" \
  KIKIMORA_SKIP_PLATFORM_CHECK=1 KIKIMORA_SKIP_ROOT_CHECK=1 \
  KIKIMORA_CONFIG_DIR="$tmp/config" KIKIMORA_STATE_DIR="$tmp/state" \
  KIKIMORA_RUNTIME_ROOT="$tmp/runtime" DIG_BIN="$tmp/bin/dig" \
  DSCACHEUTIL_BIN="$tmp/bin/noop" KILLALL_BIN="$tmp/bin/noop" \
    "$ROOT/macos/leshy-dns" "$1" >/dev/null
}

run_dns enable
grep -Fqx 127.0.0.1 "$tmp/dns-current"
[[ -e $tmp/state/dns-enabled && -e $tmp/runtime/dns/enabled ]]
run_dns check
run_dns suspend
grep -Fqx 9.9.9.9 "$tmp/dns-current"
[[ -e $tmp/state/dns-enabled && ! -e $tmp/runtime/dns/enabled ]]
run_dns resume
grep -Fqx 127.0.0.1 "$tmp/dns-current"
run_dns disable
grep -Fqx 9.9.9.9 "$tmp/dns-current"
[[ ! -e $tmp/state/dns-enabled ]]
printf 'macOS DNS lifecycle tests: OK\n'
