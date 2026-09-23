#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Thin wrapper around the canonical Linux staging builder.
# Retained for backward compatibility.
set -Eeuo pipefail

DESKTOP_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd -- "$DESKTOP_ROOT/.." && pwd)"
exec "$REPO_ROOT/packaging/linux/build-release.sh" "$@"
