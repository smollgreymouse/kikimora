//go:build linux

package openconnect

import (
	"fmt"
	"os"
	"path/filepath"
)

const routeFreeVPNScript = `#!/bin/sh
set -eu

# Kikimora owns routing and DNS policy. This script is intentionally limited to
# configuring the OpenConnect-owned TUN as a stable route target and publishing
# the non-secret network parameters pushed by the server for the orchestrator.
case "${reason:-}" in
  connect|reconnect)
    ip link set dev "$TUNDEV" up

    if [ -n "${INTERNAL_IP4_ADDRESS:-}" ]; then
      prefix="${INTERNAL_IP4_NETMASKLEN:-32}"
      ip -4 addr flush dev "$TUNDEV" scope global || true
      ip -4 addr add "$INTERNAL_IP4_ADDRESS/$prefix" dev "$TUNDEV"
    fi

    if [ -n "${INTERNAL_IP6_ADDRESS:-}" ]; then
      ip -6 addr flush dev "$TUNDEV" scope global || true
      case "$INTERNAL_IP6_ADDRESS" in
        */*) ip -6 addr add "$INTERNAL_IP6_ADDRESS" dev "$TUNDEV" ;;
        *)   ip -6 addr add "$INTERNAL_IP6_ADDRESS/${INTERNAL_IP6_NETMASK:-128}" dev "$TUNDEV" ;;
      esac
    fi

    mtu="${INTERNAL_IP4_MTU:-${INTERNAL_IP6_MTU:-}}"
    if [ -n "$mtu" ]; then
      ip link set dev "$TUNDEV" mtu "$mtu"
    fi

    state_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
    network_state="$state_dir/openconnect-network.env"
    network_state_tmp="$network_state.tmp.$$"
    umask 077
    {
      printf 'reason=%s\n' "${reason:-}"
      printf 'tun_dev=%s\n' "${TUNDEV:-}"
      printf 'mtu=%s\n' "$mtu"
      printf 'ipv4_address=%s\n' "${INTERNAL_IP4_ADDRESS:-}"
      printf 'ipv4_netmasklen=%s\n' "${INTERNAL_IP4_NETMASKLEN:-}"
      printf 'ipv4_dns=%s\n' "${INTERNAL_IP4_DNS:-}"
      printf 'ipv6_address=%s\n' "${INTERNAL_IP6_ADDRESS:-}"
      printf 'ipv6_dns=%s\n' "${INTERNAL_IP6_DNS:-}"
      printf 'split_dns=%s\n' "${CISCO_SPLIT_DNS:-}"
      printf 'default_domain=%s\n' "${CISCO_DEF_DOMAIN:-}"
      printf 'banner=%s\n' "${CISCO_BANNER:-}"
    } > "$network_state_tmp"
    chmod 0600 "$network_state_tmp"
    mv -f -- "$network_state_tmp" "$network_state"
    ;;
  disconnect|pre-init|attempt-reconnect)
    :
    ;;
  *)
    :
    ;;
esac

exit 0
`

func writeRouteFreeVPNScript(stateDir string) (string, error) {
	path := filepath.Join(stateDir, ".openconnect-vpnc-script.sh")
	if err := os.WriteFile(path, []byte(routeFreeVPNScript), 0o700); err != nil {
		return "", fmt.Errorf("write route-free OpenConnect script: %w", err)
	}
	return path, nil
}
