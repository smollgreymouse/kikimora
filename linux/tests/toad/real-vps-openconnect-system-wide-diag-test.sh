#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${1:-$ROOT/real-vps-openconnect-profile.secret}"
ARCHIVE="${TOAD_DIAG_OUTPUT:-$PWD/toad-system-wide-openconnect-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"

# shellcheck source=linux/tests/toad/lib/openconnect-real.sh
source "$ROOT/lib/openconnect-real.sh"

RESULT=FAIL
cleanup() {
    local status=$?
    set +e
    if [[ -n "${OC_DIAG:-}" && -d "${OC_DIAG:-}" ]]; then oc_snapshot before-rollback || true; fi
    oc_cleanup_runtime || true
    if [[ -n "${OC_DIAG:-}" && -d "${OC_DIAG:-}" ]]; then oc_snapshot after || true; fi
    if [[ -n "${OC_WORK:-}" && -d "${OC_WORK:-}" ]]; then
        if (( status == 0 )); then RESULT=PASS; fi
        oc_archive "$RESULT" "$ARCHIVE" || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

oc_prepare "$PROFILE"
sudo -v
oc_log "START system-wide real OpenConnect test; legacy Kikimora must already be disabled and will not be modified"
oc_snapshot before

# Refuse to cut over if the old VPN still owns the default route.
default_dev="$(ip -4 route show default | awk 'NR==1{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}')"
case "$default_dev" in vpn0|amn0|kk-*|tun*|wg*) oc_fail "current default route still uses VPN-like interface $default_dev; stop old Kikimora/VPN first";; esac

oc_pin_endpoint
oc_start_toad
oc_configure_split_dns
oc_resolve_targets

sudo ip -4 route replace default dev "$OC_IFACE" metric 5
OC_DEFAULT_ROUTE=1
ip -4 route get "$OC_GOOGLE_IP" | grep -Fq "dev $OC_IFACE" || oc_fail "Google is not routed through $OC_IFACE"
ip -4 route get "$OC_INTERNAL_IP" | grep -Fq "dev $OC_IFACE" || oc_fail "internal GitLab is not routed through $OC_IFACE"
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

printf '\nSYSTEM-WIDE OPENCONNECT TOAD IS ACTIVE.\n'
printf 'Google and %s have passed automatic probes.\n' "$OC_INTERNAL_HOST"
printf 'ChatGPT is intentionally NOT probed because it is expected to be unavailable on this VPN.\n'
printf 'Use the browser now. Press Enter to finish early; automatic rollback occurs after %s seconds.\n\n' "$OC_HOLD_SECONDS"
if [[ -t 0 ]]; then
    if ! read -r -t "$OC_HOLD_SECONDS" _; then
        oc_warn "manual browser window reached ${OC_HOLD_SECONDS}s; rolling back automatically"
    fi
else
    sleep "$OC_HOLD_SECONDS"
fi

oc_snapshot active-final
sudo cat "$OC_STATE/state.json" >"$OC_DIAG/state-active-final.json" 2>/dev/null || true
oc_log "PASS: OpenConnect system-wide route, Google, internal GitLab, endpoint underlay pin and automatic rollback validated"
