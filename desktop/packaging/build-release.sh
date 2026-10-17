#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Thin wrapper around the canonical Linux staging builder.
# Retained for backward compatibility.
set -Eeuo pipefail

DESKTOP_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd -- "$DESKTOP_ROOT/.." && pwd)"
# CI consumes desktop/dist/*; keep the canonical builder's default ROOT/dist
# for local builds and redirect only this wrapper's output.
export KIKIMORA_OUT_DIR="${KIKIMORA_OUT_DIR:-$DESKTOP_ROOT/dist}"
exec "$REPO_ROOT/packaging/linux/build-release.sh" "$@"
