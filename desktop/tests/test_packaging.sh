#!/usr/bin/env bash
# Compatibility entry point for the historical desktop CI job.
# Chapter 08 verifies the console/runtime package; UI packaging is chapter 09.
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
exec "$ROOT/linux/tests/test_release_packaging.sh" "$@"
