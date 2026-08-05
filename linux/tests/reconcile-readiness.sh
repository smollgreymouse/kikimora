#!/usr/bin/env bash
set -Eeuo pipefail

readonly RECONCILE="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/reconcile}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin" "$tmp/state" "$tmp/runtime"

cat >"$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail

iface="${*: -1}"
[[ ! -e "${MOCK_STATE_DIR}/down-${iface}" ]] || exit 1

case "$*" in
  "-o link show dev "*)
    printf '7: %s: <POINTOPOINT,UP,LOWER_UP> mtu 1500 state UNKNOWN\n' "$iface"
    ;;
  "-4 -o address show dev "*)
    printf '7: %s inet 10.0.0.2/24 scope global %s\n' "$iface" "$iface"
    ;;
  *)
    printf 'unexpected ip invocation: %s\n' "$*" >&2
    exit 64
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

cat >"$tmp/bin/curl" <<'EOF_CURL'
#!/usr/bin/env bash
set -Eeuo pipefail

iface=''
while (($#)); do
  case "$1" in
    --interface)
      iface="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

[[ -n "$iface" ]] || exit 64
[[ -e "${MOCK_STATE_DIR}/probe-${iface}" ]]
EOF_CURL
chmod +x "$tmp/bin/curl"

cat >"$tmp/vpn.conf" <<EOF_CONFIG
PRIMARY_INTERFACE="p0"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"
PRIMARY_PROBE_URL="http://1.1.1.1/"

SECONDARY_INTERFACE="s0"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
SECONDARY_PROBE_URL="http://1.1.1.1/"

VPN_PROBE_TIMEOUT=1
VPN_READY_SUCCESSES=2
VPN_DOWN_FAILURES=3
EOF_CONFIG

export PATH="$tmp/bin:$PATH"
export MOCK_STATE_DIR="$tmp/state"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_exists() {
  [[ -e "$1" ]] || fail "expected file: $1"
}

assert_missing() {
  [[ ! -e "$1" ]] || fail "expected no file: $1"
}

run_reconcile() {
  "$RECONCILE" >/dev/null
}

touch "$tmp/state/probe-p0" "$tmp/state/probe-s0"

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"

run_reconcile
assert_exists "$tmp/runtime/primary.dev"
assert_exists "$tmp/runtime/secondary.dev"

rm -f "$tmp/state/probe-s0"

run_reconcile
assert_exists "$tmp/runtime/secondary.dev"

run_reconcile
assert_exists "$tmp/runtime/secondary.dev"

run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
assert_exists "$tmp/runtime/primary.dev"

touch "$tmp/state/probe-s0"

run_reconcile
assert_missing "$tmp/runtime/secondary.dev"

run_reconcile
assert_exists "$tmp/runtime/secondary.dev"

touch "$tmp/state/down-p0"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"

printf 'VPN readiness reconciliation tests: OK\n'
