#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./vpn-bench-common.sh
source "${script_dir}/vpn-bench-common.sh"

collect_snapshot "wg2-clean"
