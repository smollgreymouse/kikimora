#!/usr/bin/env bash
# Shared version source of truth for all packaging.
# All package scripts must source this file.
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
KIKIMORA_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
export KIKIMORA_VERSION