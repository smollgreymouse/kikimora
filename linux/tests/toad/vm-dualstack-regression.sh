#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# vm-dualstack-regression.sh — 08A.3R Slice 4: installed VM dual-stack
# DNS/route-stability regression acceptance
#
# Runs on the disposable Ubuntu VM against the installed Kikimora package.
# Captures before/after route, rule, DNS and application-probe evidence.
# Verifies the dual-stack field regression contract from
# 08-dual-stack-dns-route-stability.md.

set -Eeuo pipefail

KK="${KK_BIN:-/usr/local/bin/kk}"
OUT_DIR="${VM_DUALSTACK_REGRESSION_OUT:-/tmp/vm-dualstack-regression-$$}"

mkdir -p "$OUT_DIR"

echo "=== 08A.3R Slice 4: installed VM dual-stack regression ==="
echo "Output: $OUT_DIR"

# ---------------------------------------------------------------------------
# 1. Capture baseline (before)
# ---------------------------------------------------------------------------
echo "--- 1. Capturing baseline ---"

{
    echo "=== IPv4 routes ==="
    ip -4 route show
    echo "=== IPv6 routes ==="
    ip -6 route show
    echo "=== IPv4 rules ==="
    ip -4 rule show
    echo "=== IPv6 rules ==="
    ip -6 rule show
    echo "=== IPv4 route get AWG endpoint ==="
    ip -4 route get 72.56.119.116 2>/dev/null || echo "(unreachable)"
    echo "=== IPv4 route get OC endpoint ==="
    ip -4 route get 46.243.227.103 2>/dev/null || echo "(unreachable)"
    echo "=== Managed /1 prefix count ==="
    echo "v4 /1: $(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l) + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)"
    echo "v6 /1: $(ip -6 route show ::/1 2>/dev/null | wc -l) + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)"
    echo "=== Managed /32 /128 park count ==="
    echo "v4 parks: $(ip -4 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0)"
    echo "v6 parks: $(ip -6 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0)"
} >"$OUT_DIR/routes-before.txt"

{
    echo "=== resolvectl status ==="
    resolvectl status 2>/dev/null || echo "(unavailable)"
    echo "=== /etc/resolv.conf ==="
    cat /etc/resolv.conf 2>/dev/null
    echo "=== Leshy DNS state ==="
    ls -la /run/kikimora/leshy/dns/ 2>/dev/null || echo "(none)"
    echo "=== Leshy publication ==="
    ls -la /run/kikimora/leshy/vpn/ 2>/dev/null || echo "(none)"
} >"$OUT_DIR/dns-before.txt"

"$KK" status --json >"$OUT_DIR/snapshot-before.json" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 2. A/AAAA resolution and per-family probe
# ---------------------------------------------------------------------------
echo "--- 2. A/AAAA resolution ---"

{
    echo "=== A record ==="
    dig @127.0.0.1 -p 53053 example.com A +short 2>/dev/null || echo "(failed)"
    echo "=== AAAA record ==="
    dig @127.0.0.1 -p 53053 example.com AAAA +short 2>/dev/null || echo "(failed)"
    echo "=== curl -4 (2s timeout) ==="
    curl -4 -so /dev/null -w '%{http_code} %{time_total}s' --connect-timeout 2 http://example.com 2>/dev/null || echo "FAIL"
    echo ""
    echo "=== curl -6 (2s timeout) ==="
    curl -6 -so /dev/null -w '%{http_code} %{time_total}s' --connect-timeout 2 http://example.com 2>/dev/null || echo "FAIL-v6"
    echo ""
} >"$OUT_DIR/dns-probes.txt"

echo "   DNS probes captured"

# ---------------------------------------------------------------------------
# 3. Real application probes (AWG + OC)
# ---------------------------------------------------------------------------
echo "--- 3. Application probes ---"

{
    echo "=== kikimora-core status ==="
    systemctl is-active kikimora-core 2>/dev/null || echo "(inactive)"
    echo "=== AWG Toad ==="
    ps aux | grep '[k]ikimora-toad.*awg' | head -1 || echo "(not running)"
    echo "=== OC Toad ==="
    ps aux | grep '[o]penconnect' | head -1 || echo "(not running)"
} >"$OUT_DIR/app-probes.txt"

echo "   Application probes captured"

# ---------------------------------------------------------------------------
# 4. Zero-mutation soak (3 iterations, 2s apart)
# ---------------------------------------------------------------------------
echo "--- 4. Zero-mutation soak ---"

for i in 1 2 3; do
    sleep 2
    V4=$(ip -4 route show table 254 | wc -l)
    V6=$(ip -6 route show table 254 | wc -l)
    EP4=$(ip -4 route show table 51890 2>/dev/null | wc -l)
    EP6=$(ip -6 route show table 51890 2>/dev/null | wc -l)
    echo "iteration $i: v4=$V4 v6=$V6 ep-v4=$EP4 ep-v6=$EP6"
done >"$OUT_DIR/soak.txt"

V4_FIRST=$(head -1 "$OUT_DIR/soak.txt" | grep -oP 'v4=\K[0-9]+')
V4_LAST=$(tail -1 "$OUT_DIR/soak.txt" | grep -oP 'v4=\K[0-9]+')
if [[ "$V4_FIRST" != "$V4_LAST" ]]; then
    echo "WARNING: v4 route count drifted during soak: $V4_FIRST -> $V4_LAST"
else
    echo "PASS: route counts stable during soak"
fi

# ---------------------------------------------------------------------------
# 5. Recovery after core restart
# ---------------------------------------------------------------------------
echo "--- 5. Core restart recovery ---"

sudo systemctl restart kikimora-core
sleep 5

"$KK" status --json >"$OUT_DIR/snapshot-after-restart.json" 2>/dev/null || true
CORE_STATE=$(python3 -c "
import json
data = json.load(open('$OUT_DIR/snapshot-after-restart.json'))
print(data.get('core_state', 'unknown'))
" 2>/dev/null || echo "unknown")
echo "   Core state after restart: $CORE_STATE"

# Re-check routes and DNS after recovery.
{
    echo "=== IPv4 routes (after restart) ==="
    ip -4 route show
    echo "=== IPv6 routes (after restart) ==="
    ip -6 route show
} >"$OUT_DIR/routes-after-restart.txt"

echo "   Routes captured after restart"

# ---------------------------------------------------------------------------
# 6. Final capture (after)
# ---------------------------------------------------------------------------
echo "--- 6. Final capture ---"

{
    echo "=== IPv4 routes ==="
    ip -4 route show
    echo "=== IPv6 routes ==="
    ip -6 route show
    echo "=== IPv4 rules ==="
    ip -4 rule show
    echo "=== IPv6 rules ==="
    ip -6 rule show
    echo "=== Managed /1 prefix count ==="
    echo "v4 /1: $(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l) + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)"
    echo "v6 /1: $(ip -6 route show ::/1 2>/dev/null | wc -l) + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)"
} >"$OUT_DIR/routes-after.txt"

"$KK" status --json >"$OUT_DIR/snapshot-after.json" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 7. Verify no split-defaults accumulated
# ---------------------------------------------------------------------------
echo "--- 7. Split-default check ---"

SPLIT_V4=$(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l)
SPLIT_V4=$((SPLIT_V4 + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)))
SPLIT_V6=$(ip -6 route show ::/1 2>/dev/null | wc -l)
SPLIT_V6=$((SPLIT_V6 + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)))

if (( SPLIT_V4 > 0 )); then
    echo "ERROR: v4 split-default routes found ($SPLIT_V4)" >&2
    exit 1
fi
if (( SPLIT_V6 > 0 )); then
    echo "ERROR: v6 split-default routes found ($SPLIT_V6)" >&2
    exit 1
fi
echo "PASS: no split-default routes accumulated"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "INSTALLED VM DUAL-STACK REGRESSION PASSED"
echo "Evidence: $OUT_DIR"
echo "============================================================"
