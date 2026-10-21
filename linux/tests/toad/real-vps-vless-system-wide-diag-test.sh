#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_LINK_FILE="$ROOT/real-vps-vless-link.secret"

if (( $# > 1 )); then
    printf 'Usage: %s [vless://...|vpn://...]\n' "$0" >&2
    exit 2
fi

if (( $# == 1 )); then
    LINK="$1"
elif [[ -f "$LOCAL_LINK_FILE" ]]; then
    LINK="$(sed -n '1p' "$LOCAL_LINK_FILE")"
else
    printf 'Usage: %s [vless://...|vpn://...]\n' "$0" >&2
    printf 'Or put the Xray VLESS + REALITY link in %s (mode 0600).\n' "$LOCAL_LINK_FILE" >&2
    exit 2
fi

[[ -n "$LINK" ]] || { printf 'Share link is empty.\n' >&2; exit 2; }
if [[ ! "$LINK" =~ ^(vless|vpn):// ]]; then
    printf 'Expected a vless:// link or an Amnezia vpn:// Xray export.\n' >&2
    exit 2
fi
set --

# shellcheck source=linux/tests/toad/lib/system-wide-diag.sh
source "$ROOT/lib/system-wide-diag.sh"
# shellcheck source=linux/tests/toad/lib/system-wide-policy.sh
source "$ROOT/lib/system-wide-policy.sh"

run_system_wide_toad_diag "vless-reality" "kk-xray0" "real-vless-system-wide" "$LINK"
