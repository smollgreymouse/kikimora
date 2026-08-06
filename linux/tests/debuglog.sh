#!/usr/bin/env bash
set -Eeuo pipefail

readonly MAINTENANCE="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/kikimora-cli/maintenance.sh}"
readonly VERSION='test'
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p \
  "$tmp/bin" \
  "$tmp/default-output" \
  "$tmp/runtime/vpn/readiness" \
  "$tmp/runtime/route-watch" \
  "$tmp/runtime/route-lifecycle" \
  "$tmp/runtime/dns" \
  "$tmp/state"

printf '3\n' >"$tmp/runtime/vpn/readiness/secondary.stable"
printf 'vpn0\n' >"$tmp/runtime/route-watch/active.devices"
printf 'vpn0\n' >"$tmp/runtime/route-lifecycle/devices"
printf 'enabled\n' >"$tmp/runtime/dns/enabled"

cat >"$tmp/bin/mock-command" <<'EOF_MOCK'
#!/usr/bin/env bash
set -Eeuo pipefail

name="${0##*/}"
case "$name" in
  date)
    case "${1:-}" in
      +%Y%m%d-%H%M%S) printf '20260805-120000\n' ;;
      -Is) printf '2026-08-05T12:00:00+03:00\n' ;;
      *) printf '2026-08-05\n' ;;
    esac
    ;;
  journalctl)
    printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/journal.args"
    printf 'mock journal\n'
    ;;
  *)
    printf 'mock %s: %s\n' "$name" "$*"
    ;;
esac
EOF_MOCK
chmod +x "$tmp/bin/mock-command"

for command in date dig hostnamectl ip journalctl resolvectl systemctl timedatectl uname; do
  ln -s mock-command "$tmp/bin/$command"
done

export PATH="$tmp/bin:$PATH"
export MOCK_STATE_DIR="$tmp/state"
export KIKIMORA_DEBUGLOG_RUNTIME_DIR="$tmp/runtime"

# shellcheck source=../files/kikimora-cli/maintenance.sh
source "$MAINTENANCE"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local expected="$1"
  local file="$2"
  grep -Fq -- "$expected" "$file" || fail "expected '$expected' in $file"
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 64
}

require_root() {
  touch "$tmp/state/root-called"
  return 1
}

cmd_debuglog --help >"$tmp/help"
[[ ! -e "$tmp/state/root-called" ]] || fail 'debuglog help required root'
assert_contains 'Usage: sudo kikimora debuglog' "$tmp/help"

require_root() {
  :
}

cmd_verify() {
  printf 'mock verification\n'
}

output="$tmp/custom-debug.log"
cmd_debuglog --since '30 minutes ago' --lines 25 --output "$output" >"$tmp/stdout"

[[ -f "$output" ]] || fail 'custom debug log was not created'
[[ "$(stat -c '%a' "$output")" == 600 ]] || fail 'debug log mode is not 0600'
assert_contains 'Debug log written:' "$tmp/stdout"
assert_contains '== Kikimora runtime state ==' "$output"
assert_contains "$tmp/runtime/vpn/readiness/secondary.stable" "$output"
assert_contains "$tmp/runtime/route-watch/active.devices" "$output"
assert_contains "$tmp/runtime/route-lifecycle/devices" "$output"
assert_contains "$tmp/runtime/dns/enabled" "$output"
assert_contains '-n 25 --since 30 minutes ago' "$tmp/state/journal.args"

if (cmd_debuglog --lines invalid --output "$tmp/invalid.log") >/dev/null 2>&1; then
  fail 'debuglog accepted an invalid line count'
fi

(
  cd "$tmp/default-output"
  cmd_debuglog >/dev/null
)

default_output="$tmp/default-output/kikimora-debug-20260805-120000.log"
[[ -f "$default_output" ]] || fail 'default debug log was not created'
[[ "$(stat -c '%a' "$default_output")" == 600 ]] || fail 'default debug log mode is not 0600'
assert_contains '-b -u leshy.service' "$tmp/state/journal.args"

printf 'Debug log command tests: OK\n'
