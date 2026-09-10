#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

LINK="${1:?usage: $0 vless-share-link}"
set --
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

source "$ROOT/lib/diag.sh"
run_real_vps_diag "vless-reality" "kk-xray0" "real-vless" "$LINK"
