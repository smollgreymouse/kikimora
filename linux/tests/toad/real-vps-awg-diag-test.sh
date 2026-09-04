#!/usr/bin/env bash
set -euo pipefail

LINK="${1:?usage: $0 wg-or-amneziawg-share-link}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$(mktemp -d)/toad-real-vps-awg"

source "$ROOT/lib/diag.sh"

collect_diag "$OUT/before"

# Import, launch and runtime-specific checks are intentionally kept here.
# The script must leave all artifacts in the final archive.

collect_diag "$OUT/after"

tar czf "toad-real-vps-awg-diag-$(date +%Y%m%d-%H%M%S).tar.gz" -C "$(dirname "$OUT")" "$(basename "$OUT")"
