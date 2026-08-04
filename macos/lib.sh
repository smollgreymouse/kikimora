#!/bin/bash
set -eu

readonly KIKIMORA_CONFIG_DIR="${KIKIMORA_CONFIG_DIR:-/usr/local/etc/kikimora/leshy}"
readonly VPN_CONFIG="${KIKIMORA_CONFIG_DIR}/vpn.conf"
readonly MACOS_CONFIG="${KIKIMORA_CONFIG_DIR}/macos.conf"
readonly RUNTIME_DIR="${KIKIMORA_RUNTIME_DIR:-/var/run/kikimora/leshy/vpn}"
readonly STATE_DIR="${KIKIMORA_STATE_DIR:-/var/db/kikimora/leshy}"
readonly LESHY_BIN="${LESHY_BIN:-/usr/local/bin/leshy}"
readonly LESHY_CONFIG="${LESHY_CONFIG:-${KIKIMORA_CONFIG_DIR}/config.toml}"

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
log() { logger -t kikimora -- "$*" 2>/dev/null || printf 'kikimora: %s\n' "$*" >&2; }
require_root() { [[ $(id -u) -eq 0 ]] || die 'run via sudo'; }
require_macos() { [[ $(uname -s) == Darwin ]] || die 'this backend requires macOS'; }

load_config() {
  [[ -r $VPN_CONFIG ]] || die "missing $VPN_CONFIG"
  [[ -r $MACOS_CONFIG ]] || die "missing $MACOS_CONFIG"
  # shellcheck source=/dev/null
  source "$VPN_CONFIG"
  # shellcheck source=/dev/null
  source "$MACOS_CONFIG"
  : "${PRIMARY_INTERFACE:?PRIMARY_INTERFACE is required}"
  : "${SECONDARY_INTERFACE:?SECONDARY_INTERFACE is required}"
  : "${DNS_SERVICES:?DNS_SERVICES is required}"
}

interface_ready() {
  local iface="$1"
  ifconfig "$iface" >/dev/null 2>&1 && ifconfig "$iface" | awk '$1 == "inet" { found=1 } END { exit !found }'
}

launchctl_loaded() { launchctl print "system/$1" >/dev/null 2>&1; }
