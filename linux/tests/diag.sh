#!/usr/bin/env bash
set -Eeuo pipefail

readonly STATUS="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/kikimora-cli/status.sh}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p \
  "$tmp/bin" "$tmp/config/domains" "$tmp/runtime" "$tmp/state" \
  "$tmp/sys/class/net/tun0" "$tmp/sys/class/net/vpn0" \
  "$tmp/proc/111/fdinfo" "$tmp/proc/222/fdinfo"
printf 'PRIMARY_INTERFACE="tun0"\nSECONDARY_INTERFACE="vpn0"\n' >"$tmp/config/vpn.conf"
printf '# comment\n*.wild.example\nprimary.example\n' >"$tmp/config/domains/primary.txt"
printf 'secondary.example\n' >"$tmp/config/domains/secondary.txt"
printf 'tun0\n' >"$tmp/runtime/primary.dev"
printf 'vpn0\n' >"$tmp/runtime/secondary.dev"

printf '42\n' >"$tmp/sys/class/net/tun0/ifindex"
printf '42\n' >"$tmp/sys/class/net/tun0/iflink"
printf 'up\n' >"$tmp/sys/class/net/tun0/operstate"
printf '1\n' >"$tmp/sys/class/net/tun0/carrier"
printf '1400\n' >"$tmp/sys/class/net/tun0/mtu"
printf '43\n' >"$tmp/sys/class/net/vpn0/ifindex"
printf '43\n' >"$tmp/sys/class/net/vpn0/iflink"
printf 'up\n' >"$tmp/sys/class/net/vpn0/operstate"
printf '1\n' >"$tmp/sys/class/net/vpn0/carrier"
printf '1434\n' >"$tmp/sys/class/net/vpn0/mtu"

printf 'flags:\t0100002\niff:\ttun0\n' >"$tmp/proc/111/fdinfo/7"
printf 'primary-vpn\0--interface\0tun0\0' >"$tmp/proc/111/cmdline"
printf 'primary-vpn\n' >"$tmp/proc/111/comm"
printf 'flags:\t0100002\niff:\tvpn0\n' >"$tmp/proc/222/fdinfo/8"
printf 'secondary-vpn\0--interface\0vpn0\0' >"$tmp/proc/222/cmdline"
printf 'secondary-vpn\n' >"$tmp/proc/222/comm"

cat >"$tmp/bin/mock-command" <<'MOCK'
#!/usr/bin/env bash
set -Eeuo pipefail
name="${0##*/}"
case "$name" in
  date)
    case "${1:-}" in
      +%Y%m%d-%H%M%S) printf '20260831-150000\n' ;;
      -Is) printf '2026-08-31T15:00:00+03:00\n' ;;
      *) printf '2026-08-31\n' ;;
    esac
    ;;
  hostname) printf 'diag-test-host\n' ;;
  dig)
    case " $* " in
      *' primary.example '*) printf 'primary.example. 60 IN A 203.0.113.10\n' ;;
      *' override.secondary '*) printf 'override.secondary. 60 IN A 203.0.113.20\n' ;;
      *) printf 'secondary.example. 60 IN A 203.0.113.20\n' ;;
    esac
    ;;
  journalctl)
    printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/journal.args"
    if [[ " $* " == *' -k '* ]]; then
      printf 'MOCK_KERNEL_JOURNAL\n'
    elif [[ " $* " == *' -u '* ]]; then
      printf 'MOCK_MANAGED_JOURNAL\n'
    else
      printf 'MOCK_FULL_SYSTEM_JOURNAL\n'
    fi
    ;;
  curl)
    printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/curl.args"
    printf 'mock curl\n'
    ;;
  ip)
    printf '%s\n' "$*" >>"${MOCK_STATE_DIR}/ip.args"
    case " $* " in
      *' -ts monitor link address route '*) printf '[2026-08-31T15:00:01] Deleted 42: tun0: <POINTOPOINT,UP>\n' ;;
      *' route get 203.0.113.10 '*) printf '203.0.113.10 dev tun0 src 10.0.0.2\n' ;;
      *' route get 203.0.113.20 '*) printf '203.0.113.20 dev vpn0 src 10.1.0.2\n' ;;
      *' route show dev tun0 '*) printf '203.0.113.10 dev tun0 scope link\n' ;;
      *' route show dev vpn0 '*) printf '203.0.113.20 dev vpn0 scope link\n' ;;
      *' -o -details link show dev tun0 '*) printf '42: tun0: <POINTOPOINT,UP> mtu 1400\n' ;;
      *' -o -details link show dev vpn0 '*) printf '43: vpn0: <POINTOPOINT,UP> mtu 1434\n' ;;
      *) printf 'mock ip: %s\n' "$*" ;;
    esac
    ;;
  fuser) printf 'mock fuser: %s\n' "$*" ;;
  lsof) printf 'mock lsof: %s\n' "$*" ;;
  kk)
    printf 'mock kk: %s\n' "$*"
    ;;
  *) printf 'mock %s: %s\n' "$name" "$*" ;;
esac
MOCK
chmod +x "$tmp/bin/mock-command"
for command in date hostname uname hostnamectl timedatectl nmcli ip ss pgrep dig curl resolvectl journalctl kk fuser lsof; do
  ln -s mock-command "$tmp/bin/$command"
done
export PATH="$tmp/bin:$PATH"
export MOCK_STATE_DIR="$tmp/state"
export KIKIMORA_DIAG_CONFIG_DIR="$tmp/config"
export KIKIMORA_DIAG_RUNTIME_DIR="$tmp/runtime"
export KIKIMORA_DIAG_KK_BIN="$tmp/bin/kk"
export KIKIMORA_DIAG_OUTPUT="$tmp/diag.log"
export KIKIMORA_DIAG_SAMPLES=1
export KIKIMORA_DIAG_INTERVAL=0
export KIKIMORA_DIAG_SYS_NET_ROOT="$tmp/sys/class/net"
export KIKIMORA_DIAG_PROC_ROOT="$tmp/proc"

# shellcheck source=../files/kikimora-cli/status.sh
source "$STATUS"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_contains() { grep -Fq -- "$1" "$2" || fail "expected '$1' in $2"; }
die() { printf 'Error: %s\n' "$*" >&2; return 64; }
root_called=0
require_root() { root_called=1; }
load_interfaces() { PRIMARY_INTERFACE=tun0; SECONDARY_INTERFACE=vpn0; }

cmd_diag --help >"$tmp/help"
[[ "$root_called" == 0 ]] || fail 'diag help required root'
assert_contains 'both managed VPN roles' "$tmp/help"

cmd_diag override.secondary >"$tmp/stdout"
output="$tmp/diag.log"
[[ -f "$output" ]] || fail 'diagnostic log was not created'
[[ "$(stat -c '%a' "$output")" == 600 ]] || fail 'diagnostic log mode is not 0600'
assert_contains 'FILE:' "$tmp/stdout"
assert_contains 'KIKIMORA FULL NETWORK DIAGNOSTIC' "$output"
assert_contains 'Primary:   primary.example -> tun0 (initial ifindex=42)' "$output"
assert_contains 'Secondary: override.secondary -> vpn0 (initial ifindex=43)' "$output"
assert_contains '===== PRIMARY DNS PROBE: primary.example via tun0 =====' "$output"
assert_contains '===== SECONDARY DNS PROBE: override.secondary via vpn0 =====' "$output"
assert_contains 'iface=tun0 pid=111 fd=7 cmd=primary-vpn --interface tun0 ' "$output"
assert_contains 'iface=vpn0 pid=222 fd=8 cmd=secondary-vpn --interface vpn0 ' "$output"
assert_contains 'ifindex: initial=42 current=42' "$output"
assert_contains 'NETLINK EVENT TRACE DURING DIAGNOSTIC' "$output"
assert_contains 'Deleted 42: tun0' "$output"
assert_contains 'MOCK_MANAGED_JOURNAL' "$output"
assert_contains 'MOCK_FULL_SYSTEM_JOURNAL' "$output"
assert_contains 'MOCK_KERNEL_JOURNAL' "$output"
assert_contains '--interface tun0' "$tmp/state/curl.args"
assert_contains '--interface vpn0' "$tmp/state/curl.args"
assert_contains '--resolve primary.example:443:203.0.113.10' "$tmp/state/curl.args"
assert_contains '--resolve override.secondary:443:203.0.113.20' "$tmp/state/curl.args"
assert_contains '-ts monitor link address route' "$tmp/state/ip.args"
assert_contains '-b --since -30 min --no-pager -o short-iso' "$tmp/state/journal.args"

printf 'Diag command tests: OK\n'
