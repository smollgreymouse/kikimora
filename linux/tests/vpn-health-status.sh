#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT
readonly RECONCILE="${ROOT}/files/reconcile"
readonly COMMON="${ROOT}/files/kikimora-cli/common.sh"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p \
  "$tmp/bin" \
  "$tmp/state" \
  "$tmp/runtime" \
  "$tmp/endpoint" \
  "$tmp/sys/class/net/p0" \
  "$tmp/sys/class/net/s0"

cat >"$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail

iface=''
for ((i=1; i<=$#; i++)); do
  if [[ "${!i}" == dev ]]; then
    next=$((i + 1))
    iface="${!next:-}"
    break
  fi
done
[[ -n "$iface" ]] || { printf 'mock ip: no dev in %s\n' "$*" >&2; exit 64; }
[[ ! -e "${MOCK_STATE_DIR}/down-${iface}" ]] || exit 1

index_file="${MOCK_STATE_DIR}/ifindex-${iface}"
if [[ -r "$index_file" ]]; then
  index="$(<"$index_file")"
else
  index=7
fi

case "$*" in
  "-o link show dev "*|"link show dev "*)
    printf '%s: %s: <POINTOPOINT,UP,LOWER_UP> mtu 1500 state UNKNOWN\n' "$index" "$iface"
    ;;
  "-4 -o address show dev "*|"-o addr show dev "*" scope global")
    [[ ! -e "${MOCK_STATE_DIR}/no-address-${iface}" ]] || exit 0
    printf '%s: %s inet 10.0.0.2/24 scope global %s\n' "$index" "$iface" "$iface"
    ;;
  *)
    printf 'mock ip: unexpected invocation: %s\n' "$*" >&2
    exit 64
    ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

cat >"$tmp/bin/date" <<'EOF_DATE'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "$*" == '+%s' && -n ${KIKIMORA_NOW_EPOCH:-} ]]; then
  printf '%s\n' "$KIKIMORA_NOW_EPOCH"
else
  exec /bin/date "$@"
fi
EOF_DATE
chmod +x "$tmp/bin/date"

printf '10\n' >"$tmp/state/ifindex-p0"
printf '20\n' >"$tmp/state/ifindex-s0"

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
export KIKIMORA_SYS_NET_ROOT="$tmp/sys/class/net"
export KIKIMORA_VPN_FLAP_WINDOW_SECONDS=120
export KIKIMORA_VPN_FLAP_THRESHOLD=2
export KIKIMORA_NOW_EPOCH=900

SELF_DIR="$ROOT"
# shellcheck disable=SC1090
source "$COMMON"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_state() {
  local expected="$1" role="$2" iface="$3" actual
  actual="$(vpn_role_state "$role" "$iface")"
  [[ "$actual" == "$expected" ]] ||
    fail "expected $role/$iface state '$expected', got '$actual'"
}

assert_file_value() {
  local expected="$1" file="$2"
  [[ -r "$file" ]] || fail "expected readable file: $file"
  [[ "$(<"$file")" == "$expected" ]] ||
    fail "expected $file='$expected', got '$(<"$file")'"
}

assert_missing() {
  [[ ! -e "$1" ]] || fail "expected no file: $1"
}

run_reconcile() {
  "$RECONCILE" >/dev/null
}

# UP + address alone is not ready. The role remains validating until reconcile
# publishes the exact interface into primary.dev/secondary.dev after 3/3.
run_reconcile
assert_state validating primary p0
assert_state validating secondary s0
assert_missing "$tmp/runtime/primary.dev"

run_reconcile
run_reconcile
assert_file_value p0 "$tmp/runtime/primary.dev"
assert_file_value s0 "$tmp/runtime/secondary.dev"
assert_state ready primary p0
assert_state ready secondary s0

# Recreate p0 without an observable DOWN interval. Only ifindex changes. This is
# the race that a name-only readiness check misses.
export KIKIMORA_NOW_EPOCH=1000
printf '11\n' >"$tmp/state/ifindex-p0"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_state validating primary p0
assert_file_value 1000 "$tmp/runtime/readiness/primary.recreates"

# It may become published again after a fresh streak, but one recreation alone
# does not classify the role as flapping.
run_reconcile
run_reconcile
assert_file_value p0 "$tmp/runtime/primary.dev"
assert_state ready primary p0

# A second same-name recreation inside the window is flapping. Flapping remains
# visible even after the interface earns 3/3 again, so a brief healthy instant
# cannot make kk status lie during a reconnect loop.
export KIKIMORA_NOW_EPOCH=1010
printf '12\n' >"$tmp/state/ifindex-p0"
run_reconcile
assert_missing "$tmp/runtime/primary.dev"
assert_state flapping primary p0
[[ "$(wc -l < "$tmp/runtime/readiness/primary.recreates")" -eq 2 ]] ||
  fail 'expected two primary recreation events'

run_reconcile
run_reconcile
assert_file_value p0 "$tmp/runtime/primary.dev"
assert_state flapping primary p0
assert_state ready secondary s0

# Flapping ages out automatically; no privileged cleanup from kk status is
# required.
export KIKIMORA_NOW_EPOCH=1200
assert_state ready primary p0

# A stale/mismatched role publication is explicitly degraded.
printf 'old0\n' >"$tmp/runtime/primary.dev"
assert_state degraded primary p0

# A structurally healthy interface without a publication is validating.
rm -f "$tmp/runtime/primary.dev"
assert_state validating primary p0

# Endpoint-underlay pending has precedence over readiness publication.
touch "$tmp/endpoint/primary.pending"
assert_state underlay-pending primary p0
rm -f "$tmp/endpoint/primary.pending"

# Current structural loss is still reported when there is no recent flap loop.
touch "$tmp/state/down-p0"
assert_state down primary p0
rm -f "$tmp/state/down-p0"

rm -rf "$tmp/sys/class/net/p0"
assert_state missing primary p0

printf 'VPN health/status regression tests: OK\n'
