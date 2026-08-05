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
    [[ ! -e "${MOCK_STATE_DIR}/no-address-${iface}" ]] || exit 0
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
printf 'curl must not be used by link readiness\n' >&2
exit 99
EOF_CURL
chmod +x "$tmp/bin/curl"

cat >"$tmp/vpn.conf" <<EOF_CONFIG
PRIMARY_INTERFACE="p0"
PRIMARY_DEVICE_FILE="$tmp/runtime/primary.dev"

SECONDARY_INTERFACE="s0"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"

VPN_LINK_READY_SUCCESSES=3
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

assert_value() {
  local expected="$1"
  local file="$2"
  [[ -r "$file" ]] || fail "expected readable file: $file"
  [[ "$(<"$file")" == "$expected" ]] ||
    fail "expected $file to contain '$expected', got '$(<"$file")'"
}

run_reconcile() {
  "$RECONCILE" >/dev/null
}

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"

run_reconcile
assert_value p0 "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"

# A ready interface remains published without any active network probe.
run_reconcile
assert_value p0 "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"

# Structural loss is immediate.
touch "$tmp/state/down-s0"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
assert_exists "$tmp/runtime/primary.dev"

# Recovery must stabilize again before publication.
rm -f "$tmp/state/down-s0"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
run_reconcile
assert_value s0 "$tmp/runtime/secondary.dev"

# Losing the IPv4 address also withdraws immediately.
touch "$tmp/state/no-address-p0"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_exists "$tmp/runtime/secondary.dev"

printf 'VPN link readiness reconciliation tests: OK\n'
