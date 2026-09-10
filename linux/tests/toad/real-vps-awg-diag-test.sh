#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_LINK_FILE="$ROOT/real-vps-awg-link.secret"
if (( $# > 0 )); then
    LINK="$1"
elif [[ -r "$LOCAL_LINK_FILE" ]]; then
    IFS= read -r LINK <"$LOCAL_LINK_FILE"
else
    printf 'usage: %s vpn-or-wg-or-amneziawg-share-link\n' "$0" >&2
    printf 'or store the link in %s with mode 0600\n' "$LOCAL_LINK_FILE" >&2
    exit 2
fi
[[ -n "$LINK" ]] || { printf 'share link is empty\n' >&2; exit 2; }
set --

source "$ROOT/lib/diag.sh"
run_real_vps_diag "amneziawg2" "kk-awg0" "real-awg" "$LINK"
