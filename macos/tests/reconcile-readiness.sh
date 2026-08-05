#!/bin/bash
set -Eeu -o pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d /tmp/kikimora-macos-readiness-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/config" "$tmp/runtime" "$tmp/state"

cat > "$tmp/config/vpn.conf" <<EOF
PRIMARY_INTERFACE="utun4"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
SECONDARY_INTERFACE="utun5"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
VPN_LINK_READY_SUCCESSES=3
EOF
printf 'DNS_SERVICES=("Wi-Fi")\n' > "$tmp/config/macos.conf"
cat > "$tmp/ifconfig" <<'EOF'
#!/bin/bash
if [[ ${MOCK_READY:-0} == 1 ]]; then
  printf '%s: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1380\n' "$1"
  printf '\tinet 10.0.0.2 --> 10.0.0.2 netmask 0xffffffff\n'
  exit 0
fi
exit 1
EOF
chmod +x "$tmp/ifconfig"
cat > "$tmp/scutil" <<'EOF'
#!/bin/bash
if [[ ${1:-} == --nc && ${2:-} == list ]]; then
  printf '* (Connected) 11111111-1111-1111-1111-111111111111 PPP --> "Primary VPN" [VPN]\n'
  printf '* (Connected) 22222222-2222-2222-2222-222222222222 PPP --> "Secondary VPN" [VPN]\n'
  exit 0
fi
IFS= read -r command
case "$command" in
  *11111111-1111-1111-1111-111111111111*) printf '  InterfaceName : utun7\n' ;;
  *22222222-2222-2222-2222-222222222222*) printf '  InterfaceName : utun8\n' ;;
esac
EOF
chmod +x "$tmp/scutil"

run_reconcile() {
  KIKIMORA_SKIP_PLATFORM_CHECK=1 \
  KIKIMORA_CONFIG_DIR="$tmp/config" \
  KIKIMORA_RUNTIME_DIR="$tmp/runtime" \
  KIKIMORA_STATE_DIR="$tmp/state" \
  IFCONFIG_BIN="$tmp/ifconfig" SCUTIL_BIN="$tmp/scutil" MOCK_READY="$1" \
    "$ROOT/macos/reconcile" >/dev/null
}

run_reconcile 1
[[ ! -e $tmp/runtime/primary.dev && ! -e $tmp/runtime/secondary.dev ]]
run_reconcile 1
[[ ! -e $tmp/runtime/primary.dev && ! -e $tmp/runtime/secondary.dev ]]
run_reconcile 1
grep -Fqx utun4 "$tmp/runtime/primary.dev"
grep -Fqx utun5 "$tmp/runtime/secondary.dev"
run_reconcile 0
[[ ! -e $tmp/runtime/primary.dev && ! -e $tmp/runtime/secondary.dev ]]

rm -rf "$tmp/runtime/readiness"
cat > "$tmp/config/vpn.conf" <<EOF
PRIMARY_INTERFACE=""
PRIMARY_VPN_SERVICE="Primary VPN"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
SECONDARY_INTERFACE=""
SECONDARY_VPN_SERVICE="Secondary VPN"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
VPN_LINK_READY_SUCCESSES=3
EOF
run_reconcile 1
run_reconcile 1
run_reconcile 1
grep -Fqx utun7 "$tmp/runtime/primary.dev"
grep -Fqx utun8 "$tmp/runtime/secondary.dev"
printf 'macOS reconcile readiness tests: OK\n'
