#!/usr/bin/env bash
set -Eeuo pipefail

readonly RECONCILE="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/reconcile}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin" "$tmp/state" "$tmp/runtime" "$tmp/endpoint"

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
PRIMARY_ENDPOINT_PROVIDER="static"
PRIMARY_ENDPOINT_PROVIDER_ARGS=""

SECONDARY_INTERFACE="s0"
SECONDARY_DEVICE_FILE="$tmp/runtime/secondary.dev"
SECONDARY_ENDPOINT_PROVIDER="static"
SECONDARY_ENDPOINT_PROVIDER_ARGS=""

VPN_LINK_READY_SUCCESSES=3
EOF_CONFIG

export PATH="$tmp/bin:$PATH"
export MOCK_STATE_DIR="$tmp/state"
export KIKIMORA_VPN_CONFIG="$tmp/vpn.conf"
export KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime"
export KIKIMORA_ENDPOINT_STATE_DIR="$tmp/endpoint"

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

replace_setting() {
  local key="$1" value="$2"
  sed -i -E "s|^${key}=.*$|${key}=\"${value}\"|" "$tmp/vpn.conf"
}

run_reconcile() {
  "$RECONCILE" >/dev/null
}

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"
assert_missing "$tmp/endpoint/primary.pending"
assert_missing "$tmp/endpoint/secondary.pending"

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"

run_reconcile
assert_value p0 "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"
assert_value p0 "$tmp/runtime/readiness/primary.candidate"
assert_value s0 "$tmp/runtime/readiness/secondary.candidate"
assert_exists "$tmp/runtime/readiness/primary.selection"
assert_exists "$tmp/runtime/readiness/secondary.selection"

# PR #16 semantics: endpoint pending keeps an already-ready publication when it
# is still the SAME configured interface.
printf 'interface=p0 desired=old-policy-change\n' >"$tmp/endpoint/primary.pending"
run_reconcile
assert_value p0 "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"

# Profile switch: pending must never keep the publication from the old profile.
# p1 is structurally ready, but it starts from readiness zero and remains
# unpublished while endpoint-underlay is pending.
replace_setting PRIMARY_INTERFACE p1
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_value p1 "$tmp/runtime/readiness/primary.candidate"
assert_value s0 "$tmp/runtime/secondary.dev"
assert_missing "$tmp/runtime/readiness/primary.stable"

run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"

# Once endpoint reconcile clears/applies pending, the replacement interface must
# earn a complete fresh readiness streak.
rm -f "$tmp/endpoint/primary.pending"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_value 1 "$tmp/runtime/readiness/primary.stable"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_value 2 "$tmp/runtime/readiness/primary.stable"
run_reconcile
assert_value p1 "$tmp/runtime/primary.dev"
assert_value s0 "$tmp/runtime/secondary.dev"

# Change only secondary. Reconcile itself pre-arms profile-transition pending
# before readiness, closing the race where route-watch's slower endpoint pass
# could otherwise observe the new already-UP interface too late.
replace_setting SECONDARY_INTERFACE s1
run_reconcile
assert_value p1 "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"
assert_value s1 "$tmp/runtime/readiness/secondary.candidate"
assert_exists "$tmp/endpoint/secondary.pending"
assert_missing "$tmp/runtime/readiness/secondary.stable"

run_reconcile
assert_value p1 "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"
assert_exists "$tmp/endpoint/secondary.pending"

# Simulate endpoint core accepting/applying the new profile selection.
rm -f "$tmp/endpoint/secondary.pending"
run_reconcile
assert_value p1 "$tmp/runtime/primary.dev"
assert_missing "$tmp/runtime/secondary.dev"
assert_value 1 "$tmp/runtime/readiness/secondary.stable"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
assert_value 2 "$tmp/runtime/readiness/secondary.stable"
run_reconcile
assert_value p1 "$tmp/runtime/primary.dev"
assert_value s1 "$tmp/runtime/secondary.dev"

# Provider selection is part of the profile state too. Changing provider on the
# SAME already-published interface pre-arms pending, but PR #16 same-interface
# semantics keep the established publication usable until endpoint core decides
# whether the provider transition can be applied live.
replace_setting SECONDARY_ENDPOINT_PROVIDER happ
run_reconcile
assert_value s1 "$tmp/runtime/secondary.dev"
assert_exists "$tmp/endpoint/secondary.pending"
rm -f "$tmp/endpoint/secondary.pending"
run_reconcile
assert_value s1 "$tmp/runtime/secondary.dev"

# Structural loss is immediate and only affects that role.
touch "$tmp/state/down-s1"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
assert_exists "$tmp/runtime/primary.dev"

# Recovery must stabilize again before publication.
rm -f "$tmp/state/down-s1"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
run_reconcile
assert_missing "$tmp/runtime/secondary.dev"
run_reconcile
assert_value s1 "$tmp/runtime/secondary.dev"

# Losing the IPv4 address also withdraws immediately.
touch "$tmp/state/no-address-p1"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_exists "$tmp/runtime/secondary.dev"

printf 'VPN link readiness/profile/provider reconciliation tests: OK\n'
