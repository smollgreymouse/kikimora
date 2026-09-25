#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Thin wrapper around the canonical Linux staging builder.
# Retained for backward compatibility.
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
exec "$ROOT/packaging/linux/build-release.sh" "$@"
