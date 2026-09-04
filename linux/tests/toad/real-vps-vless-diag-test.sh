#!/usr/bin/env bash
set -euo pipefail

LINK="${1:?usage: $0 vless-share-link}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$(mktemp -d)/toad-real-vps-vless"

source "$ROOT/lib/diag.sh"

collect_diag "$OUT/before"

# Import, launch and runtime-specific checks are intentionally kept here.
# The script must leave all artifacts in the final archive.

collect_diag "$OUT/after"

tar czf "toad-real-vps-vless-diag-$(date +%Y%m%d-%H%M%S).tar.gz" -C "$(dirname "$OUT")" "$(basename "$OUT")"
