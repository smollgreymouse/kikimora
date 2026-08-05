#!/bin/bash
set -Eeu -o pipefail

readonly KIKIMORA_CONFIG_DIR="${KIKIMORA_CONFIG_DIR:-/usr/local/etc/kikimora/leshy}"
readonly CONFIG_DIR="$KIKIMORA_CONFIG_DIR"
readonly VPN_CONFIG="${KIKIMORA_VPN_CONFIG:-${KIKIMORA_CONFIG_DIR}/vpn.conf}"
readonly MACOS_CONFIG="${KIKIMORA_MACOS_CONFIG:-${KIKIMORA_CONFIG_DIR}/macos.conf}"
readonly DOMAINS_DIR="${KIKIMORA_DOMAINS_DIR:-${KIKIMORA_CONFIG_DIR}/domains}"
readonly ROUTES_DIR="${KIKIMORA_ROUTES_DIR:-${KIKIMORA_CONFIG_DIR}/routes}"
readonly ROUTING_CONFIG="${KIKIMORA_ROUTING_CONFIG:-${KIKIMORA_CONFIG_DIR}/routing.conf}"
readonly RUNTIME_ROOT="${KIKIMORA_RUNTIME_ROOT:-/var/run/kikimora/leshy}"
readonly RUNTIME_DIR="${KIKIMORA_RUNTIME_DIR:-${RUNTIME_ROOT}/vpn}"
readonly STATE_DIR="${KIKIMORA_STATE_DIR:-/var/db/kikimora/leshy}"
readonly LIBEXEC_DIR="${KIKIMORA_LIBEXEC_DIR:-/usr/local/libexec/kikimora/macos}"
readonly LESHY_BIN="${LESHY_BIN:-/usr/local/bin/leshy}"
readonly LESHY_CONFIG="${LESHY_CONFIG:-${KIKIMORA_CONFIG_DIR}/config.toml}"
readonly LESHY_DNS_HOST="${LESHY_DNS_HOST:-127.0.0.1}"
readonly LESHY_DNS_PORT="${LESHY_DNS_PORT:-53}"
readonly LABEL_PREFIX='com.kikimora'

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
log() { printf 'kikimora: %s\n' "$*" >&2; logger -t kikimora -- "$*" 2>/dev/null || true; }
require_root() {
  [[ ${KIKIMORA_SKIP_ROOT_CHECK:-0} == 1 || $(id -u) -eq 0 ]] || die 'run via sudo'
}
require_macos() {
  [[ ${KIKIMORA_SKIP_PLATFORM_CHECK:-0} == 1 || $(uname -s) == Darwin ]] ||
    die 'this backend requires macOS'
}

load_config() {
  [[ -r $VPN_CONFIG ]] || die "missing $VPN_CONFIG"
  [[ -r $MACOS_CONFIG ]] || die "missing $MACOS_CONFIG"
  # shellcheck source=/dev/null
  source "$VPN_CONFIG"
  # shellcheck source=/dev/null
  source "$MACOS_CONFIG"
  : "${PRIMARY_INTERFACE:=}"
  : "${SECONDARY_INTERFACE:=}"
  : "${PRIMARY_VPN_SERVICE:=}"
  : "${SECONDARY_VPN_SERVICE:=}"
  [[ -n $PRIMARY_INTERFACE || -n $PRIMARY_VPN_SERVICE ]] || die 'primary interface or VPN service is required'
  [[ -n $SECONDARY_INTERFACE || -n $SECONDARY_VPN_SERVICE ]] || die 'secondary interface or VPN service is required'
  : "${DNS_SERVICES:?DNS_SERVICES is required}"
  : "${VPN_LINK_READY_SUCCESSES:=3}"
}

resolve_vpn_service_interface() {
  local service="$1" line uuid='' output
  [[ -n $service ]] || return 1
  while IFS= read -r line; do
    if [[ $line == *\"${service}\"* && $line =~ ([0-9A-Fa-f-]{36}) ]]; then
      uuid="${BASH_REMATCH[1]}"
      break
    fi
  done < <("${SCUTIL_BIN:-/usr/sbin/scutil}" --nc list 2>/dev/null)
  [[ -n $uuid ]] || return 1
  output="$(printf 'show State:/Network/Service/%s/IPv4\n' "$uuid" | "${SCUTIL_BIN:-/usr/sbin/scutil}" 2>/dev/null)"
  printf '%s\n' "$output" | awk '$1 == "InterfaceName" && $2 == ":" { print $3; exit }'
}

resolve_role_interface() {
  local role="$1" configured service resolved=''
  case "$role" in
    primary) configured="$PRIMARY_INTERFACE"; service="$PRIMARY_VPN_SERVICE" ;;
    secondary) configured="$SECONDARY_INTERFACE"; service="$SECONDARY_VPN_SERVICE" ;;
    *) return 64 ;;
  esac
  if [[ -n $service ]]; then
    resolved="$(resolve_vpn_service_interface "$service" || true)"
    [[ -n $resolved ]] || return 1
    printf '%s\n' "$resolved"
  else
    [[ -n $configured ]] || return 1
    printf '%s\n' "$configured"
  fi
}

interface_ready() {
  local iface="$1" output
  output="$("${IFCONFIG_BIN:-/sbin/ifconfig}" "$iface" 2>/dev/null)" || return 1
  printf '%s\n' "$output" | awk '
    NR == 1 && /<[^>]*UP[^>]*>/ { up=1 }
    $1 == "inet" && $2 ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ { ipv4=1 }
    END { exit !(up && ipv4) }
  '
}

launchctl_loaded() { /bin/launchctl print "system/$1" >/dev/null 2>&1; }

flush_dns_caches() {
  "${DSCACHEUTIL_BIN:-/usr/bin/dscacheutil}" -flushcache >/dev/null 2>&1 || true
  "${KILLALL_BIN:-/usr/bin/killall}" -HUP mDNSResponder >/dev/null 2>&1 || true
}

check_leshy_dns() {
  local output
  output="$("${DIG_BIN:-/usr/bin/dig}" "@${LESHY_DNS_HOST}" -p "$LESHY_DNS_PORT" . NS \
    +time="${LESHY_QUERY_TIMEOUT:-12}" +tries=1 +noall +comments 2>&1)" || return 1
  printf '%s\n' "$output" | grep -Fq 'status: NOERROR'
}
