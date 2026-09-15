#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${1:-$ROOT/real-vps-openconnect-profile.secret}"
ARCHIVE="${TOAD_DIAG_OUTPUT:-$PWD/toad-real-vps-openconnect-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"

# shellcheck source=linux/tests/toad/lib/openconnect-real.sh
source "$ROOT/lib/openconnect-real.sh"

RESULT=FAIL
cleanup() {
    local status=$?
    set +e
    if [[ -n "${OC_DIAG:-}" && -d "${OC_DIAG:-}" ]]; then
        oc_snapshot after || true
    fi
    oc_cleanup_runtime || true
    if [[ -n "${OC_WORK:-}" && -d "${OC_WORK:-}" ]]; then
        if (( status == 0 )); then RESULT=PASS; fi
        oc_archive "$RESULT" "$ARCHIVE" || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

oc_prepare "$PROFILE"
sudo -v
oc_log "START real OpenConnect preflight; legacy Kikimora is not modified"
oc_snapshot before
oc_pin_endpoint
oc_start_toad
oc_configure_split_dns
oc_resolve_targets

sudo ip -4 route replace "$OC_GOOGLE_IP/32" dev "$OC_IFACE" metric 5
sudo ip -4 route replace "$OC_INTERNAL_IP/32" dev "$OC_IFACE" metric 5
OC_NARROW_ROUTES=1
ip -4 route get "$OC_GOOGLE_IP" | grep -Fq "dev $OC_IFACE" || oc_fail "Google route does not use $OC_IFACE"
ip -4 route get "$OC_INTERNAL_IP" | grep -Fq "dev $OC_IFACE" || oc_fail "internal GitLab route does not use $OC_IFACE"
ip -4 route get "$OC_ENDPOINT_IP" | grep -Fq "dev $OC_UNDERLAY_DEV" || oc_fail "OpenConnect gateway recursively entered $OC_IFACE"

oc_start_trace
oc_probe google "$OC_GOOGLE_URL" "$OC_GOOGLE_IP"
oc_probe internal-gitlab "$OC_INTERNAL_URL" "$OC_INTERNAL_IP"
oc_assert_probes
sleep 1

grep -Fq "$OC_GOOGLE_IP" "$OC_DIAG/tun-trace.txt" || oc_fail "Google packets were not observed on $OC_IFACE"
grep -Fq "$OC_INTERNAL_IP" "$OC_DIAG/tun-trace.txt" || oc_fail "internal GitLab packets were not observed on $OC_IFACE"
grep -Fq "$OC_ENDPOINT_IP" "$OC_DIAG/underlay-trace.txt" || oc_fail "OpenConnect transport was not observed on underlay"
if grep -Fq "$OC_GOOGLE_IP" "$OC_DIAG/underlay-trace.txt" || grep -Fq "$OC_INTERNAL_IP" "$OC_DIAG/underlay-trace.txt"; then
    oc_fail "target traffic leaked directly onto underlay $OC_UNDERLAY_DEV"
fi

oc_snapshot active
sudo cat "$OC_STATE/state.json" >"$OC_DIAG/state-active.json" 2>/dev/null || true
oc_log "PASS: real OpenConnect server, Google and internal GitLab work through $OC_IFACE"
