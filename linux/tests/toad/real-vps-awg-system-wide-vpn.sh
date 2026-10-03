#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

exec env \
    TOAD_SYSTEM_WIDE_KIND=awg \
    TOAD_SYSTEM_WIDE_PROTOCOL=amneziawg2 \
    TOAD_SYSTEM_WIDE_INTERFACE=kk-awg0 \
    TOAD_SYSTEM_WIDE_LINK_FILE="$ROOT/real-vps-awg-link.secret" \
    TOAD_SYSTEM_WIDE_COMMAND="$0" \
    "$ROOT/real-vps-vless-system-wide-vpn.sh" "$@"
