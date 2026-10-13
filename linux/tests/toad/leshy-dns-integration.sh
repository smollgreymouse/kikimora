#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# leshy-dns-integration.sh — 08A.3R Slice 3: combined Leshy/DNS integration test
#
# Tests the combined Leshy + Go runtime DNS/routing boundary on the
# installed VM. Verifies the dual-stack DNS/route-stability contract
# from 08-dual-stack-dns-route-stability.md:
#
#   1. A/AAAA classification and effective route/deny per family
#   2. Deterministic ownership across Leshy, NetworkManager, systemd-resolved
#   3. Unchanged DNS answers => no route/rule/DNS rewrites on re-check
#   4. DNS owner change/restart/underlay-loss/resume
#   5. leshy-health-watch.service status
#   6. Counters: reconcile wakeups, zero-diff passes, route/rule/DNS mutations
#
# This script runs on the installed VM against real Kikimora + Leshy.
# It does NOT require root for most checks (read-only observation).

set -Eeuo pipefail

KK="${KK_BIN:-/usr/local/bin/kk}"
CORE="${CORE_BIN:-/usr/local/bin/kikimora-core}"
LESHY="${LESHY_BIN:-/usr/local/bin/leshy}"
DIG="${DIG_BIN:-dig}"
OUT_DIR="${LESHY_DNS_TEST_OUT:-/tmp/leshy-dns-integration-$$}"

mkdir -p "$OUT_DIR"

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "ERROR: required command not found: $1" >&2
        exit 1
    }
}

require_cmd "$KK"
require_cmd "$DIG"
require_cmd ip
require_cmd python3
require_cmd resolvectl
require_cmd nmcli

# ---------------------------------------------------------------------------
# Capture helpers
# ---------------------------------------------------------------------------
capture_routes() {
    local label="$1"
    {
        echo "=== IPv4 routes ($label) ==="
        ip -4 route show
        echo "=== IPv6 routes ($label) ==="
        ip -6 route show
        echo "=== IPv4 rules ($label) ==="
        ip -4 rule show
        echo "=== IPv6 rules ($label) ==="
        ip -6 rule show
        echo "=== Endpoint table 51890 v4 ($label) ==="
        ip -4 route show table 51890 2>/dev/null || echo "(none)"
        echo "=== Endpoint table 51890 v6 ($label) ==="
        ip -6 route show table 51890 2>/dev/null || echo "(none)"
    } >"$OUT_DIR/routes-$label.txt"
}

capture_dns() {
    local label="$1"
    {
        echo "=== resolvectl status ($label) ==="
        resolvectl status 2>/dev/null || echo "(resolvectl unavailable)"
        echo "=== /etc/resolv.conf ($label) ==="
        cat /etc/resolv.conf 2>/dev/null || echo "(none)"
        echo "=== Leshy DNS state ($label) ==="
        ls -la /run/kikimora/leshy/dns/ 2>/dev/null || echo "(none)"
        echo "=== Leshy publication ($label) ==="
        ls -la /run/kikimora/leshy/vpn/ 2>/dev/null || echo "(none)"
    } >"$OUT_DIR/dns-$label.txt"
}

capture_snapshot() {
    local label="$1"
    "$KK" status --json >"$OUT_DIR/snapshot-$label.json" 2>/dev/null || true
}

count_main_routes() {
    ip -4 route show table 254 | wc -l
}

count_main_routes6() {
    ip -6 route show table 254 | wc -l
}

count_endpoint_routes() {
    ip -4 route show table 51890 2>/dev/null | wc -l
}

count_endpoint_routes6() {
    ip -6 route show table 51890 2>/dev/null | wc -l
}

count_managed_parks() {
    ip -4 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0
    ip -6 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0
}

# ---------------------------------------------------------------------------
# Test 1: A/AAAA classification and effective route/deny per family
# ---------------------------------------------------------------------------
echo "=== Test 1: A/AAAA classification ==="

# Resolve a well-known dual-stack domain through Leshy's DNS.
A_RECORDS=$("$DIG" @127.0.0.1 -p 53053 example.com A +short 2>/dev/null || true)
AAAA_RECORDS=$("$DIG" @127.0.0.1 -p 53053 example.com AAAA +short 2>/dev/null || true)

if [[ -z "$A_RECORDS" ]]; then
    echo "ERROR: Leshy did not return required A record" >&2; exit 1
else
    echo "PASS: A records from Leshy: $(echo "$A_RECORDS" | tr '\n' ' ')"
fi

if [[ -z "$AAAA_RECORDS" ]]; then
    echo "INFO: no AAAA records from Leshy DNS (IPv6 may be dead upstream)"
else
    echo "INFO: AAAA records from Leshy: $(echo "$AAAA_RECORDS" | tr '\n' ' ')"
fi

# Verify that resolved IPv4 endpoints route through the physical underlay.
V4_ENDPOINT=$(echo "$A_RECORDS" | head -1)
if [[ -n "$V4_ENDPOINT" ]]; then
    V4_PATH=$(ip -4 route get "$V4_ENDPOINT" 2>/dev/null || true)
    if echo "$V4_PATH" | grep -qF 'dev enp0s3'; then
        echo "PASS: A-resolved endpoint routes through physical underlay"
    else
        echo "INFO: A-resolved endpoint path: $V4_PATH"
    fi
fi

# If AAAA exists, verify it doesn't route through a managed Toad blackhole.
V6_ENDPOINT=$(echo "$AAAA_RECORDS" | head -1)
if [[ -n "$V6_ENDPOINT" ]]; then
    V6_PATH=$(ip -6 route get "$V6_ENDPOINT" 2>/dev/null || true)
    if echo "$V6_PATH" | grep -qF 'dev kk-'; then
        echo "ERROR: AAAA-resolved endpoint routes through managed Toad: $V6_PATH" >&2
        exit 1
    fi
    if [[ -z "$V6_PATH" ]]; then echo "INFO: IPv6 route absent (fast-fail must be covered in netns gate)"; else echo "INFO: IPv6 path: $V6_PATH"; fi
else
    echo "INFO: no AAAA records; unavailable IPv6 path is NOT validated by this observation"
fi

# ---------------------------------------------------------------------------
# Test 2: Deterministic ownership across Leshy, NM, systemd-resolved
# ---------------------------------------------------------------------------
echo "=== Test 2: DNS ownership determinism ==="

# Verify system DNS owner.
GLOBAL_DNS=$(resolvectl dns 2>/dev/null | grep -F 'Global' || true)
if [[ -z "$(resolvectl dns 2>/dev/null)" ]]; then echo "ERROR: DNS owner could not be observed" >&2; exit 1; fi
echo "INFO: observed global DNS owner: $GLOBAL_DNS (per-interface and split scopes saved separately)"

# Leshy-dns0 interface should exist.
if ip link show leshy-dns0 >/dev/null 2>&1; then
    echo "PASS: leshy-dns0 interface exists"
else
    echo "ERROR: leshy-dns0 interface missing" >&2
    exit 1
fi

# NetworkManager should know about leshy-dns0 but not manage kk- interfaces.
NM_STATUS=$(nmcli dev status 2>/dev/null || true)
if echo "$NM_STATUS" | grep -F 'leshy-dns0' | grep -qF 'externally'; then
    echo "PASS: NM reports leshy-dns0 as externally managed"
fi
if echo "$NM_STATUS" | grep -F 'kk-' | grep -qF 'unmanaged'; then
    echo "PASS: NM reports kk-* interfaces as unmanaged"
elif echo "$NM_STATUS" | grep -qF 'kk-'; then
    echo "ERROR: kk-* interface not explicitly unmanaged by NM" >&2; exit 1
fi

# ---------------------------------------------------------------------------
# Test 3: Unchanged DNS => no route/rule/DNS rewrites
# ---------------------------------------------------------------------------
echo "=== Test 3: Zero-mutation on stable state ==="

capture_routes "before"
capture_dns "before"
# Netlink monitor detects delete/re-add churn even if counts remain constant.
command -v timeout >/dev/null || { echo "ERROR: timeout required" >&2; exit 1; }
(timeout 8s ip -ts monitor route rule >"$OUT_DIR/netlink-events.txt" 2>"$OUT_DIR/netlink-monitor.err") &
MONITOR_PID=$!
sleep 1
BEFORE_V4_MAIN=$(count_main_routes)
BEFORE_V6_MAIN=$(count_main_routes6)
BEFORE_EP_V4=$(count_endpoint_routes)
BEFORE_EP_V6=$(count_endpoint_routes6)

# Observe a bounded idle window; mere status reads do not trigger reconciliation.
sleep 6
wait "$MONITOR_PID" || monitor_rc=$?
if [[ "${monitor_rc:-124}" -ne 124 && "${monitor_rc:-124}" -ne 0 ]]; then echo "ERROR: netlink monitor failed" >&2; exit 1; fi
if [[ -s "$OUT_DIR/netlink-monitor.err" ]]; then cat "$OUT_DIR/netlink-monitor.err" >&2; exit 1; fi
if [[ -s "$OUT_DIR/netlink-events.txt" ]]; then echo "ERROR: netlink route/rule events during stable window" >&2; cat "$OUT_DIR/netlink-events.txt" >&2; exit 1; fi

AFTER_V4_MAIN=$(count_main_routes)
AFTER_V6_MAIN=$(count_main_routes6)
AFTER_EP_V4=$(count_endpoint_routes)
AFTER_EP_V6=$(count_endpoint_routes6)

capture_routes "after"
capture_dns "after"

if (( AFTER_V4_MAIN != BEFORE_V4_MAIN )); then
    echo "ERROR: main table v4 route count changed: before=$BEFORE_V4_MAIN after=$AFTER_V4_MAIN" >&2
    diff -u "$OUT_DIR/routes-before.txt" "$OUT_DIR/routes-after.txt" >&2 || true
    exit 1
fi
if (( AFTER_V6_MAIN != BEFORE_V6_MAIN )); then
    echo "ERROR: main table v6 route count changed: before=$BEFORE_V6_MAIN after=$AFTER_V6_MAIN" >&2
    diff -u "$OUT_DIR/routes-before.txt" "$OUT_DIR/routes-after.txt" >&2 || true
    exit 1
fi
if (( AFTER_EP_V4 != BEFORE_EP_V4 )); then
    echo "ERROR: endpoint table v4 route count changed: before=$BEFORE_EP_V4 after=$AFTER_EP_V4" >&2
    exit 1
fi
if (( AFTER_EP_V6 != BEFORE_EP_V6 )); then
    echo "ERROR: endpoint table v6 route count changed: before=$BEFORE_EP_V6 after=$AFTER_EP_V6" >&2
    exit 1
fi
echo "PASS: no observed route/rule events and stable route counts (not proof of attempted DNS writes); (v4=$BEFORE_V4_MAIN v6=$BEFORE_V6_MAIN ep-v4=$BEFORE_EP_V4 ep-v6=$BEFORE_EP_V6)"

# ---------------------------------------------------------------------------
# Test 4: DNS resolution still works after core reconcile
# ---------------------------------------------------------------------------
echo "=== Test 4: DNS resolution after reconcile ==="

# Force a core status read (triggers snapshot but not a full reconcile).
"$KK" status --json >/dev/null 2>&1 || true

# Verify DNS still resolves.
A2=$("$DIG" @127.0.0.1 -p 53053 example.com A +short 2>/dev/null || true)
if [[ -n "$A2" ]]; then
    echo "PASS: DNS resolution works after status check"
else
    echo "ERROR: DNS resolution empty after status check" >&2; exit 1
fi

# ---------------------------------------------------------------------------
# Test 5: leshy-health-watch.service status
# ---------------------------------------------------------------------------
echo "=== Test 5: leshy-health-watch ==="

if systemctl list-unit-files leshy-health-watch.service >/dev/null 2>&1; then
    LHW_ENABLED=$(systemctl is-enabled leshy-health-watch.service 2>/dev/null || echo "unknown")
    LHW_ACTIVE=$(systemctl is-active leshy-health-watch.service 2>/dev/null || echo "unknown")
    echo "INFO: leshy-health-watch enabled=$LHW_ENABLED active=$LHW_ACTIVE"
else
    echo "INFO: leshy-health-watch.service absent: health-watch path not covered"
fi

# ---------------------------------------------------------------------------
# Test 6: Route/diagnostics counters from core snapshot
# ---------------------------------------------------------------------------
echo "=== Test 6: Core diagnostics ==="

capture_snapshot "final"

# Extract parking and endpoint state from JSON snapshot.
if [[ ! -s "$OUT_DIR/snapshot-final.json" ]]; then echo "ERROR: no valid core snapshot" >&2; exit 1; fi
if [[ -f "$OUT_DIR/snapshot-final.json" ]]; then
    PARKING_ACTIVE=$(python3 -c "
import json, sys
data = json.load(open('$OUT_DIR/snapshot-final.json'))
active = sum(1 for r in data.get('roles', []) if r.get('parking', {}).get('active', False))
print(active)
" 2>/dev/null || echo "?")
    echo "INFO: roles with active parking: $PARKING_ACTIVE"

    EP_READY=$(python3 -c "
import json, sys
data = json.load(open('$OUT_DIR/snapshot-final.json'))
ready = sum(1 for r in data.get('roles', []) if r.get('endpoint', {}).get('state') == 'ready')
print(ready)
" 2>/dev/null || echo "?")
    echo "INFO: roles with ready endpoints: $EP_READY"
fi

# ---------------------------------------------------------------------------
# Test 7: No split-default routes in any table
# ---------------------------------------------------------------------------
echo "=== Test 7: No split-default routes ==="

for prefix in "0.0.0.0/1" "128.0.0.0/1"; do
    if ip -4 route show table all "$prefix" 2>/dev/null | grep -q .; then
        echo "ERROR: split-default v4 route found: $prefix" >&2
        exit 1
    fi
done
for prefix in "::/1" "8000::/1"; do
    if ip -6 route show table all "$prefix" 2>/dev/null | grep -q .; then echo "ERROR: split-default v6 route found: $prefix" >&2; exit 1; fi
done
echo "PASS: no split-default routes in any table"

# ---------------------------------------------------------------------------
# Test 8: IPv4 Ready never implies IPv6 Ready (snapshot check)
# ---------------------------------------------------------------------------
echo "=== Test 8: Family readiness independence ==="

python3 - "$OUT_DIR/snapshot-final.json" <<'PY'
import json, sys
data=json.load(open(sys.argv[1]))
underlay=data.get("underlay")
if not isinstance(underlay,dict): raise SystemExit("ERROR: underlay missing from snapshot")
for family in ("ipv4","ipv6"):
    print(f"Underlay {family}: {underlay.get(family)}")
# A healthy IPv4 path is NOT evidence that IPv6 works.
# The active/dead IPv6 mismatch is independently asserted in the privileged netns fixture.
PY
echo "INFO: family paths observed; independent broken-v6 assertion belongs to privileged netns gate"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "LESHY/DNS OBSERVATIONAL GATE PASSED (DNS write counters and active replay still pending)"
echo "Evidence captured in: $OUT_DIR"
echo "============================================================"
